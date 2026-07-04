package logger

import (
	"context"

	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

// RegisterMetrics publishes VictoriaLogs delivery self-metrics in the internal group: dropped records and push
// failures. Without a VictoriaLogs sink or meter it is a no-op, and it depends only on neutral otel/metric.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || obj.vlSink == nil || meterObj == nil {
		return nil
	}
	droppedObj, err := meterObj.Int64ObservableCounter("logs_dropped_total",
		metric.WithDescription("log records dropped because the VictoriaLogs queue was full"))
	if err != nil {
		return err
	}
	failuresObj, err := meterObj.Int64ObservableCounter("log_push_failures_total",
		metric.WithDescription("failed VictoriaLogs push attempts"))
	if err != nil {
		return err
	}
	_, err = meterObj.RegisterCallback(
		func(_ context.Context, observerObj metric.Observer) error {
			observerObj.ObserveInt64(droppedObj, int64(obj.vlSink.droppedTotal.Load()))
			observerObj.ObserveInt64(failuresObj, int64(obj.vlSink.failureTotal.Load()))
			return nil
		},
		droppedObj, failuresObj,
	)
	return err
}
