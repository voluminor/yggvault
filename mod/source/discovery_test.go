package source

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func startProbeServer(t *testing.T, healthFn http.HandlerFunc, infoFn http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc(route.Health, healthFn)
	mux.HandleFunc(route.Info, infoFn)
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func status(code int) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(code) }
}

func body(text string) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, text)
	}
}

// // // // // // // // // //

// Classification matrix: git is confirmed only by two definitive denials;
// unreliable and contradictory answers produce an error without classification.
func TestDiscoverClassificationMatrix(t *testing.T) {
	cases := []struct {
		name     string
		healthFn http.HandlerFunc
		infoFn   http.HandlerFunc
		wantGit  bool
		wantErr  bool
	}{
		{"health 5xx defers", status(http.StatusInternalServerError), status(http.StatusNotFound), false, true},
		{"health 429 defers", status(http.StatusTooManyRequests), status(http.StatusNotFound), false, true},
		{"both definitive miss is git", status(http.StatusNotFound), status(http.StatusNotFound), true, false},
		{"health junk body plus info miss is git", body(`<html>ok</html>`), status(http.StatusNotFound), true, false},
		{"foreign service body plus info miss is git", body(`{"service":"gitea"}`), status(http.StatusNotFound), true, false},
		{"health miss but info card defers", status(http.StatusNotFound), body(`{"name":"node-x","domain":"vault.test"}`), false, true},
		{"health miss and info 5xx defers", status(http.StatusNotFound), status(http.StatusBadGateway), false, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			ts := startProbeServer(t, c.healthFn, c.infoFn)
			obj := newTestObj(t, testConfigObj(t))

			res, err := obj.Discover(context.Background(), "core-lib", ts.URL)
			if c.wantErr {
				if err == nil {
					t.Fatalf("Discover returned nil error, class=%v", res.Class)
				}
				return
			}
			if err != nil {
				t.Fatalf("Discover returned error: %v", err)
			}
			if c.wantGit && res.Class != stcode.SourceClassGit {
				t.Fatalf("Discover class=%v want git", res.Class)
			}
		})
	}
}

// An unreachable host (connection refused) is not classified as git.
func TestDiscoverConnectionRefusedDefers(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	ts.Close()
	obj := newTestObj(t, testConfigObj(t))

	_, err := obj.Discover(context.Background(), "core-lib", ts.URL)
	if err == nil {
		t.Fatal("Discover returned nil error for a dead host")
	}
}

func TestDiscoverClassifiesBrotherWhenRPCMissing(t *testing.T) {
	ts := startProbeServer(t, body(`{"service":"yggvault"}`), vaultInfoWithHost(``))
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL+"/core-lib")
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if res.Class != stcode.SourceClassBrother {
		t.Fatalf("class=%v want brother", res.Class)
	}
	if res.RemoteKey != "core-lib" || res.BrotherURL != ts.URL+"/core-lib" {
		t.Fatalf("unexpected discovery result: %+v", res)
	}
}

func vaultInfoWithHost(extra string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		hostText := r.Host
		if host, _, err := net.SplitHostPort(r.Host); err == nil {
			hostText = host
		}
		_, _ = io.WriteString(w, `{"name":"node-a","domain":"`+hostText+`"`+extra+`}`)
	}
}

// A brother that advertises route_prefix is parsed under the remote prefix, not the local default.
// The advertised prefix differs from the local "pkg" so the key is derived correctly only when the
// remote value is honoured.
func TestDiscoverUsesRemoteRoutePrefix(t *testing.T) {
	ts := startProbeServer(t, body(`{"service":"yggvault"}`), vaultInfoWithHost(`,"route_prefix":"mods"`))
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL+"/mods/core-lib")
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if res.Class != stcode.SourceClassBrother {
		t.Fatalf("class=%v want brother", res.Class)
	}
	if res.RemoteKey != "core-lib" {
		t.Fatalf("RemoteKey=%q want core-lib (remote route_prefix not honoured)", res.RemoteKey)
	}
}

// A brother that omits route_prefix (older node) falls back to the local prefix so existing nested
// deployments keep resolving keys as before.
func TestDiscoverFallsBackToLocalPrefixWithoutRoutePrefix(t *testing.T) {
	ts := startProbeServer(t, body(`{"service":"yggvault"}`), vaultInfoWithHost(``))
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL+"/pkg/core-lib")
	if err != nil {
		t.Fatalf("Discover returned error: %v", err)
	}
	if res.RemoteKey != "core-lib" {
		t.Fatalf("RemoteKey=%q want core-lib (local prefix fallback failed)", res.RemoteKey)
	}
}
