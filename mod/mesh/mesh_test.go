package mesh

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/rs/zerolog"
	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"

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
	obj, err := New(context.Background(), meshConfig(t, ""))
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
	_, err := New(context.Background(), meshConfig(t, filepath.Join(t.TempDir(), "absent.pem")))
	if err == nil {
		t.Fatal("expected error for missing pem file")
	}
}

func TestEnabledNodeStartsAndCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	obj, err := New(ctx, meshConfig(t, writeGeneratedPEM(t)))
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

	closeCtx, closeCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer closeCancel()
	done := make(chan struct{})
	go func() { _ = obj.Close(closeCtx); close(done) }()
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("Close did not return within budget")
	}
}
