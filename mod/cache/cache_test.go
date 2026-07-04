package cache

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func newTestCache(maxBytes uint64) *Obj {
	return New(stconf.CacheObj{MetadataMaxSize: stconf.SizeObj(maxBytes)})
}

func TestGetSetHitMiss(t *testing.T) {
	obj := newTestCache(1 << 20)
	if _, ok := obj.Get("k"); ok {
		t.Fatal("expected miss on empty cache")
	}
	obj.Set("k", EntryObj{Payload: []byte("v"), ETag: `"e"`}, time.Minute)
	got, ok := obj.Get("k")
	if !ok || string(got.Payload) != "v" || got.ETag != `"e"` {
		t.Fatalf("unexpected get: %+v ok=%v", got, ok)
	}
	statsObj := obj.Stats()
	if statsObj.Hits != 1 || statsObj.Misses != 1 || statsObj.Entries != 1 {
		t.Fatalf("unexpected stats: %+v", statsObj)
	}
}

func TestTTLExpiry(t *testing.T) {
	obj := newTestCache(1 << 20)
	obj.Set("k", EntryObj{Payload: []byte("v")}, 20*time.Millisecond)
	if _, ok := obj.Get("k"); !ok {
		t.Fatal("expected hit before expiry")
	}
	time.Sleep(40 * time.Millisecond)
	if _, ok := obj.Get("k"); ok {
		t.Fatal("expected miss after expiry")
	}
}

func TestZeroTTLNotCached(t *testing.T) {
	obj := newTestCache(1 << 20)
	obj.Set("k", EntryObj{Payload: []byte("v")}, 0)
	if _, ok := obj.Get("k"); ok {
		t.Fatal("ttl<=0 must not cache")
	}
}

func TestSizeCapEviction(t *testing.T) {
	const budget = 4000
	obj := newTestCache(budget)
	for i := 0; i < 500; i++ {
		obj.Set(fmt.Sprintf("key-%04d", i), EntryObj{Payload: make([]byte, 100)}, time.Minute)
	}
	statsObj := obj.Stats()
	if statsObj.Bytes > budget {
		t.Fatalf("bytes=%d exceed budget=%d", statsObj.Bytes, budget)
	}
	if statsObj.Evictions == 0 {
		t.Fatal("expected evictions under size pressure")
	}
}

func TestGetOrBuildSingleflightDedup(t *testing.T) {
	obj := newTestCache(1 << 20)
	var buildCount atomic.Int64
	buildFn := func(_ context.Context) (EntryObj, error) {
		buildCount.Add(1)
		time.Sleep(50 * time.Millisecond)
		return EntryObj{Payload: []byte("built")}, nil
	}

	const n = 32
	var wgObj sync.WaitGroup
	wgObj.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wgObj.Done()
			got, _, err := obj.GetOrBuild(context.Background(), "cold", time.Minute, buildFn)
			if err != nil || string(got.Payload) != "built" {
				t.Errorf("GetOrBuild: got=%q err=%v", got.Payload, err)
			}
		}()
	}
	wgObj.Wait()

	if buildCount.Load() != 1 {
		t.Fatalf("buildFn called %d times, want 1 (singleflight dedup)", buildCount.Load())
	}
	if _, ok := obj.Get("cold"); !ok {
		t.Fatal("built value must be cached")
	}
	if obj.Stats().Shared == 0 {
		t.Fatal("expected shared singleflight joins")
	}
}

func TestGetOrBuildErrorNotCached(t *testing.T) {
	obj := newTestCache(1 << 20)
	buildErr := errors.New("boom")
	_, _, err := obj.GetOrBuild(context.Background(), "k", time.Minute, func(_ context.Context) (EntryObj, error) {
		return EntryObj{}, buildErr
	})
	if !errors.Is(err, buildErr) {
		t.Fatalf("expected build error, got %v", err)
	}
	if _, ok := obj.Get("k"); ok {
		t.Fatal("failed build must not be cached")
	}
}

func TestClear(t *testing.T) {
	obj := newTestCache(1 << 20)
	obj.Set("a", EntryObj{Payload: []byte("1")}, time.Minute)
	obj.Set("b", EntryObj{Payload: []byte("2")}, time.Minute)
	obj.Clear()
	if obj.Stats().Entries != 0 || obj.Stats().Bytes != 0 {
		t.Fatalf("clear left state: %+v", obj.Stats())
	}
	if _, ok := obj.Get("a"); ok {
		t.Fatal("entry survived clear")
	}
}

func TestSecondsToNextRescan(t *testing.T) {
	now := time.Date(2026, 6, 25, 12, 0, 0, 0, time.UTC)
	last := now.Add(-2 * time.Hour)
	if got := SecondsToNextRescan(last, 6*time.Hour, now); got != 4*time.Hour {
		t.Fatalf("remaining=%v want 4h", got)
	}
	if got := SecondsToNextRescan(last, time.Hour, now); got != 0 {
		t.Fatalf("overdue must be 0, got %v", got)
	}
	if got := SecondsToNextRescan(time.Time{}, time.Hour, now); got != 0 {
		t.Fatalf("zero lastRescan must be 0, got %v", got)
	}
}
