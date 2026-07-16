package server

import (
	"encoding/json"
	"encoding/xml"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/mod/server/webui"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

func TestAllOpsSmoke(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	t.Run("health", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, route.Health, nil)
		var healthObj api.HealthObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &healthObj) != nil {
			t.Fatalf("health status=%d body=%s", respObj.StatusCode, bodyArr)
		}
		if healthObj.Source != api.HealthObjSourceWeb || healthObj.Status != api.HealthObjStatusOk {
			t.Fatalf("health source=%v status=%v", healthObj.Source, healthObj.Status)
		}
	})

	t.Run("info", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, route.Info, nil)
		var infoObj api.InfoObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &infoObj) != nil || infoObj.Domain != "mirror.example" {
			t.Fatalf("info status=%d body=%s", respObj.StatusCode, bodyArr)
		}
	})

	t.Run("catalog", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/catalog.json", nil)
		if respObj.StatusCode != 200 || !strings.Contains(string(bodyArr), `"lib"`) {
			t.Fatalf("catalog status=%d body=%s", respObj.StatusCode, bodyArr)
		}
	})

	t.Run("releases", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/lib/releases.json", nil)
		var listObj api.ReleaseListObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &listObj) != nil {
			t.Fatalf("releases status=%d body=%s", respObj.StatusCode, bodyArr)
		}
		if len(listObj.Releases) != 1 || listObj.Releases[0].Version != "v1.0.0" {
			t.Fatalf("releases content: %+v", listObj.Releases)
		}
	})

	t.Run("detail json", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/lib/v1.0.0.json", nil)
		var detailObj api.ReleaseDetailObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &detailObj) != nil {
			t.Fatalf("detail status=%d body=%s", respObj.StatusCode, bodyArr)
		}
		if detailObj.Version != "v1.0.0" {
			t.Fatalf("detail version=%q", detailObj.Version)
		}
	})

	t.Run("artifact zip", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/lib/v1.0.0.zip", nil)
		if respObj.StatusCode != 200 || string(bodyArr) != cArtifactBytes {
			t.Fatalf("zip status=%d body=%q", respObj.StatusCode, bodyArr)
		}
		if ct := respObj.Header.Get("Content-Type"); ct != "application/octet-stream" {
			t.Fatalf("zip content-type=%q", ct)
		}
		if respObj.Header.Get("Etag") != cArtifactETag {
			t.Fatalf("zip etag=%q want %q", respObj.Header.Get("Etag"), cArtifactETag)
		}
	})

	t.Run("mirror", func(t *testing.T) {
		for _, c := range []struct {
			path string
			want string
		}{
			{"/lib/latest", "v1.0.0"},
			{"/lib/list", "v1.0.0"},
		} {
			respObj, bodyArr := doGET(t, tsObj, c.path, nil)
			if respObj.StatusCode != 200 || strings.TrimSpace(string(bodyArr)) != c.want {
				t.Fatalf("%s status=%d body=%q", c.path, respObj.StatusCode, bodyArr)
			}
		}
		fullResp, fullBody := doGET(t, tsObj, "/lib/list/full", nil)
		if fullResp.StatusCode != 200 || !strings.Contains(string(fullBody), "v1.0.0") {
			t.Fatalf("list/full status=%d body=%s", fullResp.StatusCode, fullBody)
		}
	})

	t.Run("go proxy", func(t *testing.T) {
		latestResp, latestBody := doGET(t, tsObj, "/lib/@latest", nil)
		var infoObj api.GoInfoObj
		if latestResp.StatusCode != 200 || json.Unmarshal(latestBody, &infoObj) != nil || infoObj.Version != "v1.0.0" {
			t.Fatalf("@latest status=%d body=%s", latestResp.StatusCode, latestBody)
		}
		listResp, listBody := doGET(t, tsObj, "/lib/@v/list", nil)
		if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
			t.Fatalf("@v/list status=%d body=%q", listResp.StatusCode, listBody)
		}
		viResp, viBody := doGET(t, tsObj, "/lib/@v/v1.0.0.info", nil)
		if viResp.StatusCode != 200 || !strings.Contains(string(viBody), "v1.0.0") {
			t.Fatalf("@v/.info status=%d body=%q", viResp.StatusCode, viBody)
		}
		modResp, modBody := doGET(t, tsObj, "/lib/@v/v1.0.0.mod", nil)
		if modResp.StatusCode != 200 || !strings.Contains(string(modBody), "module mirror.example/lib") {
			t.Fatalf("@v/.mod status=%d body=%q", modResp.StatusCode, modBody)
		}
		zipResp, zipBody := doGET(t, tsObj, "/lib/@v/v1.0.0.zip", nil)
		if zipResp.StatusCode != 200 || len(zipBody) == 0 {
			t.Fatalf("@v/.zip status=%d len=%d", zipResp.StatusCode, len(zipBody))
		}
	})

	t.Run("composer", func(t *testing.T) {
		pkgResp, pkgBody := doGET(t, tsObj, "/packages.json", nil)
		if pkgResp.StatusCode != 200 || !strings.Contains(string(pkgBody), "vendor/pkg") {
			t.Fatalf("packages.json status=%d body=%s", pkgResp.StatusCode, pkgBody)
		}
		listResp, listBody := doGET(t, tsObj, "/packages/list.json", nil)
		if listResp.StatusCode != 200 || !strings.Contains(string(listBody), "vendor/pkg") {
			t.Fatalf("packages/list.json status=%d body=%s", listResp.StatusCode, listBody)
		}
		p2Resp, p2Body := doGET(t, tsObj, "/p2/vendor/pkg.json", nil)
		if p2Resp.StatusCode != 200 || !strings.Contains(string(p2Body), "vendor/pkg") {
			t.Fatalf("p2 status=%d body=%s", p2Resp.StatusCode, p2Body)
		}
	})

	t.Run("feeds", func(t *testing.T) {
		for _, pathText := range []string{"/feed.xml", "/lib/releases.xml"} {
			respObj, bodyArr := doGET(t, tsObj, pathText, nil)
			if respObj.StatusCode != 200 || !strings.HasPrefix(respObj.Header.Get("Content-Type"), "application/atom+xml") {
				t.Fatalf("%s status=%d ct=%q", pathText, respObj.StatusCode, respObj.Header.Get("Content-Type"))
			}
			var probe struct {
				XMLName xml.Name `xml:"feed"`
			}
			if err := xml.Unmarshal(bodyArr, &probe); err != nil {
				t.Fatalf("%s not well-formed atom: %v", pathText, err)
			}
		}
	})
}

// // // // // // // // // //

// A newest version without go detection must neither appear in @v/list nor 404 the whole major.
func TestGoProxySkipsNonGoVersions(t *testing.T) {
	serverObj, lc := newTestServer(t, withNonGoNewestVersion())
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	listResp, listBody := doGET(t, tsObj, "/lib/@v/list", nil)
	if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
		t.Fatalf("@v/list status=%d body=%q want only v1.0.0", listResp.StatusCode, listBody)
	}

	latestResp, latestBody := doGET(t, tsObj, "/lib/@latest", nil)
	var infoObj api.GoInfoObj
	if latestResp.StatusCode != 200 || json.Unmarshal(latestBody, &infoObj) != nil || infoObj.Version != "v1.0.0" {
		t.Fatalf("@latest status=%d body=%s want v1.0.0", latestResp.StatusCode, latestBody)
	}

	viResp, _ := doGET(t, tsObj, "/lib/@v/v1.1.0.info", nil)
	if viResp.StatusCode != 404 {
		t.Fatalf("non-go version .info status=%d want 404", viResp.StatusCode)
	}
}

// Raw version (non-semver, universal-only): mirror routes include it and serve latest by seq,
// while go-proxy and composer p2 never see it; page and universal artifacts still serve.
func TestRawVersionRouting(t *testing.T) {
	serverObj, lc := newTestServer(t, withRawNewestVersion())
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	const rawVersion = "blockly-v9.3.3"

	listResp, listBody := doGET(t, tsObj, "/lib/@v/list", nil)
	if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
		t.Fatalf("@v/list status=%d body=%q want only v1.0.0 (raw excluded)", listResp.StatusCode, listBody)
	}
	viResp, _ := doGET(t, tsObj, "/lib/@v/"+rawVersion+".info", nil)
	if viResp.StatusCode != 404 {
		t.Fatalf("raw .info status=%d want 404", viResp.StatusCode)
	}

	mirrorResp, mirrorBody := doGET(t, tsObj, "/lib/list", nil)
	if mirrorResp.StatusCode != 200 || !strings.Contains(string(mirrorBody), rawVersion) || !strings.Contains(string(mirrorBody), "v1.0.0") {
		t.Fatalf("/lib/list status=%d body=%q want raw and semver versions", mirrorResp.StatusCode, mirrorBody)
	}
	latestResp, latestBody := doGET(t, tsObj, "/lib/latest", nil)
	if latestResp.StatusCode != 200 || strings.TrimSpace(string(latestBody)) != rawVersion {
		t.Fatalf("/lib/latest status=%d body=%q want raw version by seq", latestResp.StatusCode, latestBody)
	}

	relResp, relBody := doGET(t, tsObj, "/lib/releases.json", nil)
	var relObj api.ReleaseListObj
	if relResp.StatusCode != 200 || json.Unmarshal(relBody, &relObj) != nil {
		t.Fatalf("releases.json status=%d body=%s", relResp.StatusCode, relBody)
	}
	if len(relObj.Releases) != 2 || relObj.Releases[0].Version != rawVersion {
		t.Fatalf("releases.json content: %+v want raw version first", relObj.Releases)
	}

	p2Resp, p2Body := doGET(t, tsObj, "/p2/vendor/pkg.json", nil)
	if p2Resp.StatusCode != 200 || strings.Contains(string(p2Body), rawVersion) {
		t.Fatalf("p2 status=%d body=%s: raw version must never reach composer p2", p2Resp.StatusCode, p2Body)
	}
	if !strings.Contains(string(p2Body), "v1.0.0") {
		t.Fatalf("p2 body=%s: semver version must stay in composer p2", p2Body)
	}

	zipResp, zipBody := doGET(t, tsObj, "/lib/"+rawVersion+".zip", nil)
	if zipResp.StatusCode != 200 || string(zipBody) != cArtifactBytes {
		t.Fatalf("raw universal zip status=%d body=%q", zipResp.StatusCode, zipBody)
	}
	tarResp, _ := doGET(t, tsObj, "/lib/"+rawVersion+".tar.gz", nil)
	if tarResp.StatusCode != 200 {
		t.Fatalf("raw universal tar.gz status=%d", tarResp.StatusCode)
	}

	pageResp, pageBody := doGET(t, tsObj, "/lib/"+rawVersion, nil)
	if pageResp.StatusCode != 200 || !strings.Contains(string(pageBody), rawVersion) {
		t.Fatalf("raw version page status=%d", pageResp.StatusCode)
	}
	if !strings.Contains(string(pageBody), "raw version: universal archives only") {
		t.Fatal("raw version page must carry the raw label")
	}
}

// A version with blocked go-zip is honestly excluded from Go routes: list/@latest show only
// publishable versions, and direct requests return 404.
func TestGoProxySkipsGoZipBlockedVersions(t *testing.T) {
	serverObj, lc := newTestServer(t, withGoZipBlockedNewestVersion())
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	listResp, listBody := doGET(t, tsObj, "/lib/@v/list", nil)
	if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
		t.Fatalf("@v/list status=%d body=%q want only v1.0.0", listResp.StatusCode, listBody)
	}

	latestResp, latestBody := doGET(t, tsObj, "/lib/@latest", nil)
	var infoObj api.GoInfoObj
	if latestResp.StatusCode != 200 || json.Unmarshal(latestBody, &infoObj) != nil || infoObj.Version != "v1.0.0" {
		t.Fatalf("@latest status=%d body=%s want v1.0.0", latestResp.StatusCode, latestBody)
	}

	for _, suffix := range []string{".info", ".mod", ".zip"} {
		respObj, _ := doGET(t, tsObj, "/lib/@v/v1.2.0"+suffix, nil)
		if respObj.StatusCode != 404 {
			t.Fatalf("blocked version %s status=%d want 404", suffix, respObj.StatusCode)
		}
	}
}

// Major >=2 routes use the real module path grammar `/{key}/vN/@v/...`.
// The old `/vN/{key}/...` form is dead, and the v1 line does not mix in other majors.
func TestGoProxyMajor2Routing(t *testing.T) {
	serverObj, lc := newTestServer(t, withV2Version())
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	t.Run("v2 list short and canonical", func(t *testing.T) {
		for _, pathText := range []string{"/lib/v2/@v/list", "/mirror.example/lib/v2/@v/list"} {
			respObj, bodyArr := doGET(t, tsObj, pathText, nil)
			if respObj.StatusCode != 200 || strings.TrimSpace(string(bodyArr)) != "v2.44.0" {
				t.Fatalf("%s status=%d body=%q want v2.44.0", pathText, respObj.StatusCode, bodyArr)
			}
		}
	})

	t.Run("v2 latest", func(t *testing.T) {
		respObj, bodyArr := doGET(t, tsObj, "/lib/v2/@latest", nil)
		var infoObj api.GoInfoObj
		if respObj.StatusCode != 200 || json.Unmarshal(bodyArr, &infoObj) != nil || infoObj.Version != "v2.44.0" {
			t.Fatalf("v2 @latest status=%d body=%s", respObj.StatusCode, bodyArr)
		}
	})

	t.Run("v2 info mod zip", func(t *testing.T) {
		infoResp, infoBody := doGET(t, tsObj, "/lib/v2/@v/v2.44.0.info", nil)
		if infoResp.StatusCode != 200 || !strings.Contains(string(infoBody), "v2.44.0") {
			t.Fatalf(".info status=%d body=%q", infoResp.StatusCode, infoBody)
		}
		modResp, modBody := doGET(t, tsObj, "/lib/v2/@v/v2.44.0.mod", nil)
		if modResp.StatusCode != 200 || !strings.Contains(string(modBody), "module mirror.example/lib/v2") {
			t.Fatalf(".mod status=%d body=%q want module line ending /v2", modResp.StatusCode, modBody)
		}
		zipResp, zipBody := doGET(t, tsObj, "/lib/v2/@v/v2.44.0.zip", nil)
		if zipResp.StatusCode != 200 || len(zipBody) == 0 {
			t.Fatalf(".zip status=%d len=%d", zipResp.StatusCode, len(zipBody))
		}
	})

	t.Run("v1 line unaffected", func(t *testing.T) {
		listResp, listBody := doGET(t, tsObj, "/lib/@v/list", nil)
		if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
			t.Fatalf("v1 list status=%d body=%q want only v1.0.0", listResp.StatusCode, listBody)
		}
		latestResp, latestBody := doGET(t, tsObj, "/lib/@latest", nil)
		var infoObj api.GoInfoObj
		if latestResp.StatusCode != 200 || json.Unmarshal(latestBody, &infoObj) != nil || infoObj.Version != "v1.0.0" {
			t.Fatalf("v1 @latest status=%d body=%s", latestResp.StatusCode, latestBody)
		}
	})

	t.Run("cross-major strictness", func(t *testing.T) {
		for _, pathText := range []string{
			"/lib/@v/v2.44.0.info",
			"/lib/v2/@v/v1.0.0.info",
			"/lib/v1/@v/list",
		} {
			if respObj, _ := doGET(t, tsObj, pathText, nil); respObj.StatusCode != 404 {
				t.Fatalf("%s status=%d want 404", pathText, respObj.StatusCode)
			}
		}
	})

	t.Run("old vN-first form dead", func(t *testing.T) {
		for _, pathText := range []string{"/v2/lib/@v/list", "/mirror.example/v2/lib/@v/list", "/v2/@v/list"} {
			if respObj, _ := doGET(t, tsObj, pathText, nil); respObj.StatusCode != 404 {
				t.Fatalf("%s status=%d want 404", pathText, respObj.StatusCode)
			}
		}
	})

	t.Run("module path prefix probes get 404", func(t *testing.T) {
		for _, pathText := range []string{
			"/mirror.example/@v/v2.44.0.info",
			"/mirror.example/@latest",
			"/mirror.example/lib/@v/v2.44.0.info",
		} {
			if respObj, _ := doGET(t, tsObj, pathText, nil); respObj.StatusCode != 404 {
				t.Fatalf("%s status=%d want 404", pathText, respObj.StatusCode)
			}
		}
	})

	t.Run("html and artifact paths untouched", func(t *testing.T) {
		respObj, _ := doGET(t, tsObj, "/lib/v2.44.0", nil)
		if respObj.StatusCode != 200 || !strings.HasPrefix(respObj.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("html version page status=%d ct=%q", respObj.StatusCode, respObj.Header.Get("Content-Type"))
		}
	})
}

// // // // // // // // // //

// In nested mode, the major segment is stripped after the prefix: /prefix/key/vN/@v/...
func TestGoProxyMajor2Nested(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html>root</html>"), 0o600); err != nil {
		t.Fatalf("write index: %v", err)
	}
	serverObj, lc := newTestServer(t, withStaticDir(dir), withV2Version())
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, pathText := range []string{"/pkg/lib/v2/@v/list", "/mirror.example/pkg/lib/v2/@v/list"} {
		respObj, bodyArr := doGET(t, tsObj, pathText, nil)
		if respObj.StatusCode != 200 || strings.TrimSpace(string(bodyArr)) != "v2.44.0" {
			t.Fatalf("%s status=%d body=%q want v2.44.0", pathText, respObj.StatusCode, bodyArr)
		}
	}

	modResp, modBody := doGET(t, tsObj, "/pkg/lib/v2/@v/v2.44.0.mod", nil)
	if modResp.StatusCode != 200 || !strings.Contains(string(modBody), "module mirror.example/pkg/lib/v2") {
		t.Fatalf("nested .mod status=%d body=%q", modResp.StatusCode, modBody)
	}

	listResp, listBody := doGET(t, tsObj, "/pkg/lib/@v/list", nil)
	if listResp.StatusCode != 200 || strings.TrimSpace(string(listBody)) != "v1.0.0" {
		t.Fatalf("nested v1 list status=%d body=%q", listResp.StatusCode, listBody)
	}
}

// // // // // // // // // //

func TestCachingAndConditional(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, pathText := range []string{"/catalog.json", "/lib/releases.json"} {
		respObj, _ := doGET(t, tsObj, pathText, nil)
		etag := respObj.Header.Get("Etag")
		if respObj.StatusCode != 200 || etag == "" {
			t.Fatalf("%s status=%d etag=%q", pathText, respObj.StatusCode, etag)
		}
		if cc := respObj.Header.Get("Cache-Control"); !strings.Contains(cc, "max-age") {
			t.Fatalf("%s cache-control=%q", pathText, cc)
		}
		condResp, condBody := doGET(t, tsObj, pathText, map[string]string{"If-None-Match": etag})
		if condResp.StatusCode != http.StatusNotModified {
			t.Fatalf("%s conditional status=%d want 304", pathText, condResp.StatusCode)
		}
		if len(condBody) != 0 {
			t.Fatalf("%s 304 body must be empty, got %q", pathText, condBody)
		}
	}
}

// // // // // // // // // //

func TestStaleEtagOnDeletion(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, _ := doGET(t, tsObj, "/lib/releases.json", nil)
	etag := respObj.Header.Get("Etag")
	if respObj.StatusCode != 200 || etag == "" {
		t.Fatalf("releases status=%d etag=%q", respObj.StatusCode, etag)
	}
	if condResp, _ := doGET(t, tsObj, "/lib/releases.json", map[string]string{"If-None-Match": etag}); condResp.StatusCode != http.StatusNotModified {
		t.Fatalf("pre-mutation conditional status=%d want 304", condResp.StatusCode)
	}

	stateObj := stateFor(t, serverObj)
	keyStateObj := stateObj.keyStates["lib"]
	keyStateObj.VersionCount = 99
	stateObj.keyStates["lib"] = keyStateObj

	condResp, _ := doGET(t, tsObj, "/lib/releases.json", map[string]string{"If-None-Match": etag})
	if condResp.StatusCode == http.StatusNotModified {
		t.Fatalf("stale ETag still 304 after VersionCount change: per-key freshness ignores deletions")
	}
	if newEtag := condResp.Header.Get("Etag"); newEtag == "" || newEtag == etag {
		t.Fatalf("ETag did not change after deletion: old=%q new=%q", etag, newEtag)
	}
}

// // // // // // // // // //

func TestMethodGate(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, method := range []string{http.MethodPost, http.MethodPut} {
		respObj, _ := doReq(t, tsObj, method, "/catalog.json", nil)
		if respObj.StatusCode != http.StatusMethodNotAllowed {
			t.Fatalf("%s status=%d want 405", method, respObj.StatusCode)
		}
	}
}

// // // // // // // // // //

func TestHEAD(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, pathText := range []string{"/lib/v1.0.0.zip", "/catalog.json", route.Health} {
		respObj, bodyArr := doReq(t, tsObj, http.MethodHead, pathText, nil)
		if respObj.StatusCode != 200 {
			t.Fatalf("HEAD %s status=%d", pathText, respObj.StatusCode)
		}
		if len(bodyArr) != 0 {
			t.Fatalf("HEAD %s body must be empty, got %d bytes", pathText, len(bodyArr))
		}
		if respObj.Header.Get("Content-Type") == "" {
			t.Fatalf("HEAD %s missing Content-Type", pathText)
		}
	}
}

// // // // // // // // // //

func TestRange(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, bodyArr := doGET(t, tsObj, "/lib/v1.0.0.zip", map[string]string{"Range": "bytes=0-3"})
	if respObj.StatusCode != http.StatusPartialContent {
		t.Fatalf("range status=%d want 206", respObj.StatusCode)
	}
	if string(bodyArr) != cArtifactBytes[0:4] {
		t.Fatalf("range body=%q want %q", bodyArr, cArtifactBytes[0:4])
	}
	if cr := respObj.Header.Get("Content-Range"); cr != "bytes 0-3/8" {
		t.Fatalf("Content-Range=%q want bytes 0-3/8", cr)
	}
	if ar := respObj.Header.Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("Accept-Ranges=%q want bytes", ar)
	}
	if respObj.ContentLength != 4 {
		t.Fatalf("Content-Length=%d want 4 (fixed length, not chunked)", respObj.ContentLength)
	}
}

func TestArtifactContentLength(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, _ := doGET(t, tsObj, "/lib/v1.0.0.zip", nil)
	if respObj.StatusCode != 200 {
		t.Fatalf("status=%d want 200", respObj.StatusCode)
	}
	if respObj.ContentLength != int64(len(cArtifactBytes)) {
		t.Fatalf("Content-Length=%d want %d", respObj.ContentLength, len(cArtifactBytes))
	}
}

// // // // // // // // // //

func TestHTMLPages(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	htmlPages := []struct {
		path string
		want string
	}{
		{"/", "lib"},
		{"/lib", "v1.0.0"},
		{"/lib/v1.0.0", "v1.0.0"},
	}
	for _, c := range htmlPages {
		respObj, bodyArr := doGET(t, tsObj, c.path, nil)
		if respObj.StatusCode != 200 || !strings.HasPrefix(respObj.Header.Get("Content-Type"), "text/html") {
			t.Fatalf("%s status=%d ct=%q", c.path, respObj.StatusCode, respObj.Header.Get("Content-Type"))
		}
		if !strings.Contains(string(bodyArr), c.want) {
			t.Fatalf("%s html missing %q", c.path, c.want)
		}
	}

	if respObj, _ := doGET(t, tsObj, "/nope", nil); respObj.StatusCode != 404 {
		t.Fatalf("unknown key status=%d want 404", respObj.StatusCode)
	}
	if respObj, _ := doGET(t, tsObj, "/lib/v9.9.9", nil); respObj.StatusCode != 404 {
		t.Fatalf("missing bare version status=%d want 404", respObj.StatusCode)
	}
}

// // // // // // // // // //

// Catalog/key HTML uses an ETag gate: matching If-None-Match avoids render and body.
func TestHTMLPagesConditional(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, pathText := range []string{"/", "/lib"} {
		respObj, _ := doGET(t, tsObj, pathText, nil)
		etag := respObj.Header.Get("Etag")
		if respObj.StatusCode != 200 || etag == "" {
			t.Fatalf("%s status=%d etag=%q", pathText, respObj.StatusCode, etag)
		}
		if cc := respObj.Header.Get("Cache-Control"); !strings.Contains(cc, "no-cache") {
			t.Fatalf("%s cache-control=%q want no-cache", pathText, cc)
		}
		condResp, condBody := doGET(t, tsObj, pathText, map[string]string{"If-None-Match": etag})
		if condResp.StatusCode != http.StatusNotModified {
			t.Fatalf("%s conditional status=%d want 304", pathText, condResp.StatusCode)
		}
		if len(condBody) != 0 {
			t.Fatalf("%s 304 body must be empty, got %q", pathText, condBody)
		}
	}

	firstResp, _ := doGET(t, tsObj, "/lib", nil)
	pagedResp, _ := doGET(t, tsObj, "/lib?after="+webui.EncodeCursor(1, "v0.0.1"), nil)
	if firstResp.Header.Get("Etag") == pagedResp.Header.Get("Etag") {
		t.Fatalf("key page etag must differ per keyset cursor")
	}
}

// // // // // // // // // //

func TestErrorMapping(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, bodyArr := doGET(t, tsObj, "/nope/releases.json", nil)
	if respObj.StatusCode != 404 {
		t.Fatalf("missing key status=%d want 404", respObj.StatusCode)
	}
	var errObj api.DefaultErrorObj
	if json.Unmarshal(bodyArr, &errObj) != nil || errObj.Error != "not_found" {
		t.Fatalf("error body=%s", bodyArr)
	}
	if !errObj.RequestID.Set || errObj.RequestID.Value == "" {
		t.Fatalf("error missing request_id: %+v", errObj)
	}
	if ct := respObj.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("error content-type=%q", ct)
	}

	if badResp, _ := doGET(t, tsObj, "/lib/releases.json?after=!!bad", nil); badResp.StatusCode != http.StatusBadRequest {
		t.Fatalf("bad cursor status=%d want 400", badResp.StatusCode)
	}
}

func TestArchiveMissingArtifactForExistingVersionIsUnavailable(t *testing.T) {
	serverObj, lc := newTestServer(t)
	storeObj := storeFor(t, serverObj)
	delete(storeObj.artifacts, vkey("lib", "v1.0.0"))

	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	respObj, bodyArr := doGET(t, tsObj, "/lib/v1.0.0.zip", nil)
	if respObj.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("missing artifact status=%d want 503 body=%s", respObj.StatusCode, bodyArr)
	}
	var errObj api.DefaultErrorObj
	if json.Unmarshal(bodyArr, &errObj) != nil || errObj.Error != "unavailable" {
		t.Fatalf("missing artifact error body=%s", bodyArr)
	}

	missingRespObj, _ := doGET(t, tsObj, "/lib/v9.9.9.zip", nil)
	if missingRespObj.StatusCode != http.StatusNotFound {
		t.Fatalf("missing version status=%d want 404", missingRespObj.StatusCode)
	}
}
