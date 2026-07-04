package pebblestore

import (
	"bytes"
	"context"
	"errors"
	"sort"

	"github.com/cockroachdb/pebble/v2"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// ErrObjectNotFound is returned instead of pebble.ErrNotFound for missing objects.
var ErrObjectNotFound = errors.New("pebble object not found")

// //

func getValue(readerObj pebble.Reader, keyObj keyObj) ([]byte, func() error, bool, error) {
	valueArr, closerObj, err := readerObj.Get(keyObj[:])
	if errors.Is(err, pebble.ErrNotFound) {
		return nil, nil, false, nil
	}
	if err != nil {
		return nil, nil, false, err
	}
	return valueArr, closerObj.Close, true, nil
}

func getCopy(readerObj pebble.Reader, keyObj keyObj) ([]byte, bool, error) {
	valueArr, closeFunc, ok, err := getValue(readerObj, keyObj)
	if err != nil {
		return nil, false, err
	}
	if !ok {
		return nil, false, nil
	}
	resultArr := append([]byte(nil), valueArr...)
	if err = closeFunc(); err != nil {
		return nil, false, err
	}
	return resultArr, true, nil
}

func useBlob(readerObj pebble.Reader, hashObj core.HashObj, useFunc func([]byte) error) error {
	valueArr, closeFunc, ok, err := getValue(readerObj, blobKey(hashObj))
	if err != nil {
		return err
	}
	if !ok {
		return ErrObjectNotFound
	}
	if err = core.VerifyHash(hashObj, valueArr); err != nil {
		closeErr := closeFunc()
		return errors.Join(err, closeErr)
	}
	err = useFunc(valueArr)
	closeErr := closeFunc()
	if err != nil {
		return err
	}
	return closeErr
}

func useBlobUnverified(readerObj pebble.Reader, hashObj core.HashObj, useFunc func([]byte) error) error {
	valueArr, closeFunc, ok, err := getValue(readerObj, blobKey(hashObj))
	if err != nil {
		return err
	}
	if !ok {
		return ErrObjectNotFound
	}
	err = useFunc(valueArr)
	closeErr := closeFunc()
	if err != nil {
		return err
	}
	return closeErr
}

func readVerified(readerObj pebble.Reader, keyValue keyObj, hashObj core.HashObj) ([]byte, error) {
	valueArr, ok, err := getCopy(readerObj, keyValue)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrObjectNotFound
	}
	if err = core.VerifyHash(hashObj, valueArr); err != nil {
		return nil, err
	}
	return valueArr, nil
}

func readTree(readerObj pebble.Reader, hashObj core.HashObj) ([]byte, error) {
	return readVerified(readerObj, treeKey(hashObj), hashObj)
}

func readUnverified(readerObj pebble.Reader, keyValue keyObj) ([]byte, error) {
	valueArr, ok, err := getCopy(readerObj, keyValue)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrObjectNotFound
	}
	return valueArr, nil
}

// ReadBlobUnverified returns a blob copy without rehashing.
func (obj *Obj) ReadBlobUnverified(hashObj core.HashObj) ([]byte, error) {
	return readUnverified(obj.dbObj, blobKey(hashObj))
}

// ReadTreeUnverified returns a tree copy without rehashing.
func (obj *Obj) ReadTreeUnverified(hashObj core.HashObj) ([]byte, error) {
	return readUnverified(obj.dbObj, treeKey(hashObj))
}

// blobArr is valid only until useFunc returns.
func (obj *Obj) UseBlob(hashObj core.HashObj, useFunc func([]byte) error) error {
	return useBlob(obj.dbObj, hashObj, useFunc)
}

// ReadBlob returns a blob copy after hash verification.
func (obj *Obj) ReadBlob(hashObj core.HashObj) ([]byte, error) {
	return readVerified(obj.dbObj, blobKey(hashObj), hashObj)
}

// ReadTree returns a tree copy after hash verification.
func (obj *Obj) ReadTree(hashObj core.HashObj) ([]byte, error) {
	return readTree(obj.dbObj, hashObj)
}

// NewSnapshot opens a read-only snapshot that the caller must close.
func (obj *Obj) NewSnapshot() *SnapshotObj {
	return &SnapshotObj{snapshotObj: obj.dbObj.NewSnapshot()}
}

// Close releases the snapshot and is safe for a nil receiver.
func (obj *SnapshotObj) Close() error {
	if obj == nil || obj.snapshotObj == nil {
		return nil
	}
	return obj.snapshotObj.Close()
}

// blobArr is valid only until useFunc returns.
func (obj *SnapshotObj) UseBlob(hashObj core.HashObj, useFunc func([]byte) error) error {
	if obj == nil || obj.snapshotObj == nil {
		return errors.New("pebble snapshot is nil")
	}
	return useBlob(obj.snapshotObj, hashObj, useFunc)
}

// ReadTree returns a verified tree copy from the snapshot.
func (obj *SnapshotObj) ReadTree(hashObj core.HashObj) ([]byte, error) {
	if obj == nil || obj.snapshotObj == nil {
		return nil, errors.New("pebble snapshot is nil")
	}
	return readTree(obj.snapshotObj, hashObj)
}

// ReadBlob returns a verified blob copy from the snapshot.
func (obj *SnapshotObj) ReadBlob(hashObj core.HashObj) ([]byte, error) {
	if obj == nil || obj.snapshotObj == nil {
		return nil, errors.New("pebble snapshot is nil")
	}
	return readVerified(obj.snapshotObj, blobKey(hashObj), hashObj)
}

// ReadBlobUnverified returns a blob copy from the snapshot without rehashing.
func (obj *SnapshotObj) ReadBlobUnverified(hashObj core.HashObj) ([]byte, error) {
	if obj == nil || obj.snapshotObj == nil {
		return nil, errors.New("pebble snapshot is nil")
	}
	return readUnverified(obj.snapshotObj, blobKey(hashObj))
}

// blobArr is valid only until useFunc returns; no rehash is performed.
func (obj *SnapshotObj) UseBlobUnverified(hashObj core.HashObj, useFunc func([]byte) error) error {
	if obj == nil || obj.snapshotObj == nil {
		return errors.New("pebble snapshot is nil")
	}
	return useBlobUnverified(obj.snapshotObj, hashObj, useFunc)
}

// ReadTreeUnverified returns a tree copy from the snapshot without rehashing.
func (obj *SnapshotObj) ReadTreeUnverified(hashObj core.HashObj) ([]byte, error) {
	if obj == nil || obj.snapshotObj == nil {
		return nil, errors.New("pebble snapshot is nil")
	}
	return readUnverified(obj.snapshotObj, treeKey(hashObj))
}

// MissingBlobs returns the subset of hashArr that is absent from Pebble.
// The check uses one iterator without reading values, keeping brother anti-refetch cheap.
// Hashes are sorted ascending, so the iterator moves monotonically without backward seeks.
func (obj *Obj) MissingBlobs(hashArr []core.HashObj) ([]core.HashObj, error) {
	if len(hashArr) == 0 {
		return nil, nil
	}
	sortedArr := append([]core.HashObj(nil), hashArr...)
	sort.Slice(sortedArr, func(i, j int) bool { return bytes.Compare(sortedArr[i][:], sortedArr[j][:]) < 0 })
	iterObj, err := obj.dbObj.NewIter(&pebble.IterOptions{
		LowerBound: []byte{cBlobTag},
		UpperBound: []byte{cBlobTag + 1},
	})
	if err != nil {
		return nil, err
	}
	defer func() { _ = iterObj.Close() }()
	missingArr := make([]core.HashObj, 0, len(sortedArr))
	for i := range sortedArr {
		keyValue := blobKey(sortedArr[i])
		if iterObj.SeekGE(keyValue[:]) && bytes.Equal(iterObj.Key(), keyValue[:]) {
			continue
		}
		missingArr = append(missingArr, sortedArr[i])
	}
	return missingArr, iterObj.Error()
}

// ForEachBlobScan walks all blobs and passes the hash-verification result to useFunc.
// verify_on_read is ignored: this is integrity/repair, not serving; a non-nil useFunc error stops the scan.
func (obj *Obj) ForEachBlobScan(ctx context.Context, useFunc func(scanObj BlobScanObj) error) error {
	iterObj, err := obj.dbObj.NewIter(&pebble.IterOptions{
		LowerBound: []byte{cBlobTag},
		UpperBound: []byte{cBlobTag + 1},
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
		valueArr, valueErr := iterObj.ValueAndErr()
		if valueErr != nil {
			_ = iterObj.Close()
			return valueErr
		}
		hashObj, ok := objectHashFromKey(cObjectKindBlob, iterObj.Key())
		if !ok {
			if err = useFunc(BlobScanObj{Err: errors.New("pebble blob key prefix mismatch")}); err != nil {
				_ = iterObj.Close()
				return err
			}
			continue
		}
		verifyErr := core.VerifyHash(hashObj, valueArr)
		if err = useFunc(BlobScanObj{Hash: hashObj, Err: verifyErr}); err != nil {
			_ = iterObj.Close()
			return err
		}
	}
	err = iterObj.Error()
	if closeErr := iterObj.Close(); err == nil {
		err = closeErr
	}
	return err
}
