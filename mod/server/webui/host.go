package webui

import (
	"strings"

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

func schemeHost(lnk LinkInterface) (string, string) {
	probe := lnk.Abs("/")
	idx := strings.Index(probe, "://")
	if idx < 0 {
		return "", ""
	}
	scheme := probe[:idx]
	rest := probe[idx+len("://"):]
	if slash := strings.IndexByte(rest, '/'); slash >= 0 {
		rest = rest[:slash]
	}
	if scheme == "" || rest == "" {
		return "", ""
	}
	return scheme, rest
}
