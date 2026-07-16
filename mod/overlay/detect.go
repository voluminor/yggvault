package overlay

import (
	"bytes"
	"context"
	"encoding/json"
	"path"
	"regexp"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

var composerNamePatternObj = regexp.MustCompile(`^[a-z0-9]([_.-]?[a-z0-9]+)*/[a-z0-9]([_.-]?[a-z0-9]+)*$`)

type detectionEvidenceObj struct {
	GoModulePath string `json:"go_module_path,omitempty"`
	ComposerName string `json:"composer_name,omitempty"`
}

type goDetectorObj struct{}

type composerDetectorObj struct{}

// // // // // // // // // //

func pathDepth(pathText string) int { return strings.Count(pathText, "/") }

func isShallower(aText string, bText string) bool {
	da, db := pathDepth(aText), pathDepth(bText)
	if da != db {
		return da < db
	}
	return aText < bText
}

func shallowestEntry(treeArr []core.TreeEntryObj, baseName string) (core.TreeEntryObj, bool) {
	var bestObj core.TreeEntryObj
	foundFlag := false
	for i := range treeArr {
		entryObj := treeArr[i]
		if entryObj.Mode != core.ModeFile || path.Base(entryObj.Path) != baseName {
			continue
		}
		if !foundFlag || isShallower(entryObj.Path, bestObj.Path) {
			bestObj = entryObj
			foundFlag = true
		}
	}
	return bestObj, foundFlag
}

func isGoSource(pathText string) bool {
	return strings.HasSuffix(pathText, ".go") || path.Base(pathText) == "go.mod"
}

func readManifest(ctx context.Context, src BlobReaderInterface, hashObj core.HashObj) ([]byte, error) {
	dataArr, err := src.ReadBlob(ctx, hashObj)
	if err != nil {
		return nil, err
	}
	if uint64(len(dataArr)) > cMaxManifestBytes {
		return nil, nil
	}
	return dataArr, nil
}

func validComposerName(nameText string) bool {
	if nameText == "" || len(nameText) > cMaxComposerNameBytes {
		return false
	}
	return composerNamePatternObj.MatchString(nameText)
}

func scanRewriteBlobs(ctx context.Context, treeArr []core.TreeEntryObj, src BlobReaderInterface, modulePath string) ([]core.HashObj, error) {
	needleArr := []byte(modulePath)
	scannedSet := make(map[core.HashObj]struct{})
	matchedSet := make(map[core.HashObj]struct{})
	for i := range treeArr {
		entryObj := treeArr[i]
		if entryObj.Mode != core.ModeFile || !isGoSource(entryObj.Path) {
			continue
		}
		if _, ok := scannedSet[entryObj.BlobHash]; ok {
			continue
		}
		scannedSet[entryObj.BlobHash] = struct{}{}
		dataArr, err := src.ReadBlob(ctx, entryObj.BlobHash)
		if err != nil {
			return nil, err
		}
		if containsModulePath(dataArr, needleArr) {
			matchedSet[entryObj.BlobHash] = struct{}{}
		}
	}

	resultArr := make([]core.HashObj, 0, len(matchedSet))
	for hashObj := range matchedSet {
		resultArr = append(resultArr, hashObj)
	}
	sort.Slice(resultArr, func(i, j int) bool {
		return bytes.Compare(resultArr[i][:], resultArr[j][:]) < 0
	})
	return resultArr, nil
}

// // // // // // // // // //

// Ecosystem returns Go.
func (goDetectorObj) Ecosystem() stcode.EcosystemType { return stcode.EcosystemGo }

// Detect requires a root go.mod, extracts module path, and builds a host-independent rewrite set.
// Missing, nested-only or oversized go.mod returns nil candidate.
func (goDetectorObj) Detect(ctx context.Context, treeArr []core.TreeEntryObj, src BlobReaderInterface) (*CandidateObj, error) {
	entryObj, ok := shallowestEntry(treeArr, "go.mod")
	if !ok || entryObj.Path != "go.mod" {
		return nil, nil
	}
	dataArr, err := readManifest(ctx, src, entryObj.BlobHash)
	if err != nil {
		return nil, err
	}
	if dataArr == nil {
		return nil, nil
	}
	modulePath := modfile.ModulePath(dataArr)
	if modulePath == "" {
		return nil, nil
	}
	blockReason := goZipBlockReason(treeArr, dataArr)
	rewriteArr, err := scanRewriteBlobs(ctx, treeArr, src, modulePath)
	if err != nil {
		return nil, err
	}
	return &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: modulePath, RewriteBlobs: rewriteArr, GoZipBlockReason: blockReason}, nil
}

// //

// Ecosystem returns Composer.
func (composerDetectorObj) Ecosystem() stcode.EcosystemType { return stcode.EcosystemComposer }

// Detect finds the shallowest composer.json and extracts a strict vendor/package name. Invalid, oversized, or
// unparseable manifests return nil candidate.
func (composerDetectorObj) Detect(ctx context.Context, treeArr []core.TreeEntryObj, src BlobReaderInterface) (*CandidateObj, error) {
	entryObj, ok := shallowestEntry(treeArr, "composer.json")
	if !ok {
		return nil, nil
	}
	dataArr, err := readManifest(ctx, src, entryObj.BlobHash)
	if err != nil {
		return nil, err
	}
	if dataArr == nil {
		return nil, nil
	}
	var manifestObj struct {
		Name string `json:"name"`
	}
	if json.Unmarshal(dataArr, &manifestObj) != nil {
		return nil, nil
	}
	nameText := strings.TrimSpace(manifestObj.Name)
	if !validComposerName(nameText) {
		return nil, nil
	}
	return &CandidateObj{Ecosystem: stcode.EcosystemComposer, ComposerName: nameText}, nil
}

// // // // // // // // // //

// Detect runs detectors over a version tree and aggregates the result. Go+Composer sets Conflict and disables rewrite.
// The return value is what rescan stores in PublishObj.
func (obj *Obj) Detect(ctx context.Context, treeArr []core.TreeEntryObj, src BlobReaderInterface) (DetectionResultObj, error) {
	var resultObj DetectionResultObj
	for _, detectorObj := range obj.detectors {
		candidateObj, err := detectorObj.Detect(ctx, treeArr, src)
		if err != nil {
			return DetectionResultObj{}, err
		}
		if candidateObj == nil {
			continue
		}
		switch candidateObj.Ecosystem {
		case stcode.EcosystemGo:
			resultObj.Go = candidateObj
		case stcode.EcosystemComposer:
			resultObj.Composer = candidateObj
		}
	}

	resultObj.Detection.IsGo = resultObj.Go != nil
	resultObj.Detection.IsComposer = resultObj.Composer != nil
	resultObj.Detection.Conflict = resultObj.Go != nil && resultObj.Composer != nil
	if resultObj.Go != nil && resultObj.Go.GoZipBlockReason != "" {
		resultObj.Detection.GoZipBlocked = true
		resultObj.Detection.GoZipBlockReason = resultObj.Go.GoZipBlockReason
	}
	if resultObj.Go != nil && !resultObj.Detection.Conflict {
		resultObj.RewriteBlobs = resultObj.Go.RewriteBlobs
	}
	resultObj.Detection.EvidenceJSON = buildEvidence(resultObj)
	return resultObj, nil
}

// CandidateFromDetection restores candidates from saved DetectionObj evidence JSON. RewriteBlobs are not included;
// serve-time builders receive them separately from storage.RewriteSet.
func CandidateFromDetection(detectionObj core.DetectionObj) (goCandidate *CandidateObj, composerCandidate *CandidateObj) {
	var evidenceObj detectionEvidenceObj
	_ = json.Unmarshal([]byte(detectionObj.EvidenceJSON), &evidenceObj)
	if detectionObj.IsGo && evidenceObj.GoModulePath != "" {
		goCandidate = &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: evidenceObj.GoModulePath}
	}
	if detectionObj.IsComposer && evidenceObj.ComposerName != "" {
		composerCandidate = &CandidateObj{Ecosystem: stcode.EcosystemComposer, ComposerName: evidenceObj.ComposerName}
	}
	return goCandidate, composerCandidate
}

func buildEvidence(resultObj DetectionResultObj) string {
	evidenceObj := detectionEvidenceObj{}
	if resultObj.Go != nil {
		evidenceObj.GoModulePath = resultObj.Go.GoModulePath
	}
	if resultObj.Composer != nil {
		evidenceObj.ComposerName = resultObj.Composer.ComposerName
	}
	dataArr, err := json.Marshal(evidenceObj)
	if err != nil {
		return "{}"
	}
	return string(dataArr)
}
