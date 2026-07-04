package source

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func makeZip(t *testing.T, files map[string][]byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, data := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create: %v", err)
		}
		if _, err := w.Write(data); err != nil {
			t.Fatalf("zip write: %v", err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return buf.Bytes()
}

func serveBytes(t *testing.T, status int, body []byte) *httptest.Server {
	t.Helper()
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if status != http.StatusOK {
			w.WriteHeader(status)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(ts.Close)
	return ts
}

func archiveLimitsOf(configObj *stconf.ConfigObj) archive.LimitsObj {
	return archive.LimitsObj{
		MaxArchiveSize:         uint64(configObj.Storage.ArchiveLimits.Size.Compressed),
		MaxArchiveUnpackedSize: uint64(configObj.Storage.ArchiveLimits.Size.Unpacked),
		MaxArchiveFileBytes:    uint64(configObj.Storage.ArchiveLimits.Size.PerFile),
		MaxArchiveFiles:        configObj.Storage.ArchiveLimits.Entries.Count,
		MaxArchivePathBytes:    configObj.Storage.ArchiveLimits.Entries.PathBytes,
	}
}

// // // // // // // // // //

func TestFetchArchiveWritesToSpool(t *testing.T) {
	zipBytes := makeZip(t, map[string][]byte{"repo-1.0.0/go.mod": []byte("module example.com/x\n")})
	ts := serveBytes(t, http.StatusOK, zipBytes)
	obj := newTestObj(t, testConfigObj(t))

	destDir := t.TempDir()
	res, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key:        "core-lib",
		Version:    "v1.0.0",
		ArchiveURL: ts.URL + "/a.zip",
		Format:     cFormatZip,
		DestDir:    destDir,
	})
	if err != nil {
		t.Fatalf("FetchArchive returned error: %v", err)
	}
	if res.SizeBytes != uint64(len(zipBytes)) {
		t.Fatalf("size=%d, want %d", res.SizeBytes, len(zipBytes))
	}
	got, err := os.ReadFile(res.ArchivePath)
	if err != nil || !bytes.Equal(got, zipBytes) {
		t.Fatalf("archive file mismatch: err=%v", err)
	}
}

func TestFetchArchiveSizeCap(t *testing.T) {
	zipBytes := makeZip(t, map[string][]byte{"repo/go.mod": []byte("module example.com/x\n")})
	ts := serveBytes(t, http.StatusOK, zipBytes)
	configObj := testConfigObj(t)
	configObj.Storage.ArchiveLimits.Size.Compressed = 8
	obj := newTestObj(t, configObj)

	_, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key: "core-lib", Version: "v1.0.0", ArchiveURL: ts.URL + "/a.zip", Format: cFormatZip, DestDir: t.TempDir(),
	})
	var typedErr *stcode.ErrArchiveLimitExceededObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrArchiveLimitExceeded, got %v", err)
	}
}

func TestFetchArchive404Permanent(t *testing.T) {
	ts := serveBytes(t, http.StatusNotFound, nil)
	obj := newTestObj(t, testConfigObj(t))

	_, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key: "core-lib", Version: "v1.0.0", ArchiveURL: ts.URL + "/missing.zip", Format: cFormatZip, DestDir: t.TempDir(),
	})
	var typedErr *stcode.ErrSourceDownloadFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("expected ErrSourceDownloadFailed, got %v", err)
	}
}

// // // // // // // // // //

func TestFetchArchiveFeedsArchiveExtract(t *testing.T) {
	zipBytes := makeZip(t, map[string][]byte{
		"repo-1.0.0/go.mod":  []byte("module example.com/core\n"),
		"repo-1.0.0/main.go": []byte("package main\n"),
	})
	ts := serveBytes(t, http.StatusOK, zipBytes)
	configObj := testConfigObj(t)
	obj := newTestObj(t, configObj)

	fetchRes, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key: "core-lib", Version: "v1.0.0", ArchiveURL: ts.URL + "/a.zip", Format: cFormatZip, DestDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("FetchArchive returned error: %v", err)
	}

	archiveObj, err := archive.New(archiveLimitsOf(configObj))
	if err != nil {
		t.Fatalf("archive.New returned error: %v", err)
	}
	spoolPath := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	result, err := archiveObj.Extract(context.Background(), archive.RequestObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		Format:          archive.FormatZip,
		SourcePath:      fetchRes.ArchivePath,
		SourceSizeBytes: fetchRes.SizeBytes,
		SpoolPath:       spoolPath,
	})
	if err != nil {
		t.Fatalf("archive.Extract returned error: %v", err)
	}

	gotArr := make([]string, 0, len(result.Entries))
	for i := range result.Entries {
		gotArr = append(gotArr, result.Entries[i].Path)
	}
	slices.Sort(gotArr)
	wantArr := []string{"go.mod", "main.go"}
	if !slices.Equal(gotArr, wantArr) {
		t.Fatalf("staged paths=%v, want %v", gotArr, wantArr)
	}
}

// // // // // // // // // //

func parseRangeStart(t *testing.T, header string) int64 {
	t.Helper()
	value := strings.TrimSuffix(strings.TrimPrefix(header, "bytes="), "-")
	n, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		t.Fatalf("bad range header %q: %v", header, err)
	}
	return n
}

func TestFetchArchiveResumesWithRange(t *testing.T) {
	full := bytes.Repeat([]byte("yggvault-resume-payload!"), 64)
	const etag = `"resume-v1"`

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "no hijack", http.StatusInternalServerError)
			return
		}
		conn, buf, err := hj.Hijack()
		if err != nil {
			return
		}
		defer conn.Close()

		if rangeHdr := r.Header.Get("Range"); rangeHdr != "" {
			start := parseRangeStart(t, rangeHdr)
			rem := full[start:]
			fmt.Fprintf(buf, "HTTP/1.1 206 Partial Content\r\nETag: %s\r\nContent-Range: bytes %d-%d/%d\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
				etag, start, len(full)-1, len(full), len(rem))
			buf.Write(rem)
			buf.Flush()
			return
		}

		fmt.Fprintf(buf, "HTTP/1.1 200 OK\r\nETag: %s\r\nAccept-Ranges: bytes\r\nContent-Length: %d\r\nConnection: close\r\n\r\n",
			etag, len(full))
		buf.Write(full[:len(full)/2])
		buf.Flush()
	}))
	t.Cleanup(ts.Close)

	obj := newTestObj(t, testConfigObj(t))
	res, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key: "core-lib", Version: "v1.0.0", ArchiveURL: ts.URL + "/a.zip", Format: cFormatZip, DestDir: t.TempDir(),
	})
	if err != nil {
		t.Fatalf("FetchArchive returned error: %v", err)
	}
	got, err := os.ReadFile(res.ArchivePath)
	if err != nil || !bytes.Equal(got, full) {
		t.Fatalf("resume produced wrong content: got %d bytes, want %d (err=%v)", len(got), len(full), err)
	}
}

// // // // // // // // // //

// TestDiscoverClassifiesGitWhenHealthPageIsLarge is a regression for git forges whose /health
// returns a large HTML page; discovery must classify git instead of staying inconclusive forever.
func TestDiscoverClassifiesGitWhenHealthPageIsLarge(t *testing.T) {
	largeBody := bytes.Repeat([]byte("<html>not a vault</html>"), 8192) // ~196KB > cHealthMaxBytes
	ts := serveBytes(t, http.StatusOK, largeBody)
	obj := newTestObj(t, testConfigObj(t))

	res, err := obj.Discover(context.Background(), "core-lib", ts.URL)
	if err != nil {
		t.Fatalf("large non-vault /health must classify as git, not defer: %v", err)
	}
	if res.Class != stcode.SourceClassGit {
		t.Fatalf("expected git classification, got class=%v", res.Class)
	}
}
