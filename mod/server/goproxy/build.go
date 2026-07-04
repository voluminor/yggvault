package goproxy

import (
	"context"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// LatestInfo returns `@latest` as typed go-info for the latest publishable version in a major.
// Majors without publishable versions return serr.ErrNotFound.
func LatestInfo(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, major string, lc overlay.ListenerCtxObj) (api.GoInfoObj, error) {
	_, latestObj, ok, err := versionsInMajor(ctx, store, overlayObj, key, major, lc)
	if err != nil {
		return api.GoInfoObj{}, err
	}
	if !ok {
		return api.GoInfoObj{}, serr.ErrNotFound
	}
	return overlay.GoInfo(latestObj.Version, latestObj.IngestTS), nil
}

// // // // // // // // // //

// BuildVersionList returns `@v/list` with publishable versions for a major.
// Majors without publishable versions return serr.ErrNotFound.
func BuildVersionList(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, major string, lc overlay.ListenerCtxObj) ([]byte, error) {
	nameArr, _, ok, err := versionsInMajor(ctx, store, overlayObj, key, major, lc)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, serr.ErrNotFound
	}
	return overlay.RenderGoVersionList(nameArr), nil
}

// // // // // // // // // //

// BuildInfo returns `@v/<version>.info` JSON for one active version.
// Missing or inactive versions return serr.ErrNotFound.
func BuildInfo(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, major string, lc overlay.ListenerCtxObj) ([]byte, error) {
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, serr.ErrNotFound
	}
	active, err := goPublishable(ctx, store, overlayObj, key, version, major, lc)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, serr.ErrNotFound
	}
	return overlay.RenderGoInfo(version, versionObj.IngestTS)
}

// BuildMod returns the rewritten or raw `@v/<version>.mod` body.
// Missing or inactive versions return serr.ErrNotFound; rewrite failures pass through to the caller.
func BuildMod(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, major string, lc overlay.ListenerCtxObj) ([]byte, error) {
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, serr.ErrNotFound
	}
	detectionObj, goCand, active, err := goActive(ctx, store, overlayObj, key, version, major, lc)
	if err != nil {
		return nil, err
	}
	if !active {
		return nil, serr.ErrNotFound
	}
	return overlayObj.RenderGoMod(ctx, store, key, version, versionObj.TreeHash, detectionObj, goCand, lc)
}
