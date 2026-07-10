package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// Display order follows source position (seq): v0.9.0 is declared newer and heads the list.
// latest is semver max to match go-proxy @latest, so latest=v1.0.0 rather than the seq head.
func TestUpstreamSeqOrderingAndLatest(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	publishTestVersionSeq(t, obj, "v1.0.0", 1, []core.InputEntryObj{{Path: "f.txt", Content: []byte("a")}})
	publishTestVersionSeq(t, obj, "v0.10.0", 2, []core.InputEntryObj{{Path: "f.txt", Content: []byte("b")}})
	publishTestVersionSeq(t, obj, "v0.9.0", 3, []core.InputEntryObj{{Path: "f.txt", Content: []byte("c")}})

	pageArr, err := obj.ListVersionsKeyset(ctx, "core-lib", false, 0, "", 10)
	if err != nil {
		t.Fatalf("ListVersionsKeyset returned error: %v", err)
	}
	wantArr := []string{"v0.9.0", "v0.10.0", "v1.0.0"}
	if len(pageArr) != len(wantArr) {
		t.Fatalf("got %d versions, want %d", len(pageArr), len(wantArr))
	}
	for i := range wantArr {
		if pageArr[i].Version != wantArr[i] {
			t.Fatalf("pos %d: got %q, want %q (upstream order broken)", i, pageArr[i].Version, wantArr[i])
		}
	}
	if pageArr[0].UpstreamSeq != 3 {
		t.Fatalf("top upstream_seq=%d, want 3", pageArr[0].UpstreamSeq)
	}

	latestObj, ok, err := obj.LatestVersion(ctx, "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion ok=%v err=%v", ok, err)
	}
	if latestObj.Version != "v1.0.0" {
		t.Fatalf("latest=%s, want v1.0.0 (semver-max, not max upstream_seq)", latestObj.Version)
	}

	if err = obj.MarkUpstreamDeleted(ctx, "core-lib", "v1.0.0"); err != nil {
		t.Fatalf("MarkUpstreamDeleted returned error: %v", err)
	}
	latestObj, ok, err = obj.LatestVersion(ctx, "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion after delete ok=%v err=%v", ok, err)
	}
	if latestObj.Version != "v0.10.0" {
		t.Fatalf("latest after delete=%s, want v0.10.0 (next semver)", latestObj.Version)
	}
}

// Fallback max+1 for publish without position, and position preservation on content mutation.
func TestUpstreamSeqFallbackAndMutationKeepsSeq(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("one")}})
	publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("two")}})

	maxSeq, err := obj.MaxUpstreamSeq(ctx, "core-lib")
	if err != nil {
		t.Fatalf("MaxUpstreamSeq returned error: %v", err)
	}
	if maxSeq != 2 {
		t.Fatalf("max upstream_seq=%d, want 2 (fallback max+1 per publish)", maxSeq)
	}

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("one-mutated")}})
	versionObj, ok, err := obj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamSeq != 1 {
		t.Fatalf("mutated version upstream_seq=%d, want 1 (position preserved)", versionObj.UpstreamSeq)
	}
	latestObj, ok, err := obj.LatestVersion(ctx, "core-lib")
	if err != nil || !ok || latestObj.Version != "v1.1.0" {
		t.Fatalf("latest=%q ok=%v err=%v, want v1.1.0", latestObj.Version, ok, err)
	}
}

// A key without semver versions uses top upstream_seq as latest, the only meaningful raw ordering.
func TestLatestRawOnlyUsesSeq(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	publishTestVersionSeq(t, obj, "INKSCAPE_1_3_0", 1, []core.InputEntryObj{{Path: "f.txt", Content: []byte("a")}})
	publishTestVersionSeq(t, obj, "INKSCAPE_1_3_2", 3, []core.InputEntryObj{{Path: "f.txt", Content: []byte("c")}})
	publishTestVersionSeq(t, obj, "INKSCAPE_1_3_1", 2, []core.InputEntryObj{{Path: "f.txt", Content: []byte("b")}})

	latestObj, ok, err := obj.LatestVersion(ctx, "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion ok=%v err=%v", ok, err)
	}
	if latestObj.Version != "INKSCAPE_1_3_2" {
		t.Fatalf("raw-only latest=%q, want INKSCAPE_1_3_2 (max upstream_seq)", latestObj.Version)
	}
}
