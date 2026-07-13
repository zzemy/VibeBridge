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
	CapabilityTerminalResizeEnd   = "terminal.resize_end_v1"
	CapabilitySessionProcessExit  = "session.process_exit_v1"
	CapabilitySessionResume       = "session.resume_v1"
	MaxTerminalInputBytes         = 32 * 1024
	MaxTerminalDimension          = 65_535
)

type ClientStreamMessageKind uint8

const (
	ClientStreamMessageAttachSession ClientStreamMessageKind = iota + 1
	ClientStreamMessageTerminalInput
	ClientStreamMessageTerminalResize
	ClientStreamMessageEndSession
	ClientStreamMessageAcknowledgement
)

type ClientStreamMessage struct {
	Kind                     ClientStreamMessageKind
	Sequence                 uint64
	Data                     []byte
	SessionID                []byte
	SessionGeneration        uint64
	LastAcknowledgedSequence uint64
	Columns                  uint32
	Rows                     uint32
}

// AgentStream owns connection-local ordering state after Hello negotiation and,
// when negotiated, binds that connection to one resumable PTY generation.
type AgentStream struct {
	mu sync.Mutex

	connectionID        []byte
	peerMaxEnvelopeSize uint32
	nextOutbound        uint64
	nextInbound         uint64
	highestInbound      uint64
	highestPeerAck      uint64
	sessionResume       bool
	terminalResizeEnd   bool
	sessionProcessExit  bool
	sessionBound        bool
	sessionID           []byte
	sessionGeneration   uint64
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
		sessionResume:       negotiated.HasCapability(CapabilitySessionResume),
		terminalResizeEnd:   negotiated.HasCapability(CapabilityTerminalResizeEnd),
		sessionProcessExit:  negotiated.HasCapability(CapabilitySessionProcessExit),
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
	case *vibebridgev1.Envelope_AttachSession:
		if !s.sessionResume || s.sessionBound {
			return ClientStreamMessage{}, errors.New("AttachSession is not valid for this stream state")
		}
		message.Kind = ClientStreamMessageAttachSession
		message.SessionID = append([]byte(nil), envelope.SessionId...)
		message.SessionGeneration = envelope.SessionGeneration
		message.LastAcknowledgedSequence = payload.AttachSession.LastAcknowledgedSequence
	case *vibebridgev1.Envelope_TerminalInput:
		if len(payload.TerminalInput.Data) == 0 || len(payload.TerminalInput.Data) > MaxTerminalInputBytes {
			return ClientStreamMessage{}, fmt.Errorf("terminal input size must be between 1 and %d bytes", MaxTerminalInputBytes)
		}
		message.Kind = ClientStreamMessageTerminalInput
		message.Data = append([]byte(nil), payload.TerminalInput.Data...)
	case *vibebridgev1.Envelope_TerminalResize:
		if !s.terminalResizeEnd {
			return ClientStreamMessage{}, errors.New("terminal resize/end was not negotiated")
		}
		if payload.TerminalResize.Columns == 0 || payload.TerminalResize.Columns > MaxTerminalDimension ||
			payload.TerminalResize.Rows == 0 || payload.TerminalResize.Rows > MaxTerminalDimension {
			return ClientStreamMessage{}, fmt.Errorf("terminal dimensions must be between 1 and %d", MaxTerminalDimension)
		}
		message.Kind = ClientStreamMessageTerminalResize
		message.Columns = payload.TerminalResize.Columns
		message.Rows = payload.TerminalResize.Rows
	case *vibebridgev1.Envelope_EndSession:
		if !s.terminalResizeEnd {
			return ClientStreamMessage{}, errors.New("terminal resize/end was not negotiated")
		}
		message.Kind = ClientStreamMessageEndSession
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
	if s.sessionResume && !s.sessionBound {
		return nil, errors.New("session must be bound before terminal output")
	}
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
	if s.sessionResume && !s.sessionBound {
		return nil, 0, errors.New("session must be bound before terminal output")
	}

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

// BindSession binds a resume-enabled physical connection to one PTY session
// generation. The AttachSession envelope must first be decoded and committed
// with CommitClientMessage. It returns an error when resume was not negotiated,
// the stream is already bound, the attach was not committed, or the identity is
// not a 16-byte session ID with a positive generation.
func (s *AgentStream) BindSession(sessionID []byte, generation uint64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionResume {
		return errors.New("session resume was not negotiated")
	}
	if s.sessionBound {
		return errors.New("session is already bound")
	}
	if s.highestInbound < 2 {
		return errors.New("AttachSession must be committed before binding")
	}
	if len(sessionID) != connectionIDBytes || generation == 0 {
		return errors.New("session ID must be 16 bytes and generation must be positive")
	}
	s.sessionID = append([]byte(nil), sessionID...)
	s.sessionGeneration = generation
	s.sessionBound = true
	return nil
}

// EncodeSessionStatus encodes the next connection-local outbound envelope for
// a bound resume-enabled session. Callers must invoke it after BindSession and
// before replay or live output. It rejects unknown dispositions and returns an
// error when the stream is not bound or the envelope exceeds the peer limit.
func (s *AgentStream) EncodeSessionStatus(disposition vibebridgev1.ResumeDisposition, sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionResume || !s.sessionBound {
		return nil, errors.New("session must be bound before SessionStatus")
	}
	switch disposition {
	case vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_FRESH,
		vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED,
		vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED:
	default:
		return nil, errors.New("resume disposition is invalid")
	}
	return s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_SessionStatus{SessionStatus: &vibebridgev1.SessionStatus{
		ResumeDisposition: disposition,
	}}}, sentAt)
}

// EncodeProcessExit encodes a safe final PTY lifecycle result. It rejects
// unnegotiated streams, unknown outcomes, and unbound resumable sessions.
func (s *AgentStream) EncodeProcessExit(outcome vibebridgev1.ProcessExitOutcome, sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.sessionProcessExit {
		return nil, errors.New("session process exit was not negotiated")
	}
	if s.sessionResume && !s.sessionBound {
		return nil, errors.New("session must be bound before ProcessExit")
	}
	switch outcome {
	case vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_SUCCESS,
		vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_FAILURE:
	default:
		return nil, errors.New("process exit outcome is invalid")
	}
	return s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_ProcessExit{ProcessExit: &vibebridgev1.ProcessExit{
		Outcome: outcome,
	}}}, sentAt)
}

// UsesSessionProcessExit reports whether session.process_exit_v1 was negotiated
// for this physical connection.
func (s *AgentStream) UsesSessionProcessExit() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionProcessExit
}

// UsesTerminalResizeEnd reports whether terminal.resize_end_v1 was negotiated
// for this physical connection.
func (s *AgentStream) UsesTerminalResizeEnd() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.terminalResizeEnd
}

// UsesSessionResume reports whether session.resume_v1 was negotiated for this
// physical connection.
func (s *AgentStream) UsesSessionResume() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessionResume
}

// HighestOutboundSequence returns the highest sequence successfully encoded on
// this physical connection. Sequence numbers are connection-local and start at
// one for the agent Hello envelope.
func (s *AgentStream) HighestOutboundSequence() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.nextOutbound - 1
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
	if s.sessionResume {
		if s.sessionBound {
			if !bytes.Equal(envelope.SessionId, s.sessionID) || envelope.SessionGeneration != s.sessionGeneration {
				return errors.New("client envelope session metadata does not match the bound session")
			}
		} else {
			if envelope.GetAttachSession() == nil {
				return errors.New("first client stream message must contain AttachSession")
			}
			fresh := len(envelope.SessionId) == 0 && envelope.SessionGeneration == 0
			resume := len(envelope.SessionId) == connectionIDBytes && envelope.SessionGeneration > 0
			if !fresh && !resume {
				return errors.New("AttachSession has invalid session metadata")
			}
			if fresh && envelope.GetAttachSession().LastAcknowledgedSequence != 0 {
				return errors.New("fresh AttachSession cannot carry a resume cursor")
			}
		}
	} else if len(envelope.SessionId) != 0 || envelope.SessionGeneration != 0 {
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
		ProtocolMajor:     CurrentMajor,
		ProtocolMinor:     CurrentMinor,
		ConnectionId:      append([]byte(nil), s.connectionID...),
		SessionId:         append([]byte(nil), s.sessionID...),
		SessionGeneration: s.sessionGeneration,
		Sequence:          s.nextOutbound,
		Acknowledge:       s.highestInbound,
		SentAt:            timestamppb.New(sentAt.UTC()),
		Payload: &vibebridgev1.Envelope_TerminalOutput{TerminalOutput: &vibebridgev1.TerminalOutput{
			Data: append([]byte(nil), data...),
		}},
	}
}

func (s *AgentStream) encodeLocked(envelope *vibebridgev1.Envelope, sentAt time.Time) ([]byte, error) {
	envelope.ProtocolMajor = CurrentMajor
	envelope.ProtocolMinor = CurrentMinor
	envelope.ConnectionId = append([]byte(nil), s.connectionID...)
	envelope.SessionId = append([]byte(nil), s.sessionID...)
	envelope.SessionGeneration = s.sessionGeneration
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
