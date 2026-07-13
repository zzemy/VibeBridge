package protocol

import (
	"bytes"
	"errors"
	"fmt"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	CapabilityTerminalSequencedIO = "terminal.sequenced_io_v1"
	MaxTerminalInputBytes         = 32 * 1024
)

type ClientStreamMessageKind uint8

const (
	ClientStreamMessageTerminalInput ClientStreamMessageKind = iota + 1
	ClientStreamMessageAcknowledgement
)

type ClientStreamMessage struct {
	Kind     ClientStreamMessageKind
	Sequence uint64
	Data     []byte
}

// AgentStream owns connection-local ordering state after Hello negotiation.
// Session-level generation and resume state are introduced separately so the
// current legacy reconnect behavior remains unchanged during migration.
type AgentStream struct {
	mu sync.Mutex

	connectionID        []byte
	peerMaxEnvelopeSize uint32
	nextOutbound        uint64
	nextInbound         uint64
	highestInbound      uint64
	highestPeerAck      uint64
}

func NewAgentStream(negotiated NegotiatedHello) (*AgentStream, error) {
	if len(negotiated.ConnectionID) != connectionIDBytes {
		return nil, fmt.Errorf("connection ID must be %d bytes", connectionIDBytes)
	}
	if negotiated.PeerMaxEnvelopeBytes == 0 {
		return nil, errors.New("peer max envelope bytes must be positive")
	}
	return &AgentStream{
		connectionID:        append([]byte(nil), negotiated.ConnectionID...),
		peerMaxEnvelopeSize: negotiated.PeerMaxEnvelopeBytes,
		nextOutbound:        2,
		nextInbound:         2,
		highestInbound:      1,
	}, nil
}

func (s *AgentStream) DecodeClientMessage(encoded []byte) (ClientStreamMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(encoded) == 0 || len(encoded) > int(MaxEnvelopeBytes) {
		return ClientStreamMessage{}, fmt.Errorf("envelope size must be between 1 and %d bytes", MaxEnvelopeBytes)
	}
	envelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, envelope); err != nil {
		return ClientStreamMessage{}, fmt.Errorf("decode client envelope: %w", err)
	}
	if err := s.validateInboundEnvelope(envelope); err != nil {
		return ClientStreamMessage{}, err
	}

	message := ClientStreamMessage{Sequence: envelope.Sequence}
	switch payload := envelope.Payload.(type) {
	case *vibebridgev1.Envelope_TerminalInput:
		if len(payload.TerminalInput.Data) == 0 || len(payload.TerminalInput.Data) > MaxTerminalInputBytes {
			return ClientStreamMessage{}, fmt.Errorf("terminal input size must be between 1 and %d bytes", MaxTerminalInputBytes)
		}
		message.Kind = ClientStreamMessageTerminalInput
		message.Data = append([]byte(nil), payload.TerminalInput.Data...)
	case *vibebridgev1.Envelope_Acknowledgement:
		message.Kind = ClientStreamMessageAcknowledgement
	default:
		return ClientStreamMessage{}, errors.New("client envelope contains an unsupported payload")
	}

	// Peer acknowledgement is safe to retain once the envelope itself has been
	// validated; payload sequence is committed only after its side effect succeeds.
	s.highestPeerAck = envelope.Acknowledge
	return message, nil
}

func (s *AgentStream) CommitClientMessage(sequence uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sequence != s.nextInbound {
		return fmt.Errorf("client sequence %d cannot be committed; expected %d", sequence, s.nextInbound)
	}
	s.highestInbound = sequence
	s.nextInbound++
	return nil
}

func (s *AgentStream) EncodeTerminalOutput(data []byte, sentAt time.Time) ([]byte, error) {
	if len(data) == 0 {
		return nil, errors.New("terminal output must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	encoded, err := s.marshalLocked(s.terminalOutputEnvelope(data, sentAt))
	if err != nil {
		return nil, err
	}
	s.nextOutbound++
	return encoded, nil
}

// EncodeTerminalOutputChunk encodes the largest non-empty prefix that fits the
// negotiated peer limit. The caller writes it and repeats with the remainder.
func (s *AgentStream) EncodeTerminalOutputChunk(data []byte, sentAt time.Time) ([]byte, int, error) {
	if len(data) == 0 {
		return nil, 0, errors.New("terminal output must not be empty")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	low, high := 1, len(data)
	bestLength := 0
	var best []byte
	for low <= high {
		middle := low + (high-low)/2
		envelope := s.terminalOutputEnvelope(data[:middle], sentAt)
		encoded, err := s.marshalLocked(envelope)
		if err == nil {
			bestLength = middle
			best = encoded
			low = middle + 1
		} else {
			high = middle - 1
		}
	}
	if bestLength == 0 {
		return nil, 0, errors.New("negotiated envelope limit cannot carry terminal output")
	}
	s.nextOutbound++
	return best, bestLength, nil
}

func (s *AgentStream) EncodeAcknowledgement(sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_Acknowledgement{Acknowledgement: &vibebridgev1.Acknowledgement{}}}, sentAt)
}

func (s *AgentStream) validateInboundEnvelope(envelope *vibebridgev1.Envelope) error {
	if envelope.ProtocolMajor != CurrentMajor || envelope.ProtocolMinor != CurrentMinor {
		return errors.New("client envelope uses an unsupported protocol version")
	}
	if !bytes.Equal(envelope.ConnectionId, s.connectionID) {
		return errors.New("client envelope connection ID does not match")
	}
	if len(envelope.SessionId) != 0 || envelope.SessionGeneration != 0 {
		return errors.New("connection-local envelope contains session metadata")
	}
	if envelope.Sequence != s.nextInbound {
		return fmt.Errorf("client sequence = %d, expected %d", envelope.Sequence, s.nextInbound)
	}
	if envelope.Acknowledge < s.highestPeerAck || envelope.Acknowledge >= s.nextOutbound {
		return errors.New("client acknowledgement is invalid or refers to an unsent sequence")
	}
	return nil
}

func (s *AgentStream) terminalOutputEnvelope(data []byte, sentAt time.Time) *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: CurrentMajor,
		ProtocolMinor: CurrentMinor,
		ConnectionId:  append([]byte(nil), s.connectionID...),
		Sequence:      s.nextOutbound,
		Acknowledge:   s.highestInbound,
		SentAt:        timestamppb.New(sentAt.UTC()),
		Payload: &vibebridgev1.Envelope_TerminalOutput{TerminalOutput: &vibebridgev1.TerminalOutput{
			Data: append([]byte(nil), data...),
		}},
	}
}

func (s *AgentStream) encodeLocked(envelope *vibebridgev1.Envelope, sentAt time.Time) ([]byte, error) {
	envelope.ProtocolMajor = CurrentMajor
	envelope.ProtocolMinor = CurrentMinor
	envelope.ConnectionId = append([]byte(nil), s.connectionID...)
	envelope.Sequence = s.nextOutbound
	envelope.Acknowledge = s.highestInbound
	envelope.SentAt = timestamppb.New(sentAt.UTC())
	encoded, err := s.marshalLocked(envelope)
	if err != nil {
		return nil, err
	}
	s.nextOutbound++
	return encoded, nil
}

func (s *AgentStream) marshalLocked(envelope *vibebridgev1.Envelope) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("encode Agent envelope: %w", err)
	}
	limit := min(uint32(MaxEnvelopeBytes), s.peerMaxEnvelopeSize)
	if len(encoded) > int(limit) {
		return nil, fmt.Errorf("Agent envelope exceeds negotiated %d-byte limit", limit)
	}
	return encoded, nil
}
