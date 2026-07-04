package dataapi

import (
	"context"
	"encoding/base64"
	"errors"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/server/serr"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/target/api"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type fakeStateObj struct {
	byKey map[string]state.KeyStateObj
	order []state.KeyStateObj
}

func (f *fakeStateObj) KeyState(key string) (state.KeyStateObj, bool) {
	ksObj, ok := f.byKey[key]
	return ksObj, ok
}
func (f *fakeStateObj) KeyStates() []state.KeyStateObj  { return f.order }
func (f *fakeStateObj) Checksums() state.ChecksumSetObj { return state.ChecksumSetObj{} }

type fakeVersionStoreObj struct {
	versions map[string][]core.VersionObj
	getErr   error
}

func (f *fakeVersionStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	arr := f.versions[key]
	start := 0
	if afterVersion != "" {
		for i := range arr {
			if arr[i].UpstreamSeq == afterSeq && arr[i].Version == afterVersion {
				start = i + 1
				break
			}
		}
	}
	out := make([]core.VersionObj, 0, limit)
	for i := start; i < len(arr) && len(out) < limit; i++ {
		out = append(out, arr[i])
	}
	return out, nil
}

type fakeDetailStoreObj struct {
	version   core.VersionObj
	found     bool
	detection core.DetectionObj
	detectOK  bool
	artifacts []core.ArtifactObj
	artifErr  error
}

func (f *fakeDetailStoreObj) GetVersion(_ context.Context, _ string, _ string) (core.VersionObj, bool, error) {
	return f.version, f.found, nil
}
func (f *fakeDetailStoreObj) GetDetection(_ context.Context, _ string, _ string) (core.DetectionObj, bool, error) {
	return f.detection, f.detectOK, nil
}
func (f *fakeDetailStoreObj) ListArtifacts(_ context.Context, _ string, _ string) ([]core.ArtifactObj, error) {
	return f.artifacts, f.artifErr
}

// // // // // // // // // //

func TestCursorRoundtrip(t *testing.T) {
	seq, version := int64(42), "v1.2.3"
	enc := encodeReleaseCursor(seq, version)
	gotSeq, gotVersion, err := decodeReleaseCursor(enc)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if gotSeq != seq || gotVersion != version {
		t.Fatalf("roundtrip mismatch: seq %d→%d, version %q→%q", seq, gotSeq, version, gotVersion)
	}
}

func TestCursorEmptyFirstPage(t *testing.T) {
	gs, gv, err := decodeReleaseCursor("")
	if err != nil || gs != 0 || gv != "" {
		t.Fatalf("empty cursor: want (0,\"\",nil), got (%d,%q,%v)", gs, gv, err)
	}
}

func TestCursorGarbage(t *testing.T) {
	if _, _, err := decodeReleaseCursor("!!!not base64!!!"); !errors.Is(err, serr.ErrBadInput) {
		t.Fatalf("garbage base64: want ErrBadInput, got %v", err)
	}
	noSep := encodeReleaseCursorNoNUL("noseparator")
	if _, _, err := decodeReleaseCursor(noSep); !errors.Is(err, serr.ErrBadInput) {
		t.Fatalf("no-separator cursor: want ErrBadInput, got %v", err)
	}
	// Non-numeric and negative seq values are malicious cursors.
	for _, raw := range []string{"abc\x00v1.0.0", "-5\x00v1.0.0", "1\x00"} {
		enc := base64.RawURLEncoding.EncodeToString([]byte(raw))
		if _, _, err := decodeReleaseCursor(enc); !errors.Is(err, serr.ErrBadInput) {
			t.Fatalf("cursor %q: want ErrBadInput, got %v", raw, err)
		}
	}
}

// // // // // // // // // //

func seedState() *fakeStateObj {
	scan := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	okObj := state.KeyStateObj{
		Key:            "pkg/alpha",
		Status:         stcode.OperationalStatusOk,
		Classification: stcode.SourceClassGit,
		Classified:     true,
		SourceURL:      "https://example.test/alpha.git",
		Availability:   stcode.AvailabilityStatusAvailable,
		LastScan:       scan,
		LatestVersion:  "v2.0.0",
	}
	degradedObj := state.KeyStateObj{
		Key:          "pkg/beta",
		Status:       stcode.OperationalStatusDegraded,
		Classified:   false,
		SourceURL:    "https://example.test/beta",
		Availability: stcode.AvailabilityStatusAvailable,
		LastScan:     scan,
	}
	return &fakeStateObj{
		byKey: map[string]state.KeyStateObj{okObj.Key: okObj, degradedObj.Key: degradedObj},
		order: []state.KeyStateObj{okObj, degradedObj},
	}
}

func TestBuildCatalog(t *testing.T) {
	st := seedState()
	linkObj := link.Obj{}
	catObj := BuildCatalog(st, linkObj)
	if len(catObj.Keys) != 2 {
		t.Fatalf("want 2 catalog keys, got %d", len(catObj.Keys))
	}

	alpha := catObj.Keys[0]
	if alpha.Key != "pkg/alpha" {
		t.Fatalf("first key mismatch: %q", alpha.Key)
	}
	if alpha.Status != api.CatalogObjKeysItemStatusOk {
		t.Errorf("alpha status: want ok, got %v", alpha.Status)
	}
	if cls, ok := alpha.Classification.Get(); !ok || cls != api.CatalogObjKeysItemClassificationGit {
		t.Errorf("alpha classification: want git, got ok=%v cls=%v", ok, cls)
	}
	if latest, ok := alpha.Latest.Get(); !ok || latest != "v2.0.0" {
		t.Errorf("alpha latest: want v2.0.0, got ok=%v v=%q", ok, latest)
	}
	if url, ok := alpha.URL.Get(); !ok || url != "/pkg/alpha/releases.json" {
		t.Errorf("alpha url: want /pkg/alpha/releases.json, got %q", url)
	}
	if alpha.Source.Status != api.SourceStatusObjStatusAvailable || alpha.Source.URL != "https://example.test/alpha.git" {
		t.Errorf("alpha source mismatch: %+v", alpha.Source)
	}

	beta := catObj.Keys[1]
	if beta.Status != api.CatalogObjKeysItemStatusDegraded {
		t.Errorf("beta status: want degraded, got %v", beta.Status)
	}
	if beta.Classification.IsSet() {
		t.Errorf("beta is unclassified → classification must be unset, got %+v", beta.Classification)
	}
	if beta.Latest.IsSet() {
		t.Errorf("beta has no latest → must be unset, got %+v", beta.Latest)
	}
}

// // // // // // // // // //

func makeVersions(key string, n int) []core.VersionObj {
	arr := make([]core.VersionObj, 0, n)
	base := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < n; i++ {
		arr = append(arr, core.VersionObj{
			Key:             key,
			Version:         "v1.0." + itoa(n-i),
			UpstreamSeq:     int64(n - i),
			IngestTS:        base.Add(-time.Duration(i) * time.Hour),
			SourceSizeBytes: uint64(100 + i),
		})
	}
	return arr
}

func TestBuildReleaseListPagination(t *testing.T) {
	const pageSize = 3
	key := "pkg/alpha"
	store := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{key: makeVersions(key, pageSize+1)}}
	st := seedState()

	listObj, err := BuildReleaseList(context.Background(), store, st, key, "", pageSize, link.Obj{})
	if err != nil {
		t.Fatalf("BuildReleaseList: %v", err)
	}
	if len(listObj.Releases) != pageSize {
		t.Fatalf("page items: want %d, got %d", pageSize, len(listObj.Releases))
	}
	next, ok := listObj.Next.Get()
	if !ok || next == "" {
		t.Fatalf("expected non-empty next cursor on full page")
	}
	if listObj.Key != key {
		t.Errorf("list key mismatch: %q", listObj.Key)
	}
	if url, ok := listObj.Releases[0].URL.Get(); !ok || url == "" {
		t.Errorf("release url must be set, got %q", url)
	}

	tailObj, err := BuildReleaseList(context.Background(), store, st, key, next, pageSize, link.Obj{})
	if err != nil {
		t.Fatalf("tail BuildReleaseList: %v", err)
	}
	if len(tailObj.Releases) != 1 {
		t.Fatalf("tail items: want 1, got %d", len(tailObj.Releases))
	}
	if tailObj.Next.IsSet() {
		t.Errorf("last page must not carry next cursor, got %+v", tailObj.Next)
	}
}

func TestBuildReleaseListUnknownKeyNotFound(t *testing.T) {
	store := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{}}
	st := &fakeStateObj{byKey: map[string]state.KeyStateObj{}}
	_, err := BuildReleaseList(context.Background(), store, st, "pkg/missing", "", 10, link.Obj{})
	if !errors.Is(err, serr.ErrNotFound) {
		t.Fatalf("unknown key empty: want ErrNotFound, got %v", err)
	}
}

func TestBuildReleaseListBadCursor(t *testing.T) {
	store := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{}}
	st := seedState()
	_, err := BuildReleaseList(context.Background(), store, st, "pkg/alpha", "###", 10, link.Obj{})
	if !errors.Is(err, serr.ErrBadInput) {
		t.Fatalf("bad cursor: want ErrBadInput, got %v", err)
	}
}

// // // // // // // // // //

func TestBuildReleaseDetailFound(t *testing.T) {
	treeHash := core.HashBytes([]byte("tree-content"))
	store := &fakeDetailStoreObj{
		version: core.VersionObj{
			Key:             "pkg/alpha",
			Version:         "v2.0.0",
			TreeHash:        treeHash,
			IngestTS:        time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC),
			SourceSizeBytes: 4242,
			UpstreamDeleted: false,
			ReleaseNotes:    "# notes",
		},
		found:     true,
		detection: core.DetectionObj{IsGo: true},
		detectOK:  true,
		artifacts: nil,
	}
	st := seedState()

	detailObj, found, err := BuildReleaseDetail(context.Background(), store, st, "pkg/alpha", "v2.0.0", link.Obj{})
	if err != nil || !found {
		t.Fatalf("detail found: err=%v found=%v", err, found)
	}
	if detailObj.Key != "pkg/alpha" || detailObj.Version != "v2.0.0" {
		t.Errorf("key/version mismatch: %+v", detailObj)
	}
	if h, ok := detailObj.Hash.Get(); !ok || h != treeHash.Hex() {
		t.Errorf("hash mismatch: want %q got ok=%v %q", treeHash.Hex(), ok, h)
	}
	if sz, ok := detailObj.Size.Get(); !ok || sz != 4242 {
		t.Errorf("size mismatch: %v %v", ok, sz)
	}
	if up, ok := detailObj.UpstreamPresent.Get(); !ok || !up {
		t.Errorf("upstream_present mismatch: %v %v", ok, up)
	}
	if notes, ok := detailObj.NotesMarkdown.Get(); !ok || notes != "# notes" {
		t.Errorf("notes mismatch: %v %q", ok, notes)
	}
	if len(detailObj.Overlays) != 1 || detailObj.Overlays[0] != "go" {
		t.Errorf("overlays: want [go], got %v", detailObj.Overlays)
	}
}

func TestBuildReleaseDetailMissing(t *testing.T) {
	store := &fakeDetailStoreObj{found: false}
	st := seedState()
	_, found, err := BuildReleaseDetail(context.Background(), store, st, "pkg/alpha", "v9.9.9", link.Obj{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if found {
		t.Fatal("missing version must yield found=false")
	}
}

// // // // // // // // // //

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

func encodeReleaseCursorNoNUL(s string) string {
	return base64.RawURLEncoding.EncodeToString([]byte(s))
}
