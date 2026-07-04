package mesh

import (
	"bytes"
	"crypto/ed25519"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/voluminor/ratatoskr/mod/resolver"
	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"
)

// // // // // // // // // //

func TestHostFromKey(t *testing.T) {
	cfg := yggconfig.GenerateConfig()
	pemBytes, err := cfg.MarshalPEMPrivateKey()
	if err != nil {
		t.Fatalf("marshal pem: %v", err)
	}
	pemPath := filepath.Join(t.TempDir(), "key.pem")
	if err = os.WriteFile(pemPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}

	host, err := HostFromKey(pemPath)
	if err != nil {
		t.Fatalf("HostFromKey: %v", err)
	}

	pubKey := ed25519.PrivateKey(cfg.PrivateKey).Public().(ed25519.PublicKey)
	wantHost := hex.EncodeToString(pubKey) + resolver.NameMappingSuffix
	if host != wantHost {
		t.Fatalf("host = %q, want %q", host, wantHost)
	}
	if !strings.HasSuffix(host, resolver.NameMappingSuffix) {
		t.Fatalf("host %q missing %q suffix", host, resolver.NameMappingSuffix)
	}
}

func TestHostFromKeyMissing(t *testing.T) {
	if _, err := HostFromKey(filepath.Join(t.TempDir(), "absent.pem")); err == nil {
		t.Fatal("expected error for missing pem key")
	}
}

func TestLoadNodeConfigCertMatchesKey(t *testing.T) {
	cfg := yggconfig.GenerateConfig()
	pemBytes, err := cfg.MarshalPEMPrivateKey()
	if err != nil {
		t.Fatalf("marshal pem: %v", err)
	}
	pemPath := filepath.Join(t.TempDir(), "key.pem")
	if err = os.WriteFile(pemPath, pemBytes, 0o600); err != nil {
		t.Fatalf("write pem: %v", err)
	}

	loadedCfg, err := loadNodeConfig(pemPath)
	if err != nil {
		t.Fatalf("loadNodeConfig: %v", err)
	}
	certKey, ok := loadedCfg.Certificate.PrivateKey.(ed25519.PrivateKey)
	if !ok {
		t.Fatal("certificate private key is not ed25519")
	}
	if !bytes.Equal(certKey, []byte(loadedCfg.PrivateKey)) {
		t.Fatal("certificate key != PEM key: node would boot with a random identity (pem_key inert)")
	}

	host, err := HostFromKey(pemPath)
	if err != nil {
		t.Fatalf("HostFromKey: %v", err)
	}
	certPub := certKey.Public().(ed25519.PublicKey)
	wantHost := hex.EncodeToString(certPub) + resolver.NameMappingSuffix
	if host != wantHost {
		t.Fatalf("HostFromKey %q != certificate-derived host %q", host, wantHost)
	}
}
