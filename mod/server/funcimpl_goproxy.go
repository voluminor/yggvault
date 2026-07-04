package server

import (
	"bytes"
	"context"
	"net/http"
	"strings"

	"golang.org/x/mod/module"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/goproxy"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// GetGoLatest returns `/{key}[/vN]/@latest`, the latest canonical Go version for a major.
// The major comes from path stripping (`/{key}/vN/@latest`); no active canonical versions returns 404.
func (obj *funcObj) GetGoLatest(ctx context.Context, params api.GetGoLatestParams) (api.GetGoLatestRes, error) {
	major := majorFrom(ctx)
	lc := obj.overlayListenerCtx(listenerCtxFrom(ctx))
	etag := etagOf("go-latest", params.Key, major, lc.ListenerID.String(), obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	// LatestInfo scans all versions in a major bucket, so cache the typical result by ETag like composer p2.
	built, err := obj.cachedObj(ctx, etag, func(buildCtx context.Context) (any, error) {
		infoObj, buildErr := goproxy.LatestInfo(buildCtx, obj.deps.Storage, obj.deps.Overlay, params.Key, major, lc)
		if buildErr != nil {
			return nil, buildErr
		}
		return infoObj, nil
	})
	if err != nil {
		return nil, err
	}
	return &api.GoInfoObjHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     built.(api.GoInfoObj),
	}, nil
}

// GetGoVersionList returns cached canonical Go versions for a major in descending order.
// The major comes from context; no active canonical versions returns 404.
func (obj *funcObj) GetGoVersionList(ctx context.Context, params api.GetGoVersionListParams) (api.GetGoVersionListRes, error) {
	major := majorFrom(ctx)
	lc := obj.overlayListenerCtx(listenerCtxFrom(ctx))
	etag := etagOf("go-list", params.Key, major, lc.ListenerID.String(), obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		return goproxy.BuildVersionList(buildCtx, obj.deps.Storage, obj.deps.Overlay, params.Key, major, lc)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetGoVersionListOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetGoVersionListOK{Data: bytes.NewReader(body)},
	}, nil
}

// GetVersionArtifact dispatches go-proxy version files by extension.
// `.info` and `.mod` are cached bodies, while `.zip` streams the module zip from disk.
// Versions are unescaped before lookup; bad extension, encoding, or inactive versions return 404.
func (obj *funcObj) GetVersionArtifact(ctx context.Context, params api.GetVersionArtifactParams) (api.GetVersionArtifactRes, error) {
	major := majorFrom(ctx)
	lc := obj.overlayListenerCtx(listenerCtxFrom(ctx))

	var rawVersion, ext string
	switch {
	case strings.HasSuffix(params.VersionFile, ".info"):
		rawVersion, ext = strings.TrimSuffix(params.VersionFile, ".info"), ".info"
	case strings.HasSuffix(params.VersionFile, ".mod"):
		rawVersion, ext = strings.TrimSuffix(params.VersionFile, ".mod"), ".mod"
	case strings.HasSuffix(params.VersionFile, ".zip"):
		rawVersion, ext = strings.TrimSuffix(params.VersionFile, ".zip"), ".zip"
	default:
		return nil, serr.ErrNotFound
	}

	version, err := module.UnescapeVersion(rawVersion)
	if err != nil {
		return nil, serr.ErrNotFound
	}

	if ext == ".zip" {
		return obj.goZip(ctx, params.Key, version, major, lc, params.IfNoneMatch, params.Range, params.IfRange)
	}

	etag := etagOf("go"+ext, params.Key, version, lc.ListenerID.String(), obj.keyFreshness(params.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	body, err := obj.cachedBytes(ctx, etag, func(buildCtx context.Context) ([]byte, error) {
		if ext == ".info" {
			return goproxy.BuildInfo(buildCtx, obj.deps.Storage, obj.deps.Overlay, params.Key, version, major, lc)
		}
		return goproxy.BuildMod(buildCtx, obj.deps.Storage, obj.deps.Overlay, params.Key, version, major, lc)
	})
	if err != nil {
		return nil, err
	}
	return &api.GetVersionArtifactOKHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     api.GetVersionArtifactOK{Data: bytes.NewReader(body)},
	}, nil
}

// // // // // // // // // //

func (obj *funcObj) goZip(ctx context.Context, key string, version string, major string, lc overlay.ListenerCtxObj, ifNoneMatch api.OptString, rangeHeader api.OptString, ifRange api.OptString) (api.GetVersionArtifactRes, error) {
	handleObj, found, err := goproxy.ResolveZip(ctx, obj.deps.Storage, obj.deps.Overlay, key, version, major, lc)
	if err != nil {
		return nil, err
	}
	if !found {
		return nil, serr.ErrNotFound
	}
	artObj := handleObj.Artifact()
	gate := gateArtifact(ctx, artObj, ifNoneMatch, rangeHeader, ifRange)
	if gate.notModified {
		return &api.NotModifiedRespObj{}, nil
	}
	// HEAD responds from metadata; a cold go-zip build is unnecessary for size and ETag.
	if gate.headOnly {
		return &api.GetVersionArtifactOKHeaders{
			AcceptRanges:  api.NewOptString("bytes"),
			ETag:          api.NewOptString(gate.etag),
			CacheControl:  api.NewOptString(obj.cacheControlData()),
			ContentLength: api.NewOptInt64(int64(artObj.SizeBytes)),
			Response:      api.GetVersionArtifactOK{Data: http.NoBody},
		}, nil
	}
	if !gate.spec.satisfiable {
		return nil, serr.ErrRangeNotSatisfiable
	}
	openObj, err := handleObj.Open(ctx, obj.deps.Storage, obj.deps.Overlay, key, version, lc)
	if err != nil {
		return nil, err
	}
	cacheControl := obj.cacheControlData()
	lastModified := openObj.ModTime.UTC().Format(http.TimeFormat)
	if gate.spec.partial {
		return partialResp(openObj.Body, gate.spec, gate.etag, cacheControl, lastModified)
	}
	return &api.GetVersionArtifactOKHeaders{
		AcceptRanges:  api.NewOptString("bytes"),
		ETag:          api.NewOptString(gate.etag),
		CacheControl:  api.NewOptString(cacheControl),
		LastModified:  api.NewOptString(lastModified),
		ContentLength: api.NewOptInt64(int64(artObj.SizeBytes)),
		Response:      api.GetVersionArtifactOK{Data: openObj.Body},
	}, nil
}
