package util

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// // // // // // // // // //

func TestIsInsideOrEqual(t *testing.T) {
	parentPath := t.TempDir()
	childPath := filepath.Join(parentPath, "nested")
	otherPath := filepath.Join(t.TempDir(), "other")

	if err := os.MkdirAll(childPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	isInside, err := IsInsideOrEqual(parentPath, childPath)
	if err != nil {
		t.Fatalf("IsInsideOrEqual returned error: %v", err)
	}
	if !isInside {
		t.Fatal("expected child path to be inside parent")
	}

	isInside, err = IsInsideOrEqual(parentPath, otherPath)
	if err != nil {
		t.Fatalf("IsInsideOrEqual returned error: %v", err)
	}
	if isInside {
		t.Fatal("expected unrelated path to be outside parent")
	}
}

func TestPathsOverlap(t *testing.T) {
	leftPath := t.TempDir()
	rightPath := filepath.Join(leftPath, "cache")

	if err := os.MkdirAll(rightPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}

	conflict, err := PathsOverlap(leftPath, rightPath)
	if err != nil {
		t.Fatalf("PathsOverlap returned error: %v", err)
	}
	if !conflict {
		t.Fatal("expected nested paths to overlap")
	}
}

func TestPathsOverlapResolvesExistingParentSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink creation requires extra privileges on windows")
	}

	realPath := filepath.Join(t.TempDir(), "real")
	linkPath := filepath.Join(t.TempDir(), "link")
	if err := os.MkdirAll(realPath, 0o755); err != nil {
		t.Fatalf("MkdirAll returned error: %v", err)
	}
	if err := os.Symlink(realPath, linkPath); err != nil {
		t.Fatalf("Symlink returned error: %v", err)
	}

	conflict, err := PathsOverlap(realPath, filepath.Join(linkPath, "cache"))
	if err != nil {
		t.Fatalf("PathsOverlap returned error: %v", err)
	}
	if !conflict {
		t.Fatal("expected symlinked child path to overlap")
	}
}

func TestCleanArchiveEntryPath(t *testing.T) {
	cleanPath, err := CleanArchiveEntryPath("./dir/../dir/file.txt")
	if err != nil {
		t.Fatalf("CleanArchiveEntryPath returned error: %v", err)
	}
	if cleanPath != "dir/file.txt" {
		t.Fatalf("unexpected clean path: %q", cleanPath)
	}
}

func TestValidateArchiveEntryPathRejectsTraversal(t *testing.T) {
	err := ValidateArchiveEntryPath("../etc/passwd", 128)
	if err == nil {
		t.Fatal("ValidateArchiveEntryPath returned nil error")
	}
	if !strings.Contains(err.Error(), "path traversal") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateArchiveEntryPathRejectsWindowsVolume(t *testing.T) {
	err := ValidateArchiveEntryPath("C:/temp/file.txt", 128)
	if err == nil {
		t.Fatal("ValidateArchiveEntryPath returned nil error")
	}
	if !strings.Contains(err.Error(), "Windows volume") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateArchiveEntryPathRejectsWindowsAmbiguousSegments(t *testing.T) {
	caseObj := map[string]string{
		"pkg/file:stream.txt": "colon",
		"pkg/CON.txt":         "Windows reserved name",
		"pkg/CONIN$":          "Windows reserved name",
		"pkg/CONOUT$":         "Windows reserved name",
		"pkg/COM\u00b9":       "Windows reserved name",
		"pkg/LPT\u00b2.txt":   "Windows reserved name",
		"pkg/lpt9":            "Windows reserved name",
		"pkg/name.":           "dot or space",
		"pkg/name ":           "dot or space",
	}
	for pathText, wantText := range caseObj {
		t.Run(pathText, func(t *testing.T) {
			err := ValidateArchiveEntryPath(pathText, 128)
			if err == nil {
				t.Fatal("ValidateArchiveEntryPath returned nil error")
			}
			if !strings.Contains(err.Error(), wantText) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestHasWindowsReservedBase(t *testing.T) {
	if !HasWindowsReservedBase("CON.txt") {
		t.Fatal("expected CON.txt to be a Windows reserved base")
	}
	if HasWindowsReservedBase("config.txt") {
		t.Fatal("config.txt must not be a Windows reserved base")
	}
}

func TestJoinInsideRejectsEscape(t *testing.T) {
	basePath := t.TempDir()

	_, err := JoinInside(basePath, "..", "escape.txt")
	if err == nil {
		t.Fatal("JoinInside returned nil error")
	}
	if !strings.Contains(err.Error(), "escapes base path") {
		t.Fatalf("unexpected error: %v", err)
	}
}
