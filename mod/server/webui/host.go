package webui

import (
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/view"
)

// // // // // // // // // //

// absURL builds an absolute URL for an entry; without host it stays relative.
func absURL(scheme string, host string, pathText string) string {
	if host == "" {
		return pathText
	}
	return scheme + "://" + host + pathText
}

// dropDuplicateSnippets removes entry-independent snippets from the alternate set.
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

// schemeHost returns the entry scheme and host, or empty strings for a relative (hostless) entry.
func schemeHost(lnk link.Obj) (string, string) {
	if lnk.EntryHost == "" {
		return "", ""
	}
	return lnk.Scheme, lnk.EntryHost
}
