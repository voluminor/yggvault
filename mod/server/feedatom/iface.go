package feedatom

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

// StateReaderInterface reports key existence for empty per-key feed 404 handling.
type StateReaderInterface interface {
	KeyState(key string) (state.KeyStateObj, bool)
}

// FeedReaderInterface reads publish history for feeds; an empty key means the global feed.
type FeedReaderInterface interface {
	ListPublishFeed(ctx context.Context, key string, limit int) ([]core.FeedEventObj, error)
}
