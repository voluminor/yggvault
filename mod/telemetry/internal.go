package telemetry

import (
	"context"
	"runtime"
	runtimemetrics "runtime/metrics"

	"github.com/voluminor/yggvault/target"

	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

// Indices of the runtime samples in the reused slice; runtime/metrics avoids the STW pause of ReadMemStats.
const (
	miHeapObjectsBytes  = iota // /memory/classes/heap/objects:bytes ~ MemStats.HeapAlloc
	miHeapUnusedBytes          // /memory/classes/heap/unused:bytes
	miHeapFreeBytes            // /memory/classes/heap/free:bytes
	miHeapReleasedBytes        // /memory/classes/heap/released:bytes
	miGCHeapObjects            // /gc/heap/objects:objects ~ MemStats.HeapObjects
	miGCCyclesTotal            // /gc/cycles/total:gc-cycles ~ MemStats.NumGC
)

func runtimeSampleArr() []runtimemetrics.Sample {
	return []runtimemetrics.Sample{
		{Name: "/memory/classes/heap/objects:bytes"},
		{Name: "/memory/classes/heap/unused:bytes"},
		{Name: "/memory/classes/heap/free:bytes"},
		{Name: "/memory/classes/heap/released:bytes"},
		{Name: "/gc/heap/objects:objects"},
		{Name: "/gc/cycles/total:gc-cycles"},
	}
}

func (obj *Obj) registerInternal() error {
	meterObj := obj.provider.Meter(cScopePrefix+string(GroupInternal),
		metric.WithInstrumentationVersion(target.Version))

	var err error
	if obj.pushTotal, err = meterObj.Int64Counter("metrics_push",
		metric.WithDescription("metrics push attempts")); err != nil {
		return err
	}
	if obj.pushFailures, err = meterObj.Int64Counter("metrics_push_failures",
		metric.WithDescription("failed metrics push attempts")); err != nil {
		return err
	}
	if obj.pushBytes, err = meterObj.Int64Counter("metrics_push_bytes",
		metric.WithDescription("bytes sent to the push endpoint")); err != nil {
		return err
	}

	goroutines, err := meterObj.Int64ObservableGauge("go_goroutines",
		metric.WithDescription("number of live goroutines"))
	if err != nil {
		return err
	}
	heapAlloc, err := meterObj.Int64ObservableGauge("go_memstats_heap_alloc_bytes",
		metric.WithUnit("By"), metric.WithDescription("heap bytes allocated and still in use"))
	if err != nil {
		return err
	}
	heapSys, err := meterObj.Int64ObservableGauge("go_memstats_heap_sys_bytes",
		metric.WithUnit("By"), metric.WithDescription("heap bytes obtained from the system"))
	if err != nil {
		return err
	}
	heapObjects, err := meterObj.Int64ObservableGauge("go_memstats_heap_objects",
		metric.WithDescription("number of allocated heap objects"))
	if err != nil {
		return err
	}
	gcCycles, err := meterObj.Int64ObservableCounter("go_gc_cycles",
		metric.WithDescription("number of completed GC cycles"))
	if err != nil {
		return err
	}

	// sampleArr is reused across collections; ManualReader.Collect invokes callbacks single-threaded.
	sampleArr := runtimeSampleArr()
	_, err = meterObj.RegisterCallback(
		func(_ context.Context, observerObj metric.Observer) error {
			observerObj.ObserveInt64(goroutines, int64(runtime.NumGoroutine()))
			runtimemetrics.Read(sampleArr)
			heapAllocBytes := sampleArr[miHeapObjectsBytes].Value.Uint64()
			heapSysBytes := heapAllocBytes + sampleArr[miHeapUnusedBytes].Value.Uint64() +
				sampleArr[miHeapFreeBytes].Value.Uint64() + sampleArr[miHeapReleasedBytes].Value.Uint64()
			observerObj.ObserveInt64(heapAlloc, int64(heapAllocBytes))
			observerObj.ObserveInt64(heapSys, int64(heapSysBytes))
			observerObj.ObserveInt64(heapObjects, int64(sampleArr[miGCHeapObjects].Value.Uint64()))
			observerObj.ObserveInt64(gcCycles, int64(sampleArr[miGCCyclesTotal].Value.Uint64()))
			return nil
		},
		goroutines, heapAlloc, heapSys, heapObjects, gcCycles,
	)
	return err
}
