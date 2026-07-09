package sqliteindex

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/pressly/goose/v3"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	sqlMaxBindArgs           = 900
	cSQLiteCheckpointTimeout = 5 * time.Second
)

var requiredColumnObj = map[string][]schemaColumnObj{
	"globals": {
		{name: "name", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "value", dataType: "TEXT", notNull: true},
	},
	"versions": {
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "version", dataType: "TEXT", notNull: true, primaryKey: 2},
		{name: "source_hash", dataType: "BLOB", notNull: true},
		{name: "source_size_bytes", dataType: "INTEGER", notNull: true},
		{name: "tree_hash", dataType: "BLOB", notNull: true},
		{name: "ingest_ts", dataType: "TEXT", notNull: true},
		{name: "upstream_seq", dataType: "INTEGER", notNull: true},
		{name: "upstream_deleted", dataType: "INTEGER", notNull: true},
		{name: "replaced_by_version", dataType: "TEXT", notNull: true},
		{name: "heal_pending", dataType: "INTEGER", notNull: true},
		{name: "upstream_ref", dataType: "TEXT", notNull: true},
		{name: "verified_ts", dataType: "TEXT", notNull: true},
		{name: "release_notes", dataType: "TEXT", notNull: true},
	},
	"detection": {
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "version", dataType: "TEXT", notNull: true, primaryKey: 2},
		{name: "is_go", dataType: "INTEGER", notNull: true},
		{name: "is_composer", dataType: "INTEGER", notNull: true},
		{name: "conflict", dataType: "INTEGER", notNull: true},
		{name: "evidence_json", dataType: "TEXT", notNull: true},
		{name: "go_zip_blocked", dataType: "INTEGER", notNull: true},
		{name: "go_zip_block_reason", dataType: "TEXT", notNull: true},
	},
	"rewrite_set": {
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "version", dataType: "TEXT", notNull: true, primaryKey: 2},
		{name: "blob_hash", dataType: "BLOB", notNull: true, primaryKey: 3},
	},
	"materialized_artifacts": {
		{name: "materializer_id", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "artifact_kind", dataType: "TEXT", notNull: true, primaryKey: 2},
		{name: "listener_id", dataType: "TEXT", notNull: true, primaryKey: 3},
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 4},
		{name: "version", dataType: "TEXT", notNull: true, primaryKey: 5},
		{name: "etag", dataType: "TEXT", notNull: true},
		{name: "body_hash", dataType: "BLOB", notNull: true},
		{name: "body_sha256", dataType: "BLOB"},
		{name: "body_sha1", dataType: "BLOB"},
		{name: "size_bytes", dataType: "INTEGER", notNull: true},
		{name: "format_version", dataType: "INTEGER", notNull: true},
		{name: "file_path", dataType: "TEXT"},
		{name: "degraded_reason", dataType: "TEXT", notNull: true},
	},
	"blob_refs": {
		{name: "blob_hash", dataType: "BLOB", notNull: true, primaryKey: 1},
		{name: "refcount", dataType: "INTEGER", notNull: true},
		{name: "size_bytes", dataType: "INTEGER", notNull: true},
	},
	"history_events": {
		{name: "id", dataType: "INTEGER", notNull: false, primaryKey: 1},
		{name: "event_ts", dataType: "TEXT", notNull: true},
		{name: "key", dataType: "TEXT", notNull: true},
		{name: "version", dataType: "TEXT", notNull: true},
		{name: "event_type", dataType: "TEXT", notNull: true},
		{name: "tree_hash", dataType: "BLOB"},
		{name: "body_hash", dataType: "BLOB"},
		{name: "message", dataType: "TEXT", notNull: true},
	},
	"key_source": {
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "url", dataType: "TEXT", notNull: true},
		{name: "class", dataType: "TEXT", notNull: true},
		{name: "web_addr", dataType: "TEXT", notNull: true},
		{name: "ygg_addr", dataType: "TEXT", notNull: true},
		{name: "origin_url", dataType: "TEXT", notNull: true},
		{name: "bound_ts", dataType: "TEXT", notNull: true},
		{name: "listing_mode", dataType: "TEXT", notNull: true},
	},
	"ingest_failures": {
		{name: "key", dataType: "TEXT", notNull: true, primaryKey: 1},
		{name: "version", dataType: "TEXT", notNull: true, primaryKey: 2},
		{name: "ref_sha", dataType: "TEXT", notNull: true},
		{name: "code", dataType: "TEXT", notNull: true},
		{name: "message", dataType: "TEXT", notNull: true},
		{name: "policy", dataType: "INTEGER", notNull: true},
		{name: "first_ts", dataType: "TEXT", notNull: true},
		{name: "last_ts", dataType: "TEXT", notNull: true},
		{name: "count", dataType: "INTEGER", notNull: true},
	},
}

var requiredIndexObj = map[string][]string{
	"versions": {
		"idx_versions_key_seq",
		"idx_versions_ingest",
		"idx_versions_tree",
	},
	"materialized_artifacts": {
		"idx_artifacts_lookup",
	},
	"blob_refs": {
		"idx_blob_refs_count",
	},
	"history_events": {
		"idx_history_feed",
		"idx_history_kv",
	},
}

var requiredForeignKeyObj = map[string]schemaForeignKeyObj{
	"detection": {
		tableName: "versions",
		fromArr:   []string{"key", "version"},
		toArr:     []string{"key", "version"},
		onDelete:  "CASCADE",
	},
	"rewrite_set": {
		tableName: "versions",
		fromArr:   []string{"key", "version"},
		toArr:     []string{"key", "version"},
		onDelete:  "CASCADE",
	},
	"materialized_artifacts": {
		tableName: "versions",
		fromArr:   []string{"key", "version"},
		toArr:     []string{"key", "version"},
		onDelete:  "CASCADE",
	},
}

var requiredTableSQLObj = map[string][]string{
	"globals": {
		"STRICT",
	},
	"versions": {
		"source_hash BLOB NOT NULL CHECK(length(source_hash) = 24)",
		"tree_hash BLOB NOT NULL CHECK(length(tree_hash) = 24)",
		"STRICT",
	},
	"detection": {
		"is_go INTEGER NOT NULL DEFAULT 0 CHECK(is_go IN (0, 1))",
		"is_composer INTEGER NOT NULL DEFAULT 0 CHECK(is_composer IN (0, 1))",
		"conflict INTEGER NOT NULL DEFAULT 0 CHECK(conflict IN (0, 1))",
		"evidence_json TEXT NOT NULL DEFAULT '{}'",
		"go_zip_blocked INTEGER NOT NULL DEFAULT 0 CHECK(go_zip_blocked IN (0, 1))",
		"go_zip_block_reason TEXT NOT NULL DEFAULT ''",
		"STRICT",
	},
	"rewrite_set": {
		"blob_hash BLOB NOT NULL CHECK(length(blob_hash) = 24)",
		"STRICT",
	},
	"materialized_artifacts": {
		"body_hash BLOB NOT NULL CHECK(length(body_hash) = 24)",
		"STRICT",
	},
	"blob_refs": {
		"blob_hash BLOB PRIMARY KEY CHECK(length(blob_hash) = 24)",
		"refcount INTEGER NOT NULL CHECK(refcount >= 0)",
		"size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0)",
		"STRICT",
	},
	"history_events": {
		"tree_hash BLOB CHECK(tree_hash IS NULL OR length(tree_hash) = 24)",
		"body_hash BLOB CHECK(body_hash IS NULL OR length(body_hash) = 24)",
		"STRICT",
	},
	"key_source": {
		"listing_mode IN ('', 'releases', 'tags')",
		"STRICT",
	},
	"ingest_failures": {
		"count INTEGER NOT NULL CHECK(count >= 1)",
		"STRICT",
	},
}

// //

type rowScannerInterface interface {
	Scan(dest ...any) error
}

type schemaColumnObj struct {
	name       string
	dataType   string
	notNull    bool
	primaryKey int
}

type schemaForeignKeyObj struct {
	tableName string
	fromArr   []string
	toArr     []string
	onDelete  string
}

// Obj stores the SQLite metadata index for versions, detection, blob_refs, artifacts and history.
// Reads go through the pool; writes are serialized by the external storage.writeMu.
// Transactional mutations go through TxObj and WithTx.
type Obj struct {
	dbObj      *sql.DB
	builderObj sq.StatementBuilderType
	gooseObj   *goose.Provider
	stmtCache  *stmtCacheObj
}

// TxObj opens operations inside one transaction; mutators live here, not on Obj.
type TxObj struct {
	indexObj *Obj
	txObj    *sql.Tx
}

// //

func openSQLite(ctx context.Context, pathToFile string) (*sql.DB, error) {
	dbObj, err := sql.Open(sqliteDriverName, sqliteDSN(pathToFile))
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	maxOpen := max(runtime.NumCPU(), 4)
	dbObj.SetMaxOpenConns(maxOpen)
	dbObj.SetMaxIdleConns(maxOpen)
	dbObj.SetConnMaxLifetime(0)

	// journal_mode, synchronous, foreign_keys and busy_timeout are applied per connection through the driver DSN.
	// ExecContext PRAGMA here would bind only to one pooled connection.
	if err = dbObj.PingContext(ctx); err != nil {
		_ = dbObj.Close()
		return nil, fmt.Errorf("ping sqlite: %w", err)
	}
	return dbObj, nil
}

func validateSQLiteFile(pathToFile string) error {
	infoObj, err := os.Lstat(pathToFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if infoObj.Mode()&os.ModeSymlink != 0 || !infoObj.Mode().IsRegular() {
		return errors.New("sqlite file is not a regular file")
	}
	return nil
}

func validateSQLitePathComponents(pathText string) error {
	cleanPath, err := filepath.Abs(pathText)
	if err != nil {
		return err
	}
	currentPath, relPath := splitPathRoot(cleanPath)
	for _, partText := range strings.Split(relPath, string(filepath.Separator)) {
		if partText == "" {
			continue
		}
		currentPath = filepath.Join(currentPath, partText)
		infoObj, err := os.Lstat(currentPath)
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		if infoObj.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("sqlite path contains symlink component: %s", currentPath)
		}
		if currentPath != cleanPath && !infoObj.IsDir() {
			return fmt.Errorf("sqlite path parent is not a regular directory: %s", currentPath)
		}
	}
	return nil
}

func splitPathRoot(cleanPath string) (string, string) {
	volumeText := filepath.VolumeName(cleanPath)
	rootPath := string(filepath.Separator)
	if volumeText != "" {
		rootPath = volumeText + string(filepath.Separator)
	}
	return rootPath, strings.TrimPrefix(cleanPath, rootPath)
}

func validateSQLitePath(pathToFile string) error {
	if err := validateSQLitePathComponents(pathToFile); err != nil {
		return err
	}
	parentPath := filepath.Dir(pathToFile)
	parentInfoObj, err := os.Lstat(parentPath)
	if err != nil {
		return err
	}
	if parentInfoObj.Mode()&os.ModeSymlink != 0 || !parentInfoObj.IsDir() {
		return errors.New("sqlite directory is not a regular directory")
	}
	for _, candidatePath := range []string{pathToFile, pathToFile + "-wal", pathToFile + "-shm"} {
		if err = validateSQLiteFile(candidatePath); err != nil {
			return err
		}
	}
	return nil
}

func runMigrations(ctx context.Context, dbObj *sql.DB) (*goose.Provider, error) {
	migrationsObj, err := fs.Sub(migrationFSObj, "migrations")
	if err != nil {
		return nil, fmt.Errorf("open embedded migrations: %w", err)
	}
	providerObj, err := goose.NewProvider(goose.DialectSQLite3, dbObj, migrationsObj)
	if err != nil {
		return nil, fmt.Errorf("create migration provider: %w", err)
	}
	if _, err = providerObj.Up(ctx); err != nil {
		_ = providerObj.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}
	return providerObj, nil
}

func toSQL(sqlizerObj sq.Sqlizer) (string, []any, error) {
	query, argsArr, err := sqlizerObj.ToSql()
	if err != nil {
		return "", nil, err
	}
	return query, argsArr, nil
}

func execSQL(ctx context.Context, idxObj *Obj, txObj *sql.Tx, sqlizerObj sq.Sqlizer) (sql.Result, error) {
	query, argsArr, err := toSQL(sqlizerObj)
	if err != nil {
		return nil, err
	}
	if stmtObj, ok := idxObj.stmtCache.prepared(ctx, query); ok {
		if txObj != nil {
			return txObj.StmtContext(ctx, stmtObj).ExecContext(ctx, argsArr...)
		}
		return stmtObj.ExecContext(ctx, argsArr...)
	}
	if txObj != nil {
		return txObj.ExecContext(ctx, query, argsArr...)
	}
	return idxObj.dbObj.ExecContext(ctx, query, argsArr...)
}

func querySQL(ctx context.Context, idxObj *Obj, txObj *sql.Tx, sqlizerObj sq.Sqlizer) (*sql.Rows, error) {
	query, argsArr, err := toSQL(sqlizerObj)
	if err != nil {
		return nil, err
	}
	if stmtObj, ok := idxObj.stmtCache.prepared(ctx, query); ok {
		if txObj != nil {
			return txObj.StmtContext(ctx, stmtObj).QueryContext(ctx, argsArr...)
		}
		return stmtObj.QueryContext(ctx, argsArr...)
	}
	if txObj != nil {
		return txObj.QueryContext(ctx, query, argsArr...)
	}
	return idxObj.dbObj.QueryContext(ctx, query, argsArr...)
}

func queryRowSQL(ctx context.Context, idxObj *Obj, txObj *sql.Tx, sqlizerObj sq.Sqlizer) (*sql.Row, error) {
	query, argsArr, err := toSQL(sqlizerObj)
	if err != nil {
		return nil, err
	}
	if stmtObj, ok := idxObj.stmtCache.prepared(ctx, query); ok {
		if txObj != nil {
			return txObj.StmtContext(ctx, stmtObj).QueryRowContext(ctx, argsArr...), nil
		}
		return stmtObj.QueryRowContext(ctx, argsArr...), nil
	}
	if txObj != nil {
		return txObj.QueryRowContext(ctx, query, argsArr...), nil
	}
	return idxObj.dbObj.QueryRowContext(ctx, query, argsArr...), nil
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

func sqlChunkSize(columnCount int) int {
	if columnCount <= 0 {
		return 1
	}
	sizeValue := sqlMaxBindArgs / columnCount
	if sizeValue < 1 {
		return 1
	}
	return sizeValue
}

// scanAll collects every row into a slice via scanFn, then reports any row-iteration error. It replaces the
// repeated `for rows.Next() { scan; append } + rows.Err()` tail shared by the keyset/page version reads.
func scanAll[T any](rowsObj *sql.Rows, capacity int, scanFn func(rowScannerInterface) (T, error)) ([]T, error) {
	if capacity < 0 {
		capacity = 0
	}
	resultArr := make([]T, 0, capacity)
	for rowsObj.Next() {
		itemObj, err := scanFn(rowsObj)
		if err != nil {
			return nil, err
		}
		resultArr = append(resultArr, itemObj)
	}
	if err := rowsObj.Err(); err != nil {
		return nil, err
	}
	return resultArr, nil
}

func scanVersion(scannerObj rowScannerInterface) (core.VersionObj, error) {
	var versionObj core.VersionObj
	var sourceArr []byte
	var treeArr []byte
	var ingestText string
	var deletedValue int
	var healPendingValue int
	var verifiedText string
	if err := scannerObj.Scan(
		&versionObj.Key,
		&versionObj.Version,
		&sourceArr,
		&versionObj.SourceSizeBytes,
		&treeArr,
		&ingestText,
		&versionObj.UpstreamSeq,
		&deletedValue,
		&versionObj.ReplacedBy,
		&versionObj.ReleaseNotes,
		&healPendingValue,
		&versionObj.UpstreamRef,
		&verifiedText,
	); err != nil {
		return versionObj, err
	}

	sourceHashObj, err := core.HashFromBytes(sourceArr)
	if err != nil {
		return versionObj, fmt.Errorf("invalid source hash in database: %w", err)
	}
	treeHashObj, err := core.HashFromBytes(treeArr)
	if err != nil {
		return versionObj, fmt.Errorf("invalid tree hash in database: %w", err)
	}
	ts, err := core.ParseTime(ingestText)
	if err != nil {
		return versionObj, fmt.Errorf("invalid ingest time in database: %w", err)
	}
	// Empty verified_ts means deep verification has not run yet.
	if verifiedText != "" {
		versionObj.VerifiedTS, err = core.ParseTime(verifiedText)
		if err != nil {
			return versionObj, fmt.Errorf("invalid verified time in database: %w", err)
		}
	}

	versionObj.SourceHash = sourceHashObj
	versionObj.TreeHash = treeHashObj
	versionObj.IngestTS = ts
	versionObj.UpstreamDeleted = deletedValue != 0
	versionObj.HealPending = healPendingValue != 0
	return versionObj, nil
}

func versionSelect(builderObj sq.StatementBuilderType, includeNotes bool) sq.SelectBuilder {
	// Service scans preserve scanVersion shape without loading heavy release notes.
	notesColumn := "''"
	if includeNotes {
		notesColumn = "versions.release_notes"
	}
	return builderObj.Select(
		"versions.key",
		"versions.version",
		"versions.source_hash",
		"versions.source_size_bytes",
		"versions.tree_hash",
		"versions.ingest_ts",
		"versions.upstream_seq",
		"versions.upstream_deleted",
		"versions.replaced_by_version",
		notesColumn,
		"versions.heal_pending",
		"versions.upstream_ref",
		"versions.verified_ts",
	).From("versions")
}

func (obj *Obj) validateGlobal(ctx context.Context, name string, expectedText string) error {
	rowObj, err := queryRowSQL(ctx, obj, nil, obj.builderObj.
		Select("value").
		From("globals").
		Where(sq.Eq{"name": name}))
	if err != nil {
		return err
	}

	var valueText string
	if err = rowObj.Scan(&valueText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("storage metadata %s is missing", name)
		}
		return err
	}
	if valueText != expectedText {
		return fmt.Errorf("storage metadata %s mismatch: database=%s binary=%s", name, valueText, expectedText)
	}
	return nil
}

func (obj *Obj) validateStorageFormat(ctx context.Context) error {
	if err := obj.validateGlobal(ctx, "hash_size_bytes", strconv.Itoa(core.HashSize)); err != nil {
		return err
	}
	if err := obj.validateGlobal(ctx, "pebble_key_format", core.PebbleKeyFormat); err != nil {
		return err
	}
	if err := obj.validateStorageSchema(ctx); err != nil {
		return err
	}
	return nil
}

func (obj *Obj) validateTableConstraints(ctx context.Context, tableName string, requiredArr []string) error {
	rowObj := obj.dbObj.QueryRowContext(ctx, "SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?", tableName)
	var sqlText string
	if err := rowObj.Scan(&sqlText); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("storage table %s is missing", tableName)
		}
		return err
	}
	for _, requiredText := range requiredArr {
		if !strings.Contains(sqlText, requiredText) {
			return fmt.Errorf("storage table %s has incompatible schema", tableName)
		}
	}
	return nil
}

func (obj *Obj) validateTableStrict(ctx context.Context, tableName string) error {
	rowObj := obj.dbObj.QueryRowContext(ctx, "SELECT strict FROM pragma_table_list WHERE type = 'table' AND name = ?", tableName)
	var strictValue int
	if err := rowObj.Scan(&strictValue); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("storage table %s is missing", tableName)
		}
		return err
	}
	if strictValue != 1 {
		return fmt.Errorf("storage table %s is not STRICT", tableName)
	}
	return nil
}

func (obj *Obj) validateTableColumns(ctx context.Context, tableName string, requiredArr []schemaColumnObj) error {
	rowsObj, err := obj.dbObj.QueryContext(ctx, "SELECT name, type, \"notnull\", pk FROM pragma_table_info(?)", tableName)
	if err != nil {
		return err
	}
	defer rowsObj.Close()

	actualObj := make(map[string]schemaColumnObj, len(requiredArr))
	for rowsObj.Next() {
		var columnObj schemaColumnObj
		var notNullValue int
		if err = rowsObj.Scan(&columnObj.name, &columnObj.dataType, &notNullValue, &columnObj.primaryKey); err != nil {
			return err
		}
		columnObj.notNull = notNullValue != 0
		actualObj[columnObj.name] = columnObj
	}
	if err = rowsObj.Err(); err != nil {
		return err
	}
	for _, requiredObj := range requiredArr {
		actualColumnObj, ok := actualObj[requiredObj.name]
		if !ok {
			return fmt.Errorf("storage table %s column %s is missing", tableName, requiredObj.name)
		}
		if actualColumnObj.dataType != requiredObj.dataType || actualColumnObj.notNull != requiredObj.notNull || actualColumnObj.primaryKey != requiredObj.primaryKey {
			return fmt.Errorf("storage table %s column %s has incompatible schema", tableName, requiredObj.name)
		}
	}
	return nil
}

func (obj *Obj) validateTableIndexes(ctx context.Context, tableName string, requiredArr []string) error {
	if len(requiredArr) == 0 {
		return nil
	}
	rowsObj, err := obj.dbObj.QueryContext(ctx, "SELECT name FROM pragma_index_list(?)", tableName)
	if err != nil {
		return err
	}
	defer rowsObj.Close()

	actualObj := make(map[string]struct{}, len(requiredArr))
	for rowsObj.Next() {
		var indexName string
		if err = rowsObj.Scan(&indexName); err != nil {
			return err
		}
		actualObj[indexName] = struct{}{}
	}
	if err = rowsObj.Err(); err != nil {
		return err
	}
	for _, indexName := range requiredArr {
		if _, ok := actualObj[indexName]; !ok {
			return fmt.Errorf("storage table %s index %s is missing", tableName, indexName)
		}
	}
	return nil
}

func (obj *Obj) validateTableForeignKey(ctx context.Context, tableName string, requiredObj schemaForeignKeyObj) error {
	rowsObj, err := obj.dbObj.QueryContext(ctx, "SELECT id, seq, \"table\", \"from\", \"to\", on_delete FROM pragma_foreign_key_list(?)", tableName)
	if err != nil {
		return err
	}
	defer rowsObj.Close()

	type fkPartObj struct {
		tableName string
		fromArr   []string
		toArr     []string
		onDelete  string
	}
	actualObj := make(map[int]fkPartObj)
	for rowsObj.Next() {
		var idValue int
		var seqValue int
		var tableText string
		var fromText string
		var toText string
		var onDeleteText string
		if err = rowsObj.Scan(&idValue, &seqValue, &tableText, &fromText, &toText, &onDeleteText); err != nil {
			return err
		}
		itemObj := actualObj[idValue]
		itemObj.tableName = tableText
		itemObj.onDelete = onDeleteText
		for len(itemObj.fromArr) <= seqValue {
			itemObj.fromArr = append(itemObj.fromArr, "")
			itemObj.toArr = append(itemObj.toArr, "")
		}
		itemObj.fromArr[seqValue] = fromText
		itemObj.toArr[seqValue] = toText
		actualObj[idValue] = itemObj
	}
	if err = rowsObj.Err(); err != nil {
		return err
	}
	for _, actualFKObj := range actualObj {
		if actualFKObj.tableName == requiredObj.tableName &&
			actualFKObj.onDelete == requiredObj.onDelete &&
			strings.Join(actualFKObj.fromArr, "\x00") == strings.Join(requiredObj.fromArr, "\x00") &&
			strings.Join(actualFKObj.toArr, "\x00") == strings.Join(requiredObj.toArr, "\x00") {
			return nil
		}
	}
	return fmt.Errorf("storage table %s foreign key is missing", tableName)
}

func (obj *Obj) validateDatabaseChecks(ctx context.Context) error {
	issueArr, err := obj.CheckSQLiteIntegrity(ctx)
	if err != nil {
		return err
	}
	if len(issueArr) > 0 {
		return errors.New(strings.Join(issueArr, "; "))
	}
	return nil
}

func (obj *Obj) validateStorageSchema(ctx context.Context) error {
	for tableName, requiredArr := range requiredColumnObj {
		if err := obj.validateTableStrict(ctx, tableName); err != nil {
			return err
		}
		if err := obj.validateTableColumns(ctx, tableName, requiredArr); err != nil {
			return err
		}
	}
	for tableName, requiredArr := range requiredTableSQLObj {
		if err := obj.validateTableConstraints(ctx, tableName, requiredArr); err != nil {
			return err
		}
	}
	for tableName, requiredArr := range requiredIndexObj {
		if err := obj.validateTableIndexes(ctx, tableName, requiredArr); err != nil {
			return err
		}
	}
	for tableName, requiredObj := range requiredForeignKeyObj {
		if err := obj.validateTableForeignKey(ctx, tableName, requiredObj); err != nil {
			return err
		}
	}
	if err := obj.validateDatabaseChecks(ctx); err != nil {
		return err
	}
	return nil
}

// CheckSQLiteIntegrity runs quick_check and foreign_key_check, returning discovered issues.
// An empty list means a consistent DB; used during open and --inspect.
func (obj *Obj) CheckSQLiteIntegrity(ctx context.Context) ([]string, error) {
	quickRows, err := obj.dbObj.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return nil, err
	}
	defer quickRows.Close()

	issueArr := make([]string, 0)
	for quickRows.Next() {
		var resultText string
		if err = quickRows.Scan(&resultText); err != nil {
			return nil, err
		}
		if resultText != "ok" {
			issueArr = append(issueArr, "sqlite quick_check: "+resultText)
		}
	}
	if err = quickRows.Err(); err != nil {
		return nil, err
	}

	fkRows, err := obj.dbObj.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return nil, err
	}
	defer fkRows.Close()
	for fkRows.Next() {
		var tableText string
		var rowID any
		var parentText string
		var fkID int
		if err = fkRows.Scan(&tableText, &rowID, &parentText, &fkID); err != nil {
			return nil, err
		}
		issueArr = append(issueArr, fmt.Sprintf("sqlite foreign_key_check: table=%s rowid=%v parent=%s fkid=%d", tableText, rowID, parentText, fkID))
	}
	if err = fkRows.Err(); err != nil {
		return nil, err
	}
	return issueArr, nil
}

// //

// Open validates the path, opens SQLite with WAL/synchronous=NORMAL, runs migrations and format checks.
// Format or schema mismatch stops open.
func Open(ctx context.Context, pathToFile string) (*Obj, error) {
	if err := validateSQLitePath(pathToFile); err != nil {
		return nil, err
	}
	dbObj, err := openSQLite(ctx, pathToFile)
	if err != nil {
		return nil, err
	}
	gooseObj, err := runMigrations(ctx, dbObj)
	if err != nil {
		_ = dbObj.Close()
		return nil, err
	}
	obj := &Obj{
		dbObj:      dbObj,
		builderObj: sq.StatementBuilder.PlaceholderFormat(sq.Question),
		gooseObj:   gooseObj,
		stmtCache:  newStmtCache(dbObj, cStmtCacheCap),
	}
	if err = obj.validateStorageFormat(ctx); err != nil {
		_ = obj.CloseContext(ctx)
		return nil, err
	}
	return obj, nil
}

// Close closes the index with a background context.
func (obj *Obj) Close() error {
	return obj.CloseContext(context.Background())
}

// CloseContext runs the final checkpoint(TRUNCATE), then closes migrations, prepared cache and DB.
// The method is safe for a nil receiver; statement cache is closed before DB.
func (obj *Obj) CloseContext(ctx context.Context) error {
	if obj == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var errArr []error
	if obj.dbObj != nil {
		if err := obj.Checkpoint(ctx); err != nil {
			errArr = append(errArr, fmt.Errorf("sqlite checkpoint: %w", err))
		}
	}
	if obj.gooseObj != nil {
		if err := obj.gooseObj.Close(); err != nil {
			errArr = append(errArr, fmt.Errorf("close migrations: %w", err))
		}
	}
	if obj.stmtCache != nil {
		obj.stmtCache.close()
	}
	if obj.dbObj != nil {
		if err := obj.dbObj.Close(); err != nil {
			errArr = append(errArr, fmt.Errorf("close sqlite: %w", err))
		}
	}
	return errors.Join(errArr...)
}

// Vacuum rebuilds the DB file and returns freed pages.
// SQLite requires running outside a transaction; storage.writeMu serializes the call, and free disk must cover DB size.
func (obj *Obj) Vacuum(ctx context.Context) error {
	if _, err := obj.dbObj.ExecContext(ctx, "VACUUM"); err != nil {
		return fmt.Errorf("sqlite vacuum: %w", err)
	}
	return nil
}

// Checkpoint force-runs wal_checkpoint(TRUNCATE) with a timeout and errors on incomplete results.
func (obj *Obj) Checkpoint(ctx context.Context) error {
	checkpointCtx, cancelFunc := context.WithTimeout(ctx, cSQLiteCheckpointTimeout)
	defer cancelFunc()
	rowObj := obj.dbObj.QueryRowContext(checkpointCtx, "PRAGMA wal_checkpoint(TRUNCATE)")
	var busyValue int
	var logValue int
	var checkpointedValue int
	if err := rowObj.Scan(&busyValue, &logValue, &checkpointedValue); err != nil {
		return err
	}
	if busyValue != 0 || logValue != checkpointedValue {
		return fmt.Errorf("sqlite checkpoint incomplete: busy=%d log=%d checkpointed=%d", busyValue, logValue, checkpointedValue)
	}
	return nil
}

// WithTx runs useFunc in a transaction: nil error commits, non-nil rolls back.
// Panics are rolled back and converted to errors; callers provide write serialization.
func (obj *Obj) WithTx(ctx context.Context, useFunc func(*TxObj) error) (err error) {
	txObj, err := obj.dbObj.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	closedFlag := false
	defer func() {
		if panicObj := recover(); panicObj != nil {
			if !closedFlag {
				_ = txObj.Rollback()
			}
			err = fmt.Errorf("sqlite transaction panic: %v", panicObj)
			return
		}
		if !closedFlag {
			_ = txObj.Rollback()
		}
	}()
	wrappedObj := &TxObj{indexObj: obj, txObj: txObj}
	if err = useFunc(wrappedObj); err != nil {
		return err
	}
	if err = txObj.Commit(); err != nil {
		return err
	}
	closedFlag = true
	return nil
}

// CountTable returns COUNT(*) for whitelisted tables used by --inspect.
// Other table names are rejected against injection.
func (obj *Obj) CountTable(ctx context.Context, tableName string) (uint64, error) {
	switch tableName {
	case "versions", "blob_refs", "materialized_artifacts", "history_events":
	default:
		return 0, errors.New("invalid table name")
	}

	query, argsArr, err := obj.builderObj.
		Select("COUNT(*)").
		From(tableName).
		ToSql()
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
