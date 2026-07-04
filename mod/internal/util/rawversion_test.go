package util

import (
	"strings"
	"testing"
)

// // // // // // // // // //

func TestIsStorableRawVersion(t *testing.T) {
	acceptArr := []string{
		"INKSCAPE_1_3_2",
		"blockly-v9.3.3",
		"release-20260622.0",
		"2024-stable",
		"r1",
	}
	for _, name := range acceptArr {
		if !IsStorableRawVersion(name) {
			t.Errorf("IsStorableRawVersion(%q) = false, want true", name)
		}
	}

	rejectArr := []string{
		"",
		"a",                     // too short
		strings.Repeat("a", 65), // too long
		"v1.2.3",                // storable semver stays on the semver path
		"1.2.3",                 // storable semver without prefix
		"v2",                    // go major segment
		"v10",                   // go major segment
		"list",                  // reserved route segment
		"latest",                // reserved route segment
		"releases.json",         // reserved route segment
		"foo.zip",               // artifact suffix
		"foo.tar.gz",            // artifact suffix
		"foo.json",              // artifact suffix
		"foo.xml",               // artifact suffix
		"foo.mod",               // go proxy suffix
		"foo.info",              // go proxy suffix
		"foo.txt",               // reserved suffix
		"FOO.ZIP",               // suffixes rejected case-insensitively
		"-bad",                  // must start with alnum
		"bad-",                  // must end with alnum
		".bad",                  // must start with alnum
		"pathé",                 // non-ASCII
		"has space",             // forbidden char
		"a/b",                   // forbidden char
		"a@b",                   // forbidden char
		"v1.2.3+meta",           // '+' is outside the charset: build-metadata tags are not storable at all
		"CON",                   // windows reserved base
		"aux.1",                 // windows reserved base
	}
	for _, name := range rejectArr {
		if IsStorableRawVersion(name) {
			t.Errorf("IsStorableRawVersion(%q) = true, want false", name)
		}
	}
}

// //

func TestIsStorableSemverPrerelease(t *testing.T) {
	if !IsStorableSemverPrerelease("v1.2.3-rc.1") || !IsStorableSemverPrerelease("1.2.3-beta") {
		t.Error("prerelease semver must be detected")
	}
	if IsStorableSemverPrerelease("v1.2.3") || IsStorableSemverPrerelease("blockly-v9.3.3") || IsStorableSemverPrerelease("v1.2.3+meta") {
		t.Error("stable semver and non-semver names must not be flagged as prerelease")
	}
}
