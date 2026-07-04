package storage

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const (
	cQuotaEvictPageSize = 1024

	// cHardLimitCompactMinInterval debounces full backstop compaction under writeMu.
	cHardLimitCompactMinInterval = 30 * time.Second

	// cRealBytesCacheTTL caches physical disk usage for the admission gate and avoids Pebble mutex stalls.
	cRealBytesCacheTTL = 2 * time.Second

	// cHotBytesCacheTTL caches the hot-cache size estimate and skips the full WalkDir while there is headroom under the limit.
	cHotBytesCacheTTL = 2 * time.Second
)

var errHotFileActive = errors.New("hot file is active")

// //

type versionKeyObj struct {
	key     string
	version string
}

type hotFileInfoObj struct {
	path      string
	sizeBytes uint64
	modTime   time.Time
}

type protectedObjectsObj struct {
	blobObj map[core.HashObj]struct{}
	treeObj map[core.HashObj]struct{}
}

// //

func versionKey(versionObj core.VersionObj) versionKeyObj {
	return versionKeyObj{key: versionObj.Key, version: versionObj.Version}
}

func protectTreeObjects(treeHashObj core.HashObj, treeArr []core.TreeEntryObj) protectedObjectsObj {
	resultObj := protectedObjectsObj{
		blobObj: make(map[core.HashObj]struct{}, len(treeArr)),
		treeObj: map[core.HashObj]struct{}{treeHashObj: {}},
	}
	for i := range treeArr {
		resultObj.blobObj[treeArr[i].BlobHash] = struct{}{}
	}
	return resultObj
}

func fileSize(pathToFile string) uint64 {
	infoObj, err := os.Stat(pathToFile)
	if err != nil || infoObj.IsDir() {
		return 0
	}
	return uint64(infoObj.Size())
}

func dirSize(ctx context.Context, rootPath string) (uint64, []hotFileInfoObj, []string, error) {
	var totalBytes uint64
	fileArr := make([]hotFileInfoObj, 0)
	var strayArr []string
	err := filepath.WalkDir(rootPath, func(pathToFile string, entryObj os.DirEntry, err error) error {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if entryObj.IsDir() {
			return nil
		}
		if entryObj.Type() != 0 {
			strayArr = append(strayArr, pathToFile)
			return nil
		}
		infoObj, err := entryObj.Info()
		if err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		sizeBytes := uint64(infoObj.Size())
		totalBytes += sizeBytes
		fileArr = append(fileArr, hotFileInfoObj{path: pathToFile, sizeBytes: sizeBytes, modTime: infoObj.ModTime()})
		return nil
	})
	return totalBytes, fileArr, strayArr, err
}

func (obj *Obj) removeBudgetFile(pathToFile string, sizeBytes uint64, totalBytes *uint64) error {
	if obj.markHotDeletePending(pathToFile) {
		return errHotFileActive
	}
	err := os.Remove(pathToFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if *totalBytes >= sizeBytes {
		*totalBytes -= sizeBytes
	} else {
		*totalBytes = 0
	}
	return nil
}

func (obj *Obj) sqliteDiskBytes() uint64 {
	return fileSize(obj.indexPath) + fileSize(obj.indexPath+"-wal") + fileSize(obj.indexPath+"-shm")
}

func (obj *Obj) durableBytes() uint64 {
	if obj == nil || obj.pebbleStoreObj == nil {
		return 0
	}
	return obj.pebbleStoreObj.DiskBytes() + obj.sqliteDiskBytes()
}

func (obj *Obj) realDurableBytes() uint64 {
	if obj == nil || obj.pebbleStoreObj == nil {
		return 0
	}
	value := obj.pebbleStoreObj.RealDiskBytes() + obj.sqliteDiskBytes()
	obj.realBytesCache = value
	obj.realBytesAt = time.Now()
	return value
}

func (obj *Obj) realDurableBytesCached() uint64 {
	if obj == nil {
		return 0
	}
	if !obj.realBytesAt.IsZero() && time.Since(obj.realBytesAt) < cRealBytesCacheTTL {
		return obj.realBytesCache
	}
	return obj.realDurableBytes()
}

func (obj *Obj) compactAllLocked(ctx context.Context) error {
	compactCtx, cancelFunc := obj.operationContext(ctx)
	defer cancelFunc()
	return obj.pebbleStoreObj.CompactAll(compactCtx)
}

func (obj *Obj) logicalDurableBytes(ctx context.Context) (uint64, error) {
	return obj.indexObj.ReferencedBytes(ctx)
}

func newCacheQuotaErr(cacheArea string, checkName string, cause error, currentTotalBytes uint64, incomingBytes uint64, admissionBytes uint64, evictToBytes uint64, maxTotalBytes uint64, retainLatestPerKey uint32) error {
	return stcode.NewErrCacheQuotaExceeded(
		admissionBytes,
		cacheArea,
		cause,
		checkName,
		currentTotalBytes,
		evictToBytes,
		incomingBytes,
		maxTotalBytes,
		retainLatestPerKey,
	)
}

func (obj *Obj) versionProtected(versionObj core.VersionObj, protectedObj protectedObjectsObj) (bool, error) {
	if len(protectedObj.treeObj) == 0 && len(protectedObj.blobObj) == 0 {
		return false, nil
	}
	if _, ok := protectedObj.treeObj[versionObj.TreeHash]; ok {
		return true, nil
	}
	if len(protectedObj.blobObj) == 0 {
		return false, nil
	}
	treeArr, err := obj.readTreeInternal(versionObj.TreeHash)
	if err != nil {
		return false, err
	}
	for i := range treeArr {
		if _, ok := protectedObj.blobObj[treeArr[i].BlobHash]; ok {
			return true, nil
		}
	}
	return false, nil
}

func (obj *Obj) hotBytes(ctx context.Context) (uint64, error) {
	sizeBytes, _, _, err := dirSize(ctx, obj.hotDir)
	return sizeBytes, err
}

// //

// DurableBytes returns an estimate of durable disk usage: Pebble live data plus SQLite.
func (obj *Obj) DurableBytes() uint64 {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return 0
	}
	defer releaseFunc()
	return obj.durableBytes()
}

func (obj *Obj) ensureDurableBudgetProtectedLocked(ctx context.Context, incomingBytes uint64, protectedObj protectedObjectsObj) error {
	maxBytes := uint64(obj.configObj.Storage.Quota.MaxTotalSize)
	if maxBytes == 0 {
		return nil
	}
	retainLatest := uint32(obj.configObj.Storage.Quota.RetainLatestPerKey)
	currentBytes, err := obj.logicalDurableBytes(ctx)
	if err != nil {
		return err
	}
	if incomingBytes >= maxBytes {
		return newCacheQuotaErr(cCacheAreaDurable, cQuotaCheckAdmission, nil, currentBytes, incomingBytes, 0, 0, maxBytes, retainLatest)
	}
	admissionBytes := maxBytes - incomingBytes
	if currentBytes < admissionBytes {
		return nil
	}

	targetBytes := uint64(obj.configObj.Storage.Quota.EvictToSize)
	if targetBytes == 0 || targetBytes >= admissionBytes {
		targetBytes = admissionBytes - 1
	}
	if err = obj.evictVersionsProtectedLocked(ctx, targetBytes, protectedObj); err != nil {
		return err
	}
	currentBytes, err = obj.logicalDurableBytes(ctx)
	if err != nil {
		return err
	}
	if currentBytes >= admissionBytes {
		return newCacheQuotaErr(cCacheAreaDurable, cQuotaCheckAfterEvict, nil, currentBytes, incomingBytes, admissionBytes, targetBytes, maxBytes, retainLatest)
	}
	return nil
}

func (obj *Obj) enforceDurableHardLimitLocked(ctx context.Context) error {
	maxBytes := uint64(obj.configObj.Storage.Quota.MaxTotalSize)
	if maxBytes == 0 || obj.realDurableBytesCached() <= maxBytes {
		return nil
	}
	if time.Since(obj.lastHardLimitCompact) >= cHardLimitCompactMinInterval {
		if err := obj.compactAllLocked(ctx); err != nil {
			return err
		}
		if err := obj.indexObj.Checkpoint(ctx); err != nil {
			return err
		}
		obj.lastHardLimitCompact = time.Now()
		if obj.realDurableBytes() <= maxBytes {
			return nil
		}
	}
	obj.triggerGC()
	return newCacheQuotaErr(cCacheAreaDurable, cQuotaCheckHardLimit, nil, obj.realDurableBytes(), 0, 0, 0, maxBytes, uint32(obj.configObj.Storage.Quota.RetainLatestPerKey))
}

func (obj *Obj) hotBudgetUnderLimitLocked(incomingBytes uint64, maxBytes uint64) bool {
	if obj.hotBytesAt.IsZero() || time.Since(obj.hotBytesAt) >= cHotBytesCacheTTL {
		return false
	}
	projected := obj.hotBytesCache + incomingBytes
	if projected > maxBytes {
		return false
	}
	obj.hotBytesCache = projected
	return true
}

func (obj *Obj) setHotBytesCacheLocked(totalBytes uint64) {
	obj.hotBytesCache = totalBytes
	obj.hotBytesAt = time.Now()
}

func (obj *Obj) invalidateHotBytesCacheLocked() {
	obj.hotBytesAt = time.Time{}
}

func (obj *Obj) subHotBytesCacheLocked(removedBytes uint64) {
	if obj.hotBytesAt.IsZero() || removedBytes == 0 {
		return
	}
	if removedBytes >= obj.hotBytesCache {
		obj.hotBytesCache = 0
		return
	}
	obj.hotBytesCache -= removedBytes
}

func (obj *Obj) removeHotPathAccountedLocked(pathToFile string) error {
	if obj.markHotDeletePending(pathToFile) {
		obj.invalidateHotBytesCacheLocked()
		return nil
	}
	removedBytes := fileSize(pathToFile)
	err := obj.removeHotPath(pathToFile)
	if err == nil {
		obj.subHotBytesCacheLocked(removedBytes)
	}
	return err
}

func (obj *Obj) enforceHotBudgetProtectedLocked(ctx context.Context, protectedPath string, protectedMustFit bool) error {
	maxBytes := uint64(obj.configObj.Storage.Hot.MaxSize)
	if maxBytes == 0 {
		return nil
	}
	protectedSize := uint64(0)
	if protectedPath != "" {
		protectedSize = fileSize(protectedPath)
	}
	if protectedMustFit && protectedPath != "" && protectedSize > maxBytes {
		return newCacheQuotaErr(cCacheAreaHot, cQuotaCheckProtectedFit, nil, protectedSize, protectedSize, 0, 0, maxBytes, 0)
	}
	if obj.hotBudgetUnderLimitLocked(protectedSize, maxBytes) {
		return nil
	}
	walkStart := time.Now()
	obj.hotEnforceWalks.Add(1)
	defer func() { obj.hotEnforceWalkNanos.Add(int64(time.Since(walkStart))) }()

	totalBytes, fileArr, strayArr, err := dirSize(ctx, obj.hotDir)
	if err != nil {
		return err
	}
	defer func() { obj.setHotBytesCacheLocked(totalBytes) }()
	for _, strayPath := range strayArr {
		if removeErr := os.Remove(strayPath); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return removeErr
		}
	}

	idleBefore := time.Now().Add(-obj.configObj.Storage.Hot.IdleTtl)
	var removeErr error
	survivorArr := fileArr[:0]
	for _, fileObj := range fileArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if totalBytes <= maxBytes {
			return nil
		}
		if fileObj.path == protectedPath || fileObj.modTime.After(idleBefore) {
			survivorArr = append(survivorArr, fileObj)
			continue
		}
		if err = obj.removeBudgetFile(fileObj.path, fileObj.sizeBytes, &totalBytes); err != nil {
			removeErr = errors.Join(removeErr, fmt.Errorf("remove hot file %s: %w", fileObj.path, err))
		}
	}
	if totalBytes <= maxBytes {
		return nil
	}

	sort.Slice(survivorArr, func(i, j int) bool {
		return survivorArr[i].modTime.Before(survivorArr[j].modTime)
	})
	for _, fileObj := range survivorArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if totalBytes <= maxBytes {
			return nil
		}
		if fileObj.path == protectedPath {
			continue
		}
		if err = obj.removeBudgetFile(fileObj.path, fileObj.sizeBytes, &totalBytes); err != nil {
			removeErr = errors.Join(removeErr, fmt.Errorf("remove hot file %s: %w", fileObj.path, err))
		}
	}
	if totalBytes > maxBytes && removeErr != nil {
		return newCacheQuotaErr(cCacheAreaHot, cQuotaCheckAfterEvict, removeErr, totalBytes, 0, 0, maxBytes, maxBytes, 0)
	}
	if totalBytes > maxBytes {
		return newCacheQuotaErr(cCacheAreaHot, cQuotaCheckAfterEvict, nil, totalBytes, 0, 0, maxBytes, maxBytes, 0)
	}
	return nil
}

func (obj *Obj) evictUntilTargetBatchLocked(ctx context.Context, targetBytes uint64, currentBytes uint64, versionArr []core.VersionObj, protectedObj protectedObjectsObj) (uint64, bool, error) {
	var pendingEstimate uint64
	for i := range versionArr {
		if currentBytes < targetBytes {
			return currentBytes, true, nil
		}
		select {
		case <-ctx.Done():
			return currentBytes, false, ctx.Err()
		default:
		}
		protectedFlag, err := obj.versionProtected(versionArr[i], protectedObj)
		if err != nil {
			return currentBytes, false, err
		}
		if protectedFlag {
			continue
		}
		deletedFlag, estimateBytes, err := obj.deleteVersionLocked(ctx, versionArr[i].Key, versionArr[i].Version)
		if err != nil {
			return currentBytes, false, err
		}
		if !deletedFlag {
			continue
		}
		pendingEstimate += estimateBytes
		neededBytes := currentBytes - targetBytes
		if pendingEstimate < neededBytes {
			continue
		}
		currentBytes, err = obj.logicalDurableBytes(ctx)
		if err != nil {
			return currentBytes, false, err
		}
		pendingEstimate = 0
		if currentBytes < targetBytes {
			return currentBytes, true, nil
		}
	}
	if pendingEstimate == 0 {
		return currentBytes, currentBytes < targetBytes, nil
	}
	measuredBytes, err := obj.logicalDurableBytes(ctx)
	if err != nil {
		return measuredBytes, false, err
	}
	return measuredBytes, measuredBytes < targetBytes, nil
}

func (obj *Obj) evictPrunableVersionsLocked(ctx context.Context, retainValue uint, maxVersions uint, protectedObj protectedObjectsObj) (map[versionKeyObj]struct{}, error) {
	if maxVersions > 0 && maxVersions < retainValue {
		maxVersions = retainValue
	}
	retainObj := make(map[versionKeyObj]struct{})
	afterKey := ""
	for {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		keyArr, err := obj.indexObj.DistinctVersionKeys(ctx, afterKey, cQuotaEvictPageSize)
		if err != nil {
			return nil, err
		}
		if len(keyArr) == 0 {
			return retainObj, nil
		}
		for _, keyText := range keyArr {
			afterKey = keyText
			versionArr, listErr := obj.indexObj.ListVersions(ctx, keyText, true)
			if listErr != nil {
				return nil, listErr
			}
			if pruneErr := obj.prunePerKeyLocked(ctx, versionArr, retainValue, maxVersions, protectedObj, retainObj); pruneErr != nil {
				return nil, pruneErr
			}
		}
		if len(keyArr) < cQuotaEvictPageSize {
			return retainObj, nil
		}
	}
}

func (obj *Obj) prunePerKeyLocked(ctx context.Context, versionArr []core.VersionObj, retainValue uint, maxVersions uint, protectedObj protectedObjectsObj, retainObj map[versionKeyObj]struct{}) error {
	retainLimit := len(versionArr)
	if retainValue < uint(len(versionArr)) {
		retainLimit = int(retainValue)
	}
	for i := 0; i < retainLimit; i++ {
		retainObj[versionKey(versionArr[i])] = struct{}{}
	}
	activeArr := append([]core.VersionObj(nil), versionArr[retainLimit:]...)
	keptCount := uint(retainLimit) + uint(len(activeArr))
	if maxVersions == 0 || keptCount <= maxVersions {
		return nil
	}
	deleteCount := keptCount - maxVersions
	for i := len(activeArr) - int(deleteCount); i < len(activeArr); i++ {
		protectedFlag, protectErr := obj.versionProtected(activeArr[i], protectedObj)
		if protectErr != nil {
			return protectErr
		}
		if protectedFlag {
			continue
		}
		if _, _, deleteErr := obj.deleteVersionLocked(ctx, activeArr[i].Key, activeArr[i].Version); deleteErr != nil {
			return deleteErr
		}
	}
	return nil
}

func (obj *Obj) evictUntilTargetLocked(ctx context.Context, targetBytes uint64, retainObj map[versionKeyObj]struct{}, protectedObj protectedObjectsObj) error {
	currentBytes, err := obj.logicalDurableBytes(ctx)
	if err != nil {
		return err
	}
	if targetBytes == 0 || currentBytes < targetBytes {
		return nil
	}

	candidateArr := make([]core.VersionObj, 0, cQuotaEvictPageSize)
	var afterObj core.VersionObj
	afterFlag := false
	for currentBytes >= targetBytes {
		candidateArr = candidateArr[:0]
		for len(candidateArr) < cQuotaEvictPageSize {
			pageArr, err := obj.indexObj.VersionsByIngest(ctx, afterObj, afterFlag, cQuotaEvictPageSize)
			if err != nil {
				return err
			}
			if len(pageArr) == 0 {
				return nil
			}
			for i := range pageArr {
				afterObj = pageArr[i]
				afterFlag = true
				if _, ok := retainObj[versionKey(pageArr[i])]; ok {
					continue
				}
				protectedFlag, protectErr := obj.versionProtected(pageArr[i], protectedObj)
				if protectErr != nil {
					return protectErr
				}
				if protectedFlag {
					continue
				}
				candidateArr = append(candidateArr, pageArr[i])
				if len(candidateArr) >= cQuotaEvictPageSize {
					break
				}
			}
			if len(pageArr) < cQuotaEvictPageSize {
				break
			}
		}
		if len(candidateArr) == 0 {
			return nil
		}
		var doneFlag bool
		var err error
		currentBytes, doneFlag, err = obj.evictUntilTargetBatchLocked(ctx, targetBytes, currentBytes, candidateArr, protectedObj)
		if err != nil || doneFlag {
			return err
		}
	}
	return nil
}

func (obj *Obj) evictVersionsProtectedLocked(ctx context.Context, targetBytes uint64, protectedObj protectedObjectsObj) error {
	retainValue := obj.configObj.Storage.Quota.RetainLatestPerKey
	maxVersions := obj.configObj.Storage.Quota.MaxVersionsPerKey
	retainObj, err := obj.evictPrunableVersionsLocked(ctx, retainValue, maxVersions, protectedObj)
	if err != nil {
		return err
	}
	return obj.evictUntilTargetLocked(ctx, targetBytes, retainObj, protectedObj)
}
