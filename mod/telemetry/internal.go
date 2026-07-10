package telemetry

import (
	"runtime"
	runtimemetrics "runtime/metrics"

	"github.com/voluminor/yggvault/target"

	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

const (
	miHeapObjectsBytes = iota
	miHeapUnusedBytes
	miHeapFreeBytes
	miHeapReleasedBytes
	miGCHeapObjects
	miGCCyclesTotal
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

	sampleArr := runtimeSampleArr()
	_, err = RegisterSpecs(meterObj, []SpecObj{
		{Name: "go_goroutines", Help: "number of live goroutines"},
		{Name: "go_memstats_heap_alloc_bytes", Help: "heap bytes allocated and still in use", Unit: "By"},
		{Name: "go_memstats_heap_sys_bytes", Help: "heap bytes obtained from the system", Unit: "By"},
		{Name: "go_memstats_heap_objects", Help: "number of allocated heap objects"},
		{Name: "go_gc_cycles", Help: "number of completed GC cycles", Counter: true},
	}, func() ([]int64, bool) {
		runtimemetrics.Read(sampleArr)
		heapAllocBytes := sampleArr[miHeapObjectsBytes].Value.Uint64()
		heapSysBytes := heapAllocBytes + sampleArr[miHeapUnusedBytes].Value.Uint64() +
			sampleArr[miHeapFreeBytes].Value.Uint64() + sampleArr[miHeapReleasedBytes].Value.Uint64()
		return []int64{
			int64(runtime.NumGoroutine()),
			int64(heapAllocBytes),
			int64(heapSysBytes),
			int64(sampleArr[miGCHeapObjects].Value.Uint64()),
			int64(sampleArr[miGCCyclesTotal].Value.Uint64()),
		}, true
	})
	return err
}
