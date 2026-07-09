package rescan

import (
	"context"
	"fmt"
	"testing"
	"time"

	lightweigit "github.com/voluminor/lightweigit-loader"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

// gitStand builds a core-lib git-key fixture with a fake source layer.
func gitStand(t *testing.T, fakeSrc *fakeSourceObj) (*Obj, *storage.Obj, *state.Obj, context.Context) {
	t.Helper()
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
	return New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveFromConfig(t, configObj), ""), storageObj, stateObj, ctx
}

func listingModeOf(t *testing.T, storageObj *storage.Obj, ctx context.Context, key string) string {
	t.Helper()
	keySourceObj, ok, err := storageObj.GetKeySource(ctx, key)
	if err != nil || !ok {
		t.Fatalf("GetKeySource: ok=%v err=%v", ok, err)
	}
	return keySourceObj.ListingMode
}

func hasDiagnostic(stateObj *state.Obj, code string) bool {
	for _, diagObj := range stateObj.ActiveDiagnostics() {
		if diagObj.Code == code {
			return true
		}
	}
	return false
}

// // // // // // // // // //

// Gitea/Forgejo with disabled releases returns 404: that means "no releases feature",
// not source outage, so tag fallback must behave like an empty releases list.
func TestListingModeTagsFallbackOnReleasesNotFound(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"})
	fakeSrc := &fakeSourceObj{
		releasesErr:  fmt.Errorf("list releases: %w", lightweigit.ErrNotFound),
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)

	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeTags {
		t.Fatalf("listing mode=%q, want tags", mode)
	}
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if hasDiagnostic(stateObj, "upstream_unavailable") {
		t.Fatalf("releases 404 must not mark the key unavailable")
	}
	keyStateObj, known := stateObj.KeyState("core-lib")
	if !known || keyStateObj.Availability != stcode.AvailabilityStatusAvailable {
		t.Fatalf("availability=%v known=%v, want available", keyStateObj.Availability, known)
	}
}

func TestListingModeTagsDecidedWhenReleasesEmpty(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"})
	const tagSHA = "aaaabbbbccccddddeeeeffff0000111122223333"
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
		refsMap:      map[string]string{"v1.0.0": tagSHA},
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)

	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeTags {
		t.Fatalf("listing mode=%q, want tags", mode)
	}
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.ReleaseNotes != "" {
		t.Fatalf("tag-mode version must have no release notes, got %q", versionObj.ReleaseNotes)
	}
	// Section 6: refs advertisement keyed by tag name also works in tags mode.
	if versionObj.UpstreamRef != tagSHA {
		t.Fatalf("upstream ref=%q, want %q from refs advertisement", versionObj.UpstreamRef, tagSHA)
	}
	keyStateObj, known := stateObj.KeyState("core-lib")
	if !known || keyStateObj.Availability != stcode.AvailabilityStatusAvailable {
		t.Fatalf("availability=%v known=%v, want available", keyStateObj.Availability, known)
	}
}

func TestListingModeTagsStickyProbesReleasesShallow(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"})
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	if got := fakeSrc.tagsCallCount(); got != 1 {
		t.Fatalf("tags calls after cycle 1 = %d, want 1", got)
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.tagsCallCount(); got != 2 {
		t.Fatalf("tags calls after cycle 2 = %d, want 2", got)
	}
	depthArr := fakeSrc.releasesDepthLog()
	if len(depthArr) != 2 {
		t.Fatalf("releases calls=%d, want 2 (decision + shallow probe): %v", len(depthArr), depthArr)
	}
	if depthArr[1] != 1 {
		t.Fatalf("sticky tags mode must probe releases with depth=1, got %d", depthArr[1])
	}
	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeTags {
		t.Fatalf("listing mode=%q, want tags to stay sticky", mode)
	}
}

func TestListingModeConflictFreezesKey(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"})
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("fetch count after cycle 1 = %d, want 1", got)
	}

	// A tags key suddenly reports a release: conflict and full update freeze.
	fakeSrc.releaseArr = []source.GitReleaseObj{{Version: "v9.9.9", ArchiveURL: "https://x/r.zip", Format: "zip"}}
	for cycleNum := 2; cycleNum <= 3; cycleNum++ {
		obj.RunOnce(ctx)
		if !hasDiagnostic(stateObj, "listing_mode_conflict") {
			t.Fatalf("cycle %d: expected listing_mode_conflict diagnostic, got %+v", cycleNum, stateObj.ActiveDiagnostics())
		}
		if got := fakeSrc.tagsCallCount(); got != 1 {
			t.Fatalf("cycle %d: tags calls=%d, want 1 (frozen key must not list tags)", cycleNum, got)
		}
		if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
			t.Fatalf("cycle %d: fetch count=%d, want 1 (frozen key must not refetch)", cycleNum, got)
		}
		if got := fakeSrc.fetchCount("v9.9.9"); got != 0 {
			t.Fatalf("cycle %d: conflicting release fetched %d times, want 0", cycleNum, got)
		}
	}

	// Existing versions keep serving, and mode plus availability stay untouched.
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("frozen key must keep serving stored versions: ok=%v err=%v", ok, err)
	}
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v9.9.9"); ok {
		t.Fatal("conflicting release must not be ingested")
	}
	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeTags {
		t.Fatalf("listing mode=%q, want tags (no auto-switch)", mode)
	}
	keyStateObj, known := stateObj.KeyState("core-lib")
	if !known || keyStateObj.Availability != stcode.AvailabilityStatusAvailable {
		t.Fatalf("availability=%v known=%v, want available (degraded-first: freeze is not downtime)", keyStateObj.Availability, known)
	}
}

func TestListingModeReleasesNeverCallsTags(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "released"})
	fakeSrc := &fakeSourceObj{
		releaseArr:   []source.GitReleaseObj{{Version: "v1.0.0", BodyMD: "notes", ArchiveURL: "https://x/r.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	obj.RunOnce(ctx)

	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeReleases {
		t.Fatalf("listing mode=%q, want releases", mode)
	}
	if got := fakeSrc.tagsCallCount(); got != 0 {
		t.Fatalf("tags calls=%d, want 0 for a releases-mode key", got)
	}
}

func TestListingModeUndecidedWhenBothEmpty(t *testing.T) {
	fakeSrc := &fakeSourceObj{}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)

	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeUndecided {
		t.Fatalf("listing mode=%q, want undecided when both listings are empty", mode)
	}
	if keyStateObj, known := stateObj.KeyState("core-lib"); !known || keyStateObj.Availability == stcode.AvailabilityStatusPermanentDown {
		t.Fatalf("empty listings must not mark the key down: %+v known=%v", keyStateObj, known)
	}

	// Decision is delayed until first non-empty listing; releases appeared, so mode is releases.
	fakeSrc.releaseArr = []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/r.zip", Format: "zip"}}
	fakeSrc.archiveBytes = buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "late"})
	obj.RunOnce(ctx)
	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeReleases {
		t.Fatalf("listing mode=%q, want releases after late first listing", mode)
	}
}

// A repository with only unstorable releases such as rolling `latest` must not pin empty releases mode:
// storable names decide mode, tags stay authoritative, and conflict probes use the same logic.
func TestListingModeRollingLatestReleaseKeepsTags(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "tagged"})
	fakeSrc := &fakeSourceObj{
		releaseArr:   []source.GitReleaseObj{{Version: "latest", ArchiveURL: "https://x/l.zip", Format: "zip"}},
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	obj.RunOnce(ctx)

	if mode := listingModeOf(t, storageObj, ctx, "core-lib"); mode != core.ListingModeTags {
		t.Fatalf("listing mode=%q, want tags (rolling latest is unstorable)", mode)
	}
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if hasDiagnostic(stateObj, "listing_mode_conflict") {
		t.Fatal("unstorable release name must not trigger the conflict freeze")
	}
}

// persistMode=false for brother keys makes mode decisions ephemeral: releases are authoritative for the attempt,
// sticky mode is not written, and conflict freeze is impossible.
func TestIngestGitEphemeralModeDoesNotPersist(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "x"})
	fakeSrc := &fakeSourceObj{
		releaseArr:   []source.GitReleaseObj{{Version: "v2.0.0", ArchiveURL: "https://x/r.zip", Format: "zip"}},
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/t.zip", Format: "zip"}},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.ingestGit(ctx, "core-lib", "https://git.example/o/r", time.Now().UTC(), false, false, "", false)

	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v2.0.0"); err != nil || !ok {
		t.Fatalf("releases must be authoritative for the ephemeral attempt: ok=%v err=%v", ok, err)
	}
	if ksObj, ok, _ := storageObj.GetKeySource(ctx, "core-lib"); ok && ksObj.ListingMode != "" {
		t.Fatalf("ephemeral mode must not persist, got %q", ksObj.ListingMode)
	}
	if hasDiagnostic(stateObj, "listing_mode_conflict") {
		t.Fatal("ephemeral mode must never raise the conflict freeze")
	}
	if got := fakeSrc.tagsCallCount(); got != 0 {
		t.Fatalf("tags calls=%d, want 0 (releases were non-empty)", got)
	}
}

// Raw versions are not comparable by semver floor, so their window guard is upstream_seq.
// An old raw version below the listing window is not considered deleted; a version that actually
// disappears from the top window is marked deleted after grace cycles.
func TestRawVersionDeletionWindowExemption(t *testing.T) {
	archiveBytes := buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "raw"})
	fakeSrc := &fakeSourceObj{
		tagArr: []source.GitReleaseObj{
			{Version: "REL_B", ArchiveURL: "https://x/b.zip", Format: "zip"},
			{Version: "REL_A", ArchiveURL: "https://x/a.zip", Format: "zip"},
		},
		archiveBytes: archiveBytes,
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	for _, version := range []string{"REL_A", "REL_B"} {
		if _, ok, err := storageObj.GetVersion(ctx, "core-lib", version); err != nil || !ok {
			t.Fatalf("cycle 1: GetVersion(%s): ok=%v err=%v", version, ok, err)
		}
	}

	// The listing window moved up: REL_A is older than the minimum listed seq and is exempt.
	fakeSrc.tagArr = []source.GitReleaseObj{
		{Version: "REL_C", ArchiveURL: "https://x/c.zip", Format: "zip"},
		{Version: "REL_B", ArchiveURL: "https://x/b.zip", Format: "zip"},
	}
	for i := 0; i < 4; i++ {
		obj.RunOnce(ctx)
	}
	oldObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "REL_A")
	if err != nil || !ok {
		t.Fatalf("REL_A must survive below the listing window: ok=%v err=%v", ok, err)
	}
	if oldObj.UpstreamDeleted {
		t.Fatal("REL_A below the window must not be marked upstream-deleted")
	}

	// Real disappearance: REL_C, the top-window item, vanishes and is marked deleted after grace.
	fakeSrc.tagArr = []source.GitReleaseObj{
		{Version: "REL_B", ArchiveURL: "https://x/b.zip", Format: "zip"},
	}
	for i := 0; i < 4; i++ {
		obj.RunOnce(ctx)
	}
	goneObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "REL_C")
	if err != nil || !ok {
		t.Fatalf("GetVersion(REL_C): ok=%v err=%v", ok, err)
	}
	if !goneObj.UpstreamDeleted {
		t.Fatal("REL_C vanished from the top of the listing and must be marked upstream-deleted after grace")
	}
}

// A permanent ingest failure such as a broken archive must not download every cycle:
// the version is skipped until tag SHA changes or force_refresh arrives.
func TestPermanentIngestFailureSkipsUntilShaChanges(t *testing.T) {
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/bad.zip", Format: "zip"}},
		archiveBytes: []byte("definitely not a zip archive"),
		refsMap:      map[string]string{"v1.0.0": "aaaabbbbccccddddeeeeffff0000111122223333"},
	}
	obj, storageObj, stateObj, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("cycle 1: fetch count=%d, want 1", got)
	}
	if !hasDiagnostic(stateObj, "archive_invalid") {
		t.Fatalf("expected archive_invalid diagnostic, got %+v", stateObj.ActiveDiagnostics())
	}
	if _, ok, _ := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); ok {
		t.Fatal("broken archive must not publish")
	}

	// Same SHA: silent skip without downloads.
	for cycleNum := 2; cycleNum <= 4; cycleNum++ {
		obj.RunOnce(ctx)
		if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
			t.Fatalf("cycle %d: fetch count=%d, want 1 (permanent failure must not refetch)", cycleNum, got)
		}
	}

	// Reuploaded tag with changed SHA: one new attempt, then failure is remembered again.
	fakeSrc.refsMap = map[string]string{"v1.0.0": "9999888877776666555544443333222211110000"}
	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("after sha change: fetch count=%d, want 2", got)
	}
	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("after re-failure: fetch count=%d, want 2 (silent again)", got)
	}

	// force_refresh bypasses failure memory.
	obj.ingestGit(ctx, "core-lib", "https://git.example/o/r", time.Now().UTC(), false, true, core.ListingModeTags, true)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 3 {
		t.Fatalf("force_refresh: fetch count=%d, want 3", got)
	}

	// Content is fixed: the version publishes and failure memory is cleared.
	fakeSrc.archiveBytes = buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "fixed"})
	fakeSrc.refsMap = map[string]string{"v1.0.0": "1111222233334444555566667777888899990000"}
	obj.RunOnce(ctx)
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("fixed archive must publish: ok=%v err=%v", ok, err)
	}
}

func TestTerminalSuccessClearsDurableQuarantineAfterLoadMiss(t *testing.T) {
	refText := verifySHA('a')
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/a.zip", Format: "zip"}},
		archiveBytes: buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "ok"}),
		refsMap:      map[string]string{"v1.0.0": refText},
	}
	obj, storageObj, _, ctx := gitStand(t, fakeSrc)

	obj.RunOnce(ctx)
	if _, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0"); err != nil || !ok {
		t.Fatalf("precondition publish: ok=%v err=%v", ok, err)
	}
	if err := storageObj.PutIngestFailure(ctx, core.IngestFailureObj{
		Key:     "core-lib",
		Version: "v1.0.0",
		RefSHA:  refText,
		Code:    "archive_invalid",
		Message: "stale quarantine row from missed startup load",
	}); err != nil {
		t.Fatalf("PutIngestFailure returned error: %v", err)
	}

	obj.permFailMu.Lock()
	obj.permFailLoadMissObj["core-lib"] = struct{}{}
	obj.permFailMu.Unlock()

	obj.RunOnce(ctx)
	failureArr, err := storageObj.ListIngestFailures(ctx, "core-lib")
	if err != nil {
		t.Fatalf("ListIngestFailures returned error: %v", err)
	}
	if len(failureArr) != 0 {
		t.Fatalf("stale durable quarantine rows=%+v, want none", failureArr)
	}
	if obj.permanentFailureSkip("core-lib", "v1.0.0", refText) {
		t.Fatal("stale quarantine row must not be reloaded into permanent failure memory")
	}
	obj.permFailMu.Lock()
	_, loadMiss := obj.permFailLoadMissObj["core-lib"]
	obj.permFailMu.Unlock()
	if loadMiss {
		t.Fatal("load-miss marker must be cleared after a successful quarantine summary")
	}
}

// TestQuarantineSummarySkipsCleanKeys covers the dirty gate that lets raiseQuarantineSummary skip the
// per-cycle durable ListIngestFailures round-trip for healthy keys: a key with no quarantine history stays
// clean, a recorded failure marks it dirty, and reconciliation drops the mark again.
func TestQuarantineSummarySkipsCleanKeys(t *testing.T) {
	refText := verifySHA('a')
	fakeSrc := &fakeSourceObj{
		tagArr:       []source.GitReleaseObj{{Version: "v1.0.0", ArchiveURL: "https://x/a.zip", Format: "zip"}},
		archiveBytes: buildZip(t, map[string]string{"core-lib-1.0.0/README.md": "ok"}),
		refsMap:      map[string]string{"v1.0.0": refText},
	}
	obj, _, _, ctx := gitStand(t, fakeSrc)

	isDirty := func() bool {
		obj.permFailMu.Lock()
		defer obj.permFailMu.Unlock()
		_, ok := obj.quarantineDirtyObj["core-lib"]
		return ok
	}

	obj.RunOnce(ctx)
	if isDirty() {
		t.Fatal("healthy key must not be dirty: quarantine summary would run every cycle")
	}

	obj.recordPermanentFailure(ctx, "core-lib", "v1.0.0", refText, "archive_invalid", "bad archive")
	if !isDirty() {
		t.Fatal("recorded failure must mark the key dirty so the summary reconciles it")
	}

	obj.clearPermanentFailure(ctx, "core-lib", "v1.0.0")
	obj.raiseQuarantineSummary(ctx, "core-lib")
	if isDirty() {
		t.Fatal("key must be clean after quarantine reconciliation drains all failures")
	}
}
