//go:build !unix

package osfs

import (
	"fmt"
	"os"
)

// // // // // // // // // //

// symlinkGuard rejects a final path component that is currently a symlink. O_NOFOLLOW is unavailable on non-Unix
// platforms, so this Lstat check emulates it. It cannot close the open-time TOCTOU window on its own; the write
// path narrows that further with a post-open regular-file re-check, and read callers re-verify via os.SameFile.
func symlinkGuard(pathText string) error {
	if infoObj, err := os.Lstat(pathText); err == nil && infoObj.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("refusing to open symlink: %s", pathText)
	}
	return nil
}

// // // // // // // // // //

// OpenNoFollow opens read-only, emulating O_NOFOLLOW via an Lstat symlink check on non-Unix platforms.
// Callers additionally re-verify the opened descriptor with os.SameFile.
func OpenNoFollow(pathText string) (*os.File, error) {
	if err := symlinkGuard(pathText); err != nil {
		return nil, err
	}
	return os.OpenFile(pathText, os.O_RDONLY, 0)
}

// //

// OpenNoFollowWrite creates or overwrites, emulating O_NOFOLLOW on non-Unix platforms: it rejects a pre-existing
// symlink at the final component and re-verifies after open that the descriptor is a regular file.
func OpenNoFollowWrite(pathText string, perm os.FileMode) (*os.File, error) {
	if err := symlinkGuard(pathText); err != nil {
		return nil, err
	}
	fileObj, err := os.OpenFile(pathText, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, perm)
	if err != nil {
		return nil, err
	}
	statObj, err := fileObj.Stat()
	if err != nil {
		_ = fileObj.Close()
		return nil, err
	}
	if !statObj.Mode().IsRegular() {
		_ = fileObj.Close()
		return nil, fmt.Errorf("refusing to write non-regular file: %s", pathText)
	}
	return fileObj, nil
}
