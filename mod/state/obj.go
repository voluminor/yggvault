package state

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
)

// // // // // // // // // //

const (
	cMaxKeys                = 4096
	cDefaultMaxDiagnostics  = 4096
	cMaxDiagnosticCodeBytes = 128
	cMaxKeyBytes            = 256
	cMaxURLBytes            = 4096
	cMaxVersionBytes        = 256
	cMaxMessageBytes        = 4096
	cMaxHealthReasons       = 32
	cMaxScanFutureSkew      = 5 * time.Minute
)

// Obj holds mirror state and diagnostics.
// All mutations are serialized by lockObj; reads stay lock-free through the atomic SnapshotObj.
// Inactive diagnostics live in an intrusive list: an entry is in the list iff !active.
type Obj struct {
	selfObj *Obj
	lockObj sync.Mutex
	snapObj atomic.Pointer[SnapshotObj]

	keyMap             map[string]*keyMutObj
	keyOrder           []string
	keyIndex           map[string]int
	diagnosticMap      map[diagnosticKeyObj]*diagnosticRecordObj
	diagnosticByKey    map[string]map[diagnosticKeyObj]struct{}
	inactiveHead       *diagnosticRecordObj
	inactiveTail       *diagnosticRecordObj
	inactiveLen        int
	checksums          ChecksumSetObj
	lastRescan         time.Time
	generation         uint64
	droppedDiagnostics uint64
	permanentAt        uint32
	maxDiagnostics     int
	recentBuffer       int
}

// SnapshotObj is an immutable state snapshot published atomically.
// It is never mutated after publication; collection getters return copies.
type SnapshotObj struct {
	Checksums  ChecksumSetObj
	LastRescan time.Time
	Generation uint64

	healthObj     HealthViewObj
	keyArr        []KeyStateObj
	keyIndex      map[string]int
	diagnosticArr []DiagnosticViewObj
}

// ChecksumSetObj carries the content checksum — the cross-key page-freshness fingerprint (ETags).
type ChecksumSetObj struct {
	Content core.HashObj
}

// HealthViewObj exposes aggregate node health, reasons, and diagnostic counters.
type HealthViewObj struct {
	Status             stcode.OperationalStatusType
	Reasons            []stcode.LogReasonType
	DiagnosticsCount   uint64
	DroppedDiagnostics uint64
}

// KeyStateObj is the public state snapshot for one mirror.
// It includes classification, upstream availability, last scan, and version statistics.
type KeyStateObj struct {
	Key               string
	Status            stcode.OperationalStatusType
	Classification    stcode.SourceClassType
	Classified        bool
	SourceURL         string
	BrotherURL        string
	RemoteKey         string
	Availability      stcode.AvailabilityStatusType
	UnavailableCycles uint32
	LastScan          time.Time
	UpstreamPresent   bool
	LatestVersion     string
	VersionCount      uint64
	LastPublishTS     time.Time
}

// DiagnosticViewObj is the public snapshot of an active diagnostic with first/last-seen counters.
type DiagnosticViewObj struct {
	Code      string
	Scope     stcode.LogScopeType
	Impact    stcode.OperationalStatusType
	Reason    stcode.LogReasonType
	Key       string
	Version   string
	Message   string
	FirstSeen time.Time
	LastSeen  time.Time
	Count     uint64
}

// DiagnosticObj is the input for RaiseDiagnostic.
// Identity is code/scope/key/version; Impact and Reason affect health.
type DiagnosticObj struct {
	Code    string
	Scope   stcode.LogScopeType
	Impact  stcode.OperationalStatusType
	Reason  stcode.LogReasonType
	Key     string
	Version string
	Message string
}

// DiagnosticKeyObj identifies a diagnostic for ClearDiagnostic without aggregate or message fields.
type DiagnosticKeyObj struct {
	Code    string
	Scope   stcode.LogScopeType
	Key     string
	Version string
}

type diagnosticKeyObj struct {
	code    string
	scope   stcode.LogScopeType
	key     string
	version string
}

// MirrorStatsObj carries mirror statistics from rescan.
type MirrorStatsObj struct {
	Key           string
	LatestVersion string
	VersionCount  uint64
	LastPublishTS time.Time
}

// AvailabilityUpdateObj is one upstream availability scan result.
type AvailabilityUpdateObj struct {
	Key       string
	Available bool
	Present   bool
	ScanAt    time.Time
}

type keyMutObj struct {
	key               string
	classification    stcode.SourceClassType
	classified        bool
	sourceURL         string
	brotherURL        string
	remoteKey         string
	availability      stcode.AvailabilityStatusType
	unavailableCycles uint32
	lastScan          time.Time
	upstreamPresent   bool
	latestVersion     string
	versionCount      uint64
	lastPublishTS     time.Time
	reclassAllowed    bool
}

type diagnosticRecordObj struct {
	code      string
	scope     stcode.LogScopeType
	impact    stcode.OperationalStatusType
	reason    stcode.LogReasonType
	key       string
	version   string
	message   string
	firstSeen time.Time
	lastSeen  time.Time
	count     uint64
	active    bool

	inactivePrev *diagnosticRecordObj
	inactiveNext *diagnosticRecordObj
}
