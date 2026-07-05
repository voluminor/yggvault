package rescan

import (
	"fmt"
	"io"
	"os"
	"runtime/debug"

	"github.com/zeebo/blake3"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

func hashFile(pathText string) (core.HashObj, error) {
	fileObj, err := os.Open(pathText)
	if err != nil {
		return core.HashObj{}, err
	}
	defer fileObj.Close()
	hasherObj := blake3.New()
	if _, err := io.Copy(hasherObj, fileObj); err != nil {
		return core.HashObj{}, err
	}
	return core.HashFromHasher(hasherObj), nil
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
	if diagErr := obj.stateObj.RaiseDiagnostic(state.DiagnosticObj{
		Code:    code,
		Scope:   stcode.LogScopeVersion,
		Impact:  stcode.OperationalStatusError,
		Reason:  stcode.LogReasonArtifactMaterializationFailed,
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

// // // // // // // // // //

// recordPermanentFailure remembers the tag SHA that produced a deterministic ingest failure.
func (obj *Obj) recordPermanentFailure(key string, version string, refSHA string) {
	obj.permFailMu.Lock()
	defer obj.permFailMu.Unlock()
	obj.permFailMap[missKeyObj{key: key, version: version}] = refSHA
}

// permanentFailureSkip reports whether the version already failed for the same SHA, including unknown SHA.
func (obj *Obj) permanentFailureSkip(key string, version string, refSHA string) bool {
	obj.permFailMu.Lock()
	defer obj.permFailMu.Unlock()
	failedSHA, ok := obj.permFailMap[missKeyObj{key: key, version: version}]
	return ok && failedSHA == refSHA
}

func (obj *Obj) clearPermanentFailure(key string, version string) {
	obj.permFailMu.Lock()
	defer obj.permFailMu.Unlock()
	delete(obj.permFailMap, missKeyObj{key: key, version: version})
}
