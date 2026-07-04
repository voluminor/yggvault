package pebblestore

import (
	"context"
	"errors"

	"github.com/cockroachdb/pebble/v2"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func (obj *Obj) collectGarbagePrefix(ctx context.Context, kindObj objectKindObj, reachableObj map[core.HashObj]struct{}) error {
	keyTag, ok := objectTag(kindObj)
	if !ok {
		return errors.New("invalid pebble object kind")
	}
	iterObj, err := obj.dbObj.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyTag},
		UpperBound: []byte{keyTag + 1},
	})
	if err != nil {
		return err
	}
	keyArr := make([][]byte, 0, cDeleteBatchKeys)
	for iterObj.First(); iterObj.Valid(); iterObj.Next() {
		select {
		case <-ctx.Done():
			_ = iterObj.Close()
			return ctx.Err()
		default:
		}
		hashObj, validFlag := objectHashFromKey(kindObj, iterObj.Key())
		if !validFlag {
			keyArr = append(keyArr, append([]byte(nil), iterObj.Key()...))
		} else if _, ok = reachableObj[hashObj]; !ok {
			keyArr = append(keyArr, append([]byte(nil), iterObj.Key()...))
		}
		if len(keyArr) >= cDeleteBatchKeys {
			if err = obj.deleteKeys(ctx, keyArr); err != nil {
				_ = iterObj.Close()
				return err
			}
			keyArr = keyArr[:0]
		}
	}
	err = iterObj.Error()
	if closeErr := iterObj.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return obj.deleteKeys(ctx, keyArr)
}

// CollectGarbage deletes blobs and trees absent from the reachable set, plus malformed-prefix keys.
// It scans directly without a snapshot and is intended for full stop-the-world GC.
func (obj *Obj) CollectGarbage(ctx context.Context, reachableObj ReachableObjectsObj) error {
	if err := obj.collectGarbagePrefix(ctx, cObjectKindBlob, reachableObj.BlobSet); err != nil {
		return err
	}
	return obj.collectGarbagePrefix(ctx, cObjectKindTree, reachableObj.TreeSet)
}

func (obj *Obj) orphanCandidatesPrefix(ctx context.Context, snapshotObj *pebble.Snapshot, kindObj objectKindObj, reachableObj map[core.HashObj]struct{}, batch *[]PendingObjectObj, batchSize int, yieldFunc func([]PendingObjectObj) error) error {
	keyTag, ok := objectTag(kindObj)
	if !ok {
		return errors.New("invalid pebble object kind")
	}
	iterObj, err := snapshotObj.NewIter(&pebble.IterOptions{
		LowerBound: []byte{keyTag},
		UpperBound: []byte{keyTag + 1},
	})
	if err != nil {
		return err
	}
	for iterObj.First(); iterObj.Valid(); iterObj.Next() {
		select {
		case <-ctx.Done():
			_ = iterObj.Close()
			return ctx.Err()
		default:
		}
		hashObj, validFlag := objectHashFromKey(kindObj, iterObj.Key())
		if !validFlag {
			continue
		}
		if _, reachableFlag := reachableObj[hashObj]; reachableFlag {
			continue
		}
		*batch = append(*batch, PendingObjectObj{kindObj: kindObj, hashObj: hashObj})
		if len(*batch) >= batchSize {
			if err = yieldFunc(*batch); err != nil {
				_ = iterObj.Close()
				return err
			}
			*batch = (*batch)[:0]
		}
	}
	err = iterObj.Error()
	if closeErr := iterObj.Close(); err == nil {
		err = closeErr
	}
	return err
}

// OrphanCandidates returns delete candidates in batches under one snapshot.
// The snapshot fixes the scan point; objects written after start are picked up by the next pass.
func (obj *Obj) OrphanCandidates(ctx context.Context, reachableObj ReachableObjectsObj, batchSize int, yieldFunc func([]PendingObjectObj) error) error {
	snapshotObj := obj.dbObj.NewSnapshot()
	defer snapshotObj.Close()

	batch := make([]PendingObjectObj, 0, batchSize)
	if err := obj.orphanCandidatesPrefix(ctx, snapshotObj, cObjectKindBlob, reachableObj.BlobSet, &batch, batchSize, yieldFunc); err != nil {
		return err
	}
	if err := obj.orphanCandidatesPrefix(ctx, snapshotObj, cObjectKindTree, reachableObj.TreeSet, &batch, batchSize, yieldFunc); err != nil {
		return err
	}
	if len(batch) == 0 {
		return nil
	}
	return yieldFunc(batch)
}
