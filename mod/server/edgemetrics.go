package server

import (
	"context"

	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/server/feedatom"
	"github.com/voluminor/yggvault/mod/server/sitemap"
)

// // // // // // // // // //

type edgeMetricsObj struct {
	sitemapTruncations metric.Int64Counter
	feedNotesTruncated metric.Int64Counter
	feedOverflow       metric.Int64Counter
}

func newEdgeMetrics(meterObj metric.Meter) (*edgeMetricsObj, error) {
	if meterObj == nil {
		return nil, nil
	}
	metricsObj := &edgeMetricsObj{}
	var err error
	if metricsObj.sitemapTruncations, err = meterObj.Int64Counter("server_sitemap_truncations_total",
		metric.WithDescription("sitemap builds truncated by URL or byte caps")); err != nil {
		return nil, err
	}
	if metricsObj.feedNotesTruncated, err = meterObj.Int64Counter("server_feed_entries_truncated_total",
		metric.WithDescription("feed entries with release notes truncated before markdown rendering")); err != nil {
		return nil, err
	}
	if metricsObj.feedOverflow, err = meterObj.Int64Counter("server_feed_overflow_total",
		metric.WithDescription("feed events dropped because the Atom document byte cap was reached")); err != nil {
		return nil, err
	}
	return metricsObj, nil
}

func (obj *edgeMetricsObj) recordSitemap(statsObj sitemap.BuildStatsObj) {
	if obj == nil || !statsObj.Truncated() {
		return
	}
	obj.sitemapTruncations.Add(context.Background(), 1)
}

func (obj *edgeMetricsObj) recordFeed(statsObj feedatom.BuildStatsObj) {
	if obj == nil {
		return
	}
	if statsObj.NotesTruncated > 0 {
		obj.feedNotesTruncated.Add(context.Background(), int64(statsObj.NotesTruncated))
	}
	if statsObj.EntriesDropped > 0 {
		obj.feedOverflow.Add(context.Background(), int64(statsObj.EntriesDropped))
	}
}
