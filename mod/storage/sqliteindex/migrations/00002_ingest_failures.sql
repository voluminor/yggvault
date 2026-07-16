-- +goose Up
CREATE TABLE IF NOT EXISTS ingest_failures (
    key TEXT NOT NULL,
    version TEXT NOT NULL,
    ref_sha TEXT NOT NULL,
    code TEXT NOT NULL,
    message TEXT NOT NULL,
    policy INTEGER NOT NULL CHECK(policy >= 0),
    first_ts TEXT NOT NULL,
    last_ts TEXT NOT NULL,
    count INTEGER NOT NULL CHECK(count >= 1),
    PRIMARY KEY (key, version)
) STRICT;

-- +goose Down
DROP TABLE IF EXISTS ingest_failures;
