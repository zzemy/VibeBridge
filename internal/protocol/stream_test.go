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

func marshalClientStreamEnvelope(t *testing.T, sequence, acknowledge uint64, payload any) []byte {
	t.Helper()
	envelope := &vibebridgev1.Envelope{
		ProtocolMajor: CurrentMajor,
		ProtocolMinor: CurrentMinor,
		ConnectionId:  []byte("0123456789abcdef"),
		Sequence:      sequence,
		Acknowledge:   acknowledge,
	}
	switch value := payload.(type) {
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
