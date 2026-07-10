package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
)

// // // // // // // // // //

const (
	cMiB = 1 << 20
)

var (
	compressionArr = []string{"none", "snappy", "minlz", "fast", "fastest", "balanced", "good", "zstd"}
	memtableMBArr  = []uint64{16, 64, 256}
	cacheMBArr     = []uint64{8, 128, 512}
)

// // // // // // // // // //

type blobObj struct {
	hash core.HashObj
	data []byte
}

type resultObj struct {
	Axis         string  `json:"axis"`
	Label        string  `json:"label"`
	Compression  string  `json:"compression"`
	MemtableMB   uint64  `json:"memtable_mb"`
	CacheMB      uint64  `json:"cache_mb"`
	Blobs        int     `json:"blobs"`
	RawMB        float64 `json:"raw_mb"`
	WriteCPUSec  float64 `json:"write_cpu_s"`
	WriteAllocMB float64 `json:"write_alloc_mb"`
	DiskLiveMB   float64 `json:"disk_live_mb"`
	DiskDuMB     float64 `json:"disk_du_mb"`
	ReadColdSec  float64 `json:"read_cold_s"`
	ReadWarmSec  float64 `json:"read_warm_s"`
	RSSMB        float64 `json:"rss_mb"`
	Reps         int     `json:"reps"`
}

func envInt(name string, def int) int {
	if v := os.Getenv(name); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return def
}

func totalAlloc() uint64 {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)
	return ms.TotalAlloc
}

func rssBytes() float64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return 0
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "VmRSS:") {
			f := strings.Fields(line)
			if len(f) >= 2 {
				kb, _ := strconv.ParseFloat(f[1], 64)
				return kb * 1024
			}
		}
	}
	return 0
}

func duBytes(root string) float64 {
	var total int64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if info, e := d.Info(); e == nil {
			total += info.Size()
		}
		return nil
	})
	return float64(total)
}

func median(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	cp := append([]float64(nil), values...)
	sort.Float64s(cp)
	return cp[len(cp)/2]
}

func loadDataset(root string, fileCap, rawCap uint64, maxBlobs int) ([]blobObj, uint64) {
	seen := make(map[core.HashObj]struct{})
	var out []blobObj
	var raw uint64
	_ = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.Contains(p, string(os.PathSeparator)+"cache"+string(os.PathSeparator)) {
			return nil
		}
		if raw >= rawCap || (maxBlobs > 0 && len(out) >= maxBlobs) {
			return filepath.SkipAll
		}
		info, e := d.Info()
		if e != nil || info.Size() == 0 || uint64(info.Size()) > fileCap {
			return nil
		}
		data, e := os.ReadFile(p)
		if e != nil || len(data) == 0 {
			return nil
		}
		h := core.HashBytes(data)
		if _, dup := seen[h]; dup {
			return nil
		}
		seen[h] = struct{}{}
		out = append(out, blobObj{hash: h, data: data})
		raw += uint64(len(data))
		return nil
	})
	return out, raw
}

func runOnce(blobs []blobObj, comp string, memMB, cacheMB uint64) resultObj {
	dir, err := os.MkdirTemp("", "benchpebble-")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(dir)

	tuning := pebblestore.TuningObj{Compression: comp, MemTableBytes: memMB * cMiB, BlockCacheBytes: cacheMB * cMiB}

	runtime.GC()
	debug.FreeOSMemory()
	cpu0, alloc0 := cpuSeconds(), totalAlloc()

	st, err := pebblestore.OpenWithTuning(dir, tuning)
	if err != nil {
		panic(err)
	}
	for i := range blobs {
		if err = st.PutBlob(blobs[i].hash, blobs[i].data); err != nil {
			panic(err)
		}
	}
	_ = st.CompactAll(context.Background())
	wCPU := cpuSeconds() - cpu0
	wAlloc := float64(totalAlloc()-alloc0) / cMiB
	diskLive := float64(st.DiskBytes()) / cMiB
	_ = st.Close()

	diskDu := duBytes(dir) / cMiB

	st2, err := pebblestore.OpenWithTuning(dir, tuning)
	if err != nil {
		panic(err)
	}
	noop := func([]byte) error { return nil }
	cpuC := cpuSeconds()
	for i := range blobs {
		_ = st2.UseBlob(blobs[i].hash, noop)
	}
	rCold := cpuSeconds() - cpuC
	cpuW := cpuSeconds()
	for i := range blobs {
		_ = st2.UseBlob(blobs[i].hash, noop)
	}
	rWarm := cpuSeconds() - cpuW
	rss := rssBytes() / cMiB
	_ = st2.Close()

	return resultObj{
		Compression: comp, MemtableMB: memMB, CacheMB: cacheMB,
		Blobs: len(blobs), WriteCPUSec: wCPU, WriteAllocMB: wAlloc,
		DiskLiveMB: diskLive, DiskDuMB: diskDu, ReadColdSec: rCold, ReadWarmSec: rWarm, RSSMB: rss,
	}
}

func runMedian(axis, label string, blobs []blobObj, comp string, memMB, cacheMB uint64, reps int) resultObj {
	var wCPU, wAlloc, dLive, dDu, rCold, rWarm, rss []float64
	var last resultObj
	for r := 0; r < reps; r++ {
		last = runOnce(blobs, comp, memMB, cacheMB)
		wCPU = append(wCPU, last.WriteCPUSec)
		wAlloc = append(wAlloc, last.WriteAllocMB)
		dLive = append(dLive, last.DiskLiveMB)
		dDu = append(dDu, last.DiskDuMB)
		rCold = append(rCold, last.ReadColdSec)
		rWarm = append(rWarm, last.ReadWarmSec)
		rss = append(rss, last.RSSMB)
	}
	last.Axis, last.Label, last.Reps = axis, label, reps
	last.RawMB = float64(rawBytesOf(blobs)) / cMiB
	last.WriteCPUSec, last.WriteAllocMB = median(wCPU), median(wAlloc)
	last.DiskLiveMB, last.DiskDuMB = median(dLive), median(dDu)
	last.ReadColdSec, last.ReadWarmSec, last.RSSMB = median(rCold), median(rWarm), median(rss)
	return last
}

func rawBytesOf(blobs []blobObj) uint64 {
	var n uint64
	for i := range blobs {
		n += uint64(len(blobs[i].data))
	}
	return n
}

func filterBySize(blobs []blobObj, lo, hi int) []blobObj {
	var out []blobObj
	for i := range blobs {
		n := len(blobs[i].data)
		if n >= lo && n < hi {
			out = append(out, blobs[i])
		}
	}
	return out
}

func main() {
	dataset := os.Getenv("DATASET")
	if dataset == "" {
		dataset = "/dataset"
	}
	reps := envInt("REPS", 3)
	fileCap := uint64(envInt("FILE_CAP_MB", 20)) * cMiB
	rawCap := uint64(envInt("RAW_CAP_MB", 150)) * cMiB
	maxBlobs := envInt("BLOB_CAP", 2000)

	axes := map[string]bool{"compression": true, "memtable": true, "cache": true, "blobsize": true}
	if sel := os.Getenv("AXES"); sel != "" {
		for k := range axes {
			axes[k] = false
		}
		for _, a := range strings.Split(sel, ",") {
			axes[strings.TrimSpace(a)] = true
		}
	}

	blobs, raw := loadDataset(dataset, fileCap, rawCap, maxBlobs)
	if len(blobs) == 0 {
		fmt.Fprintln(os.Stderr, "empty dataset at", dataset)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "dataset: %d blobs, %.1f MB raw, reps=%d (cgo build decides zstd codec)\n",
		len(blobs), float64(raw)/cMiB, reps)

	var results []resultObj

	if axes["compression"] {
		for _, comp := range compressionArr {
			results = append(results, runMedian("compression", comp, blobs, comp, 64, 128, reps))
			fmt.Fprintf(os.Stderr, "  compression=%-9s done\n", comp)
		}
	}
	if axes["memtable"] {
		for _, m := range memtableMBArr {
			results = append(results, runMedian("memtable", strconv.FormatUint(m, 10)+"MB", blobs, "snappy", m, 128, reps))
			fmt.Fprintf(os.Stderr, "  memtable=%dMB done\n", m)
		}
	}
	if axes["cache"] {
		for _, c := range cacheMBArr {
			results = append(results, runMedian("cache", strconv.FormatUint(c, 10)+"MB", blobs, "snappy", 64, c, reps))
			fmt.Fprintf(os.Stderr, "  cache=%dMB done\n", c)
		}
	}
	buckets := []struct {
		name   string
		lo, hi int
	}{
		{"small_<8KiB", 0, 8 << 10},
		{"medium_8-256KiB", 8 << 10, 256 << 10},
		{"large_>=256KiB", 256 << 10, 1 << 30},
	}
	if axes["blobsize"] {
		for _, b := range buckets {
			sub := filterBySize(blobs, b.lo, b.hi)
			if len(sub) == 0 {
				continue
			}
			results = append(results, runMedian("blobsize", b.name, sub, "snappy", 64, 128, reps))
			fmt.Fprintf(os.Stderr, "  blobsize=%-16s (%d blobs) done\n", b.name, len(sub))
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(results); err != nil {
		fmt.Fprintln(os.Stderr, "encode results:", err)
		os.Exit(1)
	}
}
