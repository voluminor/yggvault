package sqliteindex

import (
	"bytes"
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func seedBlobRef(t *testing.T, indexObj *Obj, hashObj core.HashObj, refcount uint64, size uint64) {
	t.Helper()
	_, err := indexObj.dbObj.ExecContext(context.Background(),
		"INSERT INTO blob_refs(blob_hash, refcount, size_bytes) VALUES (?, ?, ?)",
		hashObj.BytesCopy(), refcount, size)
	if err != nil {
		t.Fatalf("seed blob_ref returned error: %v", err)
	}
}

func seedVersion(t *testing.T, indexObj *Obj, key string, version string, treeHashObj core.HashObj) {
	t.Helper()
	sourceHashObj := core.HashBytes([]byte(key + version))
	_, err := indexObj.dbObj.ExecContext(context.Background(),
		"INSERT INTO versions(key, version, source_hash, source_size_bytes, tree_hash, ingest_ts) VALUES (?, ?, ?, ?, ?, ?)",
		key, version, sourceHashObj.BytesCopy(), 1, treeHashObj.BytesCopy(), core.FormatTime(time.Now()))
	if err != nil {
		t.Fatalf("seed version returned error: %v", err)
	}
}

func sortedHashes(hashArr []core.HashObj) []core.HashObj {
	outArr := append([]core.HashObj(nil), hashArr...)
	sort.Slice(outArr, func(i, j int) bool {
		return bytes.Compare(outArr[i][:], outArr[j][:]) < 0
	})
	return outArr
}

func walkHashPages(t *testing.T, pageFunc func(context.Context, []byte, int) ([]core.HashObj, error)) []core.HashObj {
	t.Helper()
	ctx := context.Background()
	const limit = 2
	var collected []core.HashObj
	var afterHash []byte
	for {
		pageArr, err := pageFunc(ctx, afterHash, limit)
		if err != nil {
			t.Fatalf("page returned error: %v", err)
		}
		for i := range pageArr {
			if len(collected) > 0 && bytes.Compare(collected[len(collected)-1][:], pageArr[i][:]) >= 0 {
				t.Fatalf("hashes not strictly ascending across pages")
			}
			collected = append(collected, pageArr[i])
		}
		if len(pageArr) < limit {
			return collected
		}
		afterHash = pageArr[len(pageArr)-1].BytesCopy()
	}
}

func assertHashSetEqual(t *testing.T, label string, gotArr []core.HashObj, wantArr []core.HashObj) {
	t.Helper()
	wantSorted := sortedHashes(wantArr)
	if len(gotArr) != len(wantSorted) {
		t.Fatalf("%s: got %d hashes, want %d", label, len(gotArr), len(wantSorted))
	}
	for i := range gotArr {
		if gotArr[i] != wantSorted[i] {
			t.Fatalf("%s: hash[%d] mismatch", label, i)
		}
	}
}

// //

func TestReachablePagingHelpers(t *testing.T) {
	ctx := context.Background()
	indexObj, err := Open(ctx, tempSqlitePath(t))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() { _ = indexObj.Close() })

	blobInputArr := []struct {
		tag      string
		refcount uint64
		size     uint64
	}{
		{"blob-a", 3, 100},
		{"blob-b", 1, 200},
		{"blob-c", 0, 300},
		{"blob-d", 5, 400},
		{"blob-e", 0, 500},
		{"blob-f", 2, 600},
	}
	wantRefObj := make(map[core.HashObj]BlobRefObj)
	var allBlobArr []core.HashObj
	var liveBlobArr []core.HashObj
	for _, inputObj := range blobInputArr {
		hashObj := core.HashBytes([]byte(inputObj.tag))
		seedBlobRef(t, indexObj, hashObj, inputObj.refcount, inputObj.size)
		wantRefObj[hashObj] = BlobRefObj{Refcount: inputObj.refcount, SizeBytes: inputObj.size}
		allBlobArr = append(allBlobArr, hashObj)
		if inputObj.refcount > 0 {
			liveBlobArr = append(liveBlobArr, hashObj)
		}
	}

	treeInputArr := []struct {
		tag   string
		count int
	}{
		{"tree-1", 2},
		{"tree-2", 1},
		{"tree-3", 3},
		{"tree-4", 1},
	}
	wantCountObj := make(map[core.HashObj]uint64)
	var allTreeArr []core.HashObj
	for _, inputObj := range treeInputArr {
		treeHashObj := core.HashBytes([]byte(inputObj.tag))
		for j := 0; j < inputObj.count; j++ {
			seedVersion(t, indexObj, "k", fmt.Sprintf("%s-%d", inputObj.tag, j), treeHashObj)
		}
		wantCountObj[treeHashObj] = uint64(inputObj.count)
		allTreeArr = append(allTreeArr, treeHashObj)
	}

	assertHashSetEqual(t, "BlobRefHashesPage", walkHashPages(t, indexObj.BlobRefHashesPage), liveBlobArr)
	assertHashSetEqual(t, "TreeHashesPage", walkHashPages(t, indexObj.TreeHashesPage), allTreeArr)

	gotRefObj := make(map[core.HashObj]BlobRefObj)
	var afterBlob []byte
	for {
		pageArr, pageErr := indexObj.BlobRefsPage(ctx, afterBlob, 2)
		if pageErr != nil {
			t.Fatalf("BlobRefsPage returned error: %v", pageErr)
		}
		for _, rowObj := range pageArr {
			gotRefObj[rowObj.Hash] = rowObj.Ref
		}
		if len(pageArr) < 2 {
			break
		}
		afterBlob = pageArr[len(pageArr)-1].Hash.BytesCopy()
	}
	if len(gotRefObj) != len(wantRefObj) {
		t.Fatalf("BlobRefsPage: got %d rows, want %d", len(gotRefObj), len(wantRefObj))
	}
	for hashObj, wantRef := range wantRefObj {
		if gotRefObj[hashObj] != wantRef {
			t.Fatalf("BlobRefsPage: row %s mismatch: got %+v want %+v", hashObj.Hex(), gotRefObj[hashObj], wantRef)
		}
	}

	gotCountObj := make(map[core.HashObj]uint64)
	var afterTree []byte
	for {
		pageArr, pageErr := indexObj.TreeHashCountsPage(ctx, afterTree, 2)
		if pageErr != nil {
			t.Fatalf("TreeHashCountsPage returned error: %v", pageErr)
		}
		for _, rowObj := range pageArr {
			gotCountObj[rowObj.Hash] = rowObj.Count
		}
		if len(pageArr) < 2 {
			break
		}
		afterTree = pageArr[len(pageArr)-1].Hash.BytesCopy()
	}
	if len(gotCountObj) != len(wantCountObj) {
		t.Fatalf("TreeHashCountsPage: got %d groups, want %d", len(gotCountObj), len(wantCountObj))
	}
	for hashObj, wantCount := range wantCountObj {
		if gotCountObj[hashObj] != wantCount {
			t.Fatalf("TreeHashCountsPage: tree %s count=%d, want %d", hashObj.Hex(), gotCountObj[hashObj], wantCount)
		}
	}
}
