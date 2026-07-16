package util

import (
	"fmt"
	"strings"

	"golang.org/x/mod/semver"
)

// // // // // // // // // //

const cMaxSemverBytes = 256

// // // // // // // // // //

func canonicalForCompare(version string) string {
	if version == "" || version[0] == 'v' {
		return version
	}
	return "v" + version
}

// // // // // // // // // //

// IsStorableSemver reports whether a version is safe to store: strict semver with or without leading 'v', stored
// verbatim. Branch/dev forms, spaces/control chars, and build metadata are rejected to keep ordering unambiguous.
func IsStorableSemver(version string) bool {
	if version == "" || len(version) > cMaxSemverBytes || strings.ContainsRune(version, '+') {
		return false
	}
	return semver.IsValid(canonicalForCompare(version))
}

// IsStorableSemverPrerelease reports whether a version is storable semver with a prerelease suffix.
// Tag listings have no prerelease flag, so tag mode drops such tags for parity with release mode.
func IsStorableSemverPrerelease(version string) bool {
	return IsStorableSemver(version) && semver.Prerelease(canonicalForCompare(version)) != ""
}

// IsCanonicalSemver reports whether a version is already in canonical Go semver form with a leading 'v'.
// This gates Go serving because go-proxy accepts only canonical 'vX.Y.Z'.
func IsCanonicalSemver(version string) bool {
	return version != "" && semver.IsValid(version) && semver.Canonical(version) == version
}

// CompareSemver compares semver versions after canonicalizing mixed bare and v-prefixed forms.
func CompareSemver(leftVersion string, rightVersion string) (int, error) {
	leftCompare := canonicalForCompare(leftVersion)
	rightCompare := canonicalForCompare(rightVersion)
	if !semver.IsValid(leftCompare) {
		return 0, fmt.Errorf("invalid semver: %q", leftVersion)
	}
	if !semver.IsValid(rightCompare) {
		return 0, fmt.Errorf("invalid semver: %q", rightVersion)
	}

	return semver.Compare(leftCompare, rightCompare), nil
}
