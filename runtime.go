package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/cache"
	"github.com/voluminor/yggvault/mod/cli"
	"github.com/voluminor/yggvault/mod/logger"
	"github.com/voluminor/yggvault/mod/maintenance"
	"github.com/voluminor/yggvault/mod/mesh"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/rescan"
	"github.com/voluminor/yggvault/mod/server"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/mod/telemetry"
	"github.com/voluminor/yggvault/target"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cDefaultShutdownBudget — fallback graceful-shutdown budget when shutdown_timeout is not set.
	cDefaultShutdownBudget = 10 * time.Second

	// cStorageCloseBudget — guaranteed budget for storage.Close: integrity (WAL checkpoint,
	// step 6) is not sacrificed to draining. The real bound is storage's internal timers.
	cStorageCloseBudget = 30 * time.Second
)

// //

type runtimeObj struct {
	configObj *stconf.ConfigObj
	loggerObj *logger.Obj
	telemetry *telemetry.Obj
	storage   *storage.Obj
	overlay   *overlay.Obj
	state     *state.Obj
	cache     *cache.Obj
	mesh      *mesh.Obj
	source    *source.Obj
	archive   *archive.Obj
	rescan    *rescan.Obj
	server    *server.Obj
	profiling *http.Server // pprof loopback listener (nil when profiling.enabled=false)

	reconcileDone chan struct{}
}

// // // // // // // // // //

func archiveLimitsFromConfig(configObj *stconf.ConfigObj) archive.LimitsObj {
	return archive.LimitsObj{
		MaxArchiveSize:         uint64(configObj.Storage.ArchiveLimits.Size.Compressed),
		MaxArchiveUnpackedSize: uint64(configObj.Storage.ArchiveLimits.Size.Unpacked),
		MaxArchiveFileBytes:    uint64(configObj.Storage.ArchiveLimits.Size.PerFile),
		MaxArchiveFiles:        configObj.Storage.ArchiveLimits.Entries.Count,
		MaxArchivePathBytes:    configObj.Storage.ArchiveLimits.Entries.PathBytes,
	}
}

// // // // // // // // // //

func runRuntime(bootObj *cli.Obj) error {
	rt := &runtimeObj{configObj: bootObj.Config, loggerObj: bootObj.Logger}

	signalCtx, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()

	defer rt.shutdown()

	if err := rt.build(signalCtx); err != nil {
		rt.loggerObj.XErr(err).Msg("runtime startup failed")
		return err
	}
	if err := rt.start(signalCtx); err != nil {
		rt.loggerObj.XErr(err).Msg("runtime startup failed")
		return err
	}

	rt.loggerObj.Zero().Info().
		Str("version", target.Version).
		Str("hash", target.Hash[len(target.Hash)-8:]).
		Str("domain", rt.configObj.Web.Server.Domain).
		Bool("ygg", rt.mesh.Enabled()).
		Bool("metrics", rt.telemetry.Enabled()).
		Msg("runtime ready; press Ctrl-C to stop")

	<-signalCtx.Done()
	rt.loggerObj.Zero().Info().Msg("shutdown signal received")
	return nil
}

func (rt *runtimeObj) build(ctx context.Context) error {
	configObj := rt.configObj
	var err error

	if rt.telemetry, err = telemetry.New(configObj); err != nil {
		return fmt.Errorf("init telemetry: %w", err)
	}
	if rt.storage, err = storage.New(ctx, configObj, *rt.loggerObj.Zero()); err != nil {
		return fmt.Errorf("open storage: %w", err)
	}
	if rt.overlay, err = overlay.New(configObj); err != nil {
		return fmt.Errorf("init overlay: %w", err)
	}
	if rt.state, err = state.New(configObj); err != nil {
		return fmt.Errorf("init state: %w", err)
	}
	buildGateObj := cache.NewBuildGate(configObj.Cache.BuildMaxParallel)
	rt.cache = cache.New(configObj.Cache, buildGateObj)
	if rt.mesh, err = mesh.New(configObj, *rt.loggerObj.Zero()); err != nil {
		return fmt.Errorf("start mesh: %w", err)
	}
	if rt.source, err = source.New(configObj, rt.mesh); err != nil {
		return fmt.Errorf("init source: %w", err)
	}
	if rt.archive, err = archive.New(archiveLimitsFromConfig(configObj)); err != nil {
		return fmt.Errorf("init archive: %w", err)
	}

	yggHost := ""
	if rt.mesh.Enabled() {
		yggHost = rt.mesh.Host()
	}
	rt.rescan = rescan.New(configObj, rt.source, rt.storage, rt.overlay, rt.state, rt.archive, yggHost, *rt.loggerObj.Zero())

	rt.applyKeySourceReconcile(ctx)

	if rt.server, err = server.New(server.DepsObj{
		Config:    configObj,
		Storage:   rt.storage,
		State:     rt.state,
		Overlay:   rt.overlay,
		Cache:     rt.cache,
		Telemetry: rt.telemetry,
		Composer:  rt.rescan,
		Mesh:      rt.mesh,
		Log:       *rt.loggerObj.Zero(),
		BuildGate: buildGateObj,
	}); err != nil {
		return fmt.Errorf("init server: %w", err)
	}

	if err = rt.registerMetrics(); err != nil {
		return err
	}
	rt.runFormatSelfTest(ctx, yggHost)
	return nil
}

func (rt *runtimeObj) registerMetrics() error {
	if !rt.telemetry.Enabled() {
		return nil
	}
	meterObj := rt.telemetry.Meter(telemetry.GroupCache)
	if err := rt.storage.RegisterMetrics(meterObj); err != nil {
		return fmt.Errorf("register storage metrics: %w", err)
	}
	if err := rt.cache.RegisterMetrics(meterObj); err != nil {
		return fmt.Errorf("register cache metrics: %w", err)
	}
	if err := rt.source.RegisterMetrics(rt.telemetry.Meter(telemetry.GroupRescan)); err != nil {
		return fmt.Errorf("register source metrics: %w", err)
	}
	if err := rt.rescan.RegisterMetrics(rt.telemetry.Meter(telemetry.GroupRescan)); err != nil {
		return fmt.Errorf("register rescan metrics: %w", err)
	}
	if err := rt.state.RegisterMetrics(rt.telemetry.Meter(telemetry.GroupErrors)); err != nil {
		return fmt.Errorf("register errors metrics: %w", err)
	}
	if err := rt.loggerObj.RegisterMetrics(rt.telemetry.Meter(telemetry.GroupInternal)); err != nil {
		return fmt.Errorf("register logger metrics: %w", err)
	}
	return nil
}

func (rt *runtimeObj) applyKeySourceReconcile(ctx context.Context) {
	suppressedSet, e1Arr, verdictArr, listErr := maintenance.ReconcileKeySources(ctx, rt.storage, rt.configObj)
	zlog := rt.loggerObj.Zero()
	if listErr != nil {
		zlog.Warn().Err(listErr).Msg("key_source reconciliation skipped: could not read bindings; name-to-url changes are not enforced this start")
	}
	for i := range verdictArr {
		zlog.Error().
			Str("key", verdictArr[i].Key).
			Str("reconcile", verdictArr[i].Code).
			Msg(verdictArr[i].Detail)
	}
	if len(suppressedSet) > 0 {
		rt.rescan.SetSuppressed(suppressedSet)
	}
	now := time.Now().UTC()
	for _, key := range e1Arr {
		_ = rt.state.MarkUnavailable(key, now)
	}
}

func (rt *runtimeObj) runFormatSelfTest(ctx context.Context, yggHost string) {
	zlog := rt.loggerObj.Zero()
	listenerArr := maintenance.ListenersFromConfig(rt.configObj, yggHost)
	driftArr, err := maintenance.SelfTestFormats(ctx, rt.storage, rt.overlay, listenerArr)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		zlog.Warn().Err(err).Msg("format self-test could not complete; continuing")
		return
	}
	if len(driftArr) == 0 {
		return
	}
	for i := range driftArr {
		driftItemObj := driftArr[i]
		zlog.Error().
			Str("materializer", driftItemObj.MaterializerID).
			Uint32("format_version", driftItemObj.FormatVersion).
			Str("key", driftItemObj.Key).
			Str("version", driftItemObj.Version).
			Str("stored_body_hash", driftItemObj.StoredHash).
			Str("rebuilt_body_hash", driftItemObj.RebuiltHash).
			Msg("format self-test drift: rebuilt artifact body hash differs from stored; run rebuild-cache under the current toolchain")
	}
	_ = rt.state.RaiseDiagnostic(state.DiagnosticObj{
		Code:    "format_selftest_drift",
		Scope:   stcode.LogScopeGlobal,
		Impact:  stcode.OperationalStatusDegraded,
		Reason:  stcode.LogReasonArtifactMaterializationFailed,
		Message: "materialized artifact body hash drift detected at startup; run rebuild-cache",
	})
}

func (rt *runtimeObj) maybeReconcileArtifacts(ctx context.Context) {
	yggHost := ""
	if rt.mesh.Enabled() {
		yggHost = rt.mesh.Host()
	}
	current := maintenance.ArtifactLayoutFingerprint()
	stored, ok, err := rt.storage.GetGlobal(ctx, maintenance.ArtifactLayoutGlobalKey)
	if err != nil {
		rt.loggerObj.Zero().Warn().Err(err).Msg("artifact-layout check failed; skipping background reconcile")
		return
	}
	if ok && stored == current {
		rt.loggerObj.Zero().Debug().
			Str("layout", current).
			Msg("artifact layout current; background reconcile skipped")
		return
	}
	startEventObj := rt.loggerObj.Zero().Warn().
		Bool("stored_layout_present", ok).
		Str("current_layout", current)
	if ok {
		startEventObj = startEventObj.Str("stored_layout", stored)
	}
	startEventObj.Msg("artifact layout mismatch; background artifact reconcile started")
	rt.reconcileDone = make(chan struct{})
	go rt.runArtifactReconcile(ctx, yggHost, current)
}

func (rt *runtimeObj) runArtifactReconcile(ctx context.Context, yggHost string, fingerprint string) {
	defer close(rt.reconcileDone)
	startTime := time.Now()
	listenerArr := maintenance.ListenersFromConfig(rt.configObj, yggHost)
	resultObj, err := maintenance.RebuildArtifacts(ctx, rt.storage, rt.overlay, listenerArr)
	if err != nil {
		if ctx.Err() == nil {
			rt.loggerObj.Zero().Warn().
				Err(err).
				Dur("elapsed", time.Since(startTime)).
				Msg("background artifact reconcile failed; will retry on next start")
		}
		return
	}
	if err = rt.storage.SetGlobal(ctx, maintenance.ArtifactLayoutGlobalKey, fingerprint); err != nil {
		rt.loggerObj.Zero().Warn().
			Err(err).
			Dur("elapsed", time.Since(startTime)).
			Msg("failed to persist artifact-layout fingerprint")
		return
	}
	changed := resultObj.Drift > 0 || resultObj.Created > 0 || resultObj.Updated > 0 || resultObj.Pruned > 0
	completeEventObj := rt.loggerObj.Zero().Info()
	if changed {
		completeEventObj = rt.loggerObj.Zero().Warn()
	}
	completeEventObj.
		Uint64("scanned", resultObj.Scanned).
		Uint64("drift", resultObj.Drift).
		Uint64("created", resultObj.Created).
		Uint64("updated", resultObj.Updated).
		Uint64("pruned", resultObj.Pruned).
		Bool("changed", changed).
		Dur("elapsed", time.Since(startTime)).
		Msg("background artifact reconcile complete")
}

func (rt *runtimeObj) start(ctx context.Context) error {
	rt.telemetry.Start(ctx)
	if err := rt.startProfiling(); err != nil {
		rt.loggerObj.Zero().Warn().Err(err).Msg("pprof listener failed to start; continuing without profiling")
	}
	if err := rt.server.Start(); err != nil {
		return fmt.Errorf("start listeners: %w", err)
	}
	rt.rescan.Start()
	rt.rescan.Trigger()
	rt.maybeReconcileArtifacts(ctx)
	return nil
}

// // // // // // // // // //

func (rt *runtimeObj) shutdown() {
	budget := rt.configObj.ShutdownTimeout
	if budget <= 0 {
		budget = cDefaultShutdownBudget
	}
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), budget)
	defer cancelDrain()

	if rt.profiling != nil {
		rt.logClose("pprof", rt.profiling.Shutdown(drainCtx))
	}
	if rt.server != nil {
		rt.logClose("server", rt.server.Shutdown(drainCtx))
	}
	if rt.rescan != nil {
		rt.logClose("rescan", rt.rescan.Close(drainCtx))
	}
	if rt.source != nil {
		rt.logClose("source", rt.source.Close(drainCtx))
	}
	if rt.mesh != nil {
		rt.logClose("mesh", rt.mesh.Close(drainCtx))
	}
	if rt.telemetry != nil {
		rt.logClose("telemetry", rt.telemetry.Close(drainCtx))
	}
	if rt.reconcileDone != nil {
		select {
		case <-rt.reconcileDone:
		case <-drainCtx.Done():
		}
	}
	if rt.storage != nil {
		storageCtx, cancelStorage := context.WithTimeout(context.Background(), cStorageCloseBudget)
		rt.logClose("storage", rt.storage.Close(storageCtx))
		cancelStorage()
	}
	if rt.loggerObj != nil {
		rt.logClose("logger", rt.loggerObj.Close())
	}
}

func (rt *runtimeObj) logClose(component string, err error) {
	if err == nil || rt.loggerObj == nil {
		return
	}
	rt.loggerObj.Zero().Error().Err(err).Str("component", component).Msg("shutdown step failed")
}
