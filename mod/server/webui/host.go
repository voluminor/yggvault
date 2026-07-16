package webui

import (
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

func absURL(scheme string, host string, pathText string) string {
	if host == "" {
		return pathText
	}
	return scheme + "://" + host + pathText
}

func dropDuplicateSnippets(altArr []view.CodeSnippetObj, primaryArr []view.CodeSnippetObj) []view.CodeSnippetObj {
	seenObj := make(map[string]bool, len(primaryArr))
	for i := range primaryArr {
		seenObj[primaryArr[i].Body] = true
	}
	outArr := altArr[:0]
	for i := range altArr {
		if !seenObj[altArr[i].Body] {
			outArr = append(outArr, altArr[i])
		}
	}
	return outArr
}

func schemeHost(lnk link.Obj) (string, string) {
	if lnk.EntryHost == "" {
		return "", ""
	}
	return lnk.Scheme, lnk.EntryHost
}
