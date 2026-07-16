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
		"a",
		strings.Repeat("a", 65),
		"v1.2.3",
		"1.2.3",
		"v2",
		"v10",
		"list",
		"latest",
		"releases.json",
		"foo.zip",
		"foo.tar.gz",
		"foo.json",
		"foo.xml",
		"foo.mod",
		"foo.info",
		"foo.txt",
		"FOO.ZIP",
		"-bad",
		"bad-",
		".bad",
		"pathé",
		"has space",
		"a/b",
		"a@b",
		"v1.2.3+meta",
		"CON",
		"aux.1",
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
