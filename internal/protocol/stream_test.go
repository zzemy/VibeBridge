package protocol

import (
	"bytes"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

func TestAgentStreamSequencesTerminalTrafficAndAcknowledgements(t *testing.T) {
	stream := newTestAgentStream(t, MaxEnvelopeBytes)
	sentAt := time.Date(2026, time.July, 13, 12, 0, 0, 0, time.UTC)

	outputBytes, err := stream.EncodeTerminalOutput([]byte("ready\r\n"), sentAt)
	if err != nil {
		t.Fatalf("encode terminal output: %v", err)
	}
	output := unmarshalStreamEnvelope(t, outputBytes)
	if output.Sequence != 2 || output.Acknowledge != 1 {
		t.Fatalf("output sequence/ack = %d/%d, want 2/1", output.Sequence, output.Acknowledge)
	}
	if got := output.GetTerminalOutput().GetData(); !bytes.Equal(got, []byte("ready\r\n")) {
		t.Fatalf("terminal output = %q", got)
	}

	inputBytes := marshalClientStreamEnvelope(t, 2, 2, &vibebridgev1.Envelope_TerminalInput{
		TerminalInput: &vibebridgev1.TerminalInput{Data: []byte("yes\r")},
	})
	input, err := stream.DecodeClientMessage(inputBytes)
	if err != nil {
		t.Fatalf("decode terminal input: %v", err)
	}
	if input.Kind != ClientStreamMessageTerminalInput || input.Sequence != 2 || !bytes.Equal(input.Data, []byte("yes\r")) {
		t.Fatalf("decoded terminal input = %#v", input)
	}
	if err := stream.CommitClientMessage(input.Sequence); err != nil {
		t.Fatalf("commit terminal input: %v", err)
	}

	ackBytes, err := stream.EncodeAcknowledgement(sentAt.Add(time.Second))
	if err != nil {
		t.Fatalf("encode acknowledgement: %v", err)
	}
	ack := unmarshalStreamEnvelope(t, ackBytes)
	if ack.Sequence != 3 || ack.Acknowledge != 2 || ack.GetAcknowledgement() == nil {
		t.Fatalf("acknowledgement sequence/ack/payload = %d/%d/%T, want 3/2/Acknowledgement", ack.Sequence, ack.Acknowledge, ack.Payload)
	}
}

func TestAgentStreamBindsSessionAndSequencesResumeTraffic(t *testing.T) {
	stream := newTestAgentResumeStream(t, MaxEnvelopeBytes)
	sessionID := []byte("fedcba9876543210")
	attachBytes := marshalClientSessionEnvelope(t, nil, 0, 2, 1, &vibebridgev1.Envelope_AttachSession{
		AttachSession: &vibebridgev1.AttachSession{LastAcknowledgedSequence: 9},
	})
	attachEnvelope := unmarshalStreamEnvelope(t, attachBytes)
	attachEnvelope.SessionId = append([]byte(nil), sessionID...)
	attachEnvelope.SessionGeneration = 7
	attachBytes, _ = proto.Marshal(attachEnvelope)

	attach, err := stream.DecodeClientMessage(attachBytes)
	if err != nil {
		t.Fatalf("decode resume attachment: %v", err)
	}
	if attach.Kind != ClientStreamMessageAttachSession || attach.LastAcknowledgedSequence != 9 || attach.SessionGeneration != 7 || !bytes.Equal(attach.SessionID, sessionID) {
		t.Fatalf("decoded attachment = %#v", attach)
	}
	if err := stream.CommitClientMessage(attach.Sequence); err != nil {
		t.Fatalf("commit attachment: %v", err)
	}
	if err := stream.BindSession(sessionID, 7); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	if _, err := stream.EncodeSessionStatus(vibebridgev1.ResumeDisposition(99), time.Now()); err == nil {
		t.Fatal("unknown resume disposition was accepted")
	}

	statusBytes, err := stream.EncodeSessionStatus(vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED, time.Now())
	if err != nil {
		t.Fatalf("encode session status: %v", err)
	}
	status := unmarshalStreamEnvelope(t, statusBytes)
	if status.Sequence != 2 || status.Acknowledge != 2 || status.GetSessionStatus().GetResumeDisposition() != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED {
		t.Fatalf("status sequence/ack/disposition = %d/%d/%v", status.Sequence, status.Acknowledge, status.GetSessionStatus().GetResumeDisposition())
	}
	if !bytes.Equal(status.SessionId, sessionID) || status.SessionGeneration != 7 {
		t.Fatalf("status session metadata = %x/%d", status.SessionId, status.SessionGeneration)
	}

	outputBytes, err := stream.EncodeTerminalOutput([]byte("restored\r\n"), time.Now())
	if err != nil {
		t.Fatalf("encode resumed output: %v", err)
	}
	output := unmarshalStreamEnvelope(t, outputBytes)
	if output.Sequence != 3 || !bytes.Equal(output.SessionId, sessionID) || output.SessionGeneration != 7 {
		t.Fatalf("output sequence/session = %d/%x/%d", output.Sequence, output.SessionId, output.SessionGeneration)
	}
	if got := stream.HighestOutboundSequence(); got != 3 {
		t.Fatalf("highest outbound sequence = %d, want 3", got)
	}
}

func TestAgentResumeStreamRejectsTrafficBeforeAttachAndMismatchedSessionMetadata(t *testing.T) {
	stream := newTestAgentResumeStream(t, MaxEnvelopeBytes)
	terminalInput := marshalClientStreamEnvelope(t, 2, 1, &vibebridgev1.Envelope_TerminalInput{
		TerminalInput: &vibebridgev1.TerminalInput{Data: []byte("x")},
	})
	if _, err := stream.DecodeClientMessage(terminalInput); err == nil {
		t.Fatal("terminal input before AttachSession was accepted")
	}

	sessionID := []byte("fedcba9876543210")
	attach := marshalClientSessionEnvelope(t, nil, 0, 2, 1, &vibebridgev1.Envelope_AttachSession{AttachSession: &vibebridgev1.AttachSession{}})
	message, err := stream.DecodeClientMessage(attach)
	if err != nil {
		t.Fatalf("decode fresh attachment: %v", err)
	}
	if err := stream.CommitClientMessage(message.Sequence); err != nil {
		t.Fatalf("commit fresh attachment: %v", err)
	}
	if err := stream.BindSession(sessionID, 3); err != nil {
		t.Fatalf("bind session: %v", err)
	}

	for _, testCase := range []struct {
		name       string
		sessionID  []byte
		generation uint64
	}{
		{name: "session ID mismatch", sessionID: []byte("0123456789abcdef"), generation: 3},
		{name: "generation mismatch", sessionID: sessionID, generation: 2},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			encoded := marshalClientSessionEnvelope(t, testCase.sessionID, testCase.generation, 3, 1, &vibebridgev1.Envelope_Acknowledgement{Acknowledgement: &vibebridgev1.Acknowledgement{}})
			if _, err := stream.DecodeClientMessage(encoded); err == nil {
				t.Fatal("mismatched session metadata was accepted")
			}
		})
	}
}

func TestAgentStreamRejectsDuplicateOutOfOrderAndInvalidAcknowledgement(t *testing.T) {
	tests := []struct {
		name     string
		sequence uint64
		ack      uint64
	}{
		{name: "duplicate Hello sequence", sequence: 1, ack: 0},
		{name: "sequence gap", sequence: 3, ack: 0},
		{name: "acknowledges unsent message", sequence: 2, ack: 2},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			stream := newTestAgentStream(t, MaxEnvelopeBytes)
			encoded := marshalClientStreamEnvelope(t, testCase.sequence, testCase.ack, &vibebridgev1.Envelope_TerminalInput{
				TerminalInput: &vibebridgev1.TerminalInput{Data: []byte("x")},
			})
			if _, err := stream.DecodeClientMessage(encoded); err == nil {
				t.Fatal("invalid client envelope was accepted")
			}
		})
	}
}

func TestAgentStreamChunksTerminalOutputToNegotiatedPeerLimit(t *testing.T) {
	const peerLimit = 80
	stream := newTestAgentStream(t, peerLimit)
	remaining := bytes.Repeat([]byte("x"), 200)
	var reconstructed []byte
	var wantSequence uint64 = 2
	for len(remaining) > 0 {
		encoded, consumed, err := stream.EncodeTerminalOutputChunk(remaining, time.Now())
		if err != nil {
			t.Fatalf("encode terminal output chunk: %v", err)
		}
		if len(encoded) > peerLimit {
			t.Fatalf("encoded chunk size = %d, exceeds %d", len(encoded), peerLimit)
		}
		envelope := unmarshalStreamEnvelope(t, encoded)
		if envelope.Sequence != wantSequence {
			t.Fatalf("chunk sequence = %d, want %d", envelope.Sequence, wantSequence)
		}
		wantSequence++
		reconstructed = append(reconstructed, envelope.GetTerminalOutput().GetData()...)
		remaining = remaining[consumed:]
	}
	if !bytes.Equal(reconstructed, bytes.Repeat([]byte("x"), 200)) {
		t.Fatalf("reconstructed terminal output has %d bytes, want 200", len(reconstructed))
	}
}

func TestAgentStreamDoesNotAdvanceSequenceWhenEnvelopeExceedsPeerLimit(t *testing.T) {
	stream := newTestAgentStream(t, 1)
	if _, err := stream.EncodeTerminalOutput([]byte("x"), time.Now()); err == nil {
		t.Fatal("output exceeding peer limit was accepted")
	}
	if _, err := stream.EncodeTerminalOutput([]byte("x"), time.Now()); err == nil {
		t.Fatal("second output exceeding peer limit was accepted")
	}
}

func newTestAgentStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
	})
	if err != nil {
		t.Fatalf("create Agent stream: %v", err)
	}
	return stream
}

func newTestAgentResumeStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities:         map[string]struct{}{CapabilitySessionResume: {}},
	})
	if err != nil {
		t.Fatalf("create resumable Agent stream: %v", err)
	}
	return stream
}

func marshalClientStreamEnvelope(t *testing.T, sequence, acknowledge uint64, payload any) []byte {
	t.Helper()
	return marshalClientSessionEnvelope(t, nil, 0, sequence, acknowledge, payload)
}

func marshalClientSessionEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64, payload any) []byte {
	t.Helper()
	envelope := &vibebridgev1.Envelope{
		ProtocolMajor:     CurrentMajor,
		ProtocolMinor:     CurrentMinor,
		ConnectionId:      []byte("0123456789abcdef"),
		SessionId:         append([]byte(nil), sessionID...),
		SessionGeneration: generation,
		Sequence:          sequence,
		Acknowledge:       acknowledge,
	}
	switch value := payload.(type) {
	case *vibebridgev1.Envelope_AttachSession:
		envelope.Payload = value
	case *vibebridgev1.Envelope_TerminalInput:
		envelope.Payload = value
	case *vibebridgev1.Envelope_Acknowledgement:
		envelope.Payload = value
	default:
		t.Fatalf("unsupported test payload %T", payload)
	}
	encoded, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal client stream envelope: %v", err)
	}
	return encoded
}

func unmarshalStreamEnvelope(t *testing.T, encoded []byte) *vibebridgev1.Envelope {
	t.Helper()
	envelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, envelope); err != nil {
		t.Fatalf("unmarshal stream envelope: %v", err)
	}
	return envelope
}
