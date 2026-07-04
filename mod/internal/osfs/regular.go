package osfs

import "os"

// // // // // // // // // //

// IsRegularFile reports whether info describes a regular file, not a symlink, device, socket, or directory.
func IsRegularFile(infoObj os.FileInfo) bool {
	return infoObj.Mode().Type() == 0
}
