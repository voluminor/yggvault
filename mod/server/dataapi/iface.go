package dataapi

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
)

// // // // // // // // // //

// ArtifactBuilderInterface and HotFileObj are storage aliases for artifact builders and disk streaming.
type (
	ArtifactBuilderInterface = storage.ArtifactBuilderInterface
	HotFileObj               = storage.HotFileObj
)

// // // // // // // // // //

// StateReaderInterface is the minimal state surface for data APIs.
type StateReaderInterface interface {
	KeyState(key string) (state.KeyStateObj, bool)
	KeyStates() []state.KeyStateObj
}

// VersionReaderInterface provides keyset access for paginated release lists.
type VersionReaderInterface interface {
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
}

// DetailReaderInterface reads release-detail data: version, detection, and artifacts.
type DetailReaderInterface interface {
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
	ListArtifacts(ctx context.Context, key string, version string) ([]core.ArtifactObj, error)
}

// ArtifactReaderInterface materializes and locates canonical archives.
// ReadTree and ReadBlob are also used by overlay builders; GetArtifact and EnsureArtifactFile satisfy artifactio.StoreInterface.
type ArtifactReaderInterface interface {
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
	RewriteSet(ctx context.Context, key string, version string) ([]core.HashObj, error)
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	EnsureArtifactFile(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) (*HotFileObj, error)
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}
