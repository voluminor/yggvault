package overlay

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	modzip "golang.org/x/mod/zip"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func detectResult(t *testing.T, obj *Obj, files map[string][]byte) DetectionResultObj {
	t.Helper()
	treeArr, reader := buildTree(files)
	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	return resultObj
}

// // // // // // // // // //

// A file name invalid for Go module zips blocks the Go overlay entirely:
// GoPublishable=false, no Go artifacts are planned, and the reason includes count and path.
func TestGoZipBlockedBadFileName(t *testing.T) {
	obj := planTestOverlay(t, true)
	resultObj := detectResult(t, obj, map[string][]byte{
		"go.mod":                []byte("module example.com/foo\n\ngo 1.22\n"),
		"a.go":                  []byte("package foo\n"),
		"testdata/a?b&c=1.json": []byte("{}"),
	})

	if !resultObj.Detection.IsGo {
		t.Fatalf("expected go detection: %+v", resultObj.Detection)
	}
	if !resultObj.Detection.GoZipBlocked {
		t.Fatalf("expected GoZipBlocked, got %+v", resultObj.Detection)
	}
	reason := resultObj.Detection.GoZipBlockReason
	if !strings.Contains(reason, "invalid file paths (1)") || !strings.Contains(reason, "a?b&c=1.json") {
		t.Fatalf("unexpected block reason: %q", reason)
	}

	listenerObj := ListenerCtxObj{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"}
	if obj.GoPublishable("foo", "v1.0.0", resultObj.Detection, resultObj.Go, listenerObj) {
		t.Fatal("blocked version must not be go-publishable")
	}

	planArr := obj.ArtifactPlan(nil, "foo", "v1.0.0", core.HashObj{}, resultObj.Detection, resultObj.Go, resultObj.RewriteBlobs, []ListenerCtxObj{listenerObj})
	for _, planObj := range planArr {
		if planObj.MaterializerID == stcode.MaterializerGo {
			t.Fatalf("plan must not contain go artifacts for a blocked version: %+v", planArr)
		}
	}
	if len(planArr) == 0 {
		t.Fatal("universal artifacts must stay planned for a blocked version")
	}
}

// Fold-equivalent file and directory paths block because modzip rejects those trees.
func TestGoZipBlockedCaseCollision(t *testing.T) {
	obj := newTestOverlay(t)

	fileLevel := detectResult(t, obj, map[string][]byte{
		"go.mod":    []byte("module example.com/foo\n"),
		"README.md": []byte("a"),
		"readme.MD": []byte("b"),
	})
	if !fileLevel.Detection.GoZipBlocked || !strings.Contains(fileLevel.Detection.GoZipBlockReason, "case-insensitive path collisions (1)") {
		t.Fatalf("file-level collision must block: %+v", fileLevel.Detection)
	}

	dirLevel := detectResult(t, obj, map[string][]byte{
		"go.mod":     []byte("module example.com/foo\n"),
		"docs/a.txt": []byte("a"),
		"Docs/b.txt": []byte("b"),
	})
	if !dirLevel.Detection.GoZipBlocked || !strings.Contains(dirLevel.Detection.GoZipBlockReason, "case-insensitive path collisions") {
		t.Fatalf("directory-level collision must block: %+v", dirLevel.Detection)
	}
}

// Symlinks block go-zip because gozip.Build rejects them before x/mod/zip.
func TestGoZipBlockedSymlink(t *testing.T) {
	obj := newTestOverlay(t)
	goModArr := []byte("module example.com/foo\n")
	linkArr := []byte("target.txt")
	reader := fakeBlobReaderObj{blobs: map[core.HashObj][]byte{
		core.HashBytes(goModArr): goModArr,
		core.HashBytes(linkArr):  linkArr,
	}}
	treeArr := []core.TreeEntryObj{
		{Path: "go.mod", Mode: core.ModeFile, SizeBytes: uint64(len(goModArr)), BlobHash: core.HashBytes(goModArr)},
		{Path: "link", Mode: core.ModeSymlink, SizeBytes: uint64(len(linkArr)), BlobHash: core.HashBytes(linkArr)},
	}
	sort.Slice(treeArr, func(i, j int) bool { return treeArr[i].Path < treeArr[j].Path })

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if !resultObj.Detection.GoZipBlocked || !strings.Contains(resultObj.Detection.GoZipBlockReason, "symlinks (1)") {
		t.Fatalf("symlink must block go zip: %+v", resultObj.Detection)
	}
}

// A clean tree is not blocked and stays go-publishable.
func TestGoZipViableCleanTree(t *testing.T) {
	obj := planTestOverlay(t, true)
	resultObj := detectResult(t, obj, map[string][]byte{
		"go.mod":       []byte("module example.com/foo\n\ngo 1.22\n"),
		"a.go":         []byte("package foo\n"),
		"testdata/x.j": []byte("{}"),
	})
	if resultObj.Detection.GoZipBlocked || resultObj.Detection.GoZipBlockReason != "" {
		t.Fatalf("clean tree must not be blocked: %+v", resultObj.Detection)
	}
	listenerObj := ListenerCtxObj{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"}
	if !obj.GoPublishable("foo", "v1.0.0", resultObj.Detection, resultObj.Go, listenerObj) {
		t.Fatal("clean go version must stay publishable")
	}
}

// modzip size limits block during detection: LICENSE > MaxLICENSE and total tree > MaxZipFile
// are reachable under default archive caps and used to cause endless heal/re-download loops.
// Sizes are synthetic, so blobs are not read.
func TestGoZipBlockedSizeClasses(t *testing.T) {
	obj := newTestOverlay(t)
	goModArr := []byte("module example.com/foo\n\ngo 1.22\n")

	makeTree := func(extraArr ...core.TreeEntryObj) ([]core.TreeEntryObj, fakeBlobReaderObj) {
		reader := fakeBlobReaderObj{blobs: map[core.HashObj][]byte{core.HashBytes(goModArr): goModArr}}
		treeArr := append([]core.TreeEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, SizeBytes: uint64(len(goModArr)), BlobHash: core.HashBytes(goModArr)},
		}, extraArr...)
		sort.Slice(treeArr, func(i, j int) bool { return treeArr[i].Path < treeArr[j].Path })
		return treeArr, reader
	}

	licenseTree, licenseReader := makeTree(core.TreeEntryObj{
		Path: "LICENSE", Mode: core.ModeFile, SizeBytes: modzip.MaxLICENSE + 1,
	})
	licenseResult, err := obj.Detect(context.Background(), licenseTree, licenseReader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	reason := licenseResult.Detection.GoZipBlockReason
	if !licenseResult.Detection.GoZipBlocked || !strings.Contains(reason, "oversized files (1)") || !strings.Contains(reason, "LICENSE") {
		t.Fatalf("oversized LICENSE must block: %+v", licenseResult.Detection)
	}

	totalTree, totalReader := makeTree(
		core.TreeEntryObj{Path: "big1.bin", Mode: core.ModeFile, SizeBytes: 300 << 20},
		core.TreeEntryObj{Path: "big2.bin", Mode: core.ModeFile, SizeBytes: 300 << 20},
	)
	totalResult, err := obj.Detect(context.Background(), totalTree, totalReader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if !totalResult.Detection.GoZipBlocked || !strings.Contains(totalResult.Detection.GoZipBlockReason, "too large") {
		t.Fatalf("tree over MaxZipFile must block: %+v", totalResult.Detection)
	}
}

// A hostile but formally legal tree is blocked by the complexity budget before CheckFiles.
// Without the budget, fold-collision checks would allocate gigabytes and run for 100+ seconds.
func TestGoZipBlockedComplexityBudget(t *testing.T) {
	goModArr := []byte("module example.com/foo\n\ngo 1.22\n")
	deepDir := strings.Repeat("DEEPLY/NESTED/UPPER/", 50)
	treeArr := make([]core.TreeEntryObj, 0, 20001)
	treeArr = append(treeArr, core.TreeEntryObj{
		Path: "go.mod", Mode: core.ModeFile, SizeBytes: uint64(len(goModArr)), BlobHash: core.HashBytes(goModArr),
	})
	for i := 0; i < 20000; i++ {
		treeArr = append(treeArr, core.TreeEntryObj{
			Path: fmt.Sprintf("%sF%05d.GO", deepDir, i), Mode: core.ModeFile, SizeBytes: 1,
		})
	}

	startAt := time.Now()
	reason := goZipBlockReason(treeArr, goModArr)
	elapsed := time.Since(startAt)
	if !strings.Contains(reason, "complexity budget") {
		t.Fatalf("hostile tree must hit the complexity budget, got %q", reason)
	}
	if elapsed > 2*time.Second {
		t.Fatalf("budget check must be near-instant, took %v", elapsed)
	}
}

// Entries omitted by modzip, such as vendored packages and submodule subtrees, do not block.
func TestGoZipOmittedEntriesDoNotBlock(t *testing.T) {
	obj := newTestOverlay(t)
	resultObj := detectResult(t, obj, map[string][]byte{
		"go.mod":                []byte("module example.com/foo\n\ngo 1.22\n"),
		"a.go":                  []byte("package foo\n"),
		"vendor/pkg/bad?.txt":   []byte("vendored"),
		"sub/go.mod":            []byte("module example.com/foo/sub\n"),
		"sub/testdata/bad?.txt": []byte("submodule"),
	})
	if resultObj.Detection.GoZipBlocked {
		t.Fatalf("omitted entries must not block: %q", resultObj.Detection.GoZipBlockReason)
	}
}
