package source

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/metric"
)

// // // // // // // // // //

type sourceMetricsObj struct {
	limiterWait      metric.Float64Histogram
	upstreamRequests metric.Int64Counter
}

func (obj *sourceMetricsObj) recordUpstreamLimiterWait(elapsedObj time.Duration) {
	if obj == nil {
		return
	}
	obj.limiterWait.Record(context.Background(), elapsedObj.Seconds())
}

func (obj *sourceMetricsObj) recordUpstreamRequest() {
	if obj == nil {
		return
	}
	obj.upstreamRequests.Add(context.Background(), 1)
}

// RegisterMetrics registers source outbound HTTP metrics.
func (obj *Obj) RegisterMetrics(meterObj metric.Meter) error {
	if meterObj == nil {
		return nil
	}
	metricsObj := &sourceMetricsObj{}
	var err error
	if metricsObj.limiterWait, err = meterObj.Float64Histogram("source_upstream_limiter_wait_seconds",
		metric.WithUnit("s"), metric.WithDescription("outgoing upstream HTTP limiter wait duration")); err != nil {
		return err
	}
	if metricsObj.upstreamRequests, err = meterObj.Int64Counter("source_upstream_requests_total",
		metric.WithDescription("outgoing upstream HTTP requests admitted by the limiter")); err != nil {
		return err
	}
	obj.metricsObj = metricsObj
	return nil
}
