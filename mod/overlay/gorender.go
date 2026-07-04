package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/api"
)

// // // // // // // // // //

// GoInfo returns the generated `@latest`/`.info` object. mod/server returns it directly for `@latest` and streams it
// for `.info`; time is local IngestTS.
func GoInfo(version string, ingestTS time.Time) api.GoInfoObj {
	return api.GoInfoObj{Version: version, Time: ingestTS.UTC()}
}

// RenderGoInfo returns `@latest`/`<version>.info` as canonical JSON bytes through generated jx encoder.
func RenderGoInfo(version string, ingestTS time.Time) ([]byte, error) {
	infoObj := GoInfo(version, ingestTS)
	return json.Marshal(&infoObj)
}

// RenderGoVersionList returns `@v/list`: canonical versions, one per line, sorted descending.
func RenderGoVersionList(versionArr []string) []byte {
	sortedArr := append([]string(nil), versionArr...)
	sortSemverDesc(sortedArr)
	var bufferObj bytes.Buffer
	for _, versionText := range sortedArr {
		bufferObj.WriteString(versionText)
		bufferObj.WriteByte('\n')
	}
	return bufferObj.Bytes()
}

// RenderGoMod returns `<version>.mod` as rewritten go.mod, or raw go.mod when rewrite is unnecessary or disabled.
// Rewrite decisions go through rewritePlan.
func (obj *Obj) RenderGoMod(
	ctx context.Context,
	st StorageInterface,
	key string,
	version string,
	treeHashObj core.HashObj,
	detectionObj core.DetectionObj,
	candidateObj *CandidateObj,
	listenerCtxObj ListenerCtxObj,
) ([]byte, error) {
	entriesArr, err := st.ReadTree(ctx, treeHashObj)
	if err != nil {
		return nil, err
	}
	entryObj, ok := shallowestEntry(entriesArr, "go.mod")
	if !ok {
		return nil, fmt.Errorf("go.mod not found in tree for key=%s: %w", key, errManifestMissing)
	}
	dataArr, err := readManifest(ctx, st, entryObj.BlobHash)
	if err != nil {
		return nil, err
	}
	if dataArr == nil {
		return nil, fmt.Errorf("go.mod exceeds manifest size cap for key=%s: %w", key, errManifestTooLarge)
	}
	if planObj := obj.rewritePlan(key, version, detectionObj, candidateObj, listenerCtxObj); planObj.Rewrite {
		dataArr = rewriteContent(dataArr, planObj.OldArr, planObj.NewArr)
	}
	return dataArr, nil
}
