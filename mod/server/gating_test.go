package server

import (
	"bufio"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/route"
)

// // // // // // // // // //

func TestMetricsGating(t *testing.T) {
	t.Run("disabled", func(t *testing.T) {
		serverObj, lc := newTestServer(t)
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()
		for _, pathText := range []string{route.Metrics, route.MetricsCore, route.MetricsCache, route.MetricsErrors, route.MetricsRescan, route.MetricsInternal} {
			if respObj, _ := doGET(t, tsObj, pathText, nil); respObj.StatusCode != 404 {
				t.Fatalf("disabled %s status=%d want 404", pathText, respObj.StatusCode)
			}
		}
	})

	t.Run("public enabled", func(t *testing.T) {
		serverObj, lc := newTestServer(t, withPublicMetrics())
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()

		idxResp, idxBody := doGET(t, tsObj, route.Metrics, nil)
		if idxResp.StatusCode != 200 || !strings.HasPrefix(idxResp.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("metrics index status=%d ct=%q", idxResp.StatusCode, idxResp.Header.Get("Content-Type"))
		}
		if !strings.Contains(string(idxBody), route.MetricsCore) {
			t.Fatalf("metrics index must list public core group")
		}
		if strings.Contains(string(idxBody), route.MetricsInternal) {
			t.Fatalf("metrics index must NOT list disabled internal group")
		}

		for _, pathText := range []string{route.MetricsCore, route.MetricsCache, route.MetricsErrors, route.MetricsRescan} {
			resp, body := doGET(t, tsObj, pathText, nil)
			if resp.StatusCode != 200 {
				t.Fatalf("public %s status=%d want 200", pathText, resp.StatusCode)
			}
			if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
				t.Fatalf("group %s content-type=%q want json", pathText, ct)
			}
			if !strings.HasPrefix(strings.TrimSpace(string(body)), "{") {
				t.Fatalf("group %s body is not a JSON object:\n%s", pathText, body)
			}
		}

		if intResp, _ := doGET(t, tsObj, route.MetricsInternal, nil); intResp.StatusCode != 404 {
			t.Fatalf("internal status=%d want 404 (public flag must not open it)", intResp.StatusCode)
		}
	})

	t.Run("internal enabled", func(t *testing.T) {
		serverObj, lc := newTestServer(t, withInternalMetrics())
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()

		intResp, _ := doGET(t, tsObj, route.MetricsInternal, nil)
		if intResp.StatusCode != 200 {
			t.Fatalf("internal status=%d want 200", intResp.StatusCode)
		}
		if ct := intResp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/plain") {
			t.Fatalf("internal content-type=%q", ct)
		}
		if coreResp, _ := doGET(t, tsObj, route.MetricsCore, nil); coreResp.StatusCode != 404 {
			t.Fatalf("core status=%d want 404 (internal flag must not open public)", coreResp.StatusCode)
		}
	})
}

// // // // // // // // // //

func TestMetricsYggGating(t *testing.T) {
	t.Run("mesh disabled returns 404 and hides the group", func(t *testing.T) {
		serverObj, lc := newTestServer(t, withPublicMetrics())
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()
		if respObj, _ := doGET(t, tsObj, route.MetricsYgg, nil); respObj.StatusCode != 404 {
			t.Fatalf("ygg group with disabled mesh status=%d want 404", respObj.StatusCode)
		}
		_, idxBody := doGET(t, tsObj, route.Metrics, nil)
		if strings.Contains(string(idxBody), route.MetricsYgg) {
			t.Fatal("metrics index must NOT list the ygg group while mesh is disabled")
		}
	})

	t.Run("public metrics serve aggregates without the peer list", func(t *testing.T) {
		serverObj, lc := newTestServer(t, withPublicMetrics())
		serverObj.funcImplObj.deps.Mesh = enabledFakeMeshObj{}
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()

		respObj, body := doGET(t, tsObj, route.MetricsYgg, nil)
		if respObj.StatusCode != 200 {
			t.Fatalf("ygg group with running mesh status=%d want 200", respObj.StatusCode)
		}
		if ct := respObj.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("ygg group content-type=%q want json", ct)
		}
		if !strings.Contains(string(body), `"peers_known":`) {
			t.Fatalf("ygg group body missing aggregate fields:\n%s", body)
		}
		for _, secretText := range []string{`"peers":[`, `"public_key"`, `"uri"`} {
			if strings.Contains(string(body), secretText) {
				t.Fatalf("public ygg group must not expose the peer list (%s):\n%s", secretText, body)
			}
		}
		_, idxBody := doGET(t, tsObj, route.Metrics, nil)
		if !strings.Contains(string(idxBody), route.MetricsYgg) {
			t.Fatal("metrics index must list the ygg group when mesh runs")
		}
	})

	t.Run("internal metrics add the peer list", func(t *testing.T) {
		serverObj, lc := newTestServer(t, withPublicMetrics(), withInternalMetrics())
		serverObj.funcImplObj.deps.Mesh = enabledFakeMeshObj{}
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()

		respObj, body := doGET(t, tsObj, route.MetricsYgg, nil)
		if respObj.StatusCode != 200 {
			t.Fatalf("ygg group status=%d want 200", respObj.StatusCode)
		}
		for _, wantText := range []string{`"peers":[`, `"public_key":"aabb"`, `"uri":"tls://peer.example:443"`} {
			if !strings.Contains(string(body), wantText) {
				t.Fatalf("internal ygg group body missing %s:\n%s", wantText, body)
			}
		}
	})

	t.Run("public metrics disabled keeps ygg 404 even with a running mesh", func(t *testing.T) {
		serverObj, lc := newTestServer(t)
		serverObj.funcImplObj.deps.Mesh = enabledFakeMeshObj{}
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()
		if respObj, _ := doGET(t, tsObj, route.MetricsYgg, nil); respObj.StatusCode != 404 {
			t.Fatalf("ygg group without public metrics status=%d want 404", respObj.StatusCode)
		}
	})

	t.Run("ygg listener honors its own metric flags", func(t *testing.T) {
		serverObj, webLc := newTestServer(t, withYggPublicMetrics())
		serverObj.funcImplObj.deps.Mesh = enabledFakeMeshObj{}

		yggTS := httptest.NewServer(serverObj.Handler(serverObj.yggListenerCtx()))
		defer yggTS.Close()
		if respObj, _ := doGET(t, yggTS, route.MetricsYgg, nil); respObj.StatusCode != 200 {
			t.Fatalf("ygg group on ygg listener status=%d want 200", respObj.StatusCode)
		}

		webTS := httptest.NewServer(serverObj.Handler(webLc))
		defer webTS.Close()
		if respObj, _ := doGET(t, webTS, route.MetricsYgg, nil); respObj.StatusCode != 404 {
			t.Fatalf("metrics.ygg.public must not open the web listener, status=%d want 404", respObj.StatusCode)
		}
	})
}

// // // // // // // // // //

func TestRateLimit(t *testing.T) {
	serverObj, lc := newTestServer(t, withRateLimit(1, 1))
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	hammered := func(pathText string) bool {
		for i := 0; i < 50; i++ {
			respObj, _ := doGET(t, tsObj, pathText, nil)
			if respObj.StatusCode == http.StatusTooManyRequests {
				return true
			}
		}
		return false
	}

	if !hammered("/catalog.json") {
		t.Fatal("data op never rate-limited under burst=1")
	}
	if !hammered("/") {
		t.Fatal("HTML page never rate-limited (M1 fix): static/web must be limited")
	}
	for i := 0; i < 20; i++ {
		respObj, _ := doGET(t, tsObj, route.Health, nil)
		if respObj.StatusCode == http.StatusTooManyRequests {
			t.Fatal("health must not be rate-limited")
		}
	}
}

// // // // // // // // // //

func rpcConnectStatus(t *testing.T, tsObj *httptest.Server) int {
	t.Helper()
	connObj, err := net.Dial("tcp", tsObj.Listener.Addr().String())
	if err != nil {
		t.Fatalf("dial test server: %v", err)
	}
	defer func() { _ = connObj.Close() }()
	if _, err := connObj.Write([]byte("CONNECT " + brotherwire.RPCPath + " HTTP/1.0\r\nHost: mirror.example\r\n\r\n")); err != nil {
		t.Fatalf("write CONNECT: %v", err)
	}
	respObj, err := http.ReadResponse(bufio.NewReader(connObj), &http.Request{Method: http.MethodConnect})
	if err != nil {
		t.Fatalf("read CONNECT response: %v", err)
	}
	return respObj.StatusCode
}

func TestBrotherRPCGateByListenerConfig(t *testing.T) {
	serverObj, lc := newTestServer(t)

	t.Run("web enabled by default", func(t *testing.T) {
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()
		if respObj, _ := doGET(t, tsObj, brotherwire.RPCPath, nil); respObj.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("GET /rpc on web status=%d want 405", respObj.StatusCode)
		}
		if status := rpcConnectStatus(t, tsObj); status != http.StatusOK {
			t.Fatalf("CONNECT /rpc on web status=%d want 200", status)
		}
	})

	t.Run("web disabled", func(t *testing.T) {
		serverObj.cfg.Brother.Rpc.WebEnabled = false
		t.Cleanup(func() { serverObj.cfg.Brother.Rpc.WebEnabled = true })
		tsObj := httptest.NewServer(serverObj.Handler(lc))
		defer tsObj.Close()
		if respObj, _ := doGET(t, tsObj, brotherwire.RPCPath, nil); respObj.StatusCode != http.StatusNotFound {
			t.Fatalf("GET /rpc on disabled web status=%d want 404", respObj.StatusCode)
		}
		if status := rpcConnectStatus(t, tsObj); status != http.StatusNotFound {
			t.Fatalf("CONNECT /rpc on disabled web status=%d want 404", status)
		}
	})

	t.Run("ygg enabled by default", func(t *testing.T) {
		tsObj := httptest.NewServer(serverObj.Handler(serverObj.yggListenerCtx()))
		defer tsObj.Close()
		if status := rpcConnectStatus(t, tsObj); status != http.StatusOK {
			t.Fatalf("CONNECT /rpc on ygg status=%d want 200", status)
		}
	})

	t.Run("ygg disabled", func(t *testing.T) {
		serverObj.cfg.Brother.Rpc.YggEnabled = false
		t.Cleanup(func() { serverObj.cfg.Brother.Rpc.YggEnabled = true })
		tsObj := httptest.NewServer(serverObj.Handler(serverObj.yggListenerCtx()))
		defer tsObj.Close()
		if status := rpcConnectStatus(t, tsObj); status != http.StatusNotFound {
			t.Fatalf("CONNECT /rpc on disabled ygg status=%d want 404", status)
		}
	})
}
