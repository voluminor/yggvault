package overlay

import (
	"sort"
	"strings"

	"github.com/voluminor/yggvault/mod/internal/util"
)

// // // // // // // // // //

func sortSemverDesc(versionArr []string) {
	sort.SliceStable(versionArr, func(i, j int) bool {
		compareValue, err := util.CompareSemver(versionArr[i], versionArr[j])
		if err != nil {
			return versionArr[i] > versionArr[j]
		}
		return compareValue > 0
	})
}

func composerNormalize(version string) string {
	mainText := strings.TrimPrefix(version, "v")
	suffixText := ""
	if idx := strings.IndexAny(mainText, "-+"); idx >= 0 {
		suffixText = mainText[idx:]
		mainText = mainText[:idx]
	}
	partsArr := strings.Split(mainText, ".")
	for len(partsArr) < 4 {
		partsArr = append(partsArr, "0")
	}
	if len(partsArr) > 4 {
		partsArr = partsArr[:4]
	}
	return strings.Join(partsArr, ".") + suffixText
}
