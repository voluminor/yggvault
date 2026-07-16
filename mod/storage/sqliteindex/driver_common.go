package sqliteindex

import (
	"path/filepath"
	"strings"
)

// // // // // // // // // //

func sqliteURIPath(pathToFile string) string {
	pathText := filepath.ToSlash(pathToFile)
	if filepath.VolumeName(pathToFile) != "" && !strings.HasPrefix(pathText, "/") {
		return "/" + pathText
	}
	return pathText
}
