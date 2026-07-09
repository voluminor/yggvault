package source

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type roundTripFuncObj func(*http.Request) (*http.Response, error)

func (fnObj roundTripFuncObj) RoundTrip(reqObj *http.Request) (*http.Response, error) {
	return fnObj(reqObj)
}

func emptyResponseObj(statusValue int) *http.Response {
	return &http.Response{
		StatusCode: statusValue,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader("")),
	}
}

// // // // // // // // // //

func TestHostLimiterPacesConcurrentRequests(t *testing.T) {
	obj := &Obj{
		upstreamLimiter: newHostLimiterObj(stconf.SourceRateLimitObj{RequestsPerSecond: 20, Burst: 1}),
	}
	clientObj := &http.Client{Transport: obj.withLimit(roundTripFuncObj(func(_ *http.Request) (*http.Response, error) {
		return emptyResponseObj(http.StatusNoContent), nil
	}))}

	const requestCount = 8
	startCh := make(chan struct{})
	errCh := make(chan error, requestCount)
	var wg sync.WaitGroup
	wg.Add(requestCount)
	startObj := time.Now()
	for i := 0; i < requestCount; i++ {
		go func(idx int) {
			defer wg.Done()
			<-startCh
			reqObj, err := http.NewRequestWithContext(context.Background(), http.MethodGet, fmt.Sprintf("http://example.com/%d", idx), nil)
			if err != nil {
				errCh <- err
				return
			}
			respObj, err := clientObj.Do(reqObj)
			if err == nil {
				_ = respObj.Body.Close()
			}
			errCh <- err
		}(i)
	}
	close(startCh)
	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			t.Fatalf("request returned error: %v", err)
		}
	}
	minElapsedObj := time.Duration(requestCount-1) * time.Second / 20
	if elapsedObj := time.Since(startObj); elapsedObj < minElapsedObj-30*time.Millisecond {
		t.Fatalf("elapsed=%v, want at least about %v", elapsedObj, minElapsedObj)
	}
}

func TestLimitTransportPassthroughWhenDisabled(t *testing.T) {
	var callsObj atomic.Int64
	baseObj := roundTripFuncObj(func(_ *http.Request) (*http.Response, error) {
		callsObj.Add(1)
		return emptyResponseObj(http.StatusNoContent), nil
	})
	obj := &Obj{}
	clientObj := &http.Client{Transport: obj.withLimit(baseObj)}

	respObj, err := clientObj.Get("http://example.com/passthrough")
	if err != nil {
		t.Fatalf("Get returned error: %v", err)
	}
	_ = respObj.Body.Close()
	if got := callsObj.Load(); got != 1 {
		t.Fatalf("base transport calls=%d want 1", got)
	}
}

func TestLimitTransportCancelDuringWait(t *testing.T) {
	var callsObj atomic.Int64
	obj := &Obj{
		upstreamLimiter: newHostLimiterObj(stconf.SourceRateLimitObj{RequestsPerSecond: 1, Burst: 1}),
	}
	clientObj := &http.Client{Transport: obj.withLimit(roundTripFuncObj(func(_ *http.Request) (*http.Response, error) {
		callsObj.Add(1)
		return emptyResponseObj(http.StatusNoContent), nil
	}))}

	respObj, err := clientObj.Get("http://example.com/first")
	if err != nil {
		t.Fatalf("first Get returned error: %v", err)
	}
	_ = respObj.Body.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	reqObj, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://example.com/second", nil)
	if err != nil {
		t.Fatalf("NewRequestWithContext returned error: %v", err)
	}
	if _, err = clientObj.Do(reqObj); err == nil {
		t.Fatal("second request returned nil error")
	}
	if got := callsObj.Load(); got != 1 {
		t.Fatalf("base transport calls=%d want 1; wait must fail before RoundTrip", got)
	}
}

func TestFetchArchiveLimiterAppliesBeforeRetries(t *testing.T) {
	var (
		muObj    sync.Mutex
		timeArr  []time.Time
		callsObj atomic.Int64
	)
	obj := &Obj{
		downloadSem:     make(chan struct{}, 1),
		maxArchiveSize:  cAbsArchiveCap,
		requestTimeout:  time.Second,
		upstreamLimiter: newHostLimiterObj(stconf.SourceRateLimitObj{RequestsPerSecond: 20, Burst: 1}),
		retry: retryObj{
			maxAttempts:    3,
			backoffInitial: time.Millisecond,
			backoffMax:     time.Millisecond,
		},
	}
	obj.downloadClient = &http.Client{Transport: obj.withLimit(roundTripFuncObj(func(_ *http.Request) (*http.Response, error) {
		callsObj.Add(1)
		muObj.Lock()
		timeArr = append(timeArr, time.Now())
		muObj.Unlock()
		return emptyResponseObj(http.StatusInternalServerError), nil
	}))}

	_, err := obj.FetchArchive(context.Background(), GitFetchRequestObj{
		Key:        "core-lib",
		Version:    "v1.0.0",
		ArchiveURL: "http://example.com/archive.zip",
		Format:     cFormatZip,
		DestDir:    t.TempDir(),
	})
	if err == nil {
		t.Fatal("FetchArchive returned nil error")
	}
	if got := callsObj.Load(); got != 3 {
		t.Fatalf("transport calls=%d want 3", got)
	}

	muObj.Lock()
	defer muObj.Unlock()
	for i := 1; i < len(timeArr); i++ {
		if deltaObj := timeArr[i].Sub(timeArr[i-1]); deltaObj < 40*time.Millisecond {
			t.Fatalf("retry %d interval=%v, want limiter-paced interval", i, deltaObj)
		}
	}
}
