package overlay

import (
	"context"
	"fmt"
	"io"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

type universalRawBuilderObj struct {
	archiveObj *archive.Obj
	st         StorageInterface
	format     archive.FormatType
	treeHash   core.HashObj
	key        string
	version    string
}

// Build writes the raw version tree archive with top-dir <key>-<version>/ and no rewrite.
func (b universalRawBuilderObj) Build(ctx context.Context, writerObj io.Writer) error {
	if !validRefSegment(b.key) || !validRefSegment(b.version) {
		return fmt.Errorf("invalid key/version for universal artifact: key=%q version=%q: %w", b.key, b.version, errInvalidArtifactRef)
	}
	entriesArr, err := b.st.ReadTree(ctx, b.treeHash)
	if err != nil {
		return err
	}
	topDir := universalTopDir(b.key, b.version)
	prefixedArr := make([]core.TreeEntryObj, len(entriesArr))
	for i := range entriesArr {
		entryObj := entriesArr[i]
		entryObj.Path = topDir + "/" + entryObj.Path
		prefixedArr[i] = entryObj
	}
	_, err = b.archiveObj.Write(ctx, archive.WriteRequestObj{
		Format:  b.format,
		Writer:  writerObj,
		Entries: prefixedArr,
		Source:  storageBlobSourceObj{st: b.st},
	})
	return err
}

// // // // // // // // // //

// UniversalBuilder builds a raw universal archive with top-dir <key>-<version>/.
func (obj *Obj) UniversalBuilder(st StorageInterface, key string, version string, treeHashObj core.HashObj, format archive.FormatType) ArtifactBuilderInterface {
	return universalRawBuilderObj{
		archiveObj: obj.archiveObj,
		st:         st,
		format:     format,
		treeHash:   treeHashObj,
		key:        key,
		version:    version,
	}
}

// UniversalTopDir returns the raw universal archive top-dir (<key>-<version>/) for Bazel strip_prefix.
func (obj *Obj) UniversalTopDir(key string, version string, _ core.DetectionObj, _ *CandidateObj, _ ListenerCtxObj) string {
	return universalTopDir(key, version)
}
