package state

import (
	"errors"
	"strings"
	"time"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func (obj *Obj) validateDiagnosticScopeLocked(scopeObj stcode.LogScopeType, key string, version string) (string, error) {
	switch scopeObj {
	case stcode.LogScopeGlobal:
		if key != "" || version != "" {
			return "", errors.New("global diagnostic must not include key or version")
		}
	case stcode.LogScopeKey:
		if version != "" {
			return "", errors.New("key-scoped diagnostic must not include version")
		}
		mutObj, err := obj.keyMutLocked(key)
		if err != nil {
			return "", err
		}
		return mutObj.key, nil
	case stcode.LogScopeVersion:
		mutObj, err := obj.keyMutLocked(key)
		if err != nil {
			return "", err
		}
		if err := validateText("version", version, cMaxVersionBytes); err != nil {
			return "", err
		}
		return mutObj.key, nil
	default:
		return "", errors.New("diagnostic scope is invalid")
	}
	return "", nil
}

func validateDiagnosticObj(diagnosticObj DiagnosticObj) error {
	if err := validateText("diagnostic code", diagnosticObj.Code, cMaxDiagnosticCodeBytes); err != nil {
		return err
	}
	if err := validateOptionalText("key", diagnosticObj.Key, cMaxKeyBytes); err != nil {
		return err
	}
	if err := validateOptionalText("version", diagnosticObj.Version, cMaxVersionBytes); err != nil {
		return err
	}
	if diagnosticObj.Impact == stcode.UndefOperationalStatus || !diagnosticObj.Impact.IsValid() {
		return errors.New("diagnostic impact is invalid")
	}
	if diagnosticObj.Impact != stcode.OperationalStatusDegraded &&
		diagnosticObj.Impact != stcode.OperationalStatusError {
		return errors.New("diagnostic impact is below health threshold")
	}
	if diagnosticObj.Reason == stcode.UndefLogReason || !diagnosticObj.Reason.IsValid() {
		return errors.New("diagnostic reason is invalid")
	}
	return nil
}

func validateDiagnosticKeyObj(diagnosticObj DiagnosticKeyObj) error {
	if err := validateText("diagnostic code", diagnosticObj.Code, cMaxDiagnosticCodeBytes); err != nil {
		return err
	}
	if err := validateOptionalText("key", diagnosticObj.Key, cMaxKeyBytes); err != nil {
		return err
	}
	if err := validateOptionalText("version", diagnosticObj.Version, cMaxVersionBytes); err != nil {
		return err
	}
	return nil
}

func keyFromDiagnosticObj(diagnosticObj DiagnosticObj) diagnosticKeyObj {
	return diagnosticKeyObj{
		code:    diagnosticObj.Code,
		scope:   diagnosticObj.Scope,
		key:     diagnosticObj.Key,
		version: diagnosticObj.Version,
	}
}

func keyFromDiagnosticKeyObj(diagnosticObj DiagnosticKeyObj) diagnosticKeyObj {
	return diagnosticKeyObj{
		code:    diagnosticObj.Code,
		scope:   diagnosticObj.Scope,
		key:     diagnosticObj.Key,
		version: diagnosticObj.Version,
	}
}

func (obj *Obj) indexDiagnosticLocked(keyObj diagnosticKeyObj) {
	if keyObj.key == "" {
		return
	}
	setObj := obj.diagnosticByKey[keyObj.key]
	if setObj == nil {
		setObj = make(map[diagnosticKeyObj]struct{})
		obj.diagnosticByKey[keyObj.key] = setObj
	}
	setObj[keyObj] = struct{}{}
}

func (obj *Obj) unindexDiagnosticLocked(keyObj diagnosticKeyObj) {
	if keyObj.key == "" {
		return
	}
	setObj := obj.diagnosticByKey[keyObj.key]
	if setObj == nil {
		return
	}
	delete(setObj, keyObj)
	if len(setObj) == 0 {
		delete(obj.diagnosticByKey, keyObj.key)
	}
}

func (obj *Obj) evictInactiveDiagnosticLocked() bool {
	for index, keyObj := range obj.diagnosticOrder {
		recordObj := obj.diagnosticMap[keyObj]
		if recordObj == nil || recordObj.active {
			continue
		}

		delete(obj.diagnosticMap, keyObj)
		obj.unindexDiagnosticLocked(keyObj)
		lastIndex := len(obj.diagnosticOrder) - 1
		obj.diagnosticOrder[index] = obj.diagnosticOrder[lastIndex]
		obj.diagnosticOrder[lastIndex] = diagnosticKeyObj{}
		obj.diagnosticOrder = obj.diagnosticOrder[:lastIndex]
		return true
	}
	return false
}

func (obj *Obj) upsertDiagnosticLocked(keyObj diagnosticKeyObj, diagnosticObj DiagnosticObj, now time.Time) (bool, error) {
	if recordObj, ok := obj.diagnosticMap[keyObj]; ok {
		structuralChange := !recordObj.active ||
			recordObj.impact != diagnosticObj.Impact ||
			recordObj.reason != diagnosticObj.Reason ||
			recordObj.scope != diagnosticObj.Scope
		recordObj.scope = diagnosticObj.Scope
		recordObj.impact = diagnosticObj.Impact
		recordObj.reason = diagnosticObj.Reason
		recordObj.message = trimMessage(diagnosticObj.Message)
		recordObj.lastSeen = now
		recordObj.active = true
		if recordObj.count < ^uint64(0) {
			recordObj.count++
		}
		return structuralChange, nil
	}

	if len(obj.diagnosticMap) >= obj.maxDiagnostics && !obj.evictInactiveDiagnosticLocked() {
		if obj.droppedDiagnostics < ^uint64(0) {
			obj.droppedDiagnostics++
		}
		return false, errors.New("diagnostic registry is full")
	}

	storedCode := strings.Clone(diagnosticObj.Code)
	storedVersion := strings.Clone(diagnosticObj.Version)
	storedKeyObj := diagnosticKeyObj{
		code:    storedCode,
		scope:   diagnosticObj.Scope,
		key:     diagnosticObj.Key,
		version: storedVersion,
	}

	obj.diagnosticMap[storedKeyObj] = &diagnosticRecordObj{
		code:      storedCode,
		scope:     diagnosticObj.Scope,
		impact:    diagnosticObj.Impact,
		reason:    diagnosticObj.Reason,
		key:       diagnosticObj.Key,
		version:   storedVersion,
		message:   trimMessage(diagnosticObj.Message),
		firstSeen: now,
		lastSeen:  now,
		count:     1,
		active:    true,
	}
	obj.diagnosticOrder = append(obj.diagnosticOrder, storedKeyObj)
	obj.indexDiagnosticLocked(storedKeyObj)
	return true, nil
}

func (obj *Obj) clearDiagnosticsLocked(key string, version string, versionOnly bool) bool {
	setObj := obj.diagnosticByKey[key]
	if len(setObj) == 0 {
		return false
	}
	changedFlag := false
	for keyObj := range setObj {
		recordObj := obj.diagnosticMap[keyObj]
		if recordObj == nil || !recordObj.active {
			continue
		}
		if versionOnly && recordObj.version != version {
			continue
		}
		recordObj.active = false
		changedFlag = true
	}
	return changedFlag
}

// //

// RaiseDiagnostic creates or updates a diagnostic.
// Repeating the same active diagnostic bumps count/lastSeen cheaply; structural changes rebuild the snapshot.
// A full registry with no inactive record to evict returns an error and increments dropped diagnostics.
func (obj *Obj) RaiseDiagnostic(diagnosticObj DiagnosticObj) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if err := validateDiagnosticObj(diagnosticObj); err != nil {
		return err
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	canonicalKey, err := obj.validateDiagnosticScopeLocked(diagnosticObj.Scope, diagnosticObj.Key, diagnosticObj.Version)
	if err != nil {
		return err
	}
	diagnosticObj.Key = canonicalKey

	keyObj := keyFromDiagnosticObj(diagnosticObj)
	fullRebuildFlag, err := obj.upsertDiagnosticLocked(keyObj, diagnosticObj, time.Now().UTC())
	switch {
	case fullRebuildFlag:
		obj.publishChangedLocked(true)
	case err != nil:
		obj.publishHealthChangedLocked(true)
	default:
		obj.publishDiagnosticBumpLocked(keyObj)
	}
	return err
}

// ClearDiagnostic deactivates one diagnostic by identity; the record remains until eviction.
func (obj *Obj) ClearDiagnostic(diagnosticObj DiagnosticKeyObj) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if err := validateDiagnosticKeyObj(diagnosticObj); err != nil {
		return err
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	canonicalKey, err := obj.validateDiagnosticScopeLocked(diagnosticObj.Scope, diagnosticObj.Key, diagnosticObj.Version)
	if err != nil {
		return err
	}
	diagnosticObj.Key = canonicalKey

	keyObj := keyFromDiagnosticKeyObj(diagnosticObj)
	changedFlag := false
	if recordObj := obj.diagnosticMap[keyObj]; recordObj != nil {
		changedFlag = recordObj.active
		if changedFlag {
			recordObj.active = false
		}
	}
	obj.publishChangedLocked(changedFlag)
	return nil
}

// ClearKeyDiagnostics deactivates all active diagnostics for a key through the key index.
func (obj *Obj) ClearKeyDiagnostics(key string) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	if _, err := obj.keyMutLocked(key); err != nil {
		return err
	}
	obj.publishChangedLocked(obj.clearDiagnosticsLocked(key, "", false))
	return nil
}

// ClearVersionDiagnostics deactivates active diagnostics for one key version.
func (obj *Obj) ClearVersionDiagnostics(key string, version string) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if err := validateText("version", version, cMaxVersionBytes); err != nil {
		return err
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	if _, err := obj.keyMutLocked(key); err != nil {
		return err
	}
	obj.publishChangedLocked(obj.clearDiagnosticsLocked(key, version, true))
	return nil
}

// ClearAllDiagnostics deactivates all active diagnostics in the registry.
func (obj *Obj) ClearAllDiagnostics() {
	if err := ensureObj(obj); err != nil {
		return
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	changedFlag := false
	for _, recordObj := range obj.diagnosticMap {
		if recordObj != nil && recordObj.active {
			recordObj.active = false
			changedFlag = true
		}
	}
	obj.publishChangedLocked(changedFlag)
}
