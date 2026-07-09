package state

import (
	"errors"
	"time"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type availabilityPreparedObj struct {
	mutObj    *keyMutObj
	updateObj AvailabilityUpdateObj
}

func prepareAvailability(mutObj *keyMutObj, updateObj AvailabilityUpdateObj) availabilityPreparedObj {
	return availabilityPreparedObj{
		mutObj:    mutObj,
		updateObj: updateObj,
	}
}

func validateScanAt(scanAt time.Time, now time.Time) error {
	if scanAt.IsZero() {
		return errors.New("availability scan timestamp is empty")
	}
	if scanAt.After(now.Add(cMaxScanFutureSkew)) {
		return errors.New("availability scan timestamp is too far in the future")
	}
	return nil
}

func validateAvailabilityUpdate(updateObj AvailabilityUpdateObj, now time.Time) error {
	if err := validateText("key", updateObj.Key, cMaxKeyBytes); err != nil {
		return err
	}
	return validateScanAt(updateObj.ScanAt, now)
}

func staleScan(mutObj *keyMutObj, scanAt time.Time) bool {
	if mutObj.lastScan.IsZero() {
		return scanAt.IsZero() && mutObj.availability != stcode.AvailabilityStatusUnknown
	}
	return scanAt.Before(mutObj.lastScan)
}

// sameCycleReplay decides the fate of a write with an equal timestamp: a same-cycle
// correction on top of Available (the recovery write precedes the listing outcome) is applied,
// while a repeat on top of a down status is a replay and is ignored.
func sameCycleReplay(mutObj *keyMutObj, scanAt time.Time) bool {
	return !mutObj.lastScan.IsZero() &&
		scanAt.Equal(mutObj.lastScan) &&
		mutObj.availability != stcode.AvailabilityStatusAvailable
}

func applyAvailable(preparedObj availabilityPreparedObj) bool {
	if staleScan(preparedObj.mutObj, preparedObj.updateObj.ScanAt) ||
		sameCycleReplay(preparedObj.mutObj, preparedObj.updateObj.ScanAt) {
		return false
	}

	previousObj := preparedObj.mutObj.availability
	changedFlag := previousObj != stcode.AvailabilityStatusAvailable ||
		preparedObj.mutObj.unavailableCycles != 0 ||
		preparedObj.mutObj.upstreamPresent != preparedObj.updateObj.Present ||
		!preparedObj.mutObj.lastScan.Equal(preparedObj.updateObj.ScanAt)

	preparedObj.mutObj.availability = stcode.AvailabilityStatusAvailable
	preparedObj.mutObj.unavailableCycles = 0
	preparedObj.mutObj.upstreamPresent = preparedObj.updateObj.Present
	preparedObj.mutObj.lastScan = preparedObj.updateObj.ScanAt
	if previousObj == stcode.AvailabilityStatusPermanentDown {
		preparedObj.mutObj.reclassAllowed = true
	}
	return changedFlag
}

func applyUnavailable(preparedObj availabilityPreparedObj, permanentAt uint32) bool {
	if staleScan(preparedObj.mutObj, preparedObj.updateObj.ScanAt) ||
		sameCycleReplay(preparedObj.mutObj, preparedObj.updateObj.ScanAt) {
		return false
	}

	previousStatusObj := preparedObj.mutObj.availability
	previousCycles := preparedObj.mutObj.unavailableCycles
	previousPresent := preparedObj.mutObj.upstreamPresent
	previousScan := preparedObj.mutObj.lastScan
	previousReclass := preparedObj.mutObj.reclassAllowed

	// Freeze the counter at permanentAt: once permanent-down, further increments only churn the snapshot
	// (a changed count under an unchanged status) without adding information.
	if preparedObj.mutObj.unavailableCycles < permanentAt {
		preparedObj.mutObj.unavailableCycles++
	}

	if preparedObj.mutObj.unavailableCycles >= permanentAt {
		preparedObj.mutObj.availability = stcode.AvailabilityStatusPermanentDown
	} else {
		preparedObj.mutObj.availability = stcode.AvailabilityStatusTemporaryDown
	}
	preparedObj.mutObj.upstreamPresent = false
	preparedObj.mutObj.reclassAllowed = false
	preparedObj.mutObj.lastScan = preparedObj.updateObj.ScanAt
	return previousStatusObj != preparedObj.mutObj.availability ||
		previousCycles != preparedObj.mutObj.unavailableCycles ||
		previousPresent != preparedObj.mutObj.upstreamPresent ||
		previousReclass != preparedObj.mutObj.reclassAllowed ||
		!previousScan.Equal(preparedObj.mutObj.lastScan)
}

func applyAvailability(preparedObj availabilityPreparedObj, permanentAt uint32) bool {
	if preparedObj.updateObj.Available {
		return applyAvailable(preparedObj)
	}
	return applyUnavailable(preparedObj, permanentAt)
}

// //

// MarkAvailable records an available upstream scan and resets the unavailable counter.
func (obj *Obj) MarkAvailable(key string, present bool, scanAt time.Time) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	return obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
		{
			Key:       key,
			Available: true,
			Present:   present,
			ScanAt:    scanAt,
		},
	})
}

// MarkUnavailable records an unavailable upstream scan and switches to permanent-down after the configured limit.
func (obj *Obj) MarkUnavailable(key string, scanAt time.Time) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	return obj.ApplyAvailabilityBatch([]AvailabilityUpdateObj{
		{
			Key:       key,
			Available: false,
			ScanAt:    scanAt,
		},
	})
}

// ApplyAvailabilityBatch applies availability updates atomically and publishes one snapshot.
// Stale scans are ignored; duplicate keys in the batch are rejected.
func (obj *Obj) ApplyAvailabilityBatch(updateArr []AvailabilityUpdateObj) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if len(updateArr) == 0 {
		return nil
	}
	if len(updateArr) > len(obj.keyMap) {
		return errors.New("availability batch exceeds configured key count")
	}
	now := time.Now().UTC()
	for _, updateObj := range updateArr {
		if err := validateAvailabilityUpdate(updateObj, now); err != nil {
			return err
		}
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	if len(updateArr) == 1 {
		mutObj, err := obj.keyMutLocked(updateArr[0].Key)
		if err != nil {
			return err
		}
		preparedObj := prepareAvailability(mutObj, updateArr[0])
		obj.publishKeyViewsChangedLocked(applyAvailability(preparedObj, obj.permanentAt))
		return nil
	}

	seenMap := make(map[string]struct{}, len(updateArr))
	for _, updateObj := range updateArr {
		if _, ok := seenMap[updateObj.Key]; ok {
			return errors.New("availability batch contains duplicate key")
		}
		seenMap[updateObj.Key] = struct{}{}

		if _, err := obj.keyMutLocked(updateObj.Key); err != nil {
			return err
		}
	}

	changedFlag := false
	for _, updateObj := range updateArr {
		preparedObj := prepareAvailability(obj.keyMap[updateObj.Key], updateObj)
		changedFlag = applyAvailability(preparedObj, obj.permanentAt) || changedFlag
	}
	obj.publishKeyViewsChangedLocked(changedFlag)
	return nil
}
