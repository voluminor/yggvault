package storage

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
)

// // // // // // // // // //

const cMaxIngestFailuresPerKey = 4096

// ErrIngestFailureCap means the key's durable quarantine is full.
var ErrIngestFailureCap = errors.New("ingest failure quarantine is full")

// //

func validateIngestFailureRef(refText string) error {
	if refText == "" {
		return nil
	}
	switch len(refText) {
	case 40, core.HashSize * 2, 64:
	default:
		return fmt.Errorf("ingest failure ref %q must be a 40-, 48- or 64-char hex string: %w", refText, ErrInvalidRef)
	}
	for i := 0; i < len(refText); i++ {
		symbolByte := refText[i]
		if (symbolByte < '0' || symbolByte > '9') && (symbolByte < 'a' || symbolByte > 'f') {
			return fmt.Errorf("ingest failure ref %q must be lowercase hex: %w", refText, ErrInvalidRef)
		}
	}
	return nil
}

func normalizeIngestFailure(failureObj core.IngestFailureObj) (core.IngestFailureObj, error) {
	if err := validateKey(failureObj.Key); err != nil {
		return failureObj, err
	}
	if err := validateVersion(failureObj.Version); err != nil {
		return failureObj, err
	}
	if err := validateIngestFailureRef(failureObj.RefSHA); err != nil {
		return failureObj, err
	}
	failureObj.Code = strings.TrimSpace(failureObj.Code)
	if failureObj.Code == "" {
		return failureObj, errors.New("ingest failure code is empty")
	}
	if err := validateSmallID("ingest failure code", failureObj.Code); err != nil {
		return failureObj, err
	}
	if err := validateMaxBytes("ingest failure message", failureObj.Message, cMaxEventMessageBytes); err != nil {
		return failureObj, err
	}
	if failureObj.Policy == 0 {
		failureObj.Policy = core.IngestFailurePolicy
	}
	nowObj := time.Now().UTC()
	if failureObj.FirstTS.IsZero() {
		failureObj.FirstTS = nowObj
	}
	if failureObj.LastTS.IsZero() {
		failureObj.LastTS = nowObj
	}
	if failureObj.Count == 0 {
		failureObj.Count = 1
	}
	return failureObj, nil
}

// // // // // // // // // //

// ListIngestFailures returns the key's durable quarantine of deterministic failures.
func (obj *Obj) ListIngestFailures(ctx context.Context, key string) ([]core.IngestFailureObj, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return nil, err
	}
	return obj.indexObj.ListIngestFailures(ctx, key)
}

// ListIngestFailureKeys returns the distinct keys that still have durable quarantine rows.
func (obj *Obj) ListIngestFailureKeys(ctx context.Context) ([]string, error) {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return nil, err
	}
	defer releaseFunc()

	return obj.indexObj.ListIngestFailureKeys(ctx)
}

// PutIngestFailure records one deterministic failure under the shared storage write lock.
func (obj *Obj) PutIngestFailure(ctx context.Context, failureObj core.IngestFailureObj) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	failureObj, err = normalizeIngestFailure(failureObj)
	if err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	if _, ok, getErr := obj.indexObj.GetIngestFailure(ctx, failureObj.Key, failureObj.Version); getErr != nil {
		return getErr
	} else if !ok {
		countValue, countErr := obj.indexObj.CountIngestFailures(ctx, failureObj.Key)
		if countErr != nil {
			return countErr
		}
		if countValue >= cMaxIngestFailuresPerKey {
			return ErrIngestFailureCap
		}
	}
	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.UpsertIngestFailure(ctx, failureObj)
	})
}

// DeleteIngestFailure removes one durable quarantine row.
func (obj *Obj) DeleteIngestFailure(ctx context.Context, key string, version string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}
	if err := validateVersion(version); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.DeleteIngestFailure(ctx, key, version)
	})
}

// DeleteKeyIngestFailures removes the key's entire durable quarantine.
func (obj *Obj) DeleteKeyIngestFailures(ctx context.Context, key string) error {
	releaseFunc, err := beginOperation(obj)
	if err != nil {
		return err
	}
	defer releaseFunc()

	if err := validateKey(key); err != nil {
		return err
	}

	obj.writeMu.Lock()
	defer obj.writeMu.Unlock()

	return obj.indexObj.WithTx(ctx, func(txObj *sqliteindex.TxObj) error {
		return txObj.DeleteKeyIngestFailures(ctx, key)
	})
}
