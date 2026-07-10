package mesh

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/voluminor/ratatoskr/mod/resolver"
)

// // // // // // // // // //

func writeKey(t *testing.T, pemBytes []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "node.pem")
	if err := WriteKeyFile(path, pemBytes); err != nil {
		t.Fatalf("WriteKeyFile returned error: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat returned error: %v", err)
	}
	if runtime.GOOS != "windows" {
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Fatalf("key file perm = %o, want 600", perm)
		}
	}
	return path
}

// // // // // // // // // //

func TestGenerateKeyRoundTrip(t *testing.T) {
	pemBytes, host, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey returned error: %v", err)
	}
	if len(pemBytes) == 0 {
		t.Fatal("GenerateKey returned empty pem")
	}
	if !strings.HasSuffix(host, resolver.NameMappingSuffix) {
		t.Fatalf("host %q lacks naming suffix %q", host, resolver.NameMappingSuffix)
	}

	derived, err := HostFromKey(writeKey(t, pemBytes))
	if err != nil {
		t.Fatalf("HostFromKey returned error: %v", err)
	}
	if derived != host {
		t.Fatalf("host mismatch: GenerateKey=%q HostFromKey=%q", host, derived)
	}
}

func TestGenerateKeyUnique(t *testing.T) {
	_, hostA, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey A returned error: %v", err)
	}
	_, hostB, err := GenerateKey()
	if err != nil {
		t.Fatalf("GenerateKey B returned error: %v", err)
	}
	if hostA == hostB {
		t.Fatal("two GenerateKey calls produced the same host")
	}
}
