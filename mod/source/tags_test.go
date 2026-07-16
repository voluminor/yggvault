package source

import (
	"context"
	"fmt"
	"net/url"
	"testing"

	lightweigit "github.com/voluminor/lightweigit-loader"
	"github.com/voluminor/lightweigit-loader/target"
)

// // // // // // // // // //

type fakeTagObj struct {
	name string
}

func (f fakeTagObj) Mod() target.ModType { return target.ModTypeUnknown }
func (f fakeTagObj) Marshal() []byte     { return nil }
func (f fakeTagObj) String() string      { return f.name }
func (f fakeTagObj) URL() *url.URL {
	return &url.URL{Scheme: "https", Host: "git.example", Path: "/o/r/tag/" + f.name}
}
func (f fakeTagObj) ZIP() *url.URL {
	return &url.URL{Scheme: "https", Host: "git.example", Path: "/o/r/archive/" + f.name + ".zip"}
}
func (f fakeTagObj) TAR() *url.URL {
	return &url.URL{Scheme: "https", Host: "git.example", Path: "/o/r/archive/" + f.name + ".tar.gz"}
}

// //

type fakeTagProviderObj struct {
	tagArr    []string
	streamErr error
	gotDepth  int
}

func (f *fakeTagProviderObj) Type() string   { return "fake" }
func (f *fakeTagProviderObj) Domain() string { return "git.example" }
func (f *fakeTagProviderObj) String() string { return "fake" }
func (f *fakeTagProviderObj) URL() *url.URL  { return &url.URL{Scheme: "https", Host: "git.example"} }

func (f *fakeTagProviderObj) TagLatest() (lightweigit.ProviderTagInterface, error) {
	return nil, lightweigit.ErrNotFound
}
func (f *fakeTagProviderObj) TagFind(string) (lightweigit.ProviderTagInterface, error) {
	return nil, lightweigit.ErrNotFound
}
func (f *fakeTagProviderObj) TagsStream(ctx context.Context, tagCh chan lightweigit.ProviderTagInterface, depth int) error {
	f.gotDepth = depth
	for _, name := range f.tagArr {
		select {
		case tagCh <- fakeTagObj{name: name}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return f.streamErr
}

func (f *fakeTagProviderObj) ReleaseLatest() (lightweigit.ProviderReleaseInterface, error) {
	return nil, lightweigit.ErrNotFound
}
func (f *fakeTagProviderObj) ReleaseFind(string) (lightweigit.ProviderReleaseInterface, error) {
	return nil, lightweigit.ErrNotFound
}
func (f *fakeTagProviderObj) ReleasesStream(context.Context, chan lightweigit.ProviderReleaseInterface, int) error {
	return nil
}

// // // // // // // // // //

func TestCollectTagsMapsAndFilters(t *testing.T) {
	providerObj := &fakeTagProviderObj{tagArr: []string{
		"v2.0.0",
		"v2.1.0-rc.1",
		"INKSCAPE_1_3_2",
		"blockly-v9.3.3",
	}}

	outArr, truncated, err := collectTags(context.Background(), providerObj, 7)
	if err != nil {
		t.Fatalf("collectTags: %v", err)
	}
	if truncated {
		t.Fatal("unexpected truncation")
	}
	if providerObj.gotDepth != 7 {
		t.Fatalf("depth=%d, want 7", providerObj.gotDepth)
	}

	wantArr := []string{"v2.0.0", "INKSCAPE_1_3_2", "blockly-v9.3.3"}
	if len(outArr) != len(wantArr) {
		t.Fatalf("got %d tags, want %d: %+v", len(outArr), len(wantArr), outArr)
	}
	for i, wantVersion := range wantArr {
		relObj := outArr[i]
		if relObj.Version != wantVersion {
			t.Fatalf("tag[%d]=%q, want %q", i, relObj.Version, wantVersion)
		}
		if relObj.BodyMD != "" {
			t.Fatalf("tag %q must have empty body, got %q", wantVersion, relObj.BodyMD)
		}
		if relObj.Format != cFormatZip {
			t.Fatalf("tag %q format=%q, want zip", wantVersion, relObj.Format)
		}
		wantURL := "https://git.example/o/r/archive/" + wantVersion + ".zip"
		if relObj.ArchiveURL != wantURL {
			t.Fatalf("tag %q archive url=%q, want %q", wantVersion, relObj.ArchiveURL, wantURL)
		}
	}
}

func TestCollectTagsTruncation(t *testing.T) {
	tagArr := make([]string, 0, cMaxReleases+2)
	for i := 0; i < cMaxReleases+2; i++ {
		tagArr = append(tagArr, fmt.Sprintf("tag-%d.x", i))
	}
	providerObj := &fakeTagProviderObj{tagArr: tagArr}

	outArr, truncated, err := collectTags(context.Background(), providerObj, 0)
	if err != nil {
		t.Fatalf("collectTags: %v", err)
	}
	if !truncated {
		t.Fatal("expected truncation flag")
	}
	if len(outArr) != cMaxReleases {
		t.Fatalf("got %d tags, want cap %d", len(outArr), cMaxReleases)
	}
}

func TestCollectTagsStreamError(t *testing.T) {
	providerObj := &fakeTagProviderObj{tagArr: []string{"v1.0.0"}, streamErr: lightweigit.ErrNotFound}
	if _, _, err := collectTags(context.Background(), providerObj, 0); err == nil {
		t.Fatal("expected stream error")
	}
}

// // // // // // // // // //

type endlessTagProviderObj struct {
	fakeTagProviderObj
}

func (f *endlessTagProviderObj) TagsStream(ctx context.Context, tagCh chan lightweigit.ProviderTagInterface, _ int) error {
	for i := 0; ; i++ {
		select {
		case tagCh <- fakeTagObj{name: fmt.Sprintf("v1.0.%d", i)}:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// A hostile endless listing must truncate at the cap instead of blocking rescan forever.
func TestCollectTagsEndlessStreamTruncates(t *testing.T) {
	arr, truncated, err := collectTags(context.Background(), &endlessTagProviderObj{}, 0)
	if err != nil {
		t.Fatalf("collectTags: %v", err)
	}
	if !truncated || len(arr) != cMaxReleases {
		t.Fatalf("truncated=%v len=%d, want truncated at %d", truncated, len(arr), cMaxReleases)
	}
}
