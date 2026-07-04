package cache

import (
	"context"

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
	name      string
	counter   bool
	valueFunc func(StatsObj) int64
}

type boundCacheMetricObj struct {
	instrumentObj metric.Int64Observable
	valueFunc     func(StatsObj) int64
}

func cacheMetricSpecArr() []cacheMetricSpecObj {
	return []cacheMetricSpecObj{
		{"cache_hits", true, func(s StatsObj) int64 { return int64(s.Hits) }},
		{"cache_misses", true, func(s StatsObj) int64 { return int64(s.Misses) }},
		{"cache_materializations", true, func(s StatsObj) int64 { return int64(s.Builds) }},
		{"cache_materializations_aborted", true, func(s StatsObj) int64 { return int64(s.BuildsAborted) }},
		{"cache_evictions", true, func(s StatsObj) int64 { return int64(s.Evictions) }},
		{"cache_singleflight_shared", true, func(s StatsObj) int64 { return int64(s.Shared) }},
		{"cache_entries", false, func(s StatsObj) int64 { return s.Entries }},
		{"cache_bytes", false, func(s StatsObj) int64 { return s.Bytes }},
	}
}

// RegisterMetrics registers cache observable metrics in meter. A nil meter is a no-op.
// One callback reads Stats per pass; the cache lives for the process lifetime, so registration is not stored.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || meterObj == nil {
		return nil
	}
	specArr := cacheMetricSpecArr()
	boundArr := make([]boundCacheMetricObj, 0, len(specArr))
	instrumentArr := make([]metric.Observable, 0, len(specArr))
	for _, specObj := range specArr {
		var instrumentObj metric.Int64Observable
		var err error
		if specObj.counter {
			instrumentObj, err = meterObj.Int64ObservableCounter(specObj.name)
		} else {
			instrumentObj, err = meterObj.Int64ObservableGauge(specObj.name)
		}
		if err != nil {
			return err
		}
		boundArr = append(boundArr, boundCacheMetricObj{instrumentObj: instrumentObj, valueFunc: specObj.valueFunc})
		instrumentArr = append(instrumentArr, instrumentObj)
	}

	_, err := meterObj.RegisterCallback(func(_ context.Context, observerObj metric.Observer) error {
		statsObj := obj.Stats()
		for i := range boundArr {
			observerObj.ObserveInt64(boundArr[i].instrumentObj, boundArr[i].valueFunc(statsObj))
		}
		return nil
	}, instrumentArr...)
	return err
}
