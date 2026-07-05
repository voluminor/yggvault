package overlay

import (
	"testing"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func planTestOverlay(t *testing.T, rewriteEnabled bool) *Obj {
	t.Helper()
	configObj := stconf.FullConfig()
	configObj.Overlay.Go.RewriteEnabled = rewriteEnabled
	configObj.Web.Server.Domain = "mirror.example"
	configObj.Web.Routing.Prefix = ""
	obj, err := New(configObj)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return obj
}

func planKindSet(planArr []ArtifactPlanObj) map[string]int {
	out := make(map[string]int, len(planArr))
	for _, planObj := range planArr {
		out[planObj.MaterializerID.String()+"/"+planObj.ArtifactKind.String()+"/"+planObj.ListenerID.String()]++
	}
	return out
}

func TestArtifactPlanComposerGlobal(t *testing.T) {
	obj := planTestOverlay(t, true)
	detectionObj := core.DetectionObj{IsComposer: true}
	candidateObj := &CandidateObj{Ecosystem: stcode.EcosystemComposer, ComposerName: "vendor/pkg"}
	listenerArr := []ListenerCtxObj{{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"}}

	planArr := obj.ArtifactPlan(nil, "vendor-pkg", "v1.0.0", core.HashObj{}, detectionObj, candidateObj, nil, listenerArr)
	if len(planArr) != 2 {
		t.Fatalf("plan=%d want 2 (universal global, no go-zip): %+v", len(planArr), planArr)
	}
	kinds := planKindSet(planArr)
	if kinds["universal/zip/global"] != 1 || kinds["universal/tar.gz/global"] != 1 {
		t.Fatalf("unexpected plan kinds: %v", kinds)
	}
}

func TestArtifactPlanGoRewriteUniversalGlobal(t *testing.T) {
	obj := planTestOverlay(t, true)
	detectionObj := core.DetectionObj{IsGo: true}
	candidateObj := &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "upstream.example/foo"}
	listenerArr := []ListenerCtxObj{
		{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"},
		{ListenerID: stcode.ListenerYgg, EntryHost: "abc.pk.ygg"},
	}

	planArr := obj.ArtifactPlan(nil, "foo", "v1.0.0", core.HashObj{}, detectionObj, candidateObj, nil, listenerArr)
	if len(planArr) != 4 {
		t.Fatalf("plan=%d want 4 (universal global x2 formats + go-zip per-listener x2): %+v", len(planArr), planArr)
	}
	kinds := planKindSet(planArr)
	for _, want := range []string{
		"universal/zip/global", "universal/tar.gz/global",
		"go/zip/web", "go/zip/ygg",
	} {
		if kinds[want] != 1 {
			t.Fatalf("missing %q in plan: %v", want, kinds)
		}
	}
	for _, planObj := range planArr {
		if planObj.Builder == nil {
			t.Fatalf("nil builder in plan entry: %+v", planObj)
		}
		if planObj.MaterializerID == stcode.MaterializerGo && planObj.FormatVersion != GoZipFormatVersion {
			t.Fatalf("bad go-zip format version: %+v", planObj)
		}
	}
}

func TestArtifactPlanGoRewriteDisabledGlobalNoGoZip(t *testing.T) {
	obj := planTestOverlay(t, false)
	detectionObj := core.DetectionObj{IsGo: true}
	candidateObj := &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "upstream.example/foo"}
	listenerArr := []ListenerCtxObj{{ListenerID: stcode.ListenerWeb, EntryHost: "mirror.example"}}

	planArr := obj.ArtifactPlan(nil, "foo", "v1.0.0", core.HashObj{}, detectionObj, candidateObj, nil, listenerArr)
	if len(planArr) != 2 {
		t.Fatalf("plan=%d want 2 (raw universal global, no go-zip when rewrite off): %+v", len(planArr), planArr)
	}
	kinds := planKindSet(planArr)
	if kinds["universal/zip/global"] != 1 || kinds["universal/tar.gz/global"] != 1 {
		t.Fatalf("unexpected plan kinds: %v", kinds)
	}
}
