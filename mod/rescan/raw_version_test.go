package rescan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func TestRawVersionPublishesUniversalOnlyWithoutDetect(t *testing.T) {
	// The raw archive intentionally contains a valid Go module; Detect would make IsGo true.
	rawZip := buildZip(t, map[string]string{
		"core-lib-raw/go.mod":        "module example.com/core-lib\n\ngo 1.22\n",
		"core-lib-raw/a.go":          "package corelib\n",
		"core-lib-raw/composer.json": `{"name":"vendor/pkg"}`,
	})
	goZip := buildZip(t, map[string]string{
		"core-lib-1.0.0/go.mod": "module example.com/core-lib\n\ngo 1.22\n",
		"core-lib-1.0.0/a.go":   "package corelib\n",
	})
	// Newest-first listing: the raw version is newer than semver and must become latest by seq.
	fakeSrc := &fakeSourceObj{
		releaseArr: []source.GitReleaseObj{
			{Version: "INKSCAPE_1_3_2", BodyMD: "raw notes", ArchiveURL: "https://x/raw.zip", Format: "zip"},
			{Version: "v1.0.0", BodyMD: "go notes", ArchiveURL: "https://x/go.zip", Format: "zip"},
			{Version: "not storable name!", ArchiveURL: "https://x/bad.zip", Format: "zip"},
		},
		archiveByVersion: map[string][]byte{
			"INKSCAPE_1_3_2": rawZip,
			"v1.0.0":         goZip,
		},
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)

	// The raw version is published and release notes are stored.
	rawVersionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "INKSCAPE_1_3_2")
	if err != nil || !ok {
		t.Fatalf("GetVersion raw: ok=%v err=%v", ok, err)
	}
	if rawVersionObj.ReleaseNotes != "raw notes" {
		t.Fatalf("raw release notes=%q, want kept in releases mode", rawVersionObj.ReleaseNotes)
	}

	// Detection stays empty with raw_version evidence despite go.mod and composer.json in the tree.
	detectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "INKSCAPE_1_3_2")
	if err != nil || !ok {
		t.Fatalf("GetDetection raw: ok=%v err=%v", ok, err)
	}
	if detectionObj.IsGo || detectionObj.IsComposer || detectionObj.Conflict || detectionObj.GoZipBlocked {
		t.Fatalf("raw detection must be all-false (detect skipped): %+v", detectionObj)
	}
	if !strings.Contains(detectionObj.EvidenceJSON, `"raw_version":true`) {
		t.Fatalf("raw evidence=%q, want raw_version marker", detectionObj.EvidenceJSON)
	}

	// Raw-version artifacts are only universal zip and tar.gz.
	artifactArr, err := storageObj.ListArtifacts(ctx, "core-lib", "INKSCAPE_1_3_2")
	if err != nil {
		t.Fatalf("ListArtifacts raw: %v", err)
	}
	kindSet := make(map[string]bool, len(artifactArr))
	for i := range artifactArr {
		if artifactArr[i].MaterializerID != stcode.MaterializerUniversal.String() {
			t.Fatalf("raw version got non-universal artifact: %+v", artifactArr[i])
		}
		kindSet[artifactArr[i].ArtifactKind] = true
	}
	if !kindSet["zip"] || !kindSet["tar.gz"] {
		t.Fatalf("raw version must have universal zip+tar.gz, got %v", kindSet)
	}

	// Mixed key: the neighboring semver version is detected as Go normally.
	goDetectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok || !goDetectionObj.IsGo {
		t.Fatalf("semver version must still run detection: ok=%v det=%+v err=%v", ok, goDetectionObj, err)
	}

	// Latest by seq is the raw version, first in the listing.
	latestObj, ok, err := storageObj.LatestVersion(ctx, "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion: ok=%v err=%v", ok, err)
	}
	if latestObj.Version != "INKSCAPE_1_3_2" {
		t.Fatalf("latest=%q, want raw version by upstream seq", latestObj.Version)
	}

	// An unstorable name is dropped before download.
	if got := fakeSrc.fetchCount("not storable name!"); got != 0 {
		t.Fatalf("unstorable name fetched %d times, want 0", got)
	}
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "not storable name!"); ok {
		t.Fatal("unstorable name must not be published")
	}
}

// //

// brotherStand builds a key fixture with the supplied config for brother replication.
func brotherStand(t *testing.T, configObj *stconf.ConfigObj, fakeSrc *fakeSourceObj) (*Obj, *storage.Obj, context.Context) {
	t.Helper()
	ctx := context.Background()
	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storageObj.Close(closeCtx)
	})
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	return New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveFromConfig(t, configObj), ""), storageObj, ctx
}

func TestBrotherReplicatesRawVersionWithoutDetect(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}

	// The brother raw version has composer.json in the tree; the replica must still skip detection.
	sessionObj := &fakeBrotherSessionObj{
		notesByVersion: map[string]string{"blockly-v9.3.3": ""},
		blobByVersion:  map[string][]byte{"blockly-v9.3.3": []byte(`{"name":"vendor/pkg"}`)},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}
	obj, storageObj, ctx := brotherStand(t, configObj, fakeSrc)

	obj.RunOnce(ctx)

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "blockly-v9.3.3")
	if err != nil || !ok {
		t.Fatalf("GetVersion raw via brother: ok=%v err=%v", ok, err)
	}
	if versionObj.TreeHash.IsZero() {
		t.Fatal("replicated raw version has empty tree hash")
	}
	detectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "blockly-v9.3.3")
	if err != nil || !ok {
		t.Fatalf("GetDetection raw via brother: ok=%v err=%v", ok, err)
	}
	if detectionObj.IsComposer || detectionObj.IsGo {
		t.Fatalf("brother replica of a raw version must skip detect: %+v", detectionObj)
	}
	if !strings.Contains(detectionObj.EvidenceJSON, `"raw_version":true`) {
		t.Fatalf("raw evidence=%q, want raw_version marker", detectionObj.EvidenceJSON)
	}
}
