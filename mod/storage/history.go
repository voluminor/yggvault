package storage

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// ListPublishFeed returns newest-first publish events for Atom feeds.
// An empty key means all keys; the method is read-only and does not take writeMu.
func (obj *Obj) ListPublishFeed(ctx context.Context, key string, limit int) ([]core.FeedEventObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if key != "" {
		if err = validateKey(key); err != nil {
			return nil, err
		}
	}
	return obj.indexObj.ListPublishFeed(ctx, key, limit)
}
