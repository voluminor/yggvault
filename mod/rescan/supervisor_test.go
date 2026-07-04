package rescan

import (
	"context"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// panicSourceObj triggers a panic inside a per-key worker: the process must survive a key failure.
type panicSourceObj struct {
	fakeSourceObj
}

func (f *panicSourceObj) Releases(_ context.Context, _ string, _ uint) ([]source.GitReleaseObj, bool, error) {
	panic("boom in key worker")
}

func TestRunOnceSurvivesKeyWorkerPanic(t *testing.T) {
	configObj := stconf.FullConfig()
	configObj.Rescan.Interval = time.Hour
	configObj.ReleaseMirrors = map[string]string{"core-module": "https://example.org/core-module/"}

	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	obj := New(configObj, &panicSourceObj{}, nil, nil, stateObj, nil, "")
	obj.RunOnce(context.Background())

	diagArr := stateObj.Snapshot().RecentDiagnostics(10)
	found := false
	for i := range diagArr {
		if diagArr[i].Code == "rescan_panic" {
			found = true
		}
	}
	if !found {
		t.Fatalf("rescan_panic diagnostic not raised, got %d diagnostics", len(diagArr))
	}
}

func TestSupervisorLifecycle(t *testing.T) {
	configObj := stconf.FullConfig()
	configObj.Rescan.Interval = time.Hour
	configObj.ReleaseMirrors = map[string]string{"core-module": "https://example.org/core-module/"}

	stateObj, err := state.New(configObj)
	if err != nil {
		t.Fatalf("state.New: %v", err)
	}
	obj := New(configObj, &fakeSourceObj{}, nil, nil, stateObj, nil, "")
	obj.RunOnce(context.Background())

	obj.Start()
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := obj.Close(closeCtx); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := obj.Close(closeCtx); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSupervisorListenerSet(t *testing.T) {
	configObj := stconf.FullConfig()
	configObj.Web.Server.Domain = "mirror.example"

	obj := New(configObj, nil, nil, nil, nil, nil, "abc.pk.ygg")
	if len(obj.listenerArr) != 2 {
		t.Fatalf("listeners=%d want 2 (web+ygg)", len(obj.listenerArr))
	}
	if obj.listenerArr[0].EntryHost != "mirror.example" || obj.listenerArr[1].EntryHost != "abc.pk.ygg" {
		t.Fatalf("unexpected listener hosts: %+v", obj.listenerArr)
	}

	objNoYgg := New(configObj, nil, nil, nil, nil, nil, "")
	if len(objNoYgg.listenerArr) != 1 {
		t.Fatalf("listeners=%d want 1 (web only)", len(objNoYgg.listenerArr))
	}
}
