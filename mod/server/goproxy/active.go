package goproxy

import (
	"context"

	"golang.org/x/mod/semver"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/internal/util"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/pager"
)

// // // // // // // // // //

func majorBucket(version string) string {
	switch m := semver.Major(version); m {
	case "v0", "v1":
		return ""
	default:
		return m
	}
}

// // // // // // // // // //

func goActive(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, major string, lc overlay.ListenerCtxObj) (core.DetectionObj, *overlay.CandidateObj, bool, error) {
	if majorBucket(version) != major {
		return core.DetectionObj{}, nil, false, nil
	}
	detectionObj, ok, err := store.GetDetection(ctx, key, version)
	if err != nil || !ok {
		return core.DetectionObj{}, nil, false, err
	}
	goCand, _ := overlay.CandidateFromDetection(detectionObj)
	return detectionObj, goCand, overlayObj.GoPublishable(key, version, detectionObj, goCand, lc), nil
}

func goPublishable(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, major string, lc overlay.ListenerCtxObj) (bool, error) {
	_, _, active, err := goActive(ctx, store, overlayObj, key, version, major, lc)
	return active, err
}

func versionsInMajor(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, major string, lc overlay.ListenerCtxObj) ([]string, core.VersionObj, bool, error) {
	nameArr := make([]string, 0, pager.PageSize)
	var latest core.VersionObj
	found := false
	err := pager.EachVersion(ctx, store, key, func(versionObj core.VersionObj) (bool, error) {
		if !util.IsCanonicalSemver(versionObj.Version) || majorBucket(versionObj.Version) != major {
			return false, nil
		}
		active, activeErr := goPublishable(ctx, store, overlayObj, key, versionObj.Version, major, lc)
		if activeErr != nil {
			return false, activeErr
		}
		if !active {
			return false, nil
		}
		nameArr = append(nameArr, versionObj.Version)
		switch {
		case !found:
			latest, found = versionObj, true
		default:
			if cmp, cerr := util.CompareSemver(versionObj.Version, latest.Version); cerr == nil && cmp > 0 {
				latest = versionObj
			}
		}
		return len(nameArr) >= pager.MaxVersions, nil
	})
	if err != nil {
		return nil, core.VersionObj{}, false, err
	}
	return nameArr, latest, found, nil
}
