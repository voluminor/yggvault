package storage

import (
	"context"
	"errors"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const cMaxArtifactsPerPublish = 1024

// //

func (obj *Obj) acquirePublishSlot(ctx context.Context) error {
	select {
	case obj.publishSem <- struct{}{}:
		return nil
	case <-obj.rootCtx.Done():
		return errors.New("storage is closed")
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (obj *Obj) releasePublishSlot() {
	<-obj.publishSem
}

func newPublishLimitsErr(publishObj core.PublishObj, checkName string, countValue uint, maxCount uint) error {
	return stcode.NewErrArchiveLimitExceeded(nil, checkName, publishObj.Key, uint64(maxCount), uint64(countValue), publishObj.Version)
}

func validatePublishSideArrays(publishObj core.PublishObj, maxFiles uint) error {
	if len(publishObj.RewriteBlobs) > treecodec.MaxEntries {
		return newPublishLimitsErr(publishObj, cPublishCheckRewriteSetCount, uint(len(publishObj.RewriteBlobs)), treecodec.MaxEntries)
	}
	if maxFiles > 0 && uint(len(publishObj.RewriteBlobs)) > maxFiles {
		return newPublishLimitsErr(publishObj, cPublishCheckRewriteSetCount, uint(len(publishObj.RewriteBlobs)), maxFiles)
	}
	if len(publishObj.Artifacts) > cMaxArtifactsPerPublish {
		return newPublishLimitsErr(publishObj, cPublishCheckArtifactMetadataCount, uint(len(publishObj.Artifacts)), cMaxArtifactsPerPublish)
	}
	return nil
}

// //

func (obj *Obj) validatePublishCommon(publishObj core.PublishObj) error {
	if err := validateKey(publishObj.Key); err != nil {
		return err
	}
	if err := validateVersion(publishObj.Version); err != nil {
		return err
	}
	if publishObj.SourceHash.IsZero() {
		return errors.New("source hash is empty")
	}
	if err := validateUpstreamRef(publishObj.UpstreamRef); err != nil {
		return err
	}
	maxArchiveBytes := uint64(obj.configObj.Storage.ArchiveLimits.Size.Compressed)
	if maxArchiveBytes > 0 && publishObj.SourceSizeBytes > maxArchiveBytes {
		return stcode.NewErrArchiveLimitExceeded(nil, "archive_size", publishObj.Key, maxArchiveBytes, publishObj.SourceSizeBytes, publishObj.Version)
	}
	return nil
}

func (obj *Obj) validatePublishMetadata(ctx context.Context, publishObj *core.PublishObj) error {
	var err error
	if publishObj.Detection.EvidenceJSON, err = validateEvidenceJSON(publishObj.Detection.EvidenceJSON); err != nil {
		return err
	}
	for _, hashObj := range publishObj.RewriteBlobs {
		if hashObj.IsZero() {
			return errors.New("rewrite_set contains empty blob hash")
		}
	}
	if publishObj.Artifacts, err = obj.prepareArtifacts(ctx, publishObj.Key, publishObj.Version, publishObj.Artifacts); err != nil {
		return err
	}
	if publishObj.EventType, err = validateEventType(publishObj.EventType); err != nil {
		return err
	}
	if err = validateEventMessage(publishObj.EventMessage); err != nil {
		return err
	}
	return validateReleaseNotes(publishObj.ReleaseNotes)
}

func (obj *Obj) commitPublish(ctx context.Context, publishObj core.PublishObj, treeArr []core.TreeEntryObj, treeHashObj core.HashObj, pendingObj pebblestore.PendingObjectsObj) (core.PublishResultObj, error) {
	existingObj, existingFlag, err := obj.indexObj.GetVersion(ctx, publishObj.Key, publishObj.Version)
	if err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}
	if existingFlag && existingObj.TreeHash == treeHashObj {
		return core.PublishResultObj{
			Key:      publishObj.Key,
			Version:  publishObj.Version,
			TreeHash: treeHashObj,
			Skipped:  true,
		}, nil
	}

	var existingTreeArr []core.TreeEntryObj
	if existingFlag {
		existingTreeArr, err = obj.readTreeInternal(existingObj.TreeHash)
		if err != nil {
			return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
		}
	}

	var resultObj core.PublishResultObj
	err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		var txErr error
		resultObj, txErr = obj.publishTx(ctx, txObj, publishObj, treeArr, treeHashObj, existingObj, existingFlag, existingTreeArr)
		return txErr
	})
	if err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}
	if existingFlag && existingObj.TreeHash != treeHashObj && obj.configObj.HistoryPolicy.Mutation == stcfg.HistoryMutationModeOverwrite {
		_ = obj.cleanupDeletedObjectsLocked(ctx, existingObj.TreeHash, existingTreeArr)
	}
	return resultObj, nil
}

// //

// Publish stores a version from inline entries, with content already in memory.
// It normalizes the tree, verifies links under writeMu, writes missing objects within durable budget, and commits metadata.
// The tree object is written last, so the version is unreachable until commit; identical republishes return Skipped.
func (obj *Obj) Publish(ctx context.Context, publishObj core.PublishObj) (core.PublishResultObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	defer releaseFunc()

	if err := obj.validatePublishCommon(publishObj); err != nil {
		return core.PublishResultObj{}, err
	}

	if err := obj.acquirePublishSlot(ctx); err != nil {
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
	treeArr, contentByHashObj, err := normalizeInputEntries(publishObj.Entries, publishObj.Key, publishObj.Version, maxPathBytes, maxFileBytes, maxFiles, maxUnpackedBytes)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	treeDataArr, treeHashObj, err := treecodec.Encode(treeArr)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.validatePublishMetadata(ctx, &publishObj); err != nil {
		return core.PublishResultObj{}, err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	protectedObj := protectTreeObjects(treeHashObj, treeArr)
	if err = obj.verifySymlinkTargets(ctx, treeArr, contentByHashObj, maxPathBytes); err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.pebbleStoreObj.VerifyReferences(ctx, treeArr, contentByHashObj); err != nil {
		return core.PublishResultObj{}, err
	}
	blobSizeObj := make(map[core.HashObj]uint64, len(contentByHashObj))
	for hashObj, contentArr := range contentByHashObj {
		blobSizeObj[hashObj] = uint64(len(contentArr))
	}
	pendingObj, err := obj.pebbleStoreObj.PendingObjects(ctx, blobSizeObj, treeDataArr, treeHashObj)
	if err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.ensureDurableBudgetProtectedLocked(ctx, pendingObj.SizeBytes, protectedObj); err != nil {
		return core.PublishResultObj{}, err
	}
	if err = obj.pebbleStoreObj.WriteMissingObjects(ctx, pendingObj, contentByHashObj, treeDataArr, treeHashObj); err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}
	if err = obj.enforceDurableHardLimitLocked(ctx); err != nil {
		return core.PublishResultObj{}, obj.cleanupPublishFailure(pendingObj, err)
	}

	return obj.commitPublish(ctx, publishObj, treeArr, treeHashObj, pendingObj)
}

// GetVersion returns version metadata by key and version; the second result reports whether it exists.
func (obj *Obj) GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.VersionObj{}, false, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return core.VersionObj{}, false, err
	}
	if err := validateVersion(version); err != nil {
		return core.VersionObj{}, false, err
	}

	return obj.indexObj.GetVersion(ctx, key, version)
}

// GetDetection returns format detection for a version; the second result reports whether it exists.
func (obj *Obj) GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.DetectionObj{}, false, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return core.DetectionObj{}, false, err
	}
	if err := validateVersion(version); err != nil {
		return core.DetectionObj{}, false, err
	}

	return obj.indexObj.GetDetection(ctx, key, version)
}

// PutDetection upserts format detection for an already published version. Used by rescan heal to fix legacy rows
// whose go-zip viability was computed after publication; the versions FK rejects unknown versions.
func (obj *Obj) PutDetection(ctx context.Context, key string, version string, detectionObj core.DetectionObj) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.InsertDetection(ctx, key, version, detectionObj)
	})
}

// RewriteSet returns blob hashes rewritten by overlay and needed for artifact rebuilds.
func (obj *Obj) RewriteSet(ctx context.Context, key string, version string) ([]core.HashObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	if err := validateVersion(version); err != nil {
		return nil, err
	}

	return obj.indexObj.RewriteSet(ctx, key, version)
}

// ListVersions returns all key versions newest-first; includeDeleted includes upstream-deleted versions.
func (obj *Obj) ListVersions(ctx context.Context, key string, includeDeleted bool) ([]core.VersionObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}

	return obj.indexObj.ListVersions(ctx, key, includeDeleted)
}

// ListVersionsPage returns one newest-first page without loading the full version set into RAM.
func (obj *Obj) ListVersionsPage(ctx context.Context, key string, includeDeleted bool, limit int, offset int) ([]core.VersionObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}

	return obj.indexObj.ListVersionsPage(ctx, key, includeDeleted, limit, offset)
}

// ListVersionsKeyset returns newest-first versions by (upstream_seq, version) cursor without OFFSET.
// Serve pagination and full walks use this O(log n + limit) path.
func (obj *Obj) ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	return obj.indexObj.ListVersionsKeyset(ctx, key, includeDeleted, afterSeq, afterVersion, limit)
}

// ListVersionsKeysetBefore returns versions newer than the (beforeSeq, beforeVersion) cursor, ascending
// (closest first). It backs the "newer" pager direction; the serve layer reverses and trims for display.
func (obj *Obj) ListVersionsKeysetBefore(ctx context.Context, key string, includeDeleted bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	return obj.indexObj.ListVersionsKeysetBefore(ctx, key, includeDeleted, beforeSeq, beforeVersion, limit)
}

// MaxUpstreamSeq returns the maximum stored upstream position for a key; 0 means the key has no versions.
// Rescan pre-assigns positions from this base before publishing a listing batch.
func (obj *Obj) MaxUpstreamSeq(ctx context.Context, key string) (int64, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return 0, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return 0, err
	}
	return obj.indexObj.MaxUpstreamSeq(ctx, key)
}

// CountVersions returns active version count without loading versions.
func (obj *Obj) CountVersions(ctx context.Context, key string) (uint64, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return 0, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return 0, err
	}
	return obj.indexObj.CountVersionsByKey(ctx, key, false)
}

// LatestVersion returns the latest active key version; the second result reports whether any exists.
func (obj *Obj) LatestVersion(ctx context.Context, key string) (core.VersionObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.VersionObj{}, false, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return core.VersionObj{}, false, err
	}

	return obj.indexObj.LatestVersion(ctx, key)
}

// DeleteVersion unconditionally removes a version under writeMu.
// It deletes metadata, hot files, and unreferenced objects; missing versions are no-op.
func (obj *Obj) DeleteVersion(ctx context.Context, key string, version string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}
	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	deletedFlag, _, err := obj.deleteVersionLocked(ctx, key, version)
	if err != nil || !deletedFlag {
		return err
	}
	return nil
}

// MarkUpstreamDeleted handles versions that disappeared upstream according to history_policy.deletion.
// Delete mode physically removes the version; keep mode sets upstream_deleted, writes history, and refreshes latest.
func (obj *Obj) MarkUpstreamDeleted(ctx context.Context, key string, version string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	if obj.configObj.HistoryPolicy.Deletion.Mode == stcfg.HistoryDeletionModeDelete {
		deletedFlag, _, deleteErr := obj.deleteVersionLocked(ctx, key, version)
		if deleteErr != nil || !deletedFlag {
			return deleteErr
		}
		return nil
	}

	eventType, err := validateEventType("upstream_deleted")
	if err != nil {
		return err
	}
	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		versionObj, ok, err := txObj.GetVersion(ctx, key, version)
		if err != nil || !ok {
			return err
		}
		if err = txObj.MarkUpstreamDeleted(ctx, key, version); err != nil {
			return err
		}
		// Latest is derived by a top-1 query over versions, so no separate bookkeeping is needed.
		return txObj.AddHistory(ctx, key, version, eventType, versionObj.TreeHash, core.HashObj{}, "")
	})
}

// ResurrectVersion clears upstream_deleted for a keep-mode version that returned with the same tree.
// Blobs and artifacts are already present, so it records a publish event, refreshes latest, and is idempotent.
func (obj *Obj) ResurrectVersion(ctx context.Context, key string, version string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}

	eventType, err := validateEventType("publish")
	if err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		versionObj, ok, err := txObj.GetVersion(ctx, key, version)
		if err != nil || !ok {
			return err
		}
		if !versionObj.UpstreamDeleted {
			return nil
		}
		if err = txObj.ClearUpstreamDeleted(ctx, key, version); err != nil {
			return err
		}
		return txObj.AddHistory(ctx, key, version, eventType, versionObj.TreeHash, core.HashObj{}, "")
	})
}

// TouchVersionVerified updates deep-verification time and/or adopts upstream_ref.
// A zero verifiedTS and an empty upstreamRef leave their existing fields unchanged.
func (obj *Obj) TouchVersionVerified(ctx context.Context, key string, version string, verifiedTS time.Time, upstreamRef string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}
	if err := validateUpstreamRef(upstreamRef); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.TouchVersionVerified(ctx, key, version, verifiedTS, upstreamRef)
	})
}

// SetHealPending toggles the incomplete-materialization flag for a version.
// It is one-shot: rescan clears it after a heal attempt to avoid endless retries on healthy versions.
func (obj *Obj) SetHealPending(ctx context.Context, key string, version string, pending bool) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.SetHealPending(ctx, key, version, pending)
	})
}
