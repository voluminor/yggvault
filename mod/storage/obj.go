package storage

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"go.opentelemetry.io/otel/metric"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/mod/storage/pebblestore"
	"github.com/voluminor/yggvault/mod/storage/sqliteindex"
	"github.com/voluminor/yggvault/target/stcode"
	stcfg "github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

// cListenerGlobal sets the persisted listener_id for host-independent artifacts.
// The value comes from the domain enum, not a hard-coded string.
var cListenerGlobal = stcode.ListenerGlobal.String()

// // // // // // // // // //

const (
	cFileIndexName = "index.sqlite"
	cDirPebbleName = "pebble"
	cDirHotName    = "hot"
	cDirTempName   = "tmp"

	cModeFile    = core.ModeFile
	cModeSymlink = core.ModeSymlink

	cDefaultFormat = core.DefaultFormat

	cMaxEvidenceBytes       = 64 * 1024
	cMaxEventTypeBytes      = 64
	cMaxEventMessageBytes   = 4096
	cMaxReleaseNotesBytes   = 128 * 1024
	cMaxETagBytes           = 256
	cMaxDegradedReasonBytes = 1024

	cHotAccessUpdateInterval = time.Minute

	// cHotVerifySampleRate verifies 1 out of N hot-file opens in sampled mode.
	cHotVerifySampleRate = 16

	cCacheAreaDurable = "durable"
	cCacheAreaHot     = "hot"

	cQuotaCheckAdmission    = "admission"
	cQuotaCheckAfterEvict   = "after_eviction"
	cQuotaCheckHardLimit    = "hard_limit"
	cQuotaCheckProtectedFit = "protected_fit"

	cArchiveCheckEntryHardCap = "entry_hard_cap"
	cArchiveCheckFileCount    = "file_count"
	cArchiveCheckFileBytes    = "file_bytes"
	cArchiveCheckPathBytes    = "path_bytes"
	cArchiveCheckUnpackedSize = "unpacked_size"

	cArtifactCheckMetadataSize = "metadata_size"
	cArtifactCheckOutputSize   = "output_size"
	cArtifactCheckBodyHash     = "body_hash"
	cArtifactCheckBodySize     = "body_size"
	cArtifactCheckHotFile      = "hot_file"
	cArtifactCheckStaleHash    = "stale_hash"
	cArtifactCheckStaleSize    = "stale_size"
	cArtifactCheckPanic        = "panic"

	cPublishCheckRewriteSetCount       = "rewrite_set_count"
	cPublishCheckArtifactMetadataCount = "artifact_metadata_count"
)

var keyPatternObj = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{1,38}[a-z0-9]$`)

var smallIDPatternObj = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)

// //

// Obj combines the durable SQLite index, the Pebble blob/tree store and the hot artifact cache.
type Obj struct {
	configObj *stcfg.ConfigObj
	rootPath  string
	indexPath string
	pebbleDir string
	hotDir    string
	tempDir   string
	logObj    zerolog.Logger

	indexObj       *sqliteindex.Obj
	pebbleStoreObj *pebblestore.Obj

	closedFlag    bool
	closeDoneChan chan struct{}
	closeErr      error
	closeMu       sync.RWMutex // leaf lifecycle lock; no storage locks may be taken under it
	activeWG      sync.WaitGroup
	rootCtx       context.Context
	rootCancel    context.CancelFunc
	writeMu       sync.Mutex // serializes durable/hot mutations; hotActiveMu is the only lock allowed underneath
	publishSem    chan struct{}

	// lastHardLimitCompact debounces backstop compaction storms under writeMu.
	lastHardLimitCompact time.Time

	// realBytesCache/realBytesAt cache physical disk usage for the admission gate.
	// DiskSpaceUsage takes the global Pebble mutex and walks the LSM, so it is too expensive on every publish.
	realBytesCache uint64
	realBytesAt    time.Time

	// hotBytesCache/hotBytesAt cache the hot-cache size estimate under writeMu.
	// A full dirSize is O(hot-files), so it is refreshed on eviction paths and by TTL.
	hotBytesCache uint64
	hotBytesAt    time.Time

	hotActiveMu  sync.Mutex // leaf lock for active hot files; taken alone or under writeMu only
	hotActiveObj map[string]int
	hotDeleteObj map[string]struct{}

	// hotEnforceWalks/hotEnforceWalkNanos measure the cost of slow-path hot-budget enforcement.
	// Writes happen under writeMu, reads from metric callbacks, hence the fields are atomic.
	hotEnforceWalks     atomic.Int64
	hotEnforceWalkNanos atomic.Int64

	flightMu  sync.Mutex // leaf lock for the hot-build singleflight map
	flightMap map[string]*artifactFlightObj
	buildSem  chan struct{}

	gcLoopDone chan struct{}
	gcTrigger  chan struct{}

	metricRegMu  sync.Mutex // leaf lock for OTel callback registrations
	metricRegArr []metric.Registration

	verifySampleCounter atomic.Uint64

	// durableVerifySampleCounter drives sampled verify_on_read for durable reads.
	durableVerifySampleCounter atomic.Uint64

	// inFlightSlots bounds the number of simultaneously materialized in-RAM objects on read.
	// nil means the budget is disabled.
	inFlightSlots chan struct{}
}

// InspectObj holds a snapshot of paths, entity counts and disk usage.
type InspectObj struct {
	RootPath            string
	SQLitePath          string
	PebblePath          string
	HotPath             string
	VersionCount        uint64
	BlobCount           uint64
	ArtifactCount       uint64
	HistoryEventCount   uint64
	PebbleDiskBytes     uint64
	PebbleRealDiskBytes uint64
	SQLiteDiskBytes     uint64
	HotBytes            uint64
}

// HotFileObj holds an open descriptor of a materialized hot file.
// cleanup releases the shared-file reference after File is closed.
type HotFileObj struct {
	Path      string
	File      *os.File
	SizeBytes uint64
	BodyHash  core.HashObj
	cleanup   func() error
}

type hotSharedFileObj struct {
	path      string
	sizeBytes uint64
	bodyHash  core.HashObj
	retain    func() func() error
	cleanup   func() error
	refs      atomic.Int64
	onceObj   sync.Once
}

type artifactFlightObj struct {
	ctx      context.Context
	cancel   context.CancelFunc
	doneChan chan struct{}
	result   any
	err      error
	waiters  int
	doneFlag bool
}

// ArtifactBuilderInterface writes artifact content to a writer.
type ArtifactBuilderInterface interface {
	Build(ctx context.Context, writer io.Writer) error
}

// //

func artifactIdentityText(artifactObj core.ArtifactObj) string {
	return fmt.Sprintf(
		"%s/%s/%s/%s/%s/%s/%d/%d",
		artifactObj.MaterializerID,
		artifactObj.ArtifactKind,
		artifactObj.ListenerID,
		artifactObj.Key,
		artifactObj.Version,
		artifactObj.BodyHash.Hex(),
		artifactObj.SizeBytes,
		artifactObj.FormatVersion,
	)
}

func hotArtifactPath(root string, artifactObj core.ArtifactObj) string {
	return filepath.Join(
		root,
		artifactObj.MaterializerID,
		artifactObj.ListenerID,
		artifactObj.Key,
		artifactObj.Version,
		fmt.Sprintf("%s-%d-f%d.%s", artifactObj.BodyHash.Hex(), artifactObj.SizeBytes, artifactObj.FormatVersion, artifactObj.ArtifactKind),
	)
}
