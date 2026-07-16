package state

import (
	"encoding/binary"
	"fmt"
	"testing"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func newBenchmarkConfigObj(keyCount int) *stcfg.ConfigObj {
	mirrorMap := make(map[string]string, keyCount)
	for i := 0; i < keyCount; i++ {
		key := fmt.Sprintf("key-%04d", i)
		mirrorMap[key] = "https://example.com/" + key
	}
	return &stcfg.ConfigObj{
		ReleaseMirrors: mirrorMap,
		UpstreamAvailability: stcfg.UpstreamAvailabilityObj{
			PermanentAfterCycles: 8,
		},
	}
}

func newBenchmarkObj(b *testing.B, keyCount int) *Obj {
	b.Helper()

	obj, err := New(newBenchmarkConfigObj(keyCount))
	if err != nil {
		b.Fatalf("New returned error: %v", err)
	}
	return obj
}

func benchmarkHashObj(text string) core.HashObj {
	return core.HashBytes([]byte(text))
}

func benchmarkChangingHashObj(index int) core.HashObj {
	var hashObj core.HashObj
	binary.LittleEndian.PutUint64(hashObj[:8], uint64(index))
	return hashObj
}

func benchmarkUpstreamDiagnosticObj(key string) DiagnosticObj {
	return DiagnosticObj{
		Code:    "upstream_unavailable",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonUpstreamUnavailable,
		Key:     key,
		Message: "down",
	}
}

func benchmarkBuildDiagnosticObj(key string, version string) DiagnosticObj {
	return DiagnosticObj{
		Code:    "go_overlay_build_failed",
		Scope:   stcode.LogScopeVersion,
		Impact:  stcode.OperationalStatusError,
		Reason:  stcode.LogReasonGoOverlayDegraded,
		Key:     key,
		Version: version,
		Message: "build failed",
	}
}

func benchmarkAvailabilityArr(keyCount int, scanAt time.Time) []AvailabilityUpdateObj {
	updateArr := make([]AvailabilityUpdateObj, 0, keyCount)
	for i := 0; i < keyCount; i++ {
		updateArr = append(updateArr, AvailabilityUpdateObj{
			Key:       fmt.Sprintf("key-%04d", i),
			Available: true,
			Present:   true,
			ScanAt:    scanAt,
		})
	}
	return updateArr
}

func setBenchmarkAvailabilityScan(updateArr []AvailabilityUpdateObj, scanAt time.Time) {
	for index := range updateArr {
		updateArr[index].ScanAt = scanAt
	}
}

func setBenchmarkStatsVersion(statsArr []MirrorStatsObj, versionCount uint64) {
	for index := range statsArr {
		statsArr[index].VersionCount = versionCount
	}
}

func benchmarkStatsArr(keyCount int) []MirrorStatsObj {
	statsArr := make([]MirrorStatsObj, 0, keyCount)
	publishedAt := time.Unix(200, 0).UTC()
	for i := 0; i < keyCount; i++ {
		statsArr = append(statsArr, MirrorStatsObj{
			Key:           fmt.Sprintf("key-%04d", i),
			LatestVersion: "v1.0.0",
			VersionCount:  10,
			LastPublishTS: publishedAt,
		})
	}
	return statsArr
}

// //

func BenchmarkHealthRead(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = obj.Health()
	}
}

func BenchmarkKeyStateRead(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_, _ = obj.KeyState("key-0064")
	}
}

func BenchmarkHealthReadParallel(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_ = obj.Health()
		}
	})
}

func BenchmarkKeyStateReadParallel(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			_, _ = obj.KeyState("key-0064")
		}
	})
}

func BenchmarkKeyStatesCopy(b *testing.B) {
	for _, keyCount := range []int{128, 4096} {
		b.Run(fmt.Sprintf("keys_%d", keyCount), func(b *testing.B) {
			obj := newBenchmarkObj(b, keyCount)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				_ = obj.KeyStates()
			}
		})
	}
}

func BenchmarkApplyAvailabilityBatchChanged(b *testing.B) {
	for _, keyCount := range []int{1, 128, 4096} {
		b.Run(fmt.Sprintf("keys_%d", keyCount), func(b *testing.B) {
			obj := newBenchmarkObj(b, keyCount)
			baseScanAt := time.Unix(100, 0).UTC()
			updateArr := benchmarkAvailabilityArr(keyCount, baseScanAt)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				setBenchmarkAvailabilityScan(updateArr, baseScanAt.Add(time.Duration(i+1)*time.Second))
				if err := obj.ApplyAvailabilityBatch(updateArr); err != nil {
					b.Fatalf("ApplyAvailabilityBatch returned error: %v", err)
				}
			}
		})
	}
}

func BenchmarkApplyAvailabilityBatchNoChange128(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	updateArr := benchmarkAvailabilityArr(128, time.Unix(100, 0).UTC())
	if err := obj.ApplyAvailabilityBatch(updateArr); err != nil {
		b.Fatalf("ApplyAvailabilityBatch setup returned error: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := obj.ApplyAvailabilityBatch(updateArr); err != nil {
			b.Fatalf("ApplyAvailabilityBatch returned error: %v", err)
		}
	}
}

func BenchmarkSetMirrorStatsBatchChanged(b *testing.B) {
	for _, keyCount := range []int{1, 128, 4096} {
		b.Run(fmt.Sprintf("keys_%d", keyCount), func(b *testing.B) {
			obj := newBenchmarkObj(b, keyCount)
			statsArr := benchmarkStatsArr(keyCount)
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				setBenchmarkStatsVersion(statsArr, uint64(i+1))
				if err := obj.SetMirrorStatsBatch(statsArr); err != nil {
					b.Fatalf("SetMirrorStatsBatch returned error: %v", err)
				}
			}
		})
	}
}

func BenchmarkSetMirrorStatsBatchNoChange128(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	statsArr := benchmarkStatsArr(128)
	if err := obj.SetMirrorStatsBatch(statsArr); err != nil {
		b.Fatalf("SetMirrorStatsBatch setup returned error: %v", err)
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := obj.SetMirrorStatsBatch(statsArr); err != nil {
			b.Fatalf("SetMirrorStatsBatch returned error: %v", err)
		}
	}
}

func BenchmarkSetContentChecksumNoChange(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	contentObj := benchmarkHashObj("content")
	obj.SetContentChecksum(contentObj)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		obj.SetContentChecksum(contentObj)
	}
}

func BenchmarkSetContentChecksumChanged(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		obj.SetContentChecksum(benchmarkChangingHashObj(i))
	}
}

func BenchmarkRaiseDiagnosticUpsert(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	diagnosticObj := benchmarkUpstreamDiagnosticObj("key-0064")
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
			b.Fatalf("RaiseDiagnostic returned error: %v", err)
		}
	}
}

func BenchmarkActiveDiagnosticsCopy64(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	for i := 0; i < 64; i++ {
		version := fmt.Sprintf("v1.0.%d", i)
		diagnosticObj := benchmarkBuildDiagnosticObj("key-0064", version)
		if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
			b.Fatalf("RaiseDiagnostic setup returned error: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		_ = obj.ActiveDiagnostics()
	}
}

func BenchmarkRaiseDiagnosticEvictInactive(b *testing.B) {
	for _, maxRecords := range []int{128, 4096} {
		b.Run(fmt.Sprintf("records_%d", maxRecords), func(b *testing.B) {
			obj := newBenchmarkObj(b, 128)
			obj.maxDiagnostics = maxRecords
			for i := 0; i < obj.maxDiagnostics; i++ {
				version := fmt.Sprintf("v1.0.%d", i)
				diagnosticObj := benchmarkBuildDiagnosticObj("key-0064", version)
				if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
					b.Fatalf("RaiseDiagnostic setup returned error: %v", err)
				}
			}
			obj.ClearAllDiagnostics()
			b.ReportAllocs()
			b.ResetTimer()

			for i := 0; i < b.N; i++ {
				if i%obj.maxDiagnostics == 0 {
					obj.ClearAllDiagnostics()
				}
				version := fmt.Sprintf("v2.0.%d", i)
				diagnosticObj := benchmarkBuildDiagnosticObj("key-0064", version)
				_ = obj.RaiseDiagnostic(diagnosticObj)
			}
		})
	}
}

func BenchmarkRaiseDiagnosticFullActiveRegistry(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	obj.maxDiagnostics = cDefaultMaxDiagnostics
	for i := 0; i < obj.maxDiagnostics; i++ {
		version := fmt.Sprintf("v1.0.%d", i)
		diagnosticObj := benchmarkBuildDiagnosticObj("key-0064", version)
		if err := obj.RaiseDiagnostic(diagnosticObj); err != nil {
			b.Fatalf("RaiseDiagnostic setup returned error: %v", err)
		}
	}
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		version := fmt.Sprintf("v2.0.%d", i)
		diagnosticObj := benchmarkBuildDiagnosticObj("key-0064", version)
		if err := obj.RaiseDiagnostic(diagnosticObj); err == nil {
			b.Fatal("RaiseDiagnostic accepted new diagnostic over full active registry")
		}
	}
}

func BenchmarkClearVersionDiagnosticsNoop(b *testing.B) {
	obj := newBenchmarkObj(b, 128)
	b.ReportAllocs()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		if err := obj.ClearVersionDiagnostics("key-0064", "v9.9.9"); err != nil {
			b.Fatalf("ClearVersionDiagnostics returned error: %v", err)
		}
	}
}
