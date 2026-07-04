package archive

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type writeSourceObj struct {
	treeHashObj core.HashObj
	entryArr    []core.TreeEntryObj
	blobObj     map[core.HashObj][]byte
}

type callbackCountSourceObj struct {
	writeSourceObj
	callCount int
}

type concurrentCallbackSourceObj struct {
	writeSourceObj
}

// //

func newWriteSourceObj(entryArr []core.TreeEntryObj, blobObj map[core.HashObj][]byte) writeSourceObj {
	return writeSourceObj{
		treeHashObj: core.HashBytes([]byte("tree")),
		entryArr:    append([]core.TreeEntryObj(nil), entryArr...),
		blobObj:     blobObj,
	}
}

func (obj writeSourceObj) ReadTree(_ context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	if treeHashObj != obj.treeHashObj {
		return nil, os.ErrNotExist
	}
	return append([]core.TreeEntryObj(nil), obj.entryArr...), nil
}

func (obj writeSourceObj) UseBlob(_ context.Context, hashObj core.HashObj, useFunc func([]byte) error) error {
	dataArr, ok := obj.blobObj[hashObj]
	if !ok {
		return os.ErrNotExist
	}
	return useFunc(dataArr)
}

func (obj callbackCountSourceObj) UseBlob(_ context.Context, hashObj core.HashObj, useFunc func([]byte) error) error {
	dataArr, ok := obj.blobObj[hashObj]
	if !ok {
		return os.ErrNotExist
	}
	for i := 0; i < obj.callCount; i++ {
		if err := useFunc(dataArr); err != nil {
			return err
		}
	}
	return nil
}

func (obj concurrentCallbackSourceObj) UseBlob(_ context.Context, hashObj core.HashObj, useFunc func([]byte) error) error {
	dataArr, ok := obj.blobObj[hashObj]
	if !ok {
		return os.ErrNotExist
	}
	startChan := make(chan struct{})
	errChan := make(chan error, 2)
	var waitObj sync.WaitGroup
	for i := 0; i < 2; i++ {
		waitObj.Add(1)
		go func() {
			defer waitObj.Done()
			<-startChan
			errChan <- useFunc(dataArr)
		}()
	}
	close(startChan)
	waitObj.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			return err
		}
	}
	return nil
}

func writeTestEntryObj(pathText string, modeText string, dataArr []byte) core.TreeEntryObj {
	return core.TreeEntryObj{
		Path:      pathText,
		Mode:      modeText,
		SizeBytes: uint64(len(dataArr)),
		BlobHash:  core.HashBytes(dataArr),
	}
}

func writeTestArchiveObj(t testing.TB, obj *Obj, sourceObj writeSourceObj, formatObj FormatType) []byte {
	t.Helper()

	var bufferObj bytes.Buffer
	resultObj, err := obj.WriteTree(context.Background(), WriteTreeRequestObj{
		Format:   formatObj,
		Writer:   &bufferObj,
		TreeHash: sourceObj.treeHashObj,
		Source:   sourceObj,
	})
	if err != nil {
		t.Fatalf("WriteTree returned error: %v", err)
	}
	if resultObj.EntryCount != uint(len(sourceObj.entryArr)) {
		t.Fatalf("EntryCount=%d, want %d", resultObj.EntryCount, len(sourceObj.entryArr))
	}
	if resultObj.BodyBytes != uint64(bufferObj.Len()) {
		t.Fatalf("BodyBytes=%d, want %d", resultObj.BodyBytes, bufferObj.Len())
	}
	return bufferObj.Bytes()
}

func extractWrittenArchiveObj(t testing.TB, obj *Obj, formatObj FormatType, dataArr []byte) ResultObj {
	t.Helper()

	sourcePath := filepath.Join(t.TempDir(), "source.archive")
	if err := os.WriteFile(sourcePath, dataArr, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	spoolPath := filepath.Join(t.TempDir(), "spool")
	if err := os.Mkdir(spoolPath, 0o755); err != nil {
		t.Fatalf("Mkdir returned error: %v", err)
	}
	resultObj, err := obj.Extract(context.Background(), RequestObj{
		Key:        "core-lib",
		Version:    "v1.0.0",
		Format:     formatObj,
		SourcePath: sourcePath,
		SpoolPath:  spoolPath,
	})
	if err != nil {
		t.Fatalf("Extract returned error: %v", err)
	}
	return resultObj
}

// //

func TestWriteFormatsRoundTripThroughExtract(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	goModArr := []byte("module example.com/core\n")
	textArr := []byte("hello from archive\n")
	targetArr := []byte("pkg/a.txt")
	entryArr := []core.TreeEntryObj{
		writeTestEntryObj("pkg/a.txt", core.ModeFile, textArr),
		writeTestEntryObj("go.mod", core.ModeFile, goModArr),
		writeTestEntryObj("bin/current", core.ModeSymlink, targetArr),
	}
	sourceObj := newWriteSourceObj(entryArr, map[core.HashObj][]byte{
		core.HashBytes(goModArr):  goModArr,
		core.HashBytes(textArr):   textArr,
		core.HashBytes(targetArr): targetArr,
	})

	for _, formatObj := range []FormatType{FormatZip, FormatTar, FormatTarGz} {
		t.Run(formatObj.String(), func(t *testing.T) {
			dataArr := writeTestArchiveObj(t, archiveObj, sourceObj, formatObj)
			resultObj := extractWrittenArchiveObj(t, archiveObj, formatObj, dataArr)
			gotArr := []string{resultObj.Entries[0].Path, resultObj.Entries[1].Path, resultObj.Entries[2].Path}
			wantArr := []string{"bin/current", "go.mod", "pkg/a.txt"}
			if !slices.Equal(gotArr, wantArr) {
				t.Fatalf("paths=%v, want %v", gotArr, wantArr)
			}
			if resultObj.Entries[0].Mode != core.ModeSymlink {
				t.Fatalf("first mode=%s, want symlink", resultObj.Entries[0].Mode)
			}
		})
	}
}

func TestWriteFormatsDeterministic(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	leftArr := []byte("left")
	rightArr := []byte("right")
	entryArr := []core.TreeEntryObj{
		writeTestEntryObj("b.txt", core.ModeFile, rightArr),
		writeTestEntryObj("a.txt", core.ModeFile, leftArr),
	}
	shuffledArr := []core.TreeEntryObj{entryArr[1], entryArr[0]}
	blobObj := map[core.HashObj][]byte{
		core.HashBytes(leftArr):  leftArr,
		core.HashBytes(rightArr): rightArr,
	}
	sourceObj := newWriteSourceObj(entryArr, blobObj)
	shuffledObj := newWriteSourceObj(shuffledArr, blobObj)
	shuffledObj.treeHashObj = sourceObj.treeHashObj

	for _, formatObj := range []FormatType{FormatZip, FormatTar, FormatTarGz} {
		t.Run(formatObj.String(), func(t *testing.T) {
			firstArr := writeTestArchiveObj(t, archiveObj, sourceObj, formatObj)
			secondArr := writeTestArchiveObj(t, archiveObj, shuffledObj, formatObj)
			if !bytes.Equal(firstArr, secondArr) {
				t.Fatalf("archive output is not deterministic for %s", formatObj)
			}
		})
	}
}

func TestWriteRejectsUnsafeSymlinkTarget(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	targetArr := []byte("../escape")
	entryObj := writeTestEntryObj("link", core.ModeSymlink, targetArr)
	sourceObj := newWriteSourceObj([]core.TreeEntryObj{entryObj}, map[core.HashObj][]byte{
		entryObj.BlobHash: targetArr,
	})

	var bufferObj bytes.Buffer
	if _, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
		Format:   FormatTar,
		Writer:   &bufferObj,
		TreeHash: sourceObj.treeHashObj,
		Source:   sourceObj,
	}); err == nil {
		t.Fatal("WriteTree accepted unsafe symlink target")
	}
}

func TestWriteOutputSizeLimitIsStrict(t *testing.T) {
	limitsObj := testLimitsObj()
	limitsObj.MaxArchiveSize = 10
	archiveObj := newTestObj(t, limitsObj)
	dataArr := []byte("body")
	entryObj := writeTestEntryObj("a.txt", core.ModeFile, dataArr)
	sourceObj := newWriteSourceObj([]core.TreeEntryObj{entryObj}, map[core.HashObj][]byte{
		entryObj.BlobHash: dataArr,
	})

	var bufferObj bytes.Buffer
	if _, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
		Format:   FormatTar,
		Writer:   &bufferObj,
		TreeHash: sourceObj.treeHashObj,
		Source:   sourceObj,
	}); err == nil {
		t.Fatal("WriteTree ignored output size limit")
	}
	if bufferObj.Len() > int(limitsObj.MaxArchiveSize) {
		t.Fatalf("wrote %d bytes, limit %d", bufferObj.Len(), limitsObj.MaxArchiveSize)
	}
}

func TestWriteCanceledZipDoesNotAppendDirectory(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	dataArr := []byte("body")
	entryObj := writeTestEntryObj("a.txt", core.ModeFile, dataArr)
	sourceObj := newWriteSourceObj([]core.TreeEntryObj{entryObj}, map[core.HashObj][]byte{
		entryObj.BlobHash: dataArr,
	})
	ctx, cancelFunc := context.WithCancel(context.Background())
	cancelFunc()

	var bufferObj bytes.Buffer
	if _, err := archiveObj.WriteTree(ctx, WriteTreeRequestObj{
		Format:   FormatZip,
		Writer:   &bufferObj,
		TreeHash: sourceObj.treeHashObj,
		Source:   sourceObj,
	}); err == nil {
		t.Fatal("WriteTree ignored canceled context")
	}
	if bufferObj.Len() != 0 {
		t.Fatalf("canceled zip wrote %d bytes, want 0", bufferObj.Len())
	}
}

func TestWriteRejectsInvalidBlobCallbackCount(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	dataArr := []byte("body")
	entryObj := writeTestEntryObj("a.txt", core.ModeFile, dataArr)
	sourceObj := newWriteSourceObj([]core.TreeEntryObj{entryObj}, map[core.HashObj][]byte{
		entryObj.BlobHash: dataArr,
	})

	for _, callCount := range []int{0, 2} {
		t.Run(strconv.Itoa(callCount), func(t *testing.T) {
			var bufferObj bytes.Buffer
			_, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
				Format:   FormatTar,
				Writer:   &bufferObj,
				TreeHash: sourceObj.treeHashObj,
				Source: callbackCountSourceObj{
					writeSourceObj: sourceObj,
					callCount:      callCount,
				},
			})
			if err == nil {
				t.Fatalf("WriteTree accepted %d blob callbacks", callCount)
			}
		})
	}
}

func TestWriteRejectsConcurrentBlobCallbacks(t *testing.T) {
	archiveObj := newTestObj(t, testLimitsObj())
	dataArr := []byte("body")
	entryObj := writeTestEntryObj("a.txt", core.ModeFile, dataArr)
	sourceObj := newWriteSourceObj([]core.TreeEntryObj{entryObj}, map[core.HashObj][]byte{
		entryObj.BlobHash: dataArr,
	})

	var bufferObj bytes.Buffer
	_, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
		Format:   FormatTar,
		Writer:   &bufferObj,
		TreeHash: sourceObj.treeHashObj,
		Source: concurrentCallbackSourceObj{
			writeSourceObj: sourceObj,
		},
	})
	if err == nil {
		t.Fatal("WriteTree accepted concurrent blob callbacks")
	}
}

func BenchmarkWriteZipManySmall(b *testing.B) {
	archiveObj := newTestObj(b, testLimitsObj())
	entryArr := make([]core.TreeEntryObj, 0, 200)
	blobObj := make(map[core.HashObj][]byte, 200)
	for i := 0; i < 200; i++ {
		dataArr := []byte("small file body")
		entryObj := writeTestEntryObj("pkg/file-"+strconv.Itoa(i)+".txt", core.ModeFile, dataArr)
		entryArr = append(entryArr, entryObj)
		blobObj[entryObj.BlobHash] = dataArr
	}
	sourceObj := newWriteSourceObj(entryArr, blobObj)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var bufferObj bytes.Buffer
		if _, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
			Format:   FormatZip,
			Writer:   &bufferObj,
			TreeHash: sourceObj.treeHashObj,
			Source:   sourceObj,
		}); err != nil {
			b.Fatalf("WriteTree returned error: %v", err)
		}
	}
}

func BenchmarkWriteTarGzManySmall(b *testing.B) {
	archiveObj := newTestObj(b, testLimitsObj())
	entryArr := make([]core.TreeEntryObj, 0, 200)
	blobObj := make(map[core.HashObj][]byte, 200)
	for i := 0; i < 200; i++ {
		dataArr := []byte("small file body")
		entryObj := writeTestEntryObj("pkg/file-"+strconv.Itoa(i)+".txt", core.ModeFile, dataArr)
		entryArr = append(entryArr, entryObj)
		blobObj[entryObj.BlobHash] = dataArr
	}
	sourceObj := newWriteSourceObj(entryArr, blobObj)

	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		var bufferObj bytes.Buffer
		if _, err := archiveObj.WriteTree(context.Background(), WriteTreeRequestObj{
			Format:   FormatTarGz,
			Writer:   &bufferObj,
			TreeHash: sourceObj.treeHashObj,
			Source:   sourceObj,
		}); err != nil {
			b.Fatalf("WriteTree returned error: %v", err)
		}
	}
}
