package storage

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestListVersionsPage(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()

	for _, v := range []string{"v1.0.0", "v1.1.0", "v2.0.0"} {
		publishTestVersion(t, obj, v, []core.InputEntryObj{{Path: "f.txt", Content: []byte(v)}})
	}

	page1, err := obj.ListVersionsPage(ctx, "core-lib", false, 2, 0)
	if err != nil || len(page1) != 2 || page1[0].Version != "v2.0.0" || page1[1].Version != "v1.1.0" {
		t.Fatalf("page1=%+v err=%v", page1, err)
	}
	page2, err := obj.ListVersionsPage(ctx, "core-lib", false, 2, 2)
	if err != nil || len(page2) != 1 || page2[0].Version != "v1.0.0" {
		t.Fatalf("page2=%+v err=%v", page2, err)
	}
	if page3, _ := obj.ListVersionsPage(ctx, "core-lib", false, 2, 10); len(page3) != 0 {
		t.Fatalf("out-of-range page not empty: %d", len(page3))
	}
	if _, err := obj.ListVersionsPage(ctx, "Bad KEY!", false, 2, 0); err == nil {
		t.Fatal("invalid key should error")
	}
}

func TestValidateRejectsWindowsReservedPathBackedIDs(t *testing.T) {
	if err := validateKey("con"); err == nil {
		t.Fatal("validateKey accepted Windows reserved key")
	}
	if err := validateKey("nul.txt"); err == nil {
		t.Fatal("validateKey accepted Windows reserved key with extension")
	}
	_, err := validateArtifactKey(core.ArtifactKeyObj{
		MaterializerID: "com1",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
	})
	if err == nil {
		t.Fatal("validateArtifactKey accepted Windows reserved materializer_id")
	}
}
