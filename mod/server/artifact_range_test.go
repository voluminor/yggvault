package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// // // // // // // // // //

func TestArtifactRangeVariants(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	t.Run("suffix bytes=-3", func(t *testing.T) {
		resp, body := doGET(t, tsObj, "/lib/v1.0.0.zip", map[string]string{"Range": "bytes=-3"})
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("status=%d want 206", resp.StatusCode)
		}
		if string(body) != cArtifactBytes[5:8] {
			t.Fatalf("body=%q want %q", body, cArtifactBytes[5:8])
		}
		if cr := resp.Header.Get("Content-Range"); cr != "bytes 5-7/8" {
			t.Fatalf("Content-Range=%q want bytes 5-7/8", cr)
		}
	})

	t.Run("open bytes=4-", func(t *testing.T) {
		resp, body := doGET(t, tsObj, "/lib/v1.0.0.zip", map[string]string{"Range": "bytes=4-"})
		if resp.StatusCode != http.StatusPartialContent {
			t.Fatalf("status=%d want 206", resp.StatusCode)
		}
		if string(body) != cArtifactBytes[4:8] {
			t.Fatalf("body=%q want %q", body, cArtifactBytes[4:8])
		}
		if cr := resp.Header.Get("Content-Range"); cr != "bytes 4-7/8" {
			t.Fatalf("Content-Range=%q want bytes 4-7/8", cr)
		}
	})

	t.Run("unsatisfiable bytes=100-200", func(t *testing.T) {
		resp, body := doGET(t, tsObj, "/lib/v1.0.0.zip", map[string]string{"Range": "bytes=100-200"})
		if resp.StatusCode != http.StatusRequestedRangeNotSatisfiable {
			t.Fatalf("status=%d want 416", resp.StatusCode)
		}
		if !strings.Contains(string(body), "range_not_satisfiable") {
			t.Fatalf("416 body=%q want range_not_satisfiable envelope", body)
		}
	})
}

func TestArtifactGoZipRange(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	resp, body := doGET(t, tsObj, "/lib/@v/v1.0.0.zip", map[string]string{"Range": "bytes=0-3"})
	if resp.StatusCode != http.StatusPartialContent {
		t.Fatalf("status=%d want 206", resp.StatusCode)
	}
	if string(body) != cArtifactBytes[0:4] {
		t.Fatalf("body=%q want %q", body, cArtifactBytes[0:4])
	}
	if ar := resp.Header.Get("Accept-Ranges"); ar != "bytes" {
		t.Fatalf("Accept-Ranges=%q want bytes", ar)
	}
}

func TestArtifactConditional(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	for _, c := range []struct {
		path string
		etag string
	}{
		{"/lib/v1.0.0.zip", cArtifactETag},
		{"/lib/@v/v1.0.0.zip", `"goz"`},
	} {
		resp, body := doGET(t, tsObj, c.path, map[string]string{"If-None-Match": c.etag})
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("%s status=%d want 304", c.path, resp.StatusCode)
		}
		if len(body) != 0 {
			t.Fatalf("%s 304 body must be empty, got %d bytes", c.path, len(body))
		}
	}
}

func TestArtifactRateLimited(t *testing.T) {
	serverObj, lc := newTestServer(t, withRateLimit(1, 1))
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	if resp, _ := doGET(t, tsObj, "/lib/v1.0.0.zip", nil); resp.StatusCode != http.StatusOK {
		t.Fatalf("first artifact status=%d want 200", resp.StatusCode)
	}
	resp, body := doGET(t, tsObj, "/lib/v1.0.0.zip", nil)
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second artifact status=%d want 429", resp.StatusCode)
	}
	if !strings.Contains(string(body), "rate_limited") {
		t.Fatalf("429 body=%q want rate_limited envelope", body)
	}
}

func TestArtifactNotFoundEnvelope(t *testing.T) {
	serverObj, lc := newTestServer(t)
	tsObj := httptest.NewServer(serverObj.Handler(lc))
	defer tsObj.Close()

	resp, body := doGET(t, tsObj, "/nope/v1.0.0.zip", nil)
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status=%d want 404", resp.StatusCode)
	}
	if !strings.Contains(string(body), "not_found") {
		t.Fatalf("404 body=%q want not_found envelope", body)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("404 content-type=%q want json", ct)
	}
}
