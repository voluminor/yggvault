package pebblestore

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"

	"github.com/cockroachdb/pebble/v2"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const cMaxReadAllBytes = uint64(1<<63 - 2)

const maxIntValue = uint64(math.MaxInt)

type pendingValueWriteFunc func(*pebble.Batch, PendingObjectObj) (uint64, error)

type pendingValueLoaderFunc func(PendingObjectObj) (uint64, pendingValueWriteFunc, error)

// //

func objectKeyForKind(kindObj objectKindObj, hashObj core.HashObj) (keyObj, error) {
	switch kindObj {
	case cObjectKindBlob:
		return blobKey(hashObj), nil
	case cObjectKindTree:
		return treeKey(hashObj), nil
	default:
		return keyObj{}, errors.New("invalid pebble object kind")
	}
}

func checkedObjectKey(kindObj objectKindObj, hashObj core.HashObj, valueArr []byte) (keyObj, error) {
	keyValue, err := objectKeyForKind(kindObj, hashObj)
	if err != nil {
		return keyObj{}, err
	}
	if err = core.VerifyHash(hashObj, valueArr); err != nil {
		return keyObj{}, fmt.Errorf("pebble value hash mismatch: %w", err)
	}
	return keyValue, nil
}

func setObject(batchObj *pebble.Batch, keyValue keyObj, valueArr []byte) error {
	return batchObj.Set(keyValue[:], valueArr, nil)
}

func storedValid(readerObj pebble.Reader, keyValue keyObj, hashObj core.HashObj) (bool, error) {
	valueArr, closeFunc, ok, err := getValue(readerObj, keyValue)
	if err != nil {
		return false, err
	}
	if !ok {
		return false, nil
	}
	verifyErr := core.VerifyHash(hashObj, valueArr)
	closeErr := closeFunc()
	if closeErr != nil {
		return false, closeErr
	}
	return verifyErr == nil, nil
}

func pendingObjectValue(pendingObj PendingObjectObj, contentByHashObj map[core.HashObj][]byte, treeDataArr []byte, treeHashObj core.HashObj) ([]byte, error) {
	switch {
	case pendingObj.IsBlob():
		contentArr, ok := contentByHashObj[pendingObj.hashObj]
		if !ok {
			return nil, errors.New("pending blob content is missing")
		}
		return contentArr, nil
	case pendingObj.IsTree():
		if pendingObj.hashObj != treeHashObj {
			return nil, errors.New("pending tree content is missing")
		}
		return treeDataArr, nil
	default:
		return nil, errors.New("invalid pebble object kind")
	}
}

func pendingObjectFileSize(pendingObj PendingObjectObj, fileSizeByHashObj map[core.HashObj]uint64, treeDataArr []byte, treeHashObj core.HashObj) (uint64, error) {
	switch {
	case pendingObj.IsBlob():
		sizeBytes, ok := fileSizeByHashObj[pendingObj.hashObj]
		if !ok {
			return 0, errors.New("pending staged blob size is missing")
		}
		return sizeBytes, nil
	case pendingObj.IsTree():
		if pendingObj.hashObj != treeHashObj {
			return 0, errors.New("pending tree content is missing")
		}
		return uint64(len(treeDataArr)), nil
	default:
		return 0, errors.New("invalid pebble object kind")
	}
}

func setPendingObjectBytes(batchObj *pebble.Batch, pendingObj PendingObjectObj, valueArr []byte) (uint64, error) {
	keyValue, err := checkedObjectKey(pendingObj.kindObj, pendingObj.hashObj, valueArr)
	if err != nil {
		return 0, fmt.Errorf("write object: %w", err)
	}
	if err = setObject(batchObj, keyValue, valueArr); err != nil {
		return 0, fmt.Errorf("write object: %w", err)
	}
	return uint64(len(valueArr)), nil
}

func setPendingObjectOpenFile(batchObj *pebble.Batch, pendingObj PendingObjectObj, openFunc func(core.HashObj) (*os.File, uint64, error), fileSizeByHashObj map[core.HashObj]uint64, treeDataArr []byte, treeHashObj core.HashObj) (uint64, error) {
	switch {
	case pendingObj.IsBlob():
		if openFunc == nil {
			return 0, errors.New("pending staged blob opener is nil")
		}
		sizeBytes, ok := fileSizeByHashObj[pendingObj.hashObj]
		if !ok {
			return 0, errors.New("pending staged blob size is missing")
		}
		if sizeBytes > cMaxReadAllBytes || sizeBytes > maxIntValue {
			return 0, errors.New("pending staged blob is too large to read")
		}
		fileObj, openedSizeBytes, err := openFunc(pendingObj.hashObj)
		if err != nil {
			return 0, err
		}
		if fileObj == nil {
			return 0, errors.New("pending staged blob file is missing")
		}
		defer fileObj.Close()
		if openedSizeBytes != sizeBytes {
			return 0, errors.New("pending staged blob size changed")
		}

		keyValue, err := objectKeyForKind(pendingObj.kindObj, pendingObj.hashObj)
		if err != nil {
			return 0, err
		}
		deferredObj := batchObj.SetDeferred(len(keyValue), int(sizeBytes))
		copy(deferredObj.Key, keyValue[:])
		n, err := io.ReadFull(fileObj, deferredObj.Value)
		if err != nil {
			return 0, err
		}
		if uint64(n) != sizeBytes {
			return 0, errors.New("pending staged blob size changed")
		}
		if err = core.VerifyHash(pendingObj.hashObj, deferredObj.Value); err != nil {
			return 0, fmt.Errorf("write object: %w", err)
		}
		if err = deferredObj.Finish(); err != nil {
			return 0, fmt.Errorf("write object: %w", err)
		}
		return sizeBytes, nil
	case pendingObj.IsTree():
		if pendingObj.hashObj != treeHashObj {
			return 0, errors.New("pending tree content is missing")
		}
		return setPendingObjectBytes(batchObj, pendingObj, treeDataArr)
	default:
		return 0, errors.New("invalid pebble object kind")
	}
}

func (obj *Obj) writeMissingObjectsFromLoader(ctx context.Context, pendingObj PendingObjectsObj, loaderFunc pendingValueLoaderFunc) error {
	if len(pendingObj.ObjectArr) == 0 {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if loaderFunc == nil {
		return errors.New("pending object loader is nil")
	}
	batchObj := obj.dbObj.NewBatch()
	var batchBytes uint64
	var batchObjects uint
	defer func() {
		if batchObj != nil {
			_ = batchObj.Close()
		}
	}()
	commitBatch := func(syncMode *pebble.WriteOptions) error {
		if batchObj.Empty() {
			return nil
		}
		commitErr := batchObj.Commit(syncMode)
		closeErr := batchObj.Close()
		batchObj = obj.dbObj.NewBatch()
		batchBytes = 0
		batchObjects = 0
		if commitErr != nil {
			return commitErr
		}
		return closeErr
	}

	for i := range pendingObj.ObjectArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		pendingItemObj := pendingObj.ObjectArr[i]
		valueSizeBytes, writeFunc, err := loaderFunc(pendingItemObj)
		if err != nil {
			return err
		}
		if writeFunc == nil {
			return errors.New("pending object value writer is nil")
		}
		if batchObjects > 0 && (batchObjects >= cWriteBatchMaxObjects ||
			valueSizeBytes > ^uint64(0)-batchBytes ||
			batchBytes+valueSizeBytes > cWriteBatchMaxBytes) {
			if err = commitBatch(pebble.NoSync); err != nil {
				return err
			}
		}
		writtenBytes, err := writeFunc(batchObj, pendingItemObj)
		if err != nil {
			return err
		}
		if writtenBytes != valueSizeBytes {
			return errors.New("pending object size changed")
		}
		batchBytes += writtenBytes
		batchObjects++
	}

	return commitBatch(pebble.Sync)
}

func (obj *Obj) deleteKeys(ctx context.Context, keyArr [][]byte) error {
	for len(keyArr) > 0 {
		chunkSize := min(len(keyArr), cDeleteBatchKeys)
		batchObj := obj.dbObj.NewBatch()
		for _, keyObj := range keyArr[:chunkSize] {
			select {
			case <-ctx.Done():
				_ = batchObj.Close()
				return ctx.Err()
			default:
			}
			if err := batchObj.Delete(keyObj, nil); err != nil {
				_ = batchObj.Close()
				return err
			}
		}
		if err := batchObj.Commit(pebble.Sync); err != nil {
			_ = batchObj.Close()
			return err
		}
		if err := batchObj.Close(); err != nil {
			return err
		}
		keyArr = keyArr[chunkSize:]
	}
	return nil
}

// //

// PutBlob atomically writes one blob with Sync after verifying its content hash.
func (obj *Obj) PutBlob(hashObj core.HashObj, dataArr []byte) error {
	batchObj := obj.dbObj.NewBatch()
	defer batchObj.Close()
	keyValue, err := checkedObjectKey(cObjectKindBlob, hashObj, dataArr)
	if err != nil {
		return err
	}
	if err = setObject(batchObj, keyValue, dataArr); err != nil {
		return err
	}
	return batchObj.Commit(pebble.Sync)
}

// VerifyReferences checks that each tree entry points to an existing blob with the expected size.
// Provided content is hash-verified; stored content is looked up before tree writes.
func (obj *Obj) VerifyReferences(ctx context.Context, treeArr []core.TreeEntryObj, contentByHashObj map[core.HashObj][]byte) error {
	verifiedObj := make(map[core.HashObj]uint64, len(contentByHashObj))
	for hashObj, contentArr := range contentByHashObj {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		if err := core.VerifyHash(hashObj, contentArr); err != nil {
			return fmt.Errorf("provided blob %s: %w", hashObj.Hex(), err)
		}
		verifiedObj[hashObj] = uint64(len(contentArr))
	}

	for i := range treeArr {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		entryObj := treeArr[i]
		sizeBytes, ok := verifiedObj[entryObj.BlobHash]
		if ok {
			if sizeBytes != entryObj.SizeBytes {
				return fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
			}
			continue
		}
		err := obj.UseBlob(entryObj.BlobHash, func(blobArr []byte) error {
			if uint64(len(blobArr)) != entryObj.SizeBytes {
				return fmt.Errorf("referenced blob %s size mismatch", entryObj.BlobHash.Hex())
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

// PendingObjects selects blob and tree objects absent from Pebble by verified hash.
// blobSizeObj carries sizes for quota checks.
func (obj *Obj) PendingObjects(ctx context.Context, blobSizeObj map[core.HashObj]uint64, treeDataArr []byte, treeHashObj core.HashObj) (PendingObjectsObj, error) {
	resultObj := PendingObjectsObj{}
	for hashObj, sizeBytes := range blobSizeObj {
		select {
		case <-ctx.Done():
			return resultObj, ctx.Err()
		default:
		}
		validFlag, err := storedValid(obj.dbObj, blobKey(hashObj), hashObj)
		if err != nil {
			return resultObj, err
		}
		if validFlag {
			continue
		}
		resultObj.SizeBytes += sizeBytes
		resultObj.ObjectArr = append(resultObj.ObjectArr, PendingObjectObj{
			kindObj: cObjectKindBlob,
			hashObj: hashObj,
		})
	}
	validFlag, err := storedValid(obj.dbObj, treeKey(treeHashObj), treeHashObj)
	if err != nil {
		return resultObj, err
	}
	if validFlag {
		return resultObj, nil
	}
	resultObj.SizeBytes += uint64(len(treeDataArr))
	resultObj.ObjectArr = append(resultObj.ObjectArr, PendingObjectObj{
		kindObj: cObjectKindTree,
		hashObj: treeHashObj,
	})
	return resultObj, nil
}

// The caller has already verified tree links to blob objects.
func (obj *Obj) WriteMissingObjects(ctx context.Context, pendingObj PendingObjectsObj, contentByHashObj map[core.HashObj][]byte, treeDataArr []byte, treeHashObj core.HashObj) error {
	return obj.writeMissingObjectsFromLoader(ctx, pendingObj, func(pendingItemObj PendingObjectObj) (uint64, pendingValueWriteFunc, error) {
		valueArr, err := pendingObjectValue(pendingItemObj, contentByHashObj, treeDataArr, treeHashObj)
		if err != nil {
			return 0, nil, err
		}
		return uint64(len(valueArr)), func(batchObj *pebble.Batch, pendingItemObj PendingObjectObj) (uint64, error) {
			return setPendingObjectBytes(batchObj, pendingItemObj, valueArr)
		}, nil
	})
}

// The caller reopens staged files before writing each blob.
func (obj *Obj) WriteMissingObjectsFromFileOpener(ctx context.Context, pendingObj PendingObjectsObj, openFunc func(core.HashObj) (*os.File, uint64, error), fileSizeByHashObj map[core.HashObj]uint64, treeDataArr []byte, treeHashObj core.HashObj) error {
	return obj.writeMissingObjectsFromLoader(ctx, pendingObj, func(pendingItemObj PendingObjectObj) (uint64, pendingValueWriteFunc, error) {
		valueSizeBytes, err := pendingObjectFileSize(pendingItemObj, fileSizeByHashObj, treeDataArr, treeHashObj)
		if err != nil {
			return 0, nil, err
		}
		return valueSizeBytes, func(batchObj *pebble.Batch, pendingItemObj PendingObjectObj) (uint64, error) {
			return setPendingObjectOpenFile(batchObj, pendingItemObj, openFunc, fileSizeByHashObj, treeDataArr, treeHashObj)
		}, nil
	})
}

// DeleteObjects deletes specified blobs and trees in batches with Sync per batch under the caller's lock.
func (obj *Obj) DeleteObjects(ctx context.Context, objectArr []PendingObjectObj) error {
	keyArr := make([][]byte, 0, len(objectArr))
	for _, objectObj := range objectArr {
		var keyValue keyObj
		switch {
		case objectObj.IsBlob():
			keyValue = blobKey(objectObj.hashObj)
		case objectObj.IsTree():
			keyValue = treeKey(objectObj.hashObj)
		default:
			return errors.New("invalid pebble object kind")
		}
		keyArr = append(keyArr, append([]byte(nil), keyValue[:]...))
	}
	return obj.deleteKeys(ctx, keyArr)
}
