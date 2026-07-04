package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestGetDetectionAndRewriteSet(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)

	goModBlob := []byte("module example.com/core\n")
	rewriteHashObj := core.HashBytes(goModBlob)

	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("src")),
		SourceSizeBytes: 10,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: goModBlob},
		},
		Detection:    core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_mod":"go.mod"}`},
		RewriteBlobs: []core.HashObj{rewriteHashObj},
		EventType:    "publish",
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	detectionObj, ok, err := obj.GetDetection(context.Background(), "core-lib", "v1.0.0")
	if err != nil || !ok || !detectionObj.IsGo || detectionObj.IsComposer {
		t.Fatalf("GetDetection=%+v ok=%v err=%v", detectionObj, ok, err)
	}

	rewriteArr, err := obj.RewriteSet(context.Background(), "core-lib", "v1.0.0")
	if err != nil || len(rewriteArr) != 1 || rewriteArr[0] != rewriteHashObj {
		t.Fatalf("RewriteSet=%v err=%v", rewriteArr, err)
	}

	_, ok, err = obj.GetDetection(context.Background(), "core-lib", "v9.9.9")
	if err != nil || ok {
		t.Fatalf("GetDetection(absent) ok=%v err=%v", ok, err)
	}
}

// Go-zip block fields must survive publish-to-read and update through PutDetection.
func TestDetectionGoZipBlockedRoundTrip(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)
	ctx := context.Background()

	reasonText := `invalid file paths (1): "testdata/a?b.txt"`
	_, err := obj.Publish(ctx, core.PublishObj{
		Key:             "blocked-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("src")),
		SourceSizeBytes: 10,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte("module example.com/blocked\n")},
		},
		Detection: core.DetectionObj{IsGo: true, GoZipBlocked: true, GoZipBlockReason: reasonText},
		EventType: "publish",
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	detectionObj, ok, err := obj.GetDetection(ctx, "blocked-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetDetection ok=%v err=%v", ok, err)
	}
	if !detectionObj.GoZipBlocked || detectionObj.GoZipBlockReason != reasonText {
		t.Fatalf("blocked fields lost in round-trip: %+v", detectionObj)
	}

	detectionObj.GoZipBlocked = false
	detectionObj.GoZipBlockReason = ""
	if err := obj.PutDetection(ctx, "blocked-lib", "v1.0.0", detectionObj); err != nil {
		t.Fatalf("PutDetection returned error: %v", err)
	}
	detectionObj, ok, err = obj.GetDetection(ctx, "blocked-lib", "v1.0.0")
	if err != nil || !ok || detectionObj.GoZipBlocked || detectionObj.GoZipBlockReason != "" {
		t.Fatalf("PutDetection update lost: ok=%v err=%v %+v", ok, err, detectionObj)
	}

	// PutDetection for a missing version must fail on the FK.
	if err := obj.PutDetection(ctx, "blocked-lib", "v9.9.9", detectionObj); err == nil {
		t.Fatal("PutDetection for unknown version must fail on versions FK")
	}
}
