package storage

import (
	"context"

	"github.com/voluminor/yggvault/mod/storage/pebblestore"
)

// // // // // // // // // //

type readSnapshotKeyObj struct{}

func withReadSnapshot(ctx context.Context, snapshotObj *pebblestore.SnapshotObj) context.Context {
	return context.WithValue(ctx, readSnapshotKeyObj{}, snapshotObj)
}

func readSnapshotFrom(ctx context.Context) *pebblestore.SnapshotObj {
	snapshotObj, _ := ctx.Value(readSnapshotKeyObj{}).(*pebblestore.SnapshotObj)
	return snapshotObj
}
