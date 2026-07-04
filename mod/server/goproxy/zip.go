package goproxy

import (
	"context"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// ZipHandleObj carries a resolved go-zip lookup so a follow-up Open reuses the detection and artifact
// key without re-querying them: a full GET resolves publishable-detection and artifact metadata once,
// then materializes with only GetVersion + RewriteSet left.
type ZipHandleObj struct {
	artifactObj  core.ArtifactObj
	keyObj       core.ArtifactKeyObj
	detectionObj core.DetectionObj
	goCandObj    *overlay.CandidateObj
}

// Artifact returns the located artifact metadata for ETag/HEAD/Range decisions before materialization.
func (h ZipHandleObj) Artifact() core.ArtifactObj {
	return h.artifactObj
}

// // // // // // // // // //

// ResolveZip locates a go-zip (publishable check + artifact metadata) without materialization, so callers
// answer 304/HEAD/Range before building. Missing versions or inactive Go overlays return found=false.
func ResolveZip(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, major string, lc overlay.ListenerCtxObj) (ZipHandleObj, bool, error) {
	detectionObj, goCandObj, active, err := goActive(ctx, store, overlayObj, key, version, major, lc)
	if err != nil || !active {
		return ZipHandleObj{}, false, err
	}
	artifactObj, keyObj, ok, err := artifactio.LocateKey(ctx, store, stcode.MaterializerGo.String(), string(archive.FormatZip), key, version, lc.ListenerID)
	if err != nil || !ok {
		return ZipHandleObj{}, false, err
	}
	return ZipHandleObj{artifactObj: artifactObj, keyObj: keyObj, detectionObj: detectionObj, goCandObj: goCandObj}, true, nil
}

// Open materializes the resolved go-zip for streaming, reusing the handle's detection and artifact key.
// Only GetVersion (tree hash + mod time) and RewriteSet remain before EnsureArtifactFile.
func (h ZipHandleObj) Open(ctx context.Context, store StorageReaderInterface, overlayObj *overlay.Obj, key string, version string, lc overlay.ListenerCtxObj) (artifactio.OpenObj, error) {
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}
	if !ok {
		return artifactio.OpenObj{}, serr.ErrNotFound
	}
	rewriteArr, err := store.RewriteSet(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}
	builderObj := overlayObj.GoModuleZipBuilder(store, key, version, versionObj.TreeHash, h.detectionObj, h.goCandObj, rewriteArr, lc)
	return artifactio.OpenResolved(ctx, store, h.keyObj, builderObj, versionObj.IngestTS.UTC())
}
