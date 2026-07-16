package storage

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// //

func (obj *Obj) deleteVersionTx(ctx context.Context, txObj *sqliteindex.TxObj, versionObj core.VersionObj, treeArr []core.TreeEntryObj) error {
	if err := txObj.SubtractBlobRefs(ctx, treeArr); err != nil {
		return err
	}
	return txObj.DeleteVersionRow(ctx, versionObj.Key, versionObj.Version)
}

func treePayloadEstimate(treeArr []core.TreeEntryObj) (uint64, error) {
	var resultBytes uint64
	refMapObj, err := sqliteindex.CountBlobRefs(treeArr)
	if err != nil {
		return 0, err
	}
	for _, refObj := range refMapObj {
		resultBytes += refObj.SizeBytes
	}
	return resultBytes, nil
}

func (obj *Obj) cleanupDeletedObjectsLocked(ctx context.Context, treeHashObj core.HashObj, treeArr []core.TreeEntryObj) error {
	deleteArr := make([]pebblestore.PendingObjectObj, 0, len(treeArr)+1)
	treeReferencedFlag, err := obj.indexObj.TreeReferenced(ctx, treeHashObj)
	if err != nil {
		return err
	}
	if !treeReferencedFlag {
		deleteArr = append(deleteArr, pebblestore.PendingTree(treeHashObj))
	}

	seenObj := make(map[core.HashObj]struct{}, len(treeArr))
	hashArr := make([]core.HashObj, 0, len(treeArr))
	for i := range treeArr {
		hashObj := treeArr[i].BlobHash
		if _, ok := seenObj[hashObj]; ok {
			continue
		}
		seenObj[hashObj] = struct{}{}
		hashArr = append(hashArr, hashObj)
	}
	unreferencedObj, err := obj.indexObj.UnreferencedBlobs(ctx, hashArr)
	if err != nil {
		return err
	}
	unreferencedArr := make([]core.HashObj, 0, len(unreferencedObj))
	for hashObj := range unreferencedObj {
		unreferencedArr = append(unreferencedArr, hashObj)
		deleteArr = append(deleteArr, pebblestore.PendingBlob(hashObj))
	}
	if err = obj.indexObj.DeleteUnreferencedBlobRefs(ctx, unreferencedArr); err != nil {
		return err
	}
	return obj.pebbleStoreObj.DeleteObjects(ctx, deleteArr)
}

func (obj *Obj) deleteVersionLocked(ctx context.Context, key string, version string) (bool, uint64, error) {
	eventType, err := validateEventType("delete")
	if err != nil {
		return false, 0, err
	}

	deletedFlag := false
	var pathArr []string
	versionObj, ok, err := obj.indexObj.GetVersion(ctx, key, version)
	if err != nil || !ok {
		return false, 0, err
	}
	treeArr, err := obj.readTreeInternal(versionObj.TreeHash)
	if err != nil {
		return false, 0, err
	}
	estimateBytes, err := treePayloadEstimate(treeArr)
	if err != nil {
		return false, 0, err
	}
	err = obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		var txErr error
		pathArr, txErr = txObj.ArtifactPaths(ctx, key, version)
		if txErr != nil {
			return txErr
		}
		if txErr = obj.deleteVersionTx(ctx, txObj, versionObj, treeArr); txErr != nil {
			return txErr
		}
		if txErr = txObj.AddHistory(ctx, key, version, eventType, versionObj.TreeHash, core.HashObj{}, ""); txErr != nil {
			return txErr
		}
		deletedFlag = true
		return nil
	})
	if err != nil || !deletedFlag {
		return deletedFlag, 0, err
	}
	for _, pathText := range pathArr {
		_ = obj.removeHotPathAccountedLocked(pathText)
	}
	if err = obj.cleanupDeletedObjectsLocked(ctx, versionObj.TreeHash, treeArr); err != nil {
		return true, estimateBytes, err
	}
	return true, estimateBytes, nil
}

func resolveUpstreamSeq(ctx context.Context, txObj *sqliteindex.TxObj, publishObj core.PublishObj, existingObj core.VersionObj, existingFlag bool) (int64, error) {
	if existingFlag && existingObj.UpstreamSeq > 0 {
		return existingObj.UpstreamSeq, nil
	}
	if publishObj.UpstreamSeq > 0 {
		return publishObj.UpstreamSeq, nil
	}
	maxSeq, err := txObj.MaxUpstreamSeq(ctx, publishObj.Key)
	if err != nil {
		return 0, err
	}
	return maxSeq + 1, nil
}

func (obj *Obj) publishTx(ctx context.Context, txObj *sqliteindex.TxObj, publishObj core.PublishObj, treeArr []core.TreeEntryObj, treeHashObj core.HashObj, existingObj core.VersionObj, existingFlag bool, existingTreeArr []core.TreeEntryObj) (core.PublishResultObj, error) {
	resultObj := core.PublishResultObj{
		Key:      publishObj.Key,
		Version:  publishObj.Version,
		TreeHash: treeHashObj,
	}

	if existingFlag && existingObj.TreeHash == treeHashObj {
		resultObj.Skipped = true
		return resultObj, nil
	}
	if existingFlag {
		if obj.configObj.HistoryPolicy.Mutation == stcfg.HistoryMutationModeHistory {
			historicalVersion, nextErr := txObj.NextHistoricalVersion(ctx, obj.configObj.HistoryPolicy.Prefix)
			if nextErr != nil {
				return resultObj, nextErr
			}
			if err := txObj.CopyVersionMetadata(ctx, existingObj, historicalVersion); err != nil {
				return resultObj, err
			}
			resultObj.Historical = historicalVersion
			if err := txObj.DeleteVersionRow(ctx, publishObj.Key, publishObj.Version); err != nil {
				return resultObj, err
			}
		} else {
			if err := obj.deleteVersionTx(ctx, txObj, existingObj, existingTreeArr); err != nil {
				return resultObj, err
			}
		}
	}

	versionObj := sqliteindex.NewVersion(
		publishObj.Key,
		publishObj.Version,
		publishObj.SourceHash,
		publishObj.SourceSizeBytes,
		treeHashObj,
		publishObj.UpstreamDeleted,
	)
	versionObj.ReleaseNotes = publishObj.ReleaseNotes
	versionObj.HealPending = publishObj.HealPending
	versionObj.UpstreamRef = publishObj.UpstreamRef
	versionObj.VerifiedTS = publishObj.VerifiedTS
	seqValue, seqErr := resolveUpstreamSeq(ctx, txObj, publishObj, existingObj, existingFlag)
	if seqErr != nil {
		return resultObj, seqErr
	}
	versionObj.UpstreamSeq = seqValue
	if err := txObj.InsertVersion(ctx, versionObj); err != nil {
		return resultObj, err
	}
	if err := txObj.AddBlobRefs(ctx, treeArr); err != nil {
		return resultObj, err
	}
	if err := txObj.InsertDetection(ctx, publishObj.Key, publishObj.Version, publishObj.Detection); err != nil {
		return resultObj, err
	}
	if err := txObj.InsertRewriteSet(ctx, publishObj.Key, publishObj.Version, publishObj.RewriteBlobs); err != nil {
		return resultObj, err
	}
	if err := txObj.InsertArtifacts(ctx, publishObj.Key, publishObj.Version, publishObj.Artifacts); err != nil {
		return resultObj, err
	}
	if err := txObj.AddHistory(ctx, publishObj.Key, publishObj.Version, publishObj.EventType, treeHashObj, core.HashObj{}, publishObj.EventMessage); err != nil {
		return resultObj, err
	}
	resultObj.Published = true
	return resultObj, nil
}
