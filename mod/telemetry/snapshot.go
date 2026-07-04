package telemetry

import (
	"context"
	"time"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// // // // // // // // // //

func (obj *Obj) build(ctx context.Context) error {
	var rm metricdata.ResourceMetrics
	if err := obj.reader.Collect(ctx, &rm); err != nil {
		return err
	}
	doc, values := buildSnapshot(&rm)
	obj.snap.Store(&snapshotObj{doc: doc, values: values})
	return nil
}

func (obj *Obj) snapshotLoop(ctx context.Context) {
	tickerObj := time.NewTicker(obj.snapshotEvery)
	defer tickerObj.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-obj.doneCh:
			return
		case <-tickerObj.C:
			_ = obj.build(ctx)
		}
	}
}
