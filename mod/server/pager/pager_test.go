package pager

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type fakeStoreObj struct {
	versions []core.VersionObj
	listErr  error
	calls    int
}

func (f *fakeStoreObj) ListVersionsKeyset(_ context.Context, _ string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	f.calls++
	if f.listErr != nil {
		return nil, f.listErr
	}
	start := 0
	if afterVersion != "" {
		for i := range f.versions {
			if f.versions[i].UpstreamSeq == afterSeq && f.versions[i].Version == afterVersion {
				start = i + 1
				break
			}
		}
	}
	return f.versions[start:min(start+limit, len(f.versions))], nil
}

// // // // // // // // // //

func makeVersions(n int) []core.VersionObj {
	arr := make([]core.VersionObj, n)
	for i := range arr {
		arr[i] = core.VersionObj{Version: "v0.0." + strconv.Itoa(i), UpstreamSeq: int64(1_000_000 - i), IngestTS: time.Unix(int64(1_000_000-i), 0)}
	}
	return arr
}

// // // // // // // // // //

func TestEachVersionMultiPageOrder(t *testing.T) {
	store := &fakeStoreObj{versions: makeVersions(PageSize + 6)}
	var seen []string
	err := EachVersion(context.Background(), store, "k", func(versionObj core.VersionObj) (bool, error) {
		seen = append(seen, versionObj.Version)
		return false, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(seen) != PageSize+6 {
		t.Fatalf("seen %d versions, want %d", len(seen), PageSize+6)
	}
	if store.calls < 2 {
		t.Fatalf("expected multi-page (≥2 calls), got %d", store.calls)
	}
	for i, v := range seen {
		if want := "v0.0." + strconv.Itoa(i); v != want {
			t.Fatalf("order broken at %d: got %q want %q", i, v, want)
		}
	}
}

func TestEachVersionStop(t *testing.T) {
	store := &fakeStoreObj{versions: makeVersions(10)}
	count := 0
	err := EachVersion(context.Background(), store, "k", func(core.VersionObj) (bool, error) {
		count++
		return count == 3, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if count != 3 {
		t.Fatalf("stop ignored: visited %d, want 3", count)
	}
}

func TestEachVersionCallbackError(t *testing.T) {
	store := &fakeStoreObj{versions: makeVersions(10)}
	sentinel := errors.New("boom")
	count := 0
	err := EachVersion(context.Background(), store, "k", func(core.VersionObj) (bool, error) {
		count++
		if count == 2 {
			return false, sentinel
		}
		return false, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("callback error not propagated: %v", err)
	}
	if count != 2 {
		t.Fatalf("iteration not halted on error: visited %d, want 2", count)
	}
}

func TestEachVersionStoreError(t *testing.T) {
	sentinel := errors.New("store down")
	store := &fakeStoreObj{listErr: sentinel}
	called := false
	err := EachVersion(context.Background(), store, "k", func(core.VersionObj) (bool, error) {
		called = true
		return false, nil
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("store error not propagated: %v", err)
	}
	if called {
		t.Fatal("callback must not run on store error")
	}
}

func TestEachVersionEmpty(t *testing.T) {
	store := &fakeStoreObj{versions: nil}
	called := false
	err := EachVersion(context.Background(), store, "k", func(core.VersionObj) (bool, error) {
		called = true
		return false, nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if called {
		t.Fatal("callback must not run for empty key")
	}
}
