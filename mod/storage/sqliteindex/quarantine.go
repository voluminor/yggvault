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

func ingestFailureSelect(builderObj sq.StatementBuilderType) sq.SelectBuilder {
	return builderObj.Select(
		"ingest_failures.key",
		"ingest_failures.version",
		"ingest_failures.ref_sha",
		"ingest_failures.code",
		"ingest_failures.message",
		"ingest_failures.policy",
		"ingest_failures.first_ts",
		"ingest_failures.last_ts",
		"ingest_failures.count",
	).From("ingest_failures")
}

func scanIngestFailure(scannerObj rowScannerInterface) (core.IngestFailureObj, error) {
	var rowObj core.IngestFailureObj
	var firstText string
	var lastText string
	if err := scannerObj.Scan(
		&rowObj.Key,
		&rowObj.Version,
		&rowObj.RefSHA,
		&rowObj.Code,
		&rowObj.Message,
		&rowObj.Policy,
		&firstText,
		&lastText,
		&rowObj.Count,
	); err != nil {
		return rowObj, err
	}
	firstTS, err := core.ParseTime(firstText)
	if err != nil {
		return rowObj, fmt.Errorf("invalid ingest_failures first time in database: %w", err)
	}
	lastTS, err := core.ParseTime(lastText)
	if err != nil {
		return rowObj, fmt.Errorf("invalid ingest_failures last time in database: %w", err)
	}
	rowObj.FirstTS = firstTS
	rowObj.LastTS = lastTS
	return rowObj, nil
}

// // // // // // // // // //

// GetIngestFailure returns one durable deterministic-failure row.
func (obj *Obj) GetIngestFailure(ctx context.Context, key string, version string) (core.IngestFailureObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, ingestFailureSelect(obj.builderObj).Where(sq.Eq{"key": key, "version": version}))
	if err != nil {
		return core.IngestFailureObj{}, false, err
	}
	failureObj, err := scanIngestFailure(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.IngestFailureObj{}, false, nil
	}
	if err != nil {
		return core.IngestFailureObj{}, false, err
	}
	return failureObj, true, nil
}

// ListIngestFailures returns all durable failures of one key.
func (obj *Obj) ListIngestFailures(ctx context.Context, key string) ([]core.IngestFailureObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, ingestFailureSelect(obj.builderObj).
		Where(sq.Eq{"key": key}).
		OrderBy("version ASC"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowsObj.Close() }()

	var outArr []core.IngestFailureObj
	for rowsObj.Next() {
		failureObj, scanErr := scanIngestFailure(rowsObj)
		if scanErr != nil {
			return nil, scanErr
		}
		outArr = append(outArr, failureObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return outArr, nil
}

// CountIngestFailures returns the key's quarantine size.
func (obj *Obj) CountIngestFailures(ctx context.Context, key string) (uint64, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.
		Select("COUNT(*)").
		From("ingest_failures").
		Where(sq.Eq{"key": key}))
	if err != nil {
		return 0, err
	}
	var countValue uint64
	if err = rowObj.Scan(&countValue); err != nil {
		return 0, err
	}
	return countValue, nil
}

// ListIngestFailureKeys returns the distinct keys present in the durable quarantine.
func (obj *Obj) ListIngestFailureKeys(ctx context.Context) ([]string, error) {
	rowsObj, err := querySQL(ctx, obj, nil, obj.builderObj.
		Select("DISTINCT key").
		From("ingest_failures").
		OrderBy("key ASC"))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rowsObj.Close() }()

	var outArr []string
	for rowsObj.Next() {
		var keyText string
		if scanErr := rowsObj.Scan(&keyText); scanErr != nil {
			return nil, scanErr
		}
		outArr = append(outArr, keyText)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return outArr, nil
}

// UpsertIngestFailure creates or refreshes one failure row.
func (obj *TxObj) UpsertIngestFailure(ctx context.Context, failureObj core.IngestFailureObj) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("ingest_failures").
		Columns("key", "version", "ref_sha", "code", "message", "policy", "first_ts", "last_ts", "count").
		Values(
			failureObj.Key,
			failureObj.Version,
			failureObj.RefSHA,
			failureObj.Code,
			failureObj.Message,
			failureObj.Policy,
			core.FormatTime(failureObj.FirstTS),
			core.FormatTime(failureObj.LastTS),
			failureObj.Count,
		).
		Suffix("ON CONFLICT(key, version) DO UPDATE SET ref_sha=excluded.ref_sha, code=excluded.code, message=excluded.message, policy=excluded.policy, last_ts=excluded.last_ts, count=ingest_failures.count + 1"))
	return err
}

// DeleteIngestFailure removes one durable failure row.
func (obj *TxObj) DeleteIngestFailure(ctx context.Context, key string, version string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.
		Delete("ingest_failures").
		Where(sq.Eq{"key": key, "version": version}))
	return err
}

// DeleteKeyIngestFailures removes all durable failures of a key.
func (obj *TxObj) DeleteKeyIngestFailures(ctx context.Context, key string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.
		Delete("ingest_failures").
		Where(sq.Eq{"key": key}))
	return err
}
