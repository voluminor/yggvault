package mirror

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/pager"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

// StateReaderInterface reads per-key state for authoritative latest version and key existence.
type StateReaderInterface interface {
	KeyState(key string) (state.KeyStateObj, bool)
}

// VersionReaderInterface provides latest and keyset access to versions.
type VersionReaderInterface interface {
	LatestVersion(ctx context.Context, key string) (core.VersionObj, bool, error)
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
}

// // // // // // // // // //

func eachVersion(ctx context.Context, store VersionReaderInterface, key string, cb func(core.VersionObj) error) (count int, err error) {
	err = pager.EachVersion(ctx, store, key, func(versionObj core.VersionObj) (bool, error) {
		count++
		if cbErr := cb(versionObj); cbErr != nil {
			return false, cbErr
		}
		return count >= pager.MaxVersions, nil
	})
	return count, err
}
