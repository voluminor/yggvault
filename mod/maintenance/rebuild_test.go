package maintenance

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/voluminor/yggvault/mod/config"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cTestGoMod = "module example.com/foo\n\ngo 1.21\n"
	cTestGoSrc = "package foo\n\n// Hi returns a greeting.\nfunc Hi() string { return \"hi\" }\n"
)

// //

func newTestConfig(t *testing.T) *stconf.ConfigObj {
	t.Helper()
	// macOS: t.TempDir lives under the /var symlink — resolve it, otherwise storage.New rejects the symlinked path component
	rootDir, evalErr := filepath.EvalSymlinks(t.TempDir())
	if evalErr != nil {
		t.Fatalf("EvalSymlinks returned error: %v", evalErr)
	}
	storeDir := filepath.Join(rootDir, "store")
	cfgTemplateObj := stconf.FullConfig()
	cfgTemplateObj.Logging.Console.Enabled = true
	cfgTemplateObj.Logging.Console.Level = stconf.ConsoleLogLevelInfo
	cfgTemplateObj.Web.Server.Domain = "localhost"
	cfgTemplateObj.Web.Server.Mode = stconf.WebServerModeSingle
	cfgTemplateObj.Web.Server.Single.Proto = stconf.WebProtoHttp
	cfgTemplateObj.Web.Server.Single.Listen = "127.0.0.1:18099"
	cfgTemplateObj.Ygg.PemKey = ""
	cfgTemplateObj.Storage.Dir = storeDir
	cfgTemplateObj.ReleaseMirrors = map[string]string{
		"example-module": "https://example.org/m/",
	}
	cfgDataArr, err := cfgTemplateObj.RenderYAML(false)
	if err != nil {
		t.Fatalf("RenderYAML returned error: %v", err)
	}
	cfgPath := filepath.Join(rootDir, "config.yml")
	if err := os.WriteFile(cfgPath, cfgDataArr, 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	cfgObj, err := config.New(cfgPath)
	if err != nil {
		t.Fatalf("config.New: %v", err)
	}
	return cfgObj
}

func materializeFromStorage(t *testing.T, ctx context.Context, storeObj *storage.Obj, overlayObj *overlay.Obj, listenerArr []overlay.ListenerCtxObj, key string, version string) int {
	t.Helper()
	planArr, ok, err := artifactPlanForVersion(ctx, storeObj, overlayObj, listenerArr, key, version)
	if err != nil || !ok {
		t.Fatalf("plan: ok=%v err=%v", ok, err)
	}
	if len(planArr) == 0 {
		t.Fatal("artifact plan is empty")
	}
	for i := range planArr {
		digestObj, digestErr := storeObj.ArtifactDigest(ctx, planArr[i].Builder)
		if digestErr != nil {
			t.Fatalf("ArtifactDigest: %v", digestErr)
		}
		if err = storeObj.RegisterArtifact(ctx, artifactFromPlan(planArr[i], key, version, digestObj)); err != nil {
			t.Fatalf("RegisterArtifact: %v", err)
		}
	}
	return len(planArr)
}

func artifactKeyOf(artObj core.ArtifactObj) core.ArtifactKeyObj {
	return core.ArtifactKeyObj{
		MaterializerID: artObj.MaterializerID,
		ArtifactKind:   artObj.ArtifactKind,
		ListenerID:     artObj.ListenerID,
		Key:            artObj.Key,
		Version:        artObj.Version,
	}
}

// // // // // // // // // //

func TestRebuildAndSelfTest(t *testing.T) {
	ctx := context.Background()
	cfgObj := newTestConfig(t)

	storeObj, err := storage.New(ctx, cfgObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer func() { _ = storeObj.Close(context.Background()) }()

	overlayObj, err := overlay.New(cfgObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}

	const key = "example-module"
	const version = "v1.0.0"

	if _, err = storeObj.Publish(ctx, core.PublishObj{
		Key:             key,
		Version:         version,
		SourceHash:      core.HashBytes([]byte("src")),
		SourceSizeBytes: 1,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte(cTestGoMod)},
			{Path: "foo.go", Mode: core.ModeFile, Content: []byte(cTestGoSrc)},
		},
		Detection: core.DetectionObj{
			IsGo:         true,
			EvidenceJSON: `{"go_module_path":"example.com/foo"}`,
		},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	listenerArr := ListenersFromConfig(cfgObj, "")
	planCount := materializeFromStorage(t, ctx, storeObj, overlayObj, listenerArr, key, version)

	driftArr, err := SelfTestFormats(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("selfTestFormats: %v", err)
	}
	if len(driftArr) != 0 {
		t.Fatalf("unexpected drift on fresh store: %+v", driftArr)
	}

	resultObj, err := RebuildArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("rebuildAllArtifacts: %v", err)
	}
	if resultObj.Scanned != uint64(planCount) || resultObj.Drift != 0 || resultObj.Updated != 0 {
		t.Fatalf("fresh rebuild should be clean: %+v (planCount=%d)", resultObj, planCount)
	}

	keyArr, err := storeObj.DistinctKeys(ctx, "", 100)
	if err != nil || len(keyArr) != 1 || keyArr[0] != key {
		t.Fatalf("DistinctKeys: keys=%v err=%v", keyArr, err)
	}

	storedArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil || len(storedArr) == 0 {
		t.Fatalf("ListArtifacts: len=%d err=%v", len(storedArr), err)
	}
	corruptObj := storedArr[0]
	corruptObj.BodyHash = core.HashBytes([]byte("WRONG"))
	corruptObj.SizeBytes = 999999
	if err = storeObj.RegisterArtifact(ctx, corruptObj); err != nil {
		t.Fatalf("corrupt RegisterArtifact: %v", err)
	}

	repairObj, err := RebuildArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("rebuild after corruption: %v", err)
	}
	if repairObj.Drift < 1 || repairObj.Updated < 1 {
		t.Fatalf("rebuild must fix corruption: %+v", repairObj)
	}

	driftArr2, err := SelfTestFormats(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("selfTestFormats post-repair: %v", err)
	}
	if len(driftArr2) != 0 {
		t.Fatalf("drift remains after rebuild: %+v", driftArr2)
	}

	versionCount, _, err := storeObj.KeyDeletionEstimate(ctx, key)
	if err != nil || versionCount != 1 {
		t.Fatalf("KeyDeletionEstimate: v=%d err=%v", versionCount, err)
	}
	if _, err = storeObj.Vacuum(ctx); err != nil {
		t.Fatalf("Vacuum: %v", err)
	}
	deletedVersions, _, err := storeObj.DeleteKey(ctx, key)
	if err != nil || deletedVersions != 1 {
		t.Fatalf("DeleteKey: v=%d err=%v", deletedVersions, err)
	}
	leftArr, err := storeObj.DistinctKeys(ctx, "", 100)
	if err != nil || len(leftArr) != 0 {
		t.Fatalf("after delete keys=%v err=%v", leftArr, err)
	}
}

// TestGlobalsRoundTrip covers the durable globals key/value accessors that persist the host-identity fingerprint.
func TestGlobalsRoundTrip(t *testing.T) {
	ctx := context.Background()
	cfgObj := newTestConfig(t)
	storeObj, err := storage.New(ctx, cfgObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer func() { _ = storeObj.Close(context.Background()) }()

	if _, ok, gErr := storeObj.GetGlobal(ctx, cHostIdentityGlobalKey); gErr != nil || ok {
		t.Fatalf("expected absent key: ok=%v err=%v", ok, gErr)
	}
	if err = storeObj.SetGlobal(ctx, cHostIdentityGlobalKey, "abc"); err != nil {
		t.Fatalf("SetGlobal: %v", err)
	}
	valueText, ok, err := storeObj.GetGlobal(ctx, cHostIdentityGlobalKey)
	if err != nil || !ok || valueText != "abc" {
		t.Fatalf("GetGlobal: value=%q ok=%v err=%v", valueText, ok, err)
	}
	if err = storeObj.SetGlobal(ctx, cHostIdentityGlobalKey, "xyz"); err != nil {
		t.Fatalf("SetGlobal upsert: %v", err)
	}
	if valueText, _, _ = storeObj.GetGlobal(ctx, cHostIdentityGlobalKey); valueText != "xyz" {
		t.Fatalf("upsert value: %q", valueText)
	}
}

// TestUpgradeReconcilePrunesLegacyUniversalAndCreatesMissing simulates an upgraded store: a legacy per-listener
// universal row (the pre-collapse shape) is pruned, a deleted planned row is re-created, and no stale bytes survive.
func TestUpgradeReconcilePrunesLegacyUniversalAndCreatesMissing(t *testing.T) {
	ctx := context.Background()
	cfgObj := newTestConfig(t)
	storeObj, err := storage.New(ctx, cfgObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer func() { _ = storeObj.Close(context.Background()) }()

	overlayObj, err := overlay.New(cfgObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}

	const key = "example-module"
	const version = "v1.0.0"
	if _, err = storeObj.Publish(ctx, core.PublishObj{
		Key:             key,
		Version:         version,
		SourceHash:      core.HashBytes([]byte("src")),
		SourceSizeBytes: 1,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte(cTestGoMod)},
			{Path: "foo.go", Mode: core.ModeFile, Content: []byte(cTestGoSrc)},
		},
		Detection: core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_module_path":"example.com/foo"}`},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	listenerArr := ListenersFromConfig(cfgObj, "")
	materializeFromStorage(t, ctx, storeObj, overlayObj, listenerArr, key, version)

	storedArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	globalID := stcode.ListenerGlobal.String()
	var zipGlobal, targzGlobal core.ArtifactObj
	for i := range storedArr {
		if storedArr[i].ListenerID != globalID {
			t.Fatalf("post-collapse universal must be global, got %+v", storedArr[i])
		}
		if storedArr[i].ArtifactKind == "zip" {
			zipGlobal = storedArr[i]
		} else {
			targzGlobal = storedArr[i]
		}
	}
	if zipGlobal.Key == "" || targzGlobal.Key == "" {
		t.Fatalf("expected global zip and tar.gz universal rows, got %+v", storedArr)
	}

	legacy := zipGlobal
	legacy.ListenerID = stcode.ListenerWeb.String()
	legacy.FormatVersion = 1
	legacy.BodyHash = core.HashBytes([]byte("OLD-REWRITTEN"))
	legacy.SizeBytes = 999
	if err = storeObj.RegisterArtifact(ctx, legacy); err != nil {
		t.Fatalf("register legacy row: %v", err)
	}
	// Legacy global go-zip row: never planned (go-zip is per-listener) and unreachable after the
	// exact-listener LocateKey change, so reconcile must prune it.
	legacyGoGlobal := zipGlobal
	legacyGoGlobal.MaterializerID = stcode.MaterializerGo.String()
	legacyGoGlobal.FormatVersion = 1
	legacyGoGlobal.BodyHash = core.HashBytes([]byte("OLD-GO-GLOBAL"))
	legacyGoGlobal.SizeBytes = 555
	if err = storeObj.RegisterArtifact(ctx, legacyGoGlobal); err != nil {
		t.Fatalf("register legacy global go-zip row: %v", err)
	}
	if err = storeObj.DeleteArtifact(ctx, artifactKeyOf(targzGlobal)); err != nil {
		t.Fatalf("delete planned row: %v", err)
	}

	resultObj, err := RebuildArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("rebuildAllArtifacts: %v", err)
	}
	if resultObj.Pruned < 2 {
		t.Fatalf("legacy per-listener universal and global go-zip rows must be pruned: %+v", resultObj)
	}
	if resultObj.Created < 1 {
		t.Fatalf("deleted planned row must be re-created: %+v", resultObj)
	}

	if _, ok, gErr := storeObj.GetArtifact(ctx, artifactKeyOf(legacy)); gErr != nil || ok {
		t.Fatalf("legacy per-listener universal row must be gone: ok=%v err=%v", ok, gErr)
	}
	if _, ok, gErr := storeObj.GetArtifact(ctx, artifactKeyOf(legacyGoGlobal)); gErr != nil || ok {
		t.Fatalf("legacy global go-zip row must be gone: ok=%v err=%v", ok, gErr)
	}
	gotZip, ok, err := storeObj.GetArtifact(ctx, artifactKeyOf(zipGlobal))
	if err != nil || !ok || gotZip.BodyHash != zipGlobal.BodyHash {
		t.Fatalf("global zip must keep the raw digest: ok=%v err=%v hashMatch=%v", ok, err, gotZip.BodyHash == zipGlobal.BodyHash)
	}
	gotTargz, ok, err := storeObj.GetArtifact(ctx, artifactKeyOf(targzGlobal))
	if err != nil || !ok || gotTargz.BodyHash != targzGlobal.BodyHash {
		t.Fatalf("global tar.gz must be re-created with the raw digest: ok=%v err=%v hashMatch=%v", ok, err, gotTargz.BodyHash == targzGlobal.BodyHash)
	}

	driftArr, err := SelfTestFormats(ctx, storeObj, overlayObj, listenerArr)
	if err != nil || len(driftArr) != 0 {
		t.Fatalf("post-reconcile drift: len=%d err=%v", len(driftArr), err)
	}
}

func TestRebuildCreatesCompletelyMissingArtifactSet(t *testing.T) {
	ctx := context.Background()
	cfgObj := newTestConfig(t)
	storeObj, err := storage.New(ctx, cfgObj)
	if err != nil {
		t.Fatalf("storage.New: %v", err)
	}
	defer func() { _ = storeObj.Close(context.Background()) }()

	overlayObj, err := overlay.New(cfgObj)
	if err != nil {
		t.Fatalf("overlay.New: %v", err)
	}

	const key = "example-module"
	const version = "v1.0.0"
	if _, err = storeObj.Publish(ctx, core.PublishObj{
		Key:             key,
		Version:         version,
		SourceHash:      core.HashBytes([]byte("src")),
		SourceSizeBytes: 1,
		Entries: []core.InputEntryObj{
			{Path: "go.mod", Mode: core.ModeFile, Content: []byte(cTestGoMod)},
			{Path: "foo.go", Mode: core.ModeFile, Content: []byte(cTestGoSrc)},
		},
		Detection: core.DetectionObj{IsGo: true, EvidenceJSON: `{"go_module_path":"example.com/foo"}`},
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	listenerArr := ListenersFromConfig(cfgObj, "")
	planCount := materializeFromStorage(t, ctx, storeObj, overlayObj, listenerArr, key, version)
	storedArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil {
		t.Fatalf("ListArtifacts: %v", err)
	}
	for i := range storedArr {
		if err = storeObj.DeleteArtifact(ctx, artifactKeyOf(storedArr[i])); err != nil {
			t.Fatalf("DeleteArtifact %d: %v", i, err)
		}
	}

	resultObj, err := RebuildArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("rebuildAllArtifacts: %v", err)
	}
	if resultObj.Created != uint64(planCount) {
		t.Fatalf("rebuild should recreate the full missing plan: created=%d plan=%d result=%+v", resultObj.Created, planCount, resultObj)
	}
	rebuiltArr, err := storeObj.ListArtifacts(ctx, key, version)
	if err != nil || len(rebuiltArr) != planCount {
		t.Fatalf("rebuilt artifacts: len=%d want=%d err=%v", len(rebuiltArr), planCount, err)
	}
}
