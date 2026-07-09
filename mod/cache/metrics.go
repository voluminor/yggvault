package cache

import (
	"github.com/voluminor/yggvault/mod/telemetry"
	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

// StatsObj is a snapshot of cache counters and sizes.
type StatsObj struct {
	Hits          uint64
	Misses        uint64
	Builds        uint64
	BuildsAborted uint64
	Evictions     uint64
	Shared        uint64
	Entries       int64
	Bytes         int64
}

// Stats returns current atomic counters plus total shard entries and bytes.
func (obj *Obj) Stats() StatsObj {
	var entries int64
	var bytes int64
	for i := range obj.shardArr {
		shardEntries, shardBytes := obj.shardArr[i].stats()
		entries += int64(shardEntries)
		bytes += shardBytes
	}
	return StatsObj{
		Hits:          obj.hits.Load(),
		Misses:        obj.misses.Load(),
		Builds:        obj.builds.Load(),
		BuildsAborted: obj.buildsAborted.Load(),
		Evictions:     obj.evictions.Load(),
		Shared:        obj.shared.Load(),
		Entries:       entries,
		Bytes:         bytes,
	}
}

// // // // // // // // // //

type cacheMetricSpecObj struct {
	specObj   telemetry.SpecObj
	valueFunc func(StatsObj) int64
}

func cacheMetricSpecArr() []cacheMetricSpecObj {
	return []cacheMetricSpecObj{
		{telemetry.SpecObj{Name: "cache_hits", Counter: true}, func(s StatsObj) int64 { return int64(s.Hits) }},
		{telemetry.SpecObj{Name: "cache_misses", Counter: true}, func(s StatsObj) int64 { return int64(s.Misses) }},
		{telemetry.SpecObj{Name: "cache_materializations", Counter: true}, func(s StatsObj) int64 { return int64(s.Builds) }},
		{telemetry.SpecObj{Name: "cache_materializations_aborted", Counter: true}, func(s StatsObj) int64 { return int64(s.BuildsAborted) }},
		{telemetry.SpecObj{Name: "cache_evictions", Counter: true}, func(s StatsObj) int64 { return int64(s.Evictions) }},
		{telemetry.SpecObj{Name: "cache_singleflight_shared", Counter: true}, func(s StatsObj) int64 { return int64(s.Shared) }},
		{telemetry.SpecObj{Name: "cache_entries"}, func(s StatsObj) int64 { return s.Entries }},
		{telemetry.SpecObj{Name: "cache_bytes"}, func(s StatsObj) int64 { return s.Bytes }},
	}
}

// RegisterMetrics registers the cache snapshot through a single observable callback.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || meterObj == nil {
		return nil
	}
	specArr := cacheMetricSpecArr()
	telemetrySpecArr := make([]telemetry.SpecObj, 0, len(specArr))
	for _, specObj := range specArr {
		telemetrySpecArr = append(telemetrySpecArr, specObj.specObj)
	}
	_, err := telemetry.RegisterSpecs(meterObj, telemetrySpecArr, func() ([]int64, bool) {
		statsObj := obj.Stats()
		valueArr := make([]int64, len(specArr))
		for i := range specArr {
			valueArr[i] = specArr[i].valueFunc(statsObj)
		}
		return valueArr, true
	})
	return err
}
