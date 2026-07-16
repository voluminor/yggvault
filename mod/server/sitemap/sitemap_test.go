package sitemap

import (
	"context"
	"encoding/xml"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/state"
)

// // // // // // // // // //

type fakeStoreObj struct {
	versions map[string][]core.VersionObj
}

func (f *fakeStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, _ int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	if afterVersion != "" {
		return nil, nil
	}
	arr := f.versions[key]
	if len(arr) > limit {
		arr = arr[:limit]
	}
	return arr, nil
}

type fakeStateObj struct {
	keys []state.KeyStateObj
}

func (f *fakeStateObj) KeyStates() []state.KeyStateObj { return f.keys }

// //

func mkState() *fakeStateObj {
	return &fakeStateObj{keys: []state.KeyStateObj{
		{Key: "errors", VersionCount: 2, LatestVersion: "v0.7.1", LastPublishTS: time.Unix(1000, 0)},
		{Key: "empty", VersionCount: 0},
	}}
}

func mkStore() *fakeStoreObj {
	return &fakeStoreObj{versions: map[string][]core.VersionObj{
		"errors": {
			{Key: "errors", Version: "v0.7.1", IngestTS: time.Unix(1000, 0)},
			{Key: "errors", Version: "v0.7.0", IngestTS: time.Unix(900, 0)},
		},
	}}
}

// // // // // // // // // //

func TestBuildNestedAbsoluteURLsAndLastmod(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test", RoutePrefix: "pkg"}
	body, statsObj, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, true)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if statsObj.Truncated() {
		t.Fatalf("small sitemap was truncated: %+v", statsObj)
	}
	text := string(body)

	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
		`<loc>https://vault.test/pkg/</loc>`,
		`<loc>https://vault.test/metrics</loc>`,
		`<loc>https://vault.test/pkg/errors</loc>`,
		`<loc>https://vault.test/pkg/errors/v0.7.1</loc>`,
		`<loc>https://vault.test/pkg/errors/v0.7.0</loc>`,
		`</urlset>`,
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("sitemap missing %q in:\n%s", want, text)
		}
	}
	if strings.Contains(text, "/pkg/empty") {
		t.Fatalf("zero-version key must be omitted:\n%s", text)
	}
	if !strings.Contains(text, "<lastmod>1970-01-01T00:16:40Z</lastmod>") {
		t.Fatalf("version lastmod (IngestTS) missing:\n%s", text)
	}
}

func TestBuildMetricsExcludedWhenDisabled(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test", RoutePrefix: "pkg"}
	body, _, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(body), "/metrics") {
		t.Fatalf("metrics URL must be absent when disabled:\n%s", body)
	}
}

func TestBuildCapTruncatesNewestFirst(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test", RoutePrefix: "pkg"}
	body, statsObj, err := Build(context.Background(), mkStore(), mkState(), linkObj, 2, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := strings.Count(string(body), "<url>"); n != 2 {
		t.Fatalf("cap=2 produced %d urls, want 2:\n%s", n, body)
	}
	if !statsObj.URLTruncated || statsObj.URLsDropped == 0 {
		t.Fatalf("stats should report URL truncation: %+v", statsObj)
	}
}

func TestBuildRootWhenNoPrefix(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	body, _, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(body), "<loc>https://vault.test/errors/v0.7.1</loc>") {
		t.Fatalf("root-mode version URL missing:\n%s", body)
	}
}

func TestBuildByteCapKeepsValidXML(t *testing.T) {
	longVersion := "v" + strings.Repeat("x", 9000)
	versionsArr := make([]core.VersionObj, 1024)
	for i := range versionsArr {
		versionsArr[i] = core.VersionObj{Key: "huge", Version: longVersion + string(rune('a'+i%26)), IngestTS: time.Unix(int64(10000-i), 0)}
	}
	storeObj := &fakeStoreObj{versions: map[string][]core.VersionObj{"huge": versionsArr}}
	stateObj := &fakeStateObj{keys: []state.KeyStateObj{{Key: "huge", VersionCount: 2000, LatestVersion: versionsArr[0].Version, LastPublishTS: time.Unix(10000, 0)}}}

	body, statsObj, err := Build(context.Background(), storeObj, stateObj, link.Obj{Scheme: "https", EntryHost: "vault.test"}, 50000, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if len(body) > cMaxBytes {
		t.Fatalf("body has %d bytes, want <= %d", len(body), cMaxBytes)
	}
	if !statsObj.ByteTruncated || statsObj.URLsDropped == 0 {
		t.Fatalf("stats should report byte truncation: %+v", statsObj)
	}
	var docObj struct {
		XMLName xml.Name `xml:"urlset"`
	}
	if err := xml.Unmarshal(body, &docObj); err != nil {
		t.Fatalf("sitemap XML is invalid: %v", err)
	}
	if !strings.HasSuffix(string(body), "</urlset>\n") {
		t.Fatalf("sitemap missing closing urlset")
	}
}
