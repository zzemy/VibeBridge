package relayclient_test

import (
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"

	"github.com/zzemy/VibeBridge/internal/relayclient"
)

func TestLoadOrCreateIssuerCreatesNewKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issuer.key")
	issuer, err := relayclient.LoadOrCreateIssuer(path)
	if err != nil {
		t.Fatalf("load or create: %v", err)
	}
	if issuer == nil {
		t.Fatal("issuer must not be nil")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("expected 0600 perms, got %o", info.Mode().Perm())
	}
	if info.Size() != ed25519.PrivateKeySize {
		t.Fatalf("expected %d bytes, got %d", ed25519.PrivateKeySize, info.Size())
	}
}

func TestLoadOrCreateIssuerLoadsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issuer.key")
	first, err := relayclient.LoadOrCreateIssuer(path)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	firstPub := relayclient.IssuerPublicKeyHex(first)
	if firstPub == "" {
		t.Fatal("public key hex must not be empty")
	}
	second, err := relayclient.LoadOrCreateIssuer(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	secondPub := relayclient.IssuerPublicKeyHex(second)
	if firstPub != secondPub {
		t.Fatalf("public key changed: %s vs %s", firstPub, secondPub)
	}
}

func TestLoadOrCreateIssuerRejectsCorruptFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "issuer.key")
	if err := os.WriteFile(path, []byte("corrupt"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	_, err := relayclient.LoadOrCreateIssuer(path)
	if err == nil {
		t.Fatal("expected error for corrupt key file")
	}
}

func TestLoadOrCreateIssuerRejectsEmptyPath(t *testing.T) {
	_, err := relayclient.LoadOrCreateIssuer("")
	if err == nil {
		t.Fatal("expected error for empty path")
	}
}

func TestIssuerPublicKeyHexNil(t *testing.T) {
	if got := relayclient.IssuerPublicKeyHex(nil); got != "" {
		t.Fatalf("expected empty string for nil issuer, got %q", got)
	}
}
