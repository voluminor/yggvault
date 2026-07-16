package webui

import (
	"time"

	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

type metricGroupObj struct {
	name     string
	url      string
	detail   string
	internal bool
	ygg      bool
}

var cMetricGroupArr = []metricGroupObj{
	{name: "core", url: route.MetricsCore, detail: "request, error, and latency totals", internal: false},
	{name: "cache", url: route.MetricsCache, detail: "metadata/hot-file RAM cache and storage engine", internal: false},
	{name: "errors", url: route.MetricsErrors, detail: "layer errors and upstream failures", internal: false},
	{name: "rescan", url: route.MetricsRescan, detail: "integrator cycles and materialization", internal: false},
	{name: "ygg", url: route.MetricsYgg, detail: "yggdrasil peers, traffic, and isolation events", ygg: true},
	{name: "internal", url: route.MetricsInternal, detail: "private runtime telemetry", internal: true},
}

// // // // // // // // // //

// Metrics builds the metric group index honoring the current entry's flags.
// Groups that would return 404 are hidden from navigation: internal behind its own gate,
// the ygg group additionally requires a running Yggdrasil node.
// refreshInterval sets the live polling period for JSON groups on the page.
func Metrics(ctxObj view.ContextObj, publicEnabled bool, internalEnabled bool, yggEnabled bool, refreshInterval time.Duration) ([]byte, error) {
	rnd, err := renderer()
	if err != nil {
		return nil, err
	}

	groupArr := make([]view.ActionObj, 0, len(cMetricGroupArr))
	for i := range cMetricGroupArr {
		descObj := cMetricGroupArr[i]
		if descObj.internal && !internalEnabled {
			continue
		}
		if !descObj.internal && !publicEnabled {
			continue
		}
		if descObj.ygg && !yggEnabled {
			continue
		}
		kind := "json"
		if descObj.internal {
			kind = "openmetrics"
		}
		groupArr = append(groupArr, view.ActionObj{
			Label:  descObj.name,
			Detail: descObj.detail,
			URL:    descObj.url,
			Kind:   kind,
		})
	}

	return rnd.MetricsIndex(view.MetricsObj{
		Context:         ctxObj,
		Endpoints:       groupArr,
		RefreshInterval: refreshInterval,
	})
}
