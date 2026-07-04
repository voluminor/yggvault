package goproxy

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage"
)

// // // // // // // // // //

// ArtifactBuilderInterface and HotFileObj are storage aliases for go-zip builders and disk streaming.
type (
	ArtifactBuilderInterface = storage.ArtifactBuilderInterface
	HotFileObj               = storage.HotFileObj
)

// // // // // // // // // //

// StorageReaderInterface is the storage read surface for Go overlay detection, versions, objects, and artifact serving.
type StorageReaderInterface interface {
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	RewriteSet(ctx context.Context, key string, version string) ([]core.HashObj, error)
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	EnsureArtifactFile(ctx context.Context, keyObj core.ArtifactKeyObj, builderObj ArtifactBuilderInterface) (*HotFileObj, error)
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}
