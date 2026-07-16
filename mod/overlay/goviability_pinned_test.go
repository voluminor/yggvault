package overlay

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"testing"
	"time"

	modzip "golang.org/x/mod/zip"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type pinnedZipFileObj struct {
	pathText string
	size     int64
	mode     fs.FileMode
	bodyArr  []byte
}

func (f pinnedZipFileObj) Path() string {
	return f.pathText
}

func (f pinnedZipFileObj) Lstat() (fs.FileInfo, error) {
	return pinnedFileInfoObj{name: path.Base(f.pathText), size: f.size, mode: f.mode}, nil
}

func (f pinnedZipFileObj) Open() (io.ReadCloser, error) {
	return io.NopCloser(bytes.NewReader(f.bodyArr)), nil
}

type pinnedFileInfoObj struct {
	name string
	size int64
	mode fs.FileMode
}

func (i pinnedFileInfoObj) Name() string {
	return i.name
}

func (i pinnedFileInfoObj) Size() int64 {
	return i.size
}

func (i pinnedFileInfoObj) Mode() fs.FileMode {
	return i.mode
}

func (i pinnedFileInfoObj) ModTime() time.Time {
	return time.Time{}
}

func (i pinnedFileInfoObj) IsDir() bool {
	return i.mode.IsDir()
}

func (i pinnedFileInfoObj) Sys() any {
	return nil
}

// // // // // // // // // //

func pinnedReasonFromCheckFiles(t *testing.T, fileArr []modzip.File) string {
	t.Helper()
	cfObj, _ := modzip.CheckFiles(fileArr)
	var invalidObj, collisionObj, oversizeObj, unclassifiedObj blockSamplesObj
	for _, feObj := range cfObj.Invalid {
		classifyInvalid(feObj, &invalidObj, &collisionObj, &oversizeObj, &unclassifiedObj)
	}
	partArr := make([]string, 0, 4)
	appendPart := func(labelText string, samplesObj blockSamplesObj) {
		if samplesObj.count == 0 {
			return
		}
		partArr = append(partArr, labelText)
	}
	appendPart("invalid file paths", invalidObj)
	appendPart("case-insensitive path collisions", collisionObj)
	appendPart("oversized files", oversizeObj)
	appendPart(GoZipUnclassifiedLabel, unclassifiedObj)
	return strings.Join(partArr, "; ")
}

func TestGoZipInvalidClassificationPinnedDetect(t *testing.T) {
	obj := newTestOverlay(t)
	goModArr := []byte("module example.com/foo\n\ngo 1.22\n")
	casesArr := []struct {
		name     string
		pathText string
		label    string
	}{
		{name: "not clean", pathText: "a/./b.txt", label: "invalid file paths"},
		{name: "not relative", pathText: "/abs.txt", label: "invalid file paths"},
		{name: "go.mod case", pathText: "GO.MOD", label: "invalid file paths"},
		{name: "invalid path", pathText: "testdata/bad?.txt", label: "invalid file paths"},
	}
	for _, caseObj := range casesArr {
		t.Run(caseObj.name, func(t *testing.T) {
			resultObj := detectResult(t, obj, map[string][]byte{
				"go.mod":         goModArr,
				caseObj.pathText: []byte("x"),
			})
			if !resultObj.Detection.GoZipBlocked || !strings.Contains(resultObj.Detection.GoZipBlockReason, caseObj.label+" (1)") {
				t.Fatalf("expected %q in block reason, got %q", caseObj.label, resultObj.Detection.GoZipBlockReason)
			}
		})
	}
}

func TestGoZipInvalidClassificationPinnedCheckFiles(t *testing.T) {
	goModArr := []byte("module example.com/foo\n\ngo 1.22\n")
	regularMode := fs.FileMode(0o644)
	casesArr := []struct {
		name    string
		fileArr []modzip.File
		label   string
	}{
		{
			name: "multiple entries",
			fileArr: []modzip.File{
				pinnedZipFileObj{pathText: "go.mod", size: int64(len(goModArr)), mode: regularMode, bodyArr: goModArr},
				pinnedZipFileObj{pathText: "dup.txt", size: 1, mode: regularMode},
				pinnedZipFileObj{pathText: "dup.txt", size: 1, mode: regularMode},
			},
			label: "case-insensitive path collisions",
		},
		{
			name: "file and directory",
			fileArr: []modzip.File{
				pinnedZipFileObj{pathText: "go.mod", size: int64(len(goModArr)), mode: regularMode, bodyArr: goModArr},
				pinnedZipFileObj{pathText: "x", size: 1, mode: regularMode},
				pinnedZipFileObj{pathText: "x/y.txt", size: 1, mode: regularMode},
			},
			label: "case-insensitive path collisions",
		},
	}
	for _, caseObj := range casesArr {
		t.Run(caseObj.name, func(t *testing.T) {
			reasonText := pinnedReasonFromCheckFiles(t, caseObj.fileArr)
			if !strings.Contains(reasonText, caseObj.label) {
				t.Fatalf("expected %q in block reason, got %q", caseObj.label, reasonText)
			}
		})
	}
}

func TestGoZipInvalidClassificationPinnedGoModOversize(t *testing.T) {
	goModArr := []byte("module example.com/foo\n\ngo 1.22\n")
	treeArr := []core.TreeEntryObj{
		{Path: "go.mod", Mode: core.ModeFile, SizeBytes: modzip.MaxGoMod + 1, BlobHash: core.HashBytes(goModArr)},
	}
	reasonText := goZipBlockReason(treeArr, goModArr)
	if !strings.Contains(reasonText, "oversized files (1)") || !strings.Contains(reasonText, "go.mod") {
		t.Fatalf("expected oversized go.mod classification, got %q", reasonText)
	}
}

func TestGoZipInvalidClassificationFallback(t *testing.T) {
	var invalidObj, collisionObj, oversizeObj, unclassifiedObj blockSamplesObj
	classifyInvalid(modzip.FileError{Path: "future.txt", Err: errors.New("some future modzip error")}, &invalidObj, &collisionObj, &oversizeObj, &unclassifiedObj)
	if unclassifiedObj.count != 1 || invalidObj.count != 0 || collisionObj.count != 0 || oversizeObj.count != 0 {
		t.Fatalf("future error must be unclassified, got invalid=%d collision=%d oversize=%d unclassified=%d", invalidObj.count, collisionObj.count, oversizeObj.count, unclassifiedObj.count)
	}
}
