package state

import (
	"sort"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func copyReasons(reasonArr []stcode.LogReasonType) []stcode.LogReasonType {
	if len(reasonArr) == 0 {
		return nil
	}
	return append([]stcode.LogReasonType(nil), reasonArr...)
}

func copyDiagnostics(diagnosticArr []DiagnosticViewObj) []DiagnosticViewObj {
	if len(diagnosticArr) == 0 {
		return nil
	}
	return append([]DiagnosticViewObj(nil), diagnosticArr...)
}

func copyKeys(keyArr []KeyStateObj) []KeyStateObj {
	if len(keyArr) == 0 {
		return nil
	}
	return append([]KeyStateObj(nil), keyArr...)
}

func diagnosticViewFromRecord(recordObj *diagnosticRecordObj) DiagnosticViewObj {
	return DiagnosticViewObj{
		Code:      recordObj.code,
		Scope:     recordObj.scope,
		Impact:    recordObj.impact,
		Reason:    recordObj.reason,
		Key:       recordObj.key,
		Version:   recordObj.version,
		Message:   recordObj.message,
		FirstSeen: recordObj.firstSeen,
		LastSeen:  recordObj.lastSeen,
		Count:     recordObj.count,
	}
}

func keyViewFromMut(mutObj *keyMutObj, statusObj stcode.OperationalStatusType) KeyStateObj {
	return KeyStateObj{
		Key:               mutObj.key,
		Status:            statusObj,
		Classification:    mutObj.classification,
		Classified:        mutObj.classified,
		SourceURL:         mutObj.sourceURL,
		BrotherURL:        mutObj.brotherURL,
		RemoteKey:         mutObj.remoteKey,
		Availability:      mutObj.availability,
		UnavailableCycles: mutObj.unavailableCycles,
		LastScan:          mutObj.lastScan,
		UpstreamPresent:   mutObj.upstreamPresent,
		LatestVersion:     mutObj.latestVersion,
		VersionCount:      mutObj.versionCount,
		LastPublishTS:     mutObj.lastPublishTS,
	}
}

func sortDiagnosticViews(diagnosticArr []DiagnosticViewObj) {
	sort.Slice(diagnosticArr, func(leftIndex int, rightIndex int) bool {
		leftObj := diagnosticArr[leftIndex]
		rightObj := diagnosticArr[rightIndex]
		if !leftObj.LastSeen.Equal(rightObj.LastSeen) {
			return leftObj.LastSeen.After(rightObj.LastSeen)
		}
		if leftObj.Code != rightObj.Code {
			return leftObj.Code < rightObj.Code
		}
		if leftObj.Key != rightObj.Key {
			return leftObj.Key < rightObj.Key
		}
		return leftObj.Version < rightObj.Version
	})
}

func sortedReasons(reasonMap map[stcode.LogReasonType]struct{}) []stcode.LogReasonType {
	reasonArr := make([]stcode.LogReasonType, 0, len(reasonMap))
	for reasonObj := range reasonMap {
		reasonArr = append(reasonArr, reasonObj)
	}
	sort.Slice(reasonArr, func(leftIndex int, rightIndex int) bool {
		return reasonArr[leftIndex].Int() < reasonArr[rightIndex].Int()
	})
	if len(reasonArr) > cMaxHealthReasons {
		return reasonArr[:cMaxHealthReasons]
	}
	return reasonArr
}

func statusForKey(key string, globalObj stcode.OperationalStatusType, keyStatusMap map[string]stcode.OperationalStatusType) stcode.OperationalStatusType {
	statusObj := globalObj
	if keyStatusObj, ok := keyStatusMap[key]; ok {
		statusObj = maxStatus(statusObj, keyStatusObj)
	}
	return statusObj
}

func (obj *Obj) buildSnapshotLocked() *SnapshotObj {
	diagnosticArr := make([]DiagnosticViewObj, 0, len(obj.diagnosticMap))
	reasonMap := make(map[stcode.LogReasonType]struct{}, 8)
	keyStatusMap := make(map[string]stcode.OperationalStatusType)

	healthStatusObj := stcode.OperationalStatusOk
	globalStatusObj := stcode.OperationalStatusOk

	for _, recordObj := range obj.diagnosticMap {
		if recordObj == nil || !recordObj.active {
			continue
		}

		diagnosticArr = append(diagnosticArr, diagnosticViewFromRecord(recordObj))
		reasonMap[recordObj.reason] = struct{}{}

		statusObj := recordObj.impact
		healthStatusObj = maxStatus(healthStatusObj, statusObj)

		if recordObj.scope == stcode.LogScopeGlobal || recordObj.key == "" {
			globalStatusObj = maxStatus(globalStatusObj, statusObj)
			continue
		}
		keyStatusMap[recordObj.key] = maxStatus(keyStatusMap[recordObj.key], statusObj)
	}

	sortDiagnosticViews(diagnosticArr)
	reasonArr := sortedReasons(reasonMap)

	keyArr := make([]KeyStateObj, 0, len(obj.keyMap))
	for _, key := range obj.keyOrder {
		mutObj := obj.keyMap[key]
		keyArr = append(keyArr, keyViewFromMut(mutObj, statusForKey(key, globalStatusObj, keyStatusMap)))
	}

	return &SnapshotObj{
		Checksums:  obj.checksums,
		LastRescan: obj.lastRescan,
		healthObj: HealthViewObj{
			Status:             healthStatusObj,
			Reasons:            reasonArr,
			DiagnosticsCount:   uint64(len(diagnosticArr)),
			DroppedDiagnostics: obj.droppedDiagnostics,
		},
		keyArr:        keyArr,
		keyIndex:      obj.keyIndex,
		diagnosticArr: diagnosticArr,
	}
}

// //

// Snapshot returns the current immutable snapshot without locking.
// Nil, copied and unpublished registries yield a zero SnapshotObj.
func (obj *Obj) Snapshot() SnapshotObj {
	if obj == nil || obj.selfObj != obj {
		return SnapshotObj{}
	}
	snapshotObj := obj.snapObj.Load()
	if snapshotObj == nil {
		return SnapshotObj{}
	}
	return *snapshotObj
}

// Health returns health from the current snapshot.
func (obj *Obj) Health() HealthViewObj {
	snapshotObj := obj.Snapshot()
	return snapshotObj.Health()
}

// Checksums returns checksums from the current snapshot.
func (obj *Obj) Checksums() ChecksumSetObj {
	snapshotObj := obj.Snapshot()
	return snapshotObj.Checksums
}

// Generation returns the monotonic snapshot generation number.
func (obj *Obj) Generation() uint64 {
	snapshotObj := obj.Snapshot()
	return snapshotObj.Generation
}

// KeyState returns key state from the current snapshot; ok=false for unknown keys.
func (obj *Obj) KeyState(key string) (KeyStateObj, bool) {
	snapshotObj := obj.Snapshot()
	return snapshotObj.KeyState(key)
}

// KeyStates returns a copy of all key states from the current snapshot.
func (obj *Obj) KeyStates() []KeyStateObj {
	snapshotObj := obj.Snapshot()
	return snapshotObj.KeyStates()
}

// ActiveDiagnostics returns active diagnostics from the current snapshot, sorted by LastSeen desc.
func (obj *Obj) ActiveDiagnostics() []DiagnosticViewObj {
	snapshotObj := obj.Snapshot()
	return snapshotObj.ActiveDiagnostics()
}

// Health returns snapshot health and copies Reasons to avoid sharing the immutable slice.
func (obj SnapshotObj) Health() HealthViewObj {
	healthObj := obj.healthObj
	healthObj.Reasons = copyReasons(healthObj.Reasons)
	return healthObj
}

// KeyState returns key state from the snapshot; ok=false for unknown keys.
func (obj SnapshotObj) KeyState(key string) (KeyStateObj, bool) {
	index, ok := obj.keyIndex[key]
	if !ok {
		return KeyStateObj{}, false
	}
	return obj.keyArr[index], true
}

// KeyStates returns a copy of all key states.
func (obj SnapshotObj) KeyStates() []KeyStateObj {
	return copyKeys(obj.keyArr)
}

// ActiveDiagnostics returns a copy of active diagnostics, sorted by LastSeen desc.
func (obj SnapshotObj) ActiveDiagnostics() []DiagnosticViewObj {
	return copyDiagnostics(obj.diagnosticArr)
}

// RecentDiagnostics returns a copy of at most limit newest active diagnostics.
// Only the result is copied to bound hot-read path cost.
func (obj SnapshotObj) RecentDiagnostics(limit int) []DiagnosticViewObj {
	if limit < 0 {
		limit = 0
	}
	if limit > len(obj.diagnosticArr) {
		limit = len(obj.diagnosticArr)
	}
	return copyDiagnostics(obj.diagnosticArr[:limit])
}
