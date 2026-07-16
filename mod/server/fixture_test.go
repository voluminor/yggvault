package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/cache"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/telemetry"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cArtifactBytes = "ZIPDATA!"

	cArtifactETag = `"deadbeef"`
)

var errMeshDisabled = errors.New("mesh disabled in test")

// // // // // // // // // //

func vkey(key string, version string) string { return key + "@" + version }

// // // // // // // // // //

type fakeStoreObj struct {
	versions    map[string][]core.VersionObj
	trees       map[core.HashObj][]core.TreeEntryObj
	blobs       map[core.HashObj][]byte
	detections  map[string]core.DetectionObj
	artifacts   map[string][]core.ArtifactObj
	feed        []core.FeedEventObj
	artifactRaw []byte
	tdir        string
	keysetCalls atomic.Int64
}

func (f *fakeStoreObj) ListVersionsKeyset(_ context.Context, key string, _ bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error) {
	f.keysetCalls.Add(1)
	arr := append([]core.VersionObj(nil), f.versions[key]...)
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
		return nil, nil
	}
	end := start + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[start:end], nil
}

func (f *fakeStoreObj) ListVersionsKeysetBefore(_ context.Context, key string, _ bool, beforeSeq int64, beforeVersion string, limit int) ([]core.VersionObj, error) {
	arr := f.versions[key]
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
	return out, nil
}

func (f *fakeStoreObj) ListVersionsPage(_ context.Context, key string, _ bool, limit int, offset int) ([]core.VersionObj, error) {
	arr := f.versions[key]
	if offset < 0 {
		offset = 0
	}
	if offset >= len(arr) {
		return nil, nil
	}
	end := offset + limit
	if end > len(arr) {
		end = len(arr)
	}
	return arr[offset:end], nil
}

func (f *fakeStoreObj) CountVersions(_ context.Context, key string) (uint64, error) {
	return uint64(len(f.versions[key])), nil
}

func (f *fakeStoreObj) GetVersion(_ context.Context, key string, version string) (core.VersionObj, bool, error) {
	for _, v := range f.versions[key] {
		if v.Version == version {
			return v, true, nil
		}
	}
	return core.VersionObj{}, false, nil
}

func (f *fakeStoreObj) LatestVersion(_ context.Context, key string) (core.VersionObj, bool, error) {
	if arr := f.versions[key]; len(arr) > 0 {
		return arr[0], true, nil
	}
	return core.VersionObj{}, false, nil
}

func (f *fakeStoreObj) GetArtifact(_ context.Context, keyObj core.ArtifactKeyObj) (core.ArtifactObj, bool, error) {
	for _, a := range f.artifacts[vkey(keyObj.Key, keyObj.Version)] {
		if a.MaterializerID == keyObj.MaterializerID && a.ArtifactKind == keyObj.ArtifactKind && a.ListenerID == keyObj.ListenerID {
			return a, true, nil
		}
	}
	return core.ArtifactObj{}, false, nil
}

func (f *fakeStoreObj) ListArtifacts(_ context.Context, key string, version string) ([]core.ArtifactObj, error) {
	return f.artifacts[vkey(key, version)], nil
}

func (f *fakeStoreObj) EnsureArtifactFile(_ context.Context, keyObj core.ArtifactKeyObj, _ ArtifactBuilderInterface) (*HotFileObj, error) {
	pathText := filepath.Join(f.tdir, keyObj.Key+"-"+keyObj.Version+"-"+keyObj.ArtifactKind)
	if err := os.WriteFile(pathText, f.artifactRaw, 0o600); err != nil {
		return nil, err
	}
	fileObj, err := os.Open(pathText)
	if err != nil {
		return nil, err
	}
	return &HotFileObj{Path: pathText, File: fileObj, SizeBytes: uint64(len(f.artifactRaw)), BodyHash: core.HashBytes(f.artifactRaw)}, nil
}

func (f *fakeStoreObj) GetDetection(_ context.Context, key string, version string) (core.DetectionObj, bool, error) {
	d, ok := f.detections[vkey(key, version)]
	return d, ok, nil
}

func (f *fakeStoreObj) RewriteSet(_ context.Context, _ string, _ string) ([]core.HashObj, error) {
	return nil, nil
}

func (f *fakeStoreObj) ReadTree(_ context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error) {
	return f.trees[treeHashObj], nil
}

func (f *fakeStoreObj) ReadBlob(_ context.Context, hashObj core.HashObj) ([]byte, error) {
	if b, ok := f.blobs[hashObj]; ok {
		return b, nil
	}
	return nil, os.ErrNotExist
}

func (f *fakeStoreObj) ListPublishFeed(_ context.Context, key string, limit int) ([]core.FeedEventObj, error) {
	outArr := make([]core.FeedEventObj, 0, len(f.feed))
	for _, e := range f.feed {
		if key != "" && e.Key != key {
			continue
		}
		outArr = append(outArr, e)
		if len(outArr) >= limit {
			break
		}
	}
	return outArr, nil
}

var _ DataStoreInterface = (*fakeStoreObj)(nil)

// // // // // // // // // //

type fakeStateObj struct {
	checks     state.ChecksumSetObj
	keyStates  map[string]state.KeyStateObj
	lastRescan time.Time
}

func (f *fakeStateObj) Health() state.HealthViewObj {
	return state.HealthViewObj{Status: stcode.OperationalStatusOk}
}

func (f *fakeStateObj) Snapshot() state.SnapshotObj {
	return state.SnapshotObj{LastRescan: f.lastRescan, Checksums: f.checks}
}

func (f *fakeStateObj) KeyState(key string) (state.KeyStateObj, bool) {
	ks, ok := f.keyStates[key]
	return ks, ok
}

func (f *fakeStateObj) KeyStates() []state.KeyStateObj {
	outArr := make([]state.KeyStateObj, 0, len(f.keyStates))
	for _, ks := range f.keyStates {
		outArr = append(outArr, ks)
	}
	return outArr
}

func (f *fakeStateObj) Checksums() state.ChecksumSetObj { return f.checks }

var _ DataStateInterface = (*fakeStateObj)(nil)

// // // // // // // // // //

type fakeComposerObj struct {
	names     []string
	keyByName map[string]string
}

func (f *fakeComposerObj) ComposerPackageNames() []string { return f.names }
func (f *fakeComposerObj) ComposerKeyForName(name string) (string, bool) {
	k, ok := f.keyByName[name]
	return k, ok
}

var _ ComposerNamesInterface = (*fakeComposerObj)(nil)

// // // // // // // // // //

type fakeMeshObj struct{}

func (fakeMeshObj) DialContext(_ context.Context, _ string, _ string) (net.Conn, error) {
	return nil, errMeshDisabled
}
func (fakeMeshObj) ListenerFor(_ mesh.TransportType) (net.Listener, error) {
	return nil, errMeshDisabled
}
func (fakeMeshObj) Host() string                             { return "" }
func (fakeMeshObj) Address() net.IP                          { return nil }
func (fakeMeshObj) OwnsHost(_ string) bool                   { return false }
func (fakeMeshObj) Enabled() bool                            { return false }
func (fakeMeshObj) PeerList() ([]mesh.PeerSnapshotObj, bool) { return nil, false }
func (fakeMeshObj) Close(_ context.Context) error            { return nil }

var _ mesh.NodeInterface = fakeMeshObj{}

// enabledFakeMeshObj models a running node with one connected peer for ygg metrics tests.
type enabledFakeMeshObj struct{ fakeMeshObj }

func (enabledFakeMeshObj) Enabled() bool { return true }
func (enabledFakeMeshObj) PeerList() ([]mesh.PeerSnapshotObj, bool) {
	return []mesh.PeerSnapshotObj{{
		URI:           "tls://peer.example:443",
		Up:            true,
		PublicKey:     "aabb",
		LatencyNanos:  1500000,
		Cost:          10,
		RXBytes:       2048,
		TXBytes:       1024,
		UptimeSeconds: 12.5,
	}}, true
}

var _ mesh.NodeInterface = enabledFakeMeshObj{}

// // // // // // // // // //

type testOptionsObj struct {
	publicMetrics      bool
	internalMetrics    bool
	yggPublicMetrics   bool
	rateLimitRPS       uint
	rateLimitBurst     uint
	staticDir          string
	cacheEnabled       bool
	versions           int
	nonGoNewest        bool
	v2Version          bool
	goZipBlockedNewest bool
	rawNewest          bool
}

type testOptionFunc func(*testOptionsObj)

func withPublicMetrics() testOptionFunc {
	return func(o *testOptionsObj) { o.publicMetrics = true }
}

func withYggPublicMetrics() testOptionFunc {
	return func(o *testOptionsObj) { o.yggPublicMetrics = true }
}

func withInternalMetrics() testOptionFunc {
	return func(o *testOptionsObj) { o.internalMetrics = true }
}

func withRateLimit(rps uint, burst uint) testOptionFunc {
	return func(o *testOptionsObj) { o.rateLimitRPS, o.rateLimitBurst = rps, burst }
}

func withStaticDir(dir string) testOptionFunc {
	return func(o *testOptionsObj) { o.staticDir = dir }
}

func withVersions(n int) testOptionFunc {
	return func(o *testOptionsObj) { o.versions = n }
}

func withCache() testOptionFunc {
	return func(o *testOptionsObj) { o.cacheEnabled = true }
}

func withNonGoNewestVersion() testOptionFunc {
	return func(o *testOptionsObj) { o.nonGoNewest = true }
}

func withV2Version() testOptionFunc {
	return func(o *testOptionsObj) { o.v2Version = true }
}

func withGoZipBlockedNewestVersion() testOptionFunc {
	return func(o *testOptionsObj) { o.goZipBlockedNewest = true }
}

func withRawNewestVersion() testOptionFunc {
	return func(o *testOptionsObj) { o.rawNewest = true }
}

// // // // // // // // // //

func newTestServer(t *testing.T, optArr ...testOptionFunc) (*Obj, listenerCtxObj) {
	t.Helper()

	optionsObj := testOptionsObj{}
	for _, optFunc := range optArr {
		optFunc(&optionsObj)
	}

	goModArr := []byte("module mirror.example/lib\n\ngo 1.21\n")
	goModHash := core.HashBytes(goModArr)
	entryArr := []core.TreeEntryObj{{Path: "go.mod", Mode: core.ModeFile, SizeBytes: uint64(len(goModArr)), BlobHash: goModHash}}
	treeHash := core.HashBytes([]byte("canonical-tree"))
	now := time.Now().UTC()

	storeObj := &fakeStoreObj{
		versions:   map[string][]core.VersionObj{"lib": {{Key: "lib", Version: "v1.0.0", TreeHash: treeHash, IngestTS: now, SourceSizeBytes: 100, ReleaseNotes: "release **notes**"}}},
		trees:      map[core.HashObj][]core.TreeEntryObj{treeHash: entryArr},
		blobs:      map[core.HashObj][]byte{goModHash: goModArr},
		detections: map[string]core.DetectionObj{vkey("lib", "v1.0.0"): {IsGo: true, EvidenceJSON: `{"go_module_path":"mirror.example/lib"}`}},
		artifacts: map[string][]core.ArtifactObj{vkey("lib", "v1.0.0"): {
			{MaterializerID: stcode.MaterializerUniversal.String(), ArtifactKind: "zip", ListenerID: stcode.ListenerGlobal.String(), Key: "lib", Version: "v1.0.0", BodyHash: core.HashBytes([]byte("z")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.UniversalZipFormatVersion, ETag: cArtifactETag, BodySha1: []byte{0x01, 0x02}},
			{MaterializerID: stcode.MaterializerGo.String(), ArtifactKind: "zip", ListenerID: stcode.ListenerWeb.String(), Key: "lib", Version: "v1.0.0", BodyHash: core.HashBytes([]byte("gz")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.GoZipFormatVersion, ETag: `"goz"`, BodySha1: []byte{0x03, 0x04}},
		}},
		feed:        []core.FeedEventObj{{Key: "lib", Version: "v1.0.0", EventTS: now, TreeHash: treeHash, ReleaseNotes: "release **notes**", FirstPublish: true}},
		artifactRaw: []byte(cArtifactBytes),
		tdir:        t.TempDir(),
	}
	stateObj := &fakeStateObj{
		checks:     state.ChecksumSetObj{Content: core.HashBytes([]byte("c"))},
		keyStates:  map[string]state.KeyStateObj{"lib": {Key: "lib", Status: stcode.OperationalStatusOk, Classified: true, Classification: stcode.SourceClassGit, SourceURL: "https://up/lib", Availability: stcode.AvailabilityStatusAvailable, LatestVersion: "v1.0.0", VersionCount: 1, LastPublishTS: now}},
		lastRescan: now,
	}

	if optionsObj.nonGoNewest {
		storeObj.versions["lib"] = append([]core.VersionObj{
			{Key: "lib", Version: "v1.1.0", TreeHash: treeHash, IngestTS: now.Add(time.Second), SourceSizeBytes: 100},
		}, storeObj.versions["lib"]...)
		keyStateObj := stateObj.keyStates["lib"]
		keyStateObj.VersionCount = 2
		stateObj.keyStates["lib"] = keyStateObj
	}

	if optionsObj.rawNewest {
		storeObj.versions["lib"] = append([]core.VersionObj{
			{Key: "lib", Version: "blockly-v9.3.3", TreeHash: treeHash, IngestTS: now.Add(3 * time.Second), SourceSizeBytes: 100},
		}, storeObj.versions["lib"]...)
		storeObj.detections[vkey("lib", "blockly-v9.3.3")] = core.DetectionObj{EvidenceJSON: `{"raw_version":true}`}
		storeObj.artifacts[vkey("lib", "blockly-v9.3.3")] = []core.ArtifactObj{
			{MaterializerID: stcode.MaterializerUniversal.String(), ArtifactKind: "zip", ListenerID: stcode.ListenerGlobal.String(), Key: "lib", Version: "blockly-v9.3.3", BodyHash: core.HashBytes([]byte("rz")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.UniversalZipFormatVersion, ETag: `"rz"`, BodySha256: []byte{0x11, 0x12}},
			{MaterializerID: stcode.MaterializerUniversal.String(), ArtifactKind: "tar.gz", ListenerID: stcode.ListenerGlobal.String(), Key: "lib", Version: "blockly-v9.3.3", BodyHash: core.HashBytes([]byte("rt")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.UniversalTarGzFormatVersion, ETag: `"rt"`, BodySha256: []byte{0x13, 0x14}},
		}
		keyStateObj := stateObj.keyStates["lib"]
		keyStateObj.VersionCount = 2
		keyStateObj.LatestVersion = "blockly-v9.3.3"
		stateObj.keyStates["lib"] = keyStateObj
	}

	if optionsObj.goZipBlockedNewest {
		storeObj.versions["lib"] = append([]core.VersionObj{
			{Key: "lib", Version: "v1.2.0", TreeHash: treeHash, IngestTS: now.Add(2 * time.Second), SourceSizeBytes: 100},
		}, storeObj.versions["lib"]...)
		storeObj.detections[vkey("lib", "v1.2.0")] = core.DetectionObj{
			IsGo:             true,
			EvidenceJSON:     `{"go_module_path":"mirror.example/lib"}`,
			GoZipBlocked:     true,
			GoZipBlockReason: `invalid file paths (1): "testdata/a?b.json"`,
		}
		storeObj.artifacts[vkey("lib", "v1.2.0")] = []core.ArtifactObj{
			{MaterializerID: stcode.MaterializerUniversal.String(), ArtifactKind: "zip", ListenerID: stcode.ListenerGlobal.String(), Key: "lib", Version: "v1.2.0", BodyHash: core.HashBytes([]byte("bz")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.UniversalZipFormatVersion, ETag: `"bz"`, BodySha1: []byte{0x07, 0x08}},
		}
		keyStateObj := stateObj.keyStates["lib"]
		keyStateObj.VersionCount = 2
		stateObj.keyStates["lib"] = keyStateObj
	}

	if optionsObj.v2Version {
		storeObj.versions["lib"] = append([]core.VersionObj{
			{Key: "lib", Version: "v2.44.0", TreeHash: treeHash, IngestTS: now.Add(time.Minute), SourceSizeBytes: 100},
		}, storeObj.versions["lib"]...)
		storeObj.detections[vkey("lib", "v2.44.0")] = core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_module_path":"mirror.example/lib"}`}
		storeObj.artifacts[vkey("lib", "v2.44.0")] = []core.ArtifactObj{
			{MaterializerID: stcode.MaterializerGo.String(), ArtifactKind: "zip", ListenerID: stcode.ListenerWeb.String(), Key: "lib", Version: "v2.44.0", BodyHash: core.HashBytes([]byte("gz2")), SizeBytes: uint64(len(cArtifactBytes)), FormatVersion: overlay.GoZipFormatVersion, ETag: `"goz2"`, BodySha1: []byte{0x05, 0x06}},
		}
		keyStateObj := stateObj.keyStates["lib"]
		keyStateObj.LatestVersion = "v2.44.0"
		keyStateObj.VersionCount = 2
		stateObj.keyStates["lib"] = keyStateObj
	}

	if optionsObj.versions > 1 {
		verArr := make([]core.VersionObj, optionsObj.versions)
		for i := range verArr {
			verArr[i] = core.VersionObj{Key: "lib", Version: fmt.Sprintf("v1.0.%d", i), TreeHash: treeHash, IngestTS: now.Add(-time.Duration(i) * time.Second), SourceSizeBytes: 100}
		}
		storeObj.versions["lib"] = verArr
		keyStateObj := stateObj.keyStates["lib"]
		keyStateObj.VersionCount = uint64(optionsObj.versions)
		stateObj.keyStates["lib"] = keyStateObj
	}

	cfgObj := stconf.FullConfig()
	cfgObj.Web.Server.Domain = "mirror.example"
	cfgObj.Web.Static.Dir = optionsObj.staticDir
	storageDir, evalErr := filepath.EvalSymlinks(t.TempDir())
	if evalErr != nil {
		t.Fatalf("EvalSymlinks returned error: %v", evalErr)
	}
	cfgObj.Storage.Dir = storageDir
	cfgObj.Metrics.Web.Public = optionsObj.publicMetrics
	cfgObj.Metrics.Web.Internal = optionsObj.internalMetrics
	cfgObj.Metrics.Ygg.Public = optionsObj.yggPublicMetrics
	cfgObj.RateLimit.Web.Http.RequestsPerSecond = optionsObj.rateLimitRPS
	cfgObj.RateLimit.Web.Http.Burst = optionsObj.rateLimitBurst

	overlayObj, err := overlay.New(cfgObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	telemetryObj, err := telemetry.New(cfgObj)
	if err != nil {
		t.Fatalf("telemetry.New: %v", err)
	}

	var cacheObj *cache.Obj
	if optionsObj.cacheEnabled {
		cacheObj = cache.New(cfgObj.Cache)
	}

	serverObj, err := New(DepsObj{
		Config:    cfgObj,
		Storage:   storeObj,
		State:     stateObj,
		Overlay:   overlayObj,
		Telemetry: telemetryObj,
		Composer:  &fakeComposerObj{names: []string{"vendor/pkg"}, keyByName: map[string]string{"vendor/pkg": "lib"}},
		Mesh:      fakeMeshObj{},
		Log:       zerolog.Nop(),
		Cache:     cacheObj,
	})
	if err != nil {
		t.Fatalf("server.New: %v", err)
	}

	return serverObj, serverObj.webListenerCtx(false)
}

func stateFor(t *testing.T, serverObj *Obj) *fakeStateObj {
	t.Helper()
	stateObj, ok := serverObj.funcImplObj.deps.State.(*fakeStateObj)
	if !ok {
		t.Fatalf("server state is not *fakeStateObj")
	}
	return stateObj
}

func storeFor(t *testing.T, serverObj *Obj) *fakeStoreObj {
	t.Helper()
	storeObj, ok := serverObj.funcImplObj.deps.Storage.(*fakeStoreObj)
	if !ok {
		t.Fatalf("server storage is not *fakeStoreObj")
	}
	return storeObj
}

// // // // // // // // // //

func doReq(t *testing.T, ts *httptest.Server, method string, pathText string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	reqObj, err := http.NewRequest(method, ts.URL+pathText, nil)
	if err != nil {
		t.Fatalf("new request %s %s: %v", method, pathText, err)
	}
	for k, v := range header {
		reqObj.Header.Set(k, v)
	}
	respObj, err := ts.Client().Do(reqObj)
	if err != nil {
		t.Fatalf("%s %s: %v", method, pathText, err)
	}
	bodyArr, _ := io.ReadAll(respObj.Body)
	_ = respObj.Body.Close()
	return respObj, bodyArr
}

func doGET(t *testing.T, ts *httptest.Server, pathText string, header map[string]string) (*http.Response, []byte) {
	t.Helper()
	return doReq(t, ts, http.MethodGet, pathText, header)
}
