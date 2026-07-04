package server

import (
	"io"
	"net/http/httptest"
	"sort"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// // // // // // // // // //

type loadResultObj struct {
	rps      float64
	mbps     float64
	p50, p99 time.Duration
	count    int64
}

func measureLoad(ts *httptest.Server, pathText string, workers int, dur time.Duration) loadResultObj {
	client := ts.Client()
	deadline := time.Now().Add(dur)
	perWorker := make([][]time.Duration, workers)
	var totalReq, totalBytes int64
	var wgObj sync.WaitGroup
	for w := 0; w < workers; w++ {
		wgObj.Add(1)
		go func(idx int) {
			defer wgObj.Done()
			latArr := make([]time.Duration, 0, 8192)
			for time.Now().Before(deadline) {
				startTime := time.Now()
				respObj, err := client.Get(ts.URL + pathText)
				if err != nil {
					continue
				}
				nBytes, _ := io.Copy(io.Discard, respObj.Body)
				respObj.Body.Close()
				if respObj.StatusCode != 200 {
					continue
				}
				latArr = append(latArr, time.Since(startTime))
				atomic.AddInt64(&totalReq, 1)
				atomic.AddInt64(&totalBytes, nBytes)
			}
			perWorker[idx] = latArr
		}(w)
	}
	wgObj.Wait()

	allArr := make([]time.Duration, 0, totalReq)
	for _, latArr := range perWorker {
		allArr = append(allArr, latArr...)
	}
	sort.Slice(allArr, func(i, j int) bool { return allArr[i] < allArr[j] })
	resObj := loadResultObj{count: totalReq, rps: float64(totalReq) / dur.Seconds(), mbps: float64(totalBytes) / 1e6 / dur.Seconds()}
	if len(allArr) > 0 {
		resObj.p50 = allArr[len(allArr)*50/100]
		resObj.p99 = allArr[min(len(allArr)-1, len(allArr)*99/100)]
	}
	return resObj
}

func TestLiveServingNumbers(t *testing.T) {
	if testing.Short() {
		t.Skip("live serving numbers: skipped under -short")
	}
	const (
		nVersions = 1000
		workers   = 16
		dur       = 1500 * time.Millisecond
	)

	srvCache, lcCache := newTestServer(t, withCache(), withVersions(nVersions))
	tsCache := httptest.NewServer(srvCache.Handler(lcCache))
	defer tsCache.Close()

	srvNoCache, lcNoCache := newTestServer(t, withVersions(nVersions))
	tsNoCache := httptest.NewServer(srvNoCache.Handler(lcNoCache))
	defer tsNoCache.Close()

	t.Logf("=== LIVE serving (in-process, %d versions, %d workers, %s/measurement) ===", nVersions, workers, dur)
	report := func(name string, r loadResultObj) {
		t.Logf("%-40s RPS=%9.0f  p50=%8s  p99=%8s  (%d req, %.1f MB/s)",
			name, r.rps, r.p50.Round(time.Microsecond), r.p99.Round(time.Microsecond), r.count, r.mbps)
	}

	type rowObj struct {
		name string
		path string
	}
	for _, rw := range []rowObj{
		{"catalog.json (cross-key)", "/catalog.json"},
		{"releases.json (per-key page)", "/lib/releases.json"},
		{"list/full (1000 ver)", "/lib/list/full"},
	} {
		on := measureLoad(tsCache, rw.path, workers, dur)
		off := measureLoad(tsNoCache, rw.path, workers, dur)
		report(rw.name+"  BYTE-cache HIT", on)
		report(rw.name+"  NO cache     ", off)
		t.Logf("    └─ speedup %.2fx ; p99 %s → %s\n",
			on.rps/off.rps, off.p99.Round(time.Microsecond), on.p99.Round(time.Microsecond))
	}
}
