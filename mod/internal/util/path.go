package util

import (
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// // // // // // // // // //

// ErrArchiveEntryPathTooLong distinguishes exceeding the path-length limit from other path errors.
var ErrArchiveEntryPathTooLong = errors.New("archive entry path exceeds maximum length")

var windowsReservedNameObj = map[string]struct{}{
	"AUX":     {},
	"CON":     {},
	"NUL":     {},
	"PRN":     {},
	"COM1":    {},
	"COM2":    {},
	"COM3":    {},
	"COM4":    {},
	"COM5":    {},
	"COM6":    {},
	"COM7":    {},
	"COM8":    {},
	"COM9":    {},
	"CONIN$":  {},
	"CONOUT$": {},
	"LPT1":    {},
	"LPT2":    {},
	"LPT3":    {},
	"LPT4":    {},
	"LPT5":    {},
	"LPT6":    {},
	"LPT7":    {},
	"LPT8":    {},
	"LPT9":    {},
}

// //

func hasWindowsVolume(entryPath string) bool {
	if len(entryPath) >= 2 && entryPath[1] == ':' {
		firstByte := entryPath[0]
		return firstByte >= 'A' && firstByte <= 'Z' || firstByte >= 'a' && firstByte <= 'z'
	}
	return false
}

func hasWindowsReservedBase(pathPart string) bool {
	baseText := pathPart
	if indexValue := strings.IndexByte(baseText, '.'); indexValue >= 0 {
		baseText = baseText[:indexValue]
	}
	baseText = strings.Map(func(runeValue rune) rune {
		switch runeValue {
		case '\u00b9':
			return '1'
		case '\u00b2':
			return '2'
		case '\u00b3':
			return '3'
		default:
			return runeValue
		}
	}, baseText)
	_, ok := windowsReservedNameObj[strings.ToUpper(baseText)]
	return ok
}

// HasWindowsReservedBase checks a segment for a Windows device name.
func HasWindowsReservedBase(pathPart string) bool {
	return hasWindowsReservedBase(pathPart)
}

// //

// CanonicalPath converts a path to abs+clean and resolves symlinks when possible.
func CanonicalPath(pathToFile string) (string, error) {
	absPath, err := filepath.Abs(pathToFile)
	if err != nil {
		return "", err
	}
	absPath = filepath.Clean(absPath)
	if resolvedPath, errSym := filepath.EvalSymlinks(absPath); errSym == nil {
		return resolvedPath, nil
	}

	currentPath := absPath
	missingPartArr := make([]string, 0, 4)
	for {
		parentPath := filepath.Dir(currentPath)
		if parentPath == currentPath {
			return absPath, nil
		}
		missingPartArr = append(missingPartArr, filepath.Base(currentPath))
		if isVolumeRoot(parentPath) {
			return absPath, nil
		}
		if resolvedParentPath, errSym := filepath.EvalSymlinks(parentPath); errSym == nil {
			for i := len(missingPartArr) - 1; i >= 0; i-- {
				resolvedParentPath = filepath.Join(resolvedParentPath, missingPartArr[i])
			}
			return filepath.Clean(resolvedParentPath), nil
		}
		currentPath = parentPath
	}
}

func isVolumeRoot(pathText string) bool {
	volumeText := filepath.VolumeName(pathText)
	if volumeText == "" {
		return pathText == string(filepath.Separator)
	}
	return pathText == volumeText+string(filepath.Separator)
}

func sameVolume(leftPath string, rightPath string) bool {
	return strings.EqualFold(filepath.VolumeName(leftPath), filepath.VolumeName(rightPath))
}

// IsInsideOrEqual checks that childPath equals parentPath or lies inside it.
func IsInsideOrEqual(parentPath string, childPath string) (bool, error) {
	parentCanonicalPath, err := CanonicalPath(parentPath)
	if err != nil {
		return false, err
	}
	childCanonicalPath, err := CanonicalPath(childPath)
	if err != nil {
		return false, err
	}
	if parentCanonicalPath == childCanonicalPath {
		return true, nil
	}
	if !sameVolume(parentCanonicalPath, childCanonicalPath) {
		return false, nil
	}

	relativePath, err := filepath.Rel(parentCanonicalPath, childCanonicalPath)
	if err != nil {
		return false, err
	}
	if relativePath == "." {
		return true, nil
	}

	return !strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) && relativePath != "..", nil
}

// PathsOverlap checks for path nesting in either direction or equality.
func PathsOverlap(leftPath string, rightPath string) (bool, error) {
	if conflict, err := IsInsideOrEqual(leftPath, rightPath); err != nil || conflict {
		return conflict, err
	}
	return IsInsideOrEqual(rightPath, leftPath)
}

func archiveNameHygiene(name string) error {
	if name == "" {
		return errors.New("archive path is empty")
	}
	if !utf8.ValidString(name) {
		return errors.New("archive path is not valid UTF-8")
	}
	if strings.Contains(name, `\`) {
		return errors.New("archive path must use forward slashes")
	}
	if strings.HasPrefix(name, "/") {
		return errors.New("archive path must not be absolute")
	}
	if hasWindowsVolume(name) {
		return errors.New("archive path must not contain Windows volume")
	}
	if strings.Contains(name, ":") {
		return errors.New("archive path must not contain colon")
	}
	if strings.IndexFunc(name, func(r rune) bool {
		return r < 0x20 || r == 0x7f
	}) >= 0 {
		return errors.New("archive path must not contain control characters")
	}
	return nil
}

// SymlinkTargetWithinRoot validates a symlink target interpreted relative to the symlink's own directory.
// Unlike an entry path, a target legitimately MAY contain ".." (e.g. "../LICENSE"); it is unsafe only when it
// is absolute or, once resolved against symlinkPath's parent, climbs above the archive root.
func SymlinkTargetWithinRoot(symlinkPath string, target string) error {
	if err := archiveNameHygiene(target); err != nil {
		return err
	}
	resolved := path.Join(path.Dir(symlinkPath), target)
	if resolved == ".." || strings.HasPrefix(resolved, "../") {
		return errors.New("symlink target escapes archive root")
	}
	return nil
}

// CleanArchiveEntryPath normalizes an archive entry path and rejects traversal/absolute variants.
func CleanArchiveEntryPath(entryPath string) (string, error) {
	if err := archiveNameHygiene(entryPath); err != nil {
		return "", err
	}

	cleanPath := path.Clean(entryPath)
	if cleanPath == "." {
		return "", errors.New("archive entry path is empty after cleaning")
	}
	if cleanPath == ".." || strings.HasPrefix(cleanPath, "../") {
		return "", errors.New("archive entry path must not contain path traversal")
	}

	for _, pathPart := range strings.Split(cleanPath, "/") {
		if pathPart == "" || pathPart == "." || pathPart == ".." {
			return "", errors.New("archive entry path must not contain invalid segments")
		}
		if strings.HasSuffix(pathPart, " ") || strings.HasSuffix(pathPart, ".") {
			return "", errors.New("archive entry path segment must not end with dot or space")
		}
		if hasWindowsReservedBase(pathPart) {
			return "", errors.New("archive entry path must not contain Windows reserved name")
		}
	}

	return cleanPath, nil
}

// ValidateArchiveEntryPath checks the length and safety of an archive entry path.
func ValidateArchiveEntryPath(entryPath string, maxBytes uint) error {
	if maxBytes > 0 && len(entryPath) > int(maxBytes) {
		return fmt.Errorf("%w of %d bytes", ErrArchiveEntryPathTooLong, maxBytes)
	}
	_, err := CleanArchiveEntryPath(entryPath)
	return err
}

// JoinInside builds a path inside basePath and rejects escapes after canonicalization.
func JoinInside(basePath string, pathParts ...string) (string, error) {
	if strings.TrimSpace(basePath) == "" {
		return "", errors.New("base path is empty")
	}

	targetPath := filepath.Join(append([]string{basePath}, pathParts...)...)
	isInside, err := IsInsideOrEqual(basePath, targetPath)
	if err != nil {
		return "", err
	}
	if !isInside {
		return "", errors.New("joined path escapes base path")
	}

	return targetPath, nil
}
