package rescan

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime/debug"
	"strings"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func hashFile(pathText string) (core.HashObj, error) {
	fileObj, err := os.Open(pathText)
	if err != nil {
		return core.HashObj{}, err
	}
	defer func() { _ = fileObj.Close() }()
	hasherObj := blake3.New()
	if _, err := io.Copy(hasherObj, fileObj); err != nil {
		return core.HashObj{}, err
	}
	return core.HashFromHasher(hasherObj), nil
}

// // // // // // // // // //

const cMaxFailureMessageBytes = 4096

// //

// classifyDegraded maps a per-version failure code to node health impact.
// Error is reserved for node-side infrastructure codes: they retry every cycle and clear on success.
// Content-deterministic codes can be quarantined and never retried, so they must never carry Error
// impact — one bad upstream version would pin the whole node in error forever. For the same reason
// unknown codes default to Degraded.
func classifyDegraded(code string) (stcode.OperationalStatusType, stcode.LogReasonType) {
	switch code {
	case "spool_failed", "publish_failed", "resurrect_failed", "detect_failed",
		"artifact_register_failed", cOverlayFallbackCode:
		return stcode.OperationalStatusError, stcode.LogReasonArtifactMaterializationFailed
	case "fetch_failed", "source_hash_failed", "brother_version_failed",
		"brother_blobs_failed", "brother_blob_filter_failed":
		return stcode.OperationalStatusDegraded, stcode.LogReasonUpstreamUnavailable
	default:
		// Known members: archive_invalid, tree_invalid, tree_hash_mismatch, public_mirror_hash_mismatch,
		// brother_index_hash_mismatch, tree_decode_failed, heal_failed, and the deterministic overlay
		// codes (go_symlink_in_module, go_manifest_missing, go_manifest_too_large,
		// rewritten_file_too_large, invalid_artifact_ref, invalid_go_version).
		return stcode.OperationalStatusDegraded, stcode.LogReasonContentRejected
	}
}

func boundedFailureMessage(messageText string) string {
	return truncateUTF8(messageText, cMaxFailureMessageBytes, "...")
}

// markQuarantineDirtyLocked flags a key as needing a quarantine summary pass. permFailMu must be held.
func (obj *Obj) markQuarantineDirtyLocked(key string) {
	if obj.quarantineDirtyObj != nil {
		obj.quarantineDirtyObj[key] = struct{}{}
	}
}

func (obj *Obj) markPermanentFailureLoadMiss(key string) {
	obj.permFailMu.Lock()
	if obj.permFailLoadMissObj != nil {
		obj.permFailLoadMissObj[key] = struct{}{}
	}
	obj.markQuarantineDirtyLocked(key)
	obj.permFailMu.Unlock()
}

func (obj *Obj) loadPermanentFailures() {
	if obj.storageObj == nil {
		return
	}
	staleTotal := 0
	for _, keyText := range obj.keyArr {
		failureArr, err := obj.storageObj.ListIngestFailures(context.Background(), keyText)
		if err != nil {
			obj.logObj.Warn().
				Str("component", "rescan").
				Str("key", keyText).
				Str("error", err.Error()).
				Msg("failed to load ingest failure quarantine")
			obj.markPermanentFailureLoadMiss(keyText)
			continue
		}
		obj.permFailMu.Lock()
		for i := range failureArr {
			if failureArr[i].Policy != core.IngestFailurePolicy {
				staleTotal++
				continue
			}
			obj.permFailMap[missKeyObj{key: failureArr[i].Key, version: failureArr[i].Version}] = failureArr[i].RefSHA
		}
		// Any durable rows (active or stale-policy) need one reconcile pass on the first cycle.
		if len(failureArr) > 0 {
			obj.markQuarantineDirtyLocked(keyText)
		}
		obj.permFailMu.Unlock()
	}
	if staleTotal > 0 {
		obj.logObj.Warn().
			Str("component", "rescan").
			Int("stale", staleTotal).
			Msg("ignored quarantine rows recorded under an older unpack policy; they retry once and are reclaimed lazily")
	}
}

func (obj *Obj) warnQuarantineCapOnce(key string) {
	cycleValue := obj.cycleCount.Load()
	obj.quarantineCapMu.Lock()
	if obj.quarantineCapWarnObj[key] == cycleValue {
		obj.quarantineCapMu.Unlock()
		return
	}
	obj.quarantineCapWarnObj[key] = cycleValue
	obj.quarantineCapMu.Unlock()
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("key", key).
		Msg("ingest failure quarantine is full; using in-memory skip only")
}

func (obj *Obj) raiseQuarantineSummary(ctx context.Context, key string) {
	if obj.storageObj == nil || ctx.Err() != nil {
		return
	}
	obj.permFailMu.Lock()
	_, dirty := obj.quarantineDirtyObj[key]
	_, loadMiss := obj.permFailLoadMissObj[key]
	skip := obj.quarantineDirtyObj != nil && !dirty && !loadMiss
	obj.permFailMu.Unlock()
	if skip {
		// Healthy key: nothing in the hot skip cache, no failed startup load, and no raised diagnostic,
		// so skip the durable ListIngestFailures round-trip that would otherwise run every cycle.
		return
	}
	failureArr, err := obj.storageObj.ListIngestFailures(ctx, key)
	if err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("error", err.Error()).
			Msg("failed to list ingest failure quarantine")
		return
	}
	activeArr := failureArr[:0]
	for i := range failureArr {
		if failureArr[i].Policy != core.IngestFailurePolicy {
			// Rows recorded under an older unpack policy are invisible to skip logic; reclaim them lazily.
			if delErr := obj.storageObj.DeleteIngestFailure(ctx, failureArr[i].Key, failureArr[i].Version); delErr != nil {
				obj.logObj.Warn().
					Str("component", "rescan").
					Str("key", key).
					Str("version", failureArr[i].Version).
					Str("error", delErr.Error()).
					Msg("failed to reclaim stale-policy quarantine row")
			}
			continue
		}
		activeArr = append(activeArr, failureArr[i])
	}
	// Self-heal the in-memory map (e.g. after a failed startup load): without the entry a later
	// terminal success would never clear the durable row and the key would stay degraded forever.
	obj.permFailMu.Lock()
	for i := range activeArr {
		missObj := missKeyObj{key: activeArr[i].Key, version: activeArr[i].Version}
		if _, ok := obj.permFailMap[missObj]; !ok {
			obj.permFailMap[missObj] = activeArr[i].RefSHA
		}
	}
	delete(obj.permFailLoadMissObj, key)
	if len(activeArr) == 0 {
		// Fully reconciled: drop the dirty mark so later cycles short-circuit until a new failure appears.
		delete(obj.quarantineDirtyObj, key)
	}
	obj.permFailMu.Unlock()
	if len(activeArr) == 0 {
		_ = obj.stateObj.ClearDiagnostic(state.DiagnosticKeyObj{
			Code:  "ingest_quarantine",
			Scope: stcode.LogScopeKey,
			Key:   key,
		})
		return
	}
	sampleMax := len(activeArr)
	if sampleMax > 5 {
		sampleMax = 5
	}
	sampleArr := make([]string, 0, sampleMax)
	for i := 0; i < sampleMax; i++ {
		sampleArr = append(sampleArr, activeArr[i].Version+":"+activeArr[i].Code)
	}
	messageText := fmt.Sprintf("%d versions in ingest quarantine", len(activeArr))
	if len(sampleArr) > 0 {
		messageText += ": " + strings.Join(sampleArr, ", ")
	}
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "ingest_quarantine",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonContentRejected,
		Key:     key,
		Message: messageText,
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

// // // // // // // // // //

// recoverPanic swallows a panic in a background rescan goroutine: a failure of one key must not bring down the process.
// Called only via defer.
func (obj *Obj) recoverPanic(scope string, key string) {
	recovered := recover()
	if recovered == nil {
		return
	}
	obj.logObj.Error().
		Str("component", "rescan").
		Str("scope", scope).
		Str("key", key).
		Str("stack", string(debug.Stack())).
		Msgf("panic recovered in rescan goroutine: %v", recovered)
	if key == "" {
		return
	}
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "rescan_panic",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusError,
		Reason:  stcode.LogReasonArtifactMaterializationFailed,
		Key:     key,
		Message: fmt.Sprintf("panic: %v", recovered),
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

// //

func (obj *Obj) raiseVersionDegraded(key string, version string, code string, err error) {
	impactObj, reasonObj := classifyDegraded(code)
	// State keeps only counters, so live degradation debugging needs this log line.
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("code", code).
		Str("key", key).
		Str("version", version).
		Str("error", err.Error()).
		Msg("version degraded")
	if statsObj := obj.cycleStats(); statsObj != nil {
		statsObj.versionsDegraded.Add(1)
	}
	obj.metricsObj.recordVersionFailure(code)
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    code,
		Scope:   stcode.LogScopeVersion,
		Impact:  impactObj,
		Reason:  reasonObj,
		Key:     key,
		Version: version,
		Message: err.Error(),
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

func (obj *Obj) raiseReleasesTruncated(key string, err error) {
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("code", "releases_truncated").
		Str("key", key).
		Err(err).
		Msg("release listing exceeded processing cap; deletion disabled for this cycle")
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "releases_truncated",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonUpstreamUnavailable,
		Key:     key,
		Message: err.Error(),
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

// raiseListingModeConflict records a sticky-mode conflict: a tags key started reporting releases.
// The key freezes until an operator clears state or renames it; the warning repeats by design.
func (obj *Obj) raiseListingModeConflict(key string) {
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("code", "listing_mode_conflict").
		Str("key", key).
		Msg("upstream now publishes releases while the key is pinned to tag listing; key updates are frozen until the operator resolves the conflict")
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "listing_mode_conflict",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusError,
		Reason:  stcode.LogReasonUpstreamConflict,
		Key:     key,
		Message: "upstream started publishing releases while this key is pinned to tag listing; all updates for the key are frozen. Wipe the key state to re-decide the listing mode, or re-add the repository under a new name",
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
}

func (obj *Obj) raiseKeyUnavailable(key string, err error) {
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("code", "upstream_unavailable").
		Str("key", key).
		Str("error", err.Error()).
		Msg("key unavailable")
	if statsObj := obj.cycleStats(); statsObj != nil {
		statsObj.keysUnavailable.Add(1)
	}
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "upstream_unavailable",
		Scope:   stcode.LogScopeKey,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonUpstreamUnavailable,
		Key:     key,
		Message: err.Error(),
	}); diagErr == nil {
		if statsObj := obj.cycleStats(); statsObj != nil {
			statsObj.diagnosticsRaised.Add(1)
		}
	}
	obj.metricsObj.recordKeyUnavailable()
}

func (obj *Obj) clearKeyUnavailable(key string) {
	if err := obj.stateObj.ClearDiagnostic(state.DiagnosticKeyObj{
		Code:  "upstream_unavailable",
		Scope: stcode.LogScopeKey,
		Key:   key,
	}); err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("error", err.Error()).
			Msg("failed to clear key availability diagnostic")
	}
}

// // // // // // // // // //

func (obj *Obj) markVersionTerminalSuccess(ctx context.Context, key string, version string) {
	obj.clearPermanentFailure(ctx, key, version)
	if err := obj.stateObj.ClearVersionDiagnostics(key, version); err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("version", version).
			Str("error", err.Error()).
			Msg("failed to clear version diagnostics")
	}
}

// // // // // // // // // //

// recordPermanentFailure records a deterministic failure in memory and in the durable quarantine.
func (obj *Obj) recordPermanentFailure(ctx context.Context, key string, version string, refSHA string, code string, message string) {
	obj.permFailMu.Lock()
	obj.permFailMap[missKeyObj{key: key, version: version}] = refSHA
	obj.markQuarantineDirtyLocked(key)
	obj.permFailMu.Unlock()
	if obj.storageObj == nil {
		return
	}

	err := obj.storageObj.PutIngestFailure(ctx, core.IngestFailureObj{
		Key:     key,
		Version: version,
		RefSHA:  refSHA,
		Code:    code,
		Message: boundedFailureMessage(message),
		Policy:  core.IngestFailurePolicy,
	})
	if err == nil {
		return
	}
	if errors.Is(err, storage.ErrIngestFailureCap) {
		obj.warnQuarantineCapOnce(key)
		return
	}
	obj.logObj.Warn().
		Str("component", "rescan").
		Str("key", key).
		Str("version", version).
		Str("code", code).
		Str("error", err.Error()).
		Msg("failed to persist ingest failure quarantine")
}

// permanentFailureSkip reports whether the version already failed for the same SHA, including unknown SHA.
func (obj *Obj) permanentFailureSkip(key string, version string, refSHA string) bool {
	obj.permFailMu.Lock()
	defer obj.permFailMu.Unlock()
	failedSHA, ok := obj.permFailMap[missKeyObj{key: key, version: version}]
	return ok && failedSHA == refSHA
}

func (obj *Obj) clearPermanentFailure(ctx context.Context, key string, version string) {
	obj.permFailMu.Lock()
	missObj := missKeyObj{key: key, version: version}
	_, ok := obj.permFailMap[missObj]
	if ok {
		delete(obj.permFailMap, missObj)
	}
	_, loadMiss := obj.permFailLoadMissObj[key]
	obj.permFailMu.Unlock()
	if (!ok && !loadMiss) || obj.storageObj == nil {
		return
	}
	if err := obj.storageObj.DeleteIngestFailure(ctx, key, version); err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("version", version).
			Str("error", err.Error()).
			Msg("failed to clear ingest failure quarantine")
	}
}

func (obj *Obj) forceClearPermanentFailure(ctx context.Context, key string, version string) {
	obj.permFailMu.Lock()
	delete(obj.permFailMap, missKeyObj{key: key, version: version})
	obj.permFailMu.Unlock()
	if obj.storageObj == nil {
		return
	}
	if err := obj.storageObj.DeleteIngestFailure(ctx, key, version); err != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Str("key", key).
			Str("version", version).
			Str("error", err.Error()).
			Msg("failed to clear ingest failure quarantine")
	}
}
