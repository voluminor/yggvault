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

// OpenArtifact opens the raw universal archive for streaming, reusing an artifact key already resolved by
// the archive route so a full GET does not re-query GetArtifact.
func OpenArtifact(ctx context.Context, store ArtifactReaderInterface, overlayObj *overlay.Obj, keyObj core.ArtifactKeyObj) (artifactio.OpenObj, error) {
	key, version := keyObj.Key, keyObj.Version
	format := archive.FormatType(keyObj.ArtifactKind)
	versionObj, ok, err := store.GetVersion(ctx, key, version)
	if err != nil {
		return artifactio.OpenObj{}, err
	}
	if !ok {
		return artifactio.OpenObj{}, serr.ErrNotFound
	}
	builderObj := overlayObj.UniversalBuilder(store, key, version, versionObj.TreeHash, format)
	return artifactio.OpenResolved(ctx, store, keyObj, builderObj, versionObj.IngestTS.UTC())
}
