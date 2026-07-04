package pebblestore

import (
	"github.com/cockroachdb/pebble/v2"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	cBlobTag byte = 'b'
	cTreeTag byte = 't'

	cKeySize = 1 + core.HashSize

	cDeleteBatchKeys      = 4096
	cWriteBatchMaxBytes   = 16 * 1024 * 1024
	cWriteBatchMaxObjects = 1024
)

// //

// PendingObjectObj is one blob or tree selected for writing after hash verification.
type PendingObjectObj struct {
	kindObj objectKindObj
	hashObj core.HashObj
}

// PendingObjectsObj contains missing objects and total size for quota checks.
type PendingObjectsObj struct {
	SizeBytes uint64
	ObjectArr []PendingObjectObj
}

// ReachableObjectsObj contains reachable blob and tree hashes; GC treats everything else as garbage.
type ReachableObjectsObj struct {
	BlobSet map[core.HashObj]struct{}
	TreeSet map[core.HashObj]struct{}
}

// BlobScanObj is one full-scan blob verification result.
type BlobScanObj struct {
	Hash core.HashObj
	Err  error
}

// Obj is the Pebble-backed content-addressed blob/tree object store.
type Obj struct {
	dbObj       *pebble.DB
	countersObj *pebbleEventCountersObj
}

// SnapshotObj is a read-only Pebble snapshot at a fixed point; callers must Close it.
type SnapshotObj struct {
	snapshotObj *pebble.Snapshot
}

type objectKindObj byte

type keyObj [cKeySize]byte

// //

// Hash returns the object content hash.
func (obj PendingObjectObj) Hash() core.HashObj {
	return obj.hashObj
}

// IsBlob reports whether the object is a blob.
func (obj PendingObjectObj) IsBlob() bool {
	return obj.kindObj == cObjectKindBlob
}

// IsTree reports whether the object is a tree.
func (obj PendingObjectObj) IsTree() bool {
	return obj.kindObj == cObjectKindTree
}

// PendingBlob builds a pending blob object.
func PendingBlob(hashObj core.HashObj) PendingObjectObj {
	return PendingObjectObj{kindObj: cObjectKindBlob, hashObj: hashObj}
}

// PendingTree builds a pending tree object.
func PendingTree(hashObj core.HashObj) PendingObjectObj {
	return PendingObjectObj{kindObj: cObjectKindTree, hashObj: hashObj}
}
