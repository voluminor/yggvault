package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// // // // // // // // // //

func TestAssetsServed(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	caseArr := []struct {
		name        string
		path        string
		contentType string
	}{
		{"favicon", "/favicon.ico", "image/x-icon"},
		{"logo", "/logo/32", "image/png"},
		{"sitemap", "/sitemap.xml", "application/xml"},
		{"og-node", "/og.png", "image/png"},
	}
	for _, caseObj := range caseArr {
		respObj, _ := doGET(t, tsObj, caseObj.path, nil)
		if respObj.StatusCode != http.StatusOK {
			t.Fatalf("%s status=%d want 200", caseObj.name, respObj.StatusCode)
		}
		if ct := respObj.Header.Get("Content-Type"); !strings.HasPrefix(ct, caseObj.contentType) {
			t.Fatalf("%s content-type=%q want %q", caseObj.name, ct, caseObj.contentType)
		}
		if respObj.Header.Get("ETag") == "" {
			t.Fatalf("%s missing ETag", caseObj.name)
		}
	}
}

func TestAssetConditionalAndNotFound(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	firstResp, _ := doGET(t, tsObj, "/favicon.ico", nil)
	etag := firstResp.Header.Get("ETag")
	if etag == "" {
		t.Fatal("favicon has no ETag")
	}
	condResp, _ := doGET(t, tsObj, "/favicon.ico", map[string]string{"If-None-Match": etag})
	if condResp.StatusCode != http.StatusNotModified {
		t.Fatalf("favicon If-None-Match status=%d want 304", condResp.StatusCode)
	}

	if respObj, _ := doGET(t, tsObj, "/logo/999", nil); respObj.StatusCode != http.StatusNotFound {
		t.Fatalf("logo/999 status=%d want 404", respObj.StatusCode)
	}
	if respObj, _ := doGET(t, tsObj, "/og/nonexistent-key", nil); respObj.StatusCode != http.StatusNotFound {
		t.Fatalf("og/nonexistent-key status=%d want 404", respObj.StatusCode)
	}
}
