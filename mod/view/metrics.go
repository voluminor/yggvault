package view

import (
	"html/template"
	"time"
)

// // // // // // // // // //

// MetricsObj is the external input for metrics.html.
type MetricsObj struct {
	Context         ContextObj
	Endpoints       []ActionObj
	RefreshInterval time.Duration
}

// // // // // // // // // //

type metricsTemplateObj struct {
	Head        headObj
	Groups      []ActionObj
	HasJSON     bool
	RefreshMS   int64
	RefreshText string
}

// // // // // // // // // //

// refreshInterval floors live polling at ten seconds so the metrics page does not create noticeable load.
func refreshInterval(interval time.Duration) time.Duration {
	if interval < 10*time.Second {
		return 10 * time.Second
	}
	return interval
}

// // // // // // // // // //

// buildMetrics derives template-only fields for metrics.html.
func buildMetrics(inputObj MetricsObj, css template.CSS) metricsTemplateObj {
	hasJSON := false
	for i := range inputObj.Endpoints {
		if inputObj.Endpoints[i].Kind == "json" {
			hasJSON = true
			break
		}
	}
	interval := refreshInterval(inputObj.RefreshInterval)
	return metricsTemplateObj{
		Head:        head(inputObj.Context, css, "metrics - "+inputObj.Context.Service.Name, "telemetry endpoints and live metrics", "og.png", cleanHome(inputObj.Context), ""),
		Groups:      inputObj.Endpoints,
		HasJSON:     hasJSON,
		RefreshMS:   interval.Milliseconds(),
		RefreshText: interval.String(),
	}
}
