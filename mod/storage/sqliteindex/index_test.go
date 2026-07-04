package sqliteindex

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func expectExecFails(t *testing.T, indexObj *Obj, queryText string, argsArr ...any) {
	t.Helper()

	if _, err := indexObj.dbObj.ExecContext(context.Background(), queryText, argsArr...); err == nil {
		t.Fatal("Exec accepted invalid hash size")
	}
}

// tempSqlitePath is the index temp-file path with symlinks resolved (macOS: t.TempDir is under /var).
func tempSqlitePath(t testing.TB) string {
	t.Helper()
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatalf("EvalSymlinks returned error: %v", err)
	}
	return filepath.Join(base, "index.sqlite")
}

// //

func TestOpenRejectsSymlinkIndexFile(t *testing.T) {
	ctx := context.Background()
	rootPath := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "outside.sqlite")
	pathToFile := filepath.Join(rootPath, "index.sqlite")
	if err := os.WriteFile(targetPath, nil, 0o644); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	if err := os.Symlink(targetPath, pathToFile); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	indexObj, err := Open(ctx, pathToFile)
	if err == nil {
		_ = indexObj.Close()
		t.Fatal("Open accepted symlink index file")
	}
}

func TestOpenRejectsSymlinkIndexParent(t *testing.T) {
	ctx := context.Background()
	basePath := t.TempDir()
	targetPath := filepath.Join(t.TempDir(), "target")
	if err := os.MkdirAll(targetPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	linkPath := filepath.Join(basePath, "link")
	if err := os.Symlink(targetPath, linkPath); err != nil {
		t.Skipf("Symlink is unavailable: %v", err)
	}

	indexObj, err := Open(ctx, filepath.Join(linkPath, "index.sqlite"))
	if err == nil {
		_ = indexObj.Close()
		t.Fatal("Open accepted symlink index parent")
	}
}

func TestMigrationUsesHash24Schema(t *testing.T) {
	ctx := context.Background()
	indexObj, err := Open(ctx, tempSqlitePath(t))
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	t.Cleanup(func() {
		if err = indexObj.Close(); err != nil {
			t.Fatalf("Close returned error: %v", err)
		}
	})

	var columnCount int
	if err = indexObj.dbObj.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('versions') WHERE name = 'source_hash'").Scan(&columnCount); err != nil {
		t.Fatalf("query source_hash column returned error: %v", err)
	}
	if columnCount != 1 {
		t.Fatalf("source_hash column count=%d, want 1", columnCount)
	}
	if err = indexObj.dbObj.QueryRowContext(ctx, "SELECT COUNT(*) FROM pragma_table_info('versions') WHERE name = 'source_sha256'").Scan(&columnCount); err != nil {
		t.Fatalf("query source_sha256 column returned error: %v", err)
	}
	if columnCount != 0 {
		t.Fatalf("source_sha256 column count=%d, want 0", columnCount)
	}

	var hashSizeText string
	if err = indexObj.dbObj.QueryRowContext(ctx, "SELECT value FROM globals WHERE name = 'hash_size_bytes'").Scan(&hashSizeText); err != nil {
		t.Fatalf("query hash_size_bytes returned error: %v", err)
	}
	expectedHashSizeText := strconv.Itoa(core.HashSize)
	if hashSizeText != expectedHashSizeText {
		t.Fatalf("hash_size_bytes=%q, want %s", hashSizeText, expectedHashSizeText)
	}
	var pebbleKeyFormatText string
	if err = indexObj.dbObj.QueryRowContext(ctx, "SELECT value FROM globals WHERE name = 'pebble_key_format'").Scan(&pebbleKeyFormatText); err != nil {
		t.Fatalf("query pebble_key_format returned error: %v", err)
	}
	if pebbleKeyFormatText != core.PebbleKeyFormat {
		t.Fatalf("pebble_key_format=%q, want %q", pebbleKeyFormatText, core.PebbleKeyFormat)
	}

	hashObj := core.HashBytes([]byte("db-contract"))
	goodArr := hashObj.BytesCopy()
	badArr := make([]byte, core.HashSize+8)
	ingestText := core.FormatTime(time.Now())
	expectExecFails(t, indexObj, "INSERT INTO versions(key, version, source_hash, source_size_bytes, tree_hash, ingest_ts) VALUES (?, ?, ?, ?, ?, ?)", "k", "v-bad-source", badArr, 1, goodArr, ingestText)
	expectExecFails(t, indexObj, "INSERT INTO versions(key, version, source_hash, source_size_bytes, tree_hash, ingest_ts) VALUES (?, ?, ?, ?, ?, ?)", "k", "v-bad-tree", goodArr, 1, badArr, ingestText)

	_, err = indexObj.dbObj.ExecContext(ctx, "INSERT INTO versions(key, version, source_hash, source_size_bytes, tree_hash, ingest_ts) VALUES (?, ?, ?, ?, ?, ?)", "k", "v-ok", goodArr, 1, goodArr, ingestText)
	if err != nil {
		t.Fatalf("versions rejected a 24-byte hash: %v", err)
	}
	expectExecFails(t, indexObj, "INSERT INTO rewrite_set(key, version, blob_hash) VALUES (?, ?, ?)", "k", "v-ok", badArr)
	expectExecFails(t, indexObj, "INSERT INTO materialized_artifacts(materializer_id, artifact_kind, listener_id, key, version, etag, body_hash, size_bytes, format_version) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)", "m", "zip", "global", "k", "v-ok", `"etag"`, badArr, 1, core.DefaultFormat)
	expectExecFails(t, indexObj, "INSERT INTO blob_refs(blob_hash, refcount, size_bytes) VALUES (?, ?, ?)", badArr, 1, 1)
	expectExecFails(t, indexObj, "INSERT INTO history_events(event_ts, key, version, event_type, tree_hash, message) VALUES (?, ?, ?, ?, ?, ?)", ingestText, "k", "v-ok", "test", badArr, "")
	expectExecFails(t, indexObj, "INSERT INTO history_events(event_ts, key, version, event_type, body_hash, message) VALUES (?, ?, ?, ?, ?, ?)", ingestText, "k", "v-ok", "test", badArr, "")
}

func TestOpenRejectsHashSizeMismatch(t *testing.T) {
	ctx := context.Background()
	pathToFile := tempSqlitePath(t)
	indexObj, err := Open(ctx, pathToFile)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err = indexObj.dbObj.ExecContext(ctx, "UPDATE globals SET value = ? WHERE name = 'hash_size_bytes'", strconv.Itoa(core.HashSize+1)); err != nil {
		t.Fatalf("update hash_size_bytes returned error: %v", err)
	}
	if err = indexObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	indexObj, err = Open(ctx, pathToFile)
	if err == nil {
		_ = indexObj.Close()
		t.Fatal("Open accepted a database with incompatible hash_size_bytes")
	}
}

func TestOpenRejectsPebbleKeyFormatMismatch(t *testing.T) {
	ctx := context.Background()
	pathToFile := tempSqlitePath(t)
	indexObj, err := Open(ctx, pathToFile)
	if err != nil {
		t.Fatalf("Open returned error: %v", err)
	}
	if _, err = indexObj.dbObj.ExecContext(ctx, "UPDATE globals SET value = ? WHERE name = 'pebble_key_format'", "legacy"); err != nil {
		t.Fatalf("update pebble_key_format returned error: %v", err)
	}
	if err = indexObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	indexObj, err = Open(ctx, pathToFile)
	if err == nil {
		_ = indexObj.Close()
		t.Fatal("Open accepted a database with incompatible pebble_key_format")
	}
}

func TestOpenRejectsIncompatibleExistingSchema(t *testing.T) {
	ctx := context.Background()
	pathToFile := tempSqlitePath(t)
	dbObj, err := openSQLite(ctx, pathToFile)
	if err != nil {
		t.Fatalf("openSQLite returned error: %v", err)
	}
	_, err = dbObj.ExecContext(ctx, `
CREATE TABLE globals (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
CREATE TABLE versions (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    source_hash BLOB NOT NULL,
    source_size_bytes INTEGER NOT NULL,
    tree_hash BLOB NOT NULL,
    ingest_ts TEXT NOT NULL,
    invalid INTEGER NOT NULL DEFAULT 0,
    upstream_deleted INTEGER NOT NULL DEFAULT 0,
    previous_version TEXT NOT NULL DEFAULT '',
    replaced_by_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (key, version)
) STRICT;
INSERT INTO globals(name, value) VALUES
    ('pebble_key_format', 'tag_hash24'),
    ('hash_size_bytes', '24');
`)
	if err != nil {
		t.Fatalf("seed incompatible schema returned error: %v", err)
	}
	if err = dbObj.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	indexObj, err := Open(ctx, pathToFile)
	if err == nil {
		_ = indexObj.Close()
		t.Fatal("Open accepted incompatible existing schema")
	}
}

func TestOpenRejectsIncompatibleExistingSecondarySchema(t *testing.T) {
	ctx := context.Background()
	baseSQL := `
CREATE TABLE globals (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;
CREATE TABLE versions (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    source_hash BLOB NOT NULL CHECK(length(source_hash) = 24),
    source_size_bytes INTEGER NOT NULL CHECK(source_size_bytes >= 0),
    tree_hash BLOB NOT NULL CHECK(length(tree_hash) = 24),
    ingest_ts TEXT NOT NULL,
    upstream_deleted INTEGER NOT NULL DEFAULT 0 CHECK(upstream_deleted IN (0, 1)),
    replaced_by_version TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (key, version)
) STRICT;
INSERT INTO globals(name, value) VALUES
    ('pebble_key_format', 'tag_hash24'),
    ('hash_size_bytes', '24');
`
	for _, testObj := range []struct {
		name     string
		tableSQL string
	}{
		{
			name: "detection",
			tableSQL: `
CREATE TABLE detection (
    key TEXT PRIMARY KEY
) STRICT;
`,
		},
		{
			name: "rewrite_set_fk",
			tableSQL: `
CREATE TABLE rewrite_set (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    blob_hash BLOB NOT NULL CHECK(length(blob_hash) = 24),
    PRIMARY KEY (key, version, blob_hash)
) STRICT;
`,
		},
		{
			name: "materialized_artifacts_fk",
			tableSQL: `
CREATE TABLE materialized_artifacts (
    materializer_id TEXT NOT NULL,
    artifact_kind TEXT NOT NULL,
    listener_id TEXT NOT NULL DEFAULT 'global',
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    etag TEXT NOT NULL,
    body_hash BLOB NOT NULL CHECK(length(body_hash) = 24),
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
    format_version INTEGER NOT NULL CHECK(format_version >= 1),
    file_path TEXT,
    degraded_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (materializer_id, artifact_kind, listener_id, key, version)
) STRICT;
`,
		},
	} {
		t.Run(testObj.name, func(t *testing.T) {
			pathToFile := tempSqlitePath(t)
			dbObj, err := openSQLite(ctx, pathToFile)
			if err != nil {
				t.Fatalf("openSQLite returned error: %v", err)
			}
			if _, err = dbObj.ExecContext(ctx, baseSQL+testObj.tableSQL); err != nil {
				t.Fatalf("seed incompatible schema returned error: %v", err)
			}
			if err = dbObj.Close(); err != nil {
				t.Fatalf("Close returned error: %v", err)
			}

			indexObj, err := Open(ctx, pathToFile)
			if err == nil {
				_ = indexObj.Close()
				t.Fatal("Open accepted incompatible existing schema")
			}
		})
	}
}
