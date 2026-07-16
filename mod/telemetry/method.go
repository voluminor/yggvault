package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/metric/noop"
)

// // // // // // // // // //

// Enabled reports whether at least one metrics exposure method is enabled.
func (obj *Obj) Enabled() bool {
	return obj != nil && obj.enabled
}

// MeterProvider returns the provider to pass into ogen via api.WithMeterProvider.
// A nil receiver yields a noop provider, matching the nil tolerance of Enabled.
func (obj *Obj) MeterProvider() metric.MeterProvider {
	if obj == nil {
		return noop.NewMeterProvider()
	}
	return obj.provider
}

// Meter returns a group meter for producers such as cache, rescan and errors.
// Metric names and attributes must stay low-cardinality: key, version and raw path are never recorded.
func (obj *Obj) Meter(group Group) metric.Meter {
	return obj.MeterProvider().Meter(cScopePrefix + string(group))
}

// InternalOM returns the prebuilt full Prometheus text document across all groups.
// ok=false when telemetry is disabled; before the first build an empty document is returned.
func (obj *Obj) InternalOM() ([]byte, bool) {
	if !obj.Enabled() {
		return nil, false
	}
	snapObj := obj.snap.Load()
	if snapObj == nil {
		return nil, true
	}
	return snapObj.doc, true
}

// GroupValues returns the prebuilt map of metric name -> aggregated value for a public group.
// ok=false when telemetry is disabled or the group is unknown; before the first build the map is empty.
func (obj *Obj) GroupValues(group Group) (map[string]float64, bool) {
	if !obj.Enabled() || !knownGroupSetObj[group] {
		return nil, false
	}
	snapObj := obj.snap.Load()
	if snapObj == nil {
		return map[string]float64{}, true
	}
	if valuesObj, ok := snapObj.values[group]; ok {
		return valuesObj, true
	}
	return map[string]float64{}, true
}

// Start performs the initial build and launches the background prebuild/push loops.
func (obj *Obj) Start(ctx context.Context) {
	if !obj.Enabled() || !obj.started.CompareAndSwap(false, true) {
		return
	}
	_ = obj.build(ctx)
	go obj.snapshotLoop(ctx)
	if obj.pushEnabled {
		go obj.pushLoop(ctx)
	}
}

// Close stops the loops and shuts down the SDK provider.
// The final push is skipped: snapshots are cumulative, losing one tick does not change the counter total.
func (obj *Obj) Close(ctx context.Context) error {
	if !obj.Enabled() {
		return nil
	}
	obj.stopOnce.Do(func() { close(obj.doneCh) })
	if obj.sdkProvider != nil {
		return obj.sdkProvider.Shutdown(ctx)
	}
	return nil
}
