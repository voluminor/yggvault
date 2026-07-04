package server

import (
	"bytes"
	"net/http/httptest"
	"testing"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func TestWebListenerScheme(t *testing.T) {
	serverObj, _ := newTestServer(t)

	testList := []struct {
		name       string
		mode       stconf.WebServerModeEnum
		protoHTTPS bool
		want       string
	}{
		{name: "shared advertises https", mode: stconf.WebServerModeShared, protoHTTPS: false, want: "https"},
		{name: "split http advertises http", mode: stconf.WebServerModeSplit, protoHTTPS: false, want: "http"},
		{name: "split https advertises https", mode: stconf.WebServerModeSplit, protoHTTPS: true, want: "https"},
		{name: "single http advertises http", mode: stconf.WebServerModeSingle, protoHTTPS: false, want: "http"},
		{name: "single https advertises https", mode: stconf.WebServerModeSingle, protoHTTPS: true, want: "https"},
	}
	for _, tc := range testList {
		t.Run(tc.name, func(t *testing.T) {
			serverObj.cfg.Web.Server.Mode = tc.mode
			lc := serverObj.webListenerCtx(tc.protoHTTPS)
			if got := lc.scheme(); got != tc.want {
				t.Fatalf("scheme()=%q, want %q", got, tc.want)
			}
		})
	}
}

func TestVersionPageEtagVariesByScheme(t *testing.T) {
	serverObj, _ := newTestServer(t)
	serverObj.cfg.Web.Server.Mode = stconf.WebServerModeSplit

	lcHTTP := serverObj.webListenerCtx(false)
	lcHTTPS := serverObj.webListenerCtx(true)
	if lcHTTP.scheme() != "http" || lcHTTPS.scheme() != "https" {
		t.Fatalf("listener setup wrong: http=%q https=%q", lcHTTP.scheme(), lcHTTPS.scheme())
	}

	tsHTTP := httptest.NewServer(serverObj.Handler(lcHTTP))
	defer tsHTTP.Close()
	tsHTTPS := httptest.NewServer(serverObj.Handler(lcHTTPS))
	defer tsHTTPS.Close()

	respHTTP, bodyHTTP := doGET(t, tsHTTP, "/lib/v1.0.0", nil)
	respHTTPS, bodyHTTPS := doGET(t, tsHTTPS, "/lib/v1.0.0", nil)
	if respHTTP.StatusCode != 200 || respHTTPS.StatusCode != 200 {
		t.Fatalf("version page status: http=%d https=%d", respHTTP.StatusCode, respHTTPS.StatusCode)
	}
	etagHTTP := respHTTP.Header.Get("Etag")
	etagHTTPS := respHTTPS.Header.Get("Etag")
	if etagHTTP == "" || etagHTTPS == "" {
		t.Fatalf("empty version-page etag: http=%q https=%q", etagHTTP, etagHTTPS)
	}
	if etagHTTP == etagHTTPS {
		t.Fatalf("version-page ETag must differ by scheme, got %q for both", etagHTTP)
	}
	if bytes.Equal(bodyHTTP, bodyHTTPS) {
		t.Fatalf("version-page bodies identical across schemes")
	}
}
