package storage

import (
	"context"
	"fmt"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
)

// // // // // // // // // //

func keySourceUnchanged(aObj, bObj core.KeySourceObj) bool {
	return aObj.URL == bObj.URL && aObj.Class == bObj.Class && aObj.WebAddr == bObj.WebAddr &&
		aObj.YggAddr == bObj.YggAddr && aObj.OriginURL == bObj.OriginURL
}

// // // // // // // // // //

// GetKeySource returns the durable key-to-source binding; the second result reports whether it exists.
func (obj *Obj) GetKeySource(ctx context.Context, key string) (core.KeySourceObj, bool, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return core.KeySourceObj{}, false, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return core.KeySourceObj{}, false, err
	}
	return obj.indexObj.GetKeySource(ctx, key)
}

// ListKeySources returns all bindings used by boot name-to-URL checks.
func (obj *Obj) ListKeySources(ctx context.Context) ([]core.KeySourceObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	return obj.indexObj.ListKeySources(ctx)
}

// PutKeySource writes a binding under writeMu only when content changes.
// This avoids WAL churn on every rescan cycle; BoundTS is filled automatically when absent.
func (obj *Obj) PutKeySource(ctx context.Context, keySourceObj core.KeySourceObj) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(keySourceObj.Key); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	existingObj, ok, err := obj.indexObj.GetKeySource(ctx, keySourceObj.Key)
	if err != nil {
		return err
	}
	if ok && keySourceUnchanged(existingObj, keySourceObj) {
		return nil
	}
	if keySourceObj.BoundTS.IsZero() {
		keySourceObj.BoundTS = time.Now().UTC()
	}
	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.InsertKeySource(ctx, keySourceObj)
	})
}

// SetKeyListingMode pins the sticky git listing mode of a key (” resets it).
// PutKeySource never touches the mode, so rediscovery cannot silently switch a tags-mode key.
func (obj *Obj) SetKeyListingMode(ctx context.Context, key string, mode string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	switch mode {
	case core.ListingModeUndecided, core.ListingModeReleases, core.ListingModeTags:
	default:
		return fmt.Errorf("invalid listing mode %q: %w", mode, ErrInvalidRef)
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	existingObj, ok, err := obj.indexObj.GetKeySource(ctx, key)
	if err != nil {
		return err
	}
	if ok && existingObj.ListingMode == mode {
		return nil
	}
	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.UpdateKeySourceListingMode(ctx, key, mode)
	})
}
