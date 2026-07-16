package storage

import (
	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/telemetry"
)

// // // // // // // // // //

type metricSpecObj struct {
	specObj   telemetry.SpecObj
	valueFunc func(pebblestore.MetricsSnapshotObj, pebblestore.EventCountsObj) int64
}

// //

func metricSpecArr() []metricSpecObj {
	return []metricSpecObj{
		{telemetry.SpecObj{Name: "storage_pebble_live_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.LiveBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_obsolete_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.ObsoleteBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_zombie_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.ZombieBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_wal_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.WALBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_memtable_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.MemTableBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_compaction_debt_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return int64(s.CompactionDebtBytes)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_compaction_inprogress_bytes"}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.CompactionInProgressBytes
		}},
		{telemetry.SpecObj{Name: "storage_pebble_compactions", Counter: true}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.Compactions
		}},
		{telemetry.SpecObj{Name: "storage_pebble_flushes", Counter: true}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.Flushes
		}},
		{telemetry.SpecObj{Name: "storage_pebble_block_cache_hits", Counter: true}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.BlockCacheHits
		}},
		{telemetry.SpecObj{Name: "storage_pebble_block_cache_misses", Counter: true}, func(s pebblestore.MetricsSnapshotObj, _ pebblestore.EventCountsObj) int64 {
			return s.BlockCacheMisses
		}},
		{telemetry.SpecObj{Name: "storage_pebble_write_stalls", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.WriteStalls)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_background_errors", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.BgErrors)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_data_corruptions", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.Corruptions)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_disk_slow", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.DiskSlow)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_possible_api_misuse", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.APIMisuse)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_flush_duration_nanos", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.FlushNanos)
		}},
		{telemetry.SpecObj{Name: "storage_pebble_compaction_duration_nanos", Counter: true}, func(_ pebblestore.MetricsSnapshotObj, e pebblestore.EventCountsObj) int64 {
			return int64(e.CompactionNanos)
		}},
	}
}

// //

// RegisterMetrics registers the Pebble snapshot without holding storage locks.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || meterObj == nil {
		return nil
	}
	specArr := metricSpecArr()
	telemetrySpecArr := make([]telemetry.SpecObj, 0, len(specArr))
	for _, specObj := range specArr {
		telemetrySpecArr = append(telemetrySpecArr, specObj.specObj)
	}

	registrationObj, err := telemetry.RegisterSpecs(meterObj, telemetrySpecArr, func() ([]int64, bool) {
		snapObj, snapOK := obj.pebbleStoreObj.MetricsSnapshot()
		eventObj, eventOK := obj.pebbleStoreObj.EventCounts()
		if !snapOK || !eventOK {
			return nil, false
		}
		valueArr := make([]int64, len(specArr))
		for i := range specArr {
			valueArr[i] = specArr[i].valueFunc(snapObj, eventObj)
		}
		return valueArr, true
	})
	if err != nil {
		return err
	}
	hotRegObj, err := telemetry.RegisterSpecs(meterObj, []telemetry.SpecObj{
		{Name: "storage_hot_enforce_walks", Counter: true},
		{Name: "storage_hot_enforce_walk_duration_nanos", Counter: true},
	}, func() ([]int64, bool) {
		return []int64{obj.hotEnforceWalks.Load(), obj.hotEnforceWalkNanos.Load()}, true
	})
	if err != nil {
		return err
	}

	obj.metricRegMu.Lock()
	if registrationObj != nil {
		obj.metricRegArr = append(obj.metricRegArr, registrationObj)
	}
	if hotRegObj != nil {
		obj.metricRegArr = append(obj.metricRegArr, hotRegObj)
	}
	obj.metricRegMu.Unlock()
	return nil
}
