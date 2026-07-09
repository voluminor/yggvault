package storage

import (
	"bytes"
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cockroachdb/pebble/v2"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/storage/internal/hotverify"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type artifactBuilderObj struct {
	dataArr []byte
	delay   time.Duration
	count   atomic.Uint64
}

type blockingArtifactBuilderObj struct {
	dataArr     []byte
	startedChan chan struct{}
	releaseChan chan struct{}
	onceObj     sync.Once
}

type panicArtifactBuilderObj struct{}

func (obj *artifactBuilderObj) Build(ctx context.Context, writer io.Writer) error {
	obj.count.Add(1)
	if obj.delay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(obj.delay):
		}
	}
	_, err := writer.Write(obj.dataArr)
	return err
}

func (obj *blockingArtifactBuilderObj) Build(_ context.Context, writer io.Writer) error {
	obj.onceObj.Do(func() {
		close(obj.startedChan)
	})
	<-obj.releaseChan
	_, err := writer.Write(obj.dataArr)
	return err
}

func (obj *panicArtifactBuilderObj) Build(context.Context, io.Writer) error {
	panic("builder exploded")
}

// //

func newTestConfigObj(t testing.TB) *stcfg.ConfigObj {
	t.Helper()

	configObj := stcfg.FullConfig()
	// macOS: t.TempDir lives under the symlinked /var — resolve it, otherwise New rejects the symlink component
	baseDir, evalErr := filepath.EvalSymlinks(t.TempDir())
	if evalErr != nil {
		t.Fatalf("EvalSymlinks returned error: %v", evalErr)
	}
	configObj.Storage.Dir = filepath.Join(baseDir, "cache")
	configObj.Storage.ArchiveLimits.Size.PerFile = stcfg.SizeObj(10 * 1000 * 1000)
	configObj.Storage.ArchiveLimits.Entries.PathBytes = 512
	configObj.Storage.Hot.MaxSize = stcfg.SizeObj(10 * 1000 * 1000)
	configObj.Storage.Hot.IdleTtl = time.Hour
	configObj.Storage.Quota.MaxTotalSize = 0
	configObj.Storage.Quota.RetainLatestPerKey = 1
	configObj.HistoryPolicy.Prefix = "hr"
	configObj.HistoryPolicy.Mutation = stcfg.HistoryMutationModeHistory
	configObj.HistoryPolicy.Deletion.Mode = stcfg.HistoryDeletionModeKeep
	return configObj
}

func newTestObj(t testing.TB, configObj *stcfg.ConfigObj) *Obj {
	t.Helper()

	obj, err := New(context.Background(), configObj)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err = obj.Close(ctx); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})
	return obj
}

func publishTestVersion(t testing.TB, obj *Obj, version string, entriesArr []core.InputEntryObj) core.PublishResultObj {
	t.Helper()
	return publishTestVersionSeq(t, obj, version, 0, entriesArr)
}

// publishTestVersionSeq publishes with an explicit source position; 0 falls back to max+1.
func publishTestVersionSeq(t testing.TB, obj *Obj, version string, upstreamSeq int64, entriesArr []core.InputEntryObj) core.PublishResultObj {
	t.Helper()

	resultObj, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         version,
		SourceHash:      core.HashBytes([]byte("source-" + version)),
		SourceSizeBytes: 123,
		UpstreamSeq:     upstreamSeq,
		Entries:         entriesArr,
		Detection: core.DetectionObj{
			IsGo:         true,
			EvidenceJSON: `{"go_mod":"go.mod"}`,
		},
		EventType: "publish",
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	return resultObj
}

func blobRefCount(t *testing.T, obj *Obj, hashObj core.HashObj) uint64 {
	t.Helper()

	refObj, err := obj.indexObj.BlobRefs(context.Background())
	if err != nil {
		t.Fatalf("BlobRefs returned error: %v", err)
	}
	return refObj[hashObj].Refcount
}

func blobRefSize(t *testing.T, obj *Obj, hashObj core.HashObj) uint64 {
	t.Helper()

	refObj, err := obj.indexObj.BlobRefs(context.Background())
	if err != nil {
		t.Fatalf("BlobRefs returned error: %v", err)
	}
	return refObj[hashObj].SizeBytes
}

func seedRawPebbleValue(t *testing.T, rootPath string, tagValue byte, hashObj core.HashObj, valueArr []byte) {
	t.Helper()

	pebbleDir := filepath.Join(rootPath, cDirPebbleName)
	if err := os.MkdirAll(pebbleDir, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	dbObj, err := pebble.Open(pebbleDir, &pebble.Options{})
	if err != nil {
		t.Fatalf("pebble.Open returned error: %v", err)
	}
	if err = dbObj.Set([]byte("\x00pebble_key_format"), []byte(core.PebbleKeyFormat), pebble.Sync); err != nil {
		t.Fatalf("Set format returned error: %v", err)
	}
	keyArr := append([]byte{tagValue}, hashObj[:]...)
	if err = dbObj.Set(keyArr, valueArr, pebble.Sync); err != nil {
		t.Fatalf("Set raw pebble value returned error: %v", err)
	}
	if err = dbObj.Close(); err != nil {
		t.Fatalf("Close pebble returned error: %v", err)
	}
}

func writeRawTreeObject(t *testing.T, obj *Obj, ctx context.Context, treeDataArr []byte, treeHashObj core.HashObj) {
	t.Helper()

	pendingObj, err := obj.pebbleStoreObj.PendingObjects(ctx, nil, treeDataArr, treeHashObj)
	if err != nil {
		t.Fatalf("PendingObjects tree returned error: %v", err)
	}
	if err = obj.pebbleStoreObj.WriteMissingObjects(ctx, pendingObj, nil, treeDataArr, treeHashObj); err != nil {
		t.Fatalf("WriteMissingObjects tree returned error: %v", err)
	}
}

func writeStagedBlob(t testing.TB, spoolObj *BlobSpoolObj, name string, dataArr []byte) core.StagedBlobObj {
	t.Helper()

	hashObj := core.HashBytes(dataArr)
	filePath := filepath.Join(spoolObj.RootPath(), name)
	if err := os.WriteFile(filePath, dataArr, 0o600); err != nil {
		t.Fatalf("WriteFile staged blob returned error: %v", err)
	}
	return core.StagedBlobObj{
		BlobHash:  hashObj,
		SizeBytes: uint64(len(dataArr)),
		FilePath:  filePath,
	}
}

// //

func TestHotFileCloseIsIdempotent(t *testing.T) {
	pathToFile := filepath.Join(t.TempDir(), "hot.bin")
	if err := os.WriteFile(pathToFile, []byte("hot"), 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	fileObj, err := os.Open(pathToFile)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	var cleanupCount atomic.Int64
	hotFileObj := &HotFileObj{
		Path: pathToFile,
		File: fileObj,
		cleanup: func() error {
			cleanupCount.Add(1)
			return nil
		},
	}
	if err = hotFileObj.Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if err = hotFileObj.Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if cleanupCount.Load() != 1 {
		t.Fatalf("cleanup count=%d, want 1", cleanupCount.Load())
	}
}

func TestPublishReadAndInspect(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	sharedArr := []byte("same")
	resultObj := publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{
		{Path: "go.mod", Content: []byte("module example.com/core-lib\n")},
		{Path: "pkg/a.txt", Content: sharedArr},
		{Path: "pkg/b.txt", Content: sharedArr},
	})
	if !resultObj.Published || resultObj.TreeHash.IsZero() {
		t.Fatalf("unexpected publish result: %#v", resultObj)
	}

	versionObj, ok, err := obj.GetVersion(context.Background(), "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion ok=%v err=%v", ok, err)
	}
	if versionObj.TreeHash != resultObj.TreeHash {
		t.Fatal("stored tree hash mismatch")
	}

	treeArr, err := obj.ReadTree(context.Background(), resultObj.TreeHash)
	if err != nil {
		t.Fatalf("ReadTree returned error: %v", err)
	}
	if len(treeArr) != 3 || treeArr[0].Path != "go.mod" {
		t.Fatalf("unexpected tree: %#v", treeArr)
	}

	blobArr, err := obj.ReadBlob(context.Background(), core.HashBytes(sharedArr))
	if err != nil {
		t.Fatalf("ReadBlob returned error: %v", err)
	}
	if !bytes.Equal(blobArr, sharedArr) {
		t.Fatalf("unexpected blob: %q", string(blobArr))
	}
	if got := blobRefCount(t, obj, core.HashBytes(sharedArr)); got != 2 {
		t.Fatalf("shared blob refcount=%d, want 2", got)
	}

	inspectObj, err := obj.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if inspectObj.VersionCount != 1 || inspectObj.BlobCount != 2 {
		t.Fatalf("unexpected inspect: %#v", inspectObj)
	}
	checksumObj, err := obj.ContentChecksum(context.Background())
	if err != nil {
		t.Fatalf("ContentChecksum returned error: %v", err)
	}
	if checksumObj.IsZero() {
		t.Fatal("content checksum is empty")
	}
}

func TestPublishStagedReadAndInspect(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	spoolPath := spoolObj.RootPath()

	goModObj := writeStagedBlob(t, spoolObj, "go.mod.blob", []byte("module example.com/core-lib\n"))
	dataObj := writeStagedBlob(t, spoolObj, "data.blob", []byte("same"))
	resultObj, err := obj.PublishStaged(context.Background(), spoolObj, core.StagedPublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 123,
		Entries: []core.StagedEntryObj{
			{Path: "go.mod", Mode: cModeFile, BlobHash: goModObj.BlobHash, SizeBytes: goModObj.SizeBytes},
			{Path: "pkg/a.txt", Mode: cModeFile, BlobHash: dataObj.BlobHash, SizeBytes: dataObj.SizeBytes},
			{Path: "pkg/b.txt", Mode: cModeFile, BlobHash: dataObj.BlobHash, SizeBytes: dataObj.SizeBytes},
		},
		Blobs: []core.StagedBlobObj{goModObj, dataObj},
	})
	if err != nil {
		t.Fatalf("PublishStaged returned error: %v", err)
	}
	if !resultObj.Published || resultObj.TreeHash.IsZero() {
		t.Fatalf("unexpected publish result: %#v", resultObj)
	}
	if _, err = os.Stat(spoolPath); !os.IsNotExist(err) {
		t.Fatalf("spool was not cleaned: %v", err)
	}
	if got := blobRefCount(t, obj, dataObj.BlobHash); got != 2 {
		t.Fatalf("shared staged blob refcount=%d, want 2", got)
	}
	readArr, err := obj.ReadBlob(context.Background(), dataObj.BlobHash)
	if err != nil {
		t.Fatalf("ReadBlob returned error: %v", err)
	}
	if !bytes.Equal(readArr, []byte("same")) {
		t.Fatalf("unexpected staged blob: %q", string(readArr))
	}
}

func TestPublishStagedRejectsHashMismatchAndCleansSpool(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	spoolPath := spoolObj.RootPath()
	filePath := filepath.Join(spoolPath, "bad.blob")
	if err = os.WriteFile(filePath, []byte("actual"), 0o600); err != nil {
		t.Fatalf("WriteFile staged blob returned error: %v", err)
	}
	expectedHashObj := core.HashBytes([]byte("expected"))
	_, err = obj.PublishStaged(context.Background(), spoolObj, core.StagedPublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         []core.StagedEntryObj{{Path: "file.txt", Mode: cModeFile, BlobHash: expectedHashObj, SizeBytes: uint64(len("actual"))}},
		Blobs:           []core.StagedBlobObj{{BlobHash: expectedHashObj, SizeBytes: uint64(len("actual")), FilePath: filePath}},
	})
	if err == nil {
		t.Fatal("PublishStaged accepted mismatched staged blob")
	}
	if _, statErr := os.Stat(spoolPath); !os.IsNotExist(statErr) {
		t.Fatalf("spool was not cleaned: %v", statErr)
	}
}

func TestPublishStagedRejectsBlobOutsideSpool(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.blob")
	dataArr := []byte("data")
	if err = os.WriteFile(outsidePath, dataArr, 0o600); err != nil {
		t.Fatalf("WriteFile outside blob returned error: %v", err)
	}
	hashObj := core.HashBytes(dataArr)
	_, err = obj.PublishStaged(context.Background(), spoolObj, core.StagedPublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         []core.StagedEntryObj{{Path: "file.txt", Mode: cModeFile, BlobHash: hashObj, SizeBytes: uint64(len(dataArr))}},
		Blobs:           []core.StagedBlobObj{{BlobHash: hashObj, SizeBytes: uint64(len(dataArr)), FilePath: outsidePath}},
	})
	if err == nil {
		t.Fatal("PublishStaged accepted blob outside spool")
	}
	if !strings.Contains(err.Error(), "outside spool") {
		t.Fatalf("error=%v, want outside spool", err)
	}
}

func TestPublishStagedRejectsEscapingSymlinkFromSpool(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	spoolPath := spoolObj.RootPath()
	goModObj := writeStagedBlob(t, spoolObj, "go.mod.blob", []byte("module example.com/core-lib\n"))
	linkObj := writeStagedBlob(t, spoolObj, "link.blob", []byte("../escape"))

	_, err = obj.PublishStaged(context.Background(), spoolObj, core.StagedPublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries: []core.StagedEntryObj{
			{Path: "go.mod", Mode: cModeFile, BlobHash: goModObj.BlobHash, SizeBytes: goModObj.SizeBytes},
			{Path: "link", Mode: cModeSymlink, BlobHash: linkObj.BlobHash, SizeBytes: linkObj.SizeBytes},
		},
		Blobs: []core.StagedBlobObj{goModObj, linkObj},
	})
	if err == nil {
		t.Fatal("PublishStaged accepted escaping symlink")
	}
	if !strings.Contains(err.Error(), "symlink target") {
		t.Fatalf("error=%v, want symlink target", err)
	}
	if !errors.Is(err, ErrStagedSymlinkRejected) {
		t.Fatalf("error=%v, want errors.Is(ErrStagedSymlinkRejected) so rescan can classify it as rejected content", err)
	}
	if _, ok, getErr := obj.GetVersion(context.Background(), "core-lib", "v1.0.0"); getErr != nil || ok {
		t.Fatalf("GetVersion after reject: ok=%v err=%v", ok, getErr)
	}
	if _, statErr := os.Stat(spoolPath); !os.IsNotExist(statErr) {
		t.Fatalf("spool was not cleaned: %v", statErr)
	}
}

func TestPublishStagedRejectsOversizeSymlinkTargetWithSentinel(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.ArchiveLimits.Entries.PathBytes = 8
	obj := newTestObj(t, configObj)
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	goModObj := writeStagedBlob(t, spoolObj, "go.mod.blob", []byte("module example.com/core-lib\n"))
	linkObj := writeStagedBlob(t, spoolObj, "link.blob", []byte(strings.Repeat("a", 9)))

	_, err = obj.PublishStaged(context.Background(), spoolObj, core.StagedPublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries: []core.StagedEntryObj{
			{Path: "go.mod", Mode: cModeFile, BlobHash: goModObj.BlobHash, SizeBytes: goModObj.SizeBytes},
			{Path: "link", Mode: cModeSymlink, BlobHash: linkObj.BlobHash, SizeBytes: linkObj.SizeBytes},
		},
		Blobs: []core.StagedBlobObj{goModObj, linkObj},
	})
	if err == nil {
		t.Fatal("PublishStaged accepted oversize symlink target")
	}
	if !errors.Is(err, ErrStagedSymlinkRejected) {
		t.Fatalf("error=%v, want errors.Is(ErrStagedSymlinkRejected)", err)
	}
}

func TestPublishRejectsNonAdjacentFileChildConflict(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 123,
		Entries: []core.InputEntryObj{
			{Path: "a", Content: []byte("one")},
			{Path: "a.b", Content: []byte("two")},
			{Path: "a/b", Content: []byte("three")},
		},
	})
	if err == nil {
		t.Fatal("Publish accepted file/child path conflict")
	}
}

func TestIngestFailureQuarantineRoundTrip(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()
	failureObj := core.IngestFailureObj{
		Key:     "core-lib",
		Version: "v1.0.0",
		RefSHA:  core.HashBytes([]byte("tree")).Hex(),
		Code:    "archive_invalid",
		Message: "broken archive",
	}
	if err := obj.PutIngestFailure(ctx, failureObj); err != nil {
		t.Fatalf("PutIngestFailure returned error: %v", err)
	}
	if err := obj.PutIngestFailure(ctx, failureObj); err != nil {
		t.Fatalf("second PutIngestFailure returned error: %v", err)
	}
	failureArr, err := obj.ListIngestFailures(ctx, "core-lib")
	if err != nil {
		t.Fatalf("ListIngestFailures returned error: %v", err)
	}
	if len(failureArr) != 1 {
		t.Fatalf("failure count=%d, want 1", len(failureArr))
	}
	if failureArr[0].Count != 2 || failureArr[0].Policy != core.IngestFailurePolicy {
		t.Fatalf("failure row=%+v, want count=2 policy=%d", failureArr[0], core.IngestFailurePolicy)
	}
	if failureArr[0].FirstTS.IsZero() || failureArr[0].LastTS.IsZero() {
		t.Fatalf("failure timestamps were not persisted: %+v", failureArr[0])
	}
	if err = obj.DeleteIngestFailure(ctx, "core-lib", "v1.0.0"); err != nil {
		t.Fatalf("DeleteIngestFailure returned error: %v", err)
	}
	failureArr, err = obj.ListIngestFailures(ctx, "core-lib")
	if err != nil {
		t.Fatalf("ListIngestFailures after delete returned error: %v", err)
	}
	if len(failureArr) != 0 {
		t.Fatalf("failure count after delete=%d, want 0", len(failureArr))
	}
}

func TestIngestFailureKeysListAndPrune(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	ctx := context.Background()
	for _, keyText := range []string{"core-lib", "other-lib"} {
		if err := obj.PutIngestFailure(ctx, core.IngestFailureObj{
			Key:     keyText,
			Version: "v1.0.0",
			Code:    "archive_invalid",
			Message: "broken archive",
		}); err != nil {
			t.Fatalf("PutIngestFailure(%s) returned error: %v", keyText, err)
		}
	}
	keyArr, err := obj.ListIngestFailureKeys(ctx)
	if err != nil {
		t.Fatalf("ListIngestFailureKeys returned error: %v", err)
	}
	if len(keyArr) != 2 || keyArr[0] != "core-lib" || keyArr[1] != "other-lib" {
		t.Fatalf("quarantine keys=%v, want [core-lib other-lib]", keyArr)
	}
	if err = obj.DeleteKeyIngestFailures(ctx, "core-lib"); err != nil {
		t.Fatalf("DeleteKeyIngestFailures returned error: %v", err)
	}
	keyArr, err = obj.ListIngestFailureKeys(ctx)
	if err != nil || len(keyArr) != 1 || keyArr[0] != "other-lib" {
		t.Fatalf("quarantine keys after prune=%v err=%v, want [other-lib]", keyArr, err)
	}
}

func TestBlobSpoolCloseRemovesDirDespiteCanceledContext(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	spoolObj, err := obj.NewBlobSpool(context.Background())
	if err != nil {
		t.Fatalf("NewBlobSpool returned error: %v", err)
	}
	spoolPath := spoolObj.RootPath()
	ctx, cancelFunc := context.WithCancel(context.Background())
	cancelFunc()
	// cleanup must remove the directory even with a canceled ctx, or shutdown leaves blob-spool-* junk.
	if err = spoolObj.Close(ctx); err != nil {
		t.Fatalf("Close with canceled context returned error: %v", err)
	}
	if _, err = os.Stat(spoolPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("spool stat after canceled close error=%v, want os.ErrNotExist", err)
	}
	if err = spoolObj.Close(context.Background()); err != nil {
		t.Fatalf("idempotent Close returned error: %v", err)
	}
}

func BenchmarkPublishStagedManySmall(b *testing.B) {
	obj := newTestObj(b, newTestConfigObj(b))
	ctx := context.Background()
	const entryCount = 64
	bodyArr := []byte("payload\n")
	b.SetBytes(int64(len(bodyArr) * entryCount))
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		spoolObj, err := obj.NewBlobSpool(ctx)
		if err != nil {
			b.Fatalf("NewBlobSpool returned error: %v", err)
		}
		entryArr := make([]core.StagedEntryObj, 0, entryCount)
		blobArr := make([]core.StagedBlobObj, 0, entryCount)
		for j := 0; j < entryCount; j++ {
			dataArr := []byte(fmt.Sprintf("%s%d/%d", bodyArr, i, j))
			blobObj := writeStagedBlob(b, spoolObj, fmt.Sprintf("blob-%03d", j), dataArr)
			blobArr = append(blobArr, blobObj)
			entryArr = append(entryArr, core.StagedEntryObj{
				Path:      fmt.Sprintf("pkg/file-%03d.txt", j),
				Mode:      cModeFile,
				BlobHash:  blobObj.BlobHash,
				SizeBytes: blobObj.SizeBytes,
			})
		}
		_, err = obj.PublishStaged(ctx, spoolObj, core.StagedPublishObj{
			Key:             "core-lib",
			Version:         fmt.Sprintf("v1.%d.%d", i/1000, i%1000),
			SourceHash:      core.HashBytes([]byte(fmt.Sprintf("source-%d", i))),
			SourceSizeBytes: uint64(len(bodyArr) * entryCount),
			Entries:         entryArr,
			Blobs:           blobArr,
		})
		if err != nil {
			b.Fatalf("PublishStaged returned error: %v", err)
		}
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	entryArr := []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}}
	firstObj := publishTestVersion(t, obj, "v1.0.0", entryArr)
	secondObj := publishTestVersion(t, obj, "v1.0.0", entryArr)
	if !secondObj.Skipped || firstObj.TreeHash != secondObj.TreeHash {
		t.Fatalf("unexpected second publish: %#v", secondObj)
	}
	if got := blobRefCount(t, obj, core.HashBytes([]byte("data"))); got != 1 {
		t.Fatalf("refcount=%d, want 1", got)
	}
}

func TestTreeBinaryRoundTrip(t *testing.T) {
	entriesArr := []core.TreeEntryObj{
		{Path: "b.txt", Mode: cModeFile, SizeBytes: 1, BlobHash: core.HashBytes([]byte("b"))},
		{Path: "a.txt", Mode: cModeSymlink, SizeBytes: 1, BlobHash: core.HashBytes([]byte("a"))},
	}
	dataArr, hashObj, err := treecodec.Encode(entriesArr)
	if err != nil {
		t.Fatalf("canonicalTree returned error: %v", err)
	}
	if hashObj.IsZero() {
		t.Fatal("tree hash is empty")
	}
	if !bytes.HasPrefix(dataArr, []byte(treecodec.BinaryMagic)) {
		t.Fatalf("tree does not use binary magic: %q", dataArr[:min(len(dataArr), len(treecodec.BinaryMagic))])
	}
	expectedBytes := len(treecodec.BinaryMagic) + 1 + 2*(1+len("a.txt")+1+1+core.HashSize)
	if len(dataArr) != expectedBytes {
		t.Fatalf("tree binary size=%d, want %d", len(dataArr), expectedBytes)
	}
	treeArr, err := treecodec.Decode(dataArr)
	if err != nil {
		t.Fatalf("parseTree returned error: %v", err)
	}
	if len(treeArr) != 2 || treeArr[0].Path != "a.txt" || treeArr[1].Path != "b.txt" {
		t.Fatalf("tree is not canonical: %#v", treeArr)
	}

	overflowArr := []byte(treecodec.BinaryMagic)
	overflowArr = binary.AppendUvarint(overflowArr, 1)
	overflowArr = binary.AppendUvarint(overflowArr, 1<<32)
	overflowArr = append(overflowArr, "bad.txt"...)
	overflowArr = append(overflowArr, 1)
	overflowArr = binary.AppendUvarint(overflowArr, 1)
	badHashObj := core.HashBytes([]byte("bad"))
	overflowArr = append(overflowArr, badHashObj[:]...)
	if _, err = treecodec.Decode(overflowArr); err == nil {
		t.Fatal("parseTree accepted overflowing binary path length")
	}
}

func TestTreeRejectsNonCanonicalData(t *testing.T) {
	badHashObj := core.HashBytes([]byte("bad"))

	nonMinimalArr := []byte(treecodec.BinaryMagic)
	nonMinimalArr = append(nonMinimalArr, 0x81, 0x00)
	if _, err := treecodec.Decode(nonMinimalArr); err == nil {
		t.Fatal("parseTree accepted non-canonical uvarint")
	}

	uncleanArr := []byte(treecodec.BinaryMagic)
	uncleanArr = binary.AppendUvarint(uncleanArr, 1)
	uncleanArr = binary.AppendUvarint(uncleanArr, uint64(len("./bad.txt")))
	uncleanArr = append(uncleanArr, "./bad.txt"...)
	uncleanArr = append(uncleanArr, 1)
	uncleanArr = binary.AppendUvarint(uncleanArr, 1)
	uncleanArr = append(uncleanArr, badHashObj[:]...)
	if _, err := treecodec.Decode(uncleanArr); err == nil {
		t.Fatal("parseTree accepted non-canonical path")
	}

	hugeCountArr := []byte(treecodec.BinaryMagic)
	hugeCountArr = binary.AppendUvarint(hugeCountArr, treecodec.MaxEntries+1)
	if _, err := treecodec.Decode(hugeCountArr); err == nil {
		t.Fatal("parseTree accepted huge entry count")
	}

	if _, _, err := treecodec.Encode([]core.TreeEntryObj{{Path: "./bad.txt", Mode: cModeFile, SizeBytes: 1, BlobHash: badHashObj}}); err == nil {
		t.Fatal("canonicalTree accepted non-canonical path")
	}
}

func TestMutationHistoryKeepsHistoricalVersion(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("old")}})
	resultObj := publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("new")}})
	if resultObj.Historical != "v0.0.0-hr1" {
		t.Fatalf("historical=%q, want v0.0.0-hr1", resultObj.Historical)
	}

	versionArr, err := obj.ListVersions(context.Background(), "core-lib", true)
	if err != nil {
		t.Fatalf("ListVersions returned error: %v", err)
	}
	if len(versionArr) != 2 {
		t.Fatalf("version count=%d, want 2", len(versionArr))
	}
	if _, ok, err := obj.GetVersion(context.Background(), "core-lib", "v0.0.0-hr1"); err != nil || !ok {
		t.Fatalf("historical version ok=%v err=%v", ok, err)
	}
}

func TestDeleteVersionRejectsBlobRefUnderflow(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	if err := obj.indexObj.WithTx(context.Background(), func(txObj *sqliteindex.TxObj) error {
		return txObj.ReplaceBlobRefs(context.Background(), map[core.HashObj]sqliteindex.BlobRefObj{})
	}); err != nil {
		t.Fatalf("ReplaceBlobRefs returned error: %v", err)
	}
	if err := obj.DeleteVersion(context.Background(), "core-lib", "v1.0.0"); err == nil {
		t.Fatal("DeleteVersion accepted blob refcount underflow")
	}
	if _, ok, err := obj.GetVersion(context.Background(), "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("version was deleted after underflow: ok=%v err=%v", ok, err)
	}
}

func TestDeleteVersionDeletesUnreferencedObjects(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	hashObj := core.HashBytes([]byte("data"))
	resultObj := publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	if err := obj.DeleteVersion(context.Background(), "core-lib", "v1.0.0"); err != nil {
		t.Fatalf("DeleteVersion returned error: %v", err)
	}
	if _, err := obj.ReadBlob(context.Background(), hashObj); err == nil {
		t.Fatal("blob is still readable after delete")
	}
	if _, err := obj.ReadTree(context.Background(), resultObj.TreeHash); err == nil {
		t.Fatal("tree is still readable after delete")
	}
	if got := blobRefCount(t, obj, hashObj); got != 0 {
		t.Fatalf("blob refcount=%d, want 0", got)
	}
}

func TestPublishUsesChunkedSQLBatches(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.ArchiveLimits.Entries.Count = 400
	obj := newTestObj(t, configObj)

	entryArr := make([]core.InputEntryObj, 350)
	rewriteArr := make([]core.HashObj, 350)
	for i := range entryArr {
		contentArr := []byte(fmt.Sprintf("content-%03d", i))
		entryArr[i] = core.InputEntryObj{Path: fmt.Sprintf("dir/file-%03d.txt", i), Content: contentArr}
		rewriteArr[i] = core.HashBytes(contentArr)
	}

	artifactArr := make([]core.ArtifactObj, 80)
	for i := range artifactArr {
		bodyArr := []byte(fmt.Sprintf("artifact-%03d", i))
		artifactArr[i] = core.ArtifactObj{
			MaterializerID: "mat",
			ArtifactKind:   fmt.Sprintf("kind%03d", i),
			BodyHash:       core.HashBytes(bodyArr),
			SizeBytes:      uint64(len(bodyArr)),
		}
	}

	resultObj, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         entryArr,
		RewriteBlobs:    rewriteArr,
		Artifacts:       artifactArr,
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if !resultObj.Published {
		t.Fatalf("publish did not complete: %#v", resultObj)
	}
}

func TestOverwriteDeletesOldObjects(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.HistoryPolicy.Mutation = stcfg.HistoryMutationModeOverwrite
	obj := newTestObj(t, configObj)

	oldHashObj := core.HashBytes([]byte("old"))
	oldResultObj := publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("old")}})
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("new")}})

	if _, err := obj.ReadBlob(context.Background(), oldHashObj); err == nil {
		t.Fatal("old blob is still readable after overwrite")
	}
	if _, err := obj.ReadTree(context.Background(), oldResultObj.TreeHash); err == nil {
		t.Fatal("old tree is still readable after overwrite")
	}
	if got := blobRefCount(t, obj, oldHashObj); got != 0 {
		t.Fatalf("old blob refcount=%d, want 0", got)
	}
}

func TestRepairBlobRefsRebuildsBlobRefSizes(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	dataArr := []byte("data-with-known-size")
	hashObj := core.HashBytes(dataArr)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: dataArr}})
	if err := obj.indexObj.WithTx(context.Background(), func(txObj *sqliteindex.TxObj) error {
		return txObj.ReplaceBlobRefs(context.Background(), map[core.HashObj]sqliteindex.BlobRefObj{})
	}); err != nil {
		t.Fatalf("ReplaceBlobRefs returned error: %v", err)
	}
	if err := obj.RepairBlobRefs(context.Background()); err != nil {
		t.Fatalf("RepairBlobRefs returned error: %v", err)
	}
	if got := blobRefCount(t, obj, hashObj); got != 1 {
		t.Fatalf("refcount=%d, want 1", got)
	}
	if got := blobRefSize(t, obj, hashObj); got != uint64(len(dataArr)) {
		t.Fatalf("size=%d, want %d", got, len(dataArr))
	}
}

func TestCollectGarbageDeletesOrphanBlob(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	liveArr := []byte("live-referenced-blob")
	liveHashObj := core.HashBytes(liveArr)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: liveArr}})

	orphanArr := []byte("orphan-blob-no-refs")
	orphanHashObj := core.HashBytes(orphanArr)
	if err := obj.pebbleStoreObj.PutBlob(orphanHashObj, orphanArr); err != nil {
		t.Fatalf("seed orphan blob: %v", err)
	}
	if _, err := obj.ReadBlob(context.Background(), orphanHashObj); err != nil {
		t.Fatalf("orphan blob missing before GC: %v", err)
	}

	if err := obj.CollectGarbage(context.Background()); err != nil {
		t.Fatalf("CollectGarbage returned error: %v", err)
	}

	if _, err := obj.ReadBlob(context.Background(), orphanHashObj); err == nil {
		t.Fatal("orphan blob survived incremental GC")
	}
	if _, err := obj.ReadBlob(context.Background(), liveHashObj); err != nil {
		t.Fatalf("referenced blob deleted by GC: %v", err)
	}
}

func TestCollectGarbageConcurrentPublishKeepsCommitted(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Quota.MaxTotalSize = 0
	obj := newTestObj(t, configObj)
	ctx := context.Background()

	var waitObj sync.WaitGroup
	stopChan := make(chan struct{})
	waitObj.Add(1)
	go func() {
		defer waitObj.Done()
		for {
			select {
			case <-stopChan:
				return
			default:
			}
			_ = obj.CollectGarbage(ctx)
		}
	}()

	const count = 50
	hashArr := make([]core.HashObj, count)
	for i := 0; i < count; i++ {
		contentArr := []byte(fmt.Sprintf("concurrent-gc-blob-%d", i))
		hashArr[i] = core.HashBytes(contentArr)
		if _, err := obj.Publish(ctx, core.PublishObj{
			Key:             fmt.Sprintf("lib-%d", i),
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte(fmt.Sprintf("src-%d", i))),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "f.txt", Content: contentArr}},
			EventType:       "publish",
		}); err != nil {
			close(stopChan)
			waitObj.Wait()
			t.Fatalf("publish %d returned error: %v", i, err)
		}
		if _, err := obj.ReadBlob(ctx, hashArr[i]); err != nil {
			close(stopChan)
			waitObj.Wait()
			t.Fatalf("blob %d unreadable right after publish (GC race): %v", i, err)
		}
	}
	close(stopChan)
	waitObj.Wait()

	for i := 0; i < count; i++ {
		if _, err := obj.ReadBlob(ctx, hashArr[i]); err != nil {
			t.Fatalf("referenced blob %d deleted by concurrent GC: %v", i, err)
		}
	}
}

func TestVerifyIntegrityDetectsBlobRefMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	dataArr := []byte("data")
	hashObj := core.HashBytes(dataArr)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: dataArr}})
	if err := obj.indexObj.WithTx(context.Background(), func(txObj *sqliteindex.TxObj) error {
		return txObj.ReplaceBlobRefs(context.Background(), map[core.HashObj]sqliteindex.BlobRefObj{
			hashObj: sqliteindex.BlobRefObj{Refcount: 2, SizeBytes: uint64(len(dataArr))},
		})
	}); err != nil {
		t.Fatalf("ReplaceBlobRefs returned error: %v", err)
	}

	reportObj, err := obj.VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) == 0 {
		t.Fatal("expected integrity errors")
	}
	if !strings.Contains(reportObj.Errors[0], "blob_refs mismatch") {
		t.Fatalf("unexpected integrity error: %q", reportObj.Errors[0])
	}
}

func TestVerifyIntegrityDetectsMissingBlob(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	hashObj := core.HashBytes([]byte("data"))
	if err := obj.pebbleStoreObj.DeleteObjects(context.Background(), []pebblestore.PendingObjectObj{pebblestore.PendingBlob(hashObj)}); err != nil {
		t.Fatalf("pebble Delete returned error: %v", err)
	}

	reportObj, err := obj.VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) == 0 {
		t.Fatal("expected integrity errors")
	}
}

func TestVerifyIntegrityDetectsDuplicateBlobSizeMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	ctx := context.Background()
	dataArr := []byte("shared")
	blobHashObj := core.HashBytes(dataArr)
	treeArr := []core.TreeEntryObj{
		{Path: "a.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: blobHashObj},
		{Path: "b.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr) + 1), BlobHash: blobHashObj},
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		t.Fatalf("canonicalTree returned error: %v", err)
	}
	if err = obj.pebbleStoreObj.PutBlob(blobHashObj, dataArr); err != nil {
		t.Fatalf("PutBlob returned error: %v", err)
	}
	writeRawTreeObject(t, obj, ctx, treeDataArr, treeHashObj)
	if err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.InsertVersion(ctx, sqliteindex.NewVersion(
			"core-lib",
			"v1.0.0",
			core.HashBytes([]byte("source")),
			1,
			treeHashObj,
			false,
		))
	}); err != nil {
		t.Fatalf("InsertVersion returned error: %v", err)
	}

	reportObj, err := obj.VerifyIntegrity(ctx)
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) == 0 {
		t.Fatal("expected integrity errors")
	}
	if !strings.Contains(reportObj.Errors[0], "size mismatch") {
		t.Fatalf("unexpected integrity error: %q", reportObj.Errors[0])
	}
}

func TestVerifyIntegrityDetectsDuplicateBlobSizeMismatchBeforePayloadCheck(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	ctx := context.Background()
	dataArr := []byte("missing")
	blobHashObj := core.HashBytes(dataArr)
	treeArr := []core.TreeEntryObj{
		{Path: "a.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: blobHashObj},
		{Path: "b.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr) + 1), BlobHash: blobHashObj},
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		t.Fatalf("canonicalTree returned error: %v", err)
	}
	writeRawTreeObject(t, obj, ctx, treeDataArr, treeHashObj)
	if err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.InsertVersion(ctx, sqliteindex.NewVersion(
			"core-lib",
			"v1.0.0",
			core.HashBytes([]byte("source")),
			1,
			treeHashObj,
			false,
		))
	}); err != nil {
		t.Fatalf("InsertVersion returned error: %v", err)
	}

	reportObj, err := obj.VerifyIntegrity(ctx)
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) < 2 {
		t.Fatalf("integrity errors=%v, want missing blob and size mismatch", reportObj.Errors)
	}
	if !strings.Contains(strings.Join(reportObj.Errors, "\n"), "size mismatch") {
		t.Fatalf("integrity errors=%v, want size mismatch", reportObj.Errors)
	}
}

func TestVerifyIntegrityDoesNotRepeatFailedDuplicateBlobCheck(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	ctx := context.Background()
	dataArr := []byte("missing")
	blobHashObj := core.HashBytes(dataArr)
	treeArr := []core.TreeEntryObj{
		{Path: "a.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: blobHashObj},
		{Path: "b.txt", Mode: cModeFile, SizeBytes: uint64(len(dataArr)), BlobHash: blobHashObj},
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		t.Fatalf("canonicalTree returned error: %v", err)
	}
	writeRawTreeObject(t, obj, ctx, treeDataArr, treeHashObj)
	if err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.InsertVersion(ctx, sqliteindex.NewVersion(
			"core-lib",
			"v1.0.0",
			core.HashBytes([]byte("source")),
			1,
			treeHashObj,
			false,
		))
	}); err != nil {
		t.Fatalf("InsertVersion returned error: %v", err)
	}

	reportObj, err := obj.VerifyIntegrity(ctx)
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) != 1 {
		t.Fatalf("integrity error count=%d, want 1: %v", len(reportObj.Errors), reportObj.Errors)
	}
	if !strings.Contains(reportObj.Errors[0], "pebble object not found") {
		t.Fatalf("unexpected integrity error: %q", reportObj.Errors[0])
	}
}

func TestVerifyIntegrityReportsCorruptPebbleBlob(t *testing.T) {
	configObj := newTestConfigObj(t)
	hashObj := core.HashBytes([]byte("valid"))
	seedRawPebbleValue(t, configObj.Storage.Dir, 'b', hashObj, []byte("corrupt"))

	obj := newTestObj(t, configObj)

	reportObj, err := obj.VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) == 0 {
		t.Fatal("expected integrity errors")
	}
	if !strings.Contains(reportObj.Errors[0], "content hash mismatch") {
		t.Fatalf("unexpected integrity error: %q", reportObj.Errors[0])
	}
}

func TestPublishRepairsCorruptExistingPebbleBlob(t *testing.T) {
	configObj := newTestConfigObj(t)
	dataArr := []byte("valid")
	hashObj := core.HashBytes(dataArr)
	seedRawPebbleValue(t, configObj.Storage.Dir, 'b', hashObj, []byte("corrupt"))
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: dataArr}})
	readArr, err := obj.ReadBlob(context.Background(), hashObj)
	if err != nil {
		t.Fatalf("ReadBlob returned error: %v", err)
	}
	if !bytes.Equal(readArr, dataArr) {
		t.Fatalf("blob was not repaired: %q", readArr)
	}
}

func TestPublishRejectsDuplicateBlobSizeMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	dataArr := []byte("shared")
	hashObj := core.HashBytes(dataArr)
	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries: []core.InputEntryObj{
			{Path: "a.txt", Content: dataArr},
			{Path: "b.txt", BlobHash: hashObj, SizeBytes: uint64(len(dataArr) + 1)},
		},
		EventType: "publish",
	})
	if err == nil {
		t.Fatal("Publish accepted inconsistent duplicate blob sizes")
	}
}

func TestPublishRejectsReferencedBlobSizeMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	blobHashObj := core.HashBytes([]byte("data"))
	if err := obj.pebbleStoreObj.PutBlob(blobHashObj, []byte("data")); err != nil {
		t.Fatalf("seed blob: %v", err)
	}
	if _, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         []core.InputEntryObj{{Path: "file.txt", BlobHash: blobHashObj, SizeBytes: 999}},
	}); err == nil {
		t.Fatal("Publish accepted a referenced blob with wrong size")
	}
}

func TestNewRejectsSymlinkHotRoot(t *testing.T) {
	configObj := newTestConfigObj(t)
	rootPath := configObj.Storage.Dir
	outsidePath := t.TempDir()
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, cDirHotName)); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	obj, err := New(context.Background(), configObj)
	if err == nil {
		_ = obj.Close(context.Background())
		t.Fatal("New accepted symlink hot root")
	}
}

func TestNewRejectsSymlinkCacheAncestor(t *testing.T) {
	configObj := newTestConfigObj(t)
	basePath := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	linkPath := filepath.Join(basePath, "link")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	configObj.Storage.Dir = filepath.Join(linkPath, "cache")

	obj, err := New(context.Background(), configObj)
	if err == nil {
		_ = obj.Close(context.Background())
		t.Fatal("New accepted symlink cache ancestor")
	}
}

func TestNewRejectsSymlinkIndexFile(t *testing.T) {
	configObj := newTestConfigObj(t)
	rootPath := configObj.Storage.Dir
	if err := os.MkdirAll(rootPath, 0o700); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	outsidePath := filepath.Join(t.TempDir(), "outside.sqlite")
	if err := os.WriteFile(outsidePath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.Symlink(outsidePath, filepath.Join(rootPath, cFileIndexName)); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	obj, err := New(context.Background(), configObj)
	if err == nil {
		_ = obj.Close(context.Background())
		t.Fatal("New accepted symlink index file")
	}
}

func TestPublishRejectsArchiveLimitAbuse(t *testing.T) {
	t.Run("source_size", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Size.Compressed = 8
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 9,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
		})
		if err == nil {
			t.Fatal("Publish accepted oversized source archive")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Key != "core-lib" || typedErr.Version != "v1.0.0" || typedErr.Value != 9 || typedErr.MaxValue != 8 {
			t.Fatalf("unexpected archive size error: %+v", typedErr)
		}
	})

	t.Run("file_count", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Entries.Count = 1
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries: []core.InputEntryObj{
				{Path: "a.txt", Content: []byte("a")},
				{Path: "b.txt", Content: []byte("b")},
			},
		})
		if err == nil {
			t.Fatal("Publish accepted too many archive entries")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cArchiveCheckFileCount || typedErr.Value != 2 || typedErr.MaxValue != 1 {
			t.Fatalf("unexpected archive limits error: %+v", typedErr)
		}
	})

	t.Run("path_bytes", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Entries.PathBytes = 4
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "too-long.txt", Content: []byte("data")}},
		})
		if err == nil {
			t.Fatal("Publish accepted oversized archive path")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cArchiveCheckPathBytes || typedErr.Value != uint64(len("too-long.txt")) || typedErr.MaxValue != 4 {
			t.Fatalf("unexpected archive limits error: %+v", typedErr)
		}
		if !errors.Is(err, util.ErrArchiveEntryPathTooLong) {
			t.Fatalf("Publish error does not wrap path limit sentinel: %v", err)
		}
	})

	t.Run("file_bytes", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Size.PerFile = 3
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
		})
		if err == nil {
			t.Fatal("Publish accepted oversized archive file")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cArchiveCheckFileBytes || typedErr.Value != 4 || typedErr.MaxValue != 3 {
			t.Fatalf("unexpected archive limits error: %+v", typedErr)
		}
	})

	t.Run("unpacked_size", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Size.Unpacked = 3
		configObj.Storage.ArchiveLimits.Size.PerFile = 3
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries: []core.InputEntryObj{
				{Path: "a.txt", Content: []byte("ab")},
				{Path: "b.txt", Content: []byte("cd")},
			},
		})
		if err == nil {
			t.Fatal("Publish accepted oversized unpacked archive")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cArchiveCheckUnpackedSize || typedErr.Value != 4 || typedErr.MaxValue != 3 {
			t.Fatalf("unexpected archive limits error: %+v", typedErr)
		}
	})

	t.Run("rewrite_set_count", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.ArchiveLimits.Entries.Count = 1
		obj := newTestObj(t, configObj)
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
			RewriteBlobs: []core.HashObj{
				core.HashBytes([]byte("a")),
				core.HashBytes([]byte("b")),
			},
		})
		if err == nil {
			t.Fatal("Publish accepted too many rewrite_set entries")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cPublishCheckRewriteSetCount || typedErr.Value != 2 || typedErr.MaxValue != 1 {
			t.Fatalf("unexpected publish limits error: %+v", typedErr)
		}
	})

	t.Run("artifact_count", func(t *testing.T) {
		obj := newTestObj(t, newTestConfigObj(t))
		artifactArr := make([]core.ArtifactObj, cMaxArtifactsPerPublish+1)
		for i := range artifactArr {
			bodyArr := []byte(fmt.Sprintf("artifact-%04d", i))
			artifactArr[i] = core.ArtifactObj{
				MaterializerID: "mat",
				ArtifactKind:   fmt.Sprintf("kind%04d", i),
				BodyHash:       core.HashBytes(bodyArr),
				SizeBytes:      uint64(len(bodyArr)),
			}
		}
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
			Artifacts:       artifactArr,
		})
		if err == nil {
			t.Fatal("Publish accepted too many artifact metadata entries")
		}
		var typedErr *stcode.ErrArchiveLimitExceededObj
		if !errors.As(err, &typedErr) {
			t.Fatalf("Publish error=%T, want ErrArchiveLimitExceededObj: %v", err, err)
		}
		if typedErr.Check != cPublishCheckArtifactMetadataCount || typedErr.Value != uint64(cMaxArtifactsPerPublish+1) || typedErr.MaxValue != uint64(cMaxArtifactsPerPublish) {
			t.Fatalf("unexpected publish limits error: %+v", typedErr)
		}
	})

	t.Run("evidence_json", func(t *testing.T) {
		obj := newTestObj(t, newTestConfigObj(t))
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.0.0",
			SourceHash:      core.HashBytes([]byte("source")),
			SourceSizeBytes: 1,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
			Detection:       core.DetectionObj{EvidenceJSON: "{bad"},
		})
		if err == nil {
			t.Fatal("Publish accepted invalid evidence_json")
		}
	})
}

func TestPublishCleansPebbleObjectsAfterSQLFailure(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	entryArr := []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}}
	treeArr, contentByHashObj, err := normalizeInputEntries(entryArr, "", "", 512, 10*1000*1000, 10000, 0)
	if err != nil {
		t.Fatalf("normalizeInputEntries returned error: %v", err)
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		t.Fatalf("canonicalTree returned error: %v", err)
	}

	_, err = obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         entryArr,
		Artifacts: []core.ArtifactObj{{
			MaterializerID: "universal",
			ArtifactKind:   "zip",
			ListenerID:     cListenerGlobal,
			BodyHash:       core.HashBytes([]byte("artifact")),
			SizeBytes:      uint64(len("artifact")),
			ETag:           "bad\netag",
		}},
	})
	if err == nil {
		t.Fatal("Publish accepted invalid artifact metadata")
	}

	if _, readErr := obj.pebbleStoreObj.ReadTree(treeHashObj); readErr == nil {
		t.Fatal("tree object exists after failed publish")
	}
	if len(treeDataArr) == 0 {
		t.Fatal("normalized tree is empty")
	}
	for hashObj := range contentByHashObj {
		if _, readErr := obj.pebbleStoreObj.ReadBlob(hashObj); readErr == nil {
			t.Fatal("blob object exists after failed publish")
		}
	}
}

func TestEvictVersionsRespectsMaxVersionsPerKey(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Quota.MaxVersionsPerKey = 2
	configObj.Storage.Quota.RetainLatestPerKey = 1
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("one")}})
	publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("two")}})
	publishTestVersion(t, obj, "v1.2.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("three")}})
	if err := obj.EvictVersions(context.Background()); err != nil {
		t.Fatalf("EvictVersions returned error: %v", err)
	}
	versionArr, err := obj.ListVersions(context.Background(), "core-lib", true)
	if err != nil {
		t.Fatalf("ListVersions returned error: %v", err)
	}
	if len(versionArr) != 2 || versionArr[0].Version != "v1.2.0" || versionArr[1].Version != "v1.1.0" {
		t.Fatalf("unexpected versions after eviction: %#v", versionArr)
	}
}

// Display latest is the maximum upstream_seq and is protected from quota eviction even when oldest by ingest.
func TestQuotaTargetKeepsUpstreamLatestWhenOldest(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersionSeq(t, obj, "v2.0.0", 2, []core.InputEntryObj{{Path: "file.txt", Content: []byte("latest")}})
	publishTestVersionSeq(t, obj, "v1.0.0", 1, []core.InputEntryObj{{Path: "file.txt", Content: []byte("older")}})

	obj.writeMu.Lock()
	err := obj.evictVersionsLocked(context.Background(), 1)
	obj.writeMu.Unlock()
	if err != nil {
		t.Fatalf("evictVersionsLocked returned error: %v", err)
	}

	versionArr, err := obj.ListVersions(context.Background(), "core-lib", true)
	if err != nil {
		t.Fatalf("ListVersions returned error: %v", err)
	}
	if len(versionArr) != 1 || versionArr[0].Version != "v2.0.0" {
		t.Fatalf("unexpected versions after target eviction: %#v", versionArr)
	}
}

func TestLatestVersionTracksMutations(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("one")}})
	publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("two")}})

	versionObj, ok, err := obj.LatestVersion(context.Background(), "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion ok=%v err=%v", ok, err)
	}
	if versionObj.Version != "v1.1.0" {
		t.Fatalf("latest version=%s, want v1.1.0", versionObj.Version)
	}
	if err = obj.MarkUpstreamDeleted(context.Background(), "core-lib", "v1.1.0"); err != nil {
		t.Fatalf("MarkUpstreamDeleted returned error: %v", err)
	}
	versionObj, ok, err = obj.LatestVersion(context.Background(), "core-lib")
	if err != nil || !ok {
		t.Fatalf("LatestVersion after delete ok=%v err=%v", ok, err)
	}
	if versionObj.Version != "v1.0.0" {
		t.Fatalf("latest version=%s, want v1.0.0", versionObj.Version)
	}
}

func TestPublishQuotaEvictionDoesNotDeadlock(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.HistoryPolicy.Mutation = stcfg.HistoryMutationModeOverwrite
	configObj.Storage.Quota.RetainLatestPerKey = 1
	obj := newTestObj(t, configObj)

	for i, version := range []string{"v1.0.0", "v1.1.0", "v1.2.0"} {
		dataArr := bytes.Repeat([]byte{byte('a' + i)}, 128*1024)
		publishTestVersion(t, obj, version, []core.InputEntryObj{{Path: "file.txt", Content: dataArr}})
	}
	currentBytes := obj.DurableBytes()
	obj.configObj.Storage.Quota.MaxTotalSize = stcfg.SizeObj(currentBytes + 64*1024)
	obj.configObj.Storage.Quota.EvictToSize = stcfg.SizeObj(currentBytes / 2)

	doneChan := make(chan error, 1)
	go func() {
		_, err := obj.Publish(context.Background(), core.PublishObj{
			Key:             "core-lib",
			Version:         "v1.3.0",
			SourceHash:      core.HashBytes([]byte("source-v1.3.0")),
			SourceSizeBytes: 123,
			Entries:         []core.InputEntryObj{{Path: "file.txt", Content: bytes.Repeat([]byte("z"), 128*1024)}},
		})
		doneChan <- err
	}()
	select {
	case err := <-doneChan:
		if err != nil {
			var typedErr *stcode.ErrCacheQuotaExceededObj
			if !errors.As(err, &typedErr) {
				t.Fatalf("Publish error=%T, want ErrCacheQuotaExceededObj or nil: %v", err, err)
			}
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Publish did not return under quota pressure")
	}
}

func TestPublishReturnsTypedQuotaExceeded(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Quota.MaxTotalSize = 1
	obj := newTestObj(t, configObj)

	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}},
	})
	if err == nil {
		t.Fatal("Publish accepted data over durable quota")
	}
	var typedErr *stcode.ErrCacheQuotaExceededObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("Publish error=%T, want ErrCacheQuotaExceededObj: %v", err, err)
	}
	if typedErr.CacheArea != cCacheAreaDurable || typedErr.CheckName != cQuotaCheckAdmission || typedErr.MaxTotalBytes != 1 || typedErr.IncomingBytes == 0 {
		t.Fatalf("unexpected quota error: %+v", typedErr)
	}
}

func TestPublishQuotaEvictionProtectsReferencedExistingBlob(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Quota.RetainLatestPerKey = 0
	obj := newTestObj(t, configObj)

	sharedArr := bytes.Repeat([]byte("a"), 128*1024)
	sharedHashObj := core.HashBytes(sharedArr)
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "shared.txt", Content: sharedArr}})
	publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "old.txt", Content: bytes.Repeat([]byte("b"), 128*1024)}})
	currentBytes := obj.DurableBytes()
	obj.configObj.Storage.Quota.MaxTotalSize = stcfg.SizeObj(currentBytes + 64*1024)
	obj.configObj.Storage.Quota.EvictToSize = stcfg.SizeObj(currentBytes - 64*1024)

	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.2.0",
		SourceHash:      core.HashBytes([]byte("source-v1.2.0")),
		SourceSizeBytes: 123,
		Entries: []core.InputEntryObj{
			{Path: "shared.txt", BlobHash: sharedHashObj, SizeBytes: uint64(len(sharedArr))},
			{Path: "new.txt", Content: bytes.Repeat([]byte("c"), 128*1024)},
		},
	})
	if err != nil {
		t.Fatalf("Publish returned error: %v", err)
	}
	if _, err = obj.ReadBlob(context.Background(), sharedHashObj); err != nil {
		t.Fatalf("protected blob disappeared after publish: %v", err)
	}
	reportObj, err := obj.VerifyIntegrity(context.Background())
	if err != nil {
		t.Fatalf("VerifyIntegrity returned error: %v", err)
	}
	if len(reportObj.Errors) != 0 {
		t.Fatalf("integrity errors after quota publish: %v", reportObj.Errors)
	}
}

func TestEnsureArtifactFileSingleflight(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	artifactArr := []byte("artifact-body")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(artifactArr),
		SizeBytes:      uint64(len(artifactArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}

	builderObj := &artifactBuilderObj{dataArr: artifactArr, delay: 100 * time.Millisecond}
	var waitObj sync.WaitGroup
	errChan := make(chan error, 16)
	for i := 0; i < 16; i++ {
		waitObj.Add(1)
		go func() {
			defer waitObj.Done()
			fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
			if err != nil {
				errChan <- err
				return
			}
			if _, statErr := os.Stat(fileObj.Path); statErr != nil {
				errChan <- statErr
				_ = fileObj.Close()
				return
			}
			if fileObj.File == nil {
				errChan <- errors.New("hot file descriptor is nil")
				_ = fileObj.Close()
				return
			}
			readArr, readErr := io.ReadAll(fileObj.File)
			if readErr != nil {
				errChan <- readErr
				_ = fileObj.Close()
				return
			}
			if !bytes.Equal(readArr, artifactArr) {
				errChan <- fmt.Errorf("hot file content=%q", readArr)
			}
			_ = fileObj.Close()
		}()
	}
	waitObj.Wait()
	close(errChan)
	for err := range errChan {
		if err != nil {
			t.Fatalf("EnsureArtifactFile returned error: %v", err)
		}
	}
	if builderObj.count.Load() != 1 {
		t.Fatalf("builder calls=%d, want 1", builderObj.count.Load())
	}
}

func TestEnsureArtifactFileRecoversBuilderPanic(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact-body")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}

	_, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &panicArtifactBuilderObj{})
	if err == nil {
		t.Fatal("EnsureArtifactFile accepted panicking builder")
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("EnsureArtifactFile error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckPanic || typedErr.Cause == nil {
		t.Fatalf("unexpected panic error: %+v", typedErr)
	}
	tempEntryArr, readErr := os.ReadDir(obj.tempDir)
	if readErr != nil {
		t.Fatalf("ReadDir temp returned error: %v", readErr)
	}
	if len(tempEntryArr) != 0 {
		t.Fatalf("temp dir entries after panic=%d, want 0", len(tempEntryArr))
	}

	fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr})
	if err != nil {
		t.Fatalf("EnsureArtifactFile after panic returned error: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("HotFileObj.Close returned error: %v", err)
	}
}

func TestCloseWaitsForArtifactFlight(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj, err := New(context.Background(), configObj)
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact-body")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err = obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}

	builderObj := &blockingArtifactBuilderObj{
		dataArr:     bodyArr,
		startedChan: make(chan struct{}),
		releaseChan: make(chan struct{}),
	}
	ensureChan := make(chan error, 1)
	go func() {
		fileObj, ensureErr := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
		if fileObj != nil {
			_ = fileObj.Close()
		}
		ensureChan <- ensureErr
	}()
	select {
	case <-builderObj.startedChan:
	case <-time.After(3 * time.Second):
		t.Fatal("artifact builder did not start")
	}

	closeChan := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		closeChan <- obj.Close(ctx)
	}()
	select {
	case err = <-closeChan:
		t.Fatalf("Close returned before artifact flight finished: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(builderObj.releaseChan)
	if err = <-ensureChan; err != nil && !errors.Is(err, context.Canceled) {
		t.Fatalf("EnsureArtifactFile returned error: %v", err)
	}
	if err = <-closeChan; err != nil {
		t.Fatalf("Close returned error: %v", err)
	}
}

func TestEnsureArtifactFileTransientCleanupIsShared(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeNone
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	artifactArr := []byte("artifact-body")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(artifactArr),
		SizeBytes:      uint64(len(artifactArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}

	builderObj := &artifactBuilderObj{dataArr: artifactArr, delay: 100 * time.Millisecond}
	fileChan := make(chan *HotFileObj, 2)
	var waitObj sync.WaitGroup
	for i := 0; i < 2; i++ {
		waitObj.Add(1)
		go func() {
			defer waitObj.Done()
			fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
			if err != nil {
				t.Errorf("EnsureArtifactFile returned error: %v", err)
				return
			}
			fileChan <- fileObj
		}()
	}
	waitObj.Wait()
	close(fileChan)
	fileArr := make([]*HotFileObj, 0, 2)
	for fileObj := range fileChan {
		fileArr = append(fileArr, fileObj)
	}
	if len(fileArr) != 2 {
		t.Fatalf("file count=%d, want 2", len(fileArr))
	}
	if fileArr[0].Path != fileArr[1].Path {
		t.Fatalf("artifact flight returned different paths: %q %q", fileArr[0].Path, fileArr[1].Path)
	}
	if err := fileArr[0].Close(); err != nil {
		t.Fatalf("first Close returned error: %v", err)
	}
	if _, err := os.Stat(fileArr[1].Path); err != nil {
		t.Fatalf("transient file removed before all users closed it: %v", err)
	}
	if err := fileArr[1].Close(); err != nil {
		t.Fatalf("second Close returned error: %v", err)
	}
	if _, err := os.Stat(fileArr[1].Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("transient file still exists or stat failed: %v", err)
	}
}

func TestEnsureArtifactFileDistinctFlightsDoNotClobber(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeNone
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	artifactArr := []byte("artifact-body-distinct")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(artifactArr),
		SizeBytes:      uint64(len(artifactArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	builderObj := &artifactBuilderObj{dataArr: artifactArr}

	fileA, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
	if err != nil {
		t.Fatalf("first EnsureArtifactFile returned error: %v", err)
	}
	fileB, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
	if err != nil {
		_ = fileA.Close()
		t.Fatalf("second EnsureArtifactFile returned error: %v", err)
	}
	if fileA.Path == fileB.Path {
		_ = fileA.Close()
		_ = fileB.Close()
		t.Fatalf("distinct non-retained flights shared a hot path: %q", fileA.Path)
	}

	if err = fileA.Close(); err != nil {
		_ = fileB.Close()
		t.Fatalf("fileA.Close returned error: %v", err)
	}
	gotArr, err := os.ReadFile(fileB.Path)
	if err != nil {
		_ = fileB.Close()
		t.Fatalf("fileB unreadable after fileA cleanup (cross-flight clobber): %v", err)
	}
	if !bytes.Equal(gotArr, artifactArr) {
		_ = fileB.Close()
		t.Fatalf("fileB content mismatch after fileA cleanup")
	}
	if err = fileB.Close(); err != nil {
		t.Fatalf("fileB.Close returned error: %v", err)
	}
	if _, err = os.Stat(fileB.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("fileB transient not removed after final close: %v", err)
	}
}

func TestEnsureArtifactFileConcurrentRebuildNoClobber(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeNone
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	artifactArr := []byte("artifact-body-concurrent")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(artifactArr),
		SizeBytes:      uint64(len(artifactArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	builderObj := &artifactBuilderObj{dataArr: artifactArr}

	const workerCount = 8
	const iterCount = 25
	var waitObj sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		waitObj.Add(1)
		go func() {
			defer waitObj.Done()
			for i := 0; i < iterCount; i++ {
				fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), builderObj)
				if err != nil {
					t.Errorf("EnsureArtifactFile returned error: %v", err)
					return
				}
				gotArr, readErr := io.ReadAll(fileObj.File)
				if readErr != nil {
					_ = fileObj.Close()
					t.Errorf("read hot file returned error: %v", readErr)
					return
				}
				if !bytes.Equal(gotArr, artifactArr) {
					_ = fileObj.Close()
					t.Errorf("hot file content mismatch: got %d bytes", len(gotArr))
					return
				}
				if err = fileObj.Close(); err != nil {
					t.Errorf("Close returned error: %v", err)
					return
				}
			}
		}()
	}
	waitObj.Wait()
}

func TestEnsureArtifactFileRejectsOversizedBuilder(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	_, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: append(bodyArr, '!')})
	if err == nil {
		t.Fatal("EnsureArtifactFile accepted oversized builder output")
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("EnsureArtifactFile error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckOutputSize || typedErr.ActualSize != uint64(len(bodyArr)+1) || typedErr.ExpectedSize != uint64(len(bodyArr)) {
		t.Fatalf("unexpected artifact build error: %+v", typedErr)
	}
}

func TestEnsureArtifactFileReturnsTypedHashMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	wrongArr := []byte("ARTIFACT")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	_, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: wrongArr})
	if err == nil {
		t.Fatal("EnsureArtifactFile accepted wrong artifact hash")
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("EnsureArtifactFile error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckBodyHash || typedErr.ExpectedHash != artifactObj.BodyHash.Hex() || typedErr.ActualHash == "" {
		t.Fatalf("unexpected artifact build error: %+v", typedErr)
	}
}

func TestEnsureArtifactFileReturnsTypedSizeMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr) + 1),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	_, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr})
	if err == nil {
		t.Fatal("EnsureArtifactFile accepted wrong artifact size")
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("EnsureArtifactFile error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckBodySize || typedErr.ExpectedSize != uint64(len(bodyArr)+1) || typedErr.ActualSize != uint64(len(bodyArr)) {
		t.Fatalf("unexpected artifact build error: %+v", typedErr)
	}
}

func TestEnsureArtifactFileRebuildsCorruptHotFile(t *testing.T) {
	cfgObj := newTestConfigObj(t)
	cfgObj.Storage.Hot.VerifyOnRead = stcfg.HotVerifyOnReadAlways
	obj := newTestObj(t, cfgObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	firstBuilderObj := &artifactBuilderObj{dataArr: bodyArr}
	fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), firstBuilderObj)
	if err != nil {
		t.Fatalf("EnsureArtifactFile returned error: %v", err)
	}
	defer func() { _ = fileObj.Close() }()
	if firstBuilderObj.count.Load() != 1 {
		t.Fatalf("builder calls=%d, want 1", firstBuilderObj.count.Load())
	}

	infoObj, err := os.Stat(fileObj.Path)
	if err != nil {
		_ = fileObj.Close()
		t.Fatalf("Stat returned error: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("HotFile Close returned error: %v", err)
	}
	if err = os.WriteFile(fileObj.Path, []byte("corrupt!"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err = os.Chtimes(fileObj.Path, infoObj.ModTime(), infoObj.ModTime()); err != nil {
		t.Fatalf("Chtimes returned error: %v", err)
	}

	secondBuilderObj := &artifactBuilderObj{dataArr: bodyArr}
	secondFileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), secondBuilderObj)
	if err != nil {
		t.Fatalf("second EnsureArtifactFile returned error: %v", err)
	}
	defer func() { _ = secondFileObj.Close() }()
	if secondBuilderObj.count.Load() != 1 {
		_ = secondFileObj.Close()
		t.Fatalf("second builder calls=%d, want 1", secondBuilderObj.count.Load())
	}
	storedArr, err := os.ReadFile(secondFileObj.Path)
	if err != nil {
		_ = secondFileObj.Close()
		t.Fatalf("ReadFile returned error: %v", err)
	}
	if err = secondFileObj.Close(); err != nil {
		t.Fatalf("second HotFile Close returned error: %v", err)
	}
	if !bytes.Equal(storedArr, bodyArr) {
		t.Fatalf("hot artifact was not rebuilt: %q", storedArr)
	}
}

func TestEnsureArtifactFileUsesContentAddressedPathAfterReregister(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	oldBodyArr := []byte("old-artifact")
	newBodyArr := []byte("new-artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(oldBodyArr),
		SizeBytes:      uint64(len(oldBodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact old returned error: %v", err)
	}
	oldFileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: oldBodyArr})
	if err != nil {
		t.Fatalf("EnsureArtifactFile old returned error: %v", err)
	}
	defer func() { _ = oldFileObj.Close() }()

	artifactObj.BodyHash = core.HashBytes(newBodyArr)
	artifactObj.SizeBytes = uint64(len(newBodyArr))
	if err = obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact new returned error: %v", err)
	}
	newFileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: newBodyArr})
	if err != nil {
		t.Fatalf("EnsureArtifactFile new returned error: %v", err)
	}
	defer func() { _ = newFileObj.Close() }()

	if oldFileObj.Path == newFileObj.Path {
		t.Fatal("re-registered artifact reused the old hot path")
	}
	oldStoredArr, err := os.ReadFile(oldFileObj.Path)
	if err != nil {
		t.Fatalf("ReadFile old returned error: %v", err)
	}
	if !bytes.Equal(oldStoredArr, oldBodyArr) {
		t.Fatalf("old hot file was overwritten: %q", oldStoredArr)
	}
	newStoredArr, err := os.ReadFile(newFileObj.Path)
	if err != nil {
		t.Fatalf("ReadFile new returned error: %v", err)
	}
	if !bytes.Equal(newStoredArr, newBodyArr) {
		t.Fatalf("new hot file has wrong content: %q", newStoredArr)
	}
}

func TestEnsureArtifactFileCancellationStopsUnobservedBuild(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	doneChan := make(chan error, 1)
	go func() {
		_, err := obj.EnsureArtifactFile(ctx, artifactKeyFromObj(artifactObj), &artifactBuilderObj{
			dataArr: bodyArr,
			delay:   time.Second,
		})
		doneChan <- err
	}()
	time.Sleep(20 * time.Millisecond)
	cancel()

	select {
	case err := <-doneChan:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("EnsureArtifactFile err=%v, want context.Canceled", err)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("EnsureArtifactFile did not return after caller cancellation")
	}

	deadlineChan := time.After(500 * time.Millisecond)
	for {
		obj.flightMu.Lock()
		flightCount := len(obj.flightMap)
		obj.flightMu.Unlock()
		if flightCount == 0 {
			break
		}
		select {
		case <-deadlineChan:
			t.Fatal("artifact flight survived after every waiter canceled")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestRegisterArtifactRejectsOversizedArtifact(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeAll
	configObj.Storage.Hot.MaxSize = 4
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
	}
	err := obj.RegisterArtifact(context.Background(), artifactObj)
	if err == nil {
		t.Fatal("RegisterArtifact accepted oversized artifact")
	}
	var typedErr *stcode.ErrArtifactSizeExceededObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("RegisterArtifact error=%T, want ErrArtifactSizeExceededObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckMetadataSize || typedErr.MaxBytes != 4 || typedErr.SizeBytes != uint64(len(bodyArr)) {
		t.Fatalf("unexpected artifact size error: %+v", typedErr)
	}
}

func TestHotBudgetEvictsOversizedCacheFile(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.MaxSize = 1
	obj := newTestObj(t, configObj)

	dataArr := []byte("artifact")
	hotPath := filepath.Join(obj.hotDir, "artifact.bin")
	if err := os.WriteFile(hotPath, dataArr, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := obj.EnforceHotBudget(context.Background()); err != nil {
		t.Fatalf("EnforceHotBudget returned error: %v", err)
	}
	if _, err := os.Stat(hotPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("hot file still exists or stat failed: %v", err)
	}
}

func TestHotBudgetDefersActiveRetainedFileRemoval(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.Retain = stcfg.CacheRetainModeAll
	obj := newTestObj(t, configObj)

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	bodyArr := []byte("artifact-body")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FormatVersion:  cDefaultFormat,
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr})
	if err != nil {
		t.Fatalf("EnsureArtifactFile returned error: %v", err)
	}
	obj.configObj.Storage.Hot.MaxSize = 1
	if err = obj.EnforceHotBudget(context.Background()); err == nil {
		_ = fileObj.Close()
		t.Fatal("EnforceHotBudget removed active retained file")
	}
	if _, err = os.Stat(fileObj.Path); err != nil {
		_ = fileObj.Close()
		t.Fatalf("active retained file disappeared before close: %v", err)
	}
	if err = fileObj.Close(); err != nil {
		t.Fatalf("HotFile Close returned error: %v", err)
	}
	if _, err = os.Stat(fileObj.Path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending hot file still exists or stat failed: %v", err)
	}
}

func TestHotBudgetDoesNotDoubleCountIdleEviction(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.MaxSize = 10
	configObj.Storage.Hot.IdleTtl = time.Hour
	obj := newTestObj(t, configObj)

	oldPath := filepath.Join(obj.hotDir, "old.bin")
	freshAPath := filepath.Join(obj.hotDir, "fresh-a.bin")
	freshBPath := filepath.Join(obj.hotDir, "fresh-b.bin")
	for _, pathText := range []string{oldPath, freshAPath, freshBPath} {
		if err := os.WriteFile(pathText, []byte("123456"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}
	oldTime := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("Chtimes returned error: %v", err)
	}

	if err := obj.EnforceHotBudget(context.Background()); err != nil {
		t.Fatalf("EnforceHotBudget returned error: %v", err)
	}
	sizeBytes, err := obj.hotBytes(context.Background())
	if err != nil {
		t.Fatalf("hotBytes returned error: %v", err)
	}
	if sizeBytes > uint64(configObj.Storage.Hot.MaxSize) {
		t.Fatalf("hot cache size=%d, want <=%d", sizeBytes, configObj.Storage.Hot.MaxSize)
	}
}

func TestHotBudgetTier2EvictsNonIdle(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.MaxSize = 10
	configObj.Storage.Hot.IdleTtl = time.Hour
	obj := newTestObj(t, configObj)

	for _, name := range []string{"a.bin", "b.bin", "c.bin"} {
		if err := os.WriteFile(filepath.Join(obj.hotDir, name), []byte("123456"), 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
	}
	if err := obj.EnforceHotBudget(context.Background()); err != nil {
		t.Fatalf("EnforceHotBudget returned error: %v", err)
	}
	sizeBytes, err := obj.hotBytes(context.Background())
	if err != nil {
		t.Fatalf("hotBytes returned error: %v", err)
	}
	if sizeBytes > uint64(configObj.Storage.Hot.MaxSize) {
		t.Fatalf("hot cache size=%d, want <=%d (Tier 2 must evict non-idle)", sizeBytes, configObj.Storage.Hot.MaxSize)
	}
}

func TestInspectDoesNotMutateHotDir(t *testing.T) {
	configObj := newTestConfigObj(t)
	configObj.Storage.Hot.MaxSize = 1 << 20
	obj := newTestObj(t, configObj)

	strayPath := filepath.Join(obj.hotDir, "stray-link")
	if err := os.Symlink("nowhere", strayPath); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	if _, err := obj.Inspect(context.Background()); err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if _, err := os.Lstat(strayPath); err != nil {
		t.Fatalf("Inspect mutated hot dir (removed stray entry): %v", err)
	}

	if err := obj.EnforceHotBudget(context.Background()); err != nil {
		t.Fatalf("EnforceHotBudget returned error: %v", err)
	}
	if _, err := os.Lstat(strayPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("EnforceHotBudget did not remove stray entry: %v", err)
	}
}

func TestVerifyOnReadModes(t *testing.T) {
	ctx := context.Background()
	bodyArr := []byte("correct-artifact-body-0123456789")
	corruptArr := []byte("CORRUPT-artifact-body-0123456789")
	if len(bodyArr) != len(corruptArr) {
		t.Fatalf("test setup: body and corrupt lengths differ")
	}
	hashObj := core.HashBytes(bodyArr)

	setup := func(mode stcfg.HotVerifyOnReadEnum) (*Obj, core.ArtifactObj) {
		configObj := newTestConfigObj(t)
		configObj.Storage.Hot.VerifyOnRead = mode
		obj := newTestObj(t, configObj)
		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}})

		hotPath := filepath.Join(obj.hotDir, "art.zip")
		if err := os.WriteFile(hotPath, bodyArr, 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		artifactObj := core.ArtifactObj{
			MaterializerID: "universal",
			ArtifactKind:   "zip",
			ListenerID:     cListenerGlobal,
			Key:            "core-lib",
			Version:        "v1.0.0",
			BodyHash:       hashObj,
			SizeBytes:      uint64(len(bodyArr)),
			FilePath:       hotPath,
		}
		if err := obj.RegisterArtifact(ctx, artifactObj); err != nil {
			t.Fatalf("RegisterArtifact returned error: %v", err)
		}
		storedObj, ok, err := obj.getArtifact(ctx, artifactKeyFromObj(artifactObj))
		if err != nil || !ok {
			t.Fatalf("getArtifact err=%v ok=%v", err, ok)
		}
		if err = os.WriteFile(storedObj.FilePath, corruptArr, 0o644); err != nil {
			t.Fatalf("corrupt WriteFile returned error: %v", err)
		}
		return obj, storedObj
	}

	objNever, artNever := setup(stcfg.HotVerifyOnReadNever)
	fileObj, exists, err := objNever.openValidHotFile(ctx, artNever)
	if err != nil {
		t.Fatalf("never openValidHotFile returned error: %v", err)
	}
	if !exists {
		t.Fatal("never: expected corrupt-but-same-size file to be served (hash skipped)")
	}
	gotArr := make([]byte, len(corruptArr))
	n, _ := io.ReadFull(fileObj.File, gotArr)
	_ = fileObj.Close()
	if n != len(corruptArr) || !bytes.Equal(gotArr, corruptArr) {
		t.Fatalf("never: served content mismatch (fd not at offset 0?) n=%d", n)
	}

	objAlways, artAlways := setup(stcfg.HotVerifyOnReadAlways)
	fileObj2, exists2, err2 := objAlways.openValidHotFile(ctx, artAlways)
	if err2 != nil {
		t.Fatalf("always openValidHotFile returned error: %v", err2)
	}
	if exists2 {
		_ = fileObj2.Close()
		t.Fatal("always: expected corrupt file to be rejected by hash verification")
	}
}

// TestValidateSharedHotFileEnforcesVerifyOnRead covers the shared-flight waiter path: a hot file whose
// bytes drifted but whose size is unchanged passes the cheap metadata checks, so only content
// verification can catch it. Under verify_on_read=always the drift must be rejected and the file removed;
// under never the cheap size/type guard still serves it.
func TestValidateSharedHotFileEnforcesVerifyOnRead(t *testing.T) {
	ctx := context.Background()
	bodyArr := []byte("correct-artifact-body-0123456789")
	corruptArr := []byte("CORRUPT-artifact-body-0123456789")
	if len(bodyArr) != len(corruptArr) {
		t.Fatalf("test setup: body and corrupt lengths differ")
	}
	hashObj := core.HashBytes(bodyArr)

	setup := func(mode stcfg.HotVerifyOnReadEnum) (*Obj, core.ArtifactObj) {
		configObj := newTestConfigObj(t)
		configObj.Storage.Hot.VerifyOnRead = mode
		obj := newTestObj(t, configObj)
		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}})

		hotPath := filepath.Join(obj.hotDir, "shared.zip")
		if err := os.WriteFile(hotPath, bodyArr, 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		artifactObj := core.ArtifactObj{
			MaterializerID: "universal",
			ArtifactKind:   "zip",
			ListenerID:     cListenerGlobal,
			Key:            "core-lib",
			Version:        "v1.0.0",
			BodyHash:       hashObj,
			SizeBytes:      uint64(len(bodyArr)),
			FilePath:       hotPath,
		}
		if err := obj.RegisterArtifact(ctx, artifactObj); err != nil {
			t.Fatalf("RegisterArtifact returned error: %v", err)
		}
		// Same-size corruption: mirrors a shared build whose on-disk bytes drifted after registration.
		if err := os.WriteFile(hotPath, corruptArr, 0o644); err != nil {
			t.Fatalf("corrupt WriteFile returned error: %v", err)
		}
		return obj, artifactObj
	}

	// hotFileObj() copies the build metadata hash/size, so a shared HotFileObj carries the expected
	// hash; only content verification can detect the drift. Reproduce that here.
	openShared := func(art core.ArtifactObj) *HotFileObj {
		fileObj, err := os.Open(art.FilePath)
		if err != nil {
			t.Fatalf("Open returned error: %v", err)
		}
		return &HotFileObj{Path: art.FilePath, File: fileObj, SizeBytes: art.SizeBytes, BodyHash: art.BodyHash}
	}

	objAlways, artAlways := setup(stcfg.HotVerifyOnReadAlways)
	sharedAlways := openShared(artAlways)
	if err := objAlways.validateSharedHotFile(ctx, artifactKeyFromObj(artAlways), sharedAlways, artAlways); err == nil {
		_ = sharedAlways.Close()
		t.Fatal("always: shared-path validation accepted content-drifted hot file")
	}
	_ = sharedAlways.Close()
	if _, statErr := os.Stat(artAlways.FilePath); !os.IsNotExist(statErr) {
		t.Fatalf("always: corrupt hot file was not removed, stat err=%v", statErr)
	}

	objNever, artNever := setup(stcfg.HotVerifyOnReadNever)
	sharedNever := openShared(artNever)
	if err := objNever.validateSharedHotFile(ctx, artifactKeyFromObj(artNever), sharedNever, artNever); err != nil {
		_ = sharedNever.Close()
		t.Fatalf("never: shared-path validation rejected same-size file: %v", err)
	}
	_ = sharedNever.Close()
}

func TestRegisterArtifactRejectsHotPathHashMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})

	bodyArr := []byte("artifact")
	corruptArr := []byte("corrupt!")
	hotPath := filepath.Join(obj.hotDir, "manual.zip")
	if err := os.WriteFile(hotPath, corruptArr, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	err := obj.RegisterArtifact(context.Background(), core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FilePath:       hotPath,
	})
	if err == nil {
		t.Fatal("RegisterArtifact accepted hot path with wrong body hash")
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("RegisterArtifact error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckHotFile ||
		typedErr.ExpectedHash != core.HashBytes(bodyArr).Hex() ||
		typedErr.ActualHash != core.HashBytes(corruptArr).Hex() ||
		typedErr.ExpectedSize != uint64(len(bodyArr)) ||
		typedErr.ActualSize != uint64(len(corruptArr)) {
		t.Fatalf("unexpected artifact build error: %+v", typedErr)
	}
}

func TestRegisterArtifactRejectsHotPathSizeMismatch(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})

	bodyArr := []byte("artifact")
	shortArr := []byte("short")
	hotPath := filepath.Join(obj.hotDir, "manual-size.zip")
	if err := os.WriteFile(hotPath, shortArr, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	err := obj.RegisterArtifact(context.Background(), core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FilePath:       hotPath,
	})
	if err == nil {
		t.Fatal("RegisterArtifact accepted hot path with wrong size")
	}
	if !errors.Is(err, hotverify.ErrSizeMismatch) {
		t.Fatalf("RegisterArtifact error does not wrap size mismatch: %v", err)
	}
	var typedErr *stcode.ErrArtifactBuildFailedObj
	if !errors.As(err, &typedErr) {
		t.Fatalf("RegisterArtifact error=%T, want ErrArtifactBuildFailedObj: %v", err, err)
	}
	if typedErr.CheckName != cArtifactCheckHotFile ||
		typedErr.ExpectedHash != core.HashBytes(bodyArr).Hex() ||
		typedErr.ActualHash != "" ||
		typedErr.ExpectedSize != uint64(len(bodyArr)) ||
		typedErr.ActualSize != uint64(len(shortArr)) {
		t.Fatalf("unexpected artifact build error: %+v", typedErr)
	}
}

func TestRegisterArtifactRejectsUnsafeHotPath(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})

	outsidePath := filepath.Join(t.TempDir(), "artifact.zip")
	bodyArr := []byte("artifact")
	if err := os.WriteFile(outsidePath, bodyArr, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	err := obj.RegisterArtifact(context.Background(), core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FilePath:       outsidePath,
	})
	if err == nil {
		t.Fatal("RegisterArtifact accepted outside hot path")
	}

	symlinkPath := filepath.Join(obj.hotDir, "unsafe.zip")
	if err = os.Symlink(outsidePath, symlinkPath); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	err = obj.RegisterArtifact(context.Background(), core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FilePath:       symlinkPath,
	})
	if err == nil {
		t.Fatal("RegisterArtifact accepted symlink hot path")
	}

	linkRoot := filepath.Join(obj.hotDir, "linked")
	outsideDir := t.TempDir()
	if err = os.Symlink(outsideDir, linkRoot); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	parentSymlinkPath := filepath.Join(linkRoot, "artifact.zip")
	if err = os.WriteFile(parentSymlinkPath, bodyArr, 0o644); err != nil {
		t.Fatalf("WriteFile through parent symlink returned error: %v", err)
	}
	err = obj.RegisterArtifact(context.Background(), core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
		FilePath:       parentSymlinkPath,
	})
	if err == nil {
		t.Fatal("RegisterArtifact accepted hot path with symlink parent")
	}
}

func TestEnsureArtifactFileRejectsSymlinkParent(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))
	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})

	linkRoot := filepath.Join(obj.hotDir, "universal")
	outsideDir := t.TempDir()
	if err := os.Symlink(outsideDir, linkRoot); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}
	bodyArr := []byte("artifact")
	artifactObj := core.ArtifactObj{
		MaterializerID: "universal",
		ArtifactKind:   "zip",
		ListenerID:     cListenerGlobal,
		Key:            "core-lib",
		Version:        "v1.0.0",
		BodyHash:       core.HashBytes(bodyArr),
		SizeBytes:      uint64(len(bodyArr)),
	}
	if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
		t.Fatalf("RegisterArtifact returned error: %v", err)
	}
	if _, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr}); err == nil {
		t.Fatal("EnsureArtifactFile accepted hot path with symlink parent")
	}
	outsideArtifactPath := filepath.Join(outsideDir, cListenerGlobal)
	if _, err := os.Stat(outsideArtifactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("artifact escaped to symlink target or stat failed: %v", err)
	}
}

func TestHotRetainModes(t *testing.T) {
	t.Run("latest", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.Hot.Retain = stcfg.CacheRetainModeLatest
		obj := newTestObj(t, configObj)

		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("old")}})
		publishTestVersion(t, obj, "v1.1.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("new")}})
		oldBodyArr := []byte("old-artifact")
		newBodyArr := []byte("new-artifact")
		oldObj := core.ArtifactObj{MaterializerID: "universal", ArtifactKind: "zip", ListenerID: cListenerGlobal, Key: "core-lib", Version: "v1.0.0", BodyHash: core.HashBytes(oldBodyArr), SizeBytes: uint64(len(oldBodyArr))}
		newObj := core.ArtifactObj{MaterializerID: "universal", ArtifactKind: "zip", ListenerID: cListenerGlobal, Key: "core-lib", Version: "v1.1.0", BodyHash: core.HashBytes(newBodyArr), SizeBytes: uint64(len(newBodyArr))}
		if err := obj.RegisterArtifact(context.Background(), oldObj); err != nil {
			t.Fatalf("RegisterArtifact old returned error: %v", err)
		}
		if err := obj.RegisterArtifact(context.Background(), newObj); err != nil {
			t.Fatalf("RegisterArtifact new returned error: %v", err)
		}
		oldFileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(oldObj), &artifactBuilderObj{dataArr: oldBodyArr})
		if err != nil {
			t.Fatalf("EnsureArtifactFile old returned error: %v", err)
		}
		defer func() { _ = oldFileObj.Close() }()
		newFileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(newObj), &artifactBuilderObj{dataArr: newBodyArr})
		if err != nil {
			t.Fatalf("EnsureArtifactFile new returned error: %v", err)
		}
		defer func() { _ = newFileObj.Close() }()
		storedOldObj, _, err := obj.GetArtifact(context.Background(), artifactKeyFromObj(oldObj))
		if err != nil {
			t.Fatalf("GetArtifact old returned error: %v", err)
		}
		storedNewObj, _, err := obj.GetArtifact(context.Background(), artifactKeyFromObj(newObj))
		if err != nil {
			t.Fatalf("GetArtifact new returned error: %v", err)
		}
		if storedOldObj.FilePath != "" {
			t.Fatalf("old artifact retained path %q", storedOldObj.FilePath)
		}
		if storedNewObj.FilePath == "" {
			t.Fatal("latest artifact was not retained")
		}
		if err := oldFileObj.Close(); err != nil {
			t.Fatalf("old HotFile Close returned error: %v", err)
		}
		if _, err := os.Stat(oldFileObj.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("old transient artifact still exists or stat failed: %v", err)
		}
		if newFileObj.Path != storedNewObj.FilePath {
			_ = newFileObj.Close()
			t.Fatalf("latest path mismatch: returned %q stored %q", newFileObj.Path, storedNewObj.FilePath)
		}
		if err := newFileObj.Close(); err != nil {
			t.Fatalf("new HotFile Close returned error: %v", err)
		}
	})

	t.Run("none", func(t *testing.T) {
		configObj := newTestConfigObj(t)
		configObj.Storage.Hot.Retain = stcfg.CacheRetainModeNone
		obj := newTestObj(t, configObj)

		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
		bodyArr := []byte("artifact")
		artifactObj := core.ArtifactObj{MaterializerID: "universal", ArtifactKind: "zip", ListenerID: cListenerGlobal, Key: "core-lib", Version: "v1.0.0", BodyHash: core.HashBytes(bodyArr), SizeBytes: uint64(len(bodyArr))}
		if err := obj.RegisterArtifact(context.Background(), artifactObj); err != nil {
			t.Fatalf("RegisterArtifact returned error: %v", err)
		}
		fileObj, err := obj.EnsureArtifactFile(context.Background(), artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr})
		if err != nil {
			t.Fatalf("EnsureArtifactFile returned error: %v", err)
		}
		storedObj, _, err := obj.GetArtifact(context.Background(), artifactKeyFromObj(artifactObj))
		if err != nil {
			t.Fatalf("GetArtifact returned error: %v", err)
		}
		if storedObj.FilePath != "" {
			t.Fatalf("artifact retained path %q with retain=none", storedObj.FilePath)
		}
		if err := fileObj.Close(); err != nil {
			t.Fatalf("HotFile Close returned error: %v", err)
		}
		if _, err := os.Stat(fileObj.Path); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("retain=none artifact still exists or stat failed: %v", err)
		}
	})
}

func TestMarkUpstreamDeletedRecordsHistory(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("data")}})
	if err := obj.MarkUpstreamDeleted(context.Background(), "core-lib", "v1.0.0"); err != nil {
		t.Fatalf("MarkUpstreamDeleted returned error: %v", err)
	}
	versionObj, ok, err := obj.GetVersion(context.Background(), "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion ok=%v err=%v", ok, err)
	}
	if !versionObj.UpstreamDeleted {
		t.Fatal("version was not marked upstream_deleted")
	}
	if _, ok, err = obj.LatestVersion(context.Background(), "core-lib"); err != nil || ok {
		t.Fatalf("LatestVersion ok=%v err=%v, want no active version", ok, err)
	}
	inspectObj, err := obj.Inspect(context.Background())
	if err != nil {
		t.Fatalf("Inspect returned error: %v", err)
	}
	if inspectObj.HistoryEventCount != 2 {
		t.Fatalf("history events=%d, want 2", inspectObj.HistoryEventCount)
	}
}

func TestMaliciousPathRejected(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	_, err := obj.Publish(context.Background(), core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("source")),
		SourceSizeBytes: 1,
		Entries:         []core.InputEntryObj{{Path: "../escape.txt", Content: []byte("bad")}},
	})
	if err == nil {
		t.Fatal("Publish accepted path traversal")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRecoverCleansTempDir(t *testing.T) {
	configObj := newTestConfigObj(t)
	obj := newTestObj(t, configObj)
	tempPath := filepath.Join(obj.tempDir, "leftover")
	if err := os.WriteFile(tempPath, []byte("x"), 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := obj.Close(context.Background()); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	reopenedObj, err := New(context.Background(), configObj)
	if err != nil {
		t.Fatalf("reopen New returned error: %v", err)
	}
	defer func() { _ = reopenedObj.Close(context.Background()) }()

	_, err = os.Stat(filepath.Join(reopenedObj.tempDir, "leftover"))
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("temp file still exists or unexpected stat error: %v", err)
	}
}

func TestArtifactDigestsComputedAndStored(t *testing.T) {
	ctx := context.Background()
	bodyArr := []byte("artifact-body-for-digest-checks-1234567890")
	wantSha256 := sha256.Sum256(bodyArr)
	wantSha1 := sha1.Sum(bodyArr)
	bodyHashObj := core.HashBytes(bodyArr)

	assertDigests := func(t *testing.T, obj *Obj, keyObj core.ArtifactKeyObj) {
		t.Helper()
		storedObj, ok, err := obj.getArtifact(ctx, keyObj)
		if err != nil || !ok {
			t.Fatalf("getArtifact err=%v ok=%v", err, ok)
		}
		if !bytes.Equal(storedObj.BodySha256, wantSha256[:]) {
			t.Fatalf("body_sha256=%x, want %x", storedObj.BodySha256, wantSha256[:])
		}
		if !bytes.Equal(storedObj.BodySha1, wantSha1[:]) {
			t.Fatalf("body_sha1=%x, want %x", storedObj.BodySha1, wantSha1[:])
		}
	}

	t.Run("build", func(t *testing.T) {
		obj := newTestObj(t, newTestConfigObj(t))
		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}})
		artifactObj := core.ArtifactObj{
			MaterializerID: "universal",
			ArtifactKind:   "zip",
			ListenerID:     cListenerGlobal,
			Key:            "core-lib",
			Version:        "v1.0.0",
			BodyHash:       bodyHashObj,
			SizeBytes:      uint64(len(bodyArr)),
			FormatVersion:  cDefaultFormat,
		}
		if err := obj.RegisterArtifact(ctx, artifactObj); err != nil {
			t.Fatalf("RegisterArtifact returned error: %v", err)
		}
		fileObj, err := obj.EnsureArtifactFile(ctx, artifactKeyFromObj(artifactObj), &artifactBuilderObj{dataArr: bodyArr})
		if err != nil {
			t.Fatalf("EnsureArtifactFile returned error: %v", err)
		}
		_ = fileObj.Close()
		assertDigests(t, obj, artifactKeyFromObj(artifactObj))
	})

	t.Run("register_file", func(t *testing.T) {
		obj := newTestObj(t, newTestConfigObj(t))
		publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}})
		hotPath := filepath.Join(obj.hotDir, "art.zip")
		if err := os.WriteFile(hotPath, bodyArr, 0o644); err != nil {
			t.Fatalf("WriteFile returned error: %v", err)
		}
		artifactObj := core.ArtifactObj{
			MaterializerID: "universal",
			ArtifactKind:   "zip",
			ListenerID:     cListenerGlobal,
			Key:            "core-lib",
			Version:        "v1.0.0",
			BodyHash:       bodyHashObj,
			SizeBytes:      uint64(len(bodyArr)),
			FilePath:       hotPath,
		}
		if err := obj.RegisterArtifact(ctx, artifactObj); err != nil {
			t.Fatalf("RegisterArtifact returned error: %v", err)
		}
		assertDigests(t, obj, artifactKeyFromObj(artifactObj))
	})
}

func BenchmarkHotVerifyOnRead(b *testing.B) {
	ctx := context.Background()
	sizeArr := []int{64 << 10, 1 << 20, 16 << 20}
	modeArr := []struct {
		name string
		mode stcfg.HotVerifyOnReadEnum
	}{
		{"always", stcfg.HotVerifyOnReadAlways},
		{"sampled", stcfg.HotVerifyOnReadSampled},
		{"never", stcfg.HotVerifyOnReadNever},
	}
	for _, sizeBytes := range sizeArr {
		bodyArr := make([]byte, sizeBytes)
		for i := range bodyArr {
			bodyArr[i] = byte(i)
		}
		hashObj := core.HashBytes(bodyArr)
		for _, modeObj := range modeArr {
			b.Run(fmt.Sprintf("%dKiB/%s", sizeBytes>>10, modeObj.name), func(b *testing.B) {
				configObj := newTestConfigObj(b)
				configObj.Storage.Hot.MaxSize = stcfg.SizeObj(64 << 20)
				configObj.Storage.ArchiveLimits.Size.Compressed = stcfg.SizeObj(64 << 20)
				configObj.Storage.Hot.VerifyOnRead = modeObj.mode
				obj := newTestObj(b, configObj)
				publishTestVersion(b, obj, "v1.0.0", []core.InputEntryObj{{Path: "f.txt", Content: []byte("x")}})

				hotPath := filepath.Join(obj.hotDir, "bench.zip")
				if err := os.WriteFile(hotPath, bodyArr, 0o644); err != nil {
					b.Fatalf("WriteFile returned error: %v", err)
				}
				artifactObj := core.ArtifactObj{
					MaterializerID: "universal",
					ArtifactKind:   "zip",
					ListenerID:     cListenerGlobal,
					Key:            "core-lib",
					Version:        "v1.0.0",
					BodyHash:       hashObj,
					SizeBytes:      uint64(sizeBytes),
					FilePath:       hotPath,
				}
				if err := obj.RegisterArtifact(ctx, artifactObj); err != nil {
					b.Fatalf("RegisterArtifact returned error: %v", err)
				}
				storedObj, ok, err := obj.getArtifact(ctx, artifactKeyFromObj(artifactObj))
				if err != nil || !ok {
					b.Fatalf("getArtifact err=%v ok=%v", err, ok)
				}

				b.SetBytes(int64(sizeBytes))
				b.ReportAllocs()
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					fileObj, exists, openErr := obj.openValidHotFile(ctx, storedObj)
					if openErr != nil || !exists {
						b.Fatalf("openValidHotFile err=%v exists=%v", openErr, exists)
					}
					_ = fileObj.Close()
				}
			})
		}
	}
}
