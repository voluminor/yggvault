package overlay

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/mod/module"
	modzip "golang.org/x/mod/zip"

	"github.com/voluminor/yggvault/mod/core"
)

// // // // // // // // // //

const (
	// GoZipUnclassifiedLabel is the stable label for x/mod/zip errors this code cannot classify yet.
	GoZipUnclassifiedLabel = "unclassified module zip errors"

	// cMaxBlockSamples limits path examples per block-reason category.
	cMaxBlockSamples = 3
	// cMaxBlockSampleBytes limits one sampled path.
	cMaxBlockSampleBytes = 96
	// cMaxBlockReasonBytes stays within the storage degraded_reason-sized column budget.
	cMaxBlockReasonBytes = 1024
)

const (
	// Tree-complexity budget before modzip.CheckFiles. Its collision checker can allocate
	// gigabytes on hostile but formally legal deep uppercase trees. This cheap O(path bytes)
	// guard covers real large modules with headroom and blocks known resource-exhaustion shapes.
	cGoZipPathBudgetBytes  = 4 << 20
	cGoZipDirSegmentBudget = 1 << 18
)

const (
	// These texts pin the unexported sentinels of golang.org/x/mod/zip.
	cModZipPathNotCleanText    = "file path is not clean"
	cModZipPathNotRelativeText = "file path is not relative"
	cModZipGoModCaseText       = "go.mod files must have lowercase names"
)

// // // // // // // // // //

// viabilityFileObj adapts a tree entry to modzip.File without reading blobs.
// CheckFiles should only open root go.mod for language version parsing, so that body is preloaded.
type viabilityFileObj struct {
	entry    core.TreeEntryObj
	goModArr []byte
}

// Path returns the entry path inside the module.
func (f viabilityFileObj) Path() string { return f.entry.Path }

// Lstat returns a synthetic FileInfo built from the tree entry size.
// Size is pre-rewrite; a border case can still fail during build, which falls back to the degraded path.
func (f viabilityFileObj) Lstat() (fs.FileInfo, error) {
	return goFileInfoObj{name: path.Base(f.entry.Path), size: int64(f.entry.SizeBytes)}, nil
}

// Open serves the root go.mod bytes already in hand; any other open is a contract violation.
func (f viabilityFileObj) Open() (io.ReadCloser, error) {
	if f.entry.Path == "go.mod" {
		return io.NopCloser(bytes.NewReader(f.goModArr)), nil
	}
	return nil, fmt.Errorf("viability check must not open %q", f.entry.Path)
}

// //

// blockSamplesObj tracks a category count and the first sampled paths.
type blockSamplesObj struct {
	count     int
	sampleArr []string
}

func (s *blockSamplesObj) add(sampleText string) {
	s.count++
	if len(s.sampleArr) < cMaxBlockSamples {
		s.sampleArr = append(s.sampleArr, sampleText)
	}
}

// // // // // // // // // //

// treeComplexityReason enforces the pre-CheckFiles budget; empty means within budget.
// It counts path bytes and slash segments in one allocation-free pass.
func treeComplexityReason(treeArr []core.TreeEntryObj) string {
	var pathBytes, dirSegments uint64
	for i := range treeArr {
		pathBytes += uint64(len(treeArr[i].Path))
		dirSegments += uint64(strings.Count(treeArr[i].Path, "/"))
	}
	if pathBytes > cGoZipPathBudgetBytes || dirSegments > cGoZipDirSegmentBudget {
		return fmt.Sprintf("tree exceeds go module zip complexity budget (%d path bytes, %d directory segments)", pathBytes, dirSegments)
	}
	return ""
}

// classifyInvalid splits Invalid entries from CheckFiles so a new x/mod message text cannot masquerade as an invalid path.
func classifyInvalid(feObj modzip.FileError, invalidObj, collisionObj, oversizeObj, unclassifiedObj *blockSamplesObj) {
	msgText := ""
	if feObj.Err != nil {
		msgText = feObj.Err.Error()
	}
	var invalidPathErrObj *module.InvalidPathError
	switch {
	case strings.Contains(msgText, "collision"),
		strings.Contains(msgText, "both a file and a directory"),
		strings.Contains(msgText, "multiple entries"):
		// The collision text contains both sides of the pair, so the whole message is capped.
		collisionObj.add(clipSample(msgText))
	case strings.Contains(msgText, "too large"):
		oversizeObj.add(strconv.Quote(clipSample(feObj.Path)))
	case errors.As(feObj.Err, &invalidPathErrObj),
		msgText == cModZipPathNotCleanText,
		msgText == cModZipPathNotRelativeText,
		msgText == cModZipGoModCaseText:
		invalidObj.add(strconv.Quote(clipSample(feObj.Path)))
	default:
		unclassifiedObj.add(clipSample(feObj.Error()))
	}
}

// clipSample cuts a path sample on a rune boundary.
func clipSample(pathText string) string {
	if len(pathText) <= cMaxBlockSampleBytes {
		return pathText
	}
	cut := cMaxBlockSampleBytes
	for cut > 0 && !utf8.RuneStart(pathText[cut]) {
		cut--
	}
	return pathText[:cut] + "..."
}

// clipReason cuts the final reason on a rune boundary without exceeding cMaxBlockReasonBytes.
func clipReason(reasonText string) string {
	if len(reasonText) <= cMaxBlockReasonBytes {
		return reasonText
	}
	cut := cMaxBlockReasonBytes - len("...")
	for cut > 0 && !utf8.RuneStart(reasonText[cut]) {
		cut--
	}
	return reasonText[:cut] + "..."
}

// // // // // // // // // //

// goZipBlockReason decides during detection whether a tree can ever become a valid Go module zip.
// Empty means usable. modzip.CheckFiles supplies invalid paths, fold collisions, and size-limit
// failures; omitted x/mod/zip classes do not block. Symlinks block everywhere because gozip.Build
// rejects them hard even where modzip would omit a subtree.
func goZipBlockReason(treeArr []core.TreeEntryObj, goModArr []byte) string {
	if reasonText := treeComplexityReason(treeArr); reasonText != "" {
		return reasonText
	}

	var symlinkObj blockSamplesObj
	filesArr := make([]modzip.File, 0, len(treeArr))
	for i := range treeArr {
		entryObj := treeArr[i]
		if entryObj.Mode != core.ModeFile {
			symlinkObj.add(strconv.Quote(clipSample(entryObj.Path)))
			continue
		}
		filesArr = append(filesArr, viabilityFileObj{entry: entryObj, goModArr: goModArr})
	}

	cfObj, _ := modzip.CheckFiles(filesArr)
	var invalidObj, collisionObj, oversizeObj, unclassifiedObj blockSamplesObj
	for _, feObj := range cfObj.Invalid {
		classifyInvalid(feObj, &invalidObj, &collisionObj, &oversizeObj, &unclassifiedObj)
	}

	var partArr []string
	appendPart := func(labelText string, samplesObj blockSamplesObj) {
		if samplesObj.count == 0 {
			return
		}
		partArr = append(partArr, fmt.Sprintf("%s (%d): %s", labelText, samplesObj.count, strings.Join(samplesObj.sampleArr, ", ")))
	}
	appendPart("invalid file paths", invalidObj)
	appendPart("case-insensitive path collisions", collisionObj)
	appendPart("oversized files", oversizeObj)
	appendPart(GoZipUnclassifiedLabel, unclassifiedObj)
	if cfObj.SizeError != nil {
		partArr = append(partArr, cfObj.SizeError.Error())
	}
	appendPart("symlinks", symlinkObj)
	return clipReason(strings.Join(partArr, "; "))
}
