package rescan

import (
	"archive/zip"
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

type fakeSourceObj struct {
	releaseArr            []source.GitReleaseObj
	archiveBytes          []byte
	archiveByKey          map[string][]byte // per-key archive override for multi-key tests
	archiveByVersion      map[string][]byte // per-version archive override for multi-version tests
	discoverClass         stcode.SourceClassType
	publicMirrorArr       []source.PublicMirrorVersionObj
	publicMirrorErr       error
	publicMirrorTruncated bool
	publicMirrorCalls     int
	publicMirrorRoots     []string
	brotherSession        source.BrotherSessionInterface
	brotherSessions       []source.BrotherSessionInterface // redial sessions per BrotherDial call, in order
	dialCount             int
	releasesErr           error // simulates unavailable first source

	refsMap map[string]string // tag-to-SHA for Refs; nil means empty advertisement
	refsErr error             // simulates refs advertisement failure

	tagArr  []source.GitReleaseObj // tag listing for Tags; nil means empty
	tagsErr error                  // simulates tag-list failure

	fetchMu    sync.Mutex
	fetchCalls map[string]int // FetchArchive counter by version

	listMu        sync.Mutex
	releaseDepths []uint // depth of each Releases call in order
	tagsCalls     int    // Tags call counter
}

func (f *fakeSourceObj) fetchCount(version string) int {
	f.fetchMu.Lock()
	defer f.fetchMu.Unlock()
	return f.fetchCalls[version]
}

func (f *fakeSourceObj) releasesDepthLog() []uint {
	f.listMu.Lock()
	defer f.listMu.Unlock()
	return append([]uint(nil), f.releaseDepths...)
}

func (f *fakeSourceObj) tagsCallCount() int {
	f.listMu.Lock()
	defer f.listMu.Unlock()
	return f.tagsCalls
}

func (f *fakeSourceObj) publicMirrorCallCount() int {
	f.listMu.Lock()
	defer f.listMu.Unlock()
	return f.publicMirrorCalls
}

func (f *fakeSourceObj) Discover(_ context.Context, key string, rootURL string) (source.DiscoveryResultObj, error) {
	classObj := f.discoverClass
	if classObj == stcode.UndefSourceClass {
		classObj = stcode.SourceClassGit
	}
	resultObj := source.DiscoveryResultObj{Class: classObj, RemoteKey: key, SourceURL: rootURL}
	if classObj == stcode.SourceClassBrother {
		resultObj.BrotherURL = rootURL
	}
	return resultObj, nil
}

func (f *fakeSourceObj) Releases(_ context.Context, _ string, depth uint) ([]source.GitReleaseObj, bool, error) {
	f.listMu.Lock()
	f.releaseDepths = append(f.releaseDepths, depth)
	f.listMu.Unlock()
	if f.releasesErr != nil {
		return nil, false, f.releasesErr
	}
	return f.releaseArr, false, nil
}

func (f *fakeSourceObj) Tags(_ context.Context, _ string, _ uint) ([]source.GitReleaseObj, bool, error) {
	f.listMu.Lock()
	f.tagsCalls++
	f.listMu.Unlock()
	if f.tagsErr != nil {
		return nil, false, f.tagsErr
	}
	return f.tagArr, false, nil
}

func (f *fakeSourceObj) Refs(_ context.Context, _ string) (map[string]string, error) {
	if f.refsErr != nil {
		return nil, f.refsErr
	}
	if f.refsMap == nil {
		return map[string]string{}, nil
	}
	return f.refsMap, nil
}

func (f *fakeSourceObj) FetchArchive(_ context.Context, reqObj source.GitFetchRequestObj) (source.GitFetchResultObj, error) {
	f.fetchMu.Lock()
	if f.fetchCalls == nil {
		f.fetchCalls = make(map[string]int)
	}
	f.fetchCalls[reqObj.Version]++
	f.fetchMu.Unlock()
	dataArr := f.archiveBytes
	if f.archiveByKey != nil {
		if perKey, ok := f.archiveByKey[reqObj.Key]; ok {
			dataArr = perKey
		}
	}
	if f.archiveByVersion != nil {
		if perVersion, ok := f.archiveByVersion[reqObj.Version]; ok {
			dataArr = perVersion
		}
	}
	pathText := filepath.Join(reqObj.DestDir, "source.archive")
	if err := os.WriteFile(pathText, dataArr, 0o600); err != nil {
		return source.GitFetchResultObj{}, err
	}
	return source.GitFetchResultObj{ArchivePath: pathText, Format: reqObj.Format, SizeBytes: uint64(len(dataArr))}, nil
}

func (f *fakeSourceObj) PublicMirrorVersions(_ context.Context, rootURL string, _ string) ([]source.PublicMirrorVersionObj, bool, error) {
	f.listMu.Lock()
	f.publicMirrorCalls++
	f.publicMirrorRoots = append(f.publicMirrorRoots, rootURL)
	f.listMu.Unlock()
	if f.publicMirrorErr != nil {
		return nil, false, f.publicMirrorErr
	}
	return f.publicMirrorArr, f.publicMirrorTruncated, nil
}

func (f *fakeSourceObj) BrotherDial(_ context.Context, _ string, _ string, _ string) (source.BrotherSessionInterface, error) {
	if len(f.brotherSessions) > 0 {
		idx := f.dialCount
		if idx >= len(f.brotherSessions) {
			idx = len(f.brotherSessions) - 1
		}
		f.dialCount++
		return f.brotherSessions[idx], nil
	}
	if f.brotherSession != nil {
		f.dialCount++
		return f.brotherSession, nil
	}
	return nil, fmt.Errorf("brother session not configured")
}

// // // // // // // // // //

func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var bufferObj bytes.Buffer
	zipWriter := zip.NewWriter(&bufferObj)
	for nameText, contentText := range files {
		entryWriter, err := zipWriter.Create(nameText)
		if err != nil {
			t.Fatalf("zip create %q: %v", nameText, err)
		}
		if _, err := entryWriter.Write([]byte(contentText)); err != nil {
			t.Fatalf("zip write %q: %v", nameText, err)
		}
	}
	if err := zipWriter.Close(); err != nil {
		t.Fatalf("zip close: %v", err)
	}
	return bufferObj.Bytes()
}

func rescanTestConfig(t *testing.T) *stconf.ConfigObj {
	t.Helper()
	configObj := stconf.FullConfig()
	// macOS: t.TempDir lives under the /var symlink — resolve it, otherwise New rejects the symlink component
	baseDir, evalErr := filepath.EvalSymlinks(t.TempDir())
	if evalErr != nil {
		t.Fatalf("EvalSymlinks returned error: %v", evalErr)
	}
	configObj.Storage.Dir = filepath.Join(baseDir, "cache")
	configObj.Storage.ArchiveLimits.Size.PerFile = stconf.SizeObj(10 * 1000 * 1000)
	configObj.Storage.ArchiveLimits.Entries.PathBytes = 512
	configObj.Storage.Hot.MaxSize = stconf.SizeObj(10 * 1000 * 1000)
	configObj.Storage.Hot.IdleTtl = time.Hour
	configObj.Storage.Quota.RetainLatestPerKey = 1
	configObj.HistoryPolicy.Prefix = "hr"
	configObj.HistoryPolicy.Mutation = stconf.HistoryMutationModeHistory
	configObj.HistoryPolicy.Deletion.Mode = stconf.HistoryDeletionModeKeep
	configObj.Web.Server.Domain = "mirror.example"
	configObj.Overlay.Go.RewriteEnabled = false
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://upstream.example/core-lib/"}
	return configObj
}

func archiveFromConfig(t *testing.T, configObj *stconf.ConfigObj) *archive.Obj {
	t.Helper()
	archiveObj, err := archive.New(archive.LimitsObj{
		MaxArchiveSize:         uint64(configObj.Storage.ArchiveLimits.Size.Compressed),
		MaxArchiveUnpackedSize: uint64(configObj.Storage.ArchiveLimits.Size.Unpacked),
		MaxArchiveFileBytes:    uint64(configObj.Storage.ArchiveLimits.Size.PerFile),
		MaxArchiveFiles:        configObj.Storage.ArchiveLimits.Entries.Count,
		MaxArchivePathBytes:    configObj.Storage.ArchiveLimits.Entries.PathBytes,
	})
	if err != nil {
		t.Fatalf("archive.New: %v", err)
	}
	return archiveObj
}

// // // // // // // // // //

func TestIngestGitPublishesVersionAndArtifacts(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storageObj.Close(closeCtx)
	})
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	archiveBytes := buildZip(t, map[string]string{
		"core-lib-1.0.0/README.md": "hello world",
		"core-lib-1.0.0/data.txt":  "payload",
	})
	fakeSrc := &fakeSourceObj{
		releaseArr:   []source.GitReleaseObj{{Version: "v1.0.0", BodyMD: "release notes", ArchiveURL: "https://x/a.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.TreeHash.IsZero() {
		t.Fatal("published version has empty tree hash")
	}
	if versionObj.ReleaseNotes != "release notes" {
		t.Fatalf("release notes=%q", versionObj.ReleaseNotes)
	}

	for _, kindText := range []string{"zip", "tar.gz"} {
		artObj, ok, err := storageObj.GetArtifact(ctx, core.ArtifactKeyObj{
			MaterializerID: "universal",
			ArtifactKind:   kindText,
			ListenerID:     stcode.ListenerGlobal.String(),
			Key:            "core-lib",
			Version:        "v1.0.0",
		})
		if err != nil || !ok {
			t.Fatalf("GetArtifact universal/%s: ok=%v err=%v", kindText, ok, err)
		}
		if artObj.BodyHash.IsZero() || artObj.SizeBytes == 0 {
			t.Fatalf("artifact universal/%s not hashed: %+v", kindText, artObj)
		}
	}

	obj.RunOnce(ctx)
	versionArr, err := storageObj.ListVersions(ctx, "core-lib", false)
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	if len(versionArr) != 1 {
		t.Fatalf("versions=%d want 1 (idempotent rescan)", len(versionArr))
	}
}

// // // // // // // // // //

// blockedGoStand builds a fixture with a Go version whose tree cannot become a valid Go module zip.
func blockedGoStand(t *testing.T) (*Obj, *storage.Obj, *fakeSourceObj, context.Context) {
	t.Helper()
	configObj := rescanTestConfig(t)
	configObj.Overlay.Go.RewriteEnabled = true
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = storageObj.Close(closeCtx)
	})
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	// Blocked v1.0.0 is not rank 0; latest is redownloaded every cycle by deep verification schedule.
	fakeSrc := &fakeSourceObj{
		releaseArr: []source.GitReleaseObj{
			{Version: "v1.1.0", ArchiveURL: "https://x/b.zip", Format: "zip"},
			{Version: "v1.0.0", ArchiveURL: "https://x/a.zip", Format: "zip"},
		},
		archiveByVersion: map[string][]byte{
			"v1.1.0": buildZip(t, map[string]string{
				"core-lib-1.1.0/go.mod": "module example.com/core-lib\n\ngo 1.22\n",
				"core-lib-1.1.0/a.go":   "package corelib\n",
			}),
			"v1.0.0": buildZip(t, blockedGoFiles()),
		},
	}
	return New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveFromConfig(t, configObj), ""), storageObj, fakeSrc, ctx
}

// blockedGoFiles is a Go module containing a file name rejected by x/mod/zip.
func blockedGoFiles() map[string]string {
	return map[string]string{
		"core-lib-1.0.0/go.mod":            "module example.com/core-lib\n\ngo 1.22\n",
		"core-lib-1.0.0/a.go":              "package corelib\n",
		"core-lib-1.0.0/testdata/a?b.json": "{}",
	}
}

func assertBlockedComplete(t *testing.T, ctx context.Context, storageObj *storage.Obj) {
	t.Helper()
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.HealPending {
		t.Fatal("blocked go-zip must not leave HealPending: version is complete without go artifacts")
	}
	detectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetDetection: ok=%v err=%v", ok, err)
	}
	if !detectionObj.GoZipBlocked || detectionObj.GoZipBlockReason == "" {
		t.Fatalf("detection must persist the block: %+v", detectionObj)
	}
	artifactArr, err := storageObj.ListArtifacts(ctx, "core-lib", "v1.0.0")
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	universalCount := 0
	for i := range artifactArr {
		if artifactArr[i].MaterializerID == stcode.MaterializerGo.String() {
			t.Fatalf("go artifact must not exist for a blocked version: %+v", artifactArr[i])
		}
		if artifactArr[i].MaterializerID == stcode.MaterializerUniversal.String() {
			universalCount++
		}
	}
	if universalCount == 0 {
		t.Fatal("universal artifacts must stay for a blocked version")
	}

	// Contrast: the clean neighboring version must keep its Go artifact.
	cleanArr, err := storageObj.ListArtifacts(ctx, "core-lib", "v1.1.0")
	if err != nil {
		t.Fatalf("ListArtifacts clean: %v", err)
	}
	goCount := 0
	for i := range cleanArr {
		if cleanArr[i].MaterializerID == stcode.MaterializerGo.String() {
			goCount++
		}
	}
	if goCount == 0 {
		t.Fatalf("clean version must keep its go artifact: %+v", cleanArr)
	}
}

// A version with an unusable go-zip tree publishes once: complete, no HealPending, no Go artifacts.
// The second cycle does not redownload the archive, proving the section 6 skip rule.
func TestIngestGitGoZipBlockedPublishesOnceComplete(t *testing.T) {
	obj, storageObj, fakeSrc, ctx := blockedGoStand(t)

	obj.RunOnce(ctx)
	assertBlockedComplete(t, ctx, storageObj)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("fetches after first cycle=%d want 1", got)
	}

	obj.RunOnce(ctx)
	assertBlockedComplete(t, ctx, storageObj)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("fetches after second cycle=%d want 1 (no re-download loop)", got)
	}
}

// Stuck HealPending on a blocked version clears without redownloading when the no-Go plan is already covered.
func TestIngestGitGoZipBlockedHealClearsWithoutFetch(t *testing.T) {
	obj, storageObj, fakeSrc, ctx := blockedGoStand(t)

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("fetches after publish=%d want 1", got)
	}
	if err := storageObj.SetHealPending(ctx, "core-lib", "v1.0.0", true); err != nil {
		t.Fatalf("SetHealPending: %v", err)
	}

	obj.RunOnce(ctx)
	assertBlockedComplete(t, ctx, storageObj)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("heal must not fetch the archive: fetches=%d want 1", got)
	}
}

// Legacy row: the version was published before the block flag existed, with detection missing it and HealPending=true.
// One cycle redownloads, heal updates detection and clears HealPending, then the version stops downloading.
func TestIngestGitGoZipBlockedLegacyHealRefetchOnce(t *testing.T) {
	obj, storageObj, fakeSrc, ctx := blockedGoStand(t)

	entryArr := make([]core.InputEntryObj, 0, 3)
	for nameText, contentText := range blockedGoFiles() {
		relText := nameText[len("core-lib-1.0.0/"):]
		entryArr = append(entryArr, core.InputEntryObj{Path: relText, Mode: core.ModeFile, Content: []byte(contentText)})
	}
	_, err := storageObj.Publish(ctx, core.PublishObj{
		Key:             "core-lib",
		Version:         "v1.0.0",
		SourceHash:      core.HashBytes([]byte("legacy-src")),
		SourceSizeBytes: 10,
		Entries:         entryArr,
		Detection:       core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_module_path":"example.com/core-lib"}`},
		EventType:       "publish",
		HealPending:     true,
	})
	if err != nil {
		t.Fatalf("Publish legacy: %v", err)
	}

	obj.RunOnce(ctx)
	assertBlockedComplete(t, ctx, storageObj)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("legacy heal must refetch exactly once: fetches=%d", got)
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("healed legacy version must not be re-downloaded: fetches=%d", got)
	}
}
