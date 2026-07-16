package feedatom

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/voluminor/yggvault/mod/markdown"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/serr"
)

// // // // // // // // // //

type (
	feedDocObj struct {
		XMLName xml.Name    `xml:"feed"`
		Xmlns   string      `xml:"xmlns,attr"`
		Title   string      `xml:"title"`
		ID      string      `xml:"id"`
		Updated string      `xml:"updated"`
		Link    feedLinkObj `xml:"link"`
		Entries []entryObj  `xml:"entry"`
	}
	feedLinkObj struct {
		Href string `xml:"href,attr"`
		Rel  string `xml:"rel,attr,omitempty"`
	}
	entryObj struct {
		XMLName xml.Name    `xml:"entry"`
		Title   string      `xml:"title"`
		ID      string      `xml:"id"`
		Updated string      `xml:"updated"`
		Link    feedLinkObj `xml:"link"`
		Content contentObj  `xml:"content"`
	}
	contentObj struct {
		Type string `xml:"type,attr"`
		Body string `xml:",chardata"`
	}
)

const (
	cMaxEntryNotesBytes = 64 << 10
	cMaxFeedBytes       = 4 << 20
	cTruncatedMarker    = "\n\n_[notes truncated]_"
)

// // // // // // // // // //

// BuildStatsObj holds the final Atom feed generation counters.
type BuildStatsObj struct {
	EntriesWritten int
	EntriesDropped int
	NotesTruncated int
	BodyBytes      int
}

// Truncated reports that the feed dropped entries or truncated notes.
func (obj BuildStatsObj) Truncated() bool {
	return obj.EntriesDropped > 0 || obj.NotesTruncated > 0
}

// //

func renderNotes(markdownText string) string {
	return string(markdown.SafeHTML(markdownText))
}

func truncateNotes(markdownText string) (string, bool) {
	if len(markdownText) <= cMaxEntryNotesBytes {
		return markdownText, false
	}
	limitValue := cMaxEntryNotesBytes - len(cTruncatedMarker)
	if limitValue < 0 {
		limitValue = 0
	}
	cutValue := limitValue
	for cutValue > 0 && !utf8.RuneStart(markdownText[cutValue]) {
		cutValue--
	}
	return strings.TrimRight(markdownText[:cutValue], "\r\n\t ") + cTruncatedMarker, true
}

func servicePath(linkObj link.Obj, suffix string) string {
	pathText := suffix
	if linkObj.RoutePrefix != "" {
		pathText = "/" + linkObj.RoutePrefix + suffix
	}
	return linkObj.Abs(pathText)
}

func marshalFeedFrame(feedObj feedDocObj) ([]byte, []byte, error) {
	bodyArr, err := xml.Marshal(&feedObj)
	if err != nil {
		return nil, nil, err
	}
	idx := bytes.LastIndex(bodyArr, []byte("</feed>"))
	if idx < 0 {
		return nil, nil, errors.New("feed frame missing closing tag")
	}
	return bodyArr[:idx], bodyArr[idx:], nil
}

// // // // // // // // // //

// Build creates an Atom document from publish history.
// Empty key means the global feed; feedSize caps events and RAM use; unknown empty per-key feeds return serr.ErrNotFound.
func Build(ctx context.Context, store FeedReaderInterface, st StateReaderInterface, key string, feedSize int, linkObj link.Obj) ([]byte, BuildStatsObj, error) {
	feedSize = max(1, feedSize)
	eventArr, err := store.ListPublishFeed(ctx, key, feedSize)
	if err != nil {
		return nil, BuildStatsObj{}, err
	}
	if key != "" && len(eventArr) == 0 {
		if _, known := st.KeyState(key); !known {
			return nil, BuildStatsObj{}, serr.ErrNotFound
		}
	}

	statsObj := BuildStatsObj{}
	feedObj := feedDocObj{Xmlns: "http://www.w3.org/2005/Atom"}
	if key == "" {
		feedObj.Title = "yggvault — releases"
		feedObj.ID = "urn:ygg:feed"
		feedObj.Link = feedLinkObj{Href: servicePath(linkObj, "/feed.xml"), Rel: "self"}
	} else {
		feedObj.Title = key + " — releases"
		feedObj.ID = "urn:ygg:feed:" + key
		feedObj.Link = feedLinkObj{Href: linkObj.Abs(linkObj.Key(key, "/releases.xml")), Rel: "self"}
	}

	updated := time.Now().UTC()
	if len(eventArr) > 0 {
		updated = eventArr[0].EventTS.UTC()
	}
	feedObj.Updated = updated.Format(time.RFC3339)

	prefixArr, suffixArr, err := marshalFeedFrame(feedObj)
	if err != nil {
		return nil, statsObj, err
	}
	bodyLen := len(xml.Header) + len(prefixArr) + len(suffixArr)
	entryBytesArr := make([][]byte, 0, len(eventArr))
	for i := range eventArr {
		eventObj := eventArr[i]
		hashObj := eventObj.TreeHash
		if !eventObj.BodyHash.IsZero() {
			hashObj = eventObj.BodyHash
		}
		action := "released"
		if !eventObj.FirstPublish {
			action = "archive updated"
		}
		notesText, truncated := truncateNotes(eventObj.ReleaseNotes)
		entryObj := entryObj{
			Title:   eventObj.Key + "@" + eventObj.Version + " — " + action,
			ID:      "urn:ygg:" + eventObj.Key + ":" + eventObj.Version + ":" + hashObj.Hex(),
			Updated: eventObj.EventTS.UTC().Format(time.RFC3339),
			Link:    feedLinkObj{Href: linkObj.Abs(linkObj.Key(eventObj.Key, "/"+eventObj.Version)), Rel: "alternate"},
			Content: contentObj{Type: "html", Body: renderNotes(notesText)},
		}
		entryArr, marshalErr := xml.Marshal(&entryObj)
		if marshalErr != nil {
			return nil, statsObj, marshalErr
		}
		if bodyLen+len(entryArr) > cMaxFeedBytes {
			statsObj.EntriesDropped = len(eventArr) - i
			break
		}
		if truncated {
			statsObj.NotesTruncated++
		}
		entryBytesArr = append(entryBytesArr, entryArr)
		bodyLen += len(entryArr)
		statsObj.EntriesWritten++
	}

	var bufObj bytes.Buffer
	bufObj.Grow(bodyLen)
	bufObj.WriteString(xml.Header)
	bufObj.Write(prefixArr)
	for i := range entryBytesArr {
		bufObj.Write(entryBytesArr[i])
	}
	bufObj.Write(suffixArr)
	statsObj.BodyBytes = bufObj.Len()
	return bufObj.Bytes(), statsObj, nil
}
