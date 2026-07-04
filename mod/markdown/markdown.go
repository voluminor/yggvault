package markdown

import (
	"bytes"

	"github.com/microcosm-cc/bluemonday"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/extension"
)

// // // // // // // // // //

// markdownObj is goldmark with GFM and without html.WithUnsafe, so raw HTML is escaped.
var markdownObj = goldmark.New(goldmark.WithExtensions(extension.GFM))

// sanitizerObj removes remaining vectors such as javascript/data schemes and unsafe tags.
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
