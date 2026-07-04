package source

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/rpc"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func testConfigObj(t *testing.T) *stconf.ConfigObj {
	t.Helper()
	configObj := stconf.FullConfig()
	configObj.Source.RequestTimeout = 3 * time.Second
	configObj.Brother.FirstSourceTimeout = 3 * time.Second
	configObj.Ygg.PemKey = ""
	configObj.Ygg.Peers.Initial = nil
	return configObj
}

func newTestObj(t *testing.T, configObj *stconf.ConfigObj) *Obj {
	t.Helper()
	obj, err := New(configObj, nil, WithAllowLoopback())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() { _ = obj.Close(context.Background()) })
	return obj
}

// //

type fakeBrotherObj struct {
	helloReply   brotherwire.HelloReplyObj
	versionReply brotherwire.VersionReplyObj
	indexReply   brotherwire.IndexReplyObj
	blobs        map[brotherwire.HashWire][]byte
	fetchFunc    func(brotherwire.BlobsFetchArgObj, *brotherwire.BlobsFetchReplyObj) error
}

func (f *fakeBrotherObj) Hello(_ brotherwire.HelloArgObj, reply *brotherwire.HelloReplyObj) error {
	*reply = f.helloReply
	return nil
}

func (f *fakeBrotherObj) Index(_ brotherwire.IndexArgObj, reply *brotherwire.IndexReplyObj) error {
	*reply = f.indexReply
	return nil
}

func (f *fakeBrotherObj) Version(_ brotherwire.VersionArgObj, reply *brotherwire.VersionReplyObj) error {
	*reply = f.versionReply
	return nil
}

func (f *fakeBrotherObj) BlobsFetch(arg brotherwire.BlobsFetchArgObj, reply *brotherwire.BlobsFetchReplyObj) error {
	if f.fetchFunc != nil {
		return f.fetchFunc(arg, reply)
	}
	for _, hw := range arg.Hashes {
		if v, ok := f.blobs[hw]; ok {
			reply.Blobs = append(reply.Blobs, brotherwire.BlobObj{Hash: hw, Value: v})
		}
	}
	return nil
}

func startFakeBrother(t *testing.T, fake *fakeBrotherObj, healthIsBrother bool) *httptest.Server {
	t.Helper()

	rpcServer := rpc.NewServer()
	if err := rpcServer.RegisterName(brotherwire.ServiceName, fake); err != nil {
		t.Fatalf("RegisterName returned error: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc(route.Health, func(w http.ResponseWriter, _ *http.Request) {
		if !healthIsBrother {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"service":"yggvault"}`)
	})
	mux.HandleFunc(brotherwire.RPCPath, func(w http.ResponseWriter, _ *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		_, _ = io.WriteString(conn, "HTTP/1.0 200 Connected to Go RPC\n\n")
		rpcServer.ServeConn(conn)
	})

	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// // // // // // // // // //

func TestBackoffCappedAndJitterless(t *testing.T) {
	r := retryObj{maxAttempts: 5, backoffInitial: 100 * time.Millisecond, backoffMax: 400 * time.Millisecond, jitterPercent: 0}
	wantArr := []time.Duration{100, 200, 400, 400, 400}
	for i, want := range wantArr {
		got := r.backoff(uint8(i + 1))
		if got != want*time.Millisecond {
			t.Fatalf("backoff(%d)=%v, want %v", i+1, got, want*time.Millisecond)
		}
	}
}

func TestDoStopsOnPermanent(t *testing.T) {
	r := retryObj{maxAttempts: 3, backoffInitial: time.Millisecond, backoffMax: time.Millisecond}
	calls := 0
	err := r.do(context.Background(), func(context.Context) error {
		calls++
		return permanent(errors.New("bad descriptor"))
	})
	if err == nil || calls != 1 {
		t.Fatalf("permanent should stop after 1 call: calls=%d err=%v", calls, err)
	}
}

func TestDoRetriesNetworkThenSucceeds(t *testing.T) {
	r := retryObj{maxAttempts: 3, backoffInitial: time.Millisecond, backoffMax: time.Millisecond}
	calls := 0
	err := r.do(context.Background(), func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("temporary network")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("expected success on 3rd attempt: calls=%d err=%v", calls, err)
	}
}

func TestDoCtxCancelStops(t *testing.T) {
	r := retryObj{maxAttempts: 5, backoffInitial: time.Hour, backoffMax: time.Hour}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := r.do(ctx, func(context.Context) error { return errors.New("net") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// //

func TestDeriveRemoteKey(t *testing.T) {
	cases := []struct {
		rootURL string
		prefix  string
		want    string
		wantErr bool
	}{
		{"http://host", "", "core-lib", false},
		{"http://host/", "", "core-lib", false},
		{"http://host/other", "", "other", false},
		{"http://host/p/bar", "p", "bar", false},
		{"http://host/a/b", "", "a", false},
		{"http://host/p", "p", "", true},
	}
	for _, c := range cases {
		got, err := deriveRemoteKey("core-lib", c.rootURL, c.prefix)
		if c.wantErr {
			if err == nil {
				t.Fatalf("deriveRemoteKey(%q,%q) expected error", c.rootURL, c.prefix)
			}
			continue
		}
		if err != nil || got != c.want {
			t.Fatalf("deriveRemoteKey(%q,%q)=%q,%v want %q", c.rootURL, c.prefix, got, err, c.want)
		}
	}
}

func TestBoundedReaderCapsDecode(t *testing.T) {
	type payloadObj struct{ Data []byte }
	var buf bytes.Buffer
	if err := gob.NewEncoder(&buf).Encode(payloadObj{Data: make([]byte, 4096)}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	lr := &limitedReaderObj{r: &buf}
	lr.reset(64)
	var out payloadObj
	if err := gob.NewDecoder(lr).Decode(&out); err == nil {
		t.Fatal("expected decode error when payload exceeds budget")
	}
}

func TestParseProviderDispatch(t *testing.T) {
	provider, err := parseProvider("https://github.com/owner/repo")
	if err != nil {
		t.Fatalf("github parse: %v", err)
	}
	if provider.Type() != "github" {
		t.Fatalf("provider type=%q, want github", provider.Type())
	}
	if _, err := parseProvider("://bad url"); err == nil {
		t.Fatal("expected error for malformed url")
	}
}

func TestPublicMirrorVersionsResolvesUniversalArchives(t *testing.T) {
	treeHash := core.HashBytes([]byte("tree"))
	mux := http.NewServeMux()
	mux.HandleFunc("/core-lib/releases.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"releases":[{"version":"v1.0.0","url":"v1.0.0.json"}]}`)
	})
	mux.HandleFunc("/core-lib/v1.0.0.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"version":"v1.0.0","hash":"`+treeHash.Hex()+`","notes_markdown":"notes","artifacts":[{"kind":"go-zip","url":"v1.0.0-go.zip"},{"kind":"tree-zip","url":"v1.0.0.zip"},{"kind":"tree-targz","url":"v1.0.0.tar.gz"}]}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	obj := newTestObj(t, testConfigObj(t))

	versionArr, truncated, err := obj.PublicMirrorVersions(context.Background(), ts.URL+"/core-lib", "core-lib")
	if err != nil {
		t.Fatalf("PublicMirrorVersions returned error: %v", err)
	}
	if truncated || len(versionArr) != 1 {
		t.Fatalf("versions=%d truncated=%v want 1,false", len(versionArr), truncated)
	}
	versionObj := versionArr[0]
	if versionObj.Version != "v1.0.0" || versionObj.ReleaseNotes != "notes" || versionObj.TreeHash != treeHash {
		t.Fatalf("unexpected version: %+v", versionObj)
	}
	if versionObj.Format != "tar.gz" {
		t.Fatalf("format=%q want tar.gz", versionObj.Format)
	}
	if wantURL := ts.URL + "/core-lib/v1.0.0.tar.gz"; versionObj.ArchiveURL != wantURL {
		t.Fatalf("archive url=%q want %q", versionObj.ArchiveURL, wantURL)
	}
}

func TestPublicMirrorVersionsRejectsEmptyCursorPage(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/core-lib/releases.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"releases":[],"next":"again"}`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	obj := newTestObj(t, testConfigObj(t))

	if _, _, err := obj.PublicMirrorVersions(context.Background(), ts.URL+"/core-lib", "core-lib"); err == nil {
		t.Fatal("expected error for an empty page with a next cursor")
	}
}
