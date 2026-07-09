package server

import (
	"context"

	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/cache"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/mod/telemetry"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// ArtifactBuilderInterface and HotFileObj are storage aliases used by overlay builders and streaming artifact serving.
type (
	ArtifactBuilderInterface = storage.ArtifactBuilderInterface
	HotFileObj               = storage.HotFileObj
)

// // // // // // // // // //

// DataStoreInterface is the storage read surface for data APIs: artifacts, detection, versions, and feeds.
// Signatures match storage.Obj because it is the only production provider.
type DataStoreInterface interface {
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	ListVersionsKeysetBefore(ctx context.Context, key string, includeDeleted bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error)
	ListVersionsPage(ctx context.Context, key string, includeDeleted bool, limit int, offset int) ([]core.VersionObj, error)
	CountVersions(ctx context.Context, key string) (uint64, error)
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	LatestVersion(ctx context.Context, key string) (core.VersionObj, bool, error)
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	ListArtifacts(ctx context.Context, key string, version string) ([]core.ArtifactObj, error)
	EnsureArtifactFile(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) (*HotFileObj, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
	RewriteSet(ctx context.Context, key string, version string) ([]core.HashObj, error)
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
	ListPublishFeed(ctx context.Context, key string, limit int) ([]core.FeedEventObj, error)
}

// DataStateInterface exposes health, classification, and version snapshots for data APIs.
type DataStateInterface interface {
	Health() state.HealthViewObj
	Snapshot() state.SnapshotObj
	KeyState(key string) (state.KeyStateObj, bool)
	KeyStates() []state.KeyStateObj
	Checksums() state.ChecksumSetObj
}

// ComposerNamesInterface exposes cross-key composer names and name-to-winner mapping for serve-time p2.
type ComposerNamesInterface interface {
	ComposerPackageNames() []string
	ComposerKeyForName(name string) (string, bool)
}

// // // // // // // // // //

// DepsObj contains edge server dependencies wired by main.go and passed into New.
type DepsObj struct {
	Config    *stconf.ConfigObj
	Storage   DataStoreInterface
	State     DataStateInterface
	Overlay   *overlay.Obj
	Telemetry *telemetry.Obj
	Composer  ComposerNamesInterface
	Mesh      mesh.NodeInterface
	Log       zerolog.Logger
	// Cache is a shared RAM cache for built byte bodies. Nil disables cache and singleflight deduplication.
	Cache *cache.Obj
	// BuildGate limits detached builds shared by Cache and the typed object cache.
	BuildGate *cache.BuildGateObj
}
