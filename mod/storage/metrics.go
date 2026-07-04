package storage

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/storage/pebblestore"
)

// // // // // // // // // //

type metricSpecObj struct {
	name      string
	counter   bool
	valueFunc func(pebblestore.MetricsSnapshotObj, pebblestore.EventCountsObj) int64
}

type boundMetricObj struct {
	instrumentObj metric.Int64Observable
	valueFunc     func(pebblestore.MetricsSnapshotObj, pebblestore.EventCountsObj) int64
}

// //

func metricSpecArr() []metricSpecObj {
	return []metricSpecObj{
		{"storage_pebble_live_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return int64(s.LiveBytes) }},
		{"storage_pebble_obsolete_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.ObsoleteBytes)
		}},
		{"storage_pebble_zombie_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.ZombieBytes)
		}},
		{"storage_pebble_wal_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return int64(s.WALBytes) }},
		{"storage_pebble_memtable_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.MemTableBytes)
		}},
		{"storage_pebble_compaction_debt_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.CompactionDebtBytes)
		}},
		{"storage_pebble_compaction_inprogress_bytes", false, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.CompactionInProgressBytes
		}},
		{"storage_pebble_compactions", true, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return s.Compactions }},
		{"storage_pebble_flushes", true, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return s.Flushes }},
		{"storage_pebble_block_cache_hits", true, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return s.BlockCacheHits }},
		{"storage_pebble_block_cache_misses", true, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 { return s.BlockCacheMisses }},
		{"storage_pebble_write_stalls", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.WriteStalls)
		}},
		{"storage_pebble_background_errors", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 { return int64(e.BgErrors) }},
		{"storage_pebble_data_corruptions", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.Corruptions)
		}},
		{"storage_pebble_disk_slow", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 { return int64(e.DiskSlow) }},
		{"storage_pebble_possible_api_misuse", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 { return int64(e.APIMisuse) }},
		{"storage_pebble_flush_duration_nanos", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.FlushNanos)
		}},
		{"storage_pebble_compaction_duration_nanos", true, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.CompactionNanos)
		}},
	}
}

// //

// RegisterMetrics registers observable Pebble metrics in the cache meter.
// A nil meter is a no-op; one callback captures db.Metrics and event counters per pass.
// db.Metrics takes Pebble's global DB mutex and walks LSM levels, so scrape intervals should stay at several seconds or more.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || meterObj == nil {
		return nil
	}
	specArr := metricSpecArr()
	boundArr := make([]boundMetricObj, 0, len(specArr))
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
		boundArr = append(boundArr, boundMetricObj{instrumentObj: instrumentObj, valueFunc: specObj.valueFunc})
		instrumentArr = append(instrumentArr, instrumentObj)
	}

	registrationObj, err := meterObj.RegisterCallback(func(_ context.Context, observerObj metric.Observer) error {
		snapObj, snapOK := obj.pebbleStoreObj.MetricsSnapshot()
		eventObj, eventOK := obj.pebbleStoreObj.EventCounts()
		if !snapOK || !eventOK {
			return nil
		}
		for i := range boundArr {
			observerObj.ObserveInt64(boundArr[i].instrumentObj, boundArr[i].valueFunc(snapObj, eventObj))
		}
		return nil
	}, instrumentArr...)
	if err != nil {
		return err
	}
	hotWalksObj, err := meterObj.Int64ObservableCounter("storage_hot_enforce_walks")
	if err != nil {
		return err
	}
	hotWalkNanosObj, err := meterObj.Int64ObservableCounter("storage_hot_enforce_walk_duration_nanos")
	if err != nil {
		return err
	}
	hotRegObj, err := meterObj.RegisterCallback(func(_ context.Context, observerObj metric.Observer) error {
		observerObj.ObserveInt64(hotWalksObj, obj.hotEnforceWalks.Load())
		observerObj.ObserveInt64(hotWalkNanosObj, obj.hotEnforceWalkNanos.Load())
		return nil
	}, hotWalksObj, hotWalkNanosObj)
	if err != nil {
		return err
	}

	obj.metricRegMu.Lock()
	obj.metricRegArr = append(obj.metricRegArr, registrationObj, hotRegObj)
	obj.metricRegMu.Unlock()
	return nil
}
