package rescan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"testing"

	lightweigit "github.com/voluminor/lightweigit-loader"

	vaultarchive "github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/mod/storage/treecodec"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// fakeBrotherFileObj is one version file for fake-session multi-file mode.
type fakeBrotherFileObj struct {
	path    string
	content []byte
}

type fakeBrotherSessionObj struct {
	notesByVersion  map[string]string
	blobByVersion   map[string][]byte
	filesByVersion  map[string][]fakeBrotherFileObj
	indexEntries    []source.BrotherIndexEntryObj
	srcInfo         source.BrotherSourceInfoObj
	dieOnVersion    string
	dead            bool
	fetchBytes      uint64
	fetchCount      uint
	fetchBatchSizes []int
}

func (s *fakeBrotherSessionObj) Hello(_ context.Context) (source.HelloResultObj, error) {
	bytesObj, countObj := s.FetchLimits()
	return source.HelloResultObj{Protocol: brotherwire.Protocol, MaxFetchResponseBytes: bytesObj, MaxFetchBatchCount: countObj}, nil
}

func (s *fakeBrotherSessionObj) Healthy() bool { return !s.dead }

func (s *fakeBrotherSessionObj) FetchLimits() (uint64, uint) {
	bytesObj := s.fetchBytes
	if bytesObj == 0 {
		bytesObj = brotherwire.DefaultMaxFetchResponseBytes
	}
	countObj := s.fetchCount
	if countObj == 0 {
		countObj = brotherwire.DefaultMaxFetchBatchCount
	}
	return bytesObj, countObj
}

func (s *fakeBrotherSessionObj) Index(_ context.Context, page uint32) ([]source.BrotherIndexEntryObj, uint32, source.BrotherSourceInfoObj, error) {
	if page != 1 {
		return nil, 0, source.BrotherSourceInfoObj{}, nil
	}
	if s.indexEntries != nil {
		return s.indexEntries, 0, s.srcInfo, nil
	}
	entryArr := make([]source.BrotherIndexEntryObj, 0, len(s.notesByVersion))
	for versionText, notesText := range s.notesByVersion {
		entryArr = append(entryArr, source.BrotherIndexEntryObj{Version: versionText, ReleaseNotes: notesText})
	}
	return entryArr, 0, s.srcInfo, nil
}

func (s *fakeBrotherSessionObj) Version(_ context.Context, version string) (source.BrotherVersionObj, error) {
	if version == s.dieOnVersion {
		s.dead = true
		return source.BrotherVersionObj{}, errors.New("brother transport reset")
	}
	if s.filesByVersion != nil {
		fileArr := append([]fakeBrotherFileObj(nil), s.filesByVersion[version]...)
		sort.Slice(fileArr, func(i, j int) bool { return fileArr[i].path < fileArr[j].path })
		entryArr := make([]core.TreeEntryObj, 0, len(fileArr))
		for _, fileObj := range fileArr {
			entryArr = append(entryArr, core.TreeEntryObj{
				Path:      fileObj.path,
				Mode:      core.ModeFile,
				BlobHash:  core.HashBytes(fileObj.content),
				SizeBytes: uint64(len(fileObj.content)),
			})
		}
		treeBytes, _, err := treecodec.Encode(entryArr)
		if err != nil {
			return source.BrotherVersionObj{}, err
		}
		return source.BrotherVersionObj{TreeBytes: treeBytes}, nil
	}
	contentArr := s.blobByVersion[version]
	hashObj := core.HashBytes(contentArr)
	entryArr := []core.TreeEntryObj{
		{Path: "composer.json", Mode: core.ModeFile, BlobHash: hashObj, SizeBytes: uint64(len(contentArr))},
	}
	treeBytes, _, err := treecodec.Encode(entryArr)
	if err != nil {
		return source.BrotherVersionObj{}, err
	}
	return source.BrotherVersionObj{TreeBytes: treeBytes}, nil
}

func (s *fakeBrotherSessionObj) BlobsFetch(_ context.Context, version string, reqArr []source.BlobReqObj, destDir string) (source.BrotherFetchResultObj, error) {
	s.fetchBatchSizes = append(s.fetchBatchSizes, len(reqArr))
	if s.filesByVersion != nil {
		// Multi-file mode returns strictly requested blobs so the fake does not hide deduplication.
		contentByHash := make(map[core.HashObj][]byte)
		for _, fileObj := range s.filesByVersion[version] {
			contentByHash[core.HashBytes(fileObj.content)] = fileObj.content
		}
		outArr := make([]core.StagedBlobObj, 0, len(reqArr))
		for _, reqObj := range reqArr {
			contentArr, ok := contentByHash[reqObj.Hash]
			if !ok {
				return source.BrotherFetchResultObj{}, fmt.Errorf("requested blob unknown to fake version %s: %s", version, reqObj.Hash.Hex())
			}
			pathText := filepath.Join(destDir, reqObj.Hash.Hex()+".blob")
			if err := os.WriteFile(pathText, contentArr, 0o600); err != nil {
				return source.BrotherFetchResultObj{}, err
			}
			outArr = append(outArr, core.StagedBlobObj{BlobHash: reqObj.Hash, SizeBytes: uint64(len(contentArr)), FilePath: pathText})
		}
		return source.BrotherFetchResultObj{Blobs: outArr}, nil
	}
	contentArr := s.blobByVersion[version]
	hashObj := core.HashBytes(contentArr)
	pathText := filepath.Join(destDir, hashObj.Hex()+".blob")
	if err := os.WriteFile(pathText, contentArr, 0o600); err != nil {
		return source.BrotherFetchResultObj{}, err
	}
	return source.BrotherFetchResultObj{Blobs: []core.StagedBlobObj{
		{BlobHash: hashObj, SizeBytes: uint64(len(contentArr)), FilePath: pathText},
	}}, nil
}

func (s *fakeBrotherSessionObj) Close() error { return nil }

// // // // // // // // // //

func TestIngestBrotherPublishesViaBrother(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	sessionObj := &fakeBrotherSessionObj{
		notesByVersion: map[string]string{"v1.0.0": "brother notes"},
		blobByVersion:  map[string][]byte{"v1.0.0": []byte(`{"name":"vendor/pkg"}`)},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.ReleaseNotes != "brother notes" || versionObj.TreeHash.IsZero() {
		t.Fatalf("unexpected version: %+v", versionObj)
	}

	detectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok || !detectionObj.IsComposer {
		t.Fatalf("expected composer detection via brother: ok=%v det=%+v err=%v", ok, detectionObj, err)
	}

	namesArr := obj.ComposerPackageNames()
	if len(namesArr) != 1 || namesArr[0] != "vendor/pkg" {
		t.Fatalf("composer names=%v want [vendor/pkg]", namesArr)
	}
}

func TestKeyUnavailableDiagnosticClearsAfterBrotherRecovery(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	sessionObj := &fakeBrotherSessionObj{
		notesByVersion: map[string]string{"v1.0.0": "brother notes"},
		blobByVersion:  map[string][]byte{"v1.0.0": []byte(`{"name":"vendor/pkg"}`)},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		discoverErr:    errors.New("temporary brother discovery outage"),
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)
	if !hasDiagnostic(stateObj, "upstream_unavailable") {
		t.Fatalf("expected upstream_unavailable diagnostic after discovery failure, got %+v", stateObj.ActiveDiagnostics())
	}

	fakeSrc.discoverErr = nil
	obj.RunOnce(ctx)
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("GetVersion after recovery: ok=%v err=%v", ok, err)
	}
	if hasDiagnostic(stateObj, "upstream_unavailable") {
		t.Fatalf("upstream_unavailable diagnostic stayed active after recovery: %+v", stateObj.ActiveDiagnostics())
	}
}

func TestIngestBrotherRedialsOnTransportDeath(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	blob1 := []byte(`{"name":"vendor/one"}`)
	blob2 := []byte(`{"name":"vendor/two"}`)
	sessionA := &fakeBrotherSessionObj{
		indexEntries: []source.BrotherIndexEntryObj{
			{Version: "v1.0.0", ReleaseNotes: "n1"},
			{Version: "v2.0.0", ReleaseNotes: "n2"},
		},
		blobByVersion: map[string][]byte{"v1.0.0": blob1, "v2.0.0": blob2},
		dieOnVersion:  "v1.0.0",
	}
	sessionB := &fakeBrotherSessionObj{
		blobByVersion: map[string][]byte{"v2.0.0": blob2},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:   stcode.SourceClassBrother,
		brotherSessions: []source.BrotherSessionInterface{sessionA, sessionB},
		releasesErr:     errors.New("first-source unreachable"),
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)

	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v2.0.0"); !ok {
		t.Fatal("v2.0.0 must be published via the redialed session")
	}
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); ok {
		t.Fatal("v1.0.0 hit the dead session and must NOT be published this cycle")
	}
	if fakeSrc.dialCount != 2 {
		t.Fatalf("expected exactly one redial (dialCount=2), got %d", fakeSrc.dialCount)
	}
}

func publicArchiveTreeHash(t *testing.T, ctx context.Context, storageObj *storage.Obj, archiveObj *vaultarchive.Obj, key string, version string, archiveBytes []byte) core.HashObj {
	t.Helper()
	spoolObj, err := storageObj.NewBlobSpool(ctx)
	if err != nil {
		t.Fatalf("NewBlobSpool: %v", err)
	}
	defer func() { _ = spoolObj.Close(ctx) }()

	sourcePath := filepath.Join(spoolObj.RootPath(), "source.zip")
	if err := os.WriteFile(sourcePath, archiveBytes, 0o600); err != nil {
		t.Fatalf("write source archive: %v", err)
	}
	extractObj, err := archiveObj.Extract(ctx, vaultarchive.RequestObj{
		Key:             key,
		Version:         version,
		Format:          vaultarchive.FormatZip,
		SourcePath:      sourcePath,
		SourceSizeBytes: uint64(len(archiveBytes)),
		SpoolPath:       spoolObj.RootPath(),
	})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	_, treeHashObj, err := storageObj.CanonicalTree(extractObj.Entries, key, version)
	if err != nil {
		t.Fatalf("CanonicalTree: %v", err)
	}
	return treeHashObj
}

func TestBrotherFallsBackToPublicAPIWhenRPCMissing(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "public mirror"})
	treeHashObj := publicArchiveTreeHash(t, ctx, storageObj, archiveObj, "core-lib", "v1.0.0", archiveBytes)
	fakeSrc := &fakeSourceObj{
		discoverClass: stcode.SourceClassBrother,
		publicMirrorArr: []source.PublicMirrorVersionObj{{
			Version:      "v1.0.0",
			ReleaseNotes: "public notes",
			TreeHash:     treeHashObj,
			ArchiveURL:   "https://brother.example/core-lib/v1.0.0.zip",
			Format:       "zip",
		}},
		archiveBytes: archiveBytes,
		releasesErr:  errors.New("first-source unreachable"),
	}

	New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "").RunOnce(ctx)

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("public fallback version must publish: ok=%v err=%v", ok, err)
	}
	if versionObj.TreeHash != treeHashObj || versionObj.ReleaseNotes != "public notes" {
		t.Fatalf("unexpected public fallback version: %+v", versionObj)
	}
	if got := fakeSrc.publicMirrorCallCount(); got != 1 {
		t.Fatalf("PublicMirrorVersions calls=%d want 1", got)
	}
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("FetchArchive calls=%d want 1", got)
	}
}

func TestBrotherPublicFallbackRejectsHashMismatch(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "public mirror"})
	fakeSrc := &fakeSourceObj{
		discoverClass: stcode.SourceClassBrother,
		publicMirrorArr: []source.PublicMirrorVersionObj{{
			Version:    "v1.0.0",
			TreeHash:   core.HashBytes([]byte("wrong public tree")),
			ArchiveURL: "https://brother.example/core-lib/v1.0.0.zip",
			Format:     "zip",
		}},
		archiveBytes: archiveBytes,
		releasesErr:  errors.New("first-source unreachable"),
	}
	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")

	obj.RunOnce(ctx)
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); ok {
		t.Fatal("public fallback with mismatched tree hash must not publish")
	}
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("fetches after first mismatch=%d want 1", got)
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("same mismatched public tree hash must not redownload: fetches=%d want 1", got)
	}
}

// // // // // // // // // //

// Live-lock reproduction: version B shares a blob with stored A, so deduplication does not stage it.
// Without storage fallback in spoolSourceObj, B would degrade with detect_failed forever.
func TestIngestBrotherSharedBlobPublishes(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	configObj.Storage.Quota.RetainLatestPerKey = 2
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	// The shared blob is composer.json, which Detect reads and would fail without fallback.
	sharedComposerArr := []byte(`{"name":"vendor/pkg"}`)
	sessionObj := &fakeBrotherSessionObj{
		indexEntries: []source.BrotherIndexEntryObj{
			{Version: "v1.0.0", ReleaseNotes: "n1"},
			{Version: "v2.0.0", ReleaseNotes: "n2"},
		},
		filesByVersion: map[string][]fakeBrotherFileObj{
			"v1.0.0": {
				{path: "composer.json", content: sharedComposerArr},
				{path: "src/main.php", content: []byte("<?php // release one")},
			},
			"v2.0.0": {
				{path: "composer.json", content: sharedComposerArr},
				{path: "src/main.php", content: []byte("<?php // release two")},
			},
		},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "")
	obj.RunOnce(ctx)

	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("v1.0.0 must be published: ok=%v err=%v", ok, err)
	}
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v2.0.0")
	if err != nil || !ok {
		t.Fatalf("v2.0.0 shares a blob with v1.0.0 and must still publish: ok=%v err=%v", ok, err)
	}
	if versionObj.TreeHash.IsZero() {
		t.Fatalf("v2.0.0 published with empty tree hash: %+v", versionObj)
	}
	if versionObj.HealPending {
		t.Fatalf("v2.0.0 must materialize all artifacts via the storage fallback: %+v", versionObj)
	}
	detectionObj, ok, err := storageObj.GetDetection(ctx, "core-lib", "v2.0.0")
	if err != nil || !ok || !detectionObj.IsComposer {
		t.Fatalf("v2.0.0 composer detection: ok=%v det=%+v err=%v", ok, detectionObj, err)
	}
}

// // // // // // // // // //

func TestBrotherFetchBlobsUsesSessionLimits(t *testing.T) {
	firstArr := []byte("first")
	secondArr := []byte("second")
	sessionObj := &fakeBrotherSessionObj{
		fetchCount: 1,
		filesByVersion: map[string][]fakeBrotherFileObj{
			"v1.0.0": {
				{path: "a.txt", content: firstArr},
				{path: "b.txt", content: secondArr},
			},
		},
	}
	reqArr := []source.BlobReqObj{
		{Hash: core.HashBytes(firstArr), SizeBytes: uint64(len(firstArr))},
		{Hash: core.HashBytes(secondArr), SizeBytes: uint64(len(secondArr))},
	}
	blobArr, err := (&Obj{}).brotherFetchBlobs(context.Background(), sessionObj, "v1.0.0", reqArr, t.TempDir())
	if err != nil {
		t.Fatalf("brotherFetchBlobs returned error: %v", err)
	}
	if len(blobArr) != 2 {
		t.Fatalf("blob count=%d want 2", len(blobArr))
	}
	if gotArr := sessionObj.fetchBatchSizes; len(gotArr) != 2 || gotArr[0] != 1 || gotArr[1] != 1 {
		t.Fatalf("batch sizes=%v want [1 1]", gotArr)
	}
}

// // // // // // // // // //

// first_source fallback must also work for tag-only origins: Gitea/Forgejo releases 404 proves
// reachability rather than outage.
func TestBrotherFirstSourceReachableOnTagOnlyOrigin(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	configObj.Brother.Prefer = stconf.BrotherPreferFirstSource
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}

	sessionObj := &fakeBrotherSessionObj{
		indexEntries: []source.BrotherIndexEntryObj{{Version: "v0.0.9"}},
		srcInfo:      source.BrotherSourceInfoObj{SourceURL: "https://git.example/o/r"},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    fmt.Errorf("list releases: %w", lightweigit.ErrNotFound),
		tagArr:         []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes:   buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"}),
	}

	obj := New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveFromConfig(t, configObj), "")
	obj.RunOnce(ctx)

	// The first source is reachable: the version came from git tags, not from brother.
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("tag version from first source must publish: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v0.0.9"); ok {
		t.Fatal("brother index version must not be ingested when first source wins")
	}
	// Brother-key ephemeral mode is not persisted.
	if ksObj, ok, _ := storageObj.GetKeySource(ctx, "core-lib"); ok && ksObj.ListingMode != "" {
		t.Fatalf("brother key must not persist listing mode, got %q", ksObj.ListingMode)
	}
}

// // // // // // // // // //

// TestBrotherPermanentFailureMemory: deterministic failure by advertised tree hash is remembered
// and skipped for the same hash; a changed tree clears the skip, and zero tree hash is not keyed.
func TestBrotherPermanentFailureMemory(t *testing.T) {
	obj := &Obj{permFailMap: make(map[missKeyObj]string)}
	entryObj := source.BrotherIndexEntryObj{Version: "v1.0.0", TreeHash: core.HashBytes([]byte("bad-tree"))}

	obj.recordBrotherPermanentFailure(context.Background(), "k", entryObj, "tree_invalid", "bad tree")
	if !obj.permanentFailureSkip("k", "v1.0.0", entryObj.TreeHash.Hex()) {
		t.Fatal("same advertised tree hash must be skipped after a permanent failure")
	}
	if obj.permanentFailureSkip("k", "v1.0.0", core.HashBytes([]byte("new-tree")).Hex()) {
		t.Fatal("a changed tree hash must not be skipped")
	}

	zeroObj := &Obj{permFailMap: make(map[missKeyObj]string)}
	zeroObj.recordBrotherPermanentFailure(context.Background(), "k", source.BrotherIndexEntryObj{Version: "v2.0.0"}, "tree_invalid", "bad tree")
	if len(zeroObj.permFailMap) != 0 {
		t.Fatal("zero tree hash must not be recorded as a permanent failure")
	}
}

// // // // // // // // // //

// brotherSingleBlobTreeHash mirrors the fake session's one-blob tree so tests can advertise
// an honest TreeHash matching Version bytes.
func brotherSingleBlobTreeHash(t *testing.T, content []byte) core.HashObj {
	t.Helper()
	entryArr := []core.TreeEntryObj{
		{Path: "composer.json", Mode: core.ModeFile, BlobHash: core.HashBytes(content), SizeBytes: uint64(len(content))},
	}
	_, treeHashObj, err := treecodec.Encode(entryArr)
	if err != nil {
		t.Fatalf("encode tree: %v", err)
	}
	return treeHashObj
}

// runBrotherIngestOnce runs one brother-version integration cycle with the supplied index entry.
func runBrotherIngestOnce(t *testing.T, entryObj source.BrotherIndexEntryObj, content []byte) (context.Context, *storage.Obj) {
	t.Helper()
	configObj := rescanTestConfig(t)
	configObj.ReleaseMirrors = map[string]string{"core-lib": "https://brother.example/core-lib/"}
	ctx := context.Background()

	storageObj, err := storage.New(ctx, configObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	t.Cleanup(func() { _ = storageObj.Close(ctx) })
	overlayObj, err := overlay.New(configObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}
	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	archiveObj := archiveFromConfig(t, configObj)

	sessionObj := &fakeBrotherSessionObj{
		indexEntries:  []source.BrotherIndexEntryObj{entryObj},
		blobByVersion: map[string][]byte{entryObj.Version: content},
	}
	fakeSrc := &fakeSourceObj{
		discoverClass:  stcode.SourceClassBrother,
		brotherSession: sessionObj,
		releasesErr:    errors.New("first-source unreachable"),
	}
	New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveObj, "").RunOnce(ctx)
	return ctx, storageObj
}

// TestIngestBrotherAcceptsMatchingIndexHash: an honest brother with matching index TreeHash publishes.
func TestIngestBrotherAcceptsMatchingIndexHash(t *testing.T) {
	content := []byte(`{"name":"vendor/pkg"}`)
	entryObj := source.BrotherIndexEntryObj{Version: "v1.0.0", ReleaseNotes: "n", TreeHash: brotherSingleBlobTreeHash(t, content)}
	ctx, storageObj := runBrotherIngestOnce(t, entryObj, content)
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("matching index hash must publish: ok=%v err=%v", ok, err)
	}
}

// TestIngestBrotherRejectsMismatchedIndexHash: when index and Version tree bytes disagree, the version
// is rejected so an inconsistent index cannot force a download every cycle.
func TestIngestBrotherRejectsMismatchedIndexHash(t *testing.T) {
	content := []byte(`{"name":"vendor/pkg"}`)
	entryObj := source.BrotherIndexEntryObj{Version: "v1.0.0", ReleaseNotes: "n", TreeHash: core.HashBytes([]byte("bad-tree"))}
	ctx, storageObj := runBrotherIngestOnce(t, entryObj, content)
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); ok {
		t.Fatal("mismatched index hash must not publish")
	}
}
