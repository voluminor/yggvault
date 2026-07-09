package logger

import (
	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/telemetry"
)

// // // // // // // // // //

// RegisterMetrics publishes VictoriaLogs delivery counters.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || obj.vlSink == nil || meterObj == nil {
		return nil
	}
	_, err := telemetry.RegisterSpecs(meterObj, []telemetry.SpecObj{
		{
			Name:    "logs_dropped_total",
			Help:    "log records dropped because the VictoriaLogs queue was full",
			Counter: true,
		},
		{
			Name:    "log_push_failures_total",
			Help:    "failed VictoriaLogs push attempts",
			Counter: true,
		},
	}, func() ([]int64, bool) {
		return []int64{int64(obj.vlSink.droppedTotal.Load()), int64(obj.vlSink.failureTotal.Load())}, true
	})
	return err
}
