package mesh

import (
	"crypto/ed25519"
	"encoding/hex"
	"errors"

	"github.com/voluminor/ratatoskr/mod/resolver"
)

// // // // // // // // // //

func hostFromPublicKey(pubKey ed25519.PublicKey) string {
	return hex.EncodeToString(pubKey) + resolver.NameMappingSuffix
}

// // // // // // // // // //

// HostFromKey derives the ygg host directly from a PEM key without starting a node. Rebuild-cache/self-test uses it
// to rebuild host-sensitive artifacts under the same entry host as serving, without network boot.
func HostFromKey(pemPath string) (string, error) {
	cfg, err := loadNodeConfig(pemPath)
	if err != nil {
		return "", err
	}
	if len(cfg.PrivateKey) != ed25519.PrivateKeySize {
		return "", errors.New("yggdrasil pem key has invalid private key length")
	}
	pubKey, ok := ed25519.PrivateKey(cfg.PrivateKey).Public().(ed25519.PublicKey)
	if !ok {
		return "", errors.New("yggdrasil pem key did not yield an ed25519 public key")
	}
	return hostFromPublicKey(pubKey), nil
}
