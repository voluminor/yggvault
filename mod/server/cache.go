package server

import (
	"context"
	"time"

	"github.com/voluminor/yggvault/mod/cache"
)

// // // // // // // // // //

// cMinCacheTTL is the floor for the server-side byte/obj cache. SecondsToNextRescan returns 0 when a rescan
// is overdue, unknown, or disabled; without a floor every request would rebuild feeds, sitemaps and OG banners
// exactly while the node is degraded, the cheapest CPU-DoS vector. Flooring is safe because cache keys are ETags
// that encode content freshness (see crossFreshness/keyFreshness): when content changes the key changes too, so a
// stale entry is never served under a live key. This floor is server-only and does not touch client Cache-Control.
const cMinCacheTTL = 30 * time.Second

func (obj *funcObj) cacheTTL() time.Duration {
	ttl := cache.SecondsToNextRescan(obj.deps.State.Snapshot().LastRescan, obj.deps.Config.Rescan.Interval, time.Now())
	if ttl < cMinCacheTTL {
		return cMinCacheTTL
	}
	return ttl
}

// // // // // // // // // //

// cachedBytes stores ready HTTP/XML/JSON/HTML bytes in the shared size-budgeted byte-cache.
func (obj *funcObj) cachedBytes(ctx context.Context, etag string, build func(context.Context) ([]byte, error)) ([]byte, error) {
	if obj.deps.Cache == nil {
		return build(ctx)
	}
	entryObj, _, err := obj.deps.Cache.GetOrBuild(ctx, etag, obj.cacheTTL(), func(buildCtx context.Context) (cache.EntryObj, error) {
		body, buildErr := build(buildCtx)
		if buildErr != nil {
			return cache.EntryObj{}, buildErr
		}
		return cache.EntryObj{Payload: body, ETag: etag}, nil
	})
	if err != nil {
		return nil, err
	}
	return entryObj.Payload, nil
}

// // // // // // // // // //

// cachedObj stores typed ogen objects; ready bytes must go through cachedBytes.
func (obj *funcObj) cachedObj(ctx context.Context, etag string, build func(context.Context) (any, error)) (any, error) {
	if obj.deps.Cache == nil {
		return build(ctx)
	}
	return obj.objCache.getOrBuild(ctx, etag, obj.cacheTTL(), build)
}
