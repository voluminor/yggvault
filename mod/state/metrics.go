package state

import (
	"context"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

// RegisterMetrics publishes errors group metrics from the Error Registry:
//
//	diagnostics_active: active diagnostic count (gauge);
//	recent_error{code,scope,impact,reason,key,version}: one series per recent diagnostic with value=count,
//	  capped by metrics.errors.recent_buffer to limit cardinality.
//
// ActiveDiagnostics sorts by LastSeen descending, so the buffer keeps the newest records.
// A nil meter is a no-op; state depends only on neutral otel/metric and attribute types.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if obj == nil || meterObj == nil {
		return nil
	}
	limit := obj.recentBuffer
	if limit < 1 {
		limit = 1
	}
	activeObj, err := meterObj.Int64ObservableGauge("diagnostics_active",
		metric.WithDescription("active Error Registry diagnostics"))
	if err != nil {
		return err
	}
	recentObj, err := meterObj.Int64ObservableGauge("recent_error",
		metric.WithDescription("most recent Error Registry diagnostics (value=count); capped at metrics.errors.recent_buffer series"))
	if err != nil {
		return err
	}
	_, err = meterObj.RegisterCallback(
		func(_ context.Context, observerObj metric.Observer) error {
			viewArr := obj.ActiveDiagnostics()
			observerObj.ObserveInt64(activeObj, int64(len(viewArr)))
			for i := 0; i < len(viewArr) && i < limit; i++ {
				viewObj := viewArr[i]
				observerObj.ObserveInt64(recentObj, int64(viewObj.Count), metric.WithAttributes(
					attribute.String("code", viewObj.Code),
					attribute.String("scope", viewObj.Scope.String()),
					attribute.String("impact", viewObj.Impact.String()),
					attribute.String("reason", viewObj.Reason.String()),
					attribute.String("key", viewObj.Key),
					attribute.String("version", viewObj.Version),
				))
			}
			return nil
		},
		activeObj, recentObj,
	)
	return err
}
