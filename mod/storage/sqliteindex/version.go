package sqliteindex

import (
	"context"
	"database/sql"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

// //

// GetVersion returns a version by key and version; missing rows return false, nil.
func (obj *Obj) GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj, nil, versionSelect(obj.builderObj, true).
		Where(sq.Eq{"key": key, "version": version}))
	if err != nil {
		return core.VersionObj{}, false, err
	}
	versionObj, err := scanVersion(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.VersionObj{}, false, nil
	}
	if err != nil {
		return core.VersionObj{}, false, err
	}
	return versionObj, true, nil
}

// GetVersion returns a version by key and version within a transaction; missing rows return false, nil.
func (obj *TxObj) GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error) {
	rowObj, err := queryRowSQL(ctx, obj.indexObj, obj.txObj, versionSelect(obj.indexObj.builderObj, true).
		Where(sq.Eq{"key": key, "version": version}))
	if err != nil {
		return core.VersionObj{}, false, err
	}
	versionObj, err := scanVersion(rowObj)
	if errors.Is(err, sql.ErrNoRows) {
		return core.VersionObj{}, false, nil
	}
	if err != nil {
		return core.VersionObj{}, false, err
	}
	return versionObj, true, nil
}

// ListVersions returns the full service newest-first list without reading release_notes.
// Use GetVersion or page/keyset views when notes are needed; includeDeleted=false hides tombstones.
func (obj *Obj) ListVersions(ctx context.Context, key string, includeDeleted bool) ([]core.VersionObj, error) {
	selectObj := versionSelect(obj.builderObj, false).Where(sq.Eq{"key": key})
	if !includeDeleted {
		selectObj = selectObj.Where(sq.Eq{"upstream_deleted": 0})
	}
	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr, err := scanAll(rowsObj, 0, scanVersion)
	if err != nil {
		return nil, err
	}
	sortVersions(resultArr)
	return resultArr, nil
}

// CountVersionsByKey returns key version count through SQL COUNT without loading rows.
func (obj *Obj) CountVersionsByKey(ctx context.Context, key string, includeDeleted bool) (uint64, error) {
	selectObj := obj.builderObj.Select("COUNT(*)").From("versions").Where(sq.Eq{"key": key})
	if !includeDeleted {
		selectObj = selectObj.Where(sq.Eq{"upstream_deleted": 0})
	}
	query, argsArr, err := selectObj.ToSql()
	if err != nil {
		return 0, err
	}
	rowObj := obj.dbObj.QueryRowContext(ctx, query, argsArr...)
	var countValue uint64
	if err := rowObj.Scan(&countValue); err != nil {
		return 0, err
	}
	return countValue, nil
}

// ListVersionsPage returns one newest-first page through LIMIT/OFFSET without loading the full set.
func (obj *Obj) ListVersionsPage(ctx context.Context, key string, includeDeleted bool, limit int, offset int) ([]core.VersionObj, error) {
	if limit < 1 {
		limit = 1
	}
	if offset < 0 {
		offset = 0
	}
	selectObj := versionSelect(obj.builderObj, true).Where(sq.Eq{"key": key})
	if !includeDeleted {
		selectObj = selectObj.Where(sq.Eq{"upstream_deleted": 0})
	}
	selectObj = selectObj.OrderBy("upstream_seq DESC", "version DESC").Limit(uint64(limit)).Offset(uint64(offset))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	return scanAll(rowsObj, limit, scanVersion)
}

// ListVersionsKeyset returns a newest-first keyset page by afterSeq and afterVersion cursor.
// Empty afterVersion means first page; the path is O(log n + limit) through idx_versions_key_seq without OFFSET scans.
// The compound (seq, version) cursor tolerates duplicate seq values, so UNIQUE(key, upstream_seq) is unnecessary.
func (obj *Obj) ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := versionSelect(obj.builderObj, true).Where(sq.Eq{"key": key})
	if !includeDeleted {
		selectObj = selectObj.Where(sq.Eq{"upstream_deleted": 0})
	}
	if afterVersion != "" {
		selectObj = selectObj.Where("(upstream_seq, version) < (?, ?)", afterSeq, afterVersion)
	}
	selectObj = selectObj.OrderBy("upstream_seq DESC", "version DESC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	return scanAll(rowsObj, limit, scanVersion)
}

// ListVersionsKeysetBefore returns up to limit versions strictly newer than the (beforeSeq, beforeVersion) cursor,
// ordered ASCENDING (closest to the cursor first). The serve layer reverses for newest-first display and trims the
// keyset overshoot. Powers the "newer" pager direction of the key page without OFFSET.
func (obj *Obj) ListVersionsKeysetBefore(ctx context.Context, key string, includeDeleted bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := versionSelect(obj.builderObj, true).Where(sq.Eq{"key": key})
	if !includeDeleted {
		selectObj = selectObj.Where(sq.Eq{"upstream_deleted": 0})
	}
	selectObj = selectObj.Where("(upstream_seq, version) > (?, ?)", beforeSeq, beforeVersion)
	selectObj = selectObj.OrderBy("upstream_seq ASC", "version ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	return scanAll(rowsObj, limit, scanVersion)
}

// DistinctVersionKeys returns one keyset page of unique keys with key > afterKey.
// It supports prune without loading all versions for all keys into RAM under writeMu.
func (obj *Obj) DistinctVersionKeys(ctx context.Context, afterKey string, limit int) ([]string, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := obj.builderObj.Select("key").Distinct().From("versions")
	if afterKey != "" {
		selectObj = selectObj.Where(sq.Gt{"key": afterKey})
	}
	selectObj = selectObj.OrderBy("key ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	keyArr := make([]string, 0, limit)
	for rowsObj.Next() {
		var keyText string
		if scanErr := rowsObj.Scan(&keyText); scanErr != nil {
			return nil, scanErr
		}
		keyArr = append(keyArr, keyText)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return keyArr, nil
}

// VersionsByIngest walks versions by service ingest order and intentionally skips release_notes.
func (obj *Obj) VersionsByIngest(ctx context.Context, afterObj core.VersionObj, afterFlag bool, limitValue uint64) ([]core.VersionObj, error) {
	selectObj := versionSelect(obj.builderObj, false).OrderBy("versions.ingest_ts ASC", "versions.key ASC", "versions.version ASC")
	if afterFlag {
		ingestText := core.FormatTime(afterObj.IngestTS)
		selectObj = selectObj.Where(sq.Or{
			sq.Gt{"versions.ingest_ts": ingestText},
			sq.And{
				sq.Eq{"versions.ingest_ts": ingestText},
				sq.Gt{"versions.key": afterObj.Key},
			},
			sq.And{
				sq.Eq{"versions.ingest_ts": ingestText},
				sq.Eq{"versions.key": afterObj.Key},
				sq.Gt{"versions.version": afterObj.Version},
			},
		})
	}
	if limitValue > 0 {
		selectObj = selectObj.Limit(limitValue)
	}
	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	return scanAll(rowsObj, 0, scanVersion)
}

// LatestVersion returns the newest active version without release_notes; no row returns false, nil.
// Semver keys use semver max to match go-proxy @latest, while raw versions use upstream_seq order.
// For mixed keys, the later source-listing position wins between semver max and raw top.
func (obj *Obj) LatestVersion(ctx context.Context, key string) (core.VersionObj, bool, error) {
	rowsObj, err := querySQL(ctx, obj, nil, versionSelect(obj.builderObj, false).
		Where(sq.Eq{"key": key, "upstream_deleted": 0}).
		OrderBy("upstream_seq DESC", "version DESC"))
	if err != nil {
		return core.VersionObj{}, false, err
	}
	defer rowsObj.Close()

	var semverMax, rawTop core.VersionObj
	haveSemver, haveRaw := false, false
	for rowsObj.Next() {
		versionObj, scanErr := scanVersion(rowsObj)
		if scanErr != nil {
			return core.VersionObj{}, false, scanErr
		}
		if util.IsStorableSemver(versionObj.Version) {
			if !haveSemver {
				semverMax, haveSemver = versionObj, true
			} else if cmp, cmpErr := util.CompareSemver(versionObj.Version, semverMax.Version); cmpErr == nil && cmp > 0 {
				semverMax = versionObj
			}
		} else if !haveRaw {
			rawTop, haveRaw = versionObj, true
		}
	}
	if err = rowsObj.Err(); err != nil {
		return core.VersionObj{}, false, err
	}

	switch {
	case haveSemver && haveRaw:
		if rawTop.UpstreamSeq > semverMax.UpstreamSeq {
			return rawTop, true, nil
		}
		return semverMax, true, nil
	case haveSemver:
		return semverMax, true, nil
	case haveRaw:
		return rawTop, true, nil
	default:
		return core.VersionObj{}, false, nil
	}
}

// TreeHashCountObj is one GROUP BY row with tree hash and referencing version count.
type TreeHashCountObj struct {
	Hash  core.HashObj
	Count uint64
}

// TreeHashesPage returns one keyset page of unique tree hashes.
// It feeds GC reachable-set membership without loading all versions into RAM.
func (obj *Obj) TreeHashesPage(ctx context.Context, afterHash []byte, limit int) ([]core.HashObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := obj.builderObj.Select("tree_hash").Distinct().From("versions")
	if len(afterHash) > 0 {
		selectObj = selectObj.Where(sq.Gt{"tree_hash": afterHash})
	}
	selectObj = selectObj.OrderBy("tree_hash ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr := make([]core.HashObj, 0, limit)
	for rowsObj.Next() {
		var treeArr []byte
		if err = rowsObj.Scan(&treeArr); err != nil {
			return nil, err
		}
		treeHashObj, hashErr := core.HashFromBytes(treeArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultArr = append(resultArr, treeHashObj)
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

// TreeHashCountsPage returns one keyset page of tree_hash counts for integrity and repair.
func (obj *Obj) TreeHashCountsPage(ctx context.Context, afterHash []byte, limit int) ([]TreeHashCountObj, error) {
	if limit < 1 {
		limit = 1
	}
	selectObj := obj.builderObj.Select("tree_hash", "COUNT(*)").From("versions")
	if len(afterHash) > 0 {
		selectObj = selectObj.Where(sq.Gt{"tree_hash": afterHash})
	}
	selectObj = selectObj.GroupBy("tree_hash").OrderBy("tree_hash ASC").Limit(uint64(limit))

	rowsObj, err := querySQL(ctx, obj, nil, selectObj)
	if err != nil {
		return nil, err
	}
	defer rowsObj.Close()

	resultArr := make([]TreeHashCountObj, 0, limit)
	for rowsObj.Next() {
		var treeArr []byte
		var countValue uint64
		if err = rowsObj.Scan(&treeArr, &countValue); err != nil {
			return nil, err
		}
		treeHashObj, hashErr := core.HashFromBytes(treeArr)
		if hashErr != nil {
			return nil, hashErr
		}
		resultArr = append(resultArr, TreeHashCountObj{Hash: treeHashObj, Count: countValue})
	}
	if err = rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

// ContentChecksum is deterministic blake3 over active key, version, and tree_hash rows in canonical order.
// It is the mirror content fingerprint used to compare nodes and brothers.
func (obj *Obj) ContentChecksum(ctx context.Context) (core.HashObj, error) {
	rowsObj, err := querySQL(ctx, obj, nil, obj.builderObj.
		Select("key", "version", "tree_hash").
		From("versions").
		Where(sq.Eq{"upstream_deleted": 0}).
		OrderBy("key ASC", "version ASC"))
	if err != nil {
		return core.HashObj{}, err
	}
	defer rowsObj.Close()

	hashObj := blake3.New()
	var hexArr [core.HashSize * 2]byte
	for rowsObj.Next() {
		var key string
		var version string
		var treeArr []byte
		if err = rowsObj.Scan(&key, &version, &treeArr); err != nil {
			return core.HashObj{}, err
		}
		treeHashObj, hashErr := core.HashFromBytes(treeArr)
		if hashErr != nil {
			return core.HashObj{}, hashErr
		}
		_, _ = hashObj.WriteString(key)
		_, _ = hashObj.WriteString("\t")
		_, _ = hashObj.WriteString(version)
		_, _ = hashObj.WriteString("\t")
		hex.Encode(hexArr[:], treeHashObj[:])
		_, _ = hashObj.Write(hexArr[:])
		_, _ = hashObj.WriteString("\n")
	}
	if err = rowsObj.Err(); err != nil {
		return core.HashObj{}, err
	}
	return core.HashFromHasher(hashObj), nil
}

// InsertVersion upserts a version row and updates all fields on key/version conflict.
func (obj *TxObj) InsertVersion(ctx context.Context, versionObj core.VersionObj) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Insert("versions").
		Columns(
			"key",
			"version",
			"source_hash",
			"source_size_bytes",
			"tree_hash",
			"ingest_ts",
			"upstream_seq",
			"upstream_deleted",
			"replaced_by_version",
			"release_notes",
			"heal_pending",
			"upstream_ref",
			"verified_ts",
		).
		Values(
			versionObj.Key,
			versionObj.Version,
			versionObj.SourceHash.BytesCopy(),
			versionObj.SourceSizeBytes,
			versionObj.TreeHash.BytesCopy(),
			core.FormatTime(versionObj.IngestTS),
			versionObj.UpstreamSeq,
			boolInt(versionObj.UpstreamDeleted),
			versionObj.ReplacedBy,
			versionObj.ReleaseNotes,
			boolInt(versionObj.HealPending),
			versionObj.UpstreamRef,
			formatVerifiedTS(versionObj.VerifiedTS),
		).
		Suffix("ON CONFLICT(key, version) DO UPDATE SET source_hash=excluded.source_hash, source_size_bytes=excluded.source_size_bytes, tree_hash=excluded.tree_hash, ingest_ts=excluded.ingest_ts, upstream_seq=excluded.upstream_seq, upstream_deleted=excluded.upstream_deleted, replaced_by_version=excluded.replaced_by_version, release_notes=excluded.release_notes, heal_pending=excluded.heal_pending, upstream_ref=excluded.upstream_ref, verified_ts=excluded.verified_ts"))
	return err
}

func formatVerifiedTS(ts time.Time) string {
	if ts.IsZero() {
		return ""
	}
	return core.FormatTime(ts)
}

// TouchVersionVerified updates deep-verification time and adopts a non-empty upstream_ref.
// A zero verifiedTS leaves verified_ts unchanged, allowing ref adoption without download.
func (obj *TxObj) TouchVersionVerified(ctx context.Context, key string, version string, verifiedTS time.Time, upstreamRef string) error {
	updateObj := obj.indexObj.builderObj.Update("versions").
		Where(sq.Eq{"key": key, "version": version})
	changedFlag := false
	if !verifiedTS.IsZero() {
		updateObj = updateObj.Set("verified_ts", core.FormatTime(verifiedTS))
		changedFlag = true
	}
	if upstreamRef != "" {
		updateObj = updateObj.Set("upstream_ref", upstreamRef)
		changedFlag = true
	}
	if !changedFlag {
		return nil
	}
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, updateObj)
	return err
}

// DeleteVersionRow physically deletes a version row; FK cascade cleans dependent rows.
func (obj *TxObj) DeleteVersionRow(ctx context.Context, key string, version string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Delete("versions").
		Where(sq.Eq{"key": key, "version": version}))
	return err
}

// MarkUpstreamDeleted marks a version as deleted upstream without physically deleting the row.
func (obj *TxObj) MarkUpstreamDeleted(ctx context.Context, key string, version string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Update("versions").
		Set("upstream_deleted", 1).
		Where(sq.Eq{"key": key, "version": version}))
	return err
}

// ClearUpstreamDeleted clears the tombstone when the version appears upstream again.
func (obj *TxObj) ClearUpstreamDeleted(ctx context.Context, key string, version string) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Update("versions").
		Set("upstream_deleted", 0).
		Where(sq.Eq{"key": key, "version": version}))
	return err
}

// SetHealPending marks whether a version needs damaged content recovery.
func (obj *TxObj) SetHealPending(ctx context.Context, key string, version string, pending bool) error {
	_, err := execSQL(ctx, obj.indexObj, obj.txObj, obj.indexObj.builderObj.Update("versions").
		Set("heal_pending", boolInt(pending)).
		Where(sq.Eq{"key": key, "version": version}))
	return err
}

// NewVersion builds core.VersionObj with current UTC IngestTS and other fields from arguments.
func NewVersion(key string, version string, sourceHashObj core.HashObj, sourceSizeBytes uint64, treeHashObj core.HashObj, upstreamDeletedFlag bool) core.VersionObj {
	return core.VersionObj{
		Key:             key,
		Version:         version,
		SourceHash:      sourceHashObj,
		SourceSizeBytes: sourceSizeBytes,
		TreeHash:        treeHashObj,
		IngestTS:        time.Now().UTC(),
		UpstreamDeleted: upstreamDeletedFlag,
	}
}

func sortVersions(versionArr []core.VersionObj) {
	sort.Slice(versionArr, func(i, j int) bool {
		if versionArr[i].UpstreamSeq != versionArr[j].UpstreamSeq {
			return versionArr[i].UpstreamSeq > versionArr[j].UpstreamSeq
		}
		return versionArr[i].Version > versionArr[j].Version
	})
}

// MaxUpstreamSeq returns the maximum upstream_seq for a key across all rows, deleted included; 0 means none.
func (obj *Obj) MaxUpstreamSeq(ctx context.Context, key string) (int64, error) {
	return maxUpstreamSeq(ctx, obj, nil, key)
}

// MaxUpstreamSeq is the in-transaction variant used by publish to assign a fallback position.
func (obj *TxObj) MaxUpstreamSeq(ctx context.Context, key string) (int64, error) {
	return maxUpstreamSeq(ctx, obj.indexObj, obj.txObj, key)
}

func maxUpstreamSeq(ctx context.Context, idxObj *Obj, txObj *sql.Tx, key string) (int64, error) {
	rowObj, err := queryRowSQL(ctx, idxObj, txObj, idxObj.builderObj.
		Select("COALESCE(MAX(upstream_seq), 0)").
		From("versions").
		Where(sq.Eq{"key": key}))
	if err != nil {
		return 0, err
	}
	var seqValue int64
	if err = rowObj.Scan(&seqValue); err != nil {
		return 0, err
	}
	return seqValue, nil
}
