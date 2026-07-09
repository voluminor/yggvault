package brother

import (
	"context"
	"net"
	"net/rpc"
	"sync"
	"time"

	"golang.org/x/time/rate"

	"github.com/voluminor/yggvault/mod/brotherwire"
	"github.com/voluminor/yggvault/mod/core"
	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	// cIndexPageSize is the Brother.Index page size; page+1 detects the next page without full materialization.
	cIndexPageSize = 4096

	// cMaxIndexPage caps Brother.Index page numbers to avoid expensive OFFSET skips and 32-bit overflow.
	cMaxIndexPage = 64

	// cBlobBatchMax is the default hash cap for one Brother.BlobsFetch request.
	cBlobBatchMax = brotherwire.DefaultMaxFetchBatchCount
)

// // // // // // // // // //

// StoreInterface is the narrow storage read surface needed by brother RPCs.
// Artifacts, detection, and keyset APIs are not exposed to peers.
type StoreInterface interface {
	ListVersionsPage(ctx context.Context, key string, includeDeleted bool, limit int, offset int) ([]core.VersionObj, error)
	ListVersionsKeyset(ctx context.Context, key string, includeDeleted bool, afterSeq int64, afterVersion string, limit int) ([]core.VersionObj, error)
	GetVersion(ctx context.Context, key string, version string) (core.VersionObj, bool, error)
	ReadTree(ctx context.Context, treeHashObj core.HashObj) ([]core.TreeEntryObj, error)
	ReadBlob(ctx context.Context, hashObj core.HashObj) ([]byte, error)
}

// // // // // // // // // //

func effectiveFetchResponseBytes(valueObj stconf.SizeObj) uint64 {
	if valueObj == 0 {
		return brotherwire.DefaultMaxFetchResponseBytes
	}
	return uint64(valueObj)
}

func effectiveFetchBatchCount(valueObj uint) int {
	if valueObj == 0 {
		return brotherwire.DefaultMaxFetchBatchCount
	}
	return int(valueObj)
}

// // // // // // // // // //

// ServerObj is the brother RPC server: net/rpc gob over HTTP CONNECT `/rpc` with hijack.
// MaxParallel limits concurrent sessions, RatePerSec limits new sessions, and idle timeout closes quiet peers.
type ServerObj struct {
	rpcServerObj *rpc.Server
	rootCancel   context.CancelFunc
	sessionSem   chan struct{}
	limiterObj   *rate.Limiter
	idleTimeout  time.Duration

	connMu  sync.Mutex
	connSet map[net.Conn]struct{}
	closing bool

	// Per-peer session accounting keeps one neighbor from occupying all sessionSem slots.
	peerMu       sync.Mutex
	peerSessions map[string]int
	peerMax      int
}

// // // // // // // // // //

// New builds the brother server and registers the handler under brotherwire.ServiceName.
// Registration fails when the implementation does not match net/rpc signatures.
func New(cfg *stconf.ConfigObj, store StoreInterface) (*ServerObj, error) {
	rootCtx, rootCancel := context.WithCancel(context.Background())
	handlerObj := &handlerObj{
		ctx:                   rootCtx,
		storeObj:              store,
		releaseMirrors:        cfg.ReleaseMirrors,
		maxFetchResponseBytes: effectiveFetchResponseBytes(cfg.Brother.Rpc.MaxFetchResponseBytes),
		maxFetchBatchCount:    effectiveFetchBatchCount(cfg.Brother.Rpc.MaxFetchBatchCount),
	}
	rpcServerObj := rpc.NewServer()
	if err := rpcServerObj.RegisterName(brotherwire.ServiceName, handlerObj); err != nil {
		rootCancel()
		return nil, err
	}

	var sessionSem chan struct{}
	if maxParallel := cfg.Brother.Rpc.MaxParallel; maxParallel > 0 {
		sessionSem = make(chan struct{}, maxParallel)
	}
	var limiterObj *rate.Limiter
	if ratePerSec := cfg.Brother.Rpc.RatePerSec; ratePerSec > 0 {
		limiterObj = rate.NewLimiter(rate.Limit(ratePerSec), int(ratePerSec))
	}

	// Per-peer cap: explicit config value, or auto as half of MaxParallel. Auto is disabled at MaxParallel<=1.
	peerMax := int(cfg.Brother.Rpc.MaxParallelPerPeer)
	if peerMax <= 0 {
		if maxParallel := int(cfg.Brother.Rpc.MaxParallel); maxParallel > 1 {
			peerMax = (maxParallel + 1) / 2
		}
	}

	return &ServerObj{
		rpcServerObj: rpcServerObj,
		rootCancel:   rootCancel,
		sessionSem:   sessionSem,
		limiterObj:   limiterObj,
		idleTimeout:  cfg.Web.Ingress.IdleTimeout,
		peerMax:      peerMax,
	}, nil
}

// // // // // // // // // //

// peerHostFromAddr extracts the peer Yggdrasil host from RemoteAddr for per-peer accounting.
func peerHostFromAddr(remoteAddr string) string {
	host, _, err := net.SplitHostPort(remoteAddr)
	if err != nil {
		return remoteAddr
	}
	return host
}

// acquirePeerSlot limits simultaneous sessions per peer; peerMax<=0 disables accounting.
func (obj *ServerObj) acquirePeerSlot(peerHost string) bool {
	if obj.peerMax <= 0 {
		return true
	}
	obj.peerMu.Lock()
	defer obj.peerMu.Unlock()
	if obj.peerSessions == nil {
		obj.peerSessions = make(map[string]int)
	}
	if obj.peerSessions[peerHost] >= obj.peerMax {
		return false
	}
	obj.peerSessions[peerHost]++
	return true
}

// releasePeerSlot frees a peer slot and removes zero counters so departed peers do not grow the map.
func (obj *ServerObj) releasePeerSlot(peerHost string) {
	if obj.peerMax <= 0 {
		return
	}
	obj.peerMu.Lock()
	defer obj.peerMu.Unlock()
	if obj.peerSessions[peerHost] <= 1 {
		delete(obj.peerSessions, peerHost)
		return
	}
	obj.peerSessions[peerHost]--
}

// Close cancels the lifetime context and force-closes active hijacked connections.
// Listener shutdown does not close those connections by itself.
func (obj *ServerObj) Close() {
	obj.rootCancel()
	obj.connMu.Lock()
	obj.closing = true
	for conn := range obj.connSet {
		_ = conn.Close()
	}
	obj.connSet = nil
	obj.connMu.Unlock()
}
