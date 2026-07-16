package mesh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
	"github.com/voluminor/ratatoskr"
	"github.com/voluminor/ratatoskr/mod/peermgr"
	"github.com/voluminor/ratatoskr/mod/resolver"
	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

const (
	cYggPort = "80"
)

// //

var (
	// ErrDisabled means a ygg operation was requested with empty yggdrasil.pem_key.
	ErrDisabled = errors.New("yggdrasil mesh is disabled (yggdrasil.pem_key is empty)")
)

// //

// TransportType identifies which listener mod/server requests.
type TransportType uint8

const (
	// TransportYgg is the ygg-entry listener, the only mesh transport.
	TransportYgg TransportType = iota + 1
)

// //

// NodeInterface is the ygg node transport boundary and the only contract for source/server.
type NodeInterface interface {
	DialContext(ctx context.Context, network, address string) (net.Conn, error)
	ListenerFor(transport TransportType) (net.Listener, error)
	Host() string
	Address() net.IP
	OwnsHost(host string) bool
	Enabled() bool
	PeerList() ([]PeerSnapshotObj, bool)
	Close(ctx context.Context) error
}

// //

// Obj owns the node. In disabled mode node==nil and enabled==false.
type Obj struct {
	node     *ratatoskr.Obj
	resolver *resolver.Obj
	host     string
	addr     net.IP
	enabled  bool
	// noPeersStop ends the isolation watcher; nil when the peer manager or logger is absent.
	noPeersStop chan struct{}
	// noPeersEvents counts peer-manager isolation notifications for the metrics snapshot.
	noPeersEvents atomic.Uint64
	// closeOnce guards the single teardown; closeDone publishes closeErr to every Close caller.
	closeOnce sync.Once
	closeDone chan struct{}
	closeErr  error
	// closing plus snapMu fence metrics reads off node teardown: the library does not guard node
	// state reads on a closed core, and telemetry keeps collecting until it closes after mesh.
	// Readers take snapMu.RLock and check closing; Close sets closing, then acquires the write
	// lock once, so no reader can overlap or follow node.Close.
	closing atomic.Bool
	snapMu  sync.RWMutex
	// peerListTTL/peerListMu/peerListCache bound live node reads for the detailed peer list.
	peerListTTL   time.Duration
	peerListMu    sync.Mutex
	peerListCache *peerListCacheObj
}

var _ NodeInterface = (*Obj)(nil)

// // // // // // // // // //

// buildPeerManager maps ygg.peers config to the manager contract. The config treats zero
// health_interval and reprobe_interval as disabled, while the library reserves zero for its
// defaults and negative values for disabled, so zero is translated to -1 here. A non-zero
// refresh_interval below the anti-storm floor is clamped, not rejected: the field predates the
// floor and older configs must keep starting. Unbounded uints are clamped on conversion.
func buildPeerManager(yg stconf.YggObj) *peermgr.ConfigObj {
	if len(yg.Peers.Initial) == 0 {
		return nil
	}
	healthInterval := yg.Peers.HealthInterval
	if healthInterval == 0 {
		healthInterval = -1
	}
	reprobeInterval := yg.Peers.ReprobeInterval
	if reprobeInterval == 0 {
		reprobeInterval = -1
	}
	refreshInterval := yg.Peers.RefreshInterval
	if refreshInterval > 0 && refreshInterval < cMinRefreshInterval {
		refreshInterval = cMinRefreshInterval
	}
	return &peermgr.ConfigObj{
		Peers:                 yg.Peers.Initial,
		ProbeTimeout:          yg.Peers.ProbeTimeout,
		RefreshInterval:       refreshInterval,
		MaxPerProto:           clampToInt(yg.Peers.MaxPerProto),
		BatchSize:             clampToInt(yg.Peers.BatchSize),
		Passive:               yg.Peers.Passive,
		MinPeers:              clampToInt(yg.Peers.MinPeers),
		MinPeersConfirmations: clampToInt(yg.Peers.MinPeersConfirmations),
		HealthInterval:        healthInterval,
		ReprobeInterval:       reprobeInterval,
	}
}

// logInertPeerFields warns about configured ygg.peers values the runtime adjusts or ignores,
// mirroring the peermgr downgrade rules in config terms: passive mode drops selection knobs,
// disabled health recovery drops min_peers, and a sub-floor refresh interval is clamped.
func logInertPeerFields(logObj zerolog.Logger, yg stconf.YggObj, managerConfigObj *peermgr.ConfigObj) {
	if refreshInterval := yg.Peers.RefreshInterval; refreshInterval > 0 && refreshInterval < cMinRefreshInterval {
		logObj.Warn().Str("component", "mesh").
			Msgf("ygg.peers.refresh_interval %s is below the %s anti-storm floor and was raised to it", refreshInterval, cMinRefreshInterval)
	}
	if managerConfigObj.Passive {
		if managerConfigObj.MaxPerProto > 1 {
			logObj.Warn().Str("component", "mesh").
				Msg("ygg.peers.max_per_proto is ignored because ygg.peers.passive keeps every configured peer")
		}
		if managerConfigObj.MinPeers > 0 {
			logObj.Warn().Str("component", "mesh").
				Msg("ygg.peers.min_peers is ignored because ygg.peers.passive keeps every configured peer")
		}
		return
	}
	if managerConfigObj.HealthInterval < 0 && managerConfigObj.MinPeers > 0 {
		logObj.Warn().Str("component", "mesh").
			Msg("ygg.peers.min_peers is ignored because health recovery is disabled (ygg.peers.health_interval=0)")
	}
}

func loadNodeConfig(pemPath string) (*yggconfig.NodeConfig, error) {
	pemBytes, err := os.ReadFile(pemPath)
	if err != nil {
		return nil, fmt.Errorf("read yggdrasil pem key: %w", err)
	}

	cfg := yggconfig.GenerateConfig()
	cfg.AdminListen = "none"
	cfg.Peers = nil
	if err := cfg.UnmarshalPEMPrivateKey(pemBytes); err != nil {
		return nil, fmt.Errorf("parse yggdrasil pem key: %w", err)
	}
	if err := cfg.GenerateSelfSignedCertificate(); err != nil {
		return nil, fmt.Errorf("generate yggdrasil certificate: %w", err)
	}
	return cfg, nil
}

// // // // // // // // // //

// New starts a node when pem_key is set and returns a disabled Obj when it is empty.
// The node lifetime is owned by Close, never by a caller context: tying it to the signal context
// would begin mesh self-shutdown at SIGTERM, before the HTTP listeners drain ygg requests.
// With a logger, ratatoskr events go through the shared pipeline; otherwise library noise is discarded.
func New(configObj *stconf.ConfigObj, logArr ...zerolog.Logger) (*Obj, error) {
	yg := configObj.Ygg
	if yg.PemKey == "" {
		return &Obj{enabled: false}, nil
	}

	cfg, err := loadNodeConfig(yg.PemKey)
	if err != nil {
		return nil, err
	}

	sigilArr, err := buildSigils(configObj, hostFromNodeKey(cfg))
	if err != nil {
		return nil, fmt.Errorf("build node sigils: %w", err)
	}

	peersConfigObj := buildPeerManager(yg)
	if peersConfigObj != nil && len(logArr) > 0 {
		logInertPeerFields(logArr[0], yg, peersConfigObj)
	}

	var noPeersChan chan struct{}
	var noPeersStop chan struct{}
	if peersConfigObj != nil && len(logArr) > 0 {
		noPeersChan = make(chan struct{}, 1)
		noPeersStop = make(chan struct{})
		peersConfigObj.NoReachablePeers = noPeersChan
	}

	nodeConfigObj := ratatoskr.ConfigObj{
		Config:       cfg,
		CloseTimeout: configObj.ShutdownTimeout,
		Peers:        peersConfigObj,
		Sigils:       sigilArr,
	}
	if len(logArr) > 0 {
		nodeConfigObj.Logger = newRatatoskrLogger(logArr[0])
	}

	node, err := ratatoskr.New(nodeConfigObj)
	if err != nil {
		return nil, fmt.Errorf("start yggdrasil node: %w", err)
	}

	resolverObj, err := resolver.New(resolver.ConfigObj{Dialer: node})
	if err != nil {
		_ = node.Close()
		return nil, fmt.Errorf("start ygg resolver: %w", err)
	}

	peerListTTL := configObj.Metrics.SnapshotInterval
	if peerListTTL < time.Second {
		peerListTTL = time.Second
	}
	obj := &Obj{
		node:        node,
		resolver:    resolverObj,
		host:        hostFromPublicKey(node.PublicKey()),
		addr:        node.Address(),
		enabled:     true,
		noPeersStop: noPeersStop,
		closeDone:   make(chan struct{}),
		peerListTTL: peerListTTL,
	}
	if noPeersStop != nil {
		go obj.watchNoReachablePeers(logArr[0], noPeersChan)
	}
	return obj, nil
}

// watchNoReachablePeers counts and logs manager isolation events until noPeersStop closes.
func (obj *Obj) watchNoReachablePeers(logObj zerolog.Logger, eventChan <-chan struct{}) {
	for {
		select {
		case <-eventChan:
			obj.noPeersEvents.Add(1)
			logObj.Warn().Str("component", "mesh").Msg("yggdrasil mesh has no reachable peers")
		case <-obj.noPeersStop:
			return
		}
	}
}

// // // // // // // // // //

// Enabled reports whether the node is running.
func (obj *Obj) Enabled() bool { return obj.enabled }

// Host returns the node ygg host (<hex>.pk.ygg), empty on disabled Obj.
func (obj *Obj) Host() string { return obj.host }

// Address returns the node ygg IPv6 address, nil on disabled Obj.
func (obj *Obj) Address() net.IP { return obj.addr }

// OwnsHost reports whether host belongs to the ygg naming scheme. This is a static naming fact and remains true for
// disabled nodes; reachability is gated separately by Enabled.
func (obj *Obj) OwnsHost(host string) bool {
	hostText := strings.ToLower(strings.TrimSpace(host))
	if strings.HasSuffix(hostText, cHostSuffix) {
		return true
	}
	if ipObj := net.ParseIP(hostText); ipObj != nil {
		if ip16 := ipObj.To16(); ip16 != nil && ipObj.To4() == nil {
			return ip16[0]&0xfe == 0x02
		}
	}
	return false
}

// Close stops the resolver, then the node, within ctx budget; it is idempotent and concurrent-safe,
// and disabled mode is a no-op. Teardown runs once; every caller observes the same result through
// closeDone. The node bounds its own teardown by the configured CloseTimeout and finishes it in the
// background when that budget expires, so an early ctx exit never strands shutdown.
func (obj *Obj) Close(ctx context.Context) error {
	if !obj.enabled || obj.node == nil {
		return nil
	}

	obj.closeOnce.Do(func() {
		obj.closing.Store(true)
		go func() {
			// The write lock first drains in-flight metrics readers, then covers teardown itself:
			// with closing already set, a reader that acquires the lock later bails before touching
			// the node, so no snapshot can overlap or follow node.Close.
			obj.snapMu.Lock()
			obj.closeErr = errors.Join(obj.resolver.Close(), obj.node.Close())
			obj.snapMu.Unlock()
			if obj.noPeersStop != nil {
				close(obj.noPeersStop)
			}
			close(obj.closeDone)
		}()
	})

	select {
	case <-obj.closeDone:
		return obj.closeErr
	case <-ctx.Done():
		return ctx.Err()
	}
}
