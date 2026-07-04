package overlay

import (
	"context"
	"fmt"
	"sort"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type fakeBlobReaderObj struct {
	blobs map[core.HashObj][]byte
}

func (f fakeBlobReaderObj) ReadBlob(_ context.Context, hashObj core.HashObj) ([]byte, error) {
	dataArr, ok := f.blobs[hashObj]
	if !ok {
		return nil, fmt.Errorf("missing blob %s", hashObj.Hex())
	}
	return dataArr, nil
}

func buildTree(files map[string][]byte) ([]core.TreeEntryObj, fakeBlobReaderObj) {
	reader := fakeBlobReaderObj{blobs: make(map[core.HashObj][]byte)}
	treeArr := make([]core.TreeEntryObj, 0, len(files))
	for pathText, content := range files {
		hashObj := core.HashBytes(content)
		reader.blobs[hashObj] = content
		treeArr = append(treeArr, core.TreeEntryObj{
			Path:      pathText,
			Mode:      core.ModeFile,
			SizeBytes: uint64(len(content)),
			BlobHash:  hashObj,
		})
	}
	sort.Slice(treeArr, func(i, j int) bool { return treeArr[i].Path < treeArr[j].Path })
	return treeArr, reader
}

func newTestOverlay(t *testing.T) *Obj {
	t.Helper()
	obj, err := New(stconf.FullConfig())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return obj
}

// // // // // // // // // //

func TestDetectGoWithRewriteSet(t *testing.T) {
	files := map[string][]byte{
		"go.mod":    []byte("module example.com/foo\n\ngo 1.22\n"),
		"a.go":      []byte("package foo\nimport \"example.com/foo/bar\"\n"),
		"b.go":      []byte("package foo\n// no upstream path here\n"),
		"README.md": []byte("example.com/foo mentioned but not a .go file"),
	}
	treeArr, reader := buildTree(files)
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if !resultObj.Detection.IsGo || resultObj.Detection.IsComposer || resultObj.Detection.Conflict {
		t.Fatalf("unexpected detection: %+v", resultObj.Detection)
	}
	if resultObj.Go == nil || resultObj.Go.GoModulePath != "example.com/foo" {
		t.Fatalf("unexpected go candidate: %+v", resultObj.Go)
	}
	wantSet := map[core.HashObj]struct{}{
		core.HashBytes(files["go.mod"]): {},
		core.HashBytes(files["a.go"]):   {},
	}
	if len(resultObj.RewriteBlobs) != len(wantSet) {
		t.Fatalf("rewrite blobs=%d, want %d", len(resultObj.RewriteBlobs), len(wantSet))
	}
	for _, hashObj := range resultObj.RewriteBlobs {
		if _, ok := wantSet[hashObj]; !ok {
			t.Fatalf("unexpected rewrite blob %s", hashObj.Hex())
		}
	}
}

// go.mod only in a subdirectory is a submodule, not a Go module: x/mod/zip would drop its subtree.
func TestDetectGoIgnoresNestedGoMod(t *testing.T) {
	treeArr, reader := buildTree(map[string][]byte{
		"go/go.mod":  []byte("module example.com/foo/go\n\ngo 1.22\n"),
		"go/a.go":    []byte("package foo\n"),
		"Makefile":   []byte("all:\n"),
		"README.md":  []byte("monorepo"),
		"src/main.c": []byte("int main(void){return 0;}\n"),
	})
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if resultObj.Detection.IsGo || resultObj.Go != nil {
		t.Fatalf("nested go.mod must not detect Go: %+v", resultObj.Detection)
	}
}

func TestDetectComposer(t *testing.T) {
	treeArr, reader := buildTree(map[string][]byte{
		"composer.json": []byte(`{"name":"vendor/pkg","type":"library"}`),
		"src/Main.php":  []byte("<?php\n"),
	})
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if resultObj.Detection.IsGo || !resultObj.Detection.IsComposer || resultObj.Detection.Conflict {
		t.Fatalf("unexpected detection: %+v", resultObj.Detection)
	}
	if resultObj.Composer == nil || resultObj.Composer.ComposerName != "vendor/pkg" {
		t.Fatalf("unexpected composer candidate: %+v", resultObj.Composer)
	}
}

func TestDetectConflictDisablesRewrite(t *testing.T) {
	treeArr, reader := buildTree(map[string][]byte{
		"go.mod":        []byte("module example.com/foo\n"),
		"main.go":       []byte("package main\nimport \"example.com/foo\"\n"),
		"composer.json": []byte(`{"name":"vendor/pkg"}`),
	})
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if !resultObj.Detection.Conflict || !resultObj.Detection.IsGo || !resultObj.Detection.IsComposer {
		t.Fatalf("expected conflict, got %+v", resultObj.Detection)
	}
	if resultObj.RewriteBlobs != nil {
		t.Fatalf("conflict must disable rewrite, got %d blobs", len(resultObj.RewriteBlobs))
	}
}

func TestDetectRejectsMaliciousComposerName(t *testing.T) {
	treeArr, reader := buildTree(map[string][]byte{
		"composer.json": []byte(`{"name":"../../etc/passwd"}`),
	})
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if resultObj.Detection.IsComposer {
		t.Fatal("malicious composer name must not be detected")
	}
}

func TestRewriteContentBoundary(t *testing.T) {
	oldArr := []byte("example.com/foo")
	newArr := []byte("mirror.example/foo")
	cases := []struct {
		name string
		in   string
		want string
	}{
		{"exact in quotes", `import "example.com/foo"`, `import "mirror.example/foo"`},
		{"subpackage", `import "example.com/foo/bar"`, `import "mirror.example/foo/bar"`},
		{"module directive", "module example.com/foo\n", "module mirror.example/foo\n"},
		{"sibling prefix untouched", `import "example.com/foobar"`, `import "example.com/foobar"`},
		{"left path-byte untouched", `import "x/example.com/foo"`, `import "x/example.com/foo"`},
	}
	for _, c := range cases {
		if got := string(rewriteContent([]byte(c.in), oldArr, newArr)); got != c.want {
			t.Errorf("%s: rewriteContent=%q want %q", c.name, got, c.want)
		}
	}
}

func TestDetectRewriteSetExcludesSiblingPrefix(t *testing.T) {
	files := map[string][]byte{
		"go.mod": []byte("module example.com/foo\n\ngo 1.22\n"),
		"a.go":   []byte("package foo\nimport \"example.com/foo/bar\"\n"),
		"sib.go": []byte("package foo\nimport \"example.com/foobar/baz\"\n"),
	}
	treeArr, reader := buildTree(files)
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	inSet := make(map[core.HashObj]struct{}, len(resultObj.RewriteBlobs))
	for _, hashObj := range resultObj.RewriteBlobs {
		inSet[hashObj] = struct{}{}
	}
	if _, ok := inSet[core.HashBytes(files["sib.go"])]; ok {
		t.Fatal("sibling-prefix-only file must not be in rewrite set")
	}
	if _, ok := inSet[core.HashBytes(files["a.go"])]; !ok {
		t.Fatal("subpackage import must be in rewrite set")
	}
	if _, ok := inSet[core.HashBytes(files["go.mod"])]; !ok {
		t.Fatal("go.mod must be in rewrite set")
	}
}

func TestDetectNeither(t *testing.T) {
	treeArr, reader := buildTree(map[string][]byte{
		"README.md": []byte("just docs"),
		"LICENSE":   []byte("MIT"),
	})
	obj := newTestOverlay(t)

	resultObj, err := obj.Detect(context.Background(), treeArr, reader)
	if err != nil {
		t.Fatalf("Detect returned error: %v", err)
	}
	if resultObj.Detection.IsGo || resultObj.Detection.IsComposer || resultObj.Detection.Conflict {
		t.Fatalf("expected no detection, got %+v", resultObj.Detection)
	}
}
