package storage

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// Round-trip upstream_ref and verified_ts through publish, GetVersion, and ListVersions.
func TestUpstreamRefVerifiedRoundTrip(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	refText := strings.Repeat("ab", 20)
	verifiedTS := time.Now().UTC()
	resultObj, err := obj.Publish(ctx, core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source-v1.0.0")),
		SourceSizeBytes: 123,
		UpstreamRef:     refText,
		VerifiedTS:      verifiedTS,
		Entries:         []core.InputEntryObj{{Path: "f.txt", Content: []byte("one")}},
		EventType:       "publish",
	})
	if err != nil || !resultObj.Published {
		t.Fatalf("Publish: published=%v err=%v", resultObj.Published, err)
	}

	versionObj, ok, err := obj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamRef != refText {
		t.Fatalf("upstream_ref=%q, want %q", versionObj.UpstreamRef, refText)
	}
	if core.FormatTime(versionObj.VerifiedTS) != core.FormatTime(verifiedTS) {
		t.Fatalf("verified_ts=%v, want %v", versionObj.VerifiedTS, verifiedTS)
	}

	publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("two")}})
	listArr, err := obj.ListVersions(ctx, "core-lib", false)
	if err != nil || len(listArr) != 2 {
		t.Fatalf("ListVersions: n=%d err=%v", len(listArr), err)
	}
	for i := range listArr {
		if listArr[i].Version != "v1.1.0" {
			continue
		}
		if listArr[i].UpstreamRef != "" || !listArr[i].VerifiedTS.IsZero() {
			t.Fatalf("v1.1.0 ref=%q verified=%v, want empty/zero", listArr[i].UpstreamRef, listArr[i].VerifiedTS)
		}
	}
}

// TouchVersionVerified covers ref adoption without timestamp, timestamp without ref change, and invalid ref rejection.
func TestTouchVersionVerified(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("one")}})

	refText := strings.Repeat("cd", 20)
	if err := obj.TouchVersionVerified(ctx, "core-lib", "v1.0.0", time.Time{}, refText); err != nil {
		t.Fatalf("adopt ref: %v", err)
	}
	versionObj, _, err := obj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if versionObj.UpstreamRef != refText {
		t.Fatalf("upstream_ref=%q, want %q", versionObj.UpstreamRef, refText)
	}
	if !versionObj.VerifiedTS.IsZero() {
		t.Fatalf("verified_ts=%v, want zero (adoption must not stamp verification)", versionObj.VerifiedTS)
	}

	verifiedTS := time.Now().UTC()
	if err = obj.TouchVersionVerified(ctx, "core-lib", "v1.0.0", verifiedTS, ""); err != nil {
		t.Fatalf("stamp verified: %v", err)
	}
	versionObj, _, err = obj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil {
		t.Fatalf("GetVersion: %v", err)
	}
	if versionObj.UpstreamRef != refText {
		t.Fatalf("upstream_ref=%q, want preserved %q", versionObj.UpstreamRef, refText)
	}
	if core.FormatTime(versionObj.VerifiedTS) != core.FormatTime(verifiedTS) {
		t.Fatalf("verified_ts=%v, want %v", versionObj.VerifiedTS, verifiedTS)
	}

	if err = obj.TouchVersionVerified(ctx, "core-lib", "v1.0.0", verifiedTS, "NOT-A-SHA"); err == nil {
		t.Fatal("garbage upstream ref must be rejected")
	}
}
