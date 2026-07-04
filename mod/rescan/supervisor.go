package rescan

import (
	"context"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

const (
	// cDefaultRescanInterval is the fallback for invalid intervals; it matches the schema default.
	cDefaultRescanInterval = 6 * time.Hour

	// cMinRescanInterval prevents invalid tickers and tight loops against upstreams.
	cMinRescanInterval = time.Minute

	// cIndexSkipRefreshEvery forces a full index fetch periodically to catch stale brother index hashes.
	cIndexSkipRefreshEvery uint64 = 12
)

// //

func safeRescanInterval(configured time.Duration) time.Duration {
	if configured <= 0 {
		return cDefaultRescanInterval
	}
	if configured < cMinRescanInterval {
		return cMinRescanInterval
	}
	return configured
}

// //

// Start launches the background loop once.
// Ticks and triggers coalesce while a cycle is running, so cycles never overlap.
func (obj *Obj) Start() {
	obj.closeMu.Lock()
	if obj.startedFlag || obj.closedFlag {
		obj.closeMu.Unlock()
		return
	}
	obj.startedFlag = true
	obj.closeMu.Unlock()
	obj.logObj.Info().
		Str("component", "rescan").
		Int("keys_total", len(obj.keyArr)).
		Dur("interval", safeRescanInterval(obj.configObj.Rescan.Interval)).
		Uint("max_parallel", obj.configObj.Rescan.MaxParallelKeys).
		Int("build_parallel", cap(obj.buildSem)).
		Msg("rescan supervisor started")
	go obj.runLoop()
}

// Trigger requests an extra cycle; duplicate pending signals are dropped.
func (obj *Obj) Trigger() {
	select {
	case obj.trigger <- struct{}{}:
	default:
		obj.logObj.Debug().Str("component", "rescan").Msg("rescan trigger coalesced")
	}
}

// RunOnce runs one cycle synchronously for initial fill and tests.
// Do not call it in parallel with an active Start loop.
func (obj *Obj) RunOnce(ctx context.Context) {
	obj.runCycle(ctx)
}

// // // // // // // // // //

func (obj *Obj) cycleStats() *cycleStatsObj {
	return obj.activeStats.Load()
}

func (obj *Obj) cycleEvent(statsObj *cycleStatsObj) *zerolog.Event {
	if statsObj == nil {
		return obj.logObj.Debug()
	}
	if statsObj.versionsPublished.Load() > 0 ||
		statsObj.versionsDegraded.Load() > 0 ||
		statsObj.keysUnavailable.Load() > 0 ||
		statsObj.keysSuppressed.Load() > 0 ||
		statsObj.diagnosticsRaised.Load() > 0 {
		return obj.logObj.Info()
	}
	return obj.logObj.Debug()
}

func (obj *Obj) runLoop() {
	defer close(obj.loopDone)
	tickerObj := time.NewTicker(safeRescanInterval(obj.configObj.Rescan.Interval))
	defer tickerObj.Stop()
	for {
		select {
		case <-obj.rootCtx.Done():
			return
		case <-tickerObj.C:
		case <-obj.trigger:
		}
		obj.runCycle(obj.rootCtx)
	}
}

func (obj *Obj) runCycle(ctx context.Context) {
	// A panic in the cycle plumbing must not kill the process or the loop goroutine.
	defer obj.recoverPanic("cycle", "")
	cycleStart := time.Now().UTC()
	statsObj := &cycleStatsObj{}
	obj.activeStats.Store(statsObj)
	defer obj.activeStats.Store(nil)

	forceRefresh := obj.cycleCount.Add(1)%cIndexSkipRefreshEvery == 0

	maxParallel := int(obj.configObj.Rescan.MaxParallelKeys)
	if maxParallel < 1 {
		maxParallel = 1
	}
	semChan := make(chan struct{}, maxParallel)
	var wgObj sync.WaitGroup
	for _, keyText := range obj.keyArr {
		if ctx.Err() != nil {
			break
		}
		select {
		case semChan <- struct{}{}:
		case <-ctx.Done():
		}
		if ctx.Err() != nil {
			break
		}
		wgObj.Add(1)
		go func(keyText string) {
			defer wgObj.Done()
			defer func() { <-semChan }()
			defer obj.recoverPanic("key", keyText)
			obj.runKey(ctx, keyText, forceRefresh, cycleStart)
		}(keyText)
	}
	wgObj.Wait()

	if ctx.Err() != nil {
		obj.logObj.Warn().
			Str("component", "rescan").
			Err(ctx.Err()).
			Time("cycle_start", cycleStart).
			Dur("elapsed", time.Since(cycleStart)).
			Msg("rescan cycle canceled")
		return
	}
	obj.finishCycle(ctx)
	obj.stateObj.SetLastRescan(cycleStart)
	elapsed := time.Since(cycleStart)
	obj.metricsObj.recordCycle(elapsed)
	obj.cycleEvent(statsObj).
		Str("component", "rescan").
		Time("cycle_start", cycleStart).
		Dur("elapsed", elapsed).
		Int("keys_total", len(obj.keyArr)).
		Uint64("keys_processed", statsObj.keysProcessed.Load()).
		Uint64("keys_suppressed", statsObj.keysSuppressed.Load()).
		Uint64("keys_unavailable", statsObj.keysUnavailable.Load()).
		Uint64("versions_published", statsObj.versionsPublished.Load()).
		Uint64("versions_degraded", statsObj.versionsDegraded.Load()).
		Uint64("diagnostics_raised", statsObj.diagnosticsRaised.Load()).
		Bool("force_refresh", forceRefresh).
		Msg("rescan cycle completed")
}

// // // // // // // // // //

// Close cancels the loop and waits for exit.
// storage.Publish* calls are atomic, so there is no partial state to clean up.
func (obj *Obj) Close(ctx context.Context) error {
	obj.closeMu.Lock()
	if obj.closedFlag {
		obj.closeMu.Unlock()
		return nil
	}
	obj.closedFlag = true
	startedFlag := obj.startedFlag
	obj.closeMu.Unlock()

	obj.rootCancel()
	if !startedFlag {
		return nil
	}
	select {
	case <-obj.loopDone:
		obj.logObj.Info().Str("component", "rescan").Msg("rescan supervisor stopped")
		return nil
	case <-ctx.Done():
		obj.logObj.Warn().Str("component", "rescan").Err(ctx.Err()).Msg("rescan supervisor stop timed out")
		return ctx.Err()
	}
}
