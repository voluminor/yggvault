package server

import (
	"context"
	"time"

	"github.com/voluminor/yggvault/mod/cache"
)

// // // // // // // // // //

const cMinCacheTTL = 30 * time.Second

func (obj *funcObj) cacheTTL() time.Duration {
	ttl := cache.SecondsToNextRescan(obj.deps.State.Snapshot().LastRescan, obj.deps.Config.Rescan.Interval, time.Now())
	if ttl < cMinCacheTTL {
		return cMinCacheTTL
	}
	return ttl
}

// // // // // // // // // //

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

func (obj *funcObj) cachedObj(ctx context.Context, etag string, build func(context.Context) (any, error)) (any, error) {
	if obj.deps.Cache == nil {
		return build(ctx)
	}
	return obj.objCache.getOrBuild(ctx, etag, obj.cacheTTL(), build)
}
