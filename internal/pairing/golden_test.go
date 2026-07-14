package pairing

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

type goldenDescriptorSource struct {
	descriptor *vibebridgev1.SignedDeviceDescriptor
}

func (source goldenDescriptorSource) Descriptor() (*vibebridgev1.SignedDeviceDescriptor, error) {
	return proto.Clone(source.descriptor).(*vibebridgev1.SignedDeviceDescriptor), nil
}

func TestPairingInvitationMatchesCrossLanguageGoldenVector(t *testing.T) {
	descriptorPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "signed_device_descriptor.bin")
	descriptorBytes, err := os.ReadFile(descriptorPath)
	if err != nil {
		t.Fatalf("read signed descriptor golden vector: %v", err)
	}
	descriptor := new(vibebridgev1.SignedDeviceDescriptor)
	if err := proto.Unmarshal(descriptorBytes, descriptor); err != nil {
		t.Fatalf("decode signed descriptor golden vector: %v", err)
	}

	randomBytes := make([]byte, InvitationIDBytes+BootstrapSecretBytes)
	for index := 0; index < InvitationIDBytes; index++ {
		randomBytes[index] = byte(0xa0 + index)
	}
	for index := 0; index < BootstrapSecretBytes; index++ {
		randomBytes[InvitationIDBytes+index] = byte(0xc0 + index)
	}
	createdAt := time.Date(2026, 7, 15, 8, 5, 0, 456_000_000, time.UTC)
	manager, err := New(Config{
		Agent:  goldenDescriptorSource{descriptor: descriptor},
		TTL:    DefaultTTL,
		Now:    func() time.Time { return createdAt },
		Random: bytes.NewReader(randomBytes),
	})
	if err != nil {
		t.Fatalf("create golden pairing manager: %v", err)
	}
	defer manager.Close()

	invitation, err := manager.Create([]string{
		"http://192.168.20.5:8787/pairing/v1",
		"wss://relay.example.test/pair/v1",
	})
	if err != nil {
		t.Fatalf("create golden pairing invitation: %v", err)
	}
	if err := ValidateInvitation(invitation, createdAt); err != nil {
		t.Fatalf("validate golden pairing invitation: %v", err)
	}

	path := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "pairing_invitation.bin")
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(invitation)
	if err != nil {
		t.Fatalf("encode golden pairing invitation: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update pairing invitation golden vector: %v", err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pairing invitation golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded pairing invitation does not match %s", path)
	}
	decoded := new(vibebridgev1.PairingInvitation)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode pairing invitation golden vector: %v", err)
	}
	if !proto.Equal(decoded, invitation) {
		t.Fatal("decoded pairing invitation does not match expected message")
	}
}
