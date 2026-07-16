package markdown

import (
	"bytes"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// // // // // // // // // //

var markdownObj = goldmark.New(goldmark.WithExtensions(extension.GFM))

var sanitizerObj = bluemonday.UGCPolicy()

// // // // // // // // // //

// SafeHTML converts release-note markdown into sanitized safe HTML bytes.
func SafeHTML(markdownText string) []byte {
	if markdownText == "" {
		return nil
	}
	var bufObj bytes.Buffer
	if err := markdownObj.Convert([]byte(markdownText), &bufObj); err != nil {
		return nil
	}
	return sanitizerObj.SanitizeBytes(bufObj.Bytes())
}
