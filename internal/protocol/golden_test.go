package protocol_test

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestHelloEnvelopeMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "hello_envelope.bin")
	want := goldenHelloEnvelope()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode hello envelope: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("create golden vector directory: %v", err)
		}
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("update hello envelope golden vector: %v", err)
		}
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read hello envelope golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded hello envelope does not match %s", goldenPath)
	}

	decoded := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode hello envelope golden vector: %v", err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded hello envelope = %v, want %v", decoded, want)
	}
}

func goldenHelloEnvelope() *vibebridgev1.Envelope {
	version := func() *vibebridgev1.ProtocolVersion {
		return &vibebridgev1.ProtocolVersion{Major: 1, Minor: 0}
	}
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		Sequence: 1,
		SentAt: &timestamppb.Timestamp{
			Seconds: 1783843200,
			Nanos:   123000000,
		},
		Payload: &vibebridgev1.Envelope_Hello{Hello: &vibebridgev1.Hello{
			PeerRole: vibebridgev1.PeerRole_PEER_ROLE_CLIENT,
			SupportedVersions: &vibebridgev1.ProtocolVersionRange{
				Minimum: version(),
				Maximum: version(),
			},
			Capabilities: []string{
				"session.resume_v1",
				"terminal.binary_output",
			},
			MaxEnvelopeBytes: 65536,
		}},
	}
}

func TestTerminalOutputEnvelopeMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "terminal_output_envelope.bin")
	want := goldenTerminalOutputEnvelope()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode terminal output envelope: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("update terminal output envelope golden vector: %v", err)
		}
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read terminal output envelope golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded terminal output envelope does not match %s", goldenPath)
	}

	decoded := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode terminal output envelope golden vector: %v", err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded terminal output envelope = %v, want %v", decoded, want)
	}
}

func goldenTerminalOutputEnvelope() *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		Sequence:    42,
		Acknowledge: 17,
		SentAt: &timestamppb.Timestamp{
			Seconds: 1783843201,
			Nanos:   456000000,
		},
		Payload: &vibebridgev1.Envelope_TerminalOutput{TerminalOutput: &vibebridgev1.TerminalOutput{
			Data: []byte("\x1b[32mready\x1b[0m\r\n"),
		}},
	}
}
