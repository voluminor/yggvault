package mesh

import (
	"crypto/ed25519"
	"fmt"

	yggconfig "github.com/yggdrasil-network/yggdrasil-go/src/config"

	"github.com/voluminor/yggvault/mod/internal/osfs"
)

// // // // // // // // // //

// //

func keyToPEM(priv ed25519.PrivateKey) ([]byte, error) {
	cfg := &yggconfig.NodeConfig{PrivateKey: yggconfig.KeyBytes(priv)}
	return cfg.MarshalPEMPrivateKey()
}

// WriteKeyFile writes a private key PEM with secret permissions: 0o600, explicit Chmod, and O_NOFOLLOW. The caller
// decides whether the file may already exist or be overwritten.
func WriteKeyFile(filePath string, pemBytes []byte) error {
	fileObj, err := osfs.OpenNoFollowWrite(filePath, 0o600)
	if err != nil {
		return fmt.Errorf("open key file %s: %w", filePath, err)
	}
	if err = fileObj.Chmod(0o600); err != nil {
		_ = fileObj.Close()
		return fmt.Errorf("chmod key file %s: %w", filePath, err)
	}
	if _, err = fileObj.Write(pemBytes); err != nil {
		_ = fileObj.Close()
		return fmt.Errorf("write key file %s: %w", filePath, err)
	}
	if err = fileObj.Close(); err != nil {
		return fmt.Errorf("close key file %s: %w", filePath, err)
	}
	return nil
}

// // // // // // // // // //

// GenerateKey creates a random node private key and returns its PKCS#8 PEM plus ygg host. One random ed25519 key is
// already unique enough; the full node identity keeps the whole key through host and certificate.
func GenerateKey() ([]byte, string, error) {
	pub, priv, err := ed25519.GenerateKey(nil)
	if err != nil {
		return nil, "", fmt.Errorf("generate ed25519 key: %w", err)
	}
	pemBytes, err := keyToPEM(priv)
	if err != nil {
		return nil, "", fmt.Errorf("marshal yggdrasil pem key: %w", err)
	}
	return pemBytes, hostFromPublicKey(pub), nil
}
