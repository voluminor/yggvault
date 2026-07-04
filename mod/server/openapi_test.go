package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

func TestOpenAPIJSON(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, bodyArr := doGET(t, tsObj, route.OpenAPI, nil)
	if respObj.StatusCode != 200 {
		t.Fatalf("openapi status=%d", respObj.StatusCode)
	}
	if ct := respObj.Header.Get("Content-Type"); ct != cOpenAPIContentType {
		t.Fatalf("openapi content-type=%q", ct)
	}
	etag := respObj.Header.Get("ETag")
	if etag == "" {
		t.Fatalf("openapi missing ETag")
	}
	var specObj map[string]any
	if json.Unmarshal(bodyArr, &specObj) != nil || specObj["openapi"] == nil || specObj["paths"] == nil {
		t.Fatalf("openapi body is not a spec (len=%d)", len(bodyArr))
	}

	respCond, bodyCond := doGET(t, tsObj, route.OpenAPI, map[string]string{"If-None-Match": etag})
	if respCond.StatusCode != http.StatusNotModified || len(bodyCond) != 0 {
		t.Fatalf("openapi conditional status=%d bodyLen=%d", respCond.StatusCode, len(bodyCond))
	}

	respHead, bodyHead := doReq(t, tsObj, http.MethodHead, route.OpenAPI, nil)
	if respHead.StatusCode != 200 || len(bodyHead) != 0 {
		t.Fatalf("openapi HEAD status=%d bodyLen=%d", respHead.StatusCode, len(bodyHead))
	}
	if respHead.Header.Get("Content-Length") == "" {
		t.Fatalf("openapi HEAD missing Content-Length")
	}
}

// // // // // // // // // //

func TestNestedWellKnownStayRoot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>root</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	serverObj, lc := newTestServer(t, withStaticDir(dir))
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	t.Run("info at root", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, route.Info, nil)
		var infoObj api.InfoObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &infoObj) != nil || infoObj.Domain != "mirror.example" {
			t.Fatalf("nested %s status=%d body=%s", route.Info, respObj.StatusCode, bodyArr)
		}
	})

	t.Run("health at root", func(t *testing.T) {
		if respObj, _ := doGET(t, tsObj, route.Health, nil); respObj.StatusCode != 200 {
			t.Fatalf("nested %s status=%d", route.Health, respObj.StatusCode)
		}
	})

	t.Run("openapi at root", func(t *testing.T) {
		if respObj, _ := doGET(t, tsObj, route.OpenAPI, nil); respObj.StatusCode != 200 {
			t.Fatalf("nested %s status=%d", route.OpenAPI, respObj.StatusCode)
		}
	})

	t.Run("api under prefix", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/pkg/catalog.json", nil)
		if respObj.StatusCode != 200 || !strings.Contains(string(bodyArr), `"lib"`) {
			t.Fatalf("nested /pkg/catalog.json status=%d body=%s", respObj.StatusCode, bodyArr)
		}
	})
}
