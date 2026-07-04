package pager

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	// PageSize is the internal keyset batch size; RAM is limited to one page.
	PageSize = 1024

	// MaxVersions caps versions in one built list to limit cold-build work on huge histories.
	MaxVersions = 10000
)

// // // // // // // // // //

// VersionListerInterface provides keyset access to key versions.
type VersionListerInterface interface {
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
}

// // // // // // // // // //

// EachVersion walks key versions newest-first in PageSize batches while holding one page in RAM.
// The callback can stop the walk or return an error; the cursor advances by the last page item.
func EachVersion(ctx context.Context, store VersionListerInterface, key string, cb func(core.VersionObj) (stop bool, err error)) error {
	afterSeq, afterVersion := int64(0), ""
	for {
		pageArr, err := store.ListVersionsKeyset(ctx, key, false, afterSeq, afterVersion, PageSize)
		if err != nil {
			return err
		}
		for i := range pageArr {
			stop, cbErr := cb(pageArr[i])
			if cbErr != nil {
				return cbErr
			}
			if stop {
				return nil
			}
		}
		if len(pageArr) < PageSize {
			return nil
		}
		lastObj := pageArr[len(pageArr)-1]
		afterSeq, afterVersion = lastObj.UpstreamSeq, lastObj.Version
	}
}
