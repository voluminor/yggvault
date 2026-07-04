//go:build unix

package osfs

import (
	"os"

	"golang.org/x/sys/unix"
)

// // // // // // // // // //

// OpenNoFollow opens a read-only file with O_NOFOLLOW, rejecting a symlink in the final path component.
func OpenNoFollow(pathText string) (*os.File, error) {
	return os.OpenFile(pathText, os.O_RDONLY|unix.O_NOFOLLOW, 0)
}

// //

// OpenNoFollowWrite creates or overwrites with O_NOFOLLOW, rejecting a symlink in the final path component.
func OpenNoFollowWrite(pathText string, perm os.FileMode) (*os.File, error) {
	return os.OpenFile(pathText, os.O_WRONLY|os.O_CREATE|os.O_TRUNC|unix.O_NOFOLLOW, perm)
}
