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
	closeMu       sync.RWMutex
	activeWG      sync.WaitGroup
	rootCtx       context.Context
	rootCancel    context.CancelFunc
	writeMu       sync.Mutex
	publishSem    chan struct{}

	lastHardLimitCompact time.Time

	realBytesCache uint64
	realBytesAt    time.Time

	hotBytesCache uint64
	hotBytesAt    time.Time

	hotActiveMu  sync.Mutex
	hotActiveObj map[string]int
	hotDeleteObj map[string]struct{}

	hotEnforceWalks     atomic.Int64
	hotEnforceWalkNanos atomic.Int64

	flightMu  sync.Mutex
	flightMap map[string]*artifactFlightObj
	buildSem  chan struct{}

	gcLoopDone chan struct{}
	gcTrigger  chan struct{}

	metricRegMu  sync.Mutex
	metricRegArr []metric.Registration

	verifySampleCounter atomic.Uint64

	durableVerifySampleCounter atomic.Uint64

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
