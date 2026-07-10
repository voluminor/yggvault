package rescan

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"

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
	archiveByKey          map[string][]byte
	archiveByVersion      map[string][]byte
	discoverClass         stcode.SourceClassType
	discoverErr           error
	publicMirrorArr       []source.PublicMirrorVersionObj
	publicMirrorErr       error
	publicMirrorTruncated bool
	publicMirrorCalls     int
	publicMirrorRoots     []string
	brotherSession        source.BrotherSessionInterface
	brotherSessions       []source.BrotherSessionInterface
	dialCount             int
	releasesErr           error

	refsMap map[string]string
	refsErr error

	tagArr  []source.GitReleaseObj
	tagsErr error

	fetchMu    sync.Mutex
	fetchCalls map[string]int

	listMu        sync.Mutex
	releaseDepths []uint
	tagsCalls     int
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
	if f.discoverErr != nil {
		return source.DiscoveryResultObj{}, f.discoverErr
	}
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

func (f *fakeSourceObj) PublicMirrorVersions(_ context.Context, rootURL string, _ string, skip func(string) bool) (source.PublicMirrorListingObj, error) {
	f.listMu.Lock()
	f.publicMirrorCalls++
	f.publicMirrorRoots = append(f.publicMirrorRoots, rootURL)
	f.listMu.Unlock()
	if f.publicMirrorErr != nil {
		return source.PublicMirrorListingObj{}, f.publicMirrorErr
	}
	listingObj := source.PublicMirrorListingObj{Truncated: f.publicMirrorTruncated}
	for i := range f.publicMirrorArr {
		versionObj := f.publicMirrorArr[i]
		listingObj.Names = append(listingObj.Names, versionObj.Version)
		if skip != nil && skip(versionObj.Version) {
			continue
		}
		listingObj.Versions = append(listingObj.Versions, versionObj)
	}
	return listingObj, nil
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

func TestKeyUnavailableDiagnosticClearsAfterGitRecovery(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "ok"})
	fakeSrc := &fakeSourceObj{
		releasesErr:  errors.New("temporary upstream outage"),
		releaseArr:   []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/a.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	if !hasDiagnostic(stateObj, "upstream_unavailable") {
		t.Fatalf("expected upstream_unavailable diagnostic after listing failure, got %+v", stateObj.ActiveDiagnostics())
	}

	fakeSrc.releasesErr = nil
	obj.RunOnce(ctx)
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("GetVersion after recovery: ok=%v err=%v", ok, err)
	}
	if hasDiagnostic(stateObj, "upstream_unavailable") {
		t.Fatalf("upstream_unavailable diagnostic stayed active after recovery: %+v", stateObj.ActiveDiagnostics())
	}
}

func TestIngestGitTruncatesOversizeReleaseNotes(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{
		"core-lib-1.0.0/README.md": "hello world",
	})
	hugeNotes := strings.Repeat("я", cMaxReleaseNotesBytes)
	fakeSrc := &fakeSourceObj{
		releaseArr:   []source.GitReleaseObj{{Version: "v1.0.0", BodyMD: hugeNotes, ArchiveURL: "https://x/a.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if len(versionObj.ReleaseNotes) > cMaxReleaseNotesBytes {
		t.Fatalf("release notes bytes=%d, want <= %d", len(versionObj.ReleaseNotes), cMaxReleaseNotesBytes)
	}
	if !strings.Contains(versionObj.ReleaseNotes, "[notes truncated]") {
		t.Fatalf("release notes missing truncation marker")
	}
}

func TestClassifyDegradedDictionary(t *testing.T) {
	contentArr := []string{
		"archive_invalid",
		"tree_invalid",
		"tree_hash_mismatch",
		"public_mirror_hash_mismatch",
		"brother_index_hash_mismatch",
		"tree_decode_failed",
		"heal_failed",
		"go_symlink_in_module",
		"go_manifest_missing",
		"go_manifest_too_large",
		"rewritten_file_too_large",
		"invalid_artifact_ref",
		"invalid_go_version",
		"some_future_code",
	}
	for _, codeText := range contentArr {
		impactObj, reasonObj := classifyDegraded(codeText)
		if impactObj != stcode.OperationalStatusDegraded || reasonObj != stcode.LogReasonContentRejected {
			t.Fatalf("%s classified as %s/%s, want degraded/content_rejected", codeText, impactObj.String(), reasonObj.String())
		}
	}
	transientArr := []string{"fetch_failed", "source_hash_failed", "brother_version_failed", "brother_blobs_failed", "brother_blob_filter_failed"}
	for _, codeText := range transientArr {
		impactObj, reasonObj := classifyDegraded(codeText)
		if impactObj != stcode.OperationalStatusDegraded || reasonObj != stcode.LogReasonUpstreamUnavailable {
			t.Fatalf("%s classified as %s/%s, want degraded/upstream_unavailable", codeText, impactObj.String(), reasonObj.String())
		}
	}
	systemArr := []string{
		"spool_failed", "publish_failed", "detect_failed", "resurrect_failed",
		"artifact_register_failed", "materialization_error",
	}
	for _, codeText := range systemArr {
		impactObj, reasonObj := classifyDegraded(codeText)
		if impactObj != stcode.OperationalStatusError || reasonObj != stcode.LogReasonArtifactMaterializationFailed {
			t.Fatalf("%s classified as %s/%s, want error/artifact_materialization_failed", codeText, impactObj.String(), reasonObj.String())
		}
	}
}

func TestHealQuarantineCodeFallbackIsTransient(t *testing.T) {
	codeText, permanentFlag := healQuarantineCode(errors.New("temporary disk failure"))
	if codeText != cOverlayFallbackCode || permanentFlag {
		t.Fatalf("code=%s permanent=%v, want %s/permanent=false", codeText, permanentFlag, cOverlayFallbackCode)
	}
}

func TestVersionFailureMetricCarriesPhase(t *testing.T) {
	readerObj := sdkmetric.NewManualReader()
	providerObj := sdkmetric.NewMeterProvider(sdkmetric.WithReader(readerObj))
	obj := &Obj{}
	if err := obj.RegisterMetrics(providerObj.Meter("rescan")); err != nil {
		t.Fatalf("RegisterMetrics returned error: %v", err)
	}

	obj.metricsObj.recordVersionFailure("archive_invalid")

	var rmObj metricdata.ResourceMetrics
	if err := readerObj.Collect(context.Background(), &rmObj); err != nil {
		t.Fatalf("Collect returned error: %v", err)
	}
	foundMetric := false
	foundPoint := false
	for _, scopeObj := range rmObj.ScopeMetrics {
		for _, metricObj := range scopeObj.Metrics {
			if metricObj.Name != "rescan_version_failures_total" {
				continue
			}
			foundMetric = true
			sumObj, ok := metricObj.Data.(metricdata.Sum[int64])
			if !ok {
				t.Fatalf("rescan_version_failures_total data type=%T, want Sum[int64]", metricObj.Data)
			}
			for _, pointObj := range sumObj.DataPoints {
				attrObj, ok := pointObj.Attributes.Value(attribute.Key("phase"))
				if !ok || attrObj.AsString() != "archive_invalid" {
					continue
				}
				if pointObj.Value != 1 {
					t.Fatalf("archive_invalid point=%d want 1", pointObj.Value)
				}
				foundPoint = true
			}
		}
	}
	if !foundMetric {
		t.Fatal("rescan_version_failures_total metric not collected")
	}
	if !foundPoint {
		t.Fatal("rescan_version_failures_total missing phase=archive_invalid datapoint")
	}
}

// // // // // // // // // //

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
