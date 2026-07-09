package server

import (
	"bytes"
	"context"

	"github.com/voluminor/yggvault/mod/server/feedatom"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

func (obj *funcObj) feedSize() int {
	return max(1, int(obj.deps.Config.Web.Pages.FeedSize))
}

func (obj *funcObj) observeFeedBuild(scopeText string, statsObj feedatom.BuildStatsObj) {
	if !statsObj.Truncated() {
		return
	}
	obj.edgeMetrics.recordFeed(statsObj)
	obj.deps.Log.Warn().
		Str("scope", scopeText).
		Int("entries_written", statsObj.EntriesWritten).
		Int("entries_dropped", statsObj.EntriesDropped).
		Int("notes_truncated", statsObj.NotesTruncated).
		Int("body_bytes", statsObj.BodyBytes).
		Msg("feed truncated")
}

// // // // // // // // // //

// GetFeed returns `/feed.xml`, a cached host-sensitive global Atom release feed.
// Absolute self and entry URLs put scheme and entry host into the ETag.
func (obj *funcObj) GetFeed(ctx context.Context, params api.GetFeedParams) (api.GetFeedRes, error) {
	lc := listenerCtxFrom(ctx)
	etag := etagOf("feed", lc.scheme(), lc.entryHost, obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		bodyArr, statsObj, buildErr := feedatom.Build(buildCtx, obj.deps.Storage, obj.deps.State, "", obj.feedSize(), obj.linkCtx(lc))
		if buildErr != nil {
			return nil, buildErr
		}
		obj.observeFeedBuild("global", statsObj)
		return bodyArr, nil
	})
	if err != nil {
		return nil, err
	}
	return &api.GetFeedOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetFeedOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetKeyFeed returns `/{key}/releases.xml`, a cached host-sensitive per-key Atom feed.
// Unknown keys without events return 404; absolute URLs put scheme and entry host into the ETag.
func (obj *funcObj) GetKeyFeed(ctx context.Context, params api.GetKeyFeedParams) (api.GetKeyFeedRes, error) {
	lc := listenerCtxFrom(ctx)
	etag := etagOf("key-feed", params.Key, lc.scheme(), lc.entryHost, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		bodyArr, statsObj, buildErr := feedatom.Build(buildCtx, obj.deps.Storage, obj.deps.State, params.Key, obj.feedSize(), obj.linkCtx(lc))
		if buildErr != nil {
			return nil, buildErr
		}
		obj.observeFeedBuild("key", statsObj)
		return bodyArr, nil
	})
	if err != nil {
		return nil, err
	}
	return &api.GetKeyFeedOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetKeyFeedOK{Data: bytes.NewReader(body)},
	}, nil
}
