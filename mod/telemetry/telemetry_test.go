package telemetry

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	stcfg "github.com/voluminor/yggvault/target/stconf"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/sdk/instrumentation"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// // // // // // // // // //

func TestGroupForScope(t *testing.T) {
	cases := map[string]Group{
		"github.com/voluminor/yggvault/cache":    GroupCache,
		"github.com/voluminor/yggvault/internal": GroupInternal,
		"github.com/voluminor/yggvault/unknown":  GroupCore,
		"github.com/ogen-go/ogen":                GroupCore,
		"":                                       GroupCore,
	}
	for scope, want := range cases {
		if got := groupForScope(scope); got != want {
			t.Fatalf("groupForScope(%q) = %q, want %q", scope, got, want)
		}
	}
}

func TestBuildSnapshotCounterAndHistogram(t *testing.T) {
	rm := &metricdata.ResourceMetrics{
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{Name: "github.com/voluminor/yggvault/cache"},
				Metrics: []metricdata.Metrics{
					{
						Name:        "cache.hits",
						Description: "cache hits",
						Data: metricdata.Sum[int64]{
							IsMonotonic: true,
							Temporality: metricdata.CumulativeTemporality,
							DataPoints: []metricdata.DataPoint[int64]{
								{Value: 5, Attributes: attribute.NewSet(attribute.String("kind", "meta"))},
							},
						},
					},
					{
						Name: "cache.build.duration",
						Data: metricdata.Histogram[float64]{
							Temporality: metricdata.CumulativeTemporality,
							DataPoints: []metricdata.HistogramDataPoint[float64]{
								{
									Count:        3,
									Sum:          6,
									Bounds:       []float64{1, 2},
									BucketCounts: []uint64{1, 1, 1},
								},
							},
						},
					},
				},
			},
		},
	}

	doc, values := buildSnapshot(rm)

	if got := values[GroupCache]["cache_hits"]; got != 5 {
		t.Fatalf("extracted cache_hits=%v want 5", got)
	}
	if got := values[GroupCache]["cache_build_duration_count"]; got != 3 {
		t.Fatalf("extracted cache_build_duration_count=%v want 3", got)
	}

	docText := string(doc)
	wantParts := []string{
		"# TYPE cache_hits counter\n",
		`cache_hits_total{kind="meta"} 5` + "\n",
		"# TYPE cache_build_duration histogram\n",
		`cache_build_duration_bucket{le="1"} 1` + "\n",
		`cache_build_duration_bucket{le="2"} 2` + "\n",
		`cache_build_duration_bucket{le="+Inf"} 3` + "\n",
		"cache_build_duration_count 3\n",
		"cache_build_duration_sum 6\n",
	}
	for _, part := range wantParts {
		if !strings.Contains(docText, part) {
			t.Fatalf("document missing %q in:\n%s", part, docText)
		}
	}
	if bytes.Contains(doc, []byte("# EOF")) {
		t.Fatalf("Prometheus text document must not contain an EOF trailer:\n%s", docText)
	}
}

// TestGroupValuesKeyContract pins the sanitized metric-name keys for the JSON group handlers.
// ogen core names fold into GroupCore, rescan counters/histograms are read by their raw names.
func TestGroupValuesKeyContract(t *testing.T) {
	rm := &metricdata.ResourceMetrics{
		ScopeMetrics: []metricdata.ScopeMetrics{
			{
				Scope: instrumentation.Scope{Name: "github.com/ogen-go/ogen"},
				Metrics: []metricdata.Metrics{
					{
						Name: "ogen.server.request_count",
						Data: metricdata.Sum[int64]{
							IsMonotonic: true,
							DataPoints: []metricdata.DataPoint[int64]{
								{Value: 7, Attributes: attribute.NewSet(attribute.String("operation", "a"))},
								{Value: 5, Attributes: attribute.NewSet(attribute.String("operation", "b"))},
							},
						},
					},
					{
						Name: "ogen.server.duration",
						Data: metricdata.Histogram[float64]{
							DataPoints: []metricdata.HistogramDataPoint[float64]{
								{Count: 4, Sum: 200},
							},
						},
					},
				},
			},
			{
				Scope: instrumentation.Scope{Name: "github.com/voluminor/yggvault/rescan"},
				Metrics: []metricdata.Metrics{
					{
						Name: "rescan.cycle.duration.seconds",
						Data: metricdata.Histogram[float64]{
							DataPoints: []metricdata.HistogramDataPoint[float64]{
								{Count: 2, Sum: 3},
							},
						},
					},
				},
			},
		},
	}

	_, values := buildSnapshot(rm)

	if got := values[GroupCore]["ogen_server_request_count"]; got != 12 {
		t.Fatalf("core request_count aggregate=%v want 12 (summed across operations)", got)
	}
	if got := values[GroupCore]["ogen_server_duration_count"]; got != 4 {
		t.Fatalf("core duration_count=%v want 4", got)
	}
	if got := values[GroupCore]["ogen_server_duration_sum"]; got != 200 {
		t.Fatalf("core duration_sum=%v want 200 (ms; handler divides by 1000)", got)
	}
	if got := values[GroupRescan]["rescan_cycle_duration_seconds_sum"]; got != 3 {
		t.Fatalf("rescan cycle_duration_seconds_sum=%v want 3", got)
	}
	if got := values[GroupRescan]["rescan_cycle_duration_seconds_count"]; got != 2 {
		t.Fatalf("rescan cycle_duration_seconds_count=%v want 2", got)
	}
}

func TestNewDisabledWhenAllOff(t *testing.T) {
	obj, err := New(&stcfg.ConfigObj{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if obj.Enabled() {
		t.Fatal("telemetry must be disabled when no exposure is configured")
	}
	if obj.MeterProvider() == nil {
		t.Fatal("disabled telemetry must still return a noop provider for ogen")
	}
	if _, ok := obj.InternalOM(); ok {
		t.Fatal("disabled telemetry must report no snapshot")
	}
	obj.Start(context.Background())
	if err := obj.Close(context.Background()); err != nil {
		t.Fatalf("Close on disabled: %v", err)
	}
}

func TestEnabledInternalSnapshot(t *testing.T) {
	cfg := &stcfg.ConfigObj{}
	cfg.Metrics.Web.Internal = true
	cfg.Metrics.SnapshotInterval = time.Second

	obj, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if !obj.Enabled() {
		t.Fatal("telemetry must be enabled when internal exposure is on")
	}

	ctx := context.Background()
	obj.Start(ctx)
	t.Cleanup(func() { _ = obj.Close(ctx) })

	doc, ok := obj.InternalOM()
	if !ok {
		t.Fatal("internal snapshot must be available")
	}
	if !strings.Contains(string(doc), "go_goroutines") {
		t.Fatalf("internal snapshot missing runtime metric:\n%s", doc)
	}
}
