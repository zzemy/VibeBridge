package protocol

import (
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

func TestAcceptClientHelloNegotiatesCurrentVersion(t *testing.T) {
	encoded := marshalHello(t, clientHello(
		&vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
		&vibebridgev1.ProtocolVersion{Major: 1, Minor: 2},
	))

	negotiated, err := AcceptClientHello(encoded)
	if err != nil {
		t.Fatalf("accept client Hello: %v", err)
	}
	if negotiated.Major != 1 || negotiated.Minor != 0 {
		t.Fatalf("negotiated version = %d.%d, want 1.0", negotiated.Major, negotiated.Minor)
	}
	if negotiated.PeerMaxEnvelopeBytes != MaxEnvelopeBytes {
		t.Fatalf("peer max envelope bytes = %d, want %d", negotiated.PeerMaxEnvelopeBytes, MaxEnvelopeBytes)
	}
	if !negotiated.HasCapability(CapabilityTerminalBinaryOutput) {
		t.Fatal("terminal binary output capability was not retained")
	}
}

func TestAcceptClientHelloRejectsIncompatibleVersion(t *testing.T) {
	encoded := marshalHello(t, clientHello(
		&vibebridgev1.ProtocolVersion{Major: 2, Minor: 0},
		&vibebridgev1.ProtocolVersion{Major: 2, Minor: 1},
	))

	if _, err := AcceptClientHello(encoded); err == nil {
		t.Fatal("incompatible client Hello was accepted")
	}
}

func TestAcceptClientHelloRejectsWrongRoleAndMalformedRange(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*vibebridgev1.Envelope)
	}{
		{
			name: "agent role",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().PeerRole = vibebridgev1.PeerRole_PEER_ROLE_AGENT
			},
		},
		{
			name: "descending range",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().SupportedVersions.Minimum.Minor = 2
				envelope.GetHello().SupportedVersions.Maximum.Minor = 1
			},
		},
		{
			name: "duplicate capability",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilityTerminalBinaryOutput)
			},
		},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			envelope := clientHello(
				&vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
				&vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
			)
			testCase.mutate(envelope)
			if _, err := AcceptClientHello(marshalHello(t, envelope)); err == nil {
				t.Fatal("invalid client Hello was accepted")
			}
		})
	}
}

func TestNewAgentHelloUsesNegotiatedVersion(t *testing.T) {
	sentAt := time.Date(2026, time.July, 13, 10, 0, 0, 0, time.UTC)
	envelope, err := NewAgentHello([]byte("0123456789abcdef"), 1, 0, sentAt)
	if err != nil {
		t.Fatalf("create Agent Hello: %v", err)
	}
	if envelope.ProtocolMajor != 1 || envelope.ProtocolMinor != 0 {
		t.Fatalf("envelope version = %d.%d, want 1.0", envelope.ProtocolMajor, envelope.ProtocolMinor)
	}
	if envelope.GetHello().PeerRole != vibebridgev1.PeerRole_PEER_ROLE_AGENT {
		t.Fatalf("peer role = %v, want Agent", envelope.GetHello().PeerRole)
	}
	if envelope.Sequence != 1 {
		t.Fatalf("sequence = %d, want 1", envelope.Sequence)
	}
	foundSequencedIO := false
	foundSessionResume := false
	for _, capability := range envelope.GetHello().GetCapabilities() {
		if capability == CapabilityTerminalSequencedIO {
			foundSequencedIO = true
		}
		if capability == CapabilitySessionResume {
			foundSessionResume = true
		}
	}
	if !foundSequencedIO {
		t.Fatalf("Agent Hello capabilities = %v, missing %q", envelope.GetHello().GetCapabilities(), CapabilityTerminalSequencedIO)
	}
	if !foundSessionResume {
		t.Fatalf("Agent Hello capabilities = %v, missing %q", envelope.GetHello().GetCapabilities(), CapabilitySessionResume)
	}
}

func clientHello(minimum, maximum *vibebridgev1.ProtocolVersion) *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		ConnectionId:  []byte("0123456789abcdef"),
		Sequence:      1,
		Payload: &vibebridgev1.Envelope_Hello{Hello: &vibebridgev1.Hello{
			PeerRole: vibebridgev1.PeerRole_PEER_ROLE_CLIENT,
			SupportedVersions: &vibebridgev1.ProtocolVersionRange{
				Minimum: minimum,
				Maximum: maximum,
			},
			Capabilities:     []string{CapabilityTerminalBinaryOutput},
			MaxEnvelopeBytes: MaxEnvelopeBytes,
		}},
	}
}

func marshalHello(t *testing.T, envelope *vibebridgev1.Envelope) []byte {
	t.Helper()
	encoded, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal Hello: %v", err)
	}
	return encoded
}
