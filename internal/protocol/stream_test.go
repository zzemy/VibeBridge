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

	inputBytes := marshalClientTerminalInput(t, nil, 0, 2, 2, []byte("yes\r"))
	input, err := stream.DecodeClientMessage(inputBytes)
	if err != nil {
		t.Fatalf("decode terminal input: %v", err)
	}
	if input.Kind != ClientStreamMessageTerminalInput || input.Sequence != 2 || !bytes.Equal(input.Data, []byte("yes\r")) {
		t.Fatalf("decoded terminal input = %#v", input)
	}
	if err := stream.CommitClientMessage(input); err != nil {
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

func TestAgentStreamSequencesNegotiatedHealthCheck(t *testing.T) {
	stream := newTestAgentHealthStream(t, MaxEnvelopeBytes)
	if _, err := stream.EncodePong(time.Now()); err == nil {
		t.Fatal("Pong without a committed Ping was encoded")
	}
	ping, err := stream.DecodeClientMessage(marshalClientPingEnvelope(t, nil, 0, 2, 1))
	if err != nil {
		t.Fatalf("decode Ping: %v", err)
	}
	if ping.Kind != ClientStreamMessagePing || !stream.UsesControlHealth() {
		t.Fatalf("decoded Ping = %#v, health negotiated = %t", ping, stream.UsesControlHealth())
	}
	if err := stream.CommitClientMessage(ping); err != nil {
		t.Fatalf("commit Ping: %v", err)
	}

	encoded, err := stream.EncodePong(time.Date(2026, time.July, 13, 12, 0, 1, 0, time.UTC))
	if err != nil {
		t.Fatalf("encode Pong: %v", err)
	}
	pong := unmarshalStreamEnvelope(t, encoded)
	if pong.Sequence != 2 || pong.Acknowledge != 2 || pong.GetPong() == nil {
		t.Fatalf("Pong sequence/ack/payload = %d/%d/%T, want 2/2/Pong", pong.Sequence, pong.Acknowledge, pong.Payload)
	}
	if _, err := stream.EncodePong(time.Now()); err == nil {
		t.Fatal("second Pong for one committed Ping was encoded")
	}

	if _, err := newTestAgentStream(t, MaxEnvelopeBytes).DecodeClientMessage(marshalClientPingEnvelope(t, nil, 0, 2, 1)); err == nil {
		t.Fatal("unnegotiated Ping was accepted")
	}
	if _, err := newTestAgentStream(t, MaxEnvelopeBytes).EncodePong(time.Now()); err == nil {
		t.Fatal("unnegotiated Pong was encoded")
	}
}

func TestAgentStreamDecodesNegotiatedTerminalResizeAndEnd(t *testing.T) {
	stream := newTestAgentControlStream(t, MaxEnvelopeBytes)

	resize, err := stream.DecodeClientMessage(marshalClientResizeEnvelope(t, nil, 0, 2, 1, 120, 40))
	if err != nil {
		t.Fatalf("decode terminal resize: %v", err)
	}
	if resize.Kind != ClientStreamMessageTerminalResize || resize.Columns != 120 || resize.Rows != 40 {
		t.Fatalf("decoded terminal resize = %#v", resize)
	}
	if err := stream.CommitClientMessage(resize); err != nil {
		t.Fatalf("commit terminal resize: %v", err)
	}

	end, err := stream.DecodeClientMessage(marshalClientEndEnvelope(t, nil, 0, 3, 1))
	if err != nil {
		t.Fatalf("decode EndSession: %v", err)
	}
	if end.Kind != ClientStreamMessageEndSession {
		t.Fatalf("decoded EndSession = %#v", end)
	}
}

func TestAgentStreamRejectsUnnegotiatedOrInvalidTerminalResize(t *testing.T) {
	if _, err := newTestAgentStream(t, MaxEnvelopeBytes).DecodeClientMessage(
		marshalClientResizeEnvelope(t, nil, 0, 2, 1, 80, 24),
	); err == nil {
		t.Fatal("unnegotiated terminal resize was accepted")
	}

	for _, dimensions := range [][2]uint32{{0, 24}, {80, 0}, {MaxTerminalDimension + 1, 24}, {80, MaxTerminalDimension + 1}} {
		stream := newTestAgentControlStream(t, MaxEnvelopeBytes)
		if _, err := stream.DecodeClientMessage(
			marshalClientResizeEnvelope(t, nil, 0, 2, 1, dimensions[0], dimensions[1]),
		); err == nil {
			t.Fatalf("invalid terminal dimensions %v were accepted", dimensions)
		}
	}
}

func TestAgentStreamEncodesNegotiatedProcessExit(t *testing.T) {
	stream := newTestAgentProcessExitStream(t, MaxEnvelopeBytes)
	encoded, err := stream.EncodeProcessExit(vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_SUCCESS, time.Now())
	if err != nil {
		t.Fatalf("encode ProcessExit: %v", err)
	}
	envelope := unmarshalStreamEnvelope(t, encoded)
	if envelope.Sequence != 2 || envelope.Acknowledge != 1 || envelope.GetProcessExit().GetOutcome() != vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_SUCCESS {
		t.Fatalf("ProcessExit sequence/ack/outcome = %d/%d/%v", envelope.Sequence, envelope.Acknowledge, envelope.GetProcessExit().GetOutcome())
	}

	if _, err := newTestAgentStream(t, MaxEnvelopeBytes).EncodeProcessExit(vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_SUCCESS, time.Now()); err == nil {
		t.Fatal("unnegotiated ProcessExit was encoded")
	}
	if _, err := stream.EncodeProcessExit(vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_UNSPECIFIED, time.Now()); err == nil {
		t.Fatal("unspecified ProcessExit outcome was encoded")
	}
}

func TestAgentStreamEncodesNegotiatedError(t *testing.T) {
	stream := newTestAgentErrorStream(t, MaxEnvelopeBytes)
	encoded, err := stream.EncodeError(vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED, time.Now())
	if err != nil {
		t.Fatalf("encode Error: %v", err)
	}
	envelope := unmarshalStreamEnvelope(t, encoded)
	if envelope.Sequence != 2 || envelope.Acknowledge != 1 || envelope.GetError().GetCode() != vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED {
		t.Fatalf("Error sequence/ack/code = %d/%d/%v", envelope.Sequence, envelope.Acknowledge, envelope.GetError().GetCode())
	}
	if !stream.UsesControlError() {
		t.Fatal("stream did not report negotiated control error capability")
	}

	if _, err := newTestAgentStream(t, MaxEnvelopeBytes).EncodeError(vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED, time.Now()); err == nil {
		t.Fatal("unnegotiated Error was encoded")
	}
	for _, code := range []vibebridgev1.ErrorCode{
		vibebridgev1.ErrorCode_ERROR_CODE_UNSPECIFIED,
		vibebridgev1.ErrorCode(99),
	} {
		if _, err := newTestAgentErrorStream(t, MaxEnvelopeBytes).EncodeError(code, time.Now()); err == nil {
			t.Fatalf("invalid Error code %v was encoded", code)
		}
	}
}

func TestAgentStreamEncodesErrorBeforeOrAfterResumeBinding(t *testing.T) {
	unbound := newTestAgentResumeErrorStream(t, MaxEnvelopeBytes)
	encoded, err := unbound.EncodeError(vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED, time.Now())
	if err != nil {
		t.Fatalf("encode pre-bind Error: %v", err)
	}
	envelope := unmarshalStreamEnvelope(t, encoded)
	if len(envelope.SessionId) != 0 || envelope.SessionGeneration != 0 || envelope.Sequence != 2 || envelope.Acknowledge != 1 {
		t.Fatalf("pre-bind Error metadata = session %x/%d sequence/ack %d/%d", envelope.SessionId, envelope.SessionGeneration, envelope.Sequence, envelope.Acknowledge)
	}
	for _, code := range []vibebridgev1.ErrorCode{
		vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_RESIZE_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_UNSUPPORTED_MESSAGE,
	} {
		if _, err := newTestAgentResumeErrorStream(t, MaxEnvelopeBytes).EncodeError(code, time.Now()); err == nil {
			t.Fatalf("pre-bind Error code %v was encoded", code)
		}
	}

	bound := newTestAgentResumeErrorStream(t, MaxEnvelopeBytes)
	attach, err := bound.DecodeClientMessage(marshalClientAttachEnvelope(t, nil, 0, 2, 1, 0))
	if err != nil {
		t.Fatalf("decode fresh attachment: %v", err)
	}
	if err := bound.CommitClientMessage(attach); err != nil {
		t.Fatalf("commit fresh attachment: %v", err)
	}
	sessionID := []byte("fedcba9876543210")
	if err := bound.BindSession(sessionID, 7); err != nil {
		t.Fatalf("bind session: %v", err)
	}
	encoded, err = bound.EncodeError(vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED, time.Now())
	if err != nil {
		t.Fatalf("encode bound Error: %v", err)
	}
	envelope = unmarshalStreamEnvelope(t, encoded)
	if !bytes.Equal(envelope.SessionId, sessionID) || envelope.SessionGeneration != 7 || envelope.Acknowledge != 2 {
		t.Fatalf("bound Error metadata = session %x/%d ack %d", envelope.SessionId, envelope.SessionGeneration, envelope.Acknowledge)
	}
}

func TestAgentStreamBindsSessionAndSequencesResumeTraffic(t *testing.T) {
	stream := newTestAgentResumeStream(t, MaxEnvelopeBytes)
	sessionID := []byte("fedcba9876543210")
	attachBytes := marshalClientAttachEnvelope(t, nil, 0, 2, 1, 9)
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
	if err := stream.CommitClientMessage(attach); err != nil {
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
	terminalInput := marshalClientTerminalInput(t, nil, 0, 2, 1, []byte("x"))
	if _, err := stream.DecodeClientMessage(terminalInput); err == nil {
		t.Fatal("terminal input before AttachSession was accepted")
	}

	sessionID := []byte("fedcba9876543210")
	attach := marshalClientAttachEnvelope(t, nil, 0, 2, 1, 0)
	message, err := stream.DecodeClientMessage(attach)
	if err != nil {
		t.Fatalf("decode fresh attachment: %v", err)
	}
	if err := stream.CommitClientMessage(message); err != nil {
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
			encoded := marshalClientAcknowledgementEnvelope(t, testCase.sessionID, testCase.generation, 3, 1)
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
			encoded := marshalClientTerminalInput(t, nil, 0, testCase.sequence, testCase.ack, []byte("x"))
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

func newTestAgentControlStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities:         map[string]struct{}{CapabilityTerminalResizeEnd: {}},
	})
	if err != nil {
		t.Fatalf("create terminal-control Agent stream: %v", err)
	}
	return stream
}

func newTestAgentHealthStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities:         map[string]struct{}{CapabilityControlHealth: {}},
	})
	if err != nil {
		t.Fatalf("create control-health Agent stream: %v", err)
	}
	return stream
}

func newTestAgentErrorStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities:         map[string]struct{}{CapabilityControlError: {}},
	})
	if err != nil {
		t.Fatalf("create control-error Agent stream: %v", err)
	}
	return stream
}

func newTestAgentResumeErrorStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities: map[string]struct{}{
			CapabilityControlError:  {},
			CapabilitySessionResume: {},
		},
	})
	if err != nil {
		t.Fatalf("create resumable control-error Agent stream: %v", err)
	}
	return stream
}

func newTestAgentProcessExitStream(t *testing.T, peerLimit uint32) *AgentStream {
	t.Helper()
	stream, err := NewAgentStream(NegotiatedHello{
		Major:                CurrentMajor,
		Minor:                CurrentMinor,
		PeerMaxEnvelopeBytes: peerLimit,
		ConnectionID:         []byte("0123456789abcdef"),
		capabilities:         map[string]struct{}{CapabilitySessionProcessExit: {}},
	})
	if err != nil {
		t.Fatalf("create process-exit Agent stream: %v", err)
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

func marshalClientTerminalInput(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64, data []byte) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_TerminalInput{TerminalInput: &vibebridgev1.TerminalInput{Data: append([]byte(nil), data...)}}
	return marshalClientStreamEnvelope(t, envelope)
}

func marshalClientPingEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_Ping{Ping: &vibebridgev1.Ping{}}
	return marshalClientStreamEnvelope(t, envelope)
}

func marshalClientAttachEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge, cursor uint64) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_AttachSession{AttachSession: &vibebridgev1.AttachSession{LastAcknowledgedSequence: cursor}}
	return marshalClientStreamEnvelope(t, envelope)
}

func marshalClientAcknowledgementEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_Acknowledgement{Acknowledgement: &vibebridgev1.Acknowledgement{}}
	return marshalClientStreamEnvelope(t, envelope)
}

func marshalClientResizeEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64, columns, rows uint32) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_TerminalResize{TerminalResize: &vibebridgev1.TerminalResize{Columns: columns, Rows: rows}}
	return marshalClientStreamEnvelope(t, envelope)
}

func marshalClientEndEnvelope(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64) []byte {
	t.Helper()
	envelope := newClientStreamEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_EndSession{EndSession: &vibebridgev1.EndSession{}}
	return marshalClientStreamEnvelope(t, envelope)
}

func newClientStreamEnvelope(sessionID []byte, generation, sequence, acknowledge uint64) *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor:     CurrentMajor,
		ProtocolMinor:     CurrentMinor,
		ConnectionId:      []byte("0123456789abcdef"),
		SessionId:         append([]byte(nil), sessionID...),
		SessionGeneration: generation,
		Sequence:          sequence,
		Acknowledge:       acknowledge,
	}
}

func marshalClientStreamEnvelope(t *testing.T, envelope *vibebridgev1.Envelope) []byte {
	t.Helper()
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
