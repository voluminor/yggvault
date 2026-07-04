package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
)

// // // // // // // // // //

const cSpoolCleanupTimeout = 30 * time.Second
const cMaxStagedReadBytes = uint64(1<<63 - 2)

// //

// BlobSpoolObj is a temporary directory under temp/ for staged blobs of one publication.
// Close removes it best-effort even when publish fails.
type BlobSpoolObj struct {
	rootPath   string
	closeMu    sync.Mutex
	closedFlag bool
}

type stagedBlobFileObj struct {
	filePath  string
	infoObj   os.FileInfo
	sizeBytes uint64
}

// //

func stagedPublishToPublishObj(stagedObj core.StagedPublishObj) core.PublishObj {
	return core.PublishObj{
		Key:             stagedObj.Key,
		Version:         stagedObj.Version,
		SourceHash:      stagedObj.SourceHash,
		SourceSizeBytes: stagedObj.SourceSizeBytes,
		UpstreamSeq:     stagedObj.UpstreamSeq,
		Detection:       stagedObj.Detection,
		RewriteBlobs:    stagedObj.RewriteBlobs,
		Artifacts:       stagedObj.Artifacts,
		UpstreamDeleted: stagedObj.UpstreamDeleted,
		EventType:       stagedObj.EventType,
		EventMessage:    stagedObj.EventMessage,
		ReleaseNotes:    stagedObj.ReleaseNotes,
		HealPending:     stagedObj.HealPending,
		UpstreamRef:     stagedObj.UpstreamRef,
		VerifiedTS:      stagedObj.VerifiedTS,
	}
}

// symlinkTargetFromBytes bounds a symlink target's length. Hygiene and the per-path escape check run in
// verifySymlinkTargets, the authoritative publish gate that knows each symlink's final path (a target is
// resolved relative to the symlink's own directory, so it may legitimately contain "..").
func symlinkTargetFromBytes(dataArr []byte, maxPathBytes uint) error {
	if maxPathBytes > 0 && uint(len(dataArr)) > maxPathBytes {
		return errors.New("symlink target exceeds path limit")
	}
	return nil
}

func (obj *Obj) cleanupSpool(spoolObj *BlobSpoolObj) {
	if spoolObj == nil {
		return
	}
	ctx, cancelFunc := context.WithTimeout(obj.rootCtx, cSpoolCleanupTimeout)
	defer cancelFunc()
	_ = spoolObj.Close(ctx)
}

func (obj *Obj) validateStagedBlobPath(spoolObj *BlobSpoolObj, filePath string) (string, os.FileInfo, *os.File, error) {
	if spoolObj == nil {
		return "", nil, nil, errors.New("blob spool is nil")
	}
	spoolObj.closeMu.Lock()
	closedFlag := spoolObj.closedFlag
	rootPath := spoolObj.rootPath
	spoolObj.closeMu.Unlock()
	if closedFlag {
		return "", nil, nil, errors.New("blob spool is closed")
	}
	if strings.TrimSpace(rootPath) == "" {
		return "", nil, nil, errors.New("blob spool root is empty")
	}
	rootInfoObj, err := os.Lstat(rootPath)
	if err != nil {
		return "", nil, nil, err
	}
	if rootInfoObj.Mode()&os.ModeSymlink != 0 || !rootInfoObj.IsDir() {
		return "", nil, nil, errors.New("blob spool root is not a regular directory")
	}
	if filePath == "" {
		return "", nil, nil, errors.New("staged blob path is empty")
	}
	pathText, err := pathInside(rootPath, filePath, "staged blob path is outside spool")
	if err != nil {
		return "", nil, nil, err
	}
	rootAbsPath, err := filepath.Abs(rootPath)
	if err != nil {
		return "", nil, nil, err
	}
	if filepath.Dir(pathText) != rootAbsPath {
		return "", nil, nil, errors.New("staged blob path must be a direct spool child")
	}
	infoObj, err := os.Lstat(pathText)
	if err != nil {
		return "", nil, nil, err
	}
	if !osfs.IsRegularFile(infoObj) {
		return "", nil, nil, errors.New("staged blob path is not a regular file")
	}
	fileObj, err := osfs.OpenNoFollow(pathText)
	if err != nil {
		return "", nil, nil, err
	}
	fdInfoObj, err := fileObj.Stat()
	if err != nil {
		_ = fileObj.Close()
		return "", nil, nil, err
	}
	if !osfs.IsRegularFile(fdInfoObj) || !os.SameFile(infoObj, fdInfoObj) {
		_ = fileObj.Close()
		return "", nil, nil, errors.New("staged blob path changed during validation")
	}
	return pathText, infoObj, fileObj, nil
}

func verifyStagedBlobFile(ctx context.Context, fileObj *os.File, infoObj os.FileInfo, hashObj core.HashObj, sizeBytes uint64, bufferArr []byte) error {
	if uint64(infoObj.Size()) != sizeBytes {
		return fmt.Errorf("staged blob %s size mismatch", hashObj.Hex())
	}
	if _, err := fileObj.Seek(0, io.SeekStart); err != nil {
		return err
	}

	hasherObj := blake3.New()
	var readBytes uint64
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		n, readErr := fileObj.Read(bufferArr)
		if n > 0 {
			readBytes += uint64(n)
			_, _ = hasherObj.Write(bufferArr[:n])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return readErr
		}
	}
	if readBytes != sizeBytes {
		return fmt.Errorf("staged blob %s size mismatch", hashObj.Hex())
	}
	if actualObj := core.HashFromHasher(hasherObj); actualObj != hashObj {
		return fmt.Errorf("staged blob %s hash mismatch", hashObj.Hex())
	}
	_, err := fileObj.Seek(0, io.SeekStart)
	return err
}

func readStagedFileBytes(fileObj *os.File, sizeBytes uint64) ([]byte, error) {
	if sizeBytes > cMaxStagedReadBytes {
		return nil, errors.New("staged file is too large to read")
	}
	if _, err := fileObj.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	readerObj := io.LimitReader(fileObj, int64(sizeBytes)+1)
	dataArr, err := io.ReadAll(readerObj)
	if err != nil {
		return nil, err
	}
	if uint64(len(dataArr)) != sizeBytes {
		return nil, errors.New("staged file size changed")
	}
	if _, err = fileObj.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return dataArr, nil
}

func validateStagedSymlinkFile(fileObj *os.File, sizeBytes uint64, maxPathBytes uint) error {
	if uint64(maxPathBytes) > 0 && sizeBytes > uint64(maxPathBytes) {
		return errors.New("symlink target exceeds path limit")
	}
	dataArr, err := readStagedFileBytes(fileObj, sizeBytes)
	if err != nil {
		return err
	}
	return symlinkTargetFromBytes(dataArr, maxPathBytes)
}

func (obj *Obj) verifySymlinkTargets(ctx context.Context, treeArr []core.TreeEntryObj, contentByHashObj map[core.HashObj][]byte, maxPathBytes uint) error {
	targetByHashObj := make(map[core.HashObj][]byte)
	for i := range treeArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entryObj := treeArr[i]
		if entryObj.Mode != cModeSymlink {
			continue
		}
		targetArr, ok := targetByHashObj[entryObj.BlobHash]
		if !ok {
			loadedArr, err := obj.loadSymlinkTarget(entryObj, contentByHashObj)
			if err != nil {
				return err
			}
			targetArr = loadedArr
			targetByHashObj[entryObj.BlobHash] = targetArr
		}
		if err := symlinkTargetFromBytes(targetArr, maxPathBytes); err != nil {
			return err
		}
		// Escape checks are per path: the same target bytes can be safe or unsafe at different depths.
		if err := util.SymlinkTargetWithinRoot(entryObj.Path, string(targetArr)); err != nil {
			return fmt.Errorf("symlink target for %s: %w", entryObj.Path, err)
		}
	}
	return nil
}

// loadSymlinkTarget reads symlink target bytes from staged content or Pebble and verifies the size.
func (obj *Obj) loadSymlinkTarget(entryObj core.TreeEntryObj, contentByHashObj map[core.HashObj][]byte) ([]byte, error) {
	if contentArr, ok := contentByHashObj[entryObj.BlobHash]; ok {
		if uint64(len(contentArr)) != entryObj.SizeBytes {
			return nil, fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
		}
		return contentArr, nil
	}
	var out []byte
	err := obj.pebbleStoreObj.UseBlob(entryObj.BlobHash, func(blobArr []byte) error {
		if uint64(len(blobArr)) != entryObj.SizeBytes {
			return fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
		}
		out = append([]byte(nil), blobArr...)
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("missing referenced blob %s: %w", entryObj.BlobHash.Hex(), err)
	}
	return out, nil
}

func stagedBlobSizes(blobFileObj map[core.HashObj]stagedBlobFileObj) map[core.HashObj]uint64 {
	resultObj := make(map[core.HashObj]uint64, len(blobFileObj))
	for hashObj, blobObj := range blobFileObj {
		resultObj[hashObj] = blobObj.sizeBytes
	}
	return resultObj
}

func (obj *Obj) openPreparedStagedBlob(spoolObj *BlobSpoolObj, blobObj stagedBlobFileObj) (*os.File, uint64, error) {
	if blobObj.filePath == "" || blobObj.infoObj == nil {
		return nil, 0, errors.New("staged blob file was not prepared")
	}
	pathText, infoObj, fileObj, err := obj.validateStagedBlobPath(spoolObj, blobObj.filePath)
	if err != nil {
		return nil, 0, err
	}
	if pathText != blobObj.filePath || !os.SameFile(infoObj, blobObj.infoObj) {
		_ = fileObj.Close()
		return nil, 0, errors.New("staged blob file changed")
	}
	if uint64(infoObj.Size()) != blobObj.sizeBytes {
		_ = fileObj.Close()
		return nil, 0, errors.New("staged blob size changed")
	}
	return fileObj, blobObj.sizeBytes, nil
}

func (obj *Obj) stagedBlobOpenFunc(spoolObj *BlobSpoolObj, blobFileObj map[core.HashObj]stagedBlobFileObj) func(core.HashObj) (*os.File, uint64, error) {
	return func(hashObj core.HashObj) (*os.File, uint64, error) {
		blobObj, ok := blobFileObj[hashObj]
		if !ok {
			return nil, 0, errors.New("pending staged blob file is missing")
		}
		return obj.openPreparedStagedBlob(spoolObj, blobObj)
	}
}

// RootPath returns the spool directory path for callers writing staged blobs.
func (obj *BlobSpoolObj) RootPath() string {
	if obj == nil {
		return ""
	}
	return obj.rootPath
}

// Close idempotently removes the spool directory and all contents.
// Cleanup is best-effort and independent of ctx cancellation: an aborted ingest cycle or shutdown
// must still delete the temp blob-spool directory, otherwise it leaks on disk until the next start.
func (obj *BlobSpoolObj) Close(_ context.Context) error {
	if obj == nil {
		return nil
	}
	obj.closeMu.Lock()
	defer obj.closeMu.Unlock()
	if obj.closedFlag {
		return nil
	}
	if err := os.RemoveAll(obj.rootPath); err != nil {
		return err
	}
	obj.closedFlag = true
	return nil
}

func (obj *Obj) normalizeStagedEntries(entriesArr []core.StagedEntryObj, key string, version string, maxPathBytes uint, maxFileBytes uint64, maxFiles uint, maxUnpackedBytes uint64) ([]core.TreeEntryObj, map[core.HashObj]struct{}, map[core.HashObj]struct{}, error) {
	if len(entriesArr) == 0 {
		return nil, nil, nil, errors.New("entries must not be empty")
	}
	filesCount := uint(len(entriesArr))
	if len(entriesArr) > treecodec.MaxEntries {
		return nil, nil, nil, newArchiveLimitsErr(nil, cArchiveCheckEntryHardCap, filesCount, key, treecodec.MaxEntries, maxFileBytes, maxPathBytes, maxUnpackedBytes, 0, 0, 0, version)
	}
	if maxFiles > 0 && filesCount > maxFiles {
		return nil, nil, nil, newArchiveLimitsErr(nil, cArchiveCheckFileCount, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, 0, 0, 0, version)
	}

	treeArr := make([]core.TreeEntryObj, 0, len(entriesArr))
	usedHashObj := make(map[core.HashObj]struct{}, len(entriesArr))
	symlinkHashObj := make(map[core.HashObj]struct{})
	var totalBytes uint64
	for i := range entriesArr {
		entryObj := entriesArr[i]
		pathText, err := validateEntryPath(entryObj.Path, maxPathBytes)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("entry %q: %w", entryObj.Path, err)
		}
		modeText, err := validateEntryMode(entryObj.Mode)
		if err != nil {
			return nil, nil, nil, err
		}
		if entryObj.BlobHash.IsZero() {
			return nil, nil, nil, fmt.Errorf("entry %s has empty blob hash", pathText)
		}
		if maxFileBytes > 0 && entryObj.SizeBytes > maxFileBytes {
			return nil, nil, nil, newArchiveLimitsErr(nil, cArchiveCheckFileBytes, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, entryObj.SizeBytes, uint(len(pathText)), 0, version)
		}
		attemptedBytes, overflowFlag := saturatingAddUint64(totalBytes, entryObj.SizeBytes)
		if maxUnpackedBytes > 0 && (overflowFlag || attemptedBytes > maxUnpackedBytes) {
			return nil, nil, nil, newArchiveLimitsErr(nil, cArchiveCheckUnpackedSize, filesCount, key, maxFiles, maxFileBytes, maxPathBytes, maxUnpackedBytes, entryObj.SizeBytes, uint(len(pathText)), attemptedBytes, version)
		}
		totalBytes = attemptedBytes
		usedHashObj[entryObj.BlobHash] = struct{}{}
		if modeText == cModeSymlink {
			symlinkHashObj[entryObj.BlobHash] = struct{}{}
		}
		treeArr = append(treeArr, core.TreeEntryObj{
			Path:      pathText,
			Mode:      modeText,
			SizeBytes: entryObj.SizeBytes,
			BlobHash:  entryObj.BlobHash,
		})
	}
	return treeArr, usedHashObj, symlinkHashObj, nil
}

func (obj *Obj) prepareStagedBlobs(ctx context.Context, spoolObj *BlobSpoolObj, blobArr []core.StagedBlobObj, usedHashObj map[core.HashObj]struct{}, symlinkHashObj map[core.HashObj]struct{}, maxPathBytes uint) (map[core.HashObj]stagedBlobFileObj, error) {
	resultObj := make(map[core.HashObj]stagedBlobFileObj, len(blobArr))
	bufferArr := make([]byte, 64*1024)
	for i := range blobArr {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}
		blobObj := blobArr[i]
		if blobObj.BlobHash.IsZero() {
			return nil, errors.New("staged blob has empty hash")
		}
		if _, ok := usedHashObj[blobObj.BlobHash]; !ok {
			return nil, fmt.Errorf("staged blob %s is not referenced by tree", blobObj.BlobHash.Hex())
		}
		if existingObj, ok := resultObj[blobObj.BlobHash]; ok {
			if existingObj.sizeBytes != blobObj.SizeBytes {
				return nil, fmt.Errorf("staged blob %s has inconsistent sizes", blobObj.BlobHash.Hex())
			}
			continue
		}
		pathText, infoObj, fileObj, err := obj.validateStagedBlobPath(spoolObj, blobObj.FilePath)
		if err != nil {
			return nil, err
		}
		if err = verifyStagedBlobFile(ctx, fileObj, infoObj, blobObj.BlobHash, blobObj.SizeBytes, bufferArr); err != nil {
			_ = fileObj.Close()
			return nil, err
		}
		if _, ok := symlinkHashObj[blobObj.BlobHash]; ok {
			if err = validateStagedSymlinkFile(fileObj, blobObj.SizeBytes, maxPathBytes); err != nil {
				_ = fileObj.Close()
				return nil, err
			}
		}
		if err = fileObj.Close(); err != nil {
			return nil, err
		}
		resultObj[blobObj.BlobHash] = stagedBlobFileObj{
			filePath:  pathText,
			infoObj:   infoObj,
			sizeBytes: blobObj.SizeBytes,
		}
	}
	return resultObj, nil
}

func (obj *Obj) verifyStagedReferences(ctx context.Context, treeArr []core.TreeEntryObj, blobFileObj map[core.HashObj]stagedBlobFileObj, symlinkHashObj map[core.HashObj]struct{}, maxPathBytes uint) error {
	verifiedObj := make(map[core.HashObj]uint64, len(treeArr))
	for i := range treeArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entryObj := treeArr[i]
		if sizeBytes, ok := verifiedObj[entryObj.BlobHash]; ok {
			if sizeBytes != entryObj.SizeBytes {
				return fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
			}
			continue
		}
		if fileObj, ok := blobFileObj[entryObj.BlobHash]; ok {
			if fileObj.sizeBytes != entryObj.SizeBytes {
				return fmt.Errorf("referenced staged blob %s size mismatch", entryObj.BlobHash.Hex())
			}
			verifiedObj[entryObj.BlobHash] = entryObj.SizeBytes
			continue
		}
		err := obj.pebbleStoreObj.UseBlob(entryObj.BlobHash, func(blobArr []byte) error {
			if uint64(len(blobArr)) != entryObj.SizeBytes {
				return fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
			}
			if _, ok := symlinkHashObj[entryObj.BlobHash]; ok {
				return symlinkTargetFromBytes(blobArr, maxPathBytes)
			}
			return nil
		})
		if err != nil {
			return fmt.Errorf("missing referenced blob %s: %w", entryObj.BlobHash.Hex(), err)
		}
		verifiedObj[entryObj.BlobHash] = entryObj.SizeBytes
	}
	return nil
}

// CanonicalTree normalizes staged entries and computes the tree_hash exactly like PublishStaged.
// Rescan uses it for cheap version-skip before materialization and reuses canonical entries for artifact builds.
// It is a pure calculation over config and input entries and does not touch disk.
func (obj *Obj) CanonicalTree(stagedEntries []core.StagedEntryObj, key string, version string) ([]core.TreeEntryObj, core.HashObj, error) {
	maxPathBytes := obj.configObj.Storage.ArchiveLimits.Entries.PathBytes
	maxFileBytes := obj.maxBlobBytes()
	maxFiles := obj.configObj.Storage.ArchiveLimits.Entries.Count
	maxUnpackedBytes := uint64(obj.configObj.Storage.ArchiveLimits.Size.Unpacked)
	treeArr, _, _, err := obj.normalizeStagedEntries(stagedEntries, key, version, maxPathBytes, maxFileBytes, maxFiles, maxUnpackedBytes)
	if err != nil {
		return nil, core.HashObj{}, err
	}
	_, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		return nil, core.HashObj{}, err
	}
	return treeArr, treeHashObj, nil
}

// NewBlobSpool creates a temp/ spool for staged blobs before PublishStaged.
// Callers must close the spool; PublishStaged does it through cleanupSpool.
func (obj *Obj) NewBlobSpool(ctx context.Context) (*BlobSpoolObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	rootPath, err := os.MkdirTemp(obj.tempDir, "blob-spool-*")
	if err != nil {
		return nil, err
	}
	if err = validateStorageDir(rootPath); err != nil {
		_ = os.RemoveAll(rootPath)
		return nil, err
	}
	return &BlobSpoolObj{rootPath: rootPath}, nil
}

// PublishStaged stores a version from staged spool blobs.
// It normalizes the tree, verifies blobs and symlinks, writes missing Pebble objects under durable budget, and commits.
// The spool is removed in all cases; the tree is written last, so the version is unreachable before commit.
func (obj *Obj) PublishStaged(ctx context.Context, spoolObj *BlobSpoolObj, stagedObj core.StagedPublishObj) (core.PublishResultObj, error) {
	defer obj.cleanupSpool(spoolObj)

	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	defer releaseFunc()

	publishObj := stagedPublishToPublishObj(stagedObj)
	if err = obj.validatePublishCommon(publishObj); err != nil {
		return core.PublishResultObj{}, err
	}

	if err = obj.acquirePublishSlot(ctx); err != nil {
		return core.PublishResultObj{}, err
	}
	defer obj.releasePublishSlot()

	maxPathBytes := obj.configObj.Storage.ArchiveLimits.Entries.PathBytes
	maxFileBytes := obj.maxBlobBytes()
	maxFiles := obj.configObj.Storage.ArchiveLimits.Entries.Count
	maxUnpackedBytes := uint64(obj.configObj.Storage.ArchiveLimits.Size.Unpacked)
	if err = validatePublishSideArrays(publishObj, maxFiles); err != nil {
		return core.PublishResultObj{}, err
	}
	treeArr, usedHashObj, symlinkHashObj, err := obj.normalizeStagedEntries(stagedObj.Entries, publishObj.Key, publishObj.Version, maxPathBytes, maxFileBytes, maxFiles, maxUnpackedBytes)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	blobFileObj, err := obj.prepareStagedBlobs(ctx, spoolObj, stagedObj.Blobs, usedHashObj, symlinkHashObj, maxPathBytes)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.validatePublishMetadata(ctx, &publishObj); err != nil {
		return core.PublishResultObj{}, err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	protectedObj := protectTreeObjects(treeHashObj, treeArr)
	if err = obj.verifyStagedReferences(ctx, treeArr, blobFileObj, symlinkHashObj, maxPathBytes); err != nil {
		return core.PublishResultObj{}, err
	}
	blobSizeObj := stagedBlobSizes(blobFileObj)
	pendingObj, err := obj.pebbleStoreObj.PendingObjects(ctx, blobSizeObj, treeDataArr, treeHashObj)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.ensureDurableBudgetProtectedLocked(ctx, pendingObj.SizeBytes, protectedObj); err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.pebbleStoreObj.WriteMissingObjectsFromFileOpener(ctx, pendingObj, obj.stagedBlobOpenFunc(spoolObj, blobFileObj), blobSizeObj, treeDataArr, treeHashObj); err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}
	if err = obj.enforceDurableHardLimitLocked(ctx); err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}

	return obj.commitPublish(ctx, publishObj, treeArr, treeHashObj, pendingObj)
}
