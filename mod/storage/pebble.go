package storage

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const cPublishCleanupTimeout = 30 * time.Second

// //

func (obj *Obj) readTreeInternal(treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	dataArr, err := obj.pebbleStoreObj.ReadTree(treeHashObj)
	if err != nil {
		return nil, err
	}
	return treecodec.Decode(dataArr)
}

func (obj *Obj) pendingObjectReferenced(ctx context.Context, pendingObj pebblestore.PendingObjectObj) (bool, error) {
	switch {
	case pendingObj.IsBlob():
		return obj.indexObj.BlobReferenced(ctx, pendingObj.Hash())
	case pendingObj.IsTree():
		return obj.indexObj.TreeReferenced(ctx, pendingObj.Hash())
	default:
		return false, errors.New("unknown pebble object kind")
	}
}

func (obj *Obj) cleanupUnreferencedPendingObjects(ctx context.Context, pendingObj pebblestore.PendingObjectsObj) error {
	if len(pendingObj.ObjectArr) == 0 {
		return nil
	}
	deleteArr := make([]pebblestore.PendingObjectObj, 0, len(pendingObj.ObjectArr))
	for i := range pendingObj.ObjectArr {
		referencedFlag, err := obj.pendingObjectReferenced(ctx, pendingObj.ObjectArr[i])
		if err != nil {
			return err
		}
		if referencedFlag {
			continue
		}
		deleteArr = append(deleteArr, pendingObj.ObjectArr[i])
	}
	return obj.pebbleStoreObj.DeleteObjects(ctx, deleteArr)
}

func (obj *Obj) cleanupPublishFailure(pendingObj pebblestore.PendingObjectsObj, causeErr error) error {
	cleanupCtx, cancelFunc := context.WithTimeout(obj.rootCtx, cPublishCleanupTimeout)
	defer cancelFunc()

	cleanupErr := obj.cleanupUnreferencedPendingObjects(cleanupCtx, pendingObj)
	if cleanupErr != nil {
		return errors.Join(causeErr, fmt.Errorf("cleanup unreferenced pebble objects: %w", cleanupErr))
	}
	return causeErr
}

// //

func (obj *Obj) acquireInFlight(ctx context.Context) (func(), error) {
	if obj.inFlightSlots == nil {
		return func() {}, nil
	}
	select {
	case obj.inFlightSlots <- struct{}{}:
		return func() { <-obj.inFlightSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (obj *Obj) shouldVerifyDurableRead() bool {
	switch obj.configObj.Storage.Pebble.VerifyOnRead {
	case stcfg.DurableVerifyOnReadNever:
		return false
	case stcfg.DurableVerifyOnReadSampled:
		return obj.durableVerifySampleCounter.Add(1)%cHotVerifySampleRate == 0
	default:
		return true
	}
}

func buildInFlightSlots(budgetBytes uint64, perObjectBytes uint64) chan struct{} {
	if budgetBytes == 0 || perObjectBytes == 0 {
		return nil
	}
	slots := budgetBytes / perObjectBytes
	if slots < 1 {
		slots = 1
	}
	return make(chan struct{}, slots)
}

// ReadBlob reads a durable blob under an in-flight slot.
// Build paths can carry a snapshot in ctx so all reads use the same cut; verify_on_read follows policy.
func (obj *Obj) ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	releaseSlot, err := obj.acquireInFlight(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	if snapshotObj := readSnapshotFrom(ctx); snapshotObj != nil {
		if obj.shouldVerifyDurableRead() {
			return snapshotObj.ReadBlob(hashObj)
		}
		return snapshotObj.ReadBlobUnverified(hashObj)
	}
	if obj.shouldVerifyDurableRead() {
		return obj.pebbleStoreObj.ReadBlob(hashObj)
	}
	return obj.pebbleStoreObj.ReadBlobUnverified(hashObj)
}

// ReadTree reads and decodes a tree under an in-flight slot, using ctx snapshot and verify_on_read like ReadBlob.
func (obj *Obj) ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	releaseSlot, err := obj.acquireInFlight(ctx)
	if err != nil {
		return nil, err
	}
	defer releaseSlot()

	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	var dataArr []byte
	if snapshotObj := readSnapshotFrom(ctx); snapshotObj != nil {
		if obj.shouldVerifyDurableRead() {
			dataArr, err = snapshotObj.ReadTree(treeHashObj)
		} else {
			dataArr, err = snapshotObj.ReadTreeUnverified(treeHashObj)
		}
	} else if obj.shouldVerifyDurableRead() {
		dataArr, err = obj.pebbleStoreObj.ReadTree(treeHashObj)
	} else {
		dataArr, err = obj.pebbleStoreObj.ReadTreeUnverified(treeHashObj)
	}
	if err != nil {
		return nil, err
	}
	return treecodec.Decode(dataArr)
}

// FilterMissingBlobs returns the subset of hashArr absent from durable storage.
// Brother sync fetches only missing blobs; the tree is the manifest and shared blobs are skipped locally.
func (obj *Obj) FilterMissingBlobs(ctx context.Context, hashArr []core.HashObj) ([]core.HashObj, error) {
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
	return obj.pebbleStoreObj.MissingBlobs(hashArr)
}
