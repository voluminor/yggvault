package mesh

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
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
	// cYggPort is the fixed transport port for the ygg entry.
	cYggPort = "80"

	// cCoreStopTimeout is the dedicated upper bound for ratatoskr core shutdown.
	cCoreStopTimeout = 5 * time.Second
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
}

var _ NodeInterface = (*Obj)(nil)

// // // // // // // // // //

func buildPeerManager(yg stconf.YggObj) *peermgr.ConfigObj {
	if len(yg.Peers.Initial) == 0 {
		return nil
	}
	return &peermgr.ConfigObj{
		Peers:           yg.Peers.Initial,
		ProbeTimeout:    yg.Peers.ProbeTimeout,
		RefreshInterval: yg.Peers.RefreshInterval,
		MaxPerProto:     int(yg.Peers.MaxPerProto),
		BatchSize:       int(yg.Peers.BatchSize),
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

	// Ctx stays nil: ratatoskr then requires a manual Close, which runtime shutdown guarantees.
	nodeConfigObj := ratatoskr.ConfigObj{
		Config:          cfg,
		CoreStopTimeout: cCoreStopTimeout,
		Peers:           buildPeerManager(yg),
		Sigils:          sigilArr,
	}
	if len(logArr) > 0 {
		nodeConfigObj.Logger = newRatatoskrLogger(logArr[0])
	}

	node, err := ratatoskr.New(nodeConfigObj)
	if err != nil {
		return nil, fmt.Errorf("start yggdrasil node: %w", err)
	}

	obj := &Obj{
		node:     node,
		resolver: resolver.New(node, ""),
		host:     hostFromPublicKey(node.PublicKey()),
		addr:     node.Address(),
		enabled:  true,
	}
	return obj, nil
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
	if strings.HasSuffix(hostText, resolver.NameMappingSuffix) {
		return true
	}
	if ipObj := net.ParseIP(hostText); ipObj != nil {
		if ip16 := ipObj.To16(); ip16 != nil && ipObj.To4() == nil {
			return ip16[0]&0xfe == 0x02
		}
	}
	return false
}

// Close stops the node within ctx budget; disabled mode is an idempotent no-op.
func (obj *Obj) Close(ctx context.Context) error {
	if !obj.enabled || obj.node == nil {
		return nil
	}

	done := make(chan error, 1)
	go func() { done <- obj.node.Close() }()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}
