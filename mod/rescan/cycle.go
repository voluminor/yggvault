package rescan

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func composerNameOf(evidenceJSON string) string {
	var evidenceObj struct {
		ComposerName string `json:"composer_name"`
	}
	if json.Unmarshal([]byte(evidenceJSON), &evidenceObj) != nil {
		return ""
	}
	return evidenceObj.ComposerName
}

func (obj *Obj) raiseComposerCollision(collisionObj overlay.ComposerCollisionObj) {
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "composer_name_collision",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonGoOverlayDegraded,
		Key:     collisionObj.Key,
		Message: overlay.CollisionError(collisionObj).Error(),
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

// // // // // // // // // //

func (obj *Obj) finishCycle(ctx context.Context) {
	parallel := int(obj.configObj.Rescan.MaxParallelKeys)
	if parallel < 1 {
		parallel = 1
	}

	var aggMu sync.Mutex
	nameByKey := make(map[string]string, len(obj.keyArr))

	semChan := make(chan struct{}, parallel)
	var wgObj sync.WaitGroup
	for _, keyText := range obj.keyArr {
		if ctx.Err() != nil {
			break
		}
		keyValue := keyText
		select {
		case semChan <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wgObj.Add(1)
		go func() {
			defer wgObj.Done()
			defer func() { <-semChan }()
			defer obj.recoverPanic("finish_cycle", keyValue)
			if ctx.Err() != nil {
				return
			}
			// Latest was already reconciled per key before this barrier; use state without another storage read.
			keyStateObj, known := obj.stateObj.KeyState(keyValue)
			if !known || keyStateObj.LatestVersion == "" {
				return
			}
			detectionObj, detOk, detErr := obj.storageObj.GetDetection(ctx, keyValue, keyStateObj.LatestVersion)
			if detErr != nil || !detOk {
				return
			}
			nameText := ""
			if detectionObj.IsComposer {
				nameText = composerNameOf(detectionObj.EvidenceJSON)
			}
			if nameText == "" {
				return
			}

			aggMu.Lock()
			nameByKey[keyValue] = nameText
			aggMu.Unlock()
		}()
	}
	wgObj.Wait()

	namesArr, nameToKey, collisionArr := overlay.ResolveComposerNames(nameByKey)
	for i := range collisionArr {
		obj.raiseComposerCollision(collisionArr[i])
	}
	obj.composerMu.Lock()
	obj.composerNames = namesArr
	obj.composerKeyByName = nameToKey
	obj.composerMu.Unlock()

	obj.pruneMissMap()

	if contentHashObj, err := obj.storageObj.ContentChecksum(ctx); err == nil {
		obj.stateObj.SetContentChecksum(contentHashObj)
	}
}

// reconcileKeyStats refreshes one key's mirror stats (latest version, count, last-publish) from storage and
// publishes them immediately. Running this at the end of the key's OWN runKey — instead of the cycle-final
// batch — makes a key's newest version visible (web page + go-proxy ETag) as soon as that key finishes, so a
// slow or stuck key no longer freezes the whole fleet's "latest" until the cycle ends. Snapshot swaps stay
// cheap: publishKeyViewsChangedLocked is a no-op unless the stats actually changed.
func (obj *Obj) reconcileKeyStats(ctx context.Context, key string) {
	if ctx.Err() != nil {
		return
	}
	latestObj, ok, err := obj.storageObj.LatestVersion(ctx, key)
	if err != nil || !ok {
		return
	}
	versionCount, countErr := obj.storageObj.CountVersions(ctx, key)
	if countErr != nil {
		versionCount = 0
	}
	_ = obj.stateObj.SetMirrorStats(state.MirrorStatsObj{
		Key:           key,
		LatestVersion: latestObj.Version,
		VersionCount:  versionCount,
		LastPublishTS: latestObj.IngestTS,
	})
}

func (obj *Obj) pruneMissMap() {
	validSet := make(map[string]struct{}, len(obj.keyArr))
	for _, keyText := range obj.keyArr {
		validSet[keyText] = struct{}{}
	}
	obj.missMu.Lock()
	for missObj := range obj.missMap {
		if _, ok := validSet[missObj.key]; !ok {
			delete(obj.missMap, missObj)
		}
	}
	obj.missMu.Unlock()

	// permFailMap follows configured keys; removed keys must not retain failure memory.
	obj.permFailMu.Lock()
	for failObj := range obj.permFailMap {
		if _, ok := validSet[failObj.key]; !ok {
			delete(obj.permFailMap, failObj)
		}
	}
	obj.permFailMu.Unlock()
}

// // // // // // // // // //

// ComposerPackageNames returns sorted composer names without collisions.
// mod/server serves them live through overlay.ComposerPackages and overlay.ComposerPackageList.
func (obj *Obj) ComposerPackageNames() []string {
	obj.composerMu.RLock()
	defer obj.composerMu.RUnlock()
	return append([]string(nil), obj.composerNames...)
}

// ComposerKeyForName returns the storage key that owns a composer name.
// Only collision winners are exposed; unknown or collided names return false.
func (obj *Obj) ComposerKeyForName(name string) (string, bool) {
	obj.composerMu.RLock()
	defer obj.composerMu.RUnlock()
	keyText, ok := obj.composerKeyByName[name]
	return keyText, ok
}
