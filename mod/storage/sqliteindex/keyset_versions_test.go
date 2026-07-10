package sqliteindex

import (
	"context"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func seedVersionSeq(t *testing.T, indexObj *Obj, key, version string, upstreamSeq int64) {
	t.Helper()
	hashObj := core.HashBytes([]byte(key + version))
	_, err := indexObj.dbObj.ExecContext(context.Background(),
		"INSERT INTO versions(key, version, source_hash, source_size_bytes, tree_hash, ingest_ts, upstream_seq) VALUES (?, ?, ?, ?, ?, ?, ?)",
		key, version, hashObj.BytesCopy(), 1, hashObj.BytesCopy(), core.FormatTime(time.Now().UTC()), upstreamSeq)
	if err != nil {
		t.Fatalf("seed version: %v", err)
	}
}

// TestListVersionsKeysetPagination checks display order by upstream_seq DESC.
// Semver and string order are intentionally inverted relative to source positions.
func TestListVersionsKeysetPagination(t *testing.T) {
	ctx := context.Background()
	indexObj, err := Open(ctx, tempSqlitePath(t))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = indexObj.Close() })

	type rowObj struct {
		version string
		seq     int64
	}
	rowArr := []rowObj{
		{"v0.9.0", 30},
		{"v0.10.0", 20},
		{"v1.0.0", 10}, {"v0.0.5", 10},
		{"v2.0.0", 5},
	}
	for _, r := range rowArr {
		seedVersionSeq(t, indexObj, "k", r.version, r.seq)
	}

	wantArr := []string{"v0.9.0", "v0.10.0", "v1.0.0", "v0.0.5", "v2.0.0"}

	var gotArr []core.VersionObj
	var afterSeq int64
	var afterVersion string
	const limit = 2
	for {
		pageArr, pageErr := indexObj.ListVersionsKeyset(ctx, "k", false, afterSeq, afterVersion, limit)
		if pageErr != nil {
			t.Fatalf("keyset: %v", pageErr)
		}
		gotArr = append(gotArr, pageArr...)
		if len(pageArr) < limit {
			break
		}
		lastObj := pageArr[len(pageArr)-1]
		afterSeq = lastObj.UpstreamSeq
		afterVersion = lastObj.Version
	}

	if len(gotArr) != len(wantArr) {
		t.Fatalf("got %d versions, want %d", len(gotArr), len(wantArr))
	}
	for i := range wantArr {
		if gotArr[i].Version != wantArr[i] {
			t.Fatalf("pos %d: got %q, want %q (order or coverage broken)", i, gotArr[i].Version, wantArr[i])
		}
	}
}
