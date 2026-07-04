package sqliteindex

import (
	"context"
	"database/sql"
	"errors"

	sq "github.com/Masterminds/squirrel"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// //

func digestArg(digestArr []byte) any {
	if len(digestArr) == 0 {
		return nil
	}
	return digestArr
}

func artifactSelect(builderObj sq.StatementBuilderType) sq.SelectBuilder {
	return builderObj.Select(
		"materializer_id",
		"artifact_kind",
		"listener_id",
		"key",
		"version",
		"etag",
		"body_hash",
		"body_sha256",
		"body_sha1",
		"size_bytes",
		"format_version",
		"file_path",
		"degraded_reason",
	).From("materialized_artifacts")
}

func scanArtifact(scannerObj rowScannerInterface) (core.ArtifactObj, error) {
	var artifactObj core.ArtifactObj
	var bodyArr []byte
	var sha256Arr []byte
	var sha1Arr []byte
	var filePathObj sql.NullString
	if err := scannerObj.Scan(
		&artifactObj.MaterializerID,
		&artifactObj.ArtifactKind,
		&artifactObj.ListenerID,
		&artifactObj.Key,
		&artifactObj.Version,
		&artifactObj.ETag,
		&bodyArr,
		&sha256Arr,
		&sha1Arr,
		&artifactObj.SizeBytes,
		&artifactObj.FormatVersion,
		&filePathObj,
		&artifactObj.DegradedReason,
	); err != nil {
		return artifactObj, err
	}
	bodyHashObj, err := core.HashFromBytes(bodyArr)
	if err != nil {
		return artifactObj, err
	}
	artifactObj.BodyHash = bodyHashObj
	artifactObj.BodySha256 = sha256Arr
	artifactObj.BodySha1 = sha1Arr
	if filePathObj.Valid {
		artifactObj.FilePath = filePathObj.String
	}
	return artifactObj, nil
}

// //

// GetArtifact returns a materialized artifact by full identity key; missing rows return false, nil.
func (obj *Obj) GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, artifactSelect(obj.builderObj).Where(sq.Eq{
		"materializer_id": keyObj.MaterializerID,
		"artifact_kind":   keyObj.ArtifactKind,
		"listener_id":     keyObj.ListenerID,
		"key":             keyObj.Key,
		"version":         keyObj.Version,
	}))
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	artifactObj, err := scanArtifact(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ArtifactObj{}, false, nil
	}
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	return artifactObj, true, nil
}

// ListArtifacts returns all materialized artifacts for a version for release_detail Artifacts.
// Order is not guaranteed; consumers sort when needed.
func (obj *Obj) ListArtifacts(ctx context.Context, key string, version string) ([]core.ArtifactObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, artifactSelect(obj.builderObj).Where(sq.Eq{
		"key":     key,
		"version": version,
	}))
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	artifactArr := make([]core.ArtifactObj, 0)
	for rowsObj.Next() {
		artifactObj, scanErr := scanArtifact(rowsObj)
		if scanErr != nil {
			return nil, scanErr
		}
		artifactArr = append(artifactArr, artifactObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return artifactArr, nil
}

// InsertArtifacts upserts version artifact metadata in batches within sqlMaxBindArgs.
// Identity conflicts update all mutable fields.
func (obj *TxObj) InsertArtifacts(ctx context.Context, key string, version string, artifactArr []core.ArtifactObj) error {
	chunkSize := sqlChunkSize(13)
	for startValue := 0; startValue < len(artifactArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(artifactArr))
		insertObj := obj.indexObj.builderObj.Insert("materialized_artifacts").
			Columns(
				"materializer_id",
				"artifact_kind",
				"listener_id",
				"key",
				"version",
				"etag",
				"body_hash",
				"body_sha256",
				"body_sha1",
				"size_bytes",
				"format_version",
				"file_path",
				"degraded_reason",
			).
			Suffix("ON CONFLICT(materializer_id, artifact_kind, listener_id, key, version) DO UPDATE SET etag=excluded.etag, body_hash=excluded.body_hash, body_sha256=excluded.body_sha256, body_sha1=excluded.body_sha1, size_bytes=excluded.size_bytes, format_version=excluded.format_version, file_path=excluded.file_path, degraded_reason=excluded.degraded_reason")
		for i := startValue; i < endValue; i++ {
			artifactObj := artifactArr[i]
			insertObj = insertObj.Values(
				artifactObj.MaterializerID,
				artifactObj.ArtifactKind,
				artifactObj.ListenerID,
				key,
				version,
				artifactObj.ETag,
				artifactObj.BodyHash.BytesCopy(),
				digestArg(artifactObj.BodySha256),
				digestArg(artifactObj.BodySha1),
				artifactObj.SizeBytes,
				artifactObj.FormatVersion,
				sql.NullString{String: artifactObj.FilePath, Valid: artifactObj.FilePath != ""},
				artifactObj.DegradedReason,
			)
		}
		if _, err := execSQL(ctx, obj.indexObj, obj.txObj, insertObj); err != nil {
			return err
		}
	}
	return nil
}

// SmallestArtifactByDescriptor returns the smallest artifact for a materializer, kind, and format version.
// Startup self-test uses one cheap artifact per codec; kind is required because zip and tar.gz share materializer and format.
// Missing rows return false, nil.
func (obj *Obj) SmallestArtifactByDescriptor(ctx context.Context, materializerID string, artifactKind string, formatVersion uint32) (core.ArtifactObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, artifactSelect(obj.builderObj).
		Where(sq.Eq{"materializer_id": materializerID, "artifact_kind": artifactKind, "format_version": formatVersion}).
		OrderBy("size_bytes ASC").
		Limit(1))
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	artifactObj, err := scanArtifact(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.ArtifactObj{}, false, nil
	}
	if err != nil {
		return core.ArtifactObj{}, false, err
	}
	return artifactObj, true, nil
}

// UpdateArtifactDigest updates digest metadata by identity primary key and clears the degraded state.
// file_path is cleared because the old hot file is named by the old body_hash and must be rebuilt.
// This is UPDATE, not upsert: missing rows are not resurrected.
func (obj *Obj) UpdateArtifactDigest(ctx context.Context, artifactObj core.ArtifactObj) error {
	resultObj, err := execSQL(ctx, obj, nil, obj.builderObj.Update("materialized_artifacts").
		Set("etag", artifactObj.ETag).
		Set("body_hash", artifactObj.BodyHash.BytesCopy()).
		Set("body_sha256", digestArg(artifactObj.BodySha256)).
		Set("body_sha1", digestArg(artifactObj.BodySha1)).
		Set("size_bytes", artifactObj.SizeBytes).
		Set("format_version", artifactObj.FormatVersion).
		Set("file_path", nil).
		Set("degraded_reason", "").
		Where(sq.Eq{
			"materializer_id": artifactObj.MaterializerID,
			"artifact_kind":   artifactObj.ArtifactKind,
			"listener_id":     artifactObj.ListenerID,
			"key":             artifactObj.Key,
			"version":         artifactObj.Version,
		}))
	if err != nil {
		return err
	}
	affectedCount, err := resultObj.RowsAffected()
	if err != nil {
		return err
	}
	if affectedCount == 0 {
		return errors.New("artifact metadata not found")
	}
	return nil
}

// UpdateArtifactPath attaches a hot file to an artifact and updates sha digests.
// WHERE also checks body_hash, size, and format_version so stale identities are not attached.
func (obj *Obj) UpdateArtifactPath(ctx context.Context, artifactObj core.ArtifactObj, filePath string) error {
	var filePathValue any
	if filePath != "" {
		filePathValue = filePath
	}
	resultObj, err := execSQL(ctx, obj, nil, obj.builderObj.Update("materialized_artifacts").
		Set("file_path", filePathValue).
		Set("body_sha256", digestArg(artifactObj.BodySha256)).
		Set("body_sha1", digestArg(artifactObj.BodySha1)).
		Where(sq.Eq{
			"materializer_id": artifactObj.MaterializerID,
			"artifact_kind":   artifactObj.ArtifactKind,
			"listener_id":     artifactObj.ListenerID,
			"key":             artifactObj.Key,
			"version":         artifactObj.Version,
			"body_hash":       artifactObj.BodyHash.BytesCopy(),
			"size_bytes":      artifactObj.SizeBytes,
			"format_version":  artifactObj.FormatVersion,
		}))
	if err != nil {
		return err
	}
	affectedCount, err := resultObj.RowsAffected()
	if err != nil {
		return err
	}
	if affectedCount == 0 {
		return errors.New("artifact metadata not found")
	}
	return nil
}

// ArtifactPaths returns non-empty hot-file paths for a version for transactional prune or replace.
func (obj *TxObj) ArtifactPaths(ctx context.Context, key string, version string) ([]string, error) {
	rowsObj, err := querySQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.
		Select("file_path").
		From("materialized_artifacts").
		Where(sq.Eq{"key": key, "version": version}).
		Where(sq.NotEq{"file_path": nil}))
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	pathArr := make([]string, 0)
	for rowsObj.Next() {
		var pathText string
		if err = rowsObj.Scan(&pathText); err != nil {
			return nil, err
		}
		pathArr = append(pathArr, pathText)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return pathArr, nil
}
