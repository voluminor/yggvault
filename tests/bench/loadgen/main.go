package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

// // // // // // // // // //

type statObj struct {
	mu      sync.Mutex
	latNs   []int64
	reqs    int64
	bytes   int64
	errs    int64
	statusN map[int]int64
}

// // // // // // // // // // helpers

func percentile(sorted []int64, p float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(p / 100 * float64(len(sorted)-1))
	return float64(sorted[idx]) / 1e6
}

// // // // // // // // // // main

func main() {
	url := flag.String("url", "", "target URL")
	conc := flag.Int("c", 64, "concurrent connections")
	durSec := flag.Int("d", 20, "duration seconds")
	warmSec := flag.Int("warm", 2, "warmup seconds (excluded)")
	flag.Parse()
	if *url == "" {
		fmt.Fprintln(os.Stderr, "need -url")
		os.Exit(2)
	}

	transport := &http.Transport{
		MaxIdleConns:        *conc * 2,
		MaxIdleConnsPerHost: *conc * 2,
		MaxConnsPerHost:     *conc * 2,
		IdleConnTimeout:     30 * time.Second,
	}
	client := &http.Client{Transport: transport, Timeout: 30 * time.Second}

	st := &statObj{statusN: map[int]int64{}, latNs: make([]int64, 0, 1<<16)}
	var recording atomic.Bool
	end := time.Now().Add(time.Duration(*warmSec+*durSec) * time.Second)
	recStart := time.Now().Add(time.Duration(*warmSec) * time.Second)

	var wg sync.WaitGroup
	for i := 0; i < *conc; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for time.Now().Before(end) {
				t0 := time.Now()
				resp, err := client.Get(*url)
				if err != nil {
					if recording.Load() {
						atomic.AddInt64(&st.errs, 1)
					}
					continue
				}
				n, _ := io.Copy(io.Discard, resp.Body)
				resp.Body.Close()
				lat := time.Since(t0).Nanoseconds()
				if recording.Load() {
					st.mu.Lock()
					st.reqs++
					st.bytes += n
					st.statusN[resp.StatusCode]++
					st.latNs = append(st.latNs, lat)
					st.mu.Unlock()
				}
			}
		}()
	}
	go func() { time.Sleep(time.Until(recStart)); recording.Store(true) }()
	wg.Wait()

	sort.Slice(st.latNs, func(i, j int) bool { return st.latNs[i] < st.latNs[j] })
	dur := float64(*durSec)
	fmt.Printf("url=%s c=%d d=%ds\n", *url, *conc, *durSec)
	fmt.Printf("reqs=%d  rps=%.0f  MB/s=%.1f  errs=%d  status=%v\n",
		st.reqs, float64(st.reqs)/dur, float64(st.bytes)/1e6/dur, st.errs, st.statusN)
	fmt.Printf("latency ms: p50=%.2f  p90=%.2f  p99=%.2f  max=%.2f\n",
		percentile(st.latNs, 50), percentile(st.latNs, 90), percentile(st.latNs, 99),
		percentile(st.latNs, 100))
}
