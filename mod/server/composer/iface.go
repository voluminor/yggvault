package composer

import (
	"context"
	"strings"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/serr"
)

// // // // // // // // // //

const (
	// cMaxManifestBytes caps one composer.json included in p2; larger versions become dist-only entries.
	cMaxManifestBytes = 1 << 20

	// cMaxInputBytes caps total raw composer.json bytes per document to limit cold-build memory.
	cMaxInputBytes = 64 << 20

	// cMaxFilterBytes caps filter length because matching work and cache-key size depend on it.
	cMaxFilterBytes = 256
)

// // // // // // // // // //

// VersionReaderInterface provides keyset version walking, composer.json object reads, and dist artifact lookup.
type VersionReaderInterface interface {
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	GetArtifact(ctx context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error)
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}

// NamesReaderInterface exposes cross-key composer names and name-to-winner mapping for serve-time p2.
type NamesReaderInterface interface {
	ComposerPackageNames() []string
	ComposerKeyForName(name string) (string, bool)
}

// // // // // // // // // //

// CheckFilter rejects overlong filters with serr.ErrBadInput.
func CheckFilter(filter string) error {
	if len(filter) > cMaxFilterBytes {
		return serr.ErrBadInput
	}
	return nil
}

func filterMatch(pattern string, name string) bool {
	if !strings.Contains(pattern, "*") {
		return strings.HasPrefix(name, pattern)
	}
	return globMatch(pattern, name)
}

func globMatch(pattern string, name string) bool {
	patIdx, nameIdx := 0, 0
	starIdx, retryIdx := -1, 0
	for nameIdx < len(name) {
		switch {
		case patIdx < len(pattern) && pattern[patIdx] == '*':
			starIdx = patIdx
			retryIdx = nameIdx
			patIdx++
		case patIdx < len(pattern) && pattern[patIdx] == name[nameIdx]:
			patIdx++
			nameIdx++
		case starIdx != -1:
			patIdx = starIdx + 1
			retryIdx++
			nameIdx = retryIdx
		default:
			return false
		}
	}
	for patIdx < len(pattern) && pattern[patIdx] == '*' {
		patIdx++
	}
	return patIdx == len(pattern)
}
