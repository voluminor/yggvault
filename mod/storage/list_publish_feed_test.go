package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestListPublishFeed(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("a")}})
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("b")}})
	publishTestVersion(t, obj, "v2.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("c")}})

	feedArr, err := obj.ListPublishFeed(ctx, "", 10)
	if err != nil {
		t.Fatalf("ListPublishFeed: %v", err)
	}
	if len(feedArr) != 3 {
		t.Fatalf("got %d events, want 3: %+v", len(feedArr), feedArr)
	}
	if feedArr[0].Version != "v2.0.0" || !feedArr[0].FirstPublish {
		t.Fatalf("event[0]=%+v", feedArr[0])
	}
	if feedArr[1].Version != "v1.0.0" || feedArr[1].FirstPublish {
		t.Fatalf("event[1] (mutation) should not be first publish: %+v", feedArr[1])
	}
	if feedArr[2].Version != "v1.0.0" || !feedArr[2].FirstPublish {
		t.Fatalf("event[2] (original) should be first publish: %+v", feedArr[2])
	}
	if feedArr[0].TreeHash.IsZero() {
		t.Fatal("publish event missing tree_hash")
	}

	keyArr, err := obj.ListPublishFeed(ctx, "core-lib", 10)
	if err != nil || len(keyArr) != 3 {
		t.Fatalf("per-key feed: err=%v len=%d", err, len(keyArr))
	}
	if absent, _ := obj.ListPublishFeed(ctx, "nope", 10); len(absent) != 0 {
		t.Fatalf("unknown key feed not empty: %d", len(absent))
	}

	if oneArr, _ := obj.ListPublishFeed(ctx, "", 1); len(oneArr) != 1 || oneArr[0].Version != "v2.0.0" {
		t.Fatalf("limit=1: %+v", oneArr)
	}
}
