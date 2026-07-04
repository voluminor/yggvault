package storage

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

func TestRegisterMetricsCollectsPebbleGauges(t *testing.T) {
	obj := newTestObj(t, newTestConfigObj(t))

	if err := obj.RegisterMetrics(nil); err != nil {
		t.Fatalf("RegisterMetrics(nil) returned error: %v", err)
	}

	readerObj := sdkmetric.NewManualReader()
	providerObj := sdkmetric.NewMeterProvider(sdkmetric.WithReader(readerObj))
	if err := obj.RegisterMetrics(providerObj.Meter("cache")); err != nil {
		t.Fatalf("RegisterMetrics returned error: %v", err)
	}

	publishTestVersion(t, obj, "v1.0.0", []core.InputEntryObj{{Path: "file.txt", Content: []byte("content to store and measure")}})

	var rmObj metricdata.ResourceMetrics
	if err := readerObj.Collect(context.Background(), &rmObj); err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	nameSetObj := make(map[string]struct{})
	for _, scopeObj := range rmObj.ScopeMetrics {
		for _, metricObj := range scopeObj.Metrics {
			nameSetObj[metricObj.Name] = struct{}{}
		}
	}
	for _, name := range []string{"storage_pebble_live_bytes", "storage_pebble_write_stalls", "storage_pebble_block_cache_misses"} {
		if _, ok := nameSetObj[name]; !ok {
			t.Fatalf("metric %s not collected", name)
		}
	}
}
