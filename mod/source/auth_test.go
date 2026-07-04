package source

import (
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type recordTransportObj struct {
	gotHeader http.Header
}

func (rt *recordTransportObj) RoundTrip(req *http.Request) (*http.Response, error) {
	rt.gotHeader = req.Header.Clone()
	return &http.Response{
		StatusCode: 200,
		Body:       io.NopCloser(strings.NewReader("")),
		Header:     make(http.Header),
		Request:    req,
	}, nil
}

func runAuth(t *testing.T, matcher *credentialMatcherObj, url string, meshHosts ...string) (http.Header, *http.Request) {
	t.Helper()
	rec := &recordTransportObj{}
	isMesh := func(host string) bool {
		for _, h := range meshHosts {
			if h == host {
				return true
			}
		}
		return false
	}
	tr := &authTransportObj{base: rec, matcher: matcher, isMesh: isMesh}
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if _, err := tr.RoundTrip(req); err != nil {
		t.Fatalf("roundtrip: %v", err)
	}
	return rec.gotHeader, req
}

// // // // // // // // // //

func TestCredentialMatcherPredefined(t *testing.T) {
	matcher, err := buildCredentialMatcher(stconf.SourceCredentialsObj{
		Github:    "ghp_x",
		Gitlab:    "glpat_y",
		Bitbucket: "bb_z",
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	cases := []struct {
		url       string
		wantName  string
		wantValue string
	}{
		{"https://api.github.com/repos/o/r/releases", "Authorization", "Bearer ghp_x"},
		{"https://github.com/o/r", "Authorization", "Bearer ghp_x"},
		{"https://codeload.github.com/o/r/zip", "Authorization", "Bearer ghp_x"},
		{"https://gitlab.com/api/v4/x", "PRIVATE-TOKEN", "glpat_y"},
		{"https://api.bitbucket.org/2.0/x", "Authorization", "Bearer bb_z"},
	}
	for _, c := range cases {
		got, orig := runAuth(t, matcher, c.url)
		if v := got.Get(c.wantName); v != c.wantValue {
			t.Errorf("%s: header %s = %q, want %q", c.url, c.wantName, v, c.wantValue)
		}
		if orig.Header.Get(c.wantName) != "" {
			t.Errorf("%s: original request was mutated (must clone)", c.url)
		}
	}
}

func TestCredentialMatcherNoLeakToNonMatchOrMesh(t *testing.T) {
	matcher, err := buildCredentialMatcher(stconf.SourceCredentialsObj{
		Github: "ghp_secret",
		Others: map[string]string{"abc.pk.ygg": "Authorization: Bearer leak"},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got, _ := runAuth(t, matcher, "https://example.org/x"); got.Get("Authorization") != "" {
		t.Errorf("token leaked to unrelated host example.org")
	}
	if got, _ := runAuth(t, matcher, "https://notgithub.com/x"); got.Get("Authorization") != "" {
		t.Errorf("token leaked to lookalike host notgithub.com")
	}
	got, _ := runAuth(t, matcher, "http://abc.pk.ygg/rpc", "abc.pk.ygg")
	if got.Get("Authorization") != "" {
		t.Errorf("token leaked to mesh host abc.pk.ygg")
	}
}

func TestCredentialMatcherOthersAndPrecedence(t *testing.T) {
	matcher, err := buildCredentialMatcher(stconf.SourceCredentialsObj{
		Github: "ghp_predef",
		Others: map[string]string{
			"git.corp.example": "Authorization: token gitea_tok",
			"github.com":       "PRIVATE-TOKEN: override",
		},
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if got, _ := runAuth(t, matcher, "https://git.corp.example/api/v1/x"); got.Get("Authorization") != "token gitea_tok" {
		t.Errorf("gitea others header = %q", got.Get("Authorization"))
	}
	got, _ := runAuth(t, matcher, "https://github.com/o/r")
	if got.Get("PRIVATE-TOKEN") != "override" || got.Get("Authorization") != "" {
		t.Errorf("others did not override predefined for github.com: PRIVATE-TOKEN=%q Authorization=%q",
			got.Get("PRIVATE-TOKEN"), got.Get("Authorization"))
	}
}

func TestCredentialMatcherEmptyIsAnonymous(t *testing.T) {
	matcher, err := buildCredentialMatcher(stconf.SourceCredentialsObj{})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !matcher.empty() {
		t.Fatalf("empty credentials must yield empty matcher")
	}
	if got, _ := runAuth(t, matcher, "https://api.github.com/x"); got.Get("Authorization") != "" {
		t.Errorf("anonymous config must not set any header")
	}
}

func TestCredentialMatcherMalformedOthers(t *testing.T) {
	_, err := buildCredentialMatcher(stconf.SourceCredentialsObj{
		Others: map[string]string{"git.corp.example": "no-colon-here"},
	})
	if err == nil {
		t.Fatalf("malformed others (no colon) must error")
	}
	if strings.Contains(err.Error(), "no-colon-here") {
		t.Errorf("error message must not echo the header/secret value: %v", err)
	}
}
