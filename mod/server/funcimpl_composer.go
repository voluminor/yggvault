package server

import (
	"context"

	"github.com/voluminor/yggvault/mod/server/composer"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// GetComposerPackages returns `/packages.json` with metadata URL and composer names.
// It is a pure cross-key transformation without storage reads.
func (obj *funcObj) GetComposerPackages(ctx context.Context, params api.GetComposerPackagesParams) (api.GetComposerPackagesRes, error) {
	etag := etagOf("composer-packages", obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	built, err := obj.cachedObj(ctx, etag, func(context.Context) (any, error) {
		return composer.BuildPackages(obj.deps.Overlay, obj.deps.Composer), nil
	})
	if err != nil {
		return nil, err
	}
	return &api.ComposerPackagesObjHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     *built.(*api.ComposerPackagesObj),
	}, nil
}

// GetComposerPackageList returns `/packages/list.json` with optional prefix or glob filtering.
// Filter length is gated because matching work and cache-key size depend on it.
// ETag = hash(cross freshness + filter).
func (obj *funcObj) GetComposerPackageList(ctx context.Context, params api.GetComposerPackageListParams) (api.GetComposerPackageListRes, error) {
	filter := params.Filter.Value
	if err := composer.CheckFilter(filter); err != nil {
		return nil, serr.ErrBadInput
	}
	etag := etagOf("composer-list", filter, obj.crossFreshness())
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}
	built, err := obj.cachedObj(ctx, etag, func(context.Context) (any, error) {
		return composer.BuildPackageList(obj.deps.Overlay, obj.deps.Composer, filter), nil
	})
	if err != nil {
		return nil, err
	}
	return &api.ComposerPackageListObjHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     *built.(*api.ComposerPackageListObj),
	}, nil
}

// GetComposerP2Package returns a cached host-sensitive p2 document for a package.
// Dev variants return an empty version set, bad files or unknown names return 404, and dist URLs are absolute.
func (obj *funcObj) GetComposerP2Package(ctx context.Context, params api.GetComposerP2PackageParams) (api.GetComposerP2PackageRes, error) {
	resolveObj, err := composer.Resolve(obj.deps.Composer, params.Vendor, params.PackageFile)
	if err != nil {
		return nil, err
	}

	lc := listenerCtxFrom(ctx)
	devText := "stable"
	if resolveObj.Dev {
		devText = "dev"
	}
	etag := etagOf("composer-p2", resolveObj.Name, devText, lc.scheme(), lc.entryHost, obj.keyFreshness(resolveObj.Key))
	if condMatch(params.IfNoneMatch, etag) {
		return &api.NotModifiedRespObj{}, nil
	}

	built, err := obj.cachedObj(ctx, etag, func(buildCtx context.Context) (any, error) {
		return composer.BuildP2(buildCtx, obj.deps.Storage, obj.deps.Overlay, resolveObj, obj.linkCtx(lc), obj.overlayListenerCtx(lc).ListenerID)
	})
	if err != nil {
		return nil, err
	}
	return &api.ComposerP2ObjHeaders{
		ETag:         api.NewOptString(etag),
		CacheControl: api.NewOptString(obj.cacheControlData()),
		Response:     *built.(*api.ComposerP2Obj),
	}, nil
}
