//go:build linux

package osfs

import (
	"bufio"
	"os"
	"strconv"
	"strings"
)

// // // // // // // // // //

const cMemInfoPath = "/proc/meminfo"

// //

// SystemMemoryBytes returns MemTotal from /proc/meminfo in bytes; 0 means read failed.
func SystemMemoryBytes() uint64 {
	fileObj, err := os.Open(cMemInfoPath)
	if err != nil {
		return 0
	}
	defer fileObj.Close()

	scannerObj := bufio.NewScanner(fileObj)
	for scannerObj.Scan() {
		lineText := scannerObj.Text()
		if !strings.HasPrefix(lineText, "MemTotal:") {
			continue
		}
		fieldArr := strings.Fields(lineText)
		if len(fieldArr) < 2 {
			return 0
		}
		kbValue, parseErr := strconv.ParseUint(fieldArr[1], 10, 64)
		if parseErr != nil {
			return 0
		}
		return kbValue * 1024
	}
	return 0
}
