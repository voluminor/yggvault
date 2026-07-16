package util

import (
	"strings"
)

// // // // // // // // // //

const (
	cMinRawVersionBytes = 2
	cMaxRawVersionBytes = 64
)

var reservedRawSegmentSet = map[string]struct{}{
	"list":          {},
	"latest":        {},
	"releases.json": {},
	"releases.xml":  {},
}

var forbiddenRawSuffixArr = []string{".zip", ".tar.gz", ".json", ".xml", ".mod", ".info", ".txt"}

// // // // // // // // // //

func isRawAlnum(symbolByte byte) bool {
	return (symbolByte >= 'a' && symbolByte <= 'z') ||
		(symbolByte >= 'A' && symbolByte <= 'Z') ||
		(symbolByte >= '0' && symbolByte <= '9')
}

func isRawSymbol(symbolByte byte) bool {
	return isRawAlnum(symbolByte) || symbolByte == '.' || symbolByte == '_' || symbolByte == '-'
}

func isGoMajorSegment(name string) bool {
	if len(name) < 2 || name[0] != 'v' {
		return false
	}
	for i := 1; i < len(name); i++ {
		if name[i] < '0' || name[i] > '9' {
			return false
		}
	}
	return true
}

// // // // // // // // // //

// IsStorableRawVersion reports whether a non-semver version name is safe to store and serve.
// Raw versions are universal-only: strict ASCII charset, bounded length, alnum edges, and no
// collision with route grammar (artifact suffixes, reserved segments, go major segments).
// Storable semver names are excluded on purpose: they follow the regular semver path.
func IsStorableRawVersion(name string) bool {
	if len(name) < cMinRawVersionBytes || len(name) > cMaxRawVersionBytes {
		return false
	}
	if !isRawAlnum(name[0]) || !isRawAlnum(name[len(name)-1]) {
		return false
	}
	for i := 0; i < len(name); i++ {
		if !isRawSymbol(name[i]) {
			return false
		}
	}
	if IsStorableSemver(name) || isGoMajorSegment(name) {
		return false
	}
	lowerName := strings.ToLower(name)
	if _, reserved := reservedRawSegmentSet[lowerName]; reserved {
		return false
	}
	for _, suffixText := range forbiddenRawSuffixArr {
		if strings.HasSuffix(lowerName, suffixText) {
			return false
		}
	}
	return !HasWindowsReservedBase(name)
}

// // // // // // // // // //

// IsRawVersionName classifies already-stored names; after publication validation, non-semver means raw.
// Detection evidence is informational only, while the version name is authoritative.
func IsRawVersionName(version string) bool {
	return !IsStorableSemver(version)
}
