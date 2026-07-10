package source

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	lightweigit "github.com/voluminor/lightweigit-loader"
	"golang.org/x/time/rate"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stcode"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cServiceName = "yggvault"

	cMaxAttempts = 3

	cAbsBlobCap = 256 << 20

	cAbsArchiveCap = 2 << 30

	cHealthMaxBytes = 64 << 10

	cSourceArchiveName = "source.archive"

	cFormatZip = "zip"

	cDefaultRequestTimeout = 30 * time.Second
	cMinRequestTimeout     = time.Second
)

// // // // // // // // // //

// MeshInterface is the narrow Yggdrasil node surface required by source.
// OwnsHost keeps Yggdrasil suffix policy in mesh, so source does not duplicate it.
type MeshInterface interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	OwnsHost(host string) bool
	Enabled() bool
}

// //

// Obj is the source module facade with routed HTTP clients, RPC dialing, limits, and retry policy.
type Obj struct {
	mesh MeshInterface

	metaClient     *http.Client
	refsClient     *http.Client
	downloadClient *http.Client
	downloadSem    chan struct{}

	retry retryObj

	maxArchiveSize      uint64
	maxBlobBytes        uint64
	maxFetchBytes       uint64
	maxFetchCount       uint
	requestTimeout      time.Duration
	maxDownloadDuration time.Duration
	routingPrefix       string

	rpcLimiter      *rate.Limiter
	upstreamLimiter *hostLimiterObj
	metricsObj      *sourceMetricsObj

	cred *credentialMatcherObj

	allowLoopback bool

	installedLoaderClient bool
}

// // // // // // // // // //

// DiscoveryResultObj is the source classification result; rescan writes it to state.
// For brother sources, WebAddr and YggAddr are learned public addresses used for transport fallback.
// SourceURL is the first source for git and the brother URL for brother sources.
type DiscoveryResultObj struct {
	Class      stcode.SourceClassType
	RemoteKey  string
	SourceURL  string
	BrotherURL string
	WebAddr    string
	YggAddr    string
}

// //

// GitReleaseObj is one git-source release after prereleases have been filtered out.
type GitReleaseObj struct {
	Version    string
	BodyMD     string
	ArchiveURL string
	Format     string
}

// GitFetchRequestObj requests downloading one version archive into DestDir.
type GitFetchRequestObj struct {
	Key        string
	Version    string
	ArchiveURL string
	Format     string
	DestDir    string
}

// GitFetchResultObj points to the downloaded archive; rescan performs extract and publish.
type GitFetchResultObj struct {
	ArchivePath string
	Format      string
	SizeBytes   uint64
}

// //

// PublicMirrorVersionObj is one version advertised by a yggvault public API fallback.
type PublicMirrorVersionObj struct {
	Version      string
	ReleaseNotes string
	TreeHash     core.HashObj
	ArchiveURL   string
	Format       string
}

// //

// HelloResultObj is normalized Brother.Hello data. Only Protocol is consumed; the brother's node-wide
// checksums are content-derived sync fingerprints (change on every ingest), so they are not carried here.
type HelloResultObj struct {
	Protocol              string
	MaxFetchResponseBytes uint64
	MaxFetchBatchCount    uint
	IndexKeyset           bool
}

// BrotherVersionObj carries the canonical version tree bytes; their hash24 is verified against the
// declared hash inside Version before return, so the hash is not re-exposed here.
type BrotherVersionObj struct {
	TreeBytes []byte
}

// BrotherFetchResultObj contains staged blobs written to the spool and verified by hash24.
type BrotherFetchResultObj struct {
	Blobs []core.StagedBlobObj
}

// BrotherIndexEntryObj is one brother index entry with release notes and origin hashes.
// TreeHash enables index-driven skips without fetching the tree; SourceHash carries archive provenance.
// A zero SourceHash means the brother is old or did not report it.
type BrotherIndexEntryObj struct {
	Version      string
	ReleaseNotes string
	TreeHash     core.HashObj
	SourceHash   core.HashObj
	UpstreamSeq  int64 // brother source position; 0 means legacy node and local assignment
}

// BrotherSourceInfoObj is normalized source info announced by a brother for a key.
// It is only a hint: rescan still classifies and verifies the first source itself, so only the URL is kept.
type BrotherSourceInfoObj struct {
	SourceURL string
}

// BlobReqObj carries a hash and size so batch size can be checked before sending.
type BlobReqObj struct {
	Hash      core.HashObj
	SizeBytes uint64
}

// // // // // // // // // //

var (
	loaderClientMu   sync.Mutex
	loaderClientLive bool
)

// // // // // // // // // //

// Option configures Obj construction.
type Option func(*Obj)

// WithAllowLoopback permits clearnet loopback dials for tests using httptest on 127/8.
// It is enforced as test-only, so accidental production use does not weaken SSRF protection.
func WithAllowLoopback() Option {
	return func(obj *Obj) {
		if !testing.Testing() {
			return
		}
		obj.allowLoopback = true
	}
}

// // // // // // // // // //

// New builds clients and limits from config.
// meshNode may be nil, in which case Yggdrasil hosts are rejected.
// It also installs the routed transport into lightweigit's package-global client for metadata calls.
func New(configObj *stconf.ConfigObj, meshNode MeshInterface, optArr ...Option) (*Obj, error) {
	downloadParallel := int(configObj.Source.DownloadMaxParallel)
	if downloadParallel < 1 {
		downloadParallel = 1
	}

	obj := &Obj{
		mesh:                meshNode,
		downloadSem:         make(chan struct{}, downloadParallel),
		maxArchiveSize:      effectiveArchiveCap(uint64(configObj.Storage.ArchiveLimits.Size.Compressed)),
		maxBlobBytes:        effectiveBlobCap(uint64(configObj.Storage.ArchiveLimits.Size.PerFile)),
		maxFetchBytes:       effectiveFetchBytes(uint64(configObj.Brother.Rpc.MaxFetchResponseBytes)),
		maxFetchCount:       effectiveFetchCount(configObj.Brother.Rpc.MaxFetchBatchCount),
		requestTimeout:      floorRequestTimeout(configObj.Source.RequestTimeout),
		maxDownloadDuration: configObj.Source.DownloadMaxDuration,
		routingPrefix:       configObj.Web.Routing.Prefix,
		retry: retryObj{
			maxAttempts:    cMaxAttempts,
			backoffInitial: configObj.Source.Retry.BackoffInitial,
			backoffMax:     configObj.Source.Retry.BackoffMax,
			jitterPercent:  configObj.Source.Retry.JitterPercent,
		},
		rpcLimiter:      buildRPCLimiter(configObj.Brother.Rpc.RatePerSec),
		upstreamLimiter: newHostLimiterObj(configObj.Source.RateLimit),
	}

	for _, optFn := range optArr {
		optFn(obj)
	}

	cred, err := buildCredentialMatcher(configObj.Source.Credentials)
	if err != nil {
		return nil, err
	}
	obj.cred = cred

	obj.buildClients()

	loaderClientMu.Lock()
	if loaderClientLive {
		loaderClientMu.Unlock()
		return nil, errors.New("source.New: only one live source instance per process is supported (lightweigit uses a package-global http client); close the previous instance first")
	}
	lightweigit.HttpClient = obj.metaClient
	loaderClientLive = true
	obj.installedLoaderClient = true
	loaderClientMu.Unlock()
	return obj, nil
}

// Close releases idle transport connections and the package-global loader client slot.
func (obj *Obj) Close(_ context.Context) error {
	if obj.metaClient != nil {
		obj.metaClient.CloseIdleConnections()
	}
	if obj.refsClient != nil {
		obj.refsClient.CloseIdleConnections()
	}
	if obj.downloadClient != nil {
		obj.downloadClient.CloseIdleConnections()
	}
	loaderClientMu.Lock()
	if obj.installedLoaderClient {
		obj.installedLoaderClient = false
		loaderClientLive = false
	}
	loaderClientMu.Unlock()
	return nil
}

// // // // // // // // // //

func effectiveBlobCap(configured uint64) uint64 {
	if configured == 0 || configured > cAbsBlobCap {
		return cAbsBlobCap
	}
	return configured
}

func effectiveArchiveCap(configured uint64) uint64 {
	if configured == 0 || configured > cAbsArchiveCap {
		return cAbsArchiveCap
	}
	return configured
}

func effectiveFetchBytes(configured uint64) uint64 {
	if configured == 0 {
		return brotherwire.DefaultMaxFetchResponseBytes
	}
	return configured
}

func effectiveFetchCount(configured uint) uint {
	if configured == 0 {
		return brotherwire.DefaultMaxFetchBatchCount
	}
	return configured
}

func floorRequestTimeout(configured time.Duration) time.Duration {
	if configured <= 0 {
		return cDefaultRequestTimeout
	}
	if configured < cMinRequestTimeout {
		return cMinRequestTimeout
	}
	return configured
}

func buildRPCLimiter(ratePerSec uint) *rate.Limiter {
	if ratePerSec == 0 {
		return nil
	}
	burst := int(ratePerSec)
	if burst < 1 {
		burst = 1
	}
	return rate.NewLimiter(rate.Limit(ratePerSec), burst)
}
