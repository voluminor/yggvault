package storage

import (
	"context"
	"crypto/sha1"
	"crypto/sha256"
	"errors"
	"hash"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/osfs"
)

// // // // // // // // // //

var errArtifactOversize = errors.New("artifact output exceeds expected size")

var errArtifactBuildStalled = errors.New("artifact build stalled: no write progress within idle budget")

var cArtifactBuildIdle = 90 * time.Second

// //

type hashWriterObj struct {
	ctx            context.Context
	writerObj      io.Writer
	hashObj        *blake3.Hasher
	sha256Obj      hash.Hash
	sha1Obj        hash.Hash
	sizeBytes      uint64
	maxBytes       uint64
	attemptedBytes uint64
	idleTimer      *time.Timer
	idleBudget     time.Duration
}

// Write writes to the file and computes all digests in a single pass.
// Every call checks context cancellation and the expected size cap.
func (obj *hashWriterObj) Write(dataArr []byte) (int, error) {
	select {
	case <-obj.ctx.Done():
		return 0, obj.ctx.Err()
	default:
	}
	attemptedBytes, overflowFlag := saturatingAddUint64(obj.sizeBytes, uint64(len(dataArr)))
	if overflowFlag || attemptedBytes > obj.maxBytes {
		obj.attemptedBytes = attemptedBytes
		return 0, errArtifactOversize
	}
	n, err := obj.writerObj.Write(dataArr)
	if n > 0 {
		if obj.idleTimer != nil {
			obj.idleTimer.Reset(obj.idleBudget)
		}
		_, _ = obj.hashObj.Write(dataArr[:n])
		_, _ = obj.sha256Obj.Write(dataArr[:n])
		_, _ = obj.sha1Obj.Write(dataArr[:n])
		obj.sizeBytes += uint64(n)
	}
	return n, err
}

func (obj *Obj) acquireBuildSlot(ctx context.Context) error {
	select {
	case obj.buildSem <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (obj *Obj) releaseBuildSlot() {
	<-obj.buildSem
}

func (obj *Obj) acquireBuildSlots(ctx context.Context, countValue int) (int, error) {
	acquiredCount := 0
	for acquiredCount < countValue {
		if err := obj.acquireBuildSlot(ctx); err != nil {
			obj.releaseBuildSlots(acquiredCount)
			return 0, err
		}
		acquiredCount++
	}
	return acquiredCount, nil
}

func (obj *Obj) releaseBuildSlots(countValue int) {
	for i := 0; i < countValue; i++ {
		obj.releaseBuildSlot()
	}
}

// Close closes the descriptor and runs cleanup for the shared-file refcount.
// Errors are joined; nil receiver and repeated calls are safe.
func (obj *HotFileObj) Close() error {
	if obj == nil {
		return nil
	}
	var err error
	if obj.File != nil {
		err = obj.File.Close()
		obj.File = nil
	}
	cleanupFunc := obj.cleanup
	obj.cleanup = nil
	if cleanupFunc != nil {
		err = errors.Join(err, cleanupFunc())
	}
	return err
}

func (obj *hotSharedFileObj) cleanupUnused() error {
	var err error
	obj.onceObj.Do(func() {
		if obj.cleanup != nil {
			err = obj.cleanup()
		}
	})
	return err
}

func (obj *hotSharedFileObj) hotFileObj() (*HotFileObj, error) {
	var releaseFunc func() error
	if obj.retain != nil {
		releaseFunc = obj.retain()
	}
	fileObj, err := osfs.OpenNoFollow(obj.path)
	if err != nil {
		if releaseFunc != nil {
			_ = releaseFunc()
		}
		return nil, err
	}
	obj.refs.Add(1)
	resultObj := &HotFileObj{
		Path:      obj.path,
		File:      fileObj,
		SizeBytes: obj.sizeBytes,
		BodyHash:  obj.bodyHash,
	}
	if obj.cleanup != nil || releaseFunc != nil {
		resultObj.cleanup = func() error {
			var err error
			if releaseFunc != nil {
				err = errors.Join(err, releaseFunc())
			}
			if obj.refs.Add(-1) > 0 {
				return err
			}
			return errors.Join(err, obj.cleanupUnused())
		}
	}
	return resultObj, nil
}

func (obj *Obj) buildHotFile(ctx context.Context, keyObj core.ArtifactKeyObj, artifactObj core.ArtifactObj, builderObj ArtifactBuilderInterface) (*hotSharedFileObj, error) {
	if builderObj == nil {
		return nil, errors.New("artifact builder is nil")
	}

	if err := obj.acquireBuildSlot(ctx); err != nil {
		return nil, err
	}
	defer obj.releaseBuildSlot()

	tempFileObj, err := os.CreateTemp(obj.tempDir, "artifact-*")
	if err != nil {
		return nil, err
	}
	tempPath := tempFileObj.Name()
	removeTemp := true
	defer func() {
		_ = tempFileObj.Close()
		if removeTemp {
			_ = os.Remove(tempPath)
		}
	}()

	hashObj := blake3.New()
	buildCtx, cancelBuild := context.WithCancelCause(ctx)
	defer cancelBuild(nil)
	idleTimer := time.AfterFunc(cArtifactBuildIdle, func() { cancelBuild(errArtifactBuildStalled) })
	defer idleTimer.Stop()
	writerObj := &hashWriterObj{ctx: buildCtx, writerObj: tempFileObj, hashObj: hashObj, sha256Obj: sha256.New(), sha1Obj: sha1.New(), maxBytes: min(artifactObj.SizeBytes, obj.maxArtifactBytes()), idleTimer: idleTimer, idleBudget: cArtifactBuildIdle}
	buildSnapshotObj := obj.pebbleStoreObj.NewSnapshot()
	defer func() { _ = buildSnapshotObj.Close() }()
	if err = builderObj.Build(withReadSnapshot(buildCtx, buildSnapshotObj), writerObj); err != nil {
		_ = tempFileObj.Close()
		if errors.Is(err, errArtifactOversize) {
			return nil, newArtifactBuildErr(artifactKeyFromObj(artifactObj), err, cArtifactCheckOutputSize, "", "", artifactObj.SizeBytes, writerObj.attemptedBytes)
		}
		if stallErr := context.Cause(buildCtx); errors.Is(stallErr, errArtifactBuildStalled) {
			return nil, stallErr
		}
		return nil, err
	}
	idleTimer.Stop()
	select {
	case <-ctx.Done():
		_ = tempFileObj.Close()
		return nil, ctx.Err()
	default:
	}
	if err = tempFileObj.Sync(); err != nil {
		_ = tempFileObj.Close()
		return nil, err
	}
	if err = tempFileObj.Close(); err != nil {
		return nil, err
	}

	actualHashObj := core.HashFromHasher(hashObj)
	artifactKeyObj := artifactKeyFromObj(artifactObj)
	if actualHashObj != artifactObj.BodyHash {
		return nil, newArtifactBuildErr(artifactKeyObj, nil, cArtifactCheckBodyHash, artifactObj.BodyHash.Hex(), actualHashObj.Hex(), 0, 0)
	}
	if writerObj.sizeBytes != artifactObj.SizeBytes {
		return nil, newArtifactBuildErr(artifactKeyObj, nil, cArtifactCheckBodySize, "", "", artifactObj.SizeBytes, writerObj.sizeBytes)
	}
	artifactObj.BodySha256 = writerObj.sha256Obj.Sum(nil)
	artifactObj.BodySha1 = writerObj.sha1Obj.Sum(nil)

	retainFlag, err := obj.shouldRetainArtifact(ctx, keyObj)
	if err != nil {
		return nil, err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	hotTargetText := hotArtifactPath(obj.hotDir, artifactObj)
	if !retainFlag {
		hotTargetText += "." + filepath.Base(tempPath)
	}
	targetPath, err := obj.validateHotTargetPath(hotTargetText)
	if err != nil {
		return nil, err
	}
	if err = os.MkdirAll(filepath.Dir(targetPath), 0o700); err != nil {
		return nil, err
	}
	if _, err = obj.validateHotTargetPath(targetPath); err != nil {
		return nil, err
	}
	if err = os.Rename(tempPath, targetPath); err != nil {
		return nil, err
	}
	removeTemp = false
	if dirObj, openErr := os.Open(filepath.Dir(targetPath)); openErr == nil {
		_ = dirObj.Sync()
		_ = dirObj.Close()
	}

	storedPath := ""
	if retainFlag {
		storedPath = targetPath
		if err = obj.enforceHotBudgetProtectedLocked(ctx, targetPath, true); err != nil {
			_ = obj.removeHotPath(targetPath)
			return nil, err
		}
	} else {
		obj.invalidateHotBytesCacheLocked()
	}
	if err = obj.indexObj.UpdateArtifactPath(ctx, artifactObj, storedPath); err != nil {
		_ = obj.removeHotPath(targetPath)
		return nil, err
	}
	sharedObj := &hotSharedFileObj{path: targetPath, sizeBytes: artifactObj.SizeBytes, bodyHash: artifactObj.BodyHash}
	if retainFlag {
		sharedObj.retain = func() func() error {
			return obj.retainHotPath(targetPath)
		}
	} else {
		sharedObj.cleanup = func() error {
			return obj.removeHotPath(targetPath)
		}
	}
	return sharedObj, nil
}
