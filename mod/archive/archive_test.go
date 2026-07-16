package archive

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type zipTestEntryObj struct {
	path   string
	body   []byte
	mode   os.FileMode
	method uint16
}

type tarTestEntryObj struct {
	path     string
	body     []byte
	linkPath string
	paxObj   map[string]string
	typeFlag byte
	mode     int64
}

// //

func testLimitsObj() LimitsObj {
	return LimitsObj{
		MaxArchiveSize:         10 * 1000 * 1000,
		MaxArchiveUnpackedSize: 10 * 1000 * 1000,
		MaxArchiveFileBytes:    1000 * 1000,
		MaxArchiveFiles:        1000,
		MaxArchivePathBytes:    512,
	}
}

func newTestObj(t testing.TB, limitsObj LimitsObj) *Obj {
	t.Helper()

	obj, err := New(limitsObj)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return obj
}

func writeZipSourceObj(t testing.TB, sourcePath string, entryArr []zipTestEntryObj) {
	t.Helper()

	fileObj, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	writerObj := zip.NewWriter(fileObj)
	for _, entryObj := range entryArr {
		headerObj := &zip.FileHeader{
			Name:   entryObj.path,
			Method: entryObj.method,
		}
		if headerObj.Method == 0 {
			headerObj.Method = zip.Deflate
		}
		modeObj := entryObj.mode
		if modeObj == 0 {
			modeObj = 0o644
		}
		headerObj.SetMode(modeObj)
		entryWriterObj, err := writerObj.CreateHeader(headerObj)
		if err != nil {
			t.Fatalf("CreateHeader returned error: %v", err)
		}
		if len(entryObj.body) > 0 {
			if _, err = entryWriterObj.Write(entryObj.body); err != nil {
				t.Fatalf("zip entry Write returned error: %v", err)
			}
		}
	}
	if err = writerObj.Close(); err != nil {
		t.Fatalf("zip Close returned error: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("file Close returned error: %v", err)
	}
}

func writeTarSourceObj(t testing.TB, sourcePath string, gzipFlag bool, trailingArr []byte, entryArr []tarTestEntryObj) {
	t.Helper()

	fileObj, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	var writerObj io.Writer = fileObj
	var gzipWriterObj *gzip.Writer
	if gzipFlag {
		gzipWriterObj = gzip.NewWriter(fileObj)
		writerObj = gzipWriterObj
	}
	tarWriterObj := tar.NewWriter(writerObj)
	for _, entryObj := range entryArr {
		modeValue := entryObj.mode
		if modeValue == 0 {
			modeValue = 0o644
		}
		headerObj := &tar.Header{
			Name:       entryObj.path,
			Mode:       modeValue,
			Size:       int64(len(entryObj.body)),
			Linkname:   entryObj.linkPath,
			PAXRecords: entryObj.paxObj,
			Typeflag:   entryObj.typeFlag,
		}
		if headerObj.Typeflag == 0 {
			headerObj.Typeflag = tar.TypeReg
		}
		if headerObj.Typeflag != tar.TypeReg {
			headerObj.Size = 0
		}
		if err = tarWriterObj.WriteHeader(headerObj); err != nil {
			t.Fatalf("WriteHeader returned error: %v", err)
		}
		if headerObj.Size > 0 {
			if _, err = tarWriterObj.Write(entryObj.body); err != nil {
				t.Fatalf("tar entry Write returned error: %v", err)
			}
		}
	}
	if err = tarWriterObj.Close(); err != nil {
		t.Fatalf("tar Close returned error: %v", err)
	}
	if gzipWriterObj != nil {
		if err = gzipWriterObj.Close(); err != nil {
			t.Fatalf("gzip Close returned error: %v", err)
		}
	}
	if len(trailingArr) > 0 {
		if _, err = fileObj.Write(trailingArr); err != nil {
			t.Fatalf("trailing Write returned error: %v", err)
		}
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("file Close returned error: %v", err)
	}
}

func extractSourceObj(t testing.TB, obj *Obj, formatObj FormatType, sourcePath string, spoolPath string) (ResultObj, error) {
	t.Helper()

	return obj.Extract(context.Background(), RequestObj{
		Key:        "core-lib",
		Version:    "v1.0.0",
		Format:     formatObj,
		SourcePath: sourcePath,
		SpoolPath:  spoolPath,
	})
}

func expectRejectedCheckObj(t testing.TB, err error, checkName string) {
	t.Helper()

	var typedErr *stcode.ErrArchiveRejectedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("error=%T, want ErrArchiveRejectedObj: %v", err, err)
	}
	if typedErr.CheckName != checkName {
		t.Fatalf("check_name=%s, want %s", typedErr.CheckName, checkName)
	}
}

func expectLimitsCheckObj(t testing.TB, err error, checkName string) *stcode.ErrArchiveLimitExceededObj {
	t.Helper()

	var typedErr *stcode.ErrArchiveLimitExceededObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
	}
	if typedErr.Check != checkName {
		t.Fatalf("check_name=%s, want %s", typedErr.Check, checkName)
	}
	return typedErr
}

func spoolEntryCount(t testing.TB, spoolPath string) int {
	t.Helper()

	entryArr, err := os.ReadDir(spoolPath)
	if err != nil {
		t.Fatalf("ReadDir returned error: %v", err)
	}
	return len(entryArr)
}

// //

func TestExtractZipStripsTopDirAndWritesStaged(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	spoolPath := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	goModArr := []byte("module example.com/core\n")
	textArr := []byte("hello from archive\n")
	writeZipSourceObj(t, sourcePath, []zipTestEntryObj{
		{path: "repo-v1.0.0/go.mod", body: goModArr},
		{path: "repo-v1.0.0/pkg/a.txt", body: textArr},
	})

	resultObj, err := extractSourceObj(t, archiveObj, FormatZip, sourcePath, spoolPath)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(resultObj.Entries) != 2 || resultObj.Entries[0].Path != "go.mod" || resultObj.Entries[1].Path != "pkg/a.txt" {
		t.Fatalf("entries=%+v, want stripped paths", resultObj.Entries)
	}
	if len(resultObj.Blobs) != 2 {
		t.Fatalf("blobs=%d, want 2", len(resultObj.Blobs))
	}
	blobArr, err := os.ReadFile(resultObj.Blobs[0].FilePath)
	if err != nil {
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if !bytes.Equal(blobArr, goModArr) && !bytes.Equal(blobArr, textArr) {
		t.Fatalf("first staged blob=%q, want one source body", blobArr)
	}
}

func TestExtractZipCommentCanContainEOCDSignature(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	spoolPath := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}

	fileObj, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	writerObj := zip.NewWriter(fileObj)
	entryWriterObj, err := writerObj.Create("file.txt")
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	if _, err = entryWriterObj.Write([]byte("body")); err != nil {
		t.Fatalf("zip entry Write returned error: %v", err)
	}
	if err = writerObj.SetComment("comment PK\x05\x06 tail"); err != nil {
		t.Fatalf("SetComment returned error: %v", err)
	}
	if err = writerObj.Close(); err != nil {
		t.Fatalf("zip Close returned error: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("file Close returned error: %v", err)
	}

	resultObj, err := extractSourceObj(t, archiveObj, FormatZip, sourcePath, spoolPath)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(resultObj.Entries) != 1 || resultObj.Entries[0].Path != "file.txt" {
		t.Fatalf("entries=%+v, want file.txt", resultObj.Entries)
	}
}

func TestWriteTempBytesRejectsReplacedSpool(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("open directory replacement guard is unix-only")
	}

	spoolPath := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	_, spoolGuardObj, err := validateSpoolPath(spoolPath)
	if err != nil {
		t.Fatalf("validateSpoolPath returned error: %v", err)
	}
	defer func() {
		_ = spoolGuardObj.close()
	}()
	if err = os.RemoveAll(spoolPath); err != nil {
		t.Fatalf("RemoveAll returned error: %v", err)
	}
	if err = os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir replacement returned error: %v", err)
	}

	_, err = writeTempBytes(spoolPath, spoolGuardObj, []byte("body"))
	if err == nil {
		t.Fatal("writeTempBytes accepted replaced spool")
	}
	if !strings.Contains(err.Error(), "spool path changed") {
		t.Fatalf("error=%v, want spool path changed", err)
	}
}

func TestExtractZipRejectsTraversal(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	writeZipSourceObj(t, sourcePath, []zipTestEntryObj{
		{path: "../evil.txt", body: []byte("bad")},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatZip, sourcePath, spoolPath)
	expectRejectedCheckObj(t, err, cCheckMalformed)
	if countValue := spoolEntryCount(t, spoolPath); countValue != 0 {
		t.Fatalf("spool entries=%d, want 0", countValue)
	}
}

func TestExtractZipRejectsDuplicateAfterCleanAndCleansSpool(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	writeZipSourceObj(t, sourcePath, []zipTestEntryObj{
		{path: "repo/a.txt", body: []byte("one")},
		{path: "repo/./a.txt", body: []byte("two")},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatZip, sourcePath, spoolPath)
	expectRejectedCheckObj(t, err, cCheckDuplicatePath)
	if countValue := spoolEntryCount(t, spoolPath); countValue != 0 {
		t.Fatalf("spool entries=%d, want 0", countValue)
	}
}

func TestExtractZipRejectsNonAdjacentFileChildConflict(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.zip")
	writeZipSourceObj(t, sourcePath, []zipTestEntryObj{
		{path: "a", body: []byte("one")},
		{path: "a.b", body: []byte("two")},
		{path: "a/b", body: []byte("three")},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatZip, sourcePath, spoolPath)
	expectRejectedCheckObj(t, err, cCheckPathConflict)
	if countValue := spoolEntryCount(t, spoolPath); countValue != 0 {
		t.Fatalf("spool entries=%d, want 0", countValue)
	}
}

func TestExtractTarGzDropsUnsafeSymlink(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarSourceObj(t, sourcePath, true, nil, []tarTestEntryObj{
		{path: "repo/file.txt", body: []byte("body")},
		{path: "repo/link", linkPath: "../escape", typeFlag: tar.TypeSymlink},
	})
	spoolPath := t.TempDir()

	resultObj, err := extractSourceObj(t, archiveObj, FormatTarGz, sourcePath, spoolPath)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(resultObj.Entries) != 1 || resultObj.Entries[0].Path != "file.txt" {
		t.Fatalf("entries=%+v, want only safe file", resultObj.Entries)
	}
	if len(resultObj.DroppedSymlinks) != 1 || resultObj.DroppedSymlinks[0] != "link" {
		t.Fatalf("dropped=%+v, want link", resultObj.DroppedSymlinks)
	}
}

// Forgejo regression: a relative symlink inside the archive must be accepted. After common top-dir stripping,
// `notes/latest.md -> ../RELEASE-NOTES.md` resolves to root and does not escape.
func TestExtractTarGzAcceptsInRootRelativeSymlink(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarSourceObj(t, sourcePath, true, nil, []tarTestEntryObj{
		{path: "repo/RELEASE-NOTES.md", body: []byte("notes")},
		{path: "repo/notes/latest.md", linkPath: "../RELEASE-NOTES.md", typeFlag: tar.TypeSymlink},
	})
	spoolPath := t.TempDir()

	resultObj, err := extractSourceObj(t, archiveObj, FormatTarGz, sourcePath, spoolPath)
	if err != nil {
		t.Fatalf("in-root relative symlink must be accepted, got: %v", err)
	}
	found := false
	for i := range resultObj.Entries {
		if resultObj.Entries[i].Path == "notes/latest.md" {
			found = resultObj.Entries[i].Mode == core.ModeSymlink
		}
	}
	if !found {
		t.Fatalf("symlink entry missing or not a symlink after extract: %+v", resultObj.Entries)
	}
}

// The escape check uses the path after stripCommonTopDir and drops only the dangerous symlink.
func TestExtractTarGzDropsStripDepthSymlinkEscape(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarSourceObj(t, sourcePath, true, nil, []tarTestEntryObj{
		{path: "top/file.txt", body: []byte("x")},
		{path: "top/shallow", linkPath: "../evil", typeFlag: tar.TypeSymlink},
	})
	spoolPath := t.TempDir()

	resultObj, err := extractSourceObj(t, archiveObj, FormatTarGz, sourcePath, spoolPath)
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	if len(resultObj.Entries) != 1 || resultObj.Entries[0].Path != "file.txt" {
		t.Fatalf("entries=%+v, want only safe file", resultObj.Entries)
	}
	if len(resultObj.DroppedSymlinks) != 1 || resultObj.DroppedSymlinks[0] != "shallow" {
		t.Fatalf("dropped=%+v, want shallow", resultObj.DroppedSymlinks)
	}
}

func TestExtractTarRejectsHiddenPAXMetadataLimit(t *testing.T) {
	limitsObj := testLimitsObj()
	limitsObj.MaxArchiveUnpackedSize = 1
	limitsObj.MaxArchiveFiles = 1
	limitsObj.MaxArchivePathBytes = 32
	archiveObj := newTestObj(t, limitsObj)
	sourcePath := filepath.Join(t.TempDir(), "source.tar")
	writeTarSourceObj(t, sourcePath, false, nil, []tarTestEntryObj{
		{
			path: "a.txt",
			paxObj: map[string]string{
				"comment": strings.Repeat("x", 4096),
			},
		},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatTar, sourcePath, spoolPath)
	expectLimitsCheckObj(t, err, cCheckTarStream)
}

func TestExtractTarRejectsHardlink(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.tar")
	writeTarSourceObj(t, sourcePath, false, nil, []tarTestEntryObj{
		{path: "repo/link", linkPath: "repo/file.txt", typeFlag: tar.TypeLink},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatTar, sourcePath, spoolPath)
	expectRejectedCheckObj(t, err, cCheckUnsupported)
}

func TestExtractTarGzRejectsTrailingData(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	sourcePath := filepath.Join(t.TempDir(), "source.tar.gz")
	writeTarSourceObj(t, sourcePath, true, []byte("trailing"), []tarTestEntryObj{
		{path: "repo/a.txt", body: []byte("ok")},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatTarGz, sourcePath, spoolPath)
	expectRejectedCheckObj(t, err, cCheckGzipTrailing)
	if countValue := spoolEntryCount(t, spoolPath); countValue != 0 {
		t.Fatalf("spool entries=%d, want 0", countValue)
	}
}

func TestExtractTarGzAcceptsRecordPadding(t *testing.T) {
	var tarBufObj bytes.Buffer
	tarWriterObj := tar.NewWriter(&tarBufObj)
	bodyArr := []byte("hello record padding\n")
	if err := tarWriterObj.WriteHeader(&tar.Header{Name: "repo/a.txt", Mode: 0o644, Size: int64(len(bodyArr)), Typeflag: tar.TypeReg}); err != nil {
		t.Fatalf("WriteHeader returned error: %v", err)
	}
	if _, err := tarWriterObj.Write(bodyArr); err != nil {
		t.Fatalf("tar Write returned error: %v", err)
	}
	if err := tarWriterObj.Close(); err != nil {
		t.Fatalf("tar Close returned error: %v", err)
	}
	const recordSize = 10240
	if padCount := recordSize - tarBufObj.Len()%recordSize; padCount != recordSize {
		tarBufObj.Write(make([]byte, padCount))
	}

	sourcePath := filepath.Join(t.TempDir(), "padded.tar.gz")
	fileObj, err := os.Create(sourcePath)
	if err != nil {
		t.Fatalf("Create returned error: %v", err)
	}
	gzipWriterObj := gzip.NewWriter(fileObj)
	if _, err = gzipWriterObj.Write(tarBufObj.Bytes()); err != nil {
		t.Fatalf("gzip Write returned error: %v", err)
	}
	if err = gzipWriterObj.Close(); err != nil {
		t.Fatalf("gzip Close returned error: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("file Close returned error: %v", err)
	}

	archiveObj := newTestObj(t, testLimitsObj())
	spoolPath := t.TempDir()
	if _, err = extractSourceObj(t, archiveObj, FormatTarGz, sourcePath, spoolPath); err != nil {
		t.Fatalf("extract of record-padded tar.gz returned error: %v", err)
	}
	if countValue := spoolEntryCount(t, spoolPath); countValue != 1 {
		t.Fatalf("spool entries=%d, want 1", countValue)
	}
}

func TestExtractCountsDirectoryHeaders(t *testing.T) {
	limitsObj := testLimitsObj()
	limitsObj.MaxArchiveFiles = 1
	archiveObj := newTestObj(t, limitsObj)
	sourcePath := filepath.Join(t.TempDir(), "source.tar")
	writeTarSourceObj(t, sourcePath, false, nil, []tarTestEntryObj{
		{path: "repo", typeFlag: tar.TypeDir, mode: 0o755},
		{path: "repo/pkg", typeFlag: tar.TypeDir, mode: 0o755},
	})
	spoolPath := t.TempDir()

	_, err := extractSourceObj(t, archiveObj, FormatTar, sourcePath, spoolPath)
	typedErr := expectLimitsCheckObj(t, err, cCheckHeaderCount)
	if typedErr.Value != 2 || typedErr.MaxValue != 1 {
		t.Fatalf("files=%d max_files=%d, want 2/1", typedErr.Value, typedErr.MaxValue)
	}
}

func BenchmarkExtractZipManySmall(b *testing.B) {
	basePath := b.TempDir()
	sourcePath := filepath.Join(basePath, "source.zip")
	entryArr := make([]zipTestEntryObj, 0, 256)
	bodyArr := []byte("payload\n")
	for i := 0; i < 256; i++ {
		entryArr = append(entryArr, zipTestEntryObj{
			path: fmt.Sprintf("repo/pkg/file-%03d.txt", i),
			body: bodyArr,
		})
	}
	writeZipSourceObj(b, sourcePath, entryArr)
	archiveObj := newTestObj(b, LimitsObj{
		MaxArchiveSize:         20 * 1000 * 1000,
		MaxArchiveUnpackedSize: 20 * 1000 * 1000,
		MaxArchiveFileBytes:    1000 * 1000,
		MaxArchiveFiles:        1000,
		MaxArchivePathBytes:    512,
	})
	b.SetBytes(int64(len(bodyArr) * len(entryArr)))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		spoolPath, err := os.MkdirTemp(basePath, "spool-*")
		if err != nil {
			b.Fatalf("MkdirTemp returned error: %v", err)
		}
		if _, err = extractSourceObj(b, archiveObj, FormatZip, sourcePath, spoolPath); err != nil {
			_ = os.RemoveAll(spoolPath)
			b.Fatalf("Extract returned error: %v", err)
		}
		if err = os.RemoveAll(spoolPath); err != nil {
			b.Fatalf("RemoveAll returned error: %v", err)
		}
	}
}
