package sqliteindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func keySourceSelect(builderObj sq.StatementBuilderType) sq.SelectBuilder {
	return builderObj.Select(
		"key_source.key",
		"key_source.url",
		"key_source.class",
		"key_source.web_addr",
		"key_source.ygg_addr",
		"key_source.origin_url",
		"key_source.listing_mode",
		"key_source.bound_ts",
	).From("key_source")
}

func scanKeySource(scannerObj rowScannerInterface) (core.KeySourceObj, error) {
	var rowObj core.KeySourceObj
	var boundText string
	if err := scannerObj.Scan(
		&rowObj.Key,
		&rowObj.URL,
		&rowObj.Class,
		&rowObj.WebAddr,
		&rowObj.YggAddr,
		&rowObj.OriginURL,
		&rowObj.ListingMode,
		&boundText,
	); err != nil {
		return rowObj, err
	}
	ts, err := core.ParseTime(boundText)
	if err != nil {
		return rowObj, fmt.Errorf("invalid key_source bound time in database: %w", err)
	}
	rowObj.BoundTS = ts
	return rowObj, nil
}

// // // // // // // // // //

// GetKeySource returns a binding by key name; the second result reports whether it exists.
func (obj *Obj) GetKeySource(ctx context.Context, key string) (core.KeySourceObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, keySourceSelect(obj.builderObj).Where(sq.Eq{"key": key}))
	if err != nil {
		return core.KeySourceObj{}, false, err
	}
	keySourceObj, err := scanKeySource(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.KeySourceObj{}, false, nil
	}
	if err != nil {
		return core.KeySourceObj{}, false, err
	}
	return keySourceObj, true, nil
}

// ListKeySources returns all bindings for boot name-to-URL checks.
func (obj *Obj) ListKeySources(ctx context.Context) ([]core.KeySourceObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, keySourceSelect(obj.builderObj))
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	var outArr []core.KeySourceObj
	for rowsObj.Next() {
		keySourceObj, scanErr := scanKeySource(rowsObj)
		if scanErr != nil {
			return nil, scanErr
		}
		outArr = append(outArr, keySourceObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return outArr, nil
}

// InsertKeySource upserts a binding and updates all fields on key conflict.
// listing_mode is intentionally excluded from conflict updates; only UpdateKeySourceListingMode changes it.
func (obj *TxObj) InsertKeySource(ctx context.Context, keySourceObj core.KeySourceObj) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("key_source").
		Columns("key", "url", "class", "web_addr", "ygg_addr", "origin_url", "listing_mode", "bound_ts").
		Values(
			keySourceObj.Key,
			keySourceObj.URL,
			keySourceObj.Class,
			keySourceObj.WebAddr,
			keySourceObj.YggAddr,
			keySourceObj.OriginURL,
			keySourceObj.ListingMode,
			core.FormatTime(keySourceObj.BoundTS),
		).
		Suffix("ON CONFLICT(key) DO UPDATE SET url=excluded.url, class=excluded.class, web_addr=excluded.web_addr, ygg_addr=excluded.ygg_addr, origin_url=excluded.origin_url, bound_ts=excluded.bound_ts"))
	return err
}

// UpdateKeySourceListingMode pins the git listing mode for an existing binding.
// A missing row is a no-op: the binding appears at classification time before any git listing.
func (obj *TxObj) UpdateKeySourceListingMode(ctx context.Context, key string, mode string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Update("key_source").
		Set("listing_mode", mode).
		Where(sq.Eq{"key": key}))
	return err
}
