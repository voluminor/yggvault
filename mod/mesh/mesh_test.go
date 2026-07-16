package mesh

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
	"go.opentelemetry.io/otel/metric/noop"

	"github.com/voluminor/yggvault/target/stconf"
)

// // // // // // // // // //

func meshConfig(t *testing.T, pemPath string) *stconf.ConfigObj {
	t.Helper()
	configObj := stconf.FullConfig()
	configObj.Ygg.PemKey = pemPath
	configObj.Ygg.Peers.Initial = nil
	return configObj
}

func writeGeneratedPEM(t *testing.T) string {
	t.Helper()
	cfg := yggconfig.GenerateConfig()
	pem, err := cfg.MarshalPEMPrivateKey()
	if err != nil {
		t.Fatalf("MarshalPEMPrivateKey returned error: %v", err)
	}
	path := filepath.Join(t.TempDir(), "node.pem")
	if err := os.WriteFile(path, pem, 0o600); err != nil {
		t.Fatalf("WriteFile returned error: %v", err)
	}
	return path
}

// // // // // // // // // //

func TestDisabledNode(t *testing.T) {
	obj, err := New(meshConfig(t, ""))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if obj.Enabled() {
		t.Fatal("node with empty pem_key must be disabled")
	}
	if obj.Host() != "" {
		t.Fatalf("disabled host must be empty, got %q", obj.Host())
	}
	if _, err := obj.DialContext(context.Background(), "tcp", "[200::1]:80"); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled from DialContext, got %v", err)
	}
	if _, err := obj.ListenerFor(TransportYgg); !errors.Is(err, ErrDisabled) {
		t.Fatalf("expected ErrDisabled from ListenerFor, got %v", err)
	}
	if _, ok := obj.Snapshot(); ok {
		t.Fatal("disabled node must not produce a metrics snapshot")
	}
	if _, ok := obj.PeerList(); ok {
		t.Fatal("disabled node must not produce a peer list")
	}
	if err := obj.RegisterMetrics(nil); err != nil {
		t.Fatalf("RegisterMetrics on disabled node must be nil, got %v", err)
	}
	if err := obj.Close(context.Background()); err != nil {
		t.Fatalf("Close on disabled node must be nil, got %v", err)
	}
}

func TestRatatoskrLoggerUsesZerolog(t *testing.T) {
	bufferObj := bytes.NewBuffer(nil)
	logObj := zerolog.New(bufferObj)
	loggerObj := newRatatoskrLogger(logObj)

	loggerObj.Debugf("dial %s", "peer")
	loggerObj.Warnln("peer", "slow")
	loggerObj.Errorf("peer failed: %d", 1)

	text := bufferObj.String()
	for _, wantText := range []string{
		`"component":"ratatoskr"`,
		`"level":"debug"`,
		`"level":"warn"`,
		`"level":"error"`,
		`dial peer`,
	} {
		if !strings.Contains(text, wantText) {
			t.Fatalf("log output missing %q: %s", wantText, text)
		}
	}
}

func TestMissingPemFileErrors(t *testing.T) {
	_, err := New(meshConfig(t, filepath.Join(t.TempDir(), "absent.pem")))
	if err == nil {
		t.Fatal("expected error for missing pem file")
	}
}

func TestEnabledNodeStartsAndCloses(t *testing.T) {
	obj, err := New(meshConfig(t, writeGeneratedPEM(t)))
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}
	if !obj.Enabled() {
		t.Fatal("node with pem_key must be enabled")
	}
	if !strings.HasSuffix(obj.Host(), ".pk.ygg") {
		t.Fatalf("host must end with .pk.ygg, got %q", obj.Host())
	}
	if obj.Address() == nil {
		t.Fatal("enabled node must expose an address")
	}

	snapObj, ok := obj.Snapshot()
	if !ok {
		t.Fatal("enabled node must produce a metrics snapshot")
	}
	if snapObj.PeersKnown != 0 {
		t.Fatalf("peerless node snapshot must be empty, got %+v", snapObj)
	}
	if peerArr, peersOK := obj.PeerList(); !peersOK || len(peerArr) != 0 {
		t.Fatalf("peerless node peer list must be empty, got ok=%v len=%d", peersOK, len(peerArr))
	}
	if err := obj.RegisterMetrics(noop.NewMeterProvider().Meter("test")); err != nil {
		t.Fatalf("RegisterMetrics returned error: %v", err)
	}

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer closeCancel()
	done := make(chan struct{})
	go func() { _ = obj.Close(closeCtx); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return within budget")
	}
	if _, ok := obj.Snapshot(); ok {
		t.Fatal("closing node must not produce a metrics snapshot")
	}
}

func TestRedactPeerURI(t *testing.T) {
	for _, testObj := range []struct {
		in   string
		want string
	}{
		{in: "tls://peer.example:443", want: "tls://peer.example:443"},
		{in: "socks://user:secret@127.0.0.1:1080/target", want: "socks://127.0.0.1:1080/target"},
		{in: "tcp://login@peer.example:80", want: "tcp://peer.example:80"},
		{in: "http://%zz", want: ""},
	} {
		if got := redactPeerURI(testObj.in); got != testObj.want {
			t.Fatalf("redactPeerURI(%q) = %q, want %q", testObj.in, got, testObj.want)
		}
	}
}

func TestCloseRepeatedAndConcurrent(t *testing.T) {
	configObj := meshConfig(t, writeGeneratedPEM(t))
	configObj.Ygg.Peers.Initial = []string{"tls://127.0.0.1:1"}
	obj, err := New(configObj, zerolog.Nop())
	if err != nil {
		t.Fatalf("New returned error: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	stopSnap := make(chan struct{})
	var snapWG sync.WaitGroup
	for range 2 {
		snapWG.Add(1)
		go func() {
			defer snapWG.Done()
			for {
				select {
				case <-stopSnap:
					return
				default:
					_, _ = obj.Snapshot()
					_, _ = obj.PeerList()
				}
			}
		}()
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = obj.Close(ctx)
		}()
	}
	wg.Wait()
	close(stopSnap)
	snapWG.Wait()

	if err := obj.Close(ctx); err != nil {
		t.Fatalf("repeated Close returned error: %v", err)
	}
	if _, ok := obj.Snapshot(); ok {
		t.Fatal("closed node must not produce a metrics snapshot")
	}
}

func TestBuildPeerManagerMapping(t *testing.T) {
	yg := stconf.FullConfig().Ygg
	yg.Peers.Initial = []string{"tls://peer-a.example:443", "tcp://peer-b.example:443"}
	yg.Peers.Passive = true
	yg.Peers.HealthInterval = 0
	yg.Peers.ReprobeInterval = 0

	managerConfigObj := buildPeerManager(yg)
	if managerConfigObj == nil {
		t.Fatal("expected manager config for non-empty initial peers")
	}
	if !managerConfigObj.Passive {
		t.Fatal("passive flag not mapped")
	}
	if managerConfigObj.HealthInterval != -1 || managerConfigObj.ReprobeInterval != -1 {
		t.Fatal("zero config durations must map to -1 (disabled)")
	}

	yg.Peers.Passive = false
	yg.Peers.MinPeers = 1
	yg.Peers.HealthInterval = 15 * time.Second
	yg.Peers.ReprobeInterval = time.Hour
	yg.Peers.RefreshInterval = 10 * time.Second
	managerConfigObj = buildPeerManager(yg)
	if managerConfigObj.MinPeers != 1 {
		t.Fatalf("min_peers not mapped: %+v", managerConfigObj)
	}
	if managerConfigObj.HealthInterval != 15*time.Second || managerConfigObj.ReprobeInterval != time.Hour {
		t.Fatal("non-zero config durations must pass through unchanged")
	}
	if managerConfigObj.RefreshInterval != cMinRefreshInterval {
		t.Fatalf("sub-floor refresh_interval must clamp to %s, got %s", cMinRefreshInterval, managerConfigObj.RefreshInterval)
	}

	yg.Peers.RefreshInterval = 5 * time.Minute
	if buildPeerManager(yg).RefreshInterval != 5*time.Minute {
		t.Fatal("above-floor refresh_interval must pass through unchanged")
	}
	yg.Peers.RefreshInterval = 0
	if buildPeerManager(yg).RefreshInterval != 0 {
		t.Fatal("zero refresh_interval must stay zero (scheduled refresh disabled)")
	}

	yg.Peers.Initial = nil
	if buildPeerManager(yg) != nil {
		t.Fatal("empty initial peers must disable the manager")
	}
}
