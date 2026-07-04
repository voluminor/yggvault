package sitemap

import (
	"context"
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
		return nil, nil // single-page fixture: the first page returns everything
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
	body, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, true)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	text := string(body)

	for _, want := range []string{
		`<?xml version="1.0" encoding="UTF-8"?>`,
		`<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">`,
		`<loc>https://vault.test/pkg/</loc>`,              // catalog
		`<loc>https://vault.test/metrics</loc>`,           // metrics (service route at root)
		`<loc>https://vault.test/pkg/errors</loc>`,        // key page
		`<loc>https://vault.test/pkg/errors/v0.7.1</loc>`, // version page
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
	body, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if strings.Contains(string(body), "/metrics") {
		t.Fatalf("metrics URL must be absent when disabled:\n%s", body)
	}
}

func TestBuildCapTruncatesNewestFirst(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test", RoutePrefix: "pkg"}
	// cap=2 keeps catalog and the key page, then cuts the walk off before versions.
	body, err := Build(context.Background(), mkStore(), mkState(), linkObj, 2, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if n := strings.Count(string(body), "<url>"); n != 2 {
		t.Fatalf("cap=2 produced %d urls, want 2:\n%s", n, body)
	}
}

func TestBuildRootWhenNoPrefix(t *testing.T) {
	linkObj := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	body, err := Build(context.Background(), mkStore(), mkState(), linkObj, 100, false)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if !strings.Contains(string(body), "<loc>https://vault.test/errors/v0.7.1</loc>") {
		t.Fatalf("root-mode version URL missing:\n%s", body)
	}
}
