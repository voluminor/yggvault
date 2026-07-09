package sqliteindex

import (
	"path/filepath"
	"strings"
)

// // // // // // // // // //

// sqliteURIPath normalizes a filesystem path into a file:// URI path. It is build-tag neutral and shared by
// both the cgo and non-cgo driver adapters, whose DSN query encodings differ but whose path handling does not.
func sqliteURIPath(pathToFile string) string {
	pathText := filepath.ToSlash(pathToFile)
	if filepath.VolumeName(pathToFile) != "" && !strings.HasPrefix(pathText, "/") {
		return "/" + pathText
	}
	return pathText
}
