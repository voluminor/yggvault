package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestListArtifacts(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	artifactArr := []core.ArtifactObj{
		{MaterializerID: "universal", ArtifactKind: "zip", ListenerID: "global", BodyHash: core.HashBytes([]byte("a")), SizeBytes: 1, ETag: `"a"`},
		{MaterializerID: "universal", ArtifactKind: "tar.gz", ListenerID: "global", BodyHash: core.HashBytes([]byte("b")), SizeBytes: 2, ETag: `"b"`},
	}
	if _, err := obj.Publish(ctx, core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("s")),
		SourceSizeBytes: 1,
		Entries:         []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}},
		Artifacts:       artifactArr,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	gotArr, err := obj.ListArtifacts(ctx, "core-lib", "v1.0.0")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	if len(gotArr) != 2 {
		t.Fatalf("got %d artifacts, want 2", len(gotArr))
	}
	kindSet := map[string]bool{}
	for i := range gotArr {
		kindSet[gotArr[i].ArtifactKind] = true
		if gotArr[i].Key != "core-lib" || gotArr[i].Version != "v1.0.0" || gotArr[i].ETag == "" {
			t.Fatalf("unexpected artifact row: %+v", gotArr[i])
		}
	}
	if !kindSet["zip"] || !kindSet["tar.gz"] {
		t.Fatalf("kinds=%v", kindSet)
	}

	noneArr, err := obj.ListArtifacts(ctx, "core-lib", "v9.9.9")
	if err != nil || len(noneArr) != 0 {
		t.Fatalf("absent version: err=%v len=%d", err, len(noneArr))
	}
}
