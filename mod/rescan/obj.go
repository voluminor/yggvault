package rescan

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"

	"github.com/voluminor/yggvault/mod/archive"
	"github.com/voluminor/yggvault/mod/overlay"
	"github.com/voluminor/yggvault/mod/source"
	"github.com/voluminor/yggvault/mod/state"
	"github.com/voluminor/yggvault/mod/storage"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// SourceInterface is the outbound layer orchestrated by rescan and faked in tests.
// BrotherDial returns source.BrotherSessionInterface, so *source.Obj satisfies it directly.
type SourceInterface interface {
	Discover(ctx context.Context, key string, rootURL string) (source.DiscoveryResultObj, error)
	Releases(ctx context.Context, sourceURL string, depth uint) ([]source.GitReleaseObj, bool, error)
	Tags(ctx context.Context, sourceURL string, depth uint) ([]source.GitReleaseObj, bool, error)
	Refs(ctx context.Context, sourceURL string) (map[string]string, error)
	FetchArchive(ctx context.Context, reqObj source.GitFetchRequestObj) (source.GitFetchResultObj, error)
	PublicMirrorVersions(ctx context.Context, rootURL string, remoteKey string, skip func(version string) bool) (source.PublicMirrorListingObj, error)
	BrotherDial(ctx context.Context, localKey string, remoteKey string, brotherURL string) (source.BrotherSessionInterface, error)
}

// // // // // // // // // //

type missKeyObj struct {
	key     string
	version string
}

type cycleStatsObj struct {
	keysProcessed     atomic.Uint64
	keysSuppressed    atomic.Uint64
	keysUnavailable   atomic.Uint64
	diagnosticsRaised atomic.Uint64
	versionsPublished atomic.Uint64
	versionsDegraded  atomic.Uint64
}

// Obj supervises rescans.
// Dependencies are immutable after New; mutable cycle state has dedicated locks.
type Obj struct {
	sourceObj  SourceInterface
	storageObj *storage.Obj
	overlayObj *overlay.Obj
	stateObj   *state.Obj
	archiveObj *archive.Obj
	configObj  *stconf.ConfigObj

	listenerArr []overlay.ListenerCtxObj
	keyArr      []string

	rootCtx    context.Context
	rootCancel context.CancelFunc
	loopDone   chan struct{}
	trigger    chan struct{}

	// buildSem limits concurrent ArtifactDigest builds during ingest.
	// ArtifactDigest does not gate itself, so rescan owns the CPU and disk budget.
	buildSem chan struct{}

	// metricsObj holds rescan instruments; nil before RegisterMetrics for disabled telemetry.
	metricsObj *rescanMetricsObj

	missMu  sync.Mutex
	missMap map[missKeyObj]uint

	// permFailMap is the hot skip cache over durable ingest_failures quarantine.
	permFailMu  sync.Mutex
	permFailMap map[missKeyObj]string
	// Keys whose startup durable load failed need one-cycle durable clears despite hot-cache misses.
	permFailLoadMissObj map[string]struct{}
	// quarantineDirtyObj marks keys with pending quarantine bookkeeping (in-memory entries, a failed
	// startup load, or durable rows to reconcile). Healthy keys stay absent so the per-cycle summary skips
	// the durable ListIngestFailures round-trip for them.
	quarantineDirtyObj map[string]struct{}

	quarantineCapMu      sync.Mutex
	quarantineCapWarnObj map[string]uint64

	composerMu        sync.RWMutex
	composerNames     []string
	composerKeyByName map[string]string

	// suppressedSet contains keys excluded by boot name-to-URL checks; local data stays untouched.
	suppressedMu  sync.RWMutex
	suppressedSet map[string]struct{}

	// cycleCount drives periodic full index refreshes despite index-skip.
	cycleCount atomic.Uint64

	activeStats atomic.Pointer[cycleStatsObj]
	logObj      zerolog.Logger

	closeMu     sync.Mutex
	startedFlag bool
	closedFlag  bool
}

// // // // // // // // // //

// New builds a rescan supervisor.
// yggHost is the Yggdrasil listener entry host; empty means Yggdrasil is disabled.
// The background loop is not started here; callers use Start and optionally RunOnce.
func New(
	configObj *stconf.ConfigObj,
	sourceObj SourceInterface,
	storageObj *storage.Obj,
	overlayObj *overlay.Obj,
	stateObj *state.Obj,
	archiveObj *archive.Obj,
	yggHost string,
	logArr ...zerolog.Logger,
) *Obj {
	listenerArr := overlay.ListenerContexts(configObj.Web.Server.Domain, configObj.Web.Routing.Prefix, yggHost)

	keyArr := make([]string, 0, len(configObj.ReleaseMirrors))
	for keyText := range configObj.ReleaseMirrors {
		keyArr = append(keyArr, keyText)
	}
	sort.Strings(keyArr)

	buildParallel := int(configObj.Storage.OverlayBuildMaxParallel)
	if buildParallel < 1 {
		buildParallel = 1
	}

	rootCtx, rootCancel := context.WithCancel(context.Background())
	logObj := zerolog.Nop()
	if len(logArr) > 0 {
		logObj = logArr[0]
	}
	obj := &Obj{
		sourceObj:            sourceObj,
		storageObj:           storageObj,
		overlayObj:           overlayObj,
		stateObj:             stateObj,
		archiveObj:           archiveObj,
		configObj:            configObj,
		listenerArr:          listenerArr,
		keyArr:               keyArr,
		rootCtx:              rootCtx,
		rootCancel:           rootCancel,
		loopDone:             make(chan struct{}),
		trigger:              make(chan struct{}, 1),
		buildSem:             make(chan struct{}, buildParallel),
		missMap:              make(map[missKeyObj]uint),
		permFailMap:          make(map[missKeyObj]string),
		permFailLoadMissObj:  make(map[string]struct{}),
		quarantineDirtyObj:   make(map[string]struct{}),
		quarantineCapWarnObj: make(map[string]uint64),
		logObj:               logObj,
	}
	obj.loadPermanentFailures()
	return obj
}

// SetSuppressed sets keys excluded by boot name-to-URL checks.
// Rescan skips them without touching local data, preserving degraded-first serving.
func (obj *Obj) SetSuppressed(keySet map[string]struct{}) {
	obj.suppressedMu.Lock()
	obj.suppressedSet = keySet
	obj.suppressedMu.Unlock()
}

func (obj *Obj) suppressedKey(key string) bool {
	obj.suppressedMu.RLock()
	_, ok := obj.suppressedSet[key]
	obj.suppressedMu.RUnlock()
	return ok
}
