package route

import "strings"

// // // // // // // // // //

const (
	// Health defines the live status path for the entry channel.
	Health = "/health"

	// Info defines the node's public card path.
	Info = "/info"

	// Metrics defines the root path for the metrics HTML index.
	Metrics = "/metrics"

	// OpenAPI defines the JSON spec path.
	OpenAPI = "/openapi.json"
)

const (
	// MetricsCache defines the cache metrics group path.
	MetricsCache = Metrics + "/cache"

	// MetricsCore defines the core metrics group path.
	MetricsCore = Metrics + "/core"

	// MetricsErrors defines the errors metrics group path.
	MetricsErrors = Metrics + "/errors"

	// MetricsInternal defines the full OpenMetrics exposition path.
	MetricsInternal = Metrics + "/internal"

	// MetricsRescan defines the rescan metrics group path.
	MetricsRescan = Metrics + "/rescan"
)

// // // // // // // // // //

// IsMetrics reports whether the path belongs to the metrics tree.
func IsMetrics(pathText string) bool {
	return pathText == Metrics || strings.HasPrefix(pathText, Metrics+"/")
}

// IsService reports whether a root service path must bypass nested static handling.
func IsService(pathText string) bool {
	switch pathText {
	case Health, Info, OpenAPI:
		return true
	default:
		return IsMetrics(pathText)
	}
}
