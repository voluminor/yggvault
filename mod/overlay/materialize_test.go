package overlay

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type fakeStorageObj struct {
	treeHash core.HashObj
	entries  []core.TreeEntryObj
	blobs    map[core.HashObj][]byte
}

func (f fakeStorageObj) ReadTree(_ context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	if treeHashObj != f.treeHash {
		return nil, fmt.Errorf("unknown tree %s", treeHashObj.Hex())
	}
	return f.entries, nil
}

func (f fakeStorageObj) ReadBlob(_ context.Context, hashObj core.HashObj) ([]byte, error) {
	dataArr, ok := f.blobs[hashObj]
	if !ok {
		return nil, fmt.Errorf("missing blob %s", hashObj.Hex())
	}
	return dataArr, nil
}

func buildStorage(files map[string][]byte) fakeStorageObj {
	entriesArr, reader := buildTree(files)
	return fakeStorageObj{
		treeHash: core.HashBytes([]byte("tree-marker")),
		entries:  entriesArr,
		blobs:    reader.blobs,
	}
}

func readZipNames(t *testing.T, dataArr []byte) []string {
	t.Helper()
	zrObj, err := zip.NewReader(bytes.NewReader(dataArr), int64(len(dataArr)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	namesArr := make([]string, 0, len(zrObj.File))
	for _, fileObj := range zrObj.File {
		namesArr = append(namesArr, fileObj.Name)
	}
	return namesArr
}

func readZipFile(t *testing.T, dataArr []byte, name string) []byte {
	t.Helper()
	zrObj, err := zip.NewReader(bytes.NewReader(dataArr), int64(len(dataArr)))
	if err != nil {
		t.Fatalf("zip.NewReader: %v", err)
	}
	for _, fileObj := range zrObj.File {
		if fileObj.Name != name {
			continue
		}
		rc, err := fileObj.Open()
		if err != nil {
			t.Fatalf("open %s: %v", name, err)
		}
		defer rc.Close()
		var bufferObj bytes.Buffer
		if _, err := bufferObj.ReadFrom(rc); err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		return bufferObj.Bytes()
	}
	t.Fatalf("entry %s not found", name)
	return nil
}

// // // // // // // // // //

func TestUniversalBuilderDeterministicWithTopDir(t *testing.T) {
	st := buildStorage(map[string][]byte{
		"go.mod":  []byte("module example.com/x\n"),
		"main.go": []byte("package main\n"),
	})
	obj := newTestOverlay(t)
	builderObj := obj.UniversalBuilder(st, "mykey", "v1.0.0", st.treeHash, archive.FormatZip)

	var buf1, buf2 bytes.Buffer
	if err := builderObj.Build(context.Background(), &buf1); err != nil {
		t.Fatalf("build 1: %v", err)
	}
	if err := builderObj.Build(context.Background(), &buf2); err != nil {
		t.Fatalf("build 2: %v", err)
	}
	if !bytes.Equal(buf1.Bytes(), buf2.Bytes()) {
		t.Fatal("universal archive is not byte-deterministic")
	}
	for _, name := range readZipNames(t, buf1.Bytes()) {
		if !strings.HasPrefix(name, "mykey-v1.0.0/") {
			t.Fatalf("entry %q missing top-dir prefix", name)
		}
	}
}

func TestGoModuleZipRewritesImports(t *testing.T) {
	st := buildStorage(map[string][]byte{
		"go.mod": []byte("module upstream.example/foo\n\ngo 1.22\n"),
		"foo.go": []byte("package foo\n\n// ref upstream.example/foo here\n"),
	})
	configObj := stconf.FullConfig()
	configObj.Overlay.Go.RewriteEnabled = true
	configObj.Web.Server.Domain = "mirror.example"
	configObj.Web.Routing.Prefix = ""
	obj, err := New(configObj)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	detectionObj, err := obj.Detect(context.Background(), st.entries, fakeBlobReaderObj{blobs: st.blobs})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !detectionObj.Detection.IsGo {
		t.Fatal("expected Go detection")
	}

	builderObj := obj.GoModuleZipBuilder(st, "foo", "v1.0.0", st.treeHash, detectionObj.Detection, detectionObj.Go, detectionObj.RewriteBlobs, ListenerCtxObj{})
	var bufferObj bytes.Buffer
	if err := builderObj.Build(context.Background(), &bufferObj); err != nil {
		t.Fatalf("build go zip: %v", err)
	}

	goModName := "mirror.example/foo@v1.0.0/go.mod"
	goModArr := readZipFile(t, bufferObj.Bytes(), goModName)
	if !strings.Contains(string(goModArr), "module mirror.example/foo") {
		t.Fatalf("go.mod not rewritten: %q", goModArr)
	}
	if strings.Contains(string(goModArr), "upstream.example/foo") {
		t.Fatalf("upstream path leaked into go.mod: %q", goModArr)
	}
	fooArr := readZipFile(t, bufferObj.Bytes(), "mirror.example/foo@v1.0.0/foo.go")
	if strings.Contains(string(fooArr), "upstream.example/foo") {
		t.Fatalf("upstream path leaked into foo.go: %q", fooArr)
	}
}

// TestUniversalArtifactRawForGoModule guards that the universal artifact stays raw for a rewrite-eligible Go module.
func TestUniversalArtifactRawForGoModule(t *testing.T) {
	st := buildStorage(map[string][]byte{
		"go.mod": []byte("module upstream.example/foo\n\ngo 1.22\n"),
		"foo.go": []byte("package foo\n\n// ref upstream.example/foo here\n"),
	})
	configObj := stconf.FullConfig()
	configObj.Overlay.Go.RewriteEnabled = true
	configObj.Web.Server.Domain = "mirror.example"
	configObj.Web.Routing.Prefix = ""
	obj, err := New(configObj)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	detectionObj := core.DetectionObj{IsGo: true}
	candidateObj := &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "upstream.example/foo"}
	listenerArr := []ListenerCtxObj{{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"}}

	planArr := obj.ArtifactPlan(st, "foo", "v1.0.0", st.treeHash, detectionObj, candidateObj, nil, listenerArr)

	built := false
	for i := range planArr {
		planObj := planArr[i]
		if planObj.MaterializerID != stcode.MaterializerUniversal || planObj.ArtifactKind != archive.FormatZip {
			continue
		}
		built = true
		var bufferObj bytes.Buffer
		if err := planObj.Builder.Build(context.Background(), &bufferObj); err != nil {
			t.Fatalf("build universal zip: %v", err)
		}
		for _, name := range readZipNames(t, bufferObj.Bytes()) {
			if !strings.HasPrefix(name, "foo-v1.0.0/") {
				t.Fatalf("universal entry %q is not under raw top-dir foo-v1.0.0/", name)
			}
		}
		goModArr := readZipFile(t, bufferObj.Bytes(), "foo-v1.0.0/go.mod")
		if !strings.Contains(string(goModArr), "module upstream.example/foo") {
			t.Fatalf("universal go.mod must stay raw, got: %q", goModArr)
		}
		if strings.Contains(string(goModArr), "mirror.example") {
			t.Fatalf("universal go.mod was rewritten to the vault host: %q", goModArr)
		}
	}
	if !built {
		t.Fatal("plan has no universal/zip artifact")
	}
}
