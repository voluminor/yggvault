package sqliteindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

// BlobRefObj stores blob refcount and size for reachability and logical usage accounting.
type BlobRefObj struct {
	Refcount  uint64
	SizeBytes uint64
}

// BlobRefRowObj is one blob_refs row for keyset paging.
type BlobRefRowObj struct {
	Hash core.HashObj
	Ref  BlobRefObj
}

type blobRefRowObj struct {
	hashObj core.HashObj
	refObj  BlobRefObj
}

// //

// CountBlobRefs aggregates tree entries into blob refcount and size.
// A size mismatch for the same hash means the tree is internally inconsistent.
func CountBlobRefs(entriesArr []core.TreeEntryObj) (map[core.HashObj]BlobRefObj, error) {
	resultObj := make(map[core.HashObj]BlobRefObj, len(entriesArr))
	for _, entryObj := range entriesArr {
		blobObj := resultObj[entryObj.BlobHash]
		if blobObj.Refcount == 0 {
			blobObj.SizeBytes = entryObj.SizeBytes
		} else if blobObj.SizeBytes != entryObj.SizeBytes {
			return nil, fmt.Errorf("blob %s has inconsistent entry sizes", entryObj.BlobHash.Hex())
		}
		blobObj.Refcount++
		resultObj[entryObj.BlobHash] = blobObj
	}
	return resultObj, nil
}

func blobRefRows(refsObj map[core.HashObj]BlobRefObj) []blobRefRowObj {
	rowArr := make([]blobRefRowObj, 0, len(refsObj))
	for hashObj, refObj := range refsObj {
		rowArr = append(rowArr, blobRefRowObj{hashObj: hashObj, refObj: refObj})
	}
	return rowArr
}

func blobRefDeltaPrefix(rowArr []blobRefRowObj) (string, []any) {
	argArr := make([]any, 0, len(rowArr)*2)
	builderObj := strings.Builder{}
	builderObj.WriteString("WITH delta(blob_hash, refcount) AS (VALUES ")
	for i, rowObj := range rowArr {
		if i > 0 {
			builderObj.WriteString(",")
		}
		builderObj.WriteString("(?,?)")
		argArr = append(argArr, rowObj.hashObj.BytesCopy(), rowObj.refObj.Refcount)
	}
	builderObj.WriteString(")")
	return builderObj.String(), argArr
}

func blobHashIn(hashArr []core.HashObj) sq.Eq {
	valuesArr := make([][]byte, 0, len(hashArr))
	for _, hashObj := range hashArr {
		valuesArr = append(valuesArr, hashObj.BytesCopy())
	}
	return sq.Eq{"blob_hash": valuesArr}
}

func historyCounterName(historyPrefix string) string {
	return "history_next_version_" + historyPrefix
}

// //

// InsertDetection upserts version detection results.
func (obj *TxObj) InsertDetection(ctx context.Context, key string, version string, detectionObj core.DetectionObj) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("detection").
		Columns("key", "version", "is_go", "is_composer", "conflict", "evidence_json", "go_zip_blocked", "go_zip_block_reason").
		Values(
			key,
			version,
			boolInt(detectionObj.IsGo),
			boolInt(detectionObj.IsComposer),
			boolInt(detectionObj.Conflict),
			detectionObj.EvidenceJSON,
			boolInt(detectionObj.GoZipBlocked),
			detectionObj.GoZipBlockReason,
		).
		Suffix("ON CONFLICT(key, version) DO UPDATE SET is_go=excluded.is_go, is_composer=excluded.is_composer, conflict=excluded.conflict, evidence_json=excluded.evidence_json, go_zip_blocked=excluded.go_zip_blocked, go_zip_block_reason=excluded.go_zip_block_reason"))
	return err
}

// InsertRewriteSet fully replaces the version rewrite set with delete plus batched insert.
// The set contains blobs whose paths need rewriting during materialization.
func (obj *TxObj) InsertRewriteSet(ctx context.Context, key string, version string, hashArr []core.HashObj) error {
	if _, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Delete("rewrite_set").Where(sq.Eq{"key": key, "version": version})); err != nil {
		return err
	}
	chunkSize := sqlChunkSize(3)
	for startValue := 0; startValue < len(hashArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(hashArr))
		insertObj := obj.indexObj.builderObj.Insert("rewrite_set").
			Columns("key", "version", "blob_hash").
			Suffix("ON CONFLICT(key, version, blob_hash) DO NOTHING")
		for _, hashObj := range hashArr[startValue:endValue] {
			insertObj = insertObj.Values(key, version, hashObj.BytesCopy())
		}
		if _, err := execSQL(ctx, obj.indexObj, obj.txObj, insertObj); err != nil {
			return err
		}
	}
	return nil
}

// GetDetection returns version detection; missing rows return false, nil.
func (obj *Obj) GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.
		Select("is_go", "is_composer", "conflict", "evidence_json", "go_zip_blocked", "go_zip_block_reason").
		From("detection").
		Where(sq.Eq{"key": key, "version": version}))
	if err != nil {
		return core.DetectionObj{}, false, err
	}
	var isGoValue, isComposerValue, conflictValue, goZipBlockedValue int
	var evidenceText, goZipBlockReasonText string
	err = rowObj.Scan(&isGoValue, &isComposerValue, &conflictValue, &evidenceText, &goZipBlockedValue, &goZipBlockReasonText)
	if errors.Is(err, sql.ErrNoRows) {
		return core.DetectionObj{}, false, nil
	}
	if err != nil {
		return core.DetectionObj{}, false, err
	}
	return core.DetectionObj{
		IsGo:             isGoValue != 0,
		IsComposer:       isComposerValue != 0,
		Conflict:         conflictValue != 0,
		EvidenceJSON:     evidenceText,
		GoZipBlocked:     goZipBlockedValue != 0,
		GoZipBlockReason: goZipBlockReasonText,
	}, true, nil
}

// RewriteSet returns version rewrite-set blob hashes sorted by blob_hash.
func (obj *Obj) RewriteSet(ctx context.Context, key string, version string) ([]core.HashObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, obj.builderObj.
		Select("blob_hash").
		From("rewrite_set").
		Where(sq.Eq{"key": key, "version": version}).
		OrderBy("blob_hash ASC"))
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr := make([]core.HashObj, 0)
	for rowsObj.Next() {
		var hashArr []byte
		if err = rowsObj.Scan(&hashArr); err != nil {
			return nil, err
		}
		hashObj, hashErr := core.HashFromBytes(hashArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultArr = append(resultArr, hashObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

// AddBlobRefs increments refcount and records size for blobs from tree entries through batched upsert.
func (obj *TxObj) AddBlobRefs(ctx context.Context, entriesArr []core.TreeEntryObj) error {
	refObj, err := CountBlobRefs(entriesArr)
	if err != nil {
		return err
	}
	rowArr := blobRefRows(refObj)
	chunkSize := sqlChunkSize(3)
	for startValue := 0; startValue < len(rowArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(rowArr))
		insertObj := obj.indexObj.builderObj.Insert("blob_refs").
			Columns("blob_hash", "refcount", "size_bytes").
			Suffix("ON CONFLICT(blob_hash) DO UPDATE SET refcount=refcount+excluded.refcount, size_bytes=excluded.size_bytes")
		for _, rowObj := range rowArr[startValue:endValue] {
			insertObj = insertObj.Values(rowObj.hashObj.BytesCopy(), rowObj.refObj.Refcount, rowObj.refObj.SizeBytes)
		}
		if _, err := execSQL(ctx, obj.indexObj, obj.txObj, insertObj); err != nil {
			return err
		}
	}
	return nil
}

// SubtractBlobRefs decrements blob refcount from tree entries through one CTE UPDATE.
// Underflow is an error protecting accounting from desync; refcount=0 rows remain for later GC.
func (obj *TxObj) SubtractBlobRefs(ctx context.Context, entriesArr []core.TreeEntryObj) error {
	refObj, err := CountBlobRefs(entriesArr)
	if err != nil {
		return err
	}
	rowArr := blobRefRows(refObj)
	chunkSize := sqlChunkSize(2)
	for startValue := 0; startValue < len(rowArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(rowArr))
		prefixText, argArr := blobRefDeltaPrefix(rowArr[startValue:endValue])
		resultObj, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Update("blob_refs").
			Prefix(prefixText, argArr...).
			Set("refcount", sq.Expr("refcount - (SELECT refcount FROM delta WHERE delta.blob_hash = blob_refs.blob_hash)")).
			Where("blob_hash IN (SELECT blob_hash FROM delta)").
			Where(sq.Expr("refcount >= (SELECT refcount FROM delta WHERE delta.blob_hash = blob_refs.blob_hash)")))
		if err != nil {
			return err
		}
		countValue, err := resultObj.RowsAffected()
		if err != nil {
			return err
		}
		if countValue != int64(endValue-startValue) {
			return errors.New("blob refcount underflow")
		}
	}
	return nil
}

// ReplaceBlobRefs fully rebuilds blob_refs from the supplied set.
func (obj *TxObj) ReplaceBlobRefs(ctx context.Context, refsObj map[core.HashObj]BlobRefObj) error {
	if _, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Delete("blob_refs")); err != nil {
		return err
	}
	rowArr := blobRefRows(refsObj)
	chunkSize := sqlChunkSize(3)
	for startValue := 0; startValue < len(rowArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(rowArr))
		insertObj := obj.indexObj.builderObj.Insert("blob_refs").
			Columns("blob_hash", "refcount", "size_bytes")
		for _, rowObj := range rowArr[startValue:endValue] {
			insertObj = insertObj.Values(rowObj.hashObj.BytesCopy(), rowObj.refObj.Refcount, rowObj.refObj.SizeBytes)
		}
		if _, err := execSQL(ctx, obj.indexObj, obj.txObj, insertObj); err != nil {
			return err
		}
	}
	return nil
}

// BlobRefs loads the full blob_refs table into a map for rebuild and integrity paths; paged callers use *Page.
func (obj *Obj) BlobRefs(ctx context.Context) (map[core.HashObj]BlobRefObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, obj.builderObj.
		Select("blob_hash", "refcount", "size_bytes").
		From("blob_refs"))
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultObj := make(map[core.HashObj]BlobRefObj)
	for rowsObj.Next() {
		var hashArr []byte
		var refObj BlobRefObj
		if err = rowsObj.Scan(&hashArr, &refObj.Refcount, &refObj.SizeBytes); err != nil {
			return nil, err
		}
		hashObj, hashErr := core.HashFromBytes(hashArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultObj[hashObj] = refObj
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultObj, nil
}

// BlobRefHashesPage returns a keyset page of live blob hashes for GC reachable-set membership.
func (obj *Obj) BlobRefHashesPage(ctx context.Context, afterHash []byte, limit int) ([]core.HashObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := obj.builderObj.Select("blob_hash").From("blob_refs").Where(sq.Gt{"refcount": 0})
	if len(afterHash) > 0 {
		selectObj = selectObj.Where(sq.Gt{"blob_hash": afterHash})
	}
	selectObj = selectObj.OrderBy("blob_hash ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr := make([]core.HashObj, 0, limit)
	for rowsObj.Next() {
		var hashArr []byte
		if err = rowsObj.Scan(&hashArr); err != nil {
			return nil, err
		}
		hashObj, hashErr := core.HashFromBytes(hashArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultArr = append(resultArr, hashObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

// BlobRefsPage returns a keyset page of blob_hash, refcount, and size_bytes.
// It has no refcount filter so integrity checks also see refcount=0 rows.
func (obj *Obj) BlobRefsPage(ctx context.Context, afterHash []byte, limit int) ([]BlobRefRowObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := obj.builderObj.Select("blob_hash", "refcount", "size_bytes").From("blob_refs")
	if len(afterHash) > 0 {
		selectObj = selectObj.Where(sq.Gt{"blob_hash": afterHash})
	}
	selectObj = selectObj.OrderBy("blob_hash ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr := make([]BlobRefRowObj, 0, limit)
	for rowsObj.Next() {
		var hashArr []byte
		var refObj BlobRefObj
		if err = rowsObj.Scan(&hashArr, &refObj.Refcount, &refObj.SizeBytes); err != nil {
			return nil, err
		}
		hashObj, hashErr := core.HashFromBytes(hashArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultArr = append(resultArr, BlobRefRowObj{Hash: hashObj, Ref: refObj})
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

// ReferencedBytes returns logical durable content size from live blob references.
// It is deterministic and drops immediately on delete, unlike physical DiskSpaceUsage with LSM lag.
func (obj *Obj) ReferencedBytes(ctx context.Context) (uint64, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.
		Select("COALESCE(SUM(size_bytes), 0)").
		From("blob_refs").
		Where(sq.Gt{"refcount": 0}))
	if err != nil {
		return 0, err
	}
	var totalBytes uint64
	if err = rowObj.Scan(&totalBytes); err != nil {
		return 0, err
	}
	return totalBytes, nil
}

// BlobReferenced reports whether a blob has live references.
func (obj *Obj) BlobReferenced(ctx context.Context, hashObj core.HashObj) (bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.Select("1").
		From("blob_refs").
		Where(sq.Eq{"blob_hash": hashObj.BytesCopy()}).
		Where(sq.Gt{"refcount": 0}).
		Limit(1))
	if err != nil {
		return false, err
	}
	var markerValue int
	err = rowObj.Scan(&markerValue)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

// UnreferencedBlobs returns the subset of hashArr with refcount=0 rows as deletion candidates.
// Queries are batched by sqlMaxBindArgs; hashes without blob_refs rows are handled by another path.
func (obj *Obj) UnreferencedBlobs(ctx context.Context, hashArr []core.HashObj) (map[core.HashObj]struct{}, error) {
	resultObj := make(map[core.HashObj]struct{})
	chunkSize := sqlChunkSize(1)
	for startValue := 0; startValue < len(hashArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(hashArr))
		selectObj := obj.builderObj.Select("blob_hash").
			From("blob_refs").
			Where(blobHashIn(hashArr[startValue:endValue])).
			Where(sq.Eq{"refcount": 0})
		rowsObj, err := querySQL(ctx, obj, nil, selectObj)
		if err != nil {
			return nil, err
		}
		for rowsObj.Next() {
			var hashBytesArr []byte
			if err = rowsObj.Scan(&hashBytesArr); err != nil {
				_ = rowsObj.Close()
				return nil, err
			}
			hashObj, hashErr := core.HashFromBytes(hashBytesArr)
			if hashErr != nil {
				_ = rowsObj.Close()
				return nil, hashErr
			}
			resultObj[hashObj] = struct{}{}
		}
		if err = rowsObj.Err(); err != nil {
			_ = rowsObj.Close()
			return nil, err
		}
		if err = rowsObj.Close(); err != nil {
			return nil, err
		}
	}
	return resultObj, nil
}

// DeleteUnreferencedBlobRefs deletes refcount=0 blob_refs rows for specified hashes in batches.
// It records accounting deletion after physical blob GC.
func (obj *Obj) DeleteUnreferencedBlobRefs(ctx context.Context, hashArr []core.HashObj) error {
	chunkSize := sqlChunkSize(1)
	for startValue := 0; startValue < len(hashArr); startValue += chunkSize {
		endValue := min(startValue+chunkSize, len(hashArr))
		_, err := execSQL(ctx, obj, nil, obj.builderObj.Delete("blob_refs").
			Where(blobHashIn(hashArr[startValue:endValue])).
			Where(sq.Eq{"refcount": 0}))
		if err != nil {
			return err
		}
	}
	return nil
}

// TreeReferenced reports whether any version references a tree hash for GC reachability.
func (obj *Obj) TreeReferenced(ctx context.Context, hashObj core.HashObj) (bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.Select("1").
		From("versions").
		Where(sq.Eq{"tree_hash": hashObj.BytesCopy()}).
		Limit(1))
	if err != nil {
		return false, err
	}
	var markerValue int
	err = rowObj.Scan(&markerValue)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, nil
}

func (obj *TxObj) maxHistoricalVersion(ctx context.Context, versionPrefix string) (int, error) {
	rowsObj, err := querySQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.
		Select("version").
		From("versions").
		Where(sq.Like{"version": versionPrefix + "%"}))
	if err != nil {
		return 0, err
	}
	defer rowsObj.Close()

	maxValue := 0
	for rowsObj.Next() {
		var version string
		if err = rowsObj.Scan(&version); err != nil {
			return 0, err
		}
		valueText := strings.TrimPrefix(version, versionPrefix)
		value, parseErr := strconv.Atoi(valueText)
		if parseErr == nil && value > maxValue {
			maxValue = value
		}
	}
	if err = rowsObj.Err(); err != nil {
		return 0, err
	}
	return maxValue, nil
}

// NextHistoricalVersion returns the next monotonic historical tag and increments the globals counter.
// Missing counters are initialized from the maximum existing tag for the prefix.
func (obj *TxObj) NextHistoricalVersion(ctx context.Context, historyPrefix string) (string, error) {
	versionPrefix := "v0.0.0-" + historyPrefix
	counterName := historyCounterName(historyPrefix)
	rowObj, err := queryRowSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.
		Select("value").
		From("globals").
		Where(sq.Eq{"name": counterName}))
	if err != nil {
		return "", err
	}

	nextValue := 0
	var valueText string
	err = rowObj.Scan(&valueText)
	if errors.Is(err, sql.ErrNoRows) {
		maxValue, maxErr := obj.maxHistoricalVersion(ctx, versionPrefix)
		if maxErr != nil {
			return "", maxErr
		}
		nextValue = maxValue + 1
	} else if err != nil {
		return "", err
	} else {
		nextValue, err = strconv.Atoi(valueText)
		if err != nil || nextValue < 1 {
			return "", errors.New("invalid historical version counter")
		}
	}

	_, err = execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("globals").
		Columns("name", "value").
		Values(counterName, strconv.Itoa(nextValue+1)).
		Suffix("ON CONFLICT(name) DO UPDATE SET value=excluded.value"))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("%s%d", versionPrefix, nextValue), nil
}

// AddHistory appends a feed event; zero tree or body hashes are stored as SQL NULL.
func (obj *TxObj) AddHistory(ctx context.Context, key string, version string, eventType string, treeHashObj core.HashObj, bodyHashObj core.HashObj, message string) error {
	var treeValue any
	if !treeHashObj.IsZero() {
		treeValue = treeHashObj.BytesCopy()
	}
	var bodyValue any
	if !bodyHashObj.IsZero() {
		bodyValue = bodyHashObj.BytesCopy()
	}
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("history_events").
		Columns("event_ts", "key", "version", "event_type", "tree_hash", "body_hash", "message").
		Values(core.FormatTime(time.Now()), key, version, eventType, treeValue, bodyValue, message))
	return err
}

// CopyVersionMetadata clones a version under a historical tag with detection, rewrite_set, and artifact metadata.
// file_path is cleared so hot files are rebuilt under the new tag.
func (obj *TxObj) CopyVersionMetadata(ctx context.Context, oldObj core.VersionObj, historicalVersion string) error {
	oldObj.ReplacedBy = oldObj.Version
	oldObj.Version = historicalVersion
	oldObj.UpstreamSeq = 0
	if err := obj.InsertVersion(ctx, oldObj); err != nil {
		return err
	}

	detectionSelectObj := obj.indexObj.builderObj.
		Select("key").
		Column(sq.Expr("?", historicalVersion)).
		Columns("is_go", "is_composer", "conflict", "evidence_json", "go_zip_blocked", "go_zip_block_reason").
		From("detection").
		Where(sq.Eq{"key": oldObj.Key, "version": oldObj.ReplacedBy})
	if _, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("detection").
		Columns("key", "version", "is_go", "is_composer", "conflict", "evidence_json", "go_zip_blocked", "go_zip_block_reason").
		Select(detectionSelectObj)); err != nil {
		return err
	}

	rewriteSelectObj := obj.indexObj.builderObj.
		Select("key").
		Column(sq.Expr("?", historicalVersion)).
		Column("blob_hash").
		From("rewrite_set").
		Where(sq.Eq{"key": oldObj.Key, "version": oldObj.ReplacedBy})
	if _, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("rewrite_set").
		Columns("key", "version", "blob_hash").
		Select(rewriteSelectObj)); err != nil {
		return err
	}

	artifactSelectObj := obj.indexObj.builderObj.
		Select("materializer_id", "artifact_kind", "listener_id", "key").
		Column(sq.Expr("?", historicalVersion)).
		Columns("etag", "body_hash", "body_sha256", "body_sha1", "size_bytes", "format_version").
		Column(sq.Expr("NULL")).
		Columns("degraded_reason").
		From("materialized_artifacts").
		Where(sq.Eq{"key": oldObj.Key, "version": oldObj.ReplacedBy})
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("materialized_artifacts").
		Columns("materializer_id", "artifact_kind", "listener_id", "key", "version", "etag", "body_hash", "body_sha256", "body_sha1", "size_bytes", "format_version", "file_path", "degraded_reason").
		Select(artifactSelectObj))
	return err
}
