package pebblestore

import (
	"sync/atomic"

	"github.com/cockroachdb/pebble/v2"
)

// // // // // // // // // //

// EventCountsObj contains monotonic Pebble event counters from EventListener.
type EventCountsObj struct {
	WriteStalls     uint64
	BgErrors        uint64
	Corruptions     uint64
	DiskSlow        uint64
	APIMisuse       uint64
	FlushNanos      uint64
	CompactionNanos uint64
}

// MetricsSnapshotObj contains instantaneous db.Metrics values for observable metrics.
type MetricsSnapshotObj struct {
	LiveBytes                 uint64
	ObsoleteBytes             uint64
	ZombieBytes               uint64
	WALBytes                  uint64
	MemTableBytes             uint64
	CompactionDebtBytes       uint64
	CompactionInProgressBytes int64
	Compactions               int64
	Flushes                   int64
	BlockCacheHits            int64
	BlockCacheMisses          int64
}

type pebbleEventCountersObj struct {
	writeStalls     atomic.Uint64
	bgErrors        atomic.Uint64
	corruptions     atomic.Uint64
	diskSlow        atomic.Uint64
	apiMisuse       atomic.Uint64
	flushNanos      atomic.Uint64
	compactionNanos atomic.Uint64
}

// //

func newEventListener(countersObj *pebbleEventCountersObj, loggerObj pebble.Logger) *pebble.EventListener {
	counterListener := pebble.EventListener{
		WriteStallBegin:   func(pebble.WriteStallBeginInfo) { countersObj.writeStalls.Add(1) },
		BackgroundError:   func(error) { countersObj.bgErrors.Add(1) },
		DataCorruption:    func(pebble.DataCorruptionInfo) { countersObj.corruptions.Add(1) },
		DiskSlow:          func(pebble.DiskSlowInfo) { countersObj.diskSlow.Add(1) },
		PossibleAPIMisuse: func(pebble.PossibleAPIMisuseInfo) { countersObj.apiMisuse.Add(1) },
		FlushEnd:          func(info pebble.FlushInfo) { countersObj.flushNanos.Add(uint64(info.TotalDuration)) },
		CompactionEnd:     func(info pebble.CompactionInfo) { countersObj.compactionNanos.Add(uint64(info.TotalDuration)) },
	}
	defaultListener := pebble.EventListener{}
	defaultListener.EnsureDefaults(loggerObj)
	teedListener := pebble.TeeEventListener(counterListener, defaultListener)
	return &teedListener
}

// //

// EventCounts returns current monotonic counters; ok=false means the store is unavailable.
// Counters must not be reset because OTel would see a reset and corrupt rates, so callbacks skip that pass.
func (obj *Obj) EventCounts() (EventCountsObj, bool) {
	if obj == nil || obj.countersObj == nil {
		return EventCountsObj{}, false
	}
	return EventCountsObj{
		WriteStalls:     obj.countersObj.writeStalls.Load(),
		BgErrors:        obj.countersObj.bgErrors.Load(),
		Corruptions:     obj.countersObj.corruptions.Load(),
		DiskSlow:        obj.countersObj.diskSlow.Load(),
		APIMisuse:       obj.countersObj.apiMisuse.Load(),
		FlushNanos:      obj.countersObj.flushNanos.Load(),
		CompactionNanos: obj.countersObj.compactionNanos.Load(),
	}, true
}

// MetricsSnapshot returns instantaneous engine gauge values; ok=false means the store is closed.
func (obj *Obj) MetricsSnapshot() (MetricsSnapshotObj, bool) {
	if obj == nil || obj.dbObj == nil {
		return MetricsSnapshotObj{}, false
	}
	metricsObj := obj.dbObj.Metrics()
	return MetricsSnapshotObj{
		LiveBytes:                 metricsObj.Table.Local.LiveSize + metricsObj.BlobFiles.Local.LiveSize,
		ObsoleteBytes:             metricsObj.Table.Local.ObsoleteSize + metricsObj.BlobFiles.Local.ObsoleteSize,
		ZombieBytes:               metricsObj.Table.Local.ZombieSize + metricsObj.BlobFiles.Local.ZombieSize,
		WALBytes:                  metricsObj.WAL.PhysicalSize,
		MemTableBytes:             metricsObj.MemTable.Size,
		CompactionDebtBytes:       metricsObj.Compact.EstimatedDebt,
		CompactionInProgressBytes: metricsObj.Compact.InProgressBytes,
		Compactions:               metricsObj.Compact.Count,
		Flushes:                   metricsObj.Flush.Count,
		BlockCacheHits:            metricsObj.BlockCache.Hits,
		BlockCacheMisses:          metricsObj.BlockCache.Misses,
	}, true
}
