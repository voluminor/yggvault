package util

import (
	"strings"
)

// // // // // // // // // //

// IsHistoricalVersion recognizes historical copies named v0.0.0-<prefix><N>, where N is a non-empty decimal suffix.
func IsHistoricalVersion(version string, prefix string) bool {
	if prefix == "" {
		return false
	}
	fullPrefix := "v0.0.0-" + prefix
	if !strings.HasPrefix(version, fullPrefix) {
		return false
	}
	seqText := version[len(fullPrefix):]
	if seqText == "" {
		return false
	}
	for _, symbolRune := range seqText {
		if symbolRune < '0' || symbolRune > '9' {
			return false
		}
	}
	return true
}
