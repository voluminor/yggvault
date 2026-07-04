package rescan

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage"
)

// // // // // // // // // //

func TestSpoolSourceReadBlobFallsBackToStorage(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })

	// The blob exists only in the spool map, so storage is not touched.
	stagedArr := []byte("staged content")
	stagedHashObj := core.HashBytes(stagedArr)
	stagedPath := filepath.Join(t.TempDir(), "staged.blob")
	if err := os.WriteFile(stagedPath, stagedArr, 0o600); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	srcObj := newSpoolSource(nil, []core.StagedBlobObj{
		{BlobHash: stagedHashObj, SizeBytes: uint64(len(stagedArr)), FilePath: stagedPath},
	}, storageObj)

	gotArr, err := srcObj.ReadBlob(ctx, stagedHashObj)
	if err != nil || string(gotArr) != string(stagedArr) {
		t.Fatalf("spool read: got=%q err=%v", gotArr, err)
	}
}

func TestSpoolSourceReadBlobDoubleMissErrors(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })

	srcObj := newSpoolSource(nil, nil, storageObj)
	missingHashObj := core.HashBytes([]byte("never stored anywhere"))
	if _, err := srcObj.ReadBlob(ctx, missingHashObj); err == nil {
		t.Fatal("blob absent from both spool and storage must error")
	} else if !strings.Contains(err.Error(), "not found in spool") {
		t.Fatalf("unexpected error text: %v", err)
	}
}
