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

func TestAttachmentBeginEnvelopeMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "attachment_begin_envelope.bin")
	want := goldenAttachmentBeginEnvelope()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode attachment begin envelope: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("update attachment begin envelope golden vector: %v", err)
		}
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read attachment begin envelope golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded attachment begin envelope does not match %s", goldenPath)
	}

	decoded := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode attachment begin envelope golden vector: %v", err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded attachment begin envelope = %v, want %v", decoded, want)
	}
}

func goldenAttachmentBeginEnvelope() *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		SessionId: []byte{
			0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
			0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		},
		SessionGeneration: 7,
		Sequence:          9,
		Acknowledge:       4,
		SentAt: &timestamppb.Timestamp{
			Seconds: 1783843202,
			Nanos:   789000000,
		},
		Payload: &vibebridgev1.Envelope_AttachmentBegin{AttachmentBegin: &vibebridgev1.AttachmentBegin{
			TransferId:          []byte{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf},
			DisplayName:         "diagram.png",
			DeclaredContentType: "image/png",
			DeclaredExtension:   "png",
			TotalSizeBytes:      12_345,
			TotalSha256: []byte{
				0x00, 0x01, 0x02, 0x03, 0x04, 0x05, 0x06, 0x07,
				0x08, 0x09, 0x0a, 0x0b, 0x0c, 0x0d, 0x0e, 0x0f,
				0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
				0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
			},
		}},
	}
}

func TestAttachmentPromptPreviewEnvelopeMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "attachment_prompt_preview_envelope.bin")
	want := goldenAttachmentPromptPreviewEnvelope()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode attachment prompt preview envelope: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("update attachment prompt preview golden vector: %v", err)
		}
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read attachment prompt preview golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded attachment prompt preview envelope does not match %s", goldenPath)
	}

	decoded := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode attachment prompt preview golden vector: %v", err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded attachment prompt preview envelope = %v, want %v", decoded, want)
	}
}

func goldenAttachmentPromptPreviewEnvelope() *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		SessionId: []byte{
			0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
			0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		},
		SessionGeneration: 7,
		Sequence:          10,
		Acknowledge:       9,
		SentAt: &timestamppb.Timestamp{
			Seconds: 1783843203,
			Nanos:   12_000_000,
		},
		Payload: &vibebridgev1.Envelope_AttachmentPromptPreview{AttachmentPromptPreview: &vibebridgev1.AttachmentPromptPreview{
			ActionId:    []byte{0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7},
			Disposition: vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_PREPARED,
			Preview:     "Inspect evidence\n\nUse the following local files:\n- `../.vibebridge/uploads/session/file.txt`",
			AppendEnter: true,
		}},
	}
}

func TestAttachmentTransferStatusEnvelopeMatchesCrossLanguageGoldenVector(t *testing.T) {
	goldenPath := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "attachment_transfer_status_envelope.bin")
	want := goldenAttachmentTransferStatusEnvelope()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(want)
	if err != nil {
		t.Fatalf("encode attachment transfer status envelope: %v", err)
	}
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(goldenPath, encoded, 0o644); err != nil {
			t.Fatalf("update attachment transfer status envelope golden vector: %v", err)
		}
	}

	golden, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read attachment transfer status envelope golden vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("encoded attachment transfer status envelope does not match %s", goldenPath)
	}

	decoded := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(golden, decoded); err != nil {
		t.Fatalf("decode attachment transfer status envelope golden vector: %v", err)
	}
	if !proto.Equal(decoded, want) {
		t.Fatalf("decoded attachment transfer status envelope = %v, want %v", decoded, want)
	}
}

func goldenAttachmentTransferStatusEnvelope() *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId: []byte{
			0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
			0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
		},
		SessionId: []byte{
			0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
			0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
		},
		SessionGeneration: 7,
		Sequence:          11,
		Acknowledge:       10,
		SentAt: &timestamppb.Timestamp{
			Seconds: 1783843203,
			Nanos:   345000000,
		},
		Payload: &vibebridgev1.Envelope_AttachmentTransferStatus{AttachmentTransferStatus: &vibebridgev1.AttachmentTransferStatus{
			TransferId:      []byte{0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7},
			Disposition:     vibebridgev1.AttachmentTransferDisposition_ATTACHMENT_TRANSFER_DISPOSITION_ACTIVE,
			NextOffsetBytes: 49152,
		}},
	}
}
