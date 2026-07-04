package dataapi

import (
	"context"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/artifactio"
	"github.com/voluminor/yggvault/mod/server/serr"
)

// // // // // // // // // //

// OpenArtifact opens the canonical universal archive for streaming, reusing an artifact key/metadata
// already resolved by artifactio.LocateKey so a full GET does not re-query GetArtifact.
// listenerCtxObj carries host context for host-sensitive rewrites; artifactio owns body safety and materialization errors.
func OpenArtifact(ctx context.Context, store ArtifactReaderInterface, overlayObj *overlay.Obj, keyObj core.ArtifactKeyObj, listenerCtxObj overlay.ListenerCtxObj) (artifactio.OpenObj, error) {
	key, version := keyObj.Key, keyObj.Version
	format := archive.FormatType(keyObj.ArtifactKind)
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}
	if !ok {
		return artifactio.OpenObj{}, serr.ErrNotFound
	}
	detectionObj, _, err := store.GetDetection(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}
	goCand, _ := overlay.CandidateFromDetection(detectionObj)
	rewriteArr, err := store.RewriteSet(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}

	builderObj := overlayObj.CanonicalUniversalBuilder(store, key, version, versionObj.TreeHash, detectionObj, goCand, rewriteArr, listenerCtxObj, format)
	return artifactio.OpenResolved(ctx, store, keyObj, builderObj, versionObj.IngestTS.UTC())
}
