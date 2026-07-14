package deviceidentity

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

var goldenCreatedAt = time.Date(2026, 7, 15, 8, 0, 0, 123_000_000, time.UTC)

func TestDeviceDescriptorMatchesCrossLanguageGoldenVectors(t *testing.T) {
	signed := goldenSignedDeviceDescriptor(t)
	if err := VerifySignedDescriptor(signed); err != nil {
		t.Fatalf("verify golden signed descriptor: %v", err)
	}

	assertGoldenMessage(t, "device_descriptor.bin", signed.DeviceDescriptor, new(vibebridgev1.DeviceDescriptor))
	assertGoldenMessage(t, "signed_device_descriptor.bin", signed, new(vibebridgev1.SignedDeviceDescriptor))
}

func goldenSignedDeviceDescriptor(t *testing.T) *vibebridgev1.SignedDeviceDescriptor {
	t.Helper()
	seed := make([]byte, ed25519.SeedSize)
	for index := range seed {
		seed[index] = byte(index)
	}
	signingKey := ed25519.NewKeyFromSeed(seed)
	agreementPrivateBytes := make([]byte, KeyAgreementBytes)
	for index := range agreementPrivateBytes {
		agreementPrivateBytes[index] = byte(0x40 + index)
	}
	agreementKey, err := ecdh.X25519().NewPrivateKey(agreementPrivateBytes)
	if err != nil {
		t.Fatalf("create golden key-agreement key: %v", err)
	}
	signed, err := newSignedDescriptor(DescriptorOptions{
		DeviceID: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		DisplayName:           "Home PC",
		Platform:              "windows",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT,
		SigningPublicKey:      signingKey.Public().(ed25519.PublicKey),
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             goldenCreatedAt,
		KeyVersion:            1,
		ProtocolMajor:         1,
		ProtocolMinor:         0,
	}, signingKey)
	if err != nil {
		t.Fatalf("create golden signed descriptor: %v", err)
	}
	return signed
}

func assertGoldenMessage(t *testing.T, name string, want proto.Message, decoded proto.Message) {
	t.Helper()
	path := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", name)
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode %s: %v", name, err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("create golden vector directory: %v", err)
		}
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update %s: %v", name, err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded message does not match %s", path)
	}
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded %s does not match expected message", name)
	}
}
