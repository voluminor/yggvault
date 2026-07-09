package state

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func TestDiagnosticRegistryStress(t *testing.T) {
	obj := newTestObj(t)
	obj.maxDiagnostics = 64

	var readerWgObj sync.WaitGroup
	var writerWgObj sync.WaitGroup
	stopChan := make(chan struct{})
	errChan := make(chan error, 1)

	reportErr := func(err error) {
		select {
		case errChan <- err:
		default:
		}
	}

	for i := 0; i < 8; i++ {
		readerID := i
		readerWgObj.Add(1)
		go func() {
			defer readerWgObj.Done()
			var lastGeneration uint64
			for {
				select {
				case <-stopChan:
					return
				default:
				}

				snapshotObj := obj.Snapshot()
				if snapshotObj.Generation < lastGeneration {
					reportErr(fmt.Errorf("reader %d observed generation rollback: %d -> %d", readerID, lastGeneration, snapshotObj.Generation))
					return
				}
				lastGeneration = snapshotObj.Generation
				if snapshotObj.Health().DiagnosticsCount != uint64(len(snapshotObj.ActiveDiagnostics())) {
					reportErr(fmt.Errorf("reader %d observed inconsistent diagnostics count", readerID))
					return
				}
				keyArr := snapshotObj.KeyStates()
				if len(keyArr) != 2 || keyArr[0].Key > keyArr[1].Key {
					reportErr(fmt.Errorf("reader %d observed invalid key ordering", readerID))
					return
				}
				if _, ok := snapshotObj.KeyState("core-lib"); !ok {
					reportErr(fmt.Errorf("reader %d lost core-lib state", readerID))
					return
				}
				_ = snapshotObj.RecentDiagnostics(4)
			}
		}()
	}

	for i := 0; i < 4; i++ {
		writerID := i
		writerWgObj.Add(1)
		go func() {
			defer writerWgObj.Done()
			for step := 0; step < 200; step++ {
				version := fmt.Sprintf("v%d.%d", writerID, step%96)
				diagnosticObj := testDiagnosticObj(
					fmt.Sprintf("stress_%d_%d", writerID, step%96),
					stcode.LogScopeVersion,
					stcode.OperationalStatusError,
					stcode.LogReasonGoOverlayDegraded,
					"core-lib",
					version,
					"stress diagnostic",
				)
				if err := obj.RaiseDiagnostic(diagnosticObj); err != nil && err.Error() != "diagnostic registry is full" {
					reportErr(fmt.Errorf("RaiseDiagnostic returned error: %w", err))
					return
				}
				if step%3 == 0 {
					if err := obj.ClearVersionDiagnostics("core-lib", version); err != nil {
						reportErr(fmt.Errorf("ClearVersionDiagnostics returned error: %w", err))
						return
					}
				}
				if step%5 == 0 {
					if err := obj.ClearDiagnostic(testDiagnosticKeyObj(diagnosticObj)); err != nil {
						reportErr(fmt.Errorf("ClearDiagnostic returned error: %w", err))
						return
					}
				}
				if step%7 == 0 {
					if err := obj.ClearKeyDiagnostics("core-lib"); err != nil {
						reportErr(fmt.Errorf("ClearKeyDiagnostics returned error: %w", err))
						return
					}
				}
			}
		}()
	}

	writerWgObj.Add(1)
	go func() {
		defer writerWgObj.Done()
		for step := 0; step < 200; step++ {
			scanAt := time.Unix(int64(1000+step), 0).UTC()
			if step%2 == 0 {
				if err := obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
					{Key: "core-lib", Available: true, Present: true, ScanAt: scanAt},
					{Key: "ui-kit", Available: true, Present: true, ScanAt: scanAt},
				}); err != nil {
					reportErr(fmt.Errorf("ApplyAvailabilityBatch returned error: %w", err))
					return
				}
			} else if err := obj.MarkUnavailable("core-lib", scanAt); err != nil {
				reportErr(fmt.Errorf("MarkUnavailable returned error: %w", err))
				return
			}
			if err := obj.SetMirrorStats(MirrorStatsObj{
				Key:           "core-lib",
				LatestVersion: fmt.Sprintf("v1.%d.0", step),
				VersionCount:  uint64(step + 1),
				LastPublishTS: scanAt,
			}); err != nil {
				reportErr(fmt.Errorf("SetMirrorStats returned error: %w", err))
				return
			}
			obj.SetContentChecksum(testHashObj(fmt.Sprintf("content-%d", step)))
		}
	}()

	writerWgObj.Wait()
	close(stopChan)
	readerWgObj.Wait()

	select {
	case err := <-errChan:
		t.Fatal(err)
	default:
	}
	assertInactiveDiagnosticList(t, obj)
}
