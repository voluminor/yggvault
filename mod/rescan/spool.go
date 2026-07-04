package rescan

import (
	"context"
	"fmt"
	"os"
	"sort"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// blobStoreInterface reads durable blobs as a fallback when the spool misses.
// Brother ingest does not restage blobs that already exist in storage.
type blobStoreInterface interface {
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}

type spoolSourceObj struct {
	treeArr    []core.TreeEntryObj
	pathByHash map[core.HashObj]string
	storeObj   blobStoreInterface
}

func newSpoolSource(treeArr []core.TreeEntryObj, blobArr []core.StagedBlobObj, storeObj blobStoreInterface) spoolSourceObj {
	pathByHash := make(map[core.HashObj]string, len(blobArr))
	for _, blobObj := range blobArr {
		pathByHash[blobObj.BlobHash] = blobObj.FilePath
	}
	sortedArr := append([]core.TreeEntryObj(nil), treeArr...)
	sort.Slice(sortedArr, func(i, j int) bool { return sortedArr[i].Path < sortedArr[j].Path })
	return spoolSourceObj{treeArr: sortedArr, pathByHash: pathByHash, storeObj: storeObj}
}

// ReadTree returns the staged version tree sorted by path; the hash is ignored because the source has one tree.
func (s spoolSourceObj) ReadTree(_ context.Context, _ core.HashObj) ([]core.TreeEntryObj, error) {
	return s.treeArr, nil
}

// ReadBlob reads a staged blob from spool, then falls back to storage for deduplicated brother blobs.
func (s spoolSourceObj) ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error) {
	pathText, ok := s.pathByHash[hashObj]
	if ok {
		return os.ReadFile(pathText)
	}
	if s.storeObj == nil {
		return nil, fmt.Errorf("staged blob not found in spool: %s", hashObj.Hex())
	}
	dataArr, err := s.storeObj.ReadBlob(ctx, hashObj)
	if err != nil {
		return nil, fmt.Errorf("staged blob not found in spool and storage read failed: %s: %w", hashObj.Hex(), err)
	}
	return dataArr, nil
}
