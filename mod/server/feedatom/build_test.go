package feedatom

import (
	"context"
	"encoding/xml"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

type fakeFeedStoreObj struct {
	feed []core.FeedEventObj
}

func (obj *fakeFeedStoreObj) ListPublishFeed(_ context.Context, key string, limit int) ([]core.FeedEventObj, error) {
	outArr := make([]core.FeedEventObj, 0, len(obj.feed))
	for i := range obj.feed {
		eventObj := obj.feed[i]
		if key != "" && eventObj.Key != key {
			continue
		}
		outArr = append(outArr, eventObj)
		if len(outArr) >= limit {
			break
		}
	}
	return outArr, nil
}

type fakeFeedStateObj struct {
	keys map[string]state.KeyStateObj
}

func (obj *fakeFeedStateObj) KeyState(key string) (state.KeyStateObj, bool) {
	stateObj, ok := obj.keys[key]
	return stateObj, ok
}

// //

type atomDocObj struct {
	XMLName xml.Name       `xml:"feed"`
	Entries []atomEntryObj `xml:"entry"`
}

type atomEntryObj struct {
	Title string `xml:"title"`
}

// // // // // // // // // //

func hugeFeedFixture() (*fakeFeedStoreObj, *fakeFeedStateObj) {
	nowObj := time.Unix(20000, 0).UTC()
	notesText := strings.Repeat("&", 128<<10)
	feedArr := make([]core.FeedEventObj, 500)
	for i := range feedArr {
		versionText := fmt.Sprintf("v%03d", i)
		feedArr[i] = core.FeedEventObj{
			Key:          "lib",
			Version:      versionText,
			EventTS:      nowObj.Add(-time.Duration(i) * time.Second),
			TreeHash:     core.HashBytes([]byte(versionText)),
			ReleaseNotes: notesText,
			FirstPublish: true,
		}
	}
	return &fakeFeedStoreObj{feed: feedArr}, &fakeFeedStateObj{keys: map[string]state.KeyStateObj{"lib": {Key: "lib", VersionCount: uint64(len(feedArr))}}}
}

func assertHugeFeed(t *testing.T, body []byte, statsObj BuildStatsObj) {
	t.Helper()
	if len(body) > cMaxFeedBytes {
		t.Fatalf("body has %d bytes, want <= %d", len(body), cMaxFeedBytes)
	}
	if statsObj.BodyBytes != len(body) {
		t.Fatalf("BodyBytes=%d, len(body)=%d", statsObj.BodyBytes, len(body))
	}
	if statsObj.NotesTruncated == 0 {
		t.Fatalf("notes truncation was not reported: %+v", statsObj)
	}
	if statsObj.EntriesDropped == 0 {
		t.Fatalf("feed overflow was not reported: %+v", statsObj)
	}
	if !strings.Contains(string(body), "[notes truncated]") {
		t.Fatalf("truncation marker missing")
	}
	if strings.Contains(string(body), "lib@v499") {
		t.Fatalf("oldest entry should have been dropped")
	}

	var docObj atomDocObj
	if err := xml.Unmarshal(body, &docObj); err != nil {
		t.Fatalf("feed XML is invalid: %v", err)
	}
	if len(docObj.Entries) == 0 {
		t.Fatalf("feed has no entries")
	}
	if docObj.Entries[0].Title != "lib@v000 — released" {
		t.Fatalf("freshest entry was not preserved first: %q", docObj.Entries[0].Title)
	}
	if len(docObj.Entries) != statsObj.EntriesWritten {
		t.Fatalf("parsed entries=%d, stats=%d", len(docObj.Entries), statsObj.EntriesWritten)
	}
}

func TestBuildGlobalFeedByteCapAndValidXML(t *testing.T) {
	storeObj, stateObj := hugeFeedFixture()
	body, statsObj, err := Build(context.Background(), storeObj, stateObj, "", 500, link.Obj{Scheme: "https", EntryHost: "vault.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertHugeFeed(t, body, statsObj)
}

func TestBuildKeyFeedByteCapAndValidXML(t *testing.T) {
	storeObj, stateObj := hugeFeedFixture()
	body, statsObj, err := Build(context.Background(), storeObj, stateObj, "lib", 500, link.Obj{Scheme: "https", EntryHost: "vault.test"})
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	assertHugeFeed(t, body, statsObj)
	if !strings.Contains(string(body), `<id>urn:ygg:feed:lib</id>`) {
		t.Fatalf("key feed id missing")
	}
}
