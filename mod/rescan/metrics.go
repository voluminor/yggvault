package rescan

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

type rescanMetricsObj struct {
	cycles            metric.Int64Counter
	cycleDuration     metric.Float64Histogram
	versions          metric.Int64Counter
	unavailable       metric.Int64Counter
	versionFailures   metric.Int64Counter
	degradedPublishes metric.Int64Counter
	droppedSymlinks   metric.Int64Counter
	goZipUnclassified metric.Int64Counter
}

func (obj *rescanMetricsObj) recordCycle(elapsed time.Duration) {
	if obj == nil {
		return
	}
	obj.cycles.Add(context.Background(), 1)
	obj.cycleDuration.Record(context.Background(), elapsed.Seconds())
}

func (obj *rescanMetricsObj) recordVersionPublished() {
	if obj == nil {
		return
	}
	obj.versions.Add(context.Background(), 1)
}

func (obj *rescanMetricsObj) recordKeyUnavailable() {
	if obj == nil {
		return
	}
	obj.unavailable.Add(context.Background(), 1)
}

func (obj *rescanMetricsObj) recordVersionFailure(codeText string) {
	if obj == nil {
		return
	}
	obj.versionFailures.Add(context.Background(), 1, metric.WithAttributes(attribute.String("phase", codeText)))
}

func (obj *rescanMetricsObj) recordDegradedPublish() {
	if obj == nil {
		return
	}
	obj.degradedPublishes.Add(context.Background(), 1)
}

func (obj *rescanMetricsObj) recordDroppedSymlinks(countValue int) {
	if obj == nil || countValue <= 0 {
		return
	}
	obj.droppedSymlinks.Add(context.Background(), int64(countValue))
}

func (obj *rescanMetricsObj) recordGoZipUnclassified() {
	if obj == nil {
		return
	}
	obj.goZipUnclassified.Add(context.Background(), 1)
}

// RegisterMetrics registers instruments for the rescan group.
// A nil meter is a no-op; rescan depends only on neutral otel/metric types.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if meterObj == nil {
		return nil
	}
	metricsObj := &rescanMetricsObj{}
	var err error
	if metricsObj.cycles, err = meterObj.Int64Counter("rescan_cycles_total",
		metric.WithDescription("completed rescan cycles")); err != nil {
		return err
	}
	if metricsObj.cycleDuration, err = meterObj.Float64Histogram("rescan_cycle_duration_seconds",
		metric.WithUnit("s"), metric.WithDescription("rescan cycle wall-clock duration")); err != nil {
		return err
	}
	if metricsObj.versions, err = meterObj.Int64Counter("rescan_versions_published_total",
		metric.WithDescription("versions published or changed during rescan")); err != nil {
		return err
	}
	if metricsObj.unavailable, err = meterObj.Int64Counter("rescan_keys_unavailable_total",
		metric.WithDescription("per-cycle key-unavailable events")); err != nil {
		return err
	}
	if metricsObj.versionFailures, err = meterObj.Int64Counter("rescan_version_failures_total",
		metric.WithDescription("version-level rescan failures by phase")); err != nil {
		return err
	}
	if metricsObj.degradedPublishes, err = meterObj.Int64Counter("rescan_degraded_publishes_total",
		metric.WithDescription("versions published after deterministic content degradation")); err != nil {
		return err
	}
	if metricsObj.droppedSymlinks, err = meterObj.Int64Counter("rescan_dropped_symlinks_total",
		metric.WithDescription("archive symlink entries dropped because they escape the archive root")); err != nil {
		return err
	}
	if metricsObj.goZipUnclassified, err = meterObj.Int64Counter("rescan_go_zip_unclassified_total",
		metric.WithDescription("go module zip blocks with unclassified x/mod/zip errors")); err != nil {
		return err
	}
	obj.metricsObj = metricsObj
	return nil
}
