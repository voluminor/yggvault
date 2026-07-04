package telemetry

import (
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// // // // // // // // // //

// accumulateFamily folds a metric family into scalar values for the JSON endpoints.
// Sum and Gauge are summed across all label sets; Histogram yields `<name>_sum` and `<name>_count`.
// Keys match the sanitized OpenMetrics names.
func accumulateFamily(valuesObj map[string]float64, familyObj metricdata.Metrics) {
	name := sanitizeName(familyObj.Name)
	switch data := familyObj.Data.(type) {
	case metricdata.Sum[int64]:
		for i := range data.DataPoints {
			valuesObj[name] += float64(data.DataPoints[i].Value)
		}
	case metricdata.Sum[float64]:
		for i := range data.DataPoints {
			valuesObj[name] += data.DataPoints[i].Value
		}
	case metricdata.Gauge[int64]:
		for i := range data.DataPoints {
			valuesObj[name] += float64(data.DataPoints[i].Value)
		}
	case metricdata.Gauge[float64]:
		for i := range data.DataPoints {
			valuesObj[name] += data.DataPoints[i].Value
		}
	case metricdata.Histogram[int64]:
		accumulateHistogram[int64](valuesObj, name, data.DataPoints)
	case metricdata.Histogram[float64]:
		accumulateHistogram[float64](valuesObj, name, data.DataPoints)
	}
}

func accumulateHistogram[N int64 | float64](valuesObj map[string]float64, name string, dps []metricdata.HistogramDataPoint[N]) {
	for i := range dps {
		valuesObj[name+"_sum"] += float64(dps[i].Sum)
		valuesObj[name+"_count"] += float64(dps[i].Count)
	}
}
