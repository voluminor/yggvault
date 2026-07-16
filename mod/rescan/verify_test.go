package rescan

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func verifySHA(symbolByte byte) string {
	return strings.Repeat(string(symbolByte), 40)
}

func gitReleases(versionArr ...string) []source.GitReleaseObj {
	releaseArr := make([]source.GitReleaseObj, 0, len(versionArr))
	for _, version := range versionArr {
		releaseArr = append(releaseArr, source.GitReleaseObj{
			Version:    version,
			ArchiveURL: "https://x/" + version + ".zip",
			Format:     "zip",
		})
	}
	return releaseArr
}

func newGitVerifyEnv(t *testing.T, configObj *stconf.ConfigObj, fakeSrc *fakeSourceObj) (*Obj, *storage.Obj, *state.Obj) {
	t.Helper()
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
	if fakeSrc.archiveBytes == nil {
		fakeSrc.archiveBytes = buildZip(t, map[string]string{
			"core-lib/README.md": "hello",
			"core-lib/data.txt":  "payload",
		})
	}
	return New(configObj, fakeSrc, storageObj, overlayObj, stateObj, archiveFromConfig(t, configObj), ""), storageObj, stateObj
}

// // // // // // // // // //

// Steady cycle: with matching SHAs every version is skipped on the second cycle, including the latest,
// because a confirmed matching SHA already proves content identity (no wasteful re-download).
func TestGitSecondCycleSkipsAllOnMatchingSha(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.1.0", "v1.0.0"),
		refsMap:    map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')},
	}
	obj, storageObj, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("cycle1 v1.0.0 fetches=%d, want 1", got)
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("cycle2 v1.0.0 fetches=%d, want 1 (skip on matching sha)", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 1 {
		t.Fatalf("cycle2 v1.1.0 fetches=%d, want 1 (latest skipped: confirmed sha matches)", got)
	}

	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamRef != verifySHA('b') {
		t.Fatalf("upstream_ref=%q, want %q", versionObj.UpstreamRef, verifySHA('b'))
	}
	if versionObj.VerifiedTS.IsZero() {
		t.Fatal("verified_ts must be stamped on git publish")
	}
}

// SHA drift for one version refetches only that version; after adoption, drift disappears.
func TestGitShaDriftRefetchesOnlyThatVersion(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.2.0", "v1.1.0", "v1.0.0"),
		refsMap: map[string]string{
			"v1.2.0": verifySHA('a'),
			"v1.1.0": verifySHA('b'),
			"v1.0.0": verifySHA('c'),
		},
	}
	obj, storageObj, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	fakeSrc.refsMap["v1.0.0"] = verifySHA('d')
	obj.RunOnce(ctx)

	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("drifted v1.0.0 fetches=%d, want 2", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 1 {
		t.Fatalf("untouched v1.1.0 fetches=%d, want 1", got)
	}
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamRef != verifySHA('d') {
		t.Fatalf("upstream_ref=%q, want adopted %q", versionObj.UpstreamRef, verifySHA('d'))
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("cycle3 v1.0.0 fetches=%d, want 2 (new sha adopted, no drift)", got)
	}
}

// Resurrection: a listed upstream_deleted version is refetched and revived.
func TestGitReappearedDeletedVersionRefetched(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.1.0", "v1.0.0"),
		refsMap:    map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')},
	}
	obj, storageObj, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	if err := storageObj.MarkUpstreamDeleted(ctx, "core-lib", "v1.0.0"); err != nil {
		t.Fatalf("MarkUpstreamDeleted: %v", err)
	}

	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("reappeared v1.0.0 fetches=%d, want 2", got)
	}
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamDeleted {
		t.Fatal("version must be resurrected after reappearing upstream")
	}
}

// Tiered schedule with confirmed SHAs: both the latest and rank 1 are re-verified only after
// recent_interval expires, since a matching SHA lets the latest skip the every-cycle re-download too.
func TestGitTierScheduleRefetchesAfterInterval(t *testing.T) {
	configObj := rescanTestConfig(t)
	configObj.Rescan.Verify.RecentInterval = 300 * time.Millisecond
	configObj.Rescan.Verify.ArchiveInterval = time.Hour
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.1.0", "v1.0.0"),
		refsMap:    map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')},
	}
	obj, _, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("rank1 before interval fetches=%d, want 1", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 1 {
		t.Fatalf("latest before interval fetches=%d, want 1 (confirmed sha skips re-download)", got)
	}

	time.Sleep(350 * time.Millisecond)
	obj.RunOnce(ctx)
	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("rank1 after interval fetches=%d, want 2 (deep verify due)", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 2 {
		t.Fatalf("latest after interval fetches=%d, want 2 (deep verify due)", got)
	}
}

// SHA adoption: a row with empty upstream_ref accepts the current SHA without download.
func TestGitAdoptsRefWithoutDownload(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{releaseArr: gitReleases("v1.1.0", "v1.0.0")}
	obj, storageObj, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	versionObj, ok, err := storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok || versionObj.UpstreamRef != "" {
		t.Fatalf("precondition: ref=%q ok=%v err=%v, want empty ref", versionObj.UpstreamRef, ok, err)
	}

	fakeSrc.refsMap = map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')}
	obj.RunOnce(ctx)

	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("adopted v1.0.0 fetches=%d, want 1 (no download)", got)
	}
	versionObj, ok, err = storageObj.GetVersion(ctx, "core-lib", "v1.0.0")
	if err != nil || !ok {
		t.Fatalf("GetVersion: ok=%v err=%v", ok, err)
	}
	if versionObj.UpstreamRef != verifySHA('b') {
		t.Fatalf("upstream_ref=%q, want silently adopted %q", versionObj.UpstreamRef, verifySHA('b'))
	}
}

// refs advertisement failure is non-fatal: the cycle proceeds, key stays available, and no extra downloads run.
func TestGitRefsFailureKeepsCycleWithoutRedownload(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.1.0", "v1.0.0"),
		refsMap:    map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')},
	}
	obj, _, stateObj := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	fakeSrc.refsErr = errors.New("advertisement unavailable")
	obj.RunOnce(ctx)

	if got := fakeSrc.fetchCount("v1.0.0"); got != 1 {
		t.Fatalf("v1.0.0 fetches=%d, want 1 (no redownload on refs failure)", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 2 {
		t.Fatalf("latest fetches=%d, want 2 (cycle proceeds)", got)
	}
	keyStateObj, ok := stateObj.KeyState("core-lib")
	if !ok || keyStateObj.Availability != stcode.AvailabilityStatusAvailable {
		t.Fatalf("availability=%v ok=%v, want available (refs failure must not mark key unavailable)", keyStateObj.Availability, ok)
	}
}

// force_refresh bypasses all skips so every listed version is deeply verified.
func TestGitForceRefreshBypassesSkip(t *testing.T) {
	configObj := rescanTestConfig(t)
	ctx := context.Background()
	fakeSrc := &fakeSourceObj{
		releaseArr: gitReleases("v1.1.0", "v1.0.0"),
		refsMap:    map[string]string{"v1.1.0": verifySHA('a'), "v1.0.0": verifySHA('b')},
	}
	obj, _, _ := newGitVerifyEnv(t, configObj, fakeSrc)

	obj.RunOnce(ctx)
	obj.runKey(ctx, "core-lib", true, time.Now().UTC())

	if got := fakeSrc.fetchCount("v1.0.0"); got != 2 {
		t.Fatalf("force_refresh v1.0.0 fetches=%d, want 2", got)
	}
	if got := fakeSrc.fetchCount("v1.1.0"); got != 2 {
		t.Fatalf("force_refresh v1.1.0 fetches=%d, want 2", got)
	}
}
