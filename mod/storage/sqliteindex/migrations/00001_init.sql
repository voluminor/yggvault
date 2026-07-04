-- +goose Up
PRAGMA foreign_keys = ON;

CREATE TABLE IF NOT EXISTS globals (
    name TEXT PRIMARY KEY,
    value TEXT NOT NULL
) STRICT;

CREATE TABLE IF NOT EXISTS versions (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    source_hash BLOB NOT NULL CHECK(length(source_hash) = 24),
    source_size_bytes INTEGER NOT NULL CHECK(source_size_bytes >= 0),
    tree_hash BLOB NOT NULL CHECK(length(tree_hash) = 24),
    ingest_ts TEXT NOT NULL,
    upstream_seq INTEGER NOT NULL DEFAULT 0 CHECK(upstream_seq >= 0),
    upstream_deleted INTEGER NOT NULL DEFAULT 0 CHECK(upstream_deleted IN (0, 1)),
    replaced_by_version TEXT NOT NULL DEFAULT '',
    heal_pending INTEGER NOT NULL DEFAULT 0 CHECK(heal_pending IN (0, 1)),
    upstream_ref TEXT NOT NULL DEFAULT '',
    verified_ts TEXT NOT NULL DEFAULT '',
    -- release_notes физически последняя: многокилобайтные заметки уводят строку в overflow-страницы,
    -- хвостовое размещение сохраняет горячие колонки внутри страницы.
    release_notes TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (key, version)
) STRICT;

CREATE TABLE IF NOT EXISTS detection (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    is_go INTEGER NOT NULL DEFAULT 0 CHECK(is_go IN (0, 1)),
    is_composer INTEGER NOT NULL DEFAULT 0 CHECK(is_composer IN (0, 1)),
    conflict INTEGER NOT NULL DEFAULT 0 CHECK(conflict IN (0, 1)),
    evidence_json TEXT NOT NULL DEFAULT '{}',
    go_zip_blocked INTEGER NOT NULL DEFAULT 0 CHECK(go_zip_blocked IN (0, 1)),
    go_zip_block_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (key, version),
    FOREIGN KEY (key, version) REFERENCES versions(key, version) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS rewrite_set (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    blob_hash BLOB NOT NULL CHECK(length(blob_hash) = 24),
    PRIMARY KEY (key, version, blob_hash),
    FOREIGN KEY (key, version) REFERENCES versions(key, version) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS materialized_artifacts (
    materializer_id TEXT NOT NULL,
    artifact_kind TEXT NOT NULL,
    listener_id TEXT NOT NULL DEFAULT 'global',
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    etag TEXT NOT NULL,
    body_hash BLOB NOT NULL CHECK(length(body_hash) = 24),
    body_sha256 BLOB CHECK(body_sha256 IS NULL OR length(body_sha256) = 32),
    body_sha1 BLOB CHECK(body_sha1 IS NULL OR length(body_sha1) = 20),
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0),
    format_version INTEGER NOT NULL CHECK(format_version >= 1),
    file_path TEXT,
    degraded_reason TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (materializer_id, artifact_kind, listener_id, key, version),
    FOREIGN KEY (key, version) REFERENCES versions(key, version) ON DELETE CASCADE
) STRICT;

CREATE TABLE IF NOT EXISTS blob_refs (
    blob_hash BLOB PRIMARY KEY CHECK(length(blob_hash) = 24),
    refcount INTEGER NOT NULL CHECK(refcount >= 0),
    size_bytes INTEGER NOT NULL CHECK(size_bytes >= 0)
) STRICT;

CREATE TABLE IF NOT EXISTS history_events (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    event_ts TEXT NOT NULL,
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    event_type TEXT NOT NULL,
    tree_hash BLOB CHECK(tree_hash IS NULL OR length(tree_hash) = 24),
    body_hash BLOB CHECK(body_hash IS NULL OR length(body_hash) = 24),
    message TEXT NOT NULL DEFAULT ''
) STRICT;

CREATE TABLE IF NOT EXISTS key_source (
    key TEXT PRIMARY KEY,
    url TEXT NOT NULL,
    class TEXT NOT NULL DEFAULT '',
    web_addr TEXT NOT NULL DEFAULT '',
    ygg_addr TEXT NOT NULL DEFAULT '',
    origin_url TEXT NOT NULL DEFAULT '',
    listing_mode TEXT NOT NULL DEFAULT '' CHECK(listing_mode IN ('', 'releases', 'tags')),
    bound_ts TEXT NOT NULL
) STRICT;

-- Покрывает keyset и page ORDER BY (key, upstream_seq DESC, version DESC) полностью — без residual-сортировки.
CREATE INDEX IF NOT EXISTS idx_versions_key_seq ON versions(key, upstream_seq DESC, version DESC);
CREATE INDEX IF NOT EXISTS idx_versions_ingest ON versions(ingest_ts, key, version);
CREATE INDEX IF NOT EXISTS idx_versions_tree ON versions(tree_hash);
CREATE INDEX IF NOT EXISTS idx_artifacts_lookup ON materialized_artifacts(key, version, materializer_id);
CREATE INDEX IF NOT EXISTS idx_blob_refs_count ON blob_refs(refcount, size_bytes);
CREATE INDEX IF NOT EXISTS idx_history_feed ON history_events(event_ts DESC, id DESC);
CREATE INDEX IF NOT EXISTS idx_history_kv ON history_events(key, version, event_type, id);

INSERT OR IGNORE INTO globals(name, value) VALUES
    ('pebble_key_format', 'tag_hash24'),
    ('hash_size_bytes', '24');

-- +goose Down
DROP TABLE IF EXISTS key_source;
DROP TABLE IF EXISTS history_events;
DROP TABLE IF EXISTS blob_refs;
DROP TABLE IF EXISTS materialized_artifacts;
DROP TABLE IF EXISTS rewrite_set;
DROP TABLE IF EXISTS detection;
DROP TABLE IF EXISTS versions;
DROP TABLE IF EXISTS globals;
