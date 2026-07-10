package telemetry

import (
	"bytes"
	"sort"
	"strconv"
	"strings"

	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// // // // // // // // // //

func buildSnapshot(rm *metricdata.ResourceMetrics) (doc []byte, values map[Group]map[string]float64) {
	byGroup := make(map[Group][]metricdata.Metrics, len(knownGroupsArr))
	for i := range rm.ScopeMetrics {
		scopeObj := rm.ScopeMetrics[i]
		group := groupForScope(scopeObj.Scope.Name)
		byGroup[group] = append(byGroup[group], scopeObj.Metrics...)
	}

	values = make(map[Group]map[string]float64, len(knownGroupsArr))
	var bufObj bytes.Buffer
	for _, group := range knownGroupsArr {
		families := byGroup[group]
		sort.Slice(families, func(i, j int) bool { return families[i].Name < families[j].Name })
		groupValues := make(map[string]float64, len(families))
		for i := range families {
			writeFamily(&bufObj, families[i])
			accumulateFamily(groupValues, families[i])
		}
		values[group] = groupValues
	}

	return bufObj.Bytes(), values
}

// //

func writeFamily(bufObj *bytes.Buffer, familyObj metricdata.Metrics) {
	name := sanitizeName(familyObj.Name)
	switch data := familyObj.Data.(type) {
	case metricdata.Sum[int64]:
		writeSum(bufObj, name, familyObj.Description, data.IsMonotonic, data.DataPoints)
	case metricdata.Sum[float64]:
		writeSum(bufObj, name, familyObj.Description, data.IsMonotonic, data.DataPoints)
	case metricdata.Gauge[int64]:
		writeGauge(bufObj, name, familyObj.Description, data.DataPoints)
	case metricdata.Gauge[float64]:
		writeGauge(bufObj, name, familyObj.Description, data.DataPoints)
	case metricdata.Histogram[int64]:
		writeHistogram(bufObj, name, familyObj.Description, data.DataPoints)
	case metricdata.Histogram[float64]:
		writeHistogram(bufObj, name, familyObj.Description, data.DataPoints)
	}
}

func writeHelp(bufObj *bytes.Buffer, name, help string) {
	if help == "" {
		return
	}
	bufObj.WriteString("# HELP ")
	bufObj.WriteString(name)
	bufObj.WriteByte(' ')
	bufObj.WriteString(escapeHelp(help))
	bufObj.WriteByte('\n')
}

func writeSum[N int64 | float64](bufObj *bytes.Buffer, name, help string, monotonic bool, dps []metricdata.DataPoint[N]) {
	typeName, suffix := "gauge", ""
	familyName := name
	if monotonic {
		typeName, suffix = "counter", "_total"
		familyName = strings.TrimSuffix(name, "_total")
	}
	writeHelp(bufObj, familyName, help)
	bufObj.WriteString("# TYPE " + familyName + " " + typeName + "\n")

	lines := make([]string, 0, len(dps))
	for i := range dps {
		lines = append(lines, familyName+suffix+renderLabels(labelPairs(dps[i].Attributes))+" "+formatNumber(dps[i].Value))
	}
	writeSortedLines(bufObj, lines)
}

func writeGauge[N int64 | float64](bufObj *bytes.Buffer, name, help string, dps []metricdata.DataPoint[N]) {
	writeHelp(bufObj, name, help)
	bufObj.WriteString("# TYPE " + name + " gauge\n")

	lines := make([]string, 0, len(dps))
	for i := range dps {
		lines = append(lines, name+renderLabels(labelPairs(dps[i].Attributes))+" "+formatNumber(dps[i].Value))
	}
	writeSortedLines(bufObj, lines)
}

func writeHistogram[N int64 | float64](bufObj *bytes.Buffer, name, help string, dps []metricdata.HistogramDataPoint[N]) {
	writeHelp(bufObj, name, help)
	bufObj.WriteString("# TYPE " + name + " histogram\n")

	order := make([]int, len(dps))
	keys := make([]string, len(dps))
	for i := range dps {
		order[i] = i
		keys[i] = renderLabels(labelPairs(dps[i].Attributes))
	}
	sort.Slice(order, func(i, j int) bool { return keys[order[i]] < keys[order[j]] })

	for _, idx := range order {
		dp := dps[idx]
		pairs := labelPairs(dp.Attributes)
		var cumulative uint64
		for bi, bound := range dp.Bounds {
			cumulative += dp.BucketCounts[bi]
			bucketPairs := append(append([]string(nil), pairs...), `le="`+formatFloat(bound)+`"`)
			bufObj.WriteString(name + "_bucket" + renderLabels(bucketPairs) + " " + strconv.FormatUint(cumulative, 10) + "\n")
		}
		infPairs := append(append([]string(nil), pairs...), `le="+Inf"`)
		bufObj.WriteString(name + "_bucket" + renderLabels(infPairs) + " " + strconv.FormatUint(dp.Count, 10) + "\n")
		bufObj.WriteString(name + "_count" + renderLabels(pairs) + " " + strconv.FormatUint(dp.Count, 10) + "\n")
		bufObj.WriteString(name + "_sum" + renderLabels(pairs) + " " + formatNumber(dp.Sum) + "\n")
	}
}

func writeSortedLines(bufObj *bytes.Buffer, lines []string) {
	sort.Strings(lines)
	for _, line := range lines {
		bufObj.WriteString(line)
		bufObj.WriteByte('\n')
	}
}
