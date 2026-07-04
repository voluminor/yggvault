package rescan

import (
	"context"
	"testing"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
)

// // // // // // // // // //

func TestFinishCycleComposerAggregateAndChecksums(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{
		"lib-a": "https://up.example/lib-a/",
		"lib-b": "https://up.example/lib-b/",
		"lib-c": "https://up.example/lib-c/",
	}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	composerZip := func(name string) []byte {
		return buildZip(t, map[string]string{"pkg-1.0.0/composer.json": `{"name":"` + name + `"}`})
	}
	fakeSrc := &fakeSourceObj{
		releaseArr: []source.GitReleaseObj{{Version: "v1.0.0", Format: "zip", ArchiveURL: "https://x/a.zip"}},
		archiveByKey: map[string][]byte{
			"lib-a": composerZip("vendor/dup"),
			"lib-b": composerZip("vendor/dup"),
			"lib-c": composerZip("vendor/uniq"),
		},
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)

	namesArr := obj.ComposerPackageNames()
	if len(namesArr) != 2 || namesArr[0] != "vendor/dup" || namesArr[1] != "vendor/uniq" {
		t.Fatalf("composer names=%v want [vendor/dup vendor/uniq]", namesArr)
	}

	foundCollision := false
	for _, diagObj := range stateObj.ActiveDiagnostics() {
		if diagObj.Code == "composer_name_collision" {
			foundCollision = true
		}
	}
	if !foundCollision {
		t.Fatalf("expected composer_name_collision diagnostic, got %+v", stateObj.ActiveDiagnostics())
	}

	checksumObj := stateObj.Checksums()
	if checksumObj.Content.IsZero() {
		t.Fatalf("content checksum not set: %+v", checksumObj)
	}
}
