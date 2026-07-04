package server

import (
	"bytes"
	"context"

	"github.com/voluminor/yggvault/mod/server/mirror"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// GetMirrorLatest returns `/{key}/latest`, the authoritative latest version as text.
// Unknown keys with no versions return 404.
func (obj *funcObj) GetMirrorLatest(ctx context.Context, params api.GetMirrorLatestParams) (api.GetMirrorLatestRes, error) {
	etag := etagOf("mirror-latest", params.Key, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		return mirror.BuildLatest(buildCtx, obj.deps.Storage, obj.deps.State, params.Key)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetMirrorLatestOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetMirrorLatestOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetMirrorList returns `/{key}/list`, newest-first text with one version per line.
// It builds through keyset pages in RAM; unknown keys with no versions return 404.
func (obj *funcObj) GetMirrorList(ctx context.Context, params api.GetMirrorListParams) (api.GetMirrorListRes, error) {
	etag := etagOf("mirror-list", params.Key, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		return mirror.BuildList(buildCtx, obj.deps.Storage, obj.deps.State, params.Key)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetMirrorListOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetMirrorListOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetMirrorListFull returns `/{key}/list/full`, a newest-first JSON array of version_full_entry objects.
// It builds in RAM; unknown keys with no versions return 404.
func (obj *funcObj) GetMirrorListFull(ctx context.Context, params api.GetMirrorListFullParams) (api.GetMirrorListFullRes, error) {
	etag := etagOf("mirror-listfull", params.Key, obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		return mirror.BuildListFull(buildCtx, obj.deps.Storage, obj.deps.State, params.Key)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetMirrorListFullOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetMirrorListFullOK{Data: bytes.NewReader(body)},
	}, nil
}
