package pebblestore

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/cockroachdb/pebble/v2"
	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestObjectKeyUsesTaggedHash24(t *testing.T) {
	hashObj := core.HashBytes([]byte("key-contract"))
	keyObj := blobKey(hashObj)

	if len(keyObj) != cKeySize {
		t.Fatalf("key length=%d, want %d", len(keyObj), cKeySize)
	}
	if keyObj[0] != cBlobTag {
		t.Fatalf("key tag=%q, want %q", keyObj[0], cBlobTag)
	}
	if !bytes.Equal(keyObj[1:], hashObj[:]) {
		t.Fatal("key payload does not contain the storage hash")
	}
	parsedObj, ok := objectHashFromKey(cObjectKindBlob, keyObj[:])
	if !ok {
		t.Fatal("objectHashFromKey rejected a valid blob key")
	}
	if parsedObj != hashObj {
		t.Fatal("objectHashFromKey returned a different hash")
	}
	if _, ok = objectHashFromKey(cObjectKindTree, keyObj[:]); ok {
		t.Fatal("objectHashFromKey accepted a blob key as tree key")
	}
	longKeyArr := append([]byte(nil), keyObj[:]...)
	longKeyArr = append(longKeyArr, 0)
	if _, ok = objectHashFromKey(cObjectKindBlob, longKeyArr); ok {
		t.Fatal("objectHashFromKey accepted wrong key size")
	}
}

func TestMissingBlobs(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = storeObj.Close() })

	presentArr := [][]byte{[]byte("alpha"), []byte("beta")}
	presentHashArr := make([]core.HashObj, len(presentArr))
	for i, dataArr := range presentArr {
		hashObj := core.HashBytes(dataArr)
		presentHashArr[i] = hashObj
		if err = storeObj.PutBlob(hashObj, dataArr); err != nil {
			t.Fatalf("PutBlob returned error: %v", err)
		}
	}
	absentHash := core.HashBytes([]byte("gamma-not-stored"))

	missingArr, err := storeObj.MissingBlobs([]core.HashObj{presentHashArr[0], absentHash, presentHashArr[1]})
	if err != nil {
		t.Fatalf("MissingBlobs returned error: %v", err)
	}
	if len(missingArr) != 1 || missingArr[0] != absentHash {
		t.Fatalf("MissingBlobs=%v, want exactly the absent hash", missingArr)
	}

	emptyArr, err := storeObj.MissingBlobs(nil)
	if err != nil || len(emptyArr) != 0 {
		t.Fatalf("MissingBlobs(nil)=%v err=%v, want empty", emptyArr, err)
	}
}

func TestPebbleLoggerUsesZerolog(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	logObj := zerolog.New(bufferObj)
	loggerObj := NewLogger(logObj)

	loggerObj.Infof("opened %s", "db")
	loggerObj.Errorf("failed %d", 7)

	text := bufferObj.String()
	for _, wantText := range []string{
		`"component":"pebble"`,
		`"level":"debug"`,
		`"level":"error"`,
		`opened db`,
		`failed 7`,
	} {
		if !bytes.Contains(bufferObj.Bytes(), []byte(wantText)) {
			t.Fatalf("log output missing %q: %s", wantText, text)
		}
	}
}

func TestOpenWritesFormatMetadata(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	valueArr, closeObj, err := storeObj.dbObj.Get([]byte(cFormatKeyText))
	if err != nil {
		t.Fatalf("Get format metadata returned error: %v", err)
	}
	valueText := string(append([]byte(nil), valueArr...))
	if err = closeObj.Close(); err != nil {
		t.Fatalf("Close metadata value returned error: %v", err)
	}
	if valueText != core.PebbleKeyFormat {
		t.Fatalf("format metadata=%q, want %q", valueText, core.PebbleKeyFormat)
	}
}

func writeTestObjects(t *testing.T, storeObj *Obj, treeArr []core.TreeEntryObj, contentByHashObj map[core.HashObj][]byte, treeDataArr []byte, treeHashObj core.HashObj) error {
	t.Helper()

	ctx := context.Background()
	if err := storeObj.VerifyReferences(ctx, treeArr, contentByHashObj); err != nil {
		return err
	}
	blobSizeObj := make(map[core.HashObj]uint64, len(contentByHashObj))
	for hashObj, contentArr := range contentByHashObj {
		blobSizeObj[hashObj] = uint64(len(contentArr))
	}
	pendingObj, err := storeObj.PendingObjects(ctx, blobSizeObj, treeDataArr, treeHashObj)
	if err != nil {
		return err
	}
	return storeObj.WriteMissingObjects(ctx, pendingObj, contentByHashObj, treeDataArr, treeHashObj)
}

func TestOpenRejectsMissingFormatOnNonEmptyStore(t *testing.T) {
	pathToDir := t.TempDir()
	dbObj, err := pebble.Open(pathToDir, &pebble.Options{})
	if err != nil {
		t.Fatalf("pebble.Open returned error: %v", err)
	}
	if err = dbObj.Set([]byte{cBlobTag, 0}, []byte("legacy"), pebble.Sync); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if err = dbObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	storeObj, err := Open(pathToDir)
	if err == nil {
		_ = storeObj.Close()
		t.Fatal("Open accepted non-empty Pebble without format metadata")
	}
}

func TestOpenRejectsFormatMismatch(t *testing.T) {
	pathToDir := t.TempDir()
	dbObj, err := pebble.Open(pathToDir, &pebble.Options{})
	if err != nil {
		t.Fatalf("pebble.Open returned error: %v", err)
	}
	if err = dbObj.Set([]byte(cFormatKeyText), []byte("legacy"), pebble.Sync); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if err = dbObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	storeObj, err := Open(pathToDir)
	if err == nil {
		_ = storeObj.Close()
		t.Fatal("Open accepted incompatible format metadata")
	}
}

func TestReadBlobRejectsCorruptValue(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	hashObj := core.HashBytes([]byte("valid"))
	keyObj := blobKey(hashObj)
	if err = storeObj.dbObj.Set(keyObj[:], []byte("corrupt"), pebble.Sync); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}
	if _, err = storeObj.ReadBlob(hashObj); err == nil {
		t.Fatal("ReadBlob accepted corrupt value")
	}
}

func TestPutBlobRejectsHashMismatch(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	if err = storeObj.PutBlob(core.HashBytes([]byte("valid")), []byte("corrupt")); err == nil {
		t.Fatal("PutBlob accepted mismatched hash and value")
	}
}

func TestWriteObjectsRepairsCorruptExistingObjects(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	dataArr := []byte("valid")
	hashObj := core.HashBytes(dataArr)
	blobKeyObj := blobKey(hashObj)
	if err = storeObj.dbObj.Set(blobKeyObj[:], []byte("corrupt"), pebble.Sync); err != nil {
		t.Fatalf("Set corrupt blob returned error: %v", err)
	}

	treeDataArr := []byte("tree")
	treeHashObj := core.HashBytes(treeDataArr)
	treeKeyObj := treeKey(treeHashObj)
	if err = storeObj.dbObj.Set(treeKeyObj[:], []byte("corrupt"), pebble.Sync); err != nil {
		t.Fatalf("Set corrupt tree returned error: %v", err)
	}

	treeArr := []core.TreeEntryObj{
		{Path: "file.txt", Mode: core.ModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: hashObj},
	}
	if err = writeTestObjects(t, storeObj, treeArr, map[core.HashObj][]byte{hashObj: dataArr}, treeDataArr, treeHashObj); err != nil {
		t.Fatalf("writeTestObjects returned error: %v", err)
	}
	readBlobArr, err := storeObj.ReadBlob(hashObj)
	if err != nil {
		t.Fatalf("ReadBlob returned error: %v", err)
	}
	if !bytes.Equal(readBlobArr, dataArr) {
		t.Fatal("ReadBlob returned different data")
	}
	readTreeArr, err := storeObj.ReadTree(treeHashObj)
	if err != nil {
		t.Fatalf("ReadTree returned error: %v", err)
	}
	if !bytes.Equal(readTreeArr, treeDataArr) {
		t.Fatal("ReadTree returned different data")
	}
}

func TestForEachBlobScanReportsCorruptValue(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	hashObj := core.HashBytes([]byte("valid"))
	keyObj := blobKey(hashObj)
	if err = storeObj.dbObj.Set(keyObj[:], []byte("corrupt"), pebble.Sync); err != nil {
		t.Fatalf("Set returned error: %v", err)
	}

	seenFlag := false
	err = storeObj.ForEachBlobScan(context.Background(), func(scanObj BlobScanObj) error {
		seenFlag = true
		if scanObj.Hash != hashObj {
			t.Fatal("ForEachBlobScan returned a different hash")
		}
		if scanObj.Err == nil {
			t.Fatal("ForEachBlobScan accepted corrupt value")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachBlobScan returned error: %v", err)
	}
	if !seenFlag {
		t.Fatal("ForEachBlobScan did not visit corrupt value")
	}
}

func TestWriteObjectsRejectsDuplicateBlobSizeMismatch(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	dataArr := []byte("shared")
	hashObj := core.HashBytes(dataArr)
	treeArr := []core.TreeEntryObj{
		{Path: "a.txt", Mode: core.ModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: hashObj},
		{Path: "b.txt", Mode: core.ModeFile, SizeBytes: uint64(len(dataArr) + 1), BlobHash: hashObj},
	}
	if err = writeTestObjects(t, storeObj, treeArr, map[core.HashObj][]byte{hashObj: dataArr}, []byte("tree"), core.HashBytes([]byte("tree"))); err == nil {
		t.Fatal("writeTestObjects accepted inconsistent duplicate blob sizes")
	}
}

func TestWriteObjectsCommitsChunkedBatches(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	contentByHashObj := make(map[core.HashObj][]byte, cWriteBatchMaxObjects+1)
	treeArr := make([]core.TreeEntryObj, 0, cWriteBatchMaxObjects+1)
	var firstHashObj core.HashObj
	for i := 0; i < cWriteBatchMaxObjects+1; i++ {
		dataArr := []byte(fmt.Sprintf("blob-%04d", i))
		hashObj := core.HashBytes(dataArr)
		if i == 0 {
			firstHashObj = hashObj
		}
		contentByHashObj[hashObj] = dataArr
		treeArr = append(treeArr, core.TreeEntryObj{
			Path:      fmt.Sprintf("file-%04d.txt", i),
			Mode:      core.ModeFile,
			SizeBytes: uint64(len(dataArr)),
			BlobHash:  hashObj,
		})
	}

	treeDataArr := []byte("tree-data")
	treeHashObj := core.HashBytes(treeDataArr)
	if err = writeTestObjects(t, storeObj, treeArr, contentByHashObj, treeDataArr, treeHashObj); err != nil {
		t.Fatalf("writeTestObjects returned error: %v", err)
	}
	readTreeArr, err := storeObj.ReadTree(treeHashObj)
	if err != nil {
		t.Fatalf("ReadTree returned error: %v", err)
	}
	if !bytes.Equal(readTreeArr, treeDataArr) {
		t.Fatal("ReadTree returned different tree data")
	}
	if _, err = storeObj.ReadBlob(firstHashObj); err != nil {
		t.Fatalf("ReadBlob returned error: %v", err)
	}
}

func TestWriteObjectsAcceptsDuplicateReferencedBlob(t *testing.T) {
	storeObj, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = storeObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	dataArr := []byte("shared")
	hashObj := core.HashBytes(dataArr)
	if err = storeObj.PutBlob(hashObj, dataArr); err != nil {
		t.Fatalf("PutBlob returned error: %v", err)
	}
	treeArr := []core.TreeEntryObj{
		{Path: "a.txt", Mode: core.ModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: hashObj},
		{Path: "b.txt", Mode: core.ModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: hashObj},
	}
	treeDataArr := []byte("tree-with-duplicate-reference")
	treeHashObj := core.HashBytes(treeDataArr)
	if err = writeTestObjects(t, storeObj, treeArr, nil, treeDataArr, treeHashObj); err != nil {
		t.Fatalf("writeTestObjects returned error: %v", err)
	}
	readTreeArr, err := storeObj.ReadTree(treeHashObj)
	if err != nil {
		t.Fatalf("ReadTree returned error: %v", err)
	}
	if !bytes.Equal(readTreeArr, treeDataArr) {
		t.Fatal("ReadTree returned different tree data")
	}
}
