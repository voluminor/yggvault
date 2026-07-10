package webui

import (
	"bytes"
	"context"
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/server/link"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/view"
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
	versions    map[string][]core.VersionObj
	detection   core.DetectionObj
	detectionOK bool
}

func (f *fakeVersionStoreObj) CountVersions(_ context.Context, key string) (uint64, error) {
	return uint64(len(f.versions[key])), nil
}
func (f *fakeVersionStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	return keysetForward(f.versions[key], afterSeq, afterVersion, limit), nil
}
func (f *fakeVersionStoreObj) ListVersionsKeysetBefore(_ context.Context, key string, _ bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error) {
	return keysetBefore(f.versions[key], beforeSeq, beforeVersion, limit), nil
}
func (f *fakeVersionStoreObj) GetDetection(_ context.Context, _ string, _ string) (core.DetectionObj, bool, error) {
	return f.detection, f.detectionOK, nil
}

type fakeDetailStoreObj struct {
	versions    map[string][]core.VersionObj
	versionOf   map[string]core.VersionObj
	artifacts   []core.ArtifactObj
	artifactMap map[core.ArtifactKeyObj]core.ArtifactObj
	detection   core.DetectionObj
	detectionOK bool
	found       bool
}

func (f *fakeDetailStoreObj) GetVersion(_ context.Context, key string, version string) (core.VersionObj, bool, error) {
	vObj, ok := f.versionOf[key+"@"+version]
	if !ok {
		return core.VersionObj{}, false, nil
	}
	return vObj, f.found, nil
}
func (f *fakeDetailStoreObj) GetDetection(_ context.Context, _ string, _ string) (core.DetectionObj, bool, error) {
	return f.detection, f.detectionOK, nil
}
func (f *fakeDetailStoreObj) ListArtifacts(_ context.Context, _ string, _ string) ([]core.ArtifactObj, error) {
	return f.artifacts, nil
}
func (f *fakeDetailStoreObj) GetArtifact(_ context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	artifactObj, ok := f.artifactMap[keyObj]
	return artifactObj, ok, nil
}
func (f *fakeDetailStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	return keysetForward(f.versions[key], afterSeq, afterVersion, limit), nil
}
func (f *fakeDetailStoreObj) ListVersionsKeysetBefore(_ context.Context, key string, _ bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error) {
	return keysetBefore(f.versions[key], beforeSeq, beforeVersion, limit), nil
}

// keysetForward returns the newest-first slice strictly after the (seq,version) cursor; empty starts at head.
func keysetForward(arr []core.VersionObj, afterSeq int64, afterVersion string, limit int) []core.VersionObj {
	start := 0
	if afterVersion != "" {
		for i := range arr {
			if arr[i].UpstreamSeq == afterSeq && arr[i].Version == afterVersion {
				start = i + 1
				break
			}
		}
	}
	if start >= len(arr) {
		return nil
	}
	end := start + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[start:end]
}

// keysetBefore returns versions newer than the cursor in ascending order, closest to the cursor first,
// mirroring real ListVersionsKeysetBefore over a newest-first slice.
func keysetBefore(arr []core.VersionObj, beforeSeq int64, beforeVersion string, limit int) []core.VersionObj {
	cursorIdx := len(arr)
	for i := range arr {
		if arr[i].UpstreamSeq == beforeSeq && arr[i].Version == beforeVersion {
			cursorIdx = i
			break
		}
	}
	out := make([]core.VersionObj, 0, limit)
	for i := cursorIdx - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, arr[i])
	}
	return out
}

// fakeOverlayObj simulates overlay binding for one entry; module path is built from its host.
type fakeOverlayObj struct {
	host        string
	publishable bool
}

func (f fakeOverlayObj) GoPublishable(_ string, _ string, _ core.DetectionObj, _ *overlay.CandidateObj) bool {
	return f.publishable
}
func (f fakeOverlayObj) TargetModulePath(key string, _ string) string { return f.host + "/" + key }
func (f fakeOverlayObj) UniversalTopDir(key string, version string, _ core.DetectionObj, _ *overlay.CandidateObj) string {
	return key + "-" + version
}

// // // // // // // // // //

func seedState() *fakeStateObj {
	ksObj := state.KeyStateObj{
		Key:            "pkg/alpha",
		Status:         stcode.OperationalStatusOk,
		Classification: stcode.SourceClassGit,
		Classified:     true,
		SourceURL:      "https://example.test/alpha.git",
		Availability:   stcode.AvailabilityStatusAvailable,
		LastScan:       time.Now(),
		LatestVersion:  "v2.0.0",
	}
	return &fakeStateObj{
		byKey: map[string]state.KeyStateObj{ksObj.Key: ksObj},
		order: []state.KeyStateObj{ksObj},
	}
}

func testContext(_ StateReaderInterface, lnk link.Obj) view.ContextObj {
	return view.ContextObj{
		Service: view.ServiceObj{
			Name:    "yggvault",
			Tagline: "content-addressed release vault",
			HomeURL: lnk.Key("", ""),
		},
		Client: view.ClientObj{
			Channel: "web",
			Scheme:  lnk.Scheme,
			Host:    lnk.EntryHost,
		},
	}
}

// // // // // // // // // //

func TestCatalogContainsKey(t *testing.T) {
	st := seedState()
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	store := &fakeVersionStoreObj{detection: core.DetectionObj{IsGo: true}, detectionOK: true}
	bodyArr, err := Catalog(context.Background(), st, store, testContext(st, lnk))
	if err != nil {
		t.Fatalf("Catalog: %v", err)
	}
	if !strings.Contains(string(bodyArr), "pkg/alpha") {
		t.Fatalf("catalog HTML must contain the key name")
	}
}

func TestKeyFoundAndMissing(t *testing.T) {
	key := "pkg/alpha"
	store := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{
		key: {{Key: key, Version: "v2.0.0", IngestTS: time.Now(), SourceSizeBytes: 100, TreeHash: core.HashBytes([]byte("t"))}},
	}}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}

	st := seedState()
	bodyArr, found, err := Key(context.Background(), st, store, lnk, testContext(st, lnk), key, PageCursorObj{}, 10)
	if err != nil || !found {
		t.Fatalf("Key found: err=%v found=%v", err, found)
	}
	if !strings.Contains(string(bodyArr), "v2.0.0") {
		t.Errorf("key page must contain version v2.0.0")
	}

	emptyState := &fakeStateObj{byKey: map[string]state.KeyStateObj{}}
	emptyStore := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{}}
	_, found, err = Key(context.Background(), emptyState, emptyStore, lnk, testContext(emptyState, lnk), "pkg/missing", PageCursorObj{}, 10)
	if err != nil {
		t.Fatalf("Key missing: unexpected error %v", err)
	}
	if found {
		t.Fatal("unknown key with no versions must yield found=false")
	}
}

// Key page cursor pagination: newest/older/oldest/newer navigation and neighbor flags.
func TestKeyPaginationKeyset(t *testing.T) {
	key := "pkg/alpha"
	mk := func(version string, seq int64) core.VersionObj {
		return core.VersionObj{Key: key, Version: version, UpstreamSeq: seq}
	}
	store := &fakeVersionStoreObj{versions: map[string][]core.VersionObj{
		key: {mk("v5.0.0", 5), mk("v4.0.0", 4), mk("v3.0.0", 3), mk("v2.0.0", 2), mk("v1.0.0", 1)},
	}}
	names := func(windowArr []core.VersionObj) string {
		partArr := make([]string, len(windowArr))
		for i := range windowArr {
			partArr[i] = windowArr[i].Version
		}
		return strings.Join(partArr, ",")
	}
	cases := []struct {
		name         string
		cursor       PageCursorObj
		want         string
		newer, older bool
	}{
		{"newest", PageCursorObj{}, "v5.0.0,v4.0.0", false, true},
		{"older-from-v4", AfterCursor(4, "v4.0.0"), "v3.0.0,v2.0.0", true, true},
		{"oldest", AfterCursor(2, "v2.0.0"), "v1.0.0", true, false},
		{"newer-to-newest", BeforeCursor(3, "v3.0.0"), "v5.0.0,v4.0.0", false, true},
		{"newer-middle", BeforeCursor(1, "v1.0.0"), "v3.0.0,v2.0.0", true, true},
	}
	for _, tc := range cases {
		windowArr, hasNewer, hasOlder, err := fetchVersionPage(context.Background(), store, key, tc.cursor, 2)
		if err != nil {
			t.Fatalf("%s: %v", tc.name, err)
		}
		if got := names(windowArr); got != tc.want {
			t.Errorf("%s: window=%q want %q", tc.name, got, tc.want)
		}
		if hasNewer != tc.newer || hasOlder != tc.older {
			t.Errorf("%s: newer=%v older=%v, want newer=%v older=%v", tc.name, hasNewer, hasOlder, tc.newer, tc.older)
		}
	}
}

// Keyset cursor round-trip and rejection of malformed tokens, because foreign bytes are untrusted.
func TestCursorRoundTrip(t *testing.T) {
	tok := EncodeCursor(42, "v1.2.3-rc.1")
	if seq, ver, ok := DecodeCursor(tok); !ok || seq != 42 || ver != "v1.2.3-rc.1" {
		t.Fatalf("round-trip: seq=%d ver=%q ok=%v", seq, ver, ok)
	}
	if _, _, ok := DecodeCursor("!!not base64!!"); ok {
		t.Error("malformed token must be rejected")
	}
	if _, _, ok := DecodeCursor(base64.RawURLEncoding.EncodeToString([]byte("no-separator"))); ok {
		t.Error("token without separator must be rejected")
	}
}

// // // // // // // // // //

// Key page go get: latest with major >=2 must include /vN in the module path or the snippet breaks go tooling.
// v1 latest stays suffix-free.
func TestKeyGoSnippetMajorSuffix(t *testing.T) {
	key := "pkg/alpha"
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	goDetection := core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_module_path":"example.test/alpha"}`}

	store := &fakeVersionStoreObj{
		versions: map[string][]core.VersionObj{
			key: {{Key: key, Version: "v2.0.0", IngestTS: time.Now(), SourceSizeBytes: 100, TreeHash: core.HashBytes([]byte("t"))}},
		},
		detection:   goDetection,
		detectionOK: true,
	}
	st := seedState()
	ctxObj := testContext(st, lnk)
	ctxObj.Alternate = view.AlternateObj{Channel: "ygg", Scheme: "http", Host: "[200:1::1]", CopyHost: "node.pk.ygg"}

	bodyArr, found, err := Key(context.Background(), st, store, lnk, ctxObj, key, PageCursorObj{}, 10)
	if err != nil || !found {
		t.Fatalf("Key: err=%v found=%v", err, found)
	}
	body := string(bodyArr)
	if !strings.Contains(body, "go get vault.test/pkg/alpha/v2@latest") {
		t.Error("primary go snippet must carry the /v2 major suffix for a v2 latest")
	}
	if !strings.Contains(body, "go get node.pk.ygg/pkg/alpha/v2@latest") {
		t.Error("alternate go snippet must carry the /v2 major suffix too")
	}
	if strings.Contains(body, "go get vault.test/pkg/alpha@latest") {
		t.Error("suffix-less module path must not survive for a v2 latest")
	}

	// v1 latest: no suffix is added.
	v1State := seedState()
	ksObj := v1State.byKey[key]
	ksObj.LatestVersion = "v1.5.0"
	v1State.byKey[key] = ksObj
	v1State.order = []state.KeyStateObj{ksObj}
	v1Store := &fakeVersionStoreObj{
		versions: map[string][]core.VersionObj{
			key: {{Key: key, Version: "v1.5.0", IngestTS: time.Now(), SourceSizeBytes: 100, TreeHash: core.HashBytes([]byte("t"))}},
		},
		detection:   goDetection,
		detectionOK: true,
	}
	bodyArr, found, err = Key(context.Background(), v1State, v1Store, lnk, testContext(v1State, lnk), key, PageCursorObj{}, 10)
	if err != nil || !found {
		t.Fatalf("Key v1: err=%v found=%v", err, found)
	}
	if !strings.Contains(string(bodyArr), "go get vault.test/pkg/alpha@latest") {
		t.Error("v1 latest must keep the plain module path without a major suffix")
	}
}

// // // // // // // // // //

func TestVersionFoundAndMissing(t *testing.T) {
	key, version := "pkg/alpha", "v2.0.0"
	vObj := core.VersionObj{Key: key, Version: version, IngestTS: time.Now(), SourceSizeBytes: 200, TreeHash: core.HashBytes([]byte("tree"))}
	store := &fakeDetailStoreObj{
		found:     true,
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		versions:  map[string][]core.VersionObj{key: {vObj}},
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}

	st := seedState()
	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, version, cListenerGlobal, nil, "")
	if err != nil || !found {
		t.Fatalf("Version found: err=%v found=%v", err, found)
	}
	if !strings.Contains(string(bodyArr), version) {
		t.Errorf("version page must contain %q", version)
	}

	_, found, err = Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, "v9.9.9", cListenerGlobal, nil, "")
	if err != nil {
		t.Fatalf("Version missing: unexpected error %v", err)
	}
	if found {
		t.Fatal("missing version must yield found=false")
	}
}

// Version history: newest-first keyset walk must find immediate neighbors.
// The newest has no newer link, the oldest has no older link, and the middle has both.
func TestFillHistoryNavNeighbors(t *testing.T) {
	key := "pkg/alpha"
	mk := func(version string, seq int64) core.VersionObj {
		return core.VersionObj{Key: key, Version: version, UpstreamSeq: seq}
	}
	store := &fakeDetailStoreObj{versions: map[string][]core.VersionObj{
		key: {mk("v3.0.0", 3), mk("v2.0.0", 2), mk("v1.0.0", 1)},
	}}
	cases := []struct {
		target core.VersionObj
		newer  string
		older  string
	}{
		{mk("v3.0.0", 3), "", "v2.0.0"},
		{mk("v2.0.0", 2), "v3.0.0", "v1.0.0"},
		{mk("v1.0.0", 1), "v2.0.0", ""},
	}
	for _, tc := range cases {
		var viewModel view.VersionObj
		fillHistoryNav(context.Background(), store, tc.target, &viewModel)
		if viewModel.History.NewerVersion != tc.newer || viewModel.History.OlderVersion != tc.older {
			t.Errorf("%s: got newer=%q older=%q, want newer=%q older=%q",
				tc.target.Version, viewModel.History.NewerVersion, viewModel.History.OlderVersion, tc.newer, tc.older)
		}
	}
}

// Raw versions render normally as universal-only: bazel/zig snippets exist, go/composer are absent,
// raw evidence is visible, and history plus notes still work.
func TestVersionPageRendersRawVersion(t *testing.T) {
	key, version := "pkg/alpha", "INKSCAPE_1_3_2"
	vObj := core.VersionObj{
		Key:             key,
		Version:         version,
		IngestTS:        time.Now(),
		SourceSizeBytes: 500,
		TreeHash:        core.HashBytes([]byte("raw-tree")),
		ReleaseNotes:    "raw release body",
	}
	tarKeyObj := core.ArtifactKeyObj{
		MaterializerID: cUniversalMatzer,
		ArtifactKind:   cFormatTarGz,
		ListenerID:     cListenerGlobal,
		Key:            key,
		Version:        version,
	}
	tarObj := core.ArtifactObj{
		MaterializerID: cUniversalMatzer, ArtifactKind: cFormatTarGz, ListenerID: cListenerGlobal,
		Key: key, Version: version,
		BodyHash: core.HashBytes([]byte("raw-tar")), BodySha256: bytes.Repeat([]byte{0x5a}, 32), SizeBytes: 500,
	}
	store := &fakeDetailStoreObj{
		found:       true,
		versionOf:   map[string]core.VersionObj{key + "@" + version: vObj},
		versions:    map[string][]core.VersionObj{key: {vObj}},
		artifacts:   []core.ArtifactObj{tarObj},
		artifactMap: map[core.ArtifactKeyObj]core.ArtifactObj{tarKeyObj: tarObj},
		detection:   core.DetectionObj{EvidenceJSON: `{"raw_version":true}`},
		detectionOK: true,
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()

	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, version, cListenerGlobal, nil, "")
	if err != nil || !found {
		t.Fatalf("Version raw: err=%v found=%v", err, found)
	}
	body := string(bodyArr)

	if !strings.Contains(body, version) {
		t.Errorf("page must contain raw version name %q", version)
	}
	if !strings.Contains(body, "bazel") || !strings.Contains(body, "zig") {
		t.Error("raw version must keep bazel and zig snippets (universal sha256 present)")
	}
	if strings.Contains(body, "go get") || strings.Contains(body, "composer require") {
		t.Error("raw version must not render go/composer install snippets")
	}
	if !strings.Contains(body, "raw version: universal archives only") {
		t.Error("raw version page must carry the raw label")
	}
	if !strings.Contains(body, "raw release body") {
		t.Error("raw version release notes must render")
	}
}

// Host-sensitive archives: the version page shows only the current entry variant plus global artifacts.
func TestVersionPageFiltersOtherListenerArtifacts(t *testing.T) {
	key, version := "pkg/alpha", "v2.0.0"
	vObj := core.VersionObj{Key: key, Version: version, IngestTS: time.Now(), SourceSizeBytes: 200, TreeHash: core.HashBytes([]byte("tree"))}
	globalObj := core.ArtifactObj{Key: key, Version: version, ArtifactKind: "tar.gz", ListenerID: stcode.ListenerGlobal.String(), BodyHash: core.HashBytes([]byte("global-body"))}
	webObj := core.ArtifactObj{Key: key, Version: version, ArtifactKind: "zip", ListenerID: stcode.ListenerWeb.String(), BodyHash: core.HashBytes([]byte("web-body"))}
	yggObj := core.ArtifactObj{Key: key, Version: version, ArtifactKind: "zip", ListenerID: stcode.ListenerYgg.String(), BodyHash: core.HashBytes([]byte("ygg-body"))}
	store := &fakeDetailStoreObj{
		found:     true,
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		versions:  map[string][]core.VersionObj{key: {vObj}},
		artifacts: []core.ArtifactObj{globalObj, webObj, yggObj},
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()

	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, version, stcode.ListenerWeb.String(), nil, "")
	if err != nil || !found {
		t.Fatalf("Version: err=%v found=%v", err, found)
	}
	body := string(bodyArr)
	if !strings.Contains(body, globalObj.BodyHash.Hex()) || !strings.Contains(body, webObj.BodyHash.Hex()) {
		t.Error("version page must list global and current-listener artifacts")
	}
	if strings.Contains(body, yggObj.BodyHash.Hex()) {
		t.Error("version page must hide artifacts of the other listener")
	}
}

// // // // // // // // // //

// altSnippetFixture builds a version page with go+composer detection and per-entry universal artifacts.
// sha256 values differ like they do in real storage.
func altSnippetFixture(t *testing.T, includeAltArtifact bool, altPublishable bool) string {
	t.Helper()
	key, version := "pkg/alpha", "v2.0.0"
	vObj := core.VersionObj{Key: key, Version: version, IngestTS: time.Now(), SourceSizeBytes: 200, TreeHash: core.HashBytes([]byte("tree"))}
	webSha := bytes.Repeat([]byte{0xAA}, 32)
	yggSha := bytes.Repeat([]byte{0xBB}, 32)
	artifactMap := map[core.ArtifactKeyObj]core.ArtifactObj{
		{MaterializerID: cUniversalMatzer, ArtifactKind: cFormatTarGz, ListenerID: stcode.ListenerWeb.String(), Key: key, Version: version}: {BodySha256: webSha},
	}
	if includeAltArtifact {
		artifactMap[core.ArtifactKeyObj{MaterializerID: cUniversalMatzer, ArtifactKind: cFormatTarGz, ListenerID: stcode.ListenerYgg.String(), Key: key, Version: version}] = core.ArtifactObj{BodySha256: yggSha}
	}
	store := &fakeDetailStoreObj{
		found:       true,
		versionOf:   map[string]core.VersionObj{key + "@" + version: vObj},
		versions:    map[string][]core.VersionObj{key: {vObj}},
		artifactMap: artifactMap,
		detection:   core.DetectionObj{IsGo: true, IsComposer: true, EvidenceJSON: `{"go_module_path":"example.test/alpha","composer_name":"acme/alpha"}`},
		detectionOK: true,
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()
	ctxObj := testContext(st, lnk)
	ctxObj.Alternate = view.AlternateObj{Channel: "ygg", Scheme: "http", Host: "[200:1::1]", CopyHost: "node.pk.ygg"}

	ov := fakeOverlayObj{host: "vault.test", publishable: true}
	altOv := fakeOverlayObj{host: "node.pk.ygg", publishable: altPublishable}
	bodyArr, found, err := Version(context.Background(), st, store, ov, lnk, ctxObj, key, version, stcode.ListenerWeb.String(), altOv, stcode.ListenerYgg.String())
	if err != nil || !found {
		t.Fatalf("Version: err=%v found=%v", err, found)
	}
	return string(bodyArr)
}

// The module-path host in an alternate Go snippet must match that command's GOPROXY host.
func TestVersionAltGoSnippetHostsConsistent(t *testing.T) {
	body := altSnippetFixture(t, true, true)
	wantAlt := "GOPROXY=http://node.pk.ygg GOSUMDB=off GOINSECURE=node.pk.ygg/* go get node.pk.ygg/pkg/alpha@v2.0.0"
	if !strings.Contains(body, wantAlt) {
		t.Errorf("alt go snippet must target the alt host end-to-end, want %q", wantAlt)
	}
	// Mixed bug shape: alternate-entry GOPROXY with current-entry module path.
	if strings.Contains(body, "node.pk.ygg/* go get vault.test/") {
		t.Error("alt go snippet must not mix alt GOPROXY with current-listener module path")
	}
	if !strings.Contains(body, "GOPROXY=https://vault.test GOSUMDB=off go get vault.test/pkg/alpha@v2.0.0") {
		t.Error("primary go snippet must stay on the current listener host")
	}
}

// The alternate-entry Bazel snippet must carry that entry's artifact sha256.
func TestVersionAltBazelUsesAltArtifactSha(t *testing.T) {
	body := altSnippetFixture(t, true, true)
	webIntegrity := "sha256-" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xAA}, 32))
	yggIntegrity := "sha256-" + base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0xBB}, 32))
	if !strings.Contains(body, yggIntegrity) {
		t.Error("alt bazel snippet must embed the alt listener artifact sha256")
	}
	if strings.Count(body, webIntegrity) != 1 {
		t.Error("current-listener sha256 must appear only in the primary bazel snippet")
	}
	if !strings.Contains(body, "http://node.pk.ygg/pkg/alpha/v2.0.0.tar.gz") {
		t.Error("alt snippets must point at the alt host URL")
	}
}

func TestVersionAltDownloadUsesAltArtifactSha(t *testing.T) {
	key, version := "pkg/alpha", "v2.0.0"
	webSha := bytes.Repeat([]byte{0xAA}, 32)
	yggSha := bytes.Repeat([]byte{0xBB}, 32)
	vObj := core.VersionObj{Key: key, Version: version, IngestTS: time.Now(), SourceSizeBytes: 200, TreeHash: core.HashBytes([]byte("tree"))}
	store := &fakeDetailStoreObj{
		found:     true,
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		versions:  map[string][]core.VersionObj{key: {vObj}},
		artifacts: []core.ArtifactObj{
			{MaterializerID: cUniversalMatzer, ArtifactKind: "zip", ListenerID: stcode.ListenerWeb.String(), Key: key, Version: version, BodyHash: core.HashBytes([]byte("web-body")), BodySha256: webSha, SizeBytes: 100},
			{MaterializerID: cUniversalMatzer, ArtifactKind: "zip", ListenerID: stcode.ListenerYgg.String(), Key: key, Version: version, BodyHash: core.HashBytes([]byte("ygg-body")), BodySha256: yggSha, SizeBytes: 100},
		},
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()
	ctxObj := testContext(st, lnk)
	ctxObj.Alternate = view.AlternateObj{Channel: "ygg", Scheme: "http", Host: "[200:1::1]", CopyHost: "node.pk.ygg"}

	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, ctxObj, key, version, stcode.ListenerWeb.String(), nil, stcode.ListenerYgg.String())
	if err != nil || !found {
		t.Fatalf("Version: err=%v found=%v", err, found)
	}
	body := string(bodyArr)
	webHex := strings.Repeat("aa", 32)
	yggHex := strings.Repeat("bb", 32)
	if !strings.Contains(body, "download via yggdrasil mesh") {
		t.Error("version page must expose alternate download commands when alternate artifacts are available")
	}
	if !strings.Contains(body, "http://node.pk.ygg/pkg/alpha/v2.0.0.zip") || !strings.Contains(body, yggHex) {
		t.Error("alternate download command must use the alternate host and artifact sha256")
	}
	if strings.Contains(body, "http://node.pk.ygg/pkg/alpha/v2.0.0.zip\nprintf '%s  %s\\n' '"+webHex) {
		t.Error("alternate download command must not reuse the current-listener sha256")
	}
}

// Without artifact or publishability on the alternate entry, matching snippets disappear silently.
func TestVersionAltSnippetsSkippedWhenAltUnavailable(t *testing.T) {
	body := altSnippetFixture(t, false, false)
	if got := strings.Count(body, "http_archive"); got != 1 {
		t.Errorf("expected only the primary bazel snippet, got %d http_archive blocks", got)
	}
	if strings.Contains(body, "go get node.pk.ygg") {
		t.Error("alt go snippet must be absent when the alt listener is not publishable")
	}
	// Composer snippets include the mirror host, so the alternate entry shows its own variant.
	if got := strings.Count(body, "composer require acme/alpha:v2.0.0"); got != 2 {
		t.Errorf("composer snippet must appear once per entry, got %d", got)
	}
	if !strings.Contains(body, "composer config repositories.yggvault composer https://vault.test") {
		t.Error("primary composer snippet must register the mirror repository on the current host")
	}
	if !strings.Contains(body, "composer config secure-http false\ncomposer config repositories.yggvault composer http://node.pk.ygg") {
		t.Error("alt composer snippet must disable secure-http and target the alt host")
	}
}

// // // // // // // // // //

// A version page with blocked go-zip shows the honest reason in detection rows.
func TestVersionPageShowsGoZipBlockedReason(t *testing.T) {
	key, version := "pkg/alpha", "v2.0.0"
	vObj := core.VersionObj{Key: key, Version: version, IngestTS: time.Now(), SourceSizeBytes: 200, TreeHash: core.HashBytes([]byte("tree"))}
	store := &fakeDetailStoreObj{
		found:     true,
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		versions:  map[string][]core.VersionObj{key: {vObj}},
		detection: core.DetectionObj{
			IsGo:             true,
			EvidenceJSON:     `{"go_module_path":"example.test/alpha"}`,
			GoZipBlocked:     true,
			GoZipBlockReason: "invalid file paths (1): testdata-sample",
		},
		detectionOK: true,
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()

	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, version, cListenerGlobal, nil, "")
	if err != nil || !found {
		t.Fatalf("Version: err=%v found=%v", err, found)
	}
	body := string(bodyArr)
	if !strings.Contains(body, "go module zip unavailable: invalid file paths (1): testdata-sample") {
		t.Error("version page must surface the go-zip block reason")
	}
	if strings.Contains(body, "go get ") {
		t.Error("blocked version page must not offer a go install snippet")
	}
}

// A key page whose latest has blocked go-zip does not offer a go get snippet.
func TestKeyPageOmitsGoSnippetWhenZipBlocked(t *testing.T) {
	key := "pkg/alpha"
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	store := &fakeVersionStoreObj{
		versions: map[string][]core.VersionObj{
			key: {{Key: key, Version: "v2.0.0", IngestTS: time.Now(), SourceSizeBytes: 100, TreeHash: core.HashBytes([]byte("t"))}},
		},
		detection: core.DetectionObj{
			IsGo:             true,
			EvidenceJSON:     `{"go_module_path":"example.test/alpha"}`,
			GoZipBlocked:     true,
			GoZipBlockReason: "invalid file paths (1): testdata-sample",
		},
		detectionOK: true,
	}
	st := seedState()

	bodyArr, found, err := Key(context.Background(), st, store, lnk, testContext(st, lnk), key, PageCursorObj{}, 10)
	if err != nil || !found {
		t.Fatalf("Key: err=%v found=%v", err, found)
	}
	if strings.Contains(string(bodyArr), "go get ") {
		t.Error("key page must not offer go get for a blocked latest")
	}
}

// // // // // // // // // //

// End-to-end XSS guard: malicious markdown release notes must not survive into the HTML page.
func TestVersionPageStripsScriptFromNotes(t *testing.T) {
	key, version := "pkg/alpha", "v2.0.0"
	vObj := core.VersionObj{
		Key:             key,
		Version:         version,
		IngestTS:        time.Now(),
		SourceSizeBytes: 200,
		TreeHash:        core.HashBytes([]byte("tree")),
		ReleaseNotes:    "hi <script>alert(1)</script> [x](javascript:alert(2)) <img src=x onerror=alert(3)>",
	}
	store := &fakeDetailStoreObj{
		found:     true,
		versionOf: map[string]core.VersionObj{key + "@" + version: vObj},
		versions:  map[string][]core.VersionObj{key: {vObj}},
	}
	lnk := link.Obj{Scheme: "https", EntryHost: "vault.test"}
	st := seedState()

	bodyArr, found, err := Version(context.Background(), st, store, fakeOverlayObj{}, lnk, testContext(st, lnk), key, version, cListenerGlobal, nil, "")
	if err != nil || !found {
		t.Fatalf("Version: err=%v found=%v", err, found)
	}
	body := string(bodyArr)
	for _, needle := range []string{"<script>alert", "javascript:", "onerror"} {
		if strings.Contains(body, needle) {
			t.Errorf("version page must not contain %q", needle)
		}
	}
}
