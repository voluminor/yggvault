package state

import (
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func newTestConfigObj() *stcfg.ConfigObj {
	return &stcfg.ConfigObj{
		ReleaseMirrors: map[string]string{
			"core-lib": "https://example.com/core-lib",
			"ui-kit":   "https://example.com/ui-kit",
		},
		UpstreamAvailability: stcfg.UpstreamAvailabilityObj{
			PermanentAfterCycles: 2,
		},
	}
}

func newTestObj(t *testing.T) *Obj {
	t.Helper()

	obj, err := New(newTestConfigObj())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	return obj
}

func testHashObj(text string) core.HashObj {
	return core.HashBytes([]byte(text))
}

func hasReason(reasonArr []stcode.LogReasonType, reasonObj stcode.LogReasonType) bool {
	for _, itemObj := range reasonArr {
		if itemObj == reasonObj {
			return true
		}
	}
	return false
}

func testDiagnosticObj(code string, scopeObj stcode.LogScopeType, impactObj stcode.OperationalStatusType, reasonObj stcode.LogReasonType, key string, version string, message string) DiagnosticObj {
	return DiagnosticObj{
		Code:    code,
		Scope:   scopeObj,
		Impact:  impactObj,
		Reason:  reasonObj,
		Key:     key,
		Version: version,
		Message: message,
	}
}

func testUpstreamDiagnosticObj(key string) DiagnosticObj {
	return testDiagnosticObj(
		"upstream_unavailable",
		stcode.LogScopeKey,
		stcode.OperationalStatusDegraded,
		stcode.LogReasonUpstreamUnavailable,
		key,
		"",
		"network down",
	)
}

func testBuildDiagnosticObj(key string, version string) DiagnosticObj {
	return testDiagnosticObj(
		"go_overlay_build_failed",
		stcode.LogScopeVersion,
		stcode.OperationalStatusError,
		stcode.LogReasonGoOverlayDegraded,
		key,
		version,
		"go build failed",
	)
}

func testGlobalDiagnosticObj() DiagnosticObj {
	return testDiagnosticObj(
		"cache_quota_exceeded",
		stcode.LogScopeGlobal,
		stcode.OperationalStatusError,
		stcode.LogReasonCacheQuotaExceeded,
		"",
		"",
		"quota exceeded",
	)
}

func testDiagnosticKeyObj(diagnosticObj DiagnosticObj) DiagnosticKeyObj {
	return DiagnosticKeyObj{
		Code:    diagnosticObj.Code,
		Scope:   diagnosticObj.Scope,
		Key:     diagnosticObj.Key,
		Version: diagnosticObj.Version,
	}
}

func assertInactiveDiagnosticList(t *testing.T, obj *Obj) {
	t.Helper()

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	seenMap := make(map[*diagnosticRecordObj]struct{}, obj.inactiveLen)
	count := 0
	var prevObj *diagnosticRecordObj
	for recordObj := obj.inactiveHead; recordObj != nil; recordObj = recordObj.inactiveNext {
		if _, ok := seenMap[recordObj]; ok {
			t.Fatal("inactive diagnostic list contains a cycle")
		}
		if recordObj.active {
			t.Fatalf("active diagnostic is linked as inactive: %#v", keyFromDiagnosticRecordObj(recordObj))
		}
		if recordObj.inactivePrev != prevObj {
			t.Fatalf("inactive diagnostic prev link is broken: %#v", keyFromDiagnosticRecordObj(recordObj))
		}
		if obj.diagnosticMap[keyFromDiagnosticRecordObj(recordObj)] != recordObj {
			t.Fatalf("inactive diagnostic is not present in map: %#v", keyFromDiagnosticRecordObj(recordObj))
		}
		seenMap[recordObj] = struct{}{}
		prevObj = recordObj
		count++
	}
	if obj.inactiveTail != prevObj {
		t.Fatal("inactive diagnostic tail link is broken")
	}
	if obj.inactiveLen != count {
		t.Fatalf("inactive diagnostic list len=%d, want %d", obj.inactiveLen, count)
	}

	inactiveCount := 0
	for keyObj, recordObj := range obj.diagnosticMap {
		if recordObj.active {
			if _, ok := seenMap[recordObj]; ok {
				t.Fatalf("active diagnostic is present in inactive list: %#v", keyObj)
			}
			if recordObj.inactivePrev != nil || recordObj.inactiveNext != nil {
				t.Fatalf("active diagnostic has stale inactive links: %#v", keyObj)
			}
			continue
		}
		inactiveCount++
		if _, ok := seenMap[recordObj]; !ok {
			t.Fatalf("inactive diagnostic is missing from list: %#v", keyObj)
		}
	}
	if inactiveCount != count {
		t.Fatalf("inactive diagnostics in map=%d, want list len %d", inactiveCount, count)
	}
}

func assertDiagnosticMapContains(t *testing.T, obj *Obj, diagnosticObj DiagnosticObj, wantFlag bool) {
	t.Helper()

	keyObj := keyFromDiagnosticObj(diagnosticObj)
	obj.lockObj.Lock()
	_, ok := obj.diagnosticMap[keyObj]
	obj.lockObj.Unlock()
	if ok != wantFlag {
		t.Fatalf("diagnostic map contains %#v = %v, want %v", keyObj, ok, wantFlag)
	}
}

// //

func TestNewSeedsSnapshot(t *testing.T) {
	obj := newTestObj(t)

	healthObj := obj.Health()
	if healthObj.Status != stcode.OperationalStatusOk {
		t.Fatalf("health status=%s, want ok", healthObj.Status.String())
	}
	if healthObj.DiagnosticsCount != 0 {
		t.Fatalf("diagnostics count=%d, want 0", healthObj.DiagnosticsCount)
	}

	keyArr := obj.KeyStates()
	if len(keyArr) != 2 {
		t.Fatalf("key count=%d, want 2", len(keyArr))
	}
	if keyArr[0].Key != "core-lib" || keyArr[1].Key != "ui-kit" {
		t.Fatalf("keys are not sorted: %#v", keyArr)
	}

	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found")
	}
	if keyObj.Availability != stcode.AvailabilityStatusUnknown {
		t.Fatalf("availability=%s, want unknown", keyObj.Availability.String())
	}
	if keyObj.SourceURL != "https://example.com/core-lib" {
		t.Fatalf("source URL=%q", keyObj.SourceURL)
	}
}

// A same-cycle correction (equal timestamp on top of Available) is applied:
// the "available" recovery write is corrected by the listing outcome; a replay on top of down is a no-op.
func TestAvailabilitySameCycleCorrection(t *testing.T) {
	obj := newTestObj(t)
	scanAt := time.Unix(100, 0).UTC()

	if err := obj.MarkAvailable("core-lib", true, scanAt); err != nil {
		t.Fatalf("MarkAvailable returned error: %v", err)
	}
	if err := obj.MarkUnavailable("core-lib", scanAt); err != nil {
		t.Fatalf("MarkUnavailable correction returned error: %v", err)
	}
	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found")
	}
	if keyObj.Availability != stcode.AvailabilityStatusTemporaryDown || keyObj.UnavailableCycles != 1 {
		t.Fatalf("same-cycle correction not applied: %#v", keyObj)
	}

	if err := obj.MarkUnavailable("core-lib", scanAt); err != nil {
		t.Fatalf("MarkUnavailable replay returned error: %v", err)
	}
	keyObj, _ = obj.KeyState("core-lib")
	if keyObj.UnavailableCycles != 1 {
		t.Fatalf("replay incremented cycles: %d", keyObj.UnavailableCycles)
	}

	scan2 := scanAt.Add(time.Minute)
	if err := obj.MarkAvailable("core-lib", true, scan2); err != nil {
		t.Fatalf("MarkAvailable recovery returned error: %v", err)
	}
	if err := obj.MarkAvailable("core-lib", false, scan2); err != nil {
		t.Fatalf("MarkAvailable present correction returned error: %v", err)
	}
	keyObj, _ = obj.KeyState("core-lib")
	if keyObj.Availability != stcode.AvailabilityStatusAvailable || keyObj.UpstreamPresent {
		t.Fatalf("present correction not applied: %#v", keyObj)
	}
}

func TestAvailabilityAndReclassGate(t *testing.T) {
	obj := newTestObj(t)

	if err := obj.SetClassification("core-lib", stcode.SourceClassGit, "", ""); err != nil {
		t.Fatalf("SetClassification returned error: %v", err)
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassBrother, "", ""); err == nil {
		t.Fatal("classification changed without reclass gate")
	}

	scanAt := time.Unix(100, 0).UTC()
	if err := obj.MarkUnavailable("core-lib", scanAt); err != nil {
		t.Fatalf("MarkUnavailable #1 returned error: %v", err)
	}
	if err := obj.MarkUnavailable("core-lib", scanAt.Add(time.Minute)); err != nil {
		t.Fatalf("MarkUnavailable #2 returned error: %v", err)
	}

	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found")
	}
	if keyObj.Availability != stcode.AvailabilityStatusPermanentDown {
		t.Fatalf("availability=%s, want permanent_down", keyObj.Availability.String())
	}
	if keyObj.UnavailableCycles != 2 {
		t.Fatalf("unavailable cycles=%d, want 2", keyObj.UnavailableCycles)
	}

	if err := obj.MarkAvailable("core-lib", true, scanAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkAvailable returned error: %v", err)
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassBrother, "", "https://brother.example/core-lib"); err != nil {
		t.Fatalf("SetClassification after recovery returned error: %v", err)
	}

	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after recovery")
	}
	if keyObj.Classification != stcode.SourceClassBrother {
		t.Fatalf("classification=%s, want brother", keyObj.Classification.String())
	}
	if keyObj.Availability != stcode.AvailabilityStatusAvailable {
		t.Fatalf("availability=%s, want available", keyObj.Availability.String())
	}
	if !keyObj.UpstreamPresent {
		t.Fatal("upstream present flag was not set")
	}

	if err := obj.MarkUnavailable("core-lib", scanAt.Add(3*time.Minute)); err != nil {
		t.Fatalf("MarkUnavailable after recovery returned error: %v", err)
	}
	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after down")
	}
	if keyObj.UpstreamPresent {
		t.Fatal("upstream present flag stayed true after unavailable scan")
	}

	if err := obj.MarkUnavailable("core-lib", scanAt.Add(4*time.Minute)); err != nil {
		t.Fatalf("MarkUnavailable permanent returned error: %v", err)
	}
	if err := obj.MarkAvailable("core-lib", true, scanAt.Add(5*time.Minute)); err != nil {
		t.Fatalf("MarkAvailable second recovery returned error: %v", err)
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassGit, "", ""); err != nil {
		t.Fatalf("SetClassification back to git returned error: %v", err)
	}
	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after git reclass")
	}
	if keyObj.BrotherURL != "" {
		t.Fatalf("brother URL was not cleared after git reclass: %q", keyObj.BrotherURL)
	}
}

func TestAvailabilityIgnoresReplayAndOlderScan(t *testing.T) {
	obj := newTestObj(t)

	if err := obj.SetClassification("core-lib", stcode.SourceClassGit, "", ""); err != nil {
		t.Fatalf("SetClassification returned error: %v", err)
	}

	firstAt := time.Unix(500, 0).UTC()
	secondAt := firstAt.Add(time.Minute)
	thirdAt := secondAt.Add(time.Minute)

	if err := obj.MarkUnavailable("core-lib", firstAt); err != nil {
		t.Fatalf("MarkUnavailable first returned error: %v", err)
	}
	beforeGeneration := obj.Generation()

	if err := obj.MarkUnavailable("core-lib", firstAt); err != nil {
		t.Fatalf("MarkUnavailable replay returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration {
		t.Fatalf("generation changed on replayed unavailable scan: %d", obj.Generation())
	}
	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after replay")
	}
	if keyObj.Availability != stcode.AvailabilityStatusTemporaryDown || keyObj.UnavailableCycles != 1 {
		t.Fatalf("replayed unavailable scan changed state: %#v", keyObj)
	}

	if err := obj.MarkAvailable("core-lib", true, firstAt); err != nil {
		t.Fatalf("MarkAvailable stale returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration {
		t.Fatalf("generation changed on stale available scan: %d", obj.Generation())
	}
	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after stale available")
	}
	if keyObj.Availability != stcode.AvailabilityStatusTemporaryDown || keyObj.UpstreamPresent {
		t.Fatalf("stale available scan changed state: %#v", keyObj)
	}

	if err := obj.MarkUnavailable("core-lib", secondAt); err != nil {
		t.Fatalf("MarkUnavailable second returned error: %v", err)
	}
	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after second unavailable")
	}
	if keyObj.Availability != stcode.AvailabilityStatusPermanentDown || keyObj.UnavailableCycles != 2 {
		t.Fatalf("second unavailable scan did not make permanent down: %#v", keyObj)
	}
	permanentGeneration := obj.Generation()

	if err := obj.MarkAvailable("core-lib", true, firstAt); err != nil {
		t.Fatalf("MarkAvailable older returned error: %v", err)
	}
	if obj.Generation() != permanentGeneration {
		t.Fatalf("generation changed on older available scan: %d", obj.Generation())
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassBrother, "", "https://brother.example/core-lib"); err == nil {
		t.Fatal("stale recovery unlocked reclassification")
	}

	if err := obj.MarkAvailable("core-lib", true, thirdAt); err != nil {
		t.Fatalf("MarkAvailable recovery returned error: %v", err)
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassBrother, "", "https://brother.example/core-lib"); err != nil {
		t.Fatalf("SetClassification after real recovery returned error: %v", err)
	}
	keyObj, ok = obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found after recovery")
	}
	if keyObj.Availability != stcode.AvailabilityStatusAvailable || keyObj.UnavailableCycles != 0 || !keyObj.UpstreamPresent {
		t.Fatalf("real recovery did not reset availability state: %#v", keyObj)
	}
}

func TestReclassGateClearedOnNewUnavailable(t *testing.T) {
	obj := newTestObj(t)
	scanAt := time.Unix(700, 0).UTC()

	if err := obj.SetClassification("core-lib", stcode.SourceClassGit, "", ""); err != nil {
		t.Fatalf("SetClassification returned error: %v", err)
	}
	if err := obj.MarkUnavailable("core-lib", scanAt); err != nil {
		t.Fatalf("MarkUnavailable first returned error: %v", err)
	}
	if err := obj.MarkUnavailable("core-lib", scanAt.Add(time.Minute)); err != nil {
		t.Fatalf("MarkUnavailable permanent returned error: %v", err)
	}
	if err := obj.MarkAvailable("core-lib", true, scanAt.Add(2*time.Minute)); err != nil {
		t.Fatalf("MarkAvailable recovery returned error: %v", err)
	}
	if err := obj.MarkUnavailable("core-lib", scanAt.Add(3*time.Minute)); err != nil {
		t.Fatalf("MarkUnavailable after recovery returned error: %v", err)
	}
	if err := obj.SetClassification("core-lib", stcode.SourceClassBrother, "", "https://brother.example/core-lib"); err == nil {
		t.Fatal("new unavailable scan did not clear reclassification gate")
	}
}

func TestChecksumsAndGeneration(t *testing.T) {
	obj := newTestObj(t)

	contentObj := testHashObj("content-1")
	obj.SetContentChecksum(contentObj)

	if obj.Checksums().Content != contentObj {
		t.Fatalf("unexpected content checksum: %#v", obj.Checksums())
	}
	if obj.Generation() != 1 {
		t.Fatalf("generation=%d, want 1", obj.Generation())
	}

	obj.SetContentChecksum(contentObj)
	if obj.Generation() != 1 {
		t.Fatalf("generation changed on same content checksum: %d", obj.Generation())
	}

	beforeGeneration := obj.Generation()
	if err := obj.SetClassification("core-lib", stcode.SourceClassGit, "", ""); err != nil {
		t.Fatalf("SetClassification returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration+1 {
		t.Fatalf("generation=%d after classification, want %d", obj.Generation(), beforeGeneration+1)
	}
}

func TestMirrorStatsLastRescanAndBatchReject(t *testing.T) {
	obj := newTestObj(t)
	publishedAt := time.Unix(300, 0).UTC()

	if err := obj.SetMirrorStats(MirrorStatsObj{
		Key:           "core-lib",
		LatestVersion: "v1.2.3",
		VersionCount:  7,
		LastPublishTS: publishedAt,
	}); err != nil {
		t.Fatalf("SetMirrorStats returned error: %v", err)
	}

	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found")
	}
	if keyObj.LatestVersion != "v1.2.3" || keyObj.VersionCount != 7 || !keyObj.LastPublishTS.Equal(publishedAt) {
		t.Fatalf("unexpected mirror stats: %#v", keyObj)
	}

	beforeGeneration := obj.Generation()
	if err := obj.SetMirrorStats(MirrorStatsObj{
		Key:           "core-lib",
		LatestVersion: "v1.2.3",
		VersionCount:  7,
		LastPublishTS: publishedAt,
	}); err != nil {
		t.Fatalf("SetMirrorStats no-op returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration {
		t.Fatalf("generation changed on mirror stats no-op: %d", obj.Generation())
	}

	err := obj.SetMirrorStatsBatch([]MirrorStatsObj{
		{Key: "ui-kit", LatestVersion: "v2.0.0", VersionCount: 3, LastPublishTS: publishedAt},
		{Key: "missing", LatestVersion: "v1.0.0", VersionCount: 1, LastPublishTS: publishedAt},
	})
	if err == nil {
		t.Fatal("SetMirrorStatsBatch accepted unknown key")
	}
	keyObj, ok = obj.KeyState("ui-kit")
	if !ok {
		t.Fatal("ui-kit state not found")
	}
	if keyObj.LatestVersion != "" || keyObj.VersionCount != 0 {
		t.Fatalf("batch partially applied before error: %#v", keyObj)
	}

	if err = obj.SetMirrorStatsBatch([]MirrorStatsObj{
		{Key: "core-lib"},
		{Key: "core-lib"},
	}); err == nil {
		t.Fatal("SetMirrorStatsBatch accepted duplicate key")
	}

	rescanAt := time.Unix(400, 0).UTC()
	obj.SetLastRescan(rescanAt)
	snapshotObj := obj.Snapshot()
	if !snapshotObj.LastRescan.Equal(rescanAt) {
		t.Fatalf("last rescan=%s, want %s", snapshotObj.LastRescan, rescanAt)
	}
}

func TestRegistryHealthAndClear(t *testing.T) {
	obj := newTestObj(t)

	warnObj := testUpstreamDiagnosticObj("core-lib")
	if err := obj.RaiseDiagnostic(warnObj); err != nil {
		t.Fatalf("RaiseDiagnostic warn returned error: %v", err)
	}
	if err := obj.RaiseDiagnostic(warnObj); err != nil {
		t.Fatalf("RaiseDiagnostic duplicate returned error: %v", err)
	}

	healthObj := obj.Health()
	if healthObj.Status != stcode.OperationalStatusDegraded {
		t.Fatalf("health status=%s, want degraded", healthObj.Status.String())
	}
	if !hasReason(healthObj.Reasons, stcode.LogReasonUpstreamUnavailable) {
		t.Fatalf("missing upstream reason: %#v", healthObj.Reasons)
	}

	diagnosticArr := obj.ActiveDiagnostics()
	if len(diagnosticArr) != 1 || diagnosticArr[0].Count != 2 {
		t.Fatalf("unexpected active diagnostics: %#v", diagnosticArr)
	}

	versionObj := testBuildDiagnosticObj("core-lib", "v1.0.0")
	if err := obj.RaiseDiagnostic(versionObj); err != nil {
		t.Fatalf("RaiseDiagnostic version returned error: %v", err)
	}

	keyObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("core-lib state not found")
	}
	if keyObj.Status != stcode.OperationalStatusError {
		t.Fatalf("key status=%s, want error", keyObj.Status.String())
	}
	if obj.Health().Status != stcode.OperationalStatusError {
		t.Fatalf("health status=%s, want error", obj.Health().Status.String())
	}

	if err := obj.ClearVersionDiagnostics("core-lib", "v1.0.0"); err != nil {
		t.Fatalf("ClearVersionDiagnostics returned error: %v", err)
	}
	if obj.Health().Status != stcode.OperationalStatusDegraded {
		t.Fatalf("health status=%s, want degraded", obj.Health().Status.String())
	}

	if err := obj.ClearKeyDiagnostics("core-lib"); err != nil {
		t.Fatalf("ClearKeyDiagnostics returned error: %v", err)
	}
	if obj.Health().Status != stcode.OperationalStatusOk {
		t.Fatalf("health status=%s, want ok", obj.Health().Status.String())
	}
}

func TestClearAllDiagnostics(t *testing.T) {
	obj := newTestObj(t)

	if err := obj.RaiseDiagnostic(testUpstreamDiagnosticObj("core-lib")); err != nil {
		t.Fatalf("RaiseDiagnostic core returned error: %v", err)
	}
	if err := obj.RaiseDiagnostic(testUpstreamDiagnosticObj("ui-kit")); err != nil {
		t.Fatalf("RaiseDiagnostic ui-kit returned error: %v", err)
	}
	beforeGeneration := obj.Generation()

	obj.ClearAllDiagnostics()
	healthObj := obj.Health()
	if healthObj.Status != stcode.OperationalStatusOk || healthObj.DiagnosticsCount != 0 {
		t.Fatalf("ClearAllDiagnostics left active health state: %#v", healthObj)
	}
	if len(obj.ActiveDiagnostics()) != 0 {
		t.Fatalf("ClearAllDiagnostics left active diagnostics: %#v", obj.ActiveDiagnostics())
	}
	if obj.Generation() != beforeGeneration+1 {
		t.Fatalf("generation=%d after clear-all, want %d", obj.Generation(), beforeGeneration+1)
	}

	afterGeneration := obj.Generation()
	obj.ClearAllDiagnostics()
	if obj.Generation() != afterGeneration {
		t.Fatalf("generation changed on clear-all no-op: %d", obj.Generation())
	}
}

func TestRegistryBounded(t *testing.T) {
	obj := newTestObj(t)
	obj.maxDiagnostics = 2

	globalObj := testGlobalDiagnosticObj()
	if err := obj.RaiseDiagnostic(globalObj); err != nil {
		t.Fatalf("RaiseDiagnostic global returned error: %v", err)
	}
	obj.ClearAllDiagnostics()

	coreObj := testUpstreamDiagnosticObj("core-lib")
	if err := obj.RaiseDiagnostic(coreObj); err != nil {
		t.Fatalf("RaiseDiagnostic core returned error: %v", err)
	}

	uiObj := testUpstreamDiagnosticObj("ui-kit")
	if err := obj.RaiseDiagnostic(uiObj); err != nil {
		t.Fatalf("RaiseDiagnostic ui-kit returned error: %v", err)
	}
	if len(obj.diagnosticMap) != 2 {
		t.Fatalf("diagnostic map size=%d, want 2", len(obj.diagnosticMap))
	}

	versionObj := testBuildDiagnosticObj("core-lib", "v1.0.0")
	if err := obj.RaiseDiagnostic(versionObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted new active diagnostic over full registry")
	}
	if obj.Health().DroppedDiagnostics != 1 {
		t.Fatalf("dropped diagnostics=%d, want 1", obj.Health().DroppedDiagnostics)
	}
	assertInactiveDiagnosticList(t, obj)
}

func TestDiagnosticEvictsOldestInactive(t *testing.T) {
	obj := newTestObj(t)
	obj.maxDiagnostics = 3

	firstObj := testBuildDiagnosticObj("core-lib", "v1.0.0")
	secondObj := testBuildDiagnosticObj("core-lib", "v1.0.1")
	thirdObj := testBuildDiagnosticObj("core-lib", "v1.0.2")
	fourthObj := testBuildDiagnosticObj("core-lib", "v1.0.3")
	fifthObj := testBuildDiagnosticObj("core-lib", "v1.0.4")
	for _, diagnosticObj := range []DiagnosticObj{firstObj, secondObj, thirdObj} {
		if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
			t.Fatalf("RaiseDiagnostic setup returned error: %v", err)
		}
	}
	if err := obj.ClearDiagnostic(testDiagnosticKeyObj(secondObj)); err != nil {
		t.Fatalf("ClearDiagnostic second returned error: %v", err)
	}
	if err := obj.ClearDiagnostic(testDiagnosticKeyObj(firstObj)); err != nil {
		t.Fatalf("ClearDiagnostic first returned error: %v", err)
	}

	if err := obj.RaiseDiagnostic(fourthObj); err != nil {
		t.Fatalf("RaiseDiagnostic fourth returned error: %v", err)
	}
	assertDiagnosticMapContains(t, obj, secondObj, false)
	assertDiagnosticMapContains(t, obj, firstObj, true)
	assertDiagnosticMapContains(t, obj, fourthObj, true)
	assertInactiveDiagnosticList(t, obj)

	if err := obj.RaiseDiagnostic(fifthObj); err != nil {
		t.Fatalf("RaiseDiagnostic fifth returned error: %v", err)
	}
	assertDiagnosticMapContains(t, obj, firstObj, false)
	assertDiagnosticMapContains(t, obj, thirdObj, true)
	assertDiagnosticMapContains(t, obj, fifthObj, true)
	assertInactiveDiagnosticList(t, obj)
}

func TestDiagnosticOverwriteReRaiseUnlinksInactive(t *testing.T) {
	obj := newTestObj(t)

	diagnosticObj := testBuildDiagnosticObj("core-lib", "v1.0.0")
	if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
		t.Fatalf("RaiseDiagnostic setup returned error: %v", err)
	}
	if err := obj.ClearDiagnostic(testDiagnosticKeyObj(diagnosticObj)); err != nil {
		t.Fatalf("ClearDiagnostic returned error: %v", err)
	}
	assertInactiveDiagnosticList(t, obj)

	diagnosticObj.Message = "build failed again"
	if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
		t.Fatalf("RaiseDiagnostic re-raise returned error: %v", err)
	}
	assertInactiveDiagnosticList(t, obj)

	diagnosticArr := obj.ActiveDiagnostics()
	if len(diagnosticArr) != 1 {
		t.Fatalf("active diagnostics=%d, want 1", len(diagnosticArr))
	}
	if diagnosticArr[0].Count != 2 {
		t.Fatalf("diagnostic count=%d, want 2", diagnosticArr[0].Count)
	}
	if diagnosticArr[0].Message != "build failed again" {
		t.Fatalf("diagnostic message=%q, want updated message", diagnosticArr[0].Message)
	}
}

func TestClearVersionDiagnosticsNoopDoesNotPublish(t *testing.T) {
	obj := newTestObj(t)

	beforeGeneration := obj.Generation()
	if err := obj.ClearVersionDiagnostics("core-lib", "v9.9.9"); err != nil {
		t.Fatalf("ClearVersionDiagnostics empty returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration {
		t.Fatalf("generation changed on empty clear-version: %d", obj.Generation())
	}

	diagnosticObj := testBuildDiagnosticObj("core-lib", "v1.0.0")
	if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
		t.Fatalf("RaiseDiagnostic setup returned error: %v", err)
	}
	beforeGeneration = obj.Generation()
	if err := obj.ClearVersionDiagnostics("core-lib", "v9.9.9"); err != nil {
		t.Fatalf("ClearVersionDiagnostics unmatched returned error: %v", err)
	}
	if obj.Generation() != beforeGeneration {
		t.Fatalf("generation changed on unmatched clear-version: %d", obj.Generation())
	}
	assertInactiveDiagnosticList(t, obj)
}

func TestSnapshotCopySemantics(t *testing.T) {
	obj := newTestObj(t)
	errObj := testUpstreamDiagnosticObj("core-lib")
	if err := obj.RaiseDiagnostic(errObj); err != nil {
		t.Fatalf("RaiseDiagnostic returned error: %v", err)
	}

	healthObj := obj.Health()
	healthObj.Reasons[0] = stcode.LogReasonBadRequest
	if obj.Health().Reasons[0] != stcode.LogReasonUpstreamUnavailable {
		t.Fatal("Health returned live reasons slice")
	}

	keyArr := obj.KeyStates()
	keyArr[0].Key = "mutated"
	nextKeyArr := obj.KeyStates()
	if nextKeyArr[0].Key == "mutated" {
		t.Fatal("KeyStates returned live key slice")
	}

	diagnosticArr := obj.ActiveDiagnostics()
	diagnosticArr[0].Message = "mutated"
	nextDiagnosticArr := obj.ActiveDiagnostics()
	if nextDiagnosticArr[0].Message == "mutated" {
		t.Fatal("ActiveDiagnostics returned live diagnostic slice")
	}

	snapshotObj := obj.Snapshot()
	snapshotKeyArr := snapshotObj.KeyStates()
	snapshotKeyArr[0].Key = "mutated"
	keyObj, ok := snapshotObj.KeyState("core-lib")
	if !ok || keyObj.Key != "core-lib" {
		t.Fatalf("snapshot key data was mutated: ok=%v key=%q", ok, keyObj.Key)
	}
}

func TestNewRejectsInvalidConfigValues(t *testing.T) {
	zeroCyclesObj := newTestConfigObj()
	zeroCyclesObj.UpstreamAvailability.PermanentAfterCycles = 0
	if _, err := New(zeroCyclesObj); err == nil {
		t.Fatal("New accepted zero permanent_after_cycles")
	}

	largeCyclesObj := newTestConfigObj()
	largeCyclesObj.UpstreamAvailability.PermanentAfterCycles = uint(^uint32(0)) + 1
	if _, err := New(largeCyclesObj); err == nil {
		t.Fatal("New accepted too large permanent_after_cycles")
	}

	emptyKeyObj := newTestConfigObj()
	emptyKeyObj.ReleaseMirrors[""] = "https://example.com/empty"
	if _, err := New(emptyKeyObj); err == nil {
		t.Fatal("New accepted empty release mirror key")
	}

	emptyURLObj := newTestConfigObj()
	emptyURLObj.ReleaseMirrors["bad-url"] = ""
	if _, err := New(emptyURLObj); err == nil {
		t.Fatal("New accepted empty release mirror URL")
	}

	tooManyObj := newTestConfigObj()
	tooManyObj.ReleaseMirrors = make(map[string]string, cMaxKeys+1)
	for i := 0; i <= cMaxKeys; i++ {
		key := "key-" + strings.Repeat("0", 5-len(strconv.Itoa(i))) + strconv.Itoa(i)
		tooManyObj.ReleaseMirrors[key] = "https://example.com/" + key
	}
	if _, err := New(tooManyObj); err == nil {
		t.Fatal("New accepted too many release mirrors")
	}
}

func TestCopiedObjRejectsMutation(t *testing.T) {
	obj := newTestObj(t)
	copiedObj := &Obj{selfObj: obj}

	if err := copiedObj.MarkUnavailable("core-lib", time.Now().UTC()); err == nil {
		t.Fatal("copied state object accepted mutation")
	}
	beforeGeneration := obj.Generation()
	copiedObj.SetContentChecksum(testHashObj("content"))
	if obj.Generation() != beforeGeneration {
		t.Fatal("copied state object mutated original through shared maps")
	}
	if copiedObj.Generation() != 0 {
		t.Fatal("copied state object returned non-zero snapshot")
	}
}

func TestNegativeAndAbuseInputs(t *testing.T) {
	obj := newTestObj(t)

	if _, err := New(nil); err == nil {
		t.Fatal("New accepted nil config")
	}
	if _, err := New(&stcfg.ConfigObj{}); err == nil {
		t.Fatal("New accepted empty release_mirrors")
	}
	if err := obj.SetClassification("core-lib", stcode.UndefSourceClass, "", ""); err == nil {
		t.Fatal("SetClassification accepted invalid class")
	}
	if err := obj.MarkAvailable("missing", true, time.Now()); err == nil {
		t.Fatal("MarkAvailable accepted unknown key")
	}
	scanAt := time.Unix(900, 0).UTC()
	if err := obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
		{Key: "core-lib", Available: true, ScanAt: scanAt},
		{Key: "core-lib", Available: true, ScanAt: scanAt},
	}); err == nil {
		t.Fatal("ApplyAvailabilityBatch accepted duplicate key")
	}
	if err := obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
		{Key: "core-lib", ScanAt: scanAt},
		{Key: "ui-kit", ScanAt: scanAt},
		{Key: "missing", ScanAt: scanAt},
	}); err == nil {
		t.Fatal("ApplyAvailabilityBatch accepted oversized batch")
	}
	if err := obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
		{Key: "ui-kit", Available: true, Present: true, ScanAt: scanAt},
		{Key: "missing", Available: true, Present: true, ScanAt: scanAt},
	}); err == nil {
		t.Fatal("ApplyAvailabilityBatch accepted unknown key")
	}
	keyObj, ok := obj.KeyState("ui-kit")
	if !ok {
		t.Fatal("ui-kit state not found")
	}
	if keyObj.Availability != stcode.AvailabilityStatusUnknown {
		t.Fatalf("availability batch partially applied before unknown-key error: %#v", keyObj)
	}
	if err := obj.MarkUnavailable("core-lib", time.Time{}); err == nil {
		t.Fatal("MarkUnavailable accepted empty scan timestamp")
	}
	if err := obj.MarkUnavailable("core-lib", time.Now().UTC().Add(cMaxScanFutureSkew+time.Minute)); err == nil {
		t.Fatal("MarkUnavailable accepted far-future scan timestamp")
	}

	invalidObj := testDiagnosticObj("bad_scope", stcode.UndefLogScope, stcode.OperationalStatusDegraded, stcode.LogReasonBadRequest, "", "", "bad")
	if err := obj.RaiseDiagnostic(invalidObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted invalid scope")
	}

	invalidSeverityObj := testUpstreamDiagnosticObj("core-lib")
	invalidSeverityObj.Impact = stcode.UndefOperationalStatus
	if err := obj.RaiseDiagnostic(invalidSeverityObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted invalid severity")
	}

	lowSeverityObj := testUpstreamDiagnosticObj("core-lib")
	lowSeverityObj.Impact = stcode.OperationalStatusOk
	if err := obj.RaiseDiagnostic(lowSeverityObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted below-threshold severity")
	}

	invalidReasonObj := testUpstreamDiagnosticObj("core-lib")
	invalidReasonObj.Reason = stcode.UndefLogReason
	if err := obj.RaiseDiagnostic(invalidReasonObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted invalid reason")
	}

	globalWithKeyObj := testGlobalDiagnosticObj()
	globalWithKeyObj.Key = "core-lib"
	if err := obj.RaiseDiagnostic(globalWithKeyObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted global diagnostic with key")
	}

	keyWithVersionObj := testUpstreamDiagnosticObj("core-lib")
	keyWithVersionObj.Version = "v1.0.0"
	if err := obj.RaiseDiagnostic(keyWithVersionObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted key diagnostic with version")
	}

	unknownKeyObj := testUpstreamDiagnosticObj("missing")
	if err := obj.RaiseDiagnostic(unknownKeyObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted unknown key")
	}

	longCodeObj := testUpstreamDiagnosticObj("core-lib")
	longCodeObj.Code = strings.Repeat("c", cMaxDiagnosticCodeBytes+1)
	if err := obj.RaiseDiagnostic(longCodeObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted oversized code")
	}

	longKeyObj := testUpstreamDiagnosticObj(strings.Repeat("k", cMaxKeyBytes+1))
	if err := obj.RaiseDiagnostic(longKeyObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted oversized key")
	}

	longVersion := strings.Repeat("v", cMaxVersionBytes+1)
	versionObj := testBuildDiagnosticObj("core-lib", longVersion)
	if err := obj.RaiseDiagnostic(versionObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted oversized version")
	}

	noCodeObj := testUpstreamDiagnosticObj("core-lib")
	noCodeObj.Code = ""
	if err := obj.RaiseDiagnostic(noCodeObj); err == nil {
		t.Fatal("RaiseDiagnostic accepted empty code")
	}

	longMessageObj := testDiagnosticObj("long_message", stcode.LogScopeGlobal, stcode.OperationalStatusDegraded, stcode.LogReasonCacheQuotaExceeded, "", "", strings.Repeat("界", 3000))
	if err := obj.RaiseDiagnostic(longMessageObj); err != nil {
		t.Fatalf("RaiseDiagnostic long message returned error: %v", err)
	}
	diagnosticArr := obj.ActiveDiagnostics()
	if len(diagnosticArr) != 1 {
		t.Fatalf("active diagnostics=%d, want 1", len(diagnosticArr))
	}
	if len(diagnosticArr[0].Message) > cMaxMessageBytes || !utf8.ValidString(diagnosticArr[0].Message) {
		t.Fatalf("message trim invalid: len=%d valid=%v", len(diagnosticArr[0].Message), utf8.ValidString(diagnosticArr[0].Message))
	}

	clearObj := testUpstreamDiagnosticObj("core-lib")
	if err := obj.RaiseDiagnostic(clearObj); err != nil {
		t.Fatalf("RaiseDiagnostic clear target returned error: %v", err)
	}
	if err := obj.ClearDiagnostic(testDiagnosticKeyObj(clearObj)); err != nil {
		t.Fatalf("ClearDiagnostic returned error: %v", err)
	}
	if err := obj.ClearDiagnostic(DiagnosticKeyObj{Code: "", Scope: stcode.LogScopeKey, Key: "core-lib"}); err == nil {
		t.Fatal("ClearDiagnostic accepted empty code")
	}
	if err := obj.ClearDiagnostic(DiagnosticKeyObj{Code: "missing", Scope: stcode.LogScopeKey, Key: "missing"}); err == nil {
		t.Fatal("ClearDiagnostic accepted unknown key")
	}
	for _, itemObj := range obj.ActiveDiagnostics() {
		if itemObj.Key == "core-lib" {
			t.Fatalf("ClearDiagnostic left key diagnostic active: %#v", itemObj)
		}
	}
}

func TestConcurrentReadWrite(t *testing.T) {
	obj := newTestObj(t)

	var wgObj sync.WaitGroup
	stopChan := make(chan struct{})

	for i := 0; i < 8; i++ {
		wgObj.Add(1)
		go func() {
			defer wgObj.Done()
			for {
				select {
				case <-stopChan:
					return
				default:
					_ = obj.Health()
					_ = obj.Checksums()
					_ = obj.KeyStates()
					_, _ = obj.KeyState("core-lib")
					_ = obj.ActiveDiagnostics()
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		if err := obj.MarkUnavailable("core-lib", time.Unix(int64(i), 0).UTC()); err != nil {
			t.Fatalf("MarkUnavailable returned error: %v", err)
		}
		if err := obj.MarkAvailable("core-lib", true, time.Unix(int64(i), 0).UTC()); err != nil {
			t.Fatalf("MarkAvailable returned error: %v", err)
		}
		obj.SetContentChecksum(testHashObj("content"))
		errObj := testUpstreamDiagnosticObj("core-lib")
		if err := obj.RaiseDiagnostic(errObj); err != nil {
			t.Fatalf("RaiseDiagnostic returned error: %v", err)
		}
		if err := obj.ClearKeyDiagnostics("core-lib"); err != nil {
			t.Fatalf("ClearKeyDiagnostics returned error: %v", err)
		}
	}

	close(stopChan)
	wgObj.Wait()
}

// TestRaiseDiagnosticRecurringReordersRecentWindow guards the invariant that a recurring diagnostic
// (the common bump path) is republished in LastSeen-desc order, so the capped "recent" window and the
// recent_error metric keep it visible after the bump rather than leaving it in its old position.
func TestRaiseDiagnosticRecurringReordersRecentWindow(t *testing.T) {
	obj := newTestObj(t)

	if err := obj.RaiseDiagnostic(testUpstreamDiagnosticObj("core-lib")); err != nil {
		t.Fatalf("raise core-lib: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := obj.RaiseDiagnostic(testUpstreamDiagnosticObj("ui-kit")); err != nil {
		t.Fatalf("raise ui-kit: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	if err := obj.RaiseDiagnostic(testUpstreamDiagnosticObj("core-lib")); err != nil {
		t.Fatalf("re-raise core-lib: %v", err)
	}

	recentArr := obj.Snapshot().RecentDiagnostics(1)
	if len(recentArr) != 1 {
		t.Fatalf("RecentDiagnostics(1) length = %d, want 1", len(recentArr))
	}
	if recentArr[0].Key != "core-lib" {
		t.Fatalf("recent[0].Key = %q, want core-lib: recurring bump must reorder the window", recentArr[0].Key)
	}
	if recentArr[0].Count < 2 {
		t.Fatalf("recent[0].Count = %d, want >= 2 after repeat", recentArr[0].Count)
	}
}

// TestUnavailableCyclesFreezeAtPermanent checks that once a key reaches permanent-down, further unavailable
// scans stop growing the counter (frozen at permanentAt), so a dead mirror does not churn a changed count
// under an unchanged status every cycle. Test config sets PermanentAfterCycles=2.
func TestUnavailableCyclesFreezeAtPermanent(t *testing.T) {
	obj := newTestObj(t)
	base := time.Now().UTC()
	for i := 0; i < 5; i++ {
		if err := obj.MarkUnavailable("core-lib", base.Add(time.Duration(i)*time.Minute)); err != nil {
			t.Fatalf("MarkUnavailable #%d: %v", i, err)
		}
	}

	keyStateObj, ok := obj.KeyState("core-lib")
	if !ok {
		t.Fatal("KeyState missing for core-lib")
	}
	if keyStateObj.Availability != stcode.AvailabilityStatusPermanentDown {
		t.Fatalf("availability=%v, want PermanentDown", keyStateObj.Availability)
	}
	if keyStateObj.UnavailableCycles != 2 {
		t.Fatalf("UnavailableCycles=%d, want 2 (frozen at permanentAt)", keyStateObj.UnavailableCycles)
	}
}
