package feedatom

import (
	"context"
	"encoding/xml"
	"time"

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

// // // // // // // // // //

func renderNotes(markdownText string) string {
	return string(markdown.SafeHTML(markdownText))
}

func servicePath(linkObj link.Obj, suffix string) string {
	pathText := suffix
	if linkObj.RoutePrefix != "" {
		pathText = "/" + linkObj.RoutePrefix + suffix
	}
	return linkObj.Abs(pathText)
}

// // // // // // // // // //

// Build creates an Atom document from publish history.
// Empty key means the global feed; feedSize caps events and RAM use; unknown empty per-key feeds return serr.ErrNotFound.
func Build(ctx context.Context, store FeedReaderInterface, st StateReaderInterface, key string, feedSize int, linkObj link.Obj) ([]byte, error) {
	feedSize = max(1, feedSize)
	eventArr, err := store.ListPublishFeed(ctx, key, feedSize)
	if err != nil {
		return nil, err
	}
	if key != "" && len(eventArr) == 0 {
		if _, known := st.KeyState(key); !known {
			return nil, serr.ErrNotFound
		}
	}

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

	feedObj.Entries = make([]entryObj, 0, len(eventArr))
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
		feedObj.Entries = append(feedObj.Entries, entryObj{
			Title:   eventObj.Key + "@" + eventObj.Version + " — " + action,
			ID:      "urn:ygg:" + eventObj.Key + ":" + eventObj.Version + ":" + hashObj.Hex(),
			Updated: eventObj.EventTS.UTC().Format(time.RFC3339),
			Link:    feedLinkObj{Href: linkObj.Abs(linkObj.Key(eventObj.Key, "/"+eventObj.Version)), Rel: "alternate"},
			Content: contentObj{Type: "html", Body: renderNotes(eventObj.ReleaseNotes)},
		})
	}

	bodyArr, err := xml.Marshal(&feedObj)
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), bodyArr...), nil
}
