package storage

import (
	"context"
	"errors"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestPublishRawVersionRoundTrip(t *testing.T) {
	ctx := context.Background()
	obj := newTestObj(t, newTestConfigObj(t))

	entriesArr := []core.InputEntryObj{{Path: "README.md", Mode: core.ModeFile, Content: []byte("raw content")}}
	resultObj, err := obj.Publish(ctx, core.PublishObj{
		Key:             "core-lib",
		Version:         "INKSCAPE_1_3_2",
		SourceHash:      core.HashBytes([]byte("raw-source")),
		SourceSizeBytes: 11,
		Entries:         entriesArr,
		Detection:       core.DetectionObj{EvidenceJSON: `{"raw_version":true}`},
		EventType:       "publish",
	})
	if err != nil {
		t.Fatalf("Publish raw version: %v", err)
	}
	if !resultObj.Published || resultObj.TreeHash.IsZero() {
		t.Fatalf("raw publish result: %+v", resultObj)
	}

	versionObj, ok, err := obj.GetVersion(ctx, "core-lib", "INKSCAPE_1_3_2")
	if err != nil || !ok {
		t.Fatalf("GetVersion raw: ok=%v err=%v", ok, err)
	}
	if versionObj.Version != "INKSCAPE_1_3_2" || versionObj.TreeHash != resultObj.TreeHash {
		t.Fatalf("raw round-trip mismatch: %+v", versionObj)
	}
	detectionObj, ok, err := obj.GetDetection(ctx, "core-lib", "INKSCAPE_1_3_2")
	if err != nil || !ok || detectionObj.IsGo || detectionObj.IsComposer {
		t.Fatalf("raw detection round-trip: ok=%v det=%+v err=%v", ok, detectionObj, err)
	}
}

func TestPublishRejectsUnstorableVersionNames(t *testing.T) {
	ctx := context.Background()
	obj := newTestObj(t, newTestConfigObj(t))

	entriesArr := []core.InputEntryObj{{Path: "a.txt", Mode: core.ModeFile, Content: []byte("x")}}
	// "v2" is absent: x/mod/semver accepts major-only names, which stay on the semver path.
	for _, badVersion := range []string{"foo.zip", "list", "latest", "-bad", "bad-", "pathé", "has space"} {
		_, err := obj.Publish(ctx, core.PublishObj{
			Key:             "core-lib",
			Version:         badVersion,
			SourceHash:      core.HashBytes([]byte("s")),
			SourceSizeBytes: 1,
			Entries:         entriesArr,
			EventType:       "publish",
		})
		if !errors.Is(err, ErrInvalidRef) {
			t.Fatalf("Publish(%q) err=%v, want ErrInvalidRef", badVersion, err)
		}
	}
}

// // // // // // // // // //

func TestKeyListingModeSetGetAndPutPreserves(t *testing.T) {
	ctx := context.Background()
	obj := newTestObj(t, newTestConfigObj(t))

	baseObj := core.KeySourceObj{Key: "core-lib", URL: "https://upstream.example/core-lib/", Class: "git"}
	if err := obj.PutKeySource(ctx, baseObj); err != nil {
		t.Fatalf("PutKeySource: %v", err)
	}
	gotObj, ok, err := obj.GetKeySource(ctx, "core-lib")
	if err != nil || !ok || gotObj.ListingMode != core.ListingModeUndecided {
		t.Fatalf("fresh binding mode=%q ok=%v err=%v, want undecided", gotObj.ListingMode, ok, err)
	}

	if err := obj.SetKeyListingMode(ctx, "core-lib", core.ListingModeTags); err != nil {
		t.Fatalf("SetKeyListingMode: %v", err)
	}
	gotObj, ok, err = obj.GetKeySource(ctx, "core-lib")
	if err != nil || !ok || gotObj.ListingMode != core.ListingModeTags {
		t.Fatalf("mode after set=%q ok=%v err=%v, want tags", gotObj.ListingMode, ok, err)
	}

	// Repeated Put (reclassification) must not erase sticky mode.
	updatedObj := baseObj
	updatedObj.OriginURL = "https://origin.example/core-lib/"
	if err := obj.PutKeySource(ctx, updatedObj); err != nil {
		t.Fatalf("PutKeySource update: %v", err)
	}
	gotObj, ok, err = obj.GetKeySource(ctx, "core-lib")
	if err != nil || !ok || gotObj.ListingMode != core.ListingModeTags {
		t.Fatalf("mode after re-put=%q ok=%v err=%v, want tags preserved", gotObj.ListingMode, ok, err)
	}

	if err := obj.SetKeyListingMode(ctx, "core-lib", "bogus"); !errors.Is(err, ErrInvalidRef) {
		t.Fatalf("SetKeyListingMode(bogus) err=%v, want ErrInvalidRef", err)
	}
}
