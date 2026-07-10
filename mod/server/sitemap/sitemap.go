package sitemap

import (
	"bytes"
	"context"
	"strings"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/route"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/pager"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

// GenVersion invalidates the cached sitemap ETag when the XML format changes.
const GenVersion = "1"

const cMaxBytes = 8 << 20

// // // // // // // // // //

// StateReaderInterface returns a key snapshot for catalog and per-key sitemap entries.
type StateReaderInterface interface {
	KeyStates() []state.KeyStateObj
}

// //

// BuildStatsObj holds the final sitemap generation counters.
type BuildStatsObj struct {
	URLsWritten   int
	URLsDropped   int
	URLTruncated  bool
	ByteTruncated bool
}

// Truncated reports that the sitemap was capped by URL count or by bytes.
func (obj BuildStatsObj) Truncated() bool {
	return obj.URLTruncated || obj.ByteTruncated
}

// //

func xmlEscape(bufObj *bytes.Buffer, text string) {
	for i := 0; i < len(text); i++ {
		switch text[i] {
		case '&':
			bufObj.WriteString("&amp;")
		case '<':
			bufObj.WriteString("&lt;")
		case '>':
			bufObj.WriteString("&gt;")
		case '"':
			bufObj.WriteString("&quot;")
		case '\'':
			bufObj.WriteString("&apos;")
		default:
			if text[i] >= 0x20 || text[i] == '\t' || text[i] == '\n' || text[i] == '\r' {
				bufObj.WriteByte(text[i])
			}
		}
	}
}

func totalURLCandidates(keyArr []state.KeyStateObj, includeMetrics bool) int {
	totalValue := 1
	if includeMetrics {
		totalValue++
	}
	for i := range keyArr {
		if keyArr[i].VersionCount == 0 {
			continue
		}
		totalValue++
		totalValue += int(keyArr[i].VersionCount)
	}
	return totalValue
}

// // // // // // // // // //

// Build renders the sitemap for the current entry's crawlable HTML pages.
// The maxURLs limit is filled with catalog, metrics and the freshest versions first.
// Absolute loc values are built from linkObj; version lastmod equals ingest time.
func Build(ctx context.Context, store pager.VersionListerInterface, st StateReaderInterface, linkObj link.Obj, maxURLs int, includeMetrics bool) ([]byte, BuildStatsObj, error) {
	if maxURLs < 1 {
		maxURLs = 1
	}
	keyArr := st.KeyStates()
	totalURLs := totalURLCandidates(keyArr, includeMetrics)
	statsObj := BuildStatsObj{}

	var latestPublish time.Time
	estURLs := uint64(2)
	for i := range keyArr {
		if keyArr[i].LastPublishTS.After(latestPublish) {
			latestPublish = keyArr[i].LastPublishTS
		}
		estURLs += 1 + keyArr[i].VersionCount
	}
	if estURLs > uint64(maxURLs) {
		estURLs = uint64(maxURLs)
	}

	var bufObj bytes.Buffer
	bufObj.Grow(128 + int(estURLs)*96)
	bufObj.WriteString(`<?xml version="1.0" encoding="UTF-8"?>` + "\n")
	bufObj.WriteString(`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">` + "\n")

	const footerText = "</urlset>\n"
	writeURL := func(loc string, lastmod time.Time) bool {
		if statsObj.URLsWritten >= maxURLs {
			statsObj.URLTruncated = true
			return false
		}
		var entryBufObj bytes.Buffer
		entryBufObj.WriteString("  <url><loc>")
		xmlEscape(&entryBufObj, linkObj.Abs(loc))
		entryBufObj.WriteString("</loc>")
		if !lastmod.IsZero() {
			entryBufObj.WriteString("<lastmod>")
			entryBufObj.WriteString(lastmod.UTC().Format(time.RFC3339))
			entryBufObj.WriteString("</lastmod>")
		}
		entryBufObj.WriteString("</url>\n")
		if bufObj.Len()+entryBufObj.Len()+len(footerText) > cMaxBytes {
			statsObj.ByteTruncated = true
			return false
		}
		bufObj.Write(entryBufObj.Bytes())
		statsObj.URLsWritten++
		return true
	}

	writeURL(linkObj.Key("", ""), latestPublish)
	if includeMetrics {
		writeURL(route.Metrics, time.Time{})
	}

	for i := range keyArr {
		keyStateObj := keyArr[i]
		if keyStateObj.VersionCount == 0 {
			continue
		}
		if !writeURL(linkObj.Key(keyStateObj.Key, ""), keyStateObj.LastPublishTS) {
			break
		}
		capped := false
		err := pager.EachVersion(ctx, store, keyStateObj.Key, func(versionObj core.VersionObj) (bool, error) {
			if strings.Contains(versionObj.Version, "/") {
				return false, nil
			}
			if !writeURL(linkObj.Key(keyStateObj.Key, "/"+versionObj.Version), versionObj.IngestTS) {
				capped = true
				return true, nil
			}
			return false, nil
		})
		if err != nil {
			return nil, statsObj, err
		}
		if capped {
			break
		}
	}

	if statsObj.Truncated() && totalURLs > statsObj.URLsWritten {
		statsObj.URLsDropped = totalURLs - statsObj.URLsWritten
	}
	bufObj.WriteString(footerText)
	return bufObj.Bytes(), statsObj, nil
}
