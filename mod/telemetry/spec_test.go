package telemetry

import (
	"context"
	"testing"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// // // // // // // // // //

func collectSpecMetrics(t *testing.T, readerObj *sdkmetric.ManualReader) metricdata.ResourceMetrics {
	t.Helper()
	var dataObj metricdata.ResourceMetrics
	if err := readerObj.Collect(context.Background(), &dataObj); err != nil {
		t.Fatalf("Collect: %v", err)
	}
	return dataObj
}

func findMetricObj(t *testing.T, dataObj metricdata.ResourceMetrics, nameText string) metricdata.Metrics {
	t.Helper()
	metricObj, ok := lookupMetricObj(dataObj, nameText)
	if !ok {
		t.Fatalf("metric %q not found", nameText)
	}
	return metricObj
}

func lookupMetricObj(dataObj metricdata.ResourceMetrics, nameText string) (metricdata.Metrics, bool) {
	for _, scopeObj := range dataObj.ScopeMetrics {
		for _, metricObj := range scopeObj.Metrics {
			if metricObj.Name == nameText {
				return metricObj, true
			}
		}
	}
	return metricdata.Metrics{}, false
}

//

func TestRegisterSpecsPublishesCounterGaugeUnitAndHelp(t *testing.T) {
	readerObj := sdkmetric.NewManualReader()
	providerObj := sdkmetric.NewMeterProvider(sdkmetric.WithReader(readerObj))
	regObj, err := RegisterSpecs(providerObj.Meter("test"), []SpecObj{
		{Name: "spec_counter", Help: "counter help", Counter: true},
		{Name: "spec_gauge", Help: "gauge help", Unit: "By"},
	}, func() ([]int64, bool) {
		return []int64{7, 11}, true
	})
	if err != nil {
		t.Fatalf("RegisterSpecs: %v", err)
	}
	t.Cleanup(func() { _ = regObj.Unregister() })

	dataObj := collectSpecMetrics(t, readerObj)
	counterObj := findMetricObj(t, dataObj, "spec_counter")
	if counterObj.Description != "counter help" {
		t.Fatalf("counter help=%q", counterObj.Description)
	}
	sumObj, ok := counterObj.Data.(metricdata.Sum[int64])
	if !ok {
		t.Fatalf("counter data type %T", counterObj.Data)
	}
	if len(sumObj.DataPoints) != 1 || sumObj.DataPoints[0].Value != 7 {
		t.Fatalf("counter datapoints=%v", sumObj.DataPoints)
	}

	gaugeObj := findMetricObj(t, dataObj, "spec_gauge")
	if gaugeObj.Description != "gauge help" || gaugeObj.Unit != "By" {
		t.Fatalf("gauge help/unit=%q/%q", gaugeObj.Description, gaugeObj.Unit)
	}
	gaugeDataObj, ok := gaugeObj.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("gauge data type %T", gaugeObj.Data)
	}
	if len(gaugeDataObj.DataPoints) != 1 || gaugeDataObj.DataPoints[0].Value != 11 {
		t.Fatalf("gauge datapoints=%v", gaugeDataObj.DataPoints)
	}
}

func TestRegisterSpecsSkipsWhenObserverNotReady(t *testing.T) {
	readerObj := sdkmetric.NewManualReader()
	providerObj := sdkmetric.NewMeterProvider(sdkmetric.WithReader(readerObj))
	regObj, err := RegisterSpecs(providerObj.Meter("test"), []SpecObj{{Name: "spec_skipped"}}, func() ([]int64, bool) {
		return []int64{1}, false
	})
	if err != nil {
		t.Fatalf("RegisterSpecs: %v", err)
	}
	t.Cleanup(func() { _ = regObj.Unregister() })

	dataObj := collectSpecMetrics(t, readerObj)
	metricObj, ok := lookupMetricObj(dataObj, "spec_skipped")
	if !ok {
		return
	}
	gaugeObj, ok := metricObj.Data.(metricdata.Gauge[int64])
	if !ok {
		t.Fatalf("skipped data type %T", metricObj.Data)
	}
	if len(gaugeObj.DataPoints) != 0 {
		t.Fatalf("skipped datapoints=%v", gaugeObj.DataPoints)
	}
}

func TestRegisterSpecsNilMeterNoop(t *testing.T) {
	regObj, err := RegisterSpecs(nil, []SpecObj{{Name: "none"}}, func() ([]int64, bool) {
		return []int64{1}, true
	})
	if err != nil {
		t.Fatalf("nil meter RegisterSpecs: %v", err)
	}
	if regObj != nil {
		t.Fatalf("nil meter registration=%#v", regObj)
	}
}
