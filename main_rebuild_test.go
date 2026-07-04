package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/voluminor/yggvault/mod/config"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/storage"
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

	listenerArr := listenerContextsFromConfig(cfgObj, "")
	planCount := materializeFromStorage(t, ctx, storeObj, overlayObj, listenerArr, key, version)

	driftArr, err := selfTestFormats(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("selfTestFormats: %v", err)
	}
	if len(driftArr) != 0 {
		t.Fatalf("unexpected drift on fresh store: %+v", driftArr)
	}

	resultObj, err := rebuildAllArtifacts(ctx, storeObj, overlayObj, listenerArr)
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

	repairObj, err := rebuildAllArtifacts(ctx, storeObj, overlayObj, listenerArr)
	if err != nil {
		t.Fatalf("rebuild after corruption: %v", err)
	}
	if repairObj.Drift < 1 || repairObj.Updated < 1 {
		t.Fatalf("rebuild must fix corruption: %+v", repairObj)
	}

	driftArr2, err := selfTestFormats(ctx, storeObj, overlayObj, listenerArr)
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
