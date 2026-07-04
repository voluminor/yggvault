package overlay

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func goOverlayObj(t *testing.T, rewriteEnabled bool) *Obj {
	t.Helper()
	configObj := stconf.FullConfig()
	configObj.Overlay.Go.RewriteEnabled = rewriteEnabled
	configObj.Web.Server.Domain = "mirror.example"
	configObj.Web.Routing.Prefix = ""
	obj, err := New(configObj)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return obj
}

func TestGoPublishable(t *testing.T) {
	candidateMismatch := &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "upstream.example/foo"}
	candidateMatch := &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "mirror.example/foo"}
	goDetection := core.DetectionObj{IsGo: true}
	conflictDetection := core.DetectionObj{IsGo: true, IsComposer: true, Conflict: true}

	objOff := goOverlayObj(t, false)
	if objOff.GoPublishable("foo", "v1.0.0", goDetection, candidateMismatch, ListenerCtxObj{}) {
		t.Fatal("mismatch + rewrite_off must NOT be publishable")
	}
	if !objOff.GoPublishable("foo", "v1.0.0", goDetection, candidateMatch, ListenerCtxObj{}) {
		t.Fatal("path match must be publishable even with rewrite_off")
	}

	objOn := goOverlayObj(t, true)
	if !objOn.GoPublishable("foo", "v1.0.0", goDetection, candidateMismatch, ListenerCtxObj{}) {
		t.Fatal("mismatch + rewrite_on must be publishable")
	}
	if objOn.GoPublishable("foo", "v1.0.0", conflictDetection, candidateMismatch, ListenerCtxObj{}) {
		t.Fatal("conflict must NOT be publishable")
	}
}

func TestRenderGoModNoRewriteUnderConflict(t *testing.T) {
	st := buildStorage(map[string][]byte{
		"go.mod":        []byte("module upstream.example/foo\n"),
		"composer.json": []byte(`{"name":"vendor/pkg"}`),
		"main.go":       []byte("package main\n"),
	})
	obj := goOverlayObj(t, true)

	detectionObj, err := obj.Detect(context.Background(), st.entries, fakeBlobReaderObj{blobs: st.blobs})
	if err != nil {
		t.Fatalf("Detect: %v", err)
	}
	if !detectionObj.Detection.Conflict {
		t.Fatal("expected Go+Composer conflict")
	}

	dataArr, err := obj.RenderGoMod(context.Background(), st, "foo", "v1.0.0", st.treeHash, detectionObj.Detection, detectionObj.Go, ListenerCtxObj{})
	if err != nil {
		t.Fatalf("RenderGoMod: %v", err)
	}
	if !strings.Contains(string(dataArr), "upstream.example/foo") {
		t.Fatalf("conflict must not rewrite go.mod, got %q", dataArr)
	}
}

func TestUniversalBuilderRejectsBadKey(t *testing.T) {
	st := buildStorage(map[string][]byte{"go.mod": []byte("module example.com/x\n")})
	obj := newTestOverlay(t)
	builderObj := obj.UniversalBuilder(st, "bad/key", "v1.0.0", st.treeHash, archive.FormatZip)

	var bufferObj bytes.Buffer
	if err := builderObj.Build(context.Background(), &bufferObj); err == nil {
		t.Fatal("expected error for key containing a slash")
	}
}
