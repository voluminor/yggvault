package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func optNoneMatch(value string) api.OptString { return api.NewOptString(value) }

// // // // // // // // // //

func TestEtagOf(t *testing.T) {
	first := etagOf("a", "b", "c")
	if first != etagOf("a", "b", "c") {
		t.Fatal("etagOf not deterministic for identical parts")
	}
	if first == etagOf("a", "b", "d") {
		t.Fatal("etagOf collides for distinct parts")
	}
	if etagOf("ab", "c") == etagOf("a", "bc") {
		t.Fatal("etagOf ignores part boundaries")
	}
	if etagOf() != "" {
		t.Fatalf("etagOf() with no parts = %q want empty", etagOf())
	}
	if !strings.HasPrefix(first, `"`) || !strings.HasSuffix(first, `"`) {
		t.Fatalf("etag not quoted: %q", first)
	}
	// Validators must rotate on deploy because etagOf mixes in target.Version+Hash.
	partsOnly := `"` + core.HashBytes([]byte("a\x00b\x00c")).Hex() + `"`
	if first == partsOnly {
		t.Fatal("etagOf hashes only its parts: deploy would not rotate validators")
	}
	withBuild := `"` + core.HashBytes([]byte(target.Version+"\x00"+target.Hash+"\x00a\x00b\x00c")).Hex() + `"`
	if first != withBuild {
		t.Fatalf("etagOf does not mix in build identity: got %s want %s", first, withBuild)
	}
}

// // // // // // // // // //

func TestCondMatch(t *testing.T) {
	etag := `"abc"`
	cases := []struct {
		name        string
		ifNoneMatch api.OptString
		etag        string
		want        bool
	}{
		{"star", optNoneMatch("*"), etag, true},
		{"exact in list", optNoneMatch(`"x", "abc", "y"`), etag, true},
		{"exact single", optNoneMatch(etag), etag, true},
		{"weak validator matches", optNoneMatch(`W/"abc"`), etag, true},
		{"weak in list", optNoneMatch(`"x", W/"abc"`), etag, true},
		{"weak no match", optNoneMatch(`W/"nope"`), etag, false},
		{"no match", optNoneMatch(`"nope"`), etag, false},
		{"unset", api.OptString{}, etag, false},
		{"empty header", optNoneMatch(""), etag, false},
		{"empty etag", optNoneMatch("*"), "", false},
	}
	for _, c := range cases {
		if got := condMatch(c.ifNoneMatch, c.etag); got != c.want {
			t.Fatalf("%s: condMatch=%v want %v", c.name, got, c.want)
		}
	}
}

// // // // // // // // // //

func TestLooksLikeGoProxy(t *testing.T) {
	cases := map[string]bool{
		"/lib/@v/v1.0.0.info": true,
		"/lib/@v/list":        true,
		"/lib/@latest":        true,
		"/lib/releases.json":  false,
		"/catalog.json":       false,
		"/":                   false,
	}
	for path, want := range cases {
		if got := looksLikeGoProxy(path); got != want {
			t.Fatalf("looksLikeGoProxy(%q)=%v want %v", path, got, want)
		}
	}
}

// // // // // // // // // //

func TestIsAllDigits(t *testing.T) {
	cases := map[string]bool{"2": true, "10": true, "": false, "v2": false, "1a": false, " 1": false}
	for text, want := range cases {
		if got := isAllDigits(text); got != want {
			t.Fatalf("isAllDigits(%q)=%v want %v", text, got, want)
		}
	}
}

// // // // // // // // // //

func TestStripEntryHost(t *testing.T) {
	const host = "mirror.example"
	cases := []struct {
		name        string
		path        string
		entryHost   string
		wantPath    string
		wantChanged bool
	}{
		{"host prefix peeled", "/" + host + "/lib/@v/list", host, "/lib/@v/list", true},
		{"host + major segment kept for stripMajor", "/" + host + "/lib/v2/@v/list", host, "/lib/v2/@v/list", true},
		{"non go-proxy passthrough", "/" + host + "/catalog.json", host, "/" + host + "/catalog.json", false},
		{"no host prefix", "/lib/@v/list", host, "/lib/@v/list", false},
		{"empty entry host", "/" + host + "/lib/@v/list", "", "/" + host + "/lib/@v/list", false},
		// go probes module-path prefixes; the "module = host" probe must stay untouched
		// and return 404 for an unknown key, not 400 for an empty key.
		{"host-as-module probe kept", "/" + host + "/@v/v2.44.0.info", host, "/" + host + "/@v/v2.44.0.info", false},
		{"host-as-module latest probe kept", "/" + host + "/@latest", host, "/" + host + "/@latest", false},
	}
	for _, c := range cases {
		gotPath, gotChanged := stripEntryHost(c.path, c.entryHost)
		if gotPath != c.wantPath || gotChanged != c.wantChanged {
			t.Fatalf("%s: stripEntryHost(%q,%q)=(%q,%v) want (%q,%v)",
				c.name, c.path, c.entryHost, gotPath, gotChanged, c.wantPath, c.wantChanged)
		}
	}
}

// // // // // // // // // //

// Major grammar: vN sits between the key and `/@v/` or `/@latest`.
func TestStripMajor(t *testing.T) {
	cases := []struct {
		name      string
		path      string
		wantPath  string
		wantMajor string
	}{
		{"v2 before @v", "/lib/v2/@v/list", "/lib/@v/list", "v2"},
		{"v10 before version file", "/lib/v10/@v/v10.1.0.info", "/lib/@v/v10.1.0.info", "v10"},
		{"v2 before @latest", "/lib/v2/@latest", "/lib/@latest", "v2"},
		{"no major segment", "/lib/@v/list", "/lib/@v/list", ""},
		{"v1 stripped but strict (majorBucket never matches)", "/lib/v1/@v/list", "/lib/@v/list", "v1"},
		{"v0 stripped but strict", "/lib/v0/@latest", "/lib/@latest", "v0"},
		{"nested prefix keeps composing", "/pkg/lib/v2/@v/list", "/pkg/lib/@v/list", "v2"},
		{"bare vN is a key, not a major", "/v2/@v/list", "/v2/@v/list", ""},
		{"bare vN latest", "/v2/@latest", "/v2/@latest", ""},
		{"old host-first form dead", "/v2/lib/@v/list", "/v2/lib/@v/list", ""},
		{"v without digits", "/lib/v/@latest", "/lib/v/@latest", ""},
		{"non-numeric tail", "/lib/v2x/@v/list", "/lib/v2x/@v/list", ""},
		{"non go-proxy html version page", "/lib/v2.44.0", "/lib/v2.44.0", ""},
		{"non go-proxy artifact", "/lib/v2.44.0.zip", "/lib/v2.44.0.zip", ""},
		{"root @latest", "/@latest", "/@latest", ""},
		{"root @v", "/@v/list", "/@v/list", ""},
	}
	for _, c := range cases {
		gotPath, gotMajor := stripMajor(c.path)
		if gotPath != c.wantPath || gotMajor != c.wantMajor {
			t.Fatalf("%s: stripMajor(%q)=(%q,%q) want (%q,%q)",
				c.name, c.path, gotPath, gotMajor, c.wantPath, c.wantMajor)
		}
	}
}

// // // // // // // // // //

func nestedServer(t *testing.T) *ServerObj {
	t.Helper()
	cfgObj := stconf.FullConfig()
	cfgObj.Web.Static.Dir = t.TempDir()
	cfgObj.Web.Routing.Prefix = "pkg"
	return &ServerObj{cfg: cfgObj, funcImplObj: &funcObj{deps: DepsObj{Config: cfgObj}}}
}

func TestSplitNested(t *testing.T) {
	serverObj := nestedServer(t)
	cases := []struct {
		name    string
		path    string
		wantOut string
		wantSvc bool
	}{
		{"health stays service", route.Health, route.Health, true},
		{"info stays service", route.Info, route.Info, true},
		{"metrics index service", route.Metrics, route.Metrics, true},
		{"metrics internal service", route.MetricsInternal, route.MetricsInternal, true},
		{"openapi stays service", route.OpenAPI, route.OpenAPI, true},
		{"prefix root → /", "/pkg", "/", true},
		{"prefix child stripped", "/pkg/lib/releases.json", "/lib/releases.json", true},
		{"non-prefix is static", "/assets/app.css", "/assets/app.css", false},
		{"prefix lookalike is static", "/pkgx/foo", "/pkgx/foo", false},
	}
	for _, c := range cases {
		gotOut, gotSvc := serverObj.splitNested(c.path)
		if gotOut != c.wantOut || gotSvc != c.wantSvc {
			t.Fatalf("%s: splitNested(%q)=(%q,%v) want (%q,%v)", c.name, c.path, gotOut, gotSvc, c.wantOut, c.wantSvc)
		}
	}
}

// // // // // // // // // //

func TestShallowClone(t *testing.T) {
	origObj, err := http.NewRequest(http.MethodGet, "http://mirror.example/lib/@v/list", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	origObj.Header.Set("If-None-Match", `"abc"`)
	origPath := origObj.URL.Path

	cloneObj := shallowClone(origObj)
	if cloneObj.Header.Get("If-None-Match") != `"abc"` {
		t.Fatal("clone lost Header")
	}
	cloneObj.URL.Path = "/rewritten"
	if origObj.URL.Path != origPath {
		t.Fatalf("clone URL not independent: orig path mutated to %q", origObj.URL.Path)
	}
	if cloneObj.URL == origObj.URL {
		t.Fatal("clone shares URL pointer with original")
	}
}
