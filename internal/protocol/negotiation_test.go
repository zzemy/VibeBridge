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

func TestAcceptClientHelloNegotiatesAttachmentTransferWithSequencedIO(t *testing.T) {
	envelope := clientHello(
		&vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
		&vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
	)
	envelope.GetHello().Capabilities = append(
		envelope.GetHello().Capabilities,
		CapabilityTerminalSequencedIO,
		CapabilityControlError,
		CapabilityAttachmentTransfer,
	)

	negotiated, err := AcceptClientHello(marshalHello(t, envelope))
	if err != nil {
		t.Fatalf("accept client Hello: %v", err)
	}
	if !negotiated.HasCapability(CapabilityAttachmentTransfer) {
		t.Fatal("attachment transfer capability was not retained")
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
		{
			name: "resize end without sequenced I/O",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilityTerminalResizeEnd)
			},
		},
		{
			name: "process exit without sequenced I/O",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilitySessionProcessExit)
			},
		},
		{
			name: "control error without sequenced I/O",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilityControlError)
			},
		},
		{
			name: "control health without sequenced I/O",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilityControlHealth)
			},
		},
		{
			name: "attachment transfer without sequenced I/O",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(envelope.GetHello().Capabilities, CapabilityAttachmentTransfer)
			},
		},
		{
			name: "attachment transfer without control error",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(
					envelope.GetHello().Capabilities,
					CapabilityTerminalSequencedIO,
					CapabilityAttachmentTransfer,
				)
			},
		},
		{
			name: "attachment prompt action without transfer",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(
					envelope.GetHello().Capabilities,
					CapabilityTerminalSequencedIO,
					CapabilityControlError,
					CapabilityAttachmentPromptAction,
				)
			},
		},
		{
			name: "attachment prompt action without control error",
			mutate: func(envelope *vibebridgev1.Envelope) {
				envelope.GetHello().Capabilities = append(
					envelope.GetHello().Capabilities,
					CapabilityTerminalSequencedIO,
					CapabilityAttachmentTransfer,
					CapabilityAttachmentPromptAction,
				)
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
	envelope, err := NewAgentHello([]byte("0123456789abcdef"), 1, 0, sentAt, nil)
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
	wantCapabilities := []string{CapabilityTerminalSequencedIO, CapabilityTerminalResizeEnd, CapabilitySessionProcessExit, CapabilitySessionResume, CapabilityControlError, CapabilityControlHealth}
	for _, capability := range envelope.GetHello().GetCapabilities() {
		if capability == CapabilityAttachmentTransfer || capability == CapabilityAttachmentPromptAction {
			t.Fatalf("Agent advertised dark attachment capability %q", capability)
		}
	}
	for _, want := range wantCapabilities {
		found := false
		for _, capability := range envelope.GetHello().GetCapabilities() {
			if capability == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("Agent Hello capabilities = %v, missing %q", envelope.GetHello().GetCapabilities(), want)
		}
	}
	hello := envelope.GetHello()
	if len(hello.GetDeviceId()) != 0 || hello.GetPublicKeyFingerprint() != "" || hello.GetRevocationEpoch() != 0 {
		t.Fatalf("Hello must not advertise identity when caller passes nil, got device_id=%x fingerprint=%q epoch=%d",
			hello.GetDeviceId(), hello.GetPublicKeyFingerprint(), hello.GetRevocationEpoch())
	}
}

func TestNewAgentHelloAdvertisesDeviceIdentity(t *testing.T) {
	sentAt := time.Date(2026, time.July, 13, 10, 0, 0, 0, time.UTC)
	identity := &AgentIdentity{
		DeviceID:             []byte("0123456789abcdef"),
		PublicKeyFingerprint: "AB12CD34EF",
		RevocationEpoch:      7,
	}
	envelope, err := NewAgentHello([]byte("0123456789abcdef"), 1, 0, sentAt, identity)
	if err != nil {
		t.Fatalf("create Agent Hello: %v", err)
	}
	hello := envelope.GetHello()
	if string(hello.GetDeviceId()) != string(identity.DeviceID) {
		t.Fatalf("device id = %x, want %x", hello.GetDeviceId(), identity.DeviceID)
	}
	if hello.GetPublicKeyFingerprint() != identity.PublicKeyFingerprint {
		t.Fatalf("fingerprint = %q, want %q", hello.GetPublicKeyFingerprint(), identity.PublicKeyFingerprint)
	}
	if hello.GetRevocationEpoch() != identity.RevocationEpoch {
		t.Fatalf("revocation epoch = %d, want %d", hello.GetRevocationEpoch(), identity.RevocationEpoch)
	}
}

func TestNewAgentHelloRejectsPartialIdentity(t *testing.T) {
	sentAt := time.Date(2026, time.July, 13, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		identity AgentIdentity
	}{
		{
			name:     "missing device id",
			identity: AgentIdentity{PublicKeyFingerprint: "AB12CD34EF", RevocationEpoch: 1},
		},
		{
			name:     "missing fingerprint",
			identity: AgentIdentity{DeviceID: []byte("0123456789abcdef"), RevocationEpoch: 1},
		},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			_, err := NewAgentHello([]byte("0123456789abcdef"), 1, 0, sentAt, &testCase.identity)
			if err == nil {
				t.Fatal("partial Agent identity was accepted")
			}
		})
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
