package storage

import (
	"context"
	"errors"
	"fmt"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
)

// // // // // // // // // //

const cGCCandidateBatch = 512

// cReachablePageSize is the keyset page size used while collecting reachable sets.
// The reachable set is resident for Pebble scanning, but SQLite tables are read page by page.
const cReachablePageSize = 4096

// //

func collectHashSetPaged(ctx context.Context, pageFunc func(context.Context, []byte, int) ([]core.HashObj, error)) (map[core.HashObj]struct{}, error) {
	resultObj := make(map[core.HashObj]struct{})
	var afterHash []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pageArr, err := pageFunc(ctx, afterHash, cReachablePageSize)
		if err != nil {
			return nil, err
		}
		for i := range pageArr {
			resultObj[pageArr[i]] = struct{}{}
		}
		if len(pageArr) < cReachablePageSize {
			return resultObj, nil
		}
		afterHash = pageArr[len(pageArr)-1].BytesCopy()
	}
}

// //

// VacuumResultObj reports durable physical size before and after --vacuum.
type VacuumResultObj struct {
	BeforeBytes uint64
	AfterBytes  uint64
	FreedBytes  uint64
}

func (obj *Obj) reachableObjectSet(ctx context.Context) (map[core.HashObj]sqliteindex.BlobRefObj, map[core.HashObj]struct{}, map[core.HashObj]struct{}, error) {
	refObj := make(map[core.HashObj]sqliteindex.BlobRefObj)
	blobSetObj := make(map[core.HashObj]struct{})
	treeObj := make(map[core.HashObj]struct{})

	var afterTree []byte
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, nil, err
		}
		treePageArr, err := obj.indexObj.TreeHashCountsPage(ctx, afterTree, cReachablePageSize)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, treeRowObj := range treePageArr {
			treeObj[treeRowObj.Hash] = struct{}{}
			treeEntryArr, readErr := obj.readTreeInternal(treeRowObj.Hash)
			if readErr != nil {
				return nil, nil, nil, readErr
			}
			for _, entryObj := range treeEntryArr {
				blobObj := refObj[entryObj.BlobHash]
				if blobObj.Refcount == 0 {
					blobObj.SizeBytes = entryObj.SizeBytes
				}
				if entryObj.SizeBytes != blobObj.SizeBytes {
					return nil, nil, nil, fmt.Errorf("tree size mismatch for blob %s", entryObj.BlobHash.Hex())
				}
				blobObj.Refcount += treeRowObj.Count
				refObj[entryObj.BlobHash] = blobObj
				blobSetObj[entryObj.BlobHash] = struct{}{}
			}
		}
		if len(treePageArr) < cReachablePageSize {
			break
		}
		afterTree = treePageArr[len(treePageArr)-1].Hash.BytesCopy()
	}
	return refObj, blobSetObj, treeObj, nil
}

// Recover runs startup recovery under writeMu and all build slots.
// It cleans interrupted temp files before serving begins.
func (obj *Obj) Recover(ctx context.Context) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	slotCount, err := obj.acquireBuildSlots(ctx, cap(obj.buildSem))
	if err != nil {
		return err
	}
	defer obj.releaseBuildSlots(slotCount)

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	if err = cleanupTempDir(ctx, obj.tempDir); err != nil {
		return fmt.Errorf("clean interrupted temp files: %w", err)
	}
	return nil
}

// VerifyIntegrity performs a full integrity check under writeMu.
// It runs SQLite checks, validates blob_refs against trees, re-hashes reachable Pebble blobs, and counts orphans.
func (obj *Obj) VerifyIntegrity(ctx context.Context) (core.IntegrityReportObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.IntegrityReportObj{}, err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	return obj.verifyIntegrityLocked(ctx)
}

func (obj *Obj) verifyIntegrityLocked(ctx context.Context) (core.IntegrityReportObj, error) {
	reportObj := core.IntegrityReportObj{}
	sqliteIssueArr, err := obj.indexObj.CheckSQLiteIntegrity(ctx)
	if err != nil {
		return reportObj, err
	}
	reportObj.Errors = append(reportObj.Errors, sqliteIssueArr...)

	expectedRefObj := make(map[core.HashObj]sqliteindex.BlobRefObj)
	checkedObj := make(map[core.HashObj]uint64)
	failedObj := make(map[core.HashObj]struct{})
	reportedBlobObj := make(map[core.HashObj]struct{})

	var afterTree []byte
	for {
		if err = ctx.Err(); err != nil {
			return reportObj, err
		}
		treePageArr, pageErr := obj.indexObj.TreeHashCountsPage(ctx, afterTree, cReachablePageSize)
		if pageErr != nil {
			return reportObj, pageErr
		}
		for _, treeRowObj := range treePageArr {
			treeHashObj := treeRowObj.Hash
			treeRefcount := treeRowObj.Count
			treeArr, readErr := obj.readTreeInternal(treeHashObj)
			if readErr != nil {
				reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("tree %s referenced by %d versions: %v", treeHashObj.Hex(), treeRefcount, readErr))
				continue
			}
			for _, entryObj := range treeArr {
				if err = ctx.Err(); err != nil {
					return reportObj, err
				}
				refObj := expectedRefObj[entryObj.BlobHash]
				if refObj.Refcount == 0 {
					refObj.SizeBytes = entryObj.SizeBytes
				} else if refObj.SizeBytes != entryObj.SizeBytes {
					reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s referenced by tree %s: size mismatch", entryObj.BlobHash.Hex(), treeHashObj.Hex()))
					continue
				}
				refObj.Refcount += treeRefcount
				expectedRefObj[entryObj.BlobHash] = refObj

				if _, ok := checkedObj[entryObj.BlobHash]; ok {
					continue
				}
				if _, ok := failedObj[entryObj.BlobHash]; ok {
					continue
				}
				blobErr := obj.pebbleStoreObj.UseBlob(entryObj.BlobHash, func(blobArr []byte) error {
					if uint64(len(blobArr)) != entryObj.SizeBytes {
						return errors.New("size mismatch")
					}
					return nil
				})
				if blobErr != nil {
					reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s referenced by tree %s: %v", entryObj.BlobHash.Hex(), treeHashObj.Hex(), blobErr))
					failedObj[entryObj.BlobHash] = struct{}{}
					reportedBlobObj[entryObj.BlobHash] = struct{}{}
					continue
				}
				checkedObj[entryObj.BlobHash] = entryObj.SizeBytes
			}
		}
		if len(treePageArr) < cReachablePageSize {
			break
		}
		afterTree = treePageArr[len(treePageArr)-1].Hash.BytesCopy()
	}

	var afterBlob []byte
	for {
		if err = ctx.Err(); err != nil {
			return reportObj, err
		}
		refPageArr, pageErr := obj.indexObj.BlobRefsPage(ctx, afterBlob, cReachablePageSize)
		if pageErr != nil {
			return reportObj, pageErr
		}
		for _, rowObj := range refPageArr {
			expectedObj, ok := expectedRefObj[rowObj.Hash]
			if !ok {
				if _, failedFlag := failedObj[rowObj.Hash]; !failedFlag {
					reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s has unexpected blob_refs row", rowObj.Hash.Hex()))
				}
				continue
			}
			if _, failedFlag := failedObj[rowObj.Hash]; failedFlag {
				delete(expectedRefObj, rowObj.Hash)
				continue
			}
			if rowObj.Ref.Refcount != expectedObj.Refcount || rowObj.Ref.SizeBytes != expectedObj.SizeBytes {
				reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s blob_refs mismatch", rowObj.Hash.Hex()))
			}
			delete(expectedRefObj, rowObj.Hash)
		}
		if len(refPageArr) < cReachablePageSize {
			break
		}
		afterBlob = refPageArr[len(refPageArr)-1].Hash.BytesCopy()
	}
	for hashObj := range expectedRefObj {
		if _, failedFlag := failedObj[hashObj]; failedFlag {
			continue
		}
		reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s missing from blob_refs", hashObj.Hex()))
	}

	err = obj.pebbleStoreObj.ForEachBlobScan(ctx, func(scanObj pebblestore.BlobScanObj) error {
		if scanObj.Err != nil {
			if scanObj.Hash.IsZero() {
				reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("pebble blob object: %v", scanObj.Err))
				return nil
			}
			if _, ok := reportedBlobObj[scanObj.Hash]; !ok {
				reportObj.Errors = append(reportObj.Errors, fmt.Sprintf("blob %s stored in pebble: %v", scanObj.Hash.Hex(), scanObj.Err))
				reportedBlobObj[scanObj.Hash] = struct{}{}
			}
			return nil
		}
		return nil
	})
	if err != nil {
		return reportObj, err
	}
	return reportObj, nil
}

// reachableForGC materializes the full reachable blob/tree hash set in RAM. At hundreds of
// millions of blobs this should move to sorted-merge iteration instead of a full transient map.
func (obj *Obj) reachableForGC(ctx context.Context) (pebblestore.ReachableObjectsObj, error) {
	reachableBlobObj, err := collectHashSetPaged(ctx, obj.indexObj.BlobRefHashesPage)
	if err != nil {
		return pebblestore.ReachableObjectsObj{}, err
	}
	reachableTreeObj, err := collectHashSetPaged(ctx, obj.indexObj.TreeHashesPage)
	if err != nil {
		return pebblestore.ReachableObjectsObj{}, err
	}
	return pebblestore.ReachableObjectsObj{BlobSet: reachableBlobObj, TreeSet: reachableTreeObj}, nil
}

func (obj *Obj) deleteOrphanBatchChecked(ctx context.Context, batchArr []pebblestore.PendingObjectObj) error {
	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	confirmedArr := make([]pebblestore.PendingObjectObj, 0, len(batchArr))
	for i := range batchArr {
		referencedFlag, err := obj.pendingObjectReferenced(ctx, batchArr[i])
		if err != nil {
			return err
		}
		if !referencedFlag {
			confirmedArr = append(confirmedArr, batchArr[i])
		}
	}
	return obj.pebbleStoreObj.DeleteObjects(ctx, confirmedArr)
}

func (obj *Obj) collectGarbage(ctx context.Context) error {
	reachableObj, err := obj.reachableForGC(ctx)
	if err != nil {
		return err
	}
	return obj.pebbleStoreObj.OrphanCandidates(ctx, reachableObj, cGCCandidateBatch, func(batchArr []pebblestore.PendingObjectObj) error {
		return obj.deleteOrphanBatchChecked(ctx, batchArr)
	})
}

// CollectGarbage runs incremental GC for unreachable blobs and trees.
func (obj *Obj) CollectGarbage(ctx context.Context) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()
	return obj.collectGarbage(ctx)
}

func (obj *Obj) repairBlobRefsLocked(ctx context.Context) error {
	refObj, reachableBlobObj, reachableTreeObj, err := obj.reachableObjectSet(ctx)
	if err != nil {
		return err
	}
	if err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.ReplaceBlobRefs(ctx, refObj)
	}); err != nil {
		return err
	}
	return obj.pebbleStoreObj.CollectGarbage(ctx, pebblestore.ReachableObjectsObj{BlobSet: reachableBlobObj, TreeSet: reachableTreeObj})
}

// RepairBlobRefs fully rebuilds blob_refs from trees under writeMu.
func (obj *Obj) RepairBlobRefs(ctx context.Context) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()
	return obj.repairBlobRefsLocked(ctx)
}

// Inspect returns a read-only storage snapshot with paths, table counts, and durable plus hot bytes.
func (obj *Obj) Inspect(ctx context.Context) (InspectObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return InspectObj{}, err
	}
	defer releaseFunc()

	versionCount, err := obj.indexObj.CountTable(ctx, "versions")
	if err != nil {
		return InspectObj{}, err
	}
	blobCount, err := obj.indexObj.CountTable(ctx, "blob_refs")
	if err != nil {
		return InspectObj{}, err
	}
	artifactCount, err := obj.indexObj.CountTable(ctx, "materialized_artifacts")
	if err != nil {
		return InspectObj{}, err
	}
	historyCount, err := obj.indexObj.CountTable(ctx, "history_events")
	if err != nil {
		return InspectObj{}, err
	}
	hotBytes, err := obj.hotBytes(ctx)
	if err != nil {
		return InspectObj{}, err
	}
	return InspectObj{
		RootPath:            obj.rootPath,
		SQLitePath:          obj.indexPath,
		PebblePath:          obj.pebbleDir,
		HotPath:             obj.hotDir,
		VersionCount:        versionCount,
		BlobCount:           blobCount,
		ArtifactCount:       artifactCount,
		HistoryEventCount:   historyCount,
		PebbleDiskBytes:     obj.pebbleStoreObj.DiskBytes(),
		PebbleRealDiskBytes: obj.pebbleStoreObj.RealDiskBytes(),
		SQLiteDiskBytes:     obj.sqliteDiskBytes(),
		HotBytes:            hotBytes,
	}, nil
}

// ContentChecksum returns the deterministic index content checksum for replica and state comparison.
func (obj *Obj) ContentChecksum(ctx context.Context) (core.HashObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.HashObj{}, err
	}
	defer releaseFunc()

	return obj.indexObj.ContentChecksum(ctx)
}

// // // // // // // // // //

// Vacuum reclaims disk with full Pebble compaction, WAL checkpoint, and SQLite VACUUM.
// It runs under writeMu during stopped maintenance; sizes are the real physical footprint.
func (obj *Obj) Vacuum(ctx context.Context) (VacuumResultObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return VacuumResultObj{}, err
	}
	defer releaseFunc()

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	beforeBytes := obj.realDurableBytes()
	if err = obj.compactAllLocked(ctx); err != nil {
		return VacuumResultObj{}, err
	}
	if err = obj.indexObj.Checkpoint(ctx); err != nil {
		return VacuumResultObj{}, err
	}
	if err = obj.indexObj.Vacuum(ctx); err != nil {
		return VacuumResultObj{}, err
	}
	if err = obj.indexObj.Checkpoint(ctx); err != nil {
		return VacuumResultObj{}, err
	}
	afterBytes := obj.realDurableBytes()
	freedBytes := uint64(0)
	if beforeBytes > afterBytes {
		freedBytes = beforeBytes - afterBytes
	}
	return VacuumResultObj{BeforeBytes: beforeBytes, AfterBytes: afterBytes, FreedBytes: freedBytes}, nil
}

// DistinctKeys returns one keyset page of unique version keys with key > afterKey.
// It lets --prune diff storage against release_mirrors without loading all versions into RAM.
func (obj *Obj) DistinctKeys(ctx context.Context, afterKey string, limit int) ([]string, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	return obj.indexObj.DistinctVersionKeys(ctx, afterKey, limit)
}

// KeyDeletionEstimate is a --prune dry-run for one key.
// It returns version count and an upper reclaim estimate; shared blobs may be counted more than once.
func (obj *Obj) KeyDeletionEstimate(ctx context.Context, key string) (uint64, uint64, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return 0, 0, err
	}
	defer releaseFunc()

	if err = validateKey(key); err != nil {
		return 0, 0, err
	}
	versionArr, err := obj.indexObj.ListVersions(ctx, key, true)
	if err != nil {
		return 0, 0, err
	}
	var totalBytes uint64
	for i := range versionArr {
		if ctx.Err() != nil {
			return 0, 0, ctx.Err()
		}
		treeArr, readErr := obj.readTreeInternal(versionArr[i].TreeHash)
		if readErr != nil {
			return 0, 0, readErr
		}
		estimateBytes, estErr := treePayloadEstimate(treeArr)
		if estErr != nil {
			return 0, 0, estErr
		}
		totalBytes += estimateBytes
	}
	return uint64(len(versionArr)), totalBytes, nil
}

// DeleteKey deletes all versions of a key under one writeMu for --prune --force.
// Each version removes its hot files and unreferenced objects; later RepairBlobRefs sweeps remaining orphan blobs.
func (obj *Obj) DeleteKey(ctx context.Context, key string) (uint64, uint64, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return 0, 0, err
	}
	defer releaseFunc()

	if err = validateKey(key); err != nil {
		return 0, 0, err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	versionArr, err := obj.indexObj.ListVersions(ctx, key, true)
	if err != nil {
		return 0, 0, err
	}
	var deletedCount uint64
	var reclaimedBytes uint64
	for i := range versionArr {
		if ctx.Err() != nil {
			return deletedCount, reclaimedBytes, ctx.Err()
		}
		deletedFlag, estimateBytes, delErr := obj.deleteVersionLocked(ctx, versionArr[i].Key, versionArr[i].Version)
		if delErr != nil {
			return deletedCount, reclaimedBytes, delErr
		}
		if deletedFlag {
			deletedCount++
			reclaimedBytes += estimateBytes
		}
	}
	return deletedCount, reclaimedBytes, nil
}
