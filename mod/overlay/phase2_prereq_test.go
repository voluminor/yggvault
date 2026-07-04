package overlay

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func TestRenderGoInfo(t *testing.T) {
	ts := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	dataArr, err := RenderGoInfo("v1.2.3", ts)
	if err != nil {
		t.Fatalf("RenderGoInfo: %v", err)
	}
	var parsedObj struct {
		Version string
		Time    time.Time
	}
	if err := json.Unmarshal(dataArr, &parsedObj); err != nil {
		t.Fatalf("info json unparseable: %s (%v)", dataArr, err)
	}
	if parsedObj.Version != "v1.2.3" || !parsedObj.Time.Equal(ts) {
		t.Fatalf("unexpected info: %s", dataArr)
	}
}

func TestCandidateFromDetection(t *testing.T) {
	goRes := DetectionResultObj{Go: &CandidateObj{Ecosystem: stcode.EcosystemGo, GoModulePath: "example.com/foo"}}
	goRes.Detection.IsGo = true
	goRes.Detection.EvidenceJSON = buildEvidence(goRes)
	goC, composerC := CandidateFromDetection(goRes.Detection)
	if goC == nil || goC.GoModulePath != "example.com/foo" || composerC != nil {
		t.Fatalf("go-only: go=%+v composer=%+v", goC, composerC)
	}

	cRes := DetectionResultObj{Composer: &CandidateObj{Ecosystem: stcode.EcosystemComposer, ComposerName: "vendor/pkg"}}
	cRes.Detection.IsComposer = true
	cRes.Detection.EvidenceJSON = buildEvidence(cRes)
	goC2, composerC2 := CandidateFromDetection(cRes.Detection)
	if composerC2 == nil || composerC2.ComposerName != "vendor/pkg" || goC2 != nil {
		t.Fatalf("composer-only: go=%+v composer=%+v", goC2, composerC2)
	}

	stale := core.DetectionObj{EvidenceJSON: `{"go_module_path":"x","composer_name":"y/z"}`}
	if g, c := CandidateFromDetection(stale); g != nil || c != nil {
		t.Fatalf("flags off must yield nil: go=%+v composer=%+v", g, c)
	}
}
