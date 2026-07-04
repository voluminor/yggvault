package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestPublishPersistsReleaseNotes(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)

	notes := "## v1.0.0\n\n- first release\n- bugfixes\n"
	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("src-v1")),
		SourceSizeBytes: 10,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte("module example.com/core\n")},
		},
		EventType:    "publish",
		ReleaseNotes: notes,
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}

	versionObj, ok, err := obj.GetVersion(context.Background(), "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion ok=%v err=%v", ok, err)
	}
	if versionObj.ReleaseNotes != notes {
		t.Fatalf("release notes mismatch: got %q want %q", versionObj.ReleaseNotes, notes)
	}

	listArr, err := obj.ListVersions(context.Background(), "core-lib", false)
	if err != nil || len(listArr) != 1 {
		t.Fatalf("ListVersions len=%d err=%v", len(listArr), err)
	}
	if listArr[0].ReleaseNotes != "" {
		t.Fatalf("ListVersions must omit release notes, got %q", listArr[0].ReleaseNotes)
	}

	pageArr, err := obj.ListVersionsPage(context.Background(), "core-lib", false, 10, 0)
	if err != nil || len(pageArr) != 1 {
		t.Fatalf("ListVersionsPage len=%d err=%v", len(pageArr), err)
	}
	if pageArr[0].ReleaseNotes != notes {
		t.Fatalf("ListVersionsPage release notes mismatch: got %q want %q", pageArr[0].ReleaseNotes, notes)
	}

	latestObj, ok, err := obj.LatestVersion(context.Background(), "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion ok=%v err=%v", ok, err)
	}
	if latestObj.ReleaseNotes != "" {
		t.Fatalf("LatestVersion must omit release notes, got %q", latestObj.ReleaseNotes)
	}
}

func TestPublishRejectsOversizeReleaseNotes(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)

	huge := strings.Repeat("x", (128*1024)+1)
	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("src-v1")),
		SourceSizeBytes: 10,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte("module example.com/core\n")},
		},
		EventType:    "publish",
		ReleaseNotes: huge,
	})
	if err == nil {
		t.Fatal("expected oversize release notes to be rejected")
	}
}
