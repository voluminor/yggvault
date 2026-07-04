package telemetry

import (
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"go.opentelemetry.io/otel/metric"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
)

// // // // // // // // // //

// Group defines the metric groups; public values match the JSON routes /metrics/<group>.
type Group string

// Groups separate public series from the internal exposition.
// Core collects ogen and unknown scopes, internal holds push and Go runtime self-metrics.
const (
	GroupCore     Group = "core"
	GroupCache    Group = "cache"
	GroupRescan   Group = "rescan"
	GroupErrors   Group = "errors"
	GroupInternal Group = "internal"
)

// cScopePrefix separates our own scopes from external scopes, which go to core.
const cScopePrefix = "github.com/voluminor/yggvault/"

const cPushPath = "/api/v1/import/prometheus"

// knownGroupsArr fixes the snapshot build order; knownGroupSetObj is derived from it.
var knownGroupsArr = []Group{GroupCore, GroupCache, GroupRescan, GroupErrors, GroupInternal}

var knownGroupSetObj = func() map[Group]bool {
	setObj := make(map[Group]bool, len(knownGroupsArr))
	for _, groupObj := range knownGroupsArr {
		setObj[groupObj] = true
	}
	return setObj
}()

// //

// Obj owns the SDK state, prebuilt Prometheus snapshots and the optional push to VictoriaMetrics.
type Obj struct {
	enabled       bool
	provider      metric.MeterProvider
	sdkProvider   *sdkmetric.MeterProvider
	reader        *sdkmetric.ManualReader
	snapshotEvery time.Duration

	pushEnabled bool
	pushURL     string
	pushEvery   time.Duration
	pushClient  *http.Client

	pushTotal    metric.Int64Counter
	pushFailures metric.Int64Counter
	pushBytes    metric.Int64Counter

	snap     atomic.Pointer[snapshotObj]
	doneCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
}

// doc holds the full text exposition for /metrics/internal and the VictoriaMetrics push.
// values holds per-group aggregates for the JSON endpoints.
type snapshotObj struct {
	doc    []byte
	values map[Group]map[string]float64
}
