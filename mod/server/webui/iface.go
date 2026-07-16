package webui

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

// StateReaderInterface is the narrow state read surface for catalog, key, and version pages.
type StateReaderInterface interface {
	KeyState(key string) (state.KeyStateObj, bool)
	KeyStates() []state.KeyStateObj
	Checksums() state.ChecksumSetObj
}

// VersionReaderInterface reads key version history for the key page via keyset pagination.
type VersionReaderInterface interface {
	CountVersions(ctx context.Context, key string) (uint64, error)
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	ListVersionsKeysetBefore(ctx context.Context, key string, includeDeleted bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
}

// DetailReaderInterface reads one version with metadata, artifacts, detection, and history window.
// The two keyset directions fetch the immediate older/newer neighbors of a version without scanning.
type DetailReaderInterface interface {
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	GetDetection(ctx context.Context, key string, version string) (core.DetectionObj, bool, error)
	ListArtifacts(ctx context.Context, key string, version string) ([]core.ArtifactObj, error)
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	ListVersionsKeysetBefore(ctx context.Context, key string, includeDeleted bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error)
}

// OverlayInterface exposes host-dependent overlay operations already bound to a listener.
type OverlayInterface interface {
	GoPublishable(key string, version string, detectionObj core.DetectionObj, candidateObj *overlay.CandidateObj) bool
	TargetModulePath(key string, version string) string
	UniversalTopDir(key string, version string, detectionObj core.DetectionObj, candidateObj *overlay.CandidateObj) string
}
