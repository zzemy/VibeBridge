package securestore

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestRoundTripAndAtomicReplacement(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	first := []byte("first private value")
	if err := Write(path, "device-identity-v1", first); err != nil {
		t.Fatalf("write first value: %v", err)
	}
	loaded, err := Read(path, "device-identity-v1")
	if err != nil {
		t.Fatalf("read first value: %v", err)
	}
	if !bytes.Equal(loaded, first) {
		t.Fatalf("loaded first value = %q, want %q", loaded, first)
	}
	zero(loaded)

	second := []byte("replacement private value")
	if err := Write(path, "device-identity-v1", second); err != nil {
		t.Fatalf("replace value: %v", err)
	}
	loaded, err = Read(path, "device-identity-v1")
	if err != nil {
		t.Fatalf("read replacement value: %v", err)
	}
	defer zero(loaded)
	if !bytes.Equal(loaded, second) {
		t.Fatalf("loaded replacement = %q, want %q", loaded, second)
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(path), ".secure-store-*.tmp"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("temporary secure-store files = %v/%v, want none", matches, err)
	}
	if runtime.GOOS != "windows" {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("stat secure-store: %v", err)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("secure-store permissions = %o, want 600", info.Mode().Perm())
		}
	}
}

func TestReadRejectsWrongPurpose(t *testing.T) {
	path := filepath.Join(t.TempDir(), "identity.json")
	if err := Write(path, "device-identity-v1", []byte("private")); err != nil {
		t.Fatalf("write value: %v", err)
	}
	if _, err := Read(path, "relay-ticket-v1"); err == nil || !strings.Contains(err.Error(), "purpose") {
		t.Fatalf("wrong-purpose error = %v, want purpose mismatch", err)
	}
}

func TestReadRejectsMalformedAndUnsupportedEnvelopes(t *testing.T) {
	directory := t.TempDir()
	cases := map[string][]byte{
		"empty":               {},
		"truncated":           []byte(`{"version":1`),
		"unknown field":       []byte(`{"version":1,"protection":"x","purpose":"AA==","ciphertext":"AA==","extra":true}`),
		"multiple values":     []byte(`{} {}`),
		"unsupported version": mustEnvelope(t, envelope{Version: 99, Protection: protectionName(), Purpose: purposeDigest("purpose"), Ciphertext: []byte{1}}),
		"wrong protection":    mustEnvelope(t, envelope{Version: 1, Protection: "unsupported", Purpose: purposeDigest("purpose"), Ciphertext: []byte{1}}),
		"wrong purpose":       mustEnvelope(t, envelope{Version: 1, Protection: protectionName(), Purpose: purposeDigest("other"), Ciphertext: []byte{1}}),
		"empty ciphertext":    mustEnvelope(t, envelope{Version: 1, Protection: protectionName(), Purpose: purposeDigest("purpose")}),
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatalf("write malformed envelope: %v", err)
			}
			if _, err := Read(path, "purpose"); err == nil {
				t.Fatal("malformed secure-store envelope loaded successfully")
			}
		})
	}
}

func TestReadRejectsOversizedEnvelope(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oversized.json")
	if err := os.WriteFile(path, bytes.Repeat([]byte{'x'}, maxEnvelopeBytes+1), 0o600); err != nil {
		t.Fatalf("write oversized envelope: %v", err)
	}
	if _, err := Read(path, "purpose"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("oversized error = %v, want size limit", err)
	}
}

func TestArgumentsAndSizeBoundaries(t *testing.T) {
	if err := Write("relative.json", "purpose", []byte("value")); err == nil {
		t.Fatal("relative secure-store path accepted")
	}
	path := filepath.Join(t.TempDir(), "value.json")
	if err := Write(path, "", []byte("value")); err == nil {
		t.Fatal("empty purpose accepted")
	}
	if err := Write(path, "purpose", nil); err == nil {
		t.Fatal("empty plaintext accepted")
	}
	if err := Write(path, "purpose", make([]byte, maxPlaintextBytes+1)); err == nil {
		t.Fatal("oversized plaintext accepted")
	}
}

func mustEnvelope(t *testing.T, value envelope) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode test envelope: %v", err)
	}
	return encoded
}
