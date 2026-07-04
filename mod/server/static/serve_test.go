package static

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// // // // // // // // // //

func writeFile(t *testing.T, root string, rel string, data string) {
	t.Helper()
	full := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(data), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func seedTree(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	writeFile(t, root, "index.html", "<h1>home</h1>")
	writeFile(t, root, "style.css", "body{}")
	writeFile(t, root, "sub/page.html", "<p>sub</p>")
	return root
}

// // // // // // // // // //

func TestNewBudgetTotalOverLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", strings.Repeat("a", 60))
	writeFile(t, root, "b.html", strings.Repeat("b", 60))
	if _, err := New(root, "index.html", 100, nil); err == nil {
		t.Fatal("expected total budget overflow error, got nil")
	}
}

func TestNewBudgetSingleFileOverLimit(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", strings.Repeat("x", 200))
	if _, err := New(root, "index.html", 100, nil); err == nil {
		t.Fatal("expected single-file budget overflow error, got nil")
	}
}

func TestNewBadIndexFile(t *testing.T) {
	root := seedTree(t)
	if _, err := New(root, "dir/index.html", 0, nil); err == nil {
		t.Fatal("expected error for index file with separator, got nil")
	}
	if _, err := New(root, "", 0, nil); err == nil {
		t.Fatal("expected error for empty index file, got nil")
	}
}

func TestNewDenyRules(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "ok")
	writeFile(t, root, "secret.json", "{}")
	writeFile(t, root, "private/admin.html", "secret")
	writeFile(t, root, "public/ok.html", "fine")

	snap, err := New(root, "index.html", 0, []string{".json", "/private/"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := snap.files["/secret.json"]; ok {
		t.Error("denied .json must not be in snapshot")
	}
	if _, ok := snap.files["/private/admin.html"]; ok {
		t.Error("denied /private/ prefix must not be in snapshot")
	}
	if _, ok := snap.files["/public/ok.html"]; !ok {
		t.Error("allowed file missing from snapshot")
	}
	if _, ok := snap.files["/index.html"]; !ok {
		t.Error("index missing from snapshot")
	}
}

func TestNewSymlinkNotFollowed(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	root := t.TempDir()
	writeFile(t, root, "index.html", "home")

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.txt")
	if err := os.WriteFile(secretPath, []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	linkPath := filepath.Join(root, "leak.txt")
	if err := os.Symlink(secretPath, linkPath); err != nil {
		t.Skipf("filesystem cannot symlink: %v", err)
	}

	snap, err := New(root, "index.html", 0, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := snap.files["/leak.txt"]; ok {
		t.Fatal("symlink must not be materialized into snapshot")
	}
}

func TestServeRejectsModifiedFileAfterSnapshot(t *testing.T) {
	root := t.TempDir()
	writeFile(t, root, "index.html", "home")
	snap, err := New(root, "index.html", 0, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	writeFile(t, root, "index.html", "changed-content")

	rec := httptest.NewRecorder()
	snap.Serve(rec, httptest.NewRequest(http.MethodGet, "http://x/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("modified file: want 404, got %d", rec.Code)
	}
}

func TestServeRejectsSymlinkSwapAfterSnapshot(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}

	root := t.TempDir()
	writeFile(t, root, "index.html", "home")
	snap, err := New(root, "index.html", 0, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	outsideDir := t.TempDir()
	secretPath := filepath.Join(outsideDir, "secret.html")
	if err := os.WriteFile(secretPath, []byte("secret"), 0o644); err != nil {
		t.Fatalf("write secret: %v", err)
	}
	indexPath := filepath.Join(root, "index.html")
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("remove index: %v", err)
	}
	if err := os.Symlink(secretPath, indexPath); err != nil {
		t.Skipf("filesystem cannot symlink: %v", err)
	}

	rec := httptest.NewRecorder()
	snap.Serve(rec, httptest.NewRequest(http.MethodGet, "http://x/index.html", nil))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("symlink swap: want 404, got %d", rec.Code)
	}
}

// // // // // // // // // //

func serveSnapshot(t *testing.T) *SnapshotObj {
	t.Helper()
	snap, err := New(seedTree(t), "index.html", 0, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return snap
}

func TestServeTraversalReturns404(t *testing.T) {
	snap := serveSnapshot(t)
	badPaths := []string{
		"/../etc/passwd",
		"/../../../etc/shadow",
		"/%2e%2e/x",
		"//x",
		"/./nope.html",
		"/missing.html",
		"/private/secret",
	}
	for _, p := range badPaths {
		req := httptest.NewRequest(http.MethodGet, "http://x"+p, nil)
		rec := httptest.NewRecorder()
		snap.Serve(rec, req)
		if rec.Code != http.StatusNotFound {
			t.Errorf("path %q: want 404, got %d", p, rec.Code)
		}
	}
}

func TestServeCleanedTraversalHitsRoot(t *testing.T) {
	snap := serveSnapshot(t)
	req := httptest.NewRequest(http.MethodGet, "http://x/sub/../style.css", nil)
	rec := httptest.NewRecorder()
	snap.Serve(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("cleaned-to-real path: want 200, got %d", rec.Code)
	}
}

func TestServeRealFile(t *testing.T) {
	snap := serveSnapshot(t)
	req := httptest.NewRequest(http.MethodGet, "http://x/sub/page.html", nil)
	rec := httptest.NewRecorder()
	snap.Serve(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("want 200, got %d", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "text/html") {
		t.Errorf("want text/html Content-Type, got %q", ct)
	}
	if body := rec.Body.String(); body != "<p>sub</p>" {
		t.Errorf("unexpected body %q", body)
	}
	if et := rec.Header().Get("Etag"); et == "" {
		t.Error("expected non-empty ETag")
	}
}

func TestServeIndexForRoot(t *testing.T) {
	snap := serveSnapshot(t)
	for _, p := range []string{"/", "/sub/"} {
		req := httptest.NewRequest(http.MethodGet, "http://x"+p, nil)
		rec := httptest.NewRecorder()
		snap.Serve(rec, req)
		if p == "/" {
			if rec.Code != http.StatusOK || rec.Body.String() != "<h1>home</h1>" {
				t.Errorf("root index: code=%d body=%q", rec.Code, rec.Body.String())
			}
		} else {
			if rec.Code != http.StatusNotFound {
				t.Errorf("dir without index: want 404, got %d", rec.Code)
			}
		}
	}
}

func TestServeIfNoneMatch304(t *testing.T) {
	snap := serveSnapshot(t)
	first := httptest.NewRecorder()
	snap.Serve(first, httptest.NewRequest(http.MethodGet, "http://x/index.html", nil))
	etag := first.Header().Get("Etag")
	if etag == "" {
		t.Fatal("no ETag to test conditional request")
	}

	req := httptest.NewRequest(http.MethodGet, "http://x/index.html", nil)
	req.Header.Set("If-None-Match", etag)
	rec := httptest.NewRecorder()
	snap.Serve(rec, req)
	if rec.Code != http.StatusNotModified {
		t.Fatalf("If-None-Match: want 304, got %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 must have empty body, got %d bytes", rec.Body.Len())
	}
}

func TestServeRange206(t *testing.T) {
	snap := serveSnapshot(t)
	req := httptest.NewRequest(http.MethodGet, "http://x/index.html", nil)
	req.Header.Set("Range", "bytes=0-3")
	rec := httptest.NewRecorder()
	snap.Serve(rec, req)
	if rec.Code != http.StatusPartialContent {
		t.Fatalf("Range: want 206, got %d", rec.Code)
	}
	if got := rec.Body.String(); got != "<h1>" {
		t.Errorf("partial body: want %q, got %q", "<h1>", got)
	}
}
