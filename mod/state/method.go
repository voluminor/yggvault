package state

import (
	"errors"
	"strings"
	"time"

	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

type mirrorStatsPreparedObj struct {
	mutObj   *keyMutObj
	statsObj MirrorStatsObj
}

func (obj *Obj) keyMutLocked(key string) (*keyMutObj, error) {
	if err := validateText("key", key, cMaxKeyBytes); err != nil {
		return nil, err
	}
	mutObj, ok := obj.keyMap[key]
	if !ok {
		return nil, errors.New("state key not found")
	}
	return mutObj, nil
}

func prepareMirrorStats(mutObj *keyMutObj, statsObj MirrorStatsObj) mirrorStatsPreparedObj {
	return mirrorStatsPreparedObj{
		mutObj:   mutObj,
		statsObj: statsObj,
	}
}

func applyMirrorStats(preparedObj mirrorStatsPreparedObj) {
	if preparedObj.mutObj.latestVersion == preparedObj.statsObj.LatestVersion &&
		preparedObj.mutObj.versionCount == preparedObj.statsObj.VersionCount &&
		preparedObj.mutObj.lastPublishTS.Equal(preparedObj.statsObj.LastPublishTS) {
		return
	}
	if preparedObj.mutObj.latestVersion != preparedObj.statsObj.LatestVersion {
		preparedObj.mutObj.latestVersion = strings.Clone(preparedObj.statsObj.LatestVersion)
	}
	if preparedObj.mutObj.versionCount != preparedObj.statsObj.VersionCount {
		preparedObj.mutObj.versionCount = preparedObj.statsObj.VersionCount
	}
	if !preparedObj.mutObj.lastPublishTS.Equal(preparedObj.statsObj.LastPublishTS) {
		preparedObj.mutObj.lastPublishTS = preparedObj.statsObj.LastPublishTS
	}
}

func mirrorStatsChanged(preparedObj mirrorStatsPreparedObj) bool {
	return preparedObj.mutObj.latestVersion != preparedObj.statsObj.LatestVersion ||
		preparedObj.mutObj.versionCount != preparedObj.statsObj.VersionCount ||
		!preparedObj.mutObj.lastPublishTS.Equal(preparedObj.statsObj.LastPublishTS)
}

// //

// SetClassification records a key source type and URL.
// Reclassification is blocked until permanent-down recovery allows it, and it changes Instance.
func (obj *Obj) SetClassification(key string, classObj stcode.SourceClassType, sourceURL string, brotherURL string) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if err := checkedClass(classObj); err != nil {
		return err
	}
	if err := validateOptionalText("source URL", sourceURL, cMaxURLBytes); err != nil {
		return err
	}
	if err := validateOptionalText("brother URL", brotherURL, cMaxURLBytes); err != nil {
		return err
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	mutObj, err := obj.keyMutLocked(key)
	if err != nil {
		return err
	}

	classChangedFlag := !mutObj.classified || mutObj.classification != classObj
	if classChangedFlag && mutObj.classified && !mutObj.reclassAllowed {
		return errors.New("source classification change is not allowed")
	}

	changedFlag := classChangedFlag
	mutObj.classification = classObj
	mutObj.classified = true
	if classChangedFlag {
		mutObj.reclassAllowed = false
	}

	if sourceURL != "" && sourceURL != mutObj.sourceURL {
		mutObj.sourceURL = strings.Clone(sourceURL)
		changedFlag = true
	}

	nextBrotherURL := ""
	if classObj == stcode.SourceClassBrother {
		nextBrotherURL = brotherURL
	}
	if nextBrotherURL != mutObj.brotherURL {
		if err := validateOptionalText("brother URL", nextBrotherURL, cMaxURLBytes); err != nil {
			return err
		}
		mutObj.brotherURL = strings.Clone(nextBrotherURL)
		changedFlag = true
	}

	obj.publishKeyViewsChangedLocked(changedFlag)
	return nil
}

// SetRemoteKey stores the remote key for a brother source.
// Empty remoteKey normalizes to the local key for root-form compatibility; the call is idempotent.
func (obj *Obj) SetRemoteKey(key string, remoteKey string) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if err := validateOptionalText("remote key", remoteKey, cMaxKeyBytes); err != nil {
		return err
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	mutObj, err := obj.keyMutLocked(key)
	if err != nil {
		return err
	}
	nextRemoteKey := remoteKey
	if nextRemoteKey == "" {
		nextRemoteKey = key
	}
	if nextRemoteKey == mutObj.remoteKey {
		return nil
	}
	mutObj.remoteKey = strings.Clone(nextRemoteKey)
	obj.publishKeyViewsChangedLocked(true)
	return nil
}

// SetMirrorStats updates statistics for one mirror through SetMirrorStatsBatch.
func (obj *Obj) SetMirrorStats(statsObj MirrorStatsObj) error {
	return obj.SetMirrorStatsBatch([]MirrorStatsObj{statsObj})
}

// SetMirrorStatsBatch applies mirror statistics atomically and publishes one snapshot.
// Duplicate keys in the batch are rejected.
func (obj *Obj) SetMirrorStatsBatch(statsArr []MirrorStatsObj) error {
	if err := ensureObj(obj); err != nil {
		return err
	}
	if len(statsArr) == 0 {
		return nil
	}
	if len(statsArr) > len(obj.keyMap) {
		return errors.New("mirror stats batch exceeds configured key count")
	}
	for _, statsObj := range statsArr {
		if err := validateText("key", statsObj.Key, cMaxKeyBytes); err != nil {
			return err
		}
		if err := validateOptionalText("latest version", statsObj.LatestVersion, cMaxVersionBytes); err != nil {
			return err
		}
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	if len(statsArr) == 1 {
		mutObj, err := obj.keyMutLocked(statsArr[0].Key)
		if err != nil {
			return err
		}
		preparedObj := prepareMirrorStats(mutObj, statsArr[0])
		changedFlag := mirrorStatsChanged(preparedObj)
		applyMirrorStats(preparedObj)
		obj.publishKeyViewsChangedLocked(changedFlag)
		return nil
	}

	seenMap := make(map[string]struct{}, len(statsArr))
	preparedArr := make([]mirrorStatsPreparedObj, 0, len(statsArr))
	for _, statsObj := range statsArr {
		if _, ok := seenMap[statsObj.Key]; ok {
			return errors.New("mirror stats batch contains duplicate key")
		}
		seenMap[statsObj.Key] = struct{}{}

		mutObj, err := obj.keyMutLocked(statsObj.Key)
		if err != nil {
			return err
		}
		preparedObj := prepareMirrorStats(mutObj, statsObj)
		preparedArr = append(preparedArr, preparedObj)
	}

	changedFlag := false
	for _, preparedObj := range preparedArr {
		changedFlag = mirrorStatsChanged(preparedObj) || changedFlag
		applyMirrorStats(preparedObj)
	}
	obj.publishKeyViewsChangedLocked(changedFlag)
	return nil
}

// SetLastRescan records the last rescan time used for metadata TTL and Cache-Control.
func (obj *Obj) SetLastRescan(ts time.Time) {
	if err := ensureObj(obj); err != nil {
		return
	}

	obj.lockObj.Lock()
	defer obj.lockObj.Unlock()

	if obj.lastRescan.Equal(ts) {
		return
	}
	obj.lastRescan = ts
	obj.publishMetaChangedLocked(true)
}
