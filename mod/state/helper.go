package state

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func validateText(name string, value string, maxBytes int) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds maximum size of %d bytes", name, maxBytes)
	}
	return nil
}

func ensureObj(obj *Obj) error {
	if obj == nil {
		return errors.New("state object is nil")
	}
	if obj.selfObj != obj {
		return errors.New("state object was copied or is not initialized")
	}
	return nil
}

func (obj *Obj) buildKeyIndexLocked() {
	obj.keyOrder = make([]string, 0, len(obj.keyMap))
	for key := range obj.keyMap {
		obj.keyOrder = append(obj.keyOrder, key)
	}
	sort.Strings(obj.keyOrder)

	obj.keyIndex = make(map[string]int, len(obj.keyOrder))
	for index, key := range obj.keyOrder {
		obj.keyIndex[key] = index
	}
}

func (obj *Obj) bumpGenerationLocked() {
	if obj.generation < ^uint64(0) {
		obj.generation++
	}
}

func (obj *Obj) publishSnapshotLocked(snapshotObj SnapshotObj) {
	obj.bumpGenerationLocked()
	snapshotObj.Generation = obj.generation
	obj.snapObj.Store(&snapshotObj)
}

func (obj *Obj) publishChangedLocked(changedFlag bool) {
	if !changedFlag {
		return
	}
	snapshotObj := obj.buildSnapshotLocked()
	obj.bumpGenerationLocked()
	snapshotObj.Generation = obj.generation
	obj.snapObj.Store(snapshotObj)
}

func (obj *Obj) publishMetaChangedLocked(changedFlag bool) {
	if !changedFlag {
		return
	}
	currentObj := obj.snapObj.Load()
	if currentObj == nil {
		obj.publishChangedLocked(true)
		return
	}
	nextObj := *currentObj
	nextObj.Checksums = obj.checksums
	nextObj.LastRescan = obj.lastRescan
	obj.publishSnapshotLocked(nextObj)
}

func (obj *Obj) publishKeyViewsChangedLocked(changedFlag bool) {
	if !changedFlag {
		return
	}
	currentObj := obj.snapObj.Load()
	if currentObj == nil {
		obj.publishChangedLocked(true)
		return
	}

	keyArr := make([]KeyStateObj, len(obj.keyOrder))
	for index, key := range obj.keyOrder {
		statusObj := stcode.OperationalStatusOk
		if index < len(currentObj.keyArr) {
			statusObj = currentObj.keyArr[index].Status
		}
		keyArr[index] = keyViewFromMut(obj.keyMap[key], statusObj)
	}

	nextObj := *currentObj
	nextObj.Checksums = obj.checksums
	nextObj.LastRescan = obj.lastRescan
	nextObj.keyArr = keyArr
	obj.publishSnapshotLocked(nextObj)
}

func (obj *Obj) publishHealthChangedLocked(changedFlag bool) {
	if !changedFlag {
		return
	}
	currentObj := obj.snapObj.Load()
	if currentObj == nil {
		obj.publishChangedLocked(true)
		return
	}
	nextObj := *currentObj
	nextObj.healthObj.DroppedDiagnostics = obj.droppedDiagnostics
	obj.publishSnapshotLocked(nextObj)
}

func (obj *Obj) publishDiagnosticBumpLocked(keyObj diagnosticKeyObj) {
	currentObj := obj.snapObj.Load()
	recordObj := obj.diagnosticMap[keyObj]
	if currentObj == nil || recordObj == nil {
		obj.publishChangedLocked(true)
		return
	}
	nextDiagArr := append([]DiagnosticViewObj(nil), currentObj.diagnosticArr...)
	foundFlag := false
	for i := range nextDiagArr {
		if nextDiagArr[i].Code == recordObj.code && nextDiagArr[i].Scope == recordObj.scope &&
			nextDiagArr[i].Key == recordObj.key && nextDiagArr[i].Version == recordObj.version {
			nextDiagArr[i] = diagnosticViewFromRecord(recordObj)
			foundFlag = true
			break
		}
	}
	if !foundFlag {
		obj.publishChangedLocked(true)
		return
	}
	nextObj := *currentObj
	nextObj.diagnosticArr = nextDiagArr
	obj.publishSnapshotLocked(nextObj)
}

func validateOptionalText(name string, value string, maxBytes int) error {
	if value == "" {
		return nil
	}
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s is empty", name)
	}
	if len(value) > maxBytes {
		return fmt.Errorf("%s exceeds maximum size of %d bytes", name, maxBytes)
	}
	return nil
}

func trimMessage(text string) string {
	if len(text) <= cMaxMessageBytes {
		return strings.Clone(text)
	}

	limit := cMaxMessageBytes
	for limit > 0 && !utf8.ValidString(text[:limit]) {
		limit--
	}
	if limit == 0 {
		return ""
	}
	return strings.Clone(text[:limit])
}

func maxStatus(leftObj stcode.OperationalStatusType, rightObj stcode.OperationalStatusType) stcode.OperationalStatusType {
	if statusRank(rightObj) > statusRank(leftObj) {
		return rightObj
	}
	return leftObj
}

func statusRank(statusObj stcode.OperationalStatusType) int {
	switch statusObj {
	case stcode.OperationalStatusError:
		return 2
	case stcode.OperationalStatusDegraded:
		return 1
	default:
		return 0
	}
}

func checkedClass(classObj stcode.SourceClassType) error {
	switch classObj {
	case stcode.SourceClassGit, stcode.SourceClassBrother:
		return nil
	default:
		return errors.New("source classification is invalid")
	}
}
