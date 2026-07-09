package server

import (
	"context"
	"fmt"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/cache"
)

// // // // // // // // // //

func storeForTest(t *testing.T, serverObj *Obj) *fakeStoreObj {
	t.Helper()
	storeObj, ok := serverObj.funcImplObj.deps.Storage.(*fakeStoreObj)
	if !ok {
		t.Fatalf("server storage is not *fakeStoreObj")
	}
	return storeObj
}

// // // // // // // // // //

func TestCacheDedupesBuild(t *testing.T) {
	serverObj, lc := newTestServer(t, withCache())
	ts := httptest.NewServer(serverObj.Handler(lc))
	defer ts.Close()
	storeObj := storeForTest(t, serverObj)

	resp1, body1 := doGET(t, ts, "/lib/list", nil)
	if resp1.StatusCode != 200 {
		t.Fatalf("first /lib/list: status %d", resp1.StatusCode)
	}
	callsAfterFirst := storeObj.keysetCalls.Load()
	if callsAfterFirst == 0 {
		t.Fatalf("builder did not scan storage on cache-miss")
	}

	resp2, body2 := doGET(t, ts, "/lib/list", nil)
	if resp2.StatusCode != 200 {
		t.Fatalf("second /lib/list: status %d", resp2.StatusCode)
	}
	if string(body2) != string(body1) {
		t.Fatalf("cached body differs: %q vs %q", body1, body2)
	}
	if got := storeObj.keysetCalls.Load(); got != callsAfterFirst {
		t.Fatalf("cache-hit still scanned storage: %d -> %d", callsAfterFirst, got)
	}
}

func TestCacheStillCachesWhenRescanOverdue(t *testing.T) {
	serverObj, lc := newTestServer(t, withCache())
	ts := httptest.NewServer(serverObj.Handler(lc))
	defer ts.Close()
	storeObj := storeForTest(t, serverObj)

	// A zero LastRescan makes SecondsToNextRescan return 0; without the floor the byte cache would be a
	// no-op and every request would rebuild feeds/sitemaps exactly while the node is degraded.
	stateObj := stateFor(t, serverObj)
	stateObj.lastRescan = time.Time{}

	resp1, body1 := doGET(t, ts, "/lib/list", nil)
	if resp1.StatusCode != 200 {
		t.Fatalf("first /lib/list: status %d", resp1.StatusCode)
	}
	callsAfterFirst := storeObj.keysetCalls.Load()
	if callsAfterFirst == 0 {
		t.Fatalf("builder did not scan storage on cache-miss")
	}

	resp2, body2 := doGET(t, ts, "/lib/list", nil)
	if resp2.StatusCode != 200 {
		t.Fatalf("second /lib/list: status %d", resp2.StatusCode)
	}
	if string(body2) != string(body1) {
		t.Fatalf("cached body differs: %q vs %q", body1, body2)
	}
	if got := storeObj.keysetCalls.Load(); got != callsAfterFirst {
		t.Fatalf("overdue rescan disabled caching: re-scanned storage %d -> %d", callsAfterFirst, got)
	}
}

func TestCacheInvalidatesOnFreshness(t *testing.T) {
	serverObj, lc := newTestServer(t, withCache())
	ts := httptest.NewServer(serverObj.Handler(lc))
	defer ts.Close()
	storeObj := storeForTest(t, serverObj)

	doGET(t, ts, "/lib/list", nil)
	callsAfterFirst := storeObj.keysetCalls.Load()

	stateObj := stateFor(t, serverObj)
	keyStateObj := stateObj.keyStates["lib"]
	keyStateObj.VersionCount = 2
	stateObj.keyStates["lib"] = keyStateObj

	doGET(t, ts, "/lib/list", nil)
	if got := storeObj.keysetCalls.Load(); got == callsAfterFirst {
		t.Fatalf("freshness changed but cache served stale (no rebuild): calls stayed %d", got)
	}
}

func TestObjCacheDedupes(t *testing.T) {
	serverObj, lc := newTestServer(t, withCache())
	ts := httptest.NewServer(serverObj.Handler(lc))
	defer ts.Close()
	storeObj := storeForTest(t, serverObj)

	for _, pathText := range []string{"/lib/list/full", "/p2/vendor/pkg.json", "/lib/@latest"} {
		resp1, body1 := doGET(t, ts, pathText, nil)
		if resp1.StatusCode != 200 {
			t.Fatalf("first %s: status %d", pathText, resp1.StatusCode)
		}
		callsAfterFirst := storeObj.keysetCalls.Load()

		resp2, body2 := doGET(t, ts, pathText, nil)
		if resp2.StatusCode != 200 {
			t.Fatalf("second %s: status %d", pathText, resp2.StatusCode)
		}
		if string(body2) != string(body1) {
			t.Fatalf("%s cached body differs", pathText)
		}
		if got := storeObj.keysetCalls.Load(); got != callsAfterFirst {
			t.Fatalf("%s cache-hit re-scanned storage: %d -> %d", pathText, callsAfterFirst, got)
		}
	}
}

func TestObjCacheBuildGateCapsDifferentKeys(t *testing.T) {
	cacheObj := newObjCache(cache.NewBuildGate(2))
	var (
		currentObj atomic.Int64
		maxObj     atomic.Int64
	)
	buildFn := func(_ context.Context) (any, error) {
		nowValue := currentObj.Add(1)
		for {
			maxValue := maxObj.Load()
			if nowValue <= maxValue || maxObj.CompareAndSwap(maxValue, nowValue) {
				break
			}
		}
		time.Sleep(40 * time.Millisecond)
		currentObj.Add(-1)
		return "built", nil
	}

	const countValue = 10
	var wgObj sync.WaitGroup
	wgObj.Add(countValue)
	for i := 0; i < countValue; i++ {
		go func(idx int) {
			defer wgObj.Done()
			keyText := fmt.Sprintf("cold-%02d", idx)
			if _, err := cacheObj.getOrBuild(context.Background(), keyText, time.Minute, buildFn); err != nil {
				t.Errorf("getOrBuild(%s): %v", keyText, err)
			}
		}(i)
	}
	wgObj.Wait()

	if got := maxObj.Load(); got > 2 {
		t.Fatalf("max concurrent builds=%d want <= 2", got)
	}
}
