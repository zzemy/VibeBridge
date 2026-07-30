package relayclient

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/zzemy/VibeBridge/internal/relay"
)

// LoadOrCreateIssuer opens an existing relay ticket issuer key file or
// atomically creates a fresh one. The key is a raw 64-byte Ed25519
// private key stored with 0600 permissions. The public half is
// printed via IssuerPublicKeyHex so the relay operator can configure
// the Verifier with the matching public key.
func LoadOrCreateIssuer(path string) (*relay.Issuer, error) {
	if path == "" {
		return nil, errors.New("relay issuer key path must not be empty")
	}
	if !filepath.IsAbs(path) {
		abs, err := filepath.Abs(path)
		if err != nil {
			return nil, fmt.Errorf("resolve issuer key path: %w", err)
		}
		path = abs
	}
	raw, err := os.ReadFile(path)
	if err == nil {
		if len(raw) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("relay issuer key file %s is %d bytes, expected %d", path, len(raw), ed25519.PrivateKeySize)
		}
		return relay.NewIssuer(ed25519.PrivateKey(raw))
	}
	if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read issuer key file %s: %w", path, err)
	}
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate relay issuer key: %w", err)
	}
	if err := os.WriteFile(path, priv, 0o600); err != nil {
		return nil, fmt.Errorf("write issuer key file %s: %w", path, err)
	}
	return relay.NewIssuer(priv)
}

// IssuerPublicKeyHex returns the hex-encoded public key of the issuer,
// suitable for configuring on the relay server side.
func IssuerPublicKeyHex(issuer *relay.Issuer) string {
	if issuer == nil {
		return ""
	}
	return hex.EncodeToString(issuer.PublicKey())
}
