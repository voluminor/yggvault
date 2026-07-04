package overlay

import (
	"context"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// BlobReaderInterface is a narrow blob read boundary by hash; manifests are small.
type BlobReaderInterface interface {
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}

// DetectorInterface detects one ecosystem from a version tree and returns nil candidate when absent.
type DetectorInterface interface {
	Ecosystem() stcode.EcosystemType
	Detect(ctx context.Context, treeArr []core.TreeEntryObj, src BlobReaderInterface) (*CandidateObj, error)
}

// // // // // // // // // //

// CandidateObj is one ecosystem detection result for a version.
type CandidateObj struct {
	Ecosystem    stcode.EcosystemType
	GoModulePath string         // upstream module path from go.mod
	ComposerName string         // vendor/package from composer.json
	RewriteBlobs []core.HashObj // blobs containing upstream module path, host-independent

	// GoZipBlockReason is non-empty when the tree cannot become a valid Go module zip.
	GoZipBlockReason string
}

// DetectionResultObj is the version detection aggregate stored by rescan in PublishObj.
// Simultaneous Go and Composer matches set Conflict and disable rewrites.
type DetectionResultObj struct {
	Detection    core.DetectionObj
	RewriteBlobs []core.HashObj
	Go           *CandidateObj
	Composer     *CandidateObj
}
