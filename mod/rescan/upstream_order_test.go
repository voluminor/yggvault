package rescan

import (
	"context"
	"errors"
	"testing"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type orderStandObj struct {
	storageObj *storage.Obj
	rescanObj  *Obj
}

func newOrderStand(t *testing.T, configObj *stconf.ConfigObj, fakeSrc *fakeSourceObj) orderStandObj {
	t.Helper()
	ctx := context.Background()
	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(context.Background()) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)
	return orderStandObj{
		storageObj: storageObj,
		rescanObj:  New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, ""),
	}
}

func requireVersionOrder(t *testing.T, storageObj *storage.Obj, key string, wantArr []string) {
	t.Helper()
	versionArr, err := storageObj.ListVersions(context.Background(), key, false)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versionArr) != len(wantArr) {
		t.Fatalf("got %d versions, want %d: %+v", len(versionArr), len(wantArr), versionArr)
	}
	for i := range wantArr {
		if versionArr[i].Version != wantArr[i] {
			t.Fatalf("pos %d: got %q, want %q (upstream listing order broken)", i, versionArr[i].Version, wantArr[i])
		}
	}
}

func requireLatest(t *testing.T, storageObj *storage.Obj, key string, want string) {
	t.Helper()
	latestObj, ok, err := storageObj.LatestVersion(context.Background(), key)
	if err != nil || !ok {
		t.Fatalf("LatestVersion ok=%v err=%v", ok, err)
	}
	if latestObj.Version != want {
		t.Fatalf("latest=%q, want %q", latestObj.Version, want)
	}
}

// // // // // // // // // //

// The source listing declares v0.9.0 newer than v0.10.0, so the display follows seq order.
// latest remains semver max like go-proxy @latest, so it is v0.10.0 rather than the seq head.
// The second cycle prepends a version while existing seq positions stay unchanged.
func TestIngestGitPreservesUpstreamListingOrder(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.Storage.Quota.RetainLatestPerKey = 5
	ctx := context.Background()

	archiveBytes := buildZip(t, map[string]string{"core-lib/README.md": "hello"})
	fakeSrc := &fakeSourceObj{
		releaseArr: []source.GitReleaseObj{
			{Version: "v0.9.0", ArchiveURL: "https://x/a.zip", Format: "zip"},
			{Version: "v0.10.0", ArchiveURL: "https://x/b.zip", Format: "zip"},
		},
		archiveBytes: archiveBytes,
	}
	standObj := newOrderStand(t, configObj, fakeSrc)
	standObj.rescanObj.RunOnce(ctx)

	requireVersionOrder(t, standObj.storageObj, "core-lib", []string{"v0.9.0", "v0.10.0"})
	requireLatest(t, standObj.storageObj, "core-lib", "v0.10.0")

	firstObj, ok, err := standObj.storageObj.GetVersion(ctx, "core-lib", "v0.9.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion v0.9.0: ok=%v err=%v", ok, err)
	}

	fakeSrc.releaseArr = []source.GitReleaseObj{
		{Version: "v0.9.5", ArchiveURL: "https://x/c.zip", Format: "zip"},
		{Version: "v0.9.0", ArchiveURL: "https://x/a.zip", Format: "zip"},
		{Version: "v0.10.0", ArchiveURL: "https://x/b.zip", Format: "zip"},
	}
	standObj.rescanObj.RunOnce(ctx)

	requireVersionOrder(t, standObj.storageObj, "core-lib", []string{"v0.9.5", "v0.9.0", "v0.10.0"})
	requireLatest(t, standObj.storageObj, "core-lib", "v0.10.0")

	secondObj, ok, err := standObj.storageObj.GetVersion(ctx, "core-lib", "v0.9.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion v0.9.0 after cycle 2: ok=%v err=%v", ok, err)
	}
	if secondObj.UpstreamSeq != firstObj.UpstreamSeq {
		t.Fatalf("existing version changed position: %d → %d", firstObj.UpstreamSeq, secondObj.UpstreamSeq)
	}
	newObj, ok, err := standObj.storageObj.GetVersion(ctx, "core-lib", "v0.9.5")
	if err != nil || !ok {
		t.Fatalf("GetVersion v0.9.5: ok=%v err=%v", ok, err)
	}
	if newObj.UpstreamSeq <= secondObj.UpstreamSeq {
		t.Fatalf("new top version seq %d must exceed existing %d", newObj.UpstreamSeq, secondObj.UpstreamSeq)
	}
}

// // // // // // // // // //

// A brother source provides seed positions, and the local display mirrors that order exactly.
func TestIngestBrotherMirrorsSeedOrder(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	configObj.Storage.Quota.RetainLatestPerKey = 5
	ctx := context.Background()

	sessionObj := &fakeBrotherSessionObj{
		indexEntries: []source.BrotherIndexEntryObj{
			{Version: "v0.9.0", ReleaseNotes: "n1", UpstreamSeq: 30},
			{Version: "v0.10.0", ReleaseNotes: "n2", UpstreamSeq: 20},
		},
		blobByVersion: map[string][]byte{
			"v0.9.0":  []byte(`{"name":"vendor/one"}`),
			"v0.10.0": []byte(`{"name":"vendor/two"}`),
		},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}
	standObj := newOrderStand(t, configObj, fakeSrc)
	standObj.rescanObj.RunOnce(ctx)

	requireVersionOrder(t, standObj.storageObj, "core-lib", []string{"v0.9.0", "v0.10.0"})
	requireLatest(t, standObj.storageObj, "core-lib", "v0.10.0")

	versionObj, ok, err := standObj.storageObj.GetVersion(ctx, "core-lib", "v0.9.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamSeq != 30 {
		t.Fatalf("seed position not mirrored: seq=%d, want 30", versionObj.UpstreamSeq)
	}
}

// A legacy brother without positions (seq=0) gets local newest-first assignment from index order.
func TestIngestBrotherFallbackAssignsByIndexOrder(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	configObj.Storage.Quota.RetainLatestPerKey = 5
	ctx := context.Background()

	sessionObj := &fakeBrotherSessionObj{
		indexEntries: []source.BrotherIndexEntryObj{
			{Version: "v0.9.0", ReleaseNotes: "n1"},
			{Version: "v0.10.0", ReleaseNotes: "n2"},
		},
		blobByVersion: map[string][]byte{
			"v0.9.0":  []byte(`{"name":"vendor/one"}`),
			"v0.10.0": []byte(`{"name":"vendor/two"}`),
		},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}
	standObj := newOrderStand(t, configObj, fakeSrc)
	standObj.rescanObj.RunOnce(ctx)

	requireVersionOrder(t, standObj.storageObj, "core-lib", []string{"v0.9.0", "v0.10.0"})
	requireLatest(t, standObj.storageObj, "core-lib", "v0.10.0")
}

// // // // // // // // // //
