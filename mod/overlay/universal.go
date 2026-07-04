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

// //

type universalRewrittenBuilderObj struct {
	archiveObj   *archive.Obj
	st           StorageInterface
	format       archive.FormatType
	treeHash     core.HashObj
	topDir       string
	rewriteSet   map[core.HashObj]struct{}
	oldArr       []byte
	newArr       []byte
	maxFileBytes uint64
}

// Build writes a rewritten Go-module archive with top-dir <module>@<version>/; content is rewritten on the fly with
// bounded RAM and sizes are counted with the per-file cap.
func (b universalRewrittenBuilderObj) Build(ctx context.Context, writerObj io.Writer) error {
	entriesArr, err := b.st.ReadTree(ctx, b.treeHash)
	if err != nil {
		return err
	}
	prefixedArr, err := buildRewrittenEntries(ctx, b.st, entriesArr, b.rewriteSet, b.oldArr, b.newArr, b.topDir, b.maxFileBytes)
	if err != nil {
		return err
	}
	_, err = b.archiveObj.Write(ctx, archive.WriteRequestObj{
		Format:  b.format,
		Writer:  writerObj,
		Entries: prefixedArr,
		Source:  rewritingBlobSourceObj{st: b.st, rewriteSet: b.rewriteSet, oldArr: b.oldArr, newArr: b.newArr},
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

// CanonicalUniversalBuilder returns a rewritten module archive when Go detection is unambiguous and rewrite is needed
// and allowed; otherwise it returns the raw tree. The shared rewritePlan owns the decision.
func (obj *Obj) CanonicalUniversalBuilder(
	st StorageInterface,
	key string,
	version string,
	treeHashObj core.HashObj,
	detectionObj core.DetectionObj,
	candidateObj *CandidateObj,
	rewriteArr []core.HashObj,
	listenerCtxObj ListenerCtxObj,
	format archive.FormatType,
) ArtifactBuilderInterface {
	planObj := obj.rewritePlan(key, version, detectionObj, candidateObj, listenerCtxObj)
	if planObj.Rewrite {
		return universalRewrittenBuilderObj{
			archiveObj:   obj.archiveObj,
			st:           st,
			format:       format,
			treeHash:     treeHashObj,
			topDir:       goModuleTopDir(string(planObj.NewArr), version),
			rewriteSet:   hashSetOf(rewriteArr),
			oldArr:       planObj.OldArr,
			newArr:       planObj.NewArr,
			maxFileBytes: obj.maxFileBytes,
		}
	}
	return obj.UniversalBuilder(st, key, version, treeHashObj, format)
}

// UniversalTopDir returns the canonical universal archive top-dir for Bazel strip_prefix.
func (obj *Obj) UniversalTopDir(key string, version string, detectionObj core.DetectionObj, candidateObj *CandidateObj, listenerCtxObj ListenerCtxObj) string {
	if planObj := obj.rewritePlan(key, version, detectionObj, candidateObj, listenerCtxObj); planObj.Rewrite {
		return goModuleTopDir(string(planObj.NewArr), version)
	}
	return universalTopDir(key, version)
}
