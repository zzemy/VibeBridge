package protocol

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	CapabilityTerminalSequencedIO    = "terminal.sequenced_io_v1"
	CapabilityTerminalResizeEnd      = "terminal.resize_end_v1"
	CapabilitySessionProcessExit     = "session.process_exit_v1"
	CapabilitySessionResume          = "session.resume_v1"
	CapabilityControlError           = "control.error_v1"
	CapabilityControlHealth          = "control.health_v1"
	CapabilityAttachmentTransfer     = "attachment.transfer_v1"
	CapabilityAttachmentPromptAction = "attachment.prompt_action_v1"
	MaxTerminalInputBytes            = 32 * 1024
	MaxTerminalDimension             = 65_535
	MaxAttachmentPromptBytes         = 32 * 1024
	MaxAttachmentPromptPreviewBytes  = 48 * 1024
	maxAttachmentTransferIDBytes     = 64
	maxAttachmentPromptActionIDBytes = 64
	maxAttachmentPromptTransfers     = 10
)

type ClientStreamMessageKind uint8

const (
	ClientStreamMessageAttachSession ClientStreamMessageKind = iota + 1
	ClientStreamMessageTerminalInput
	ClientStreamMessageTerminalResize
	ClientStreamMessageEndSession
	ClientStreamMessageAcknowledgement
	ClientStreamMessagePing
	ClientStreamMessageAttachmentBegin
	ClientStreamMessageAttachmentChunk
	ClientStreamMessageAttachmentComplete
	ClientStreamMessageAttachmentCancel
	ClientStreamMessageAttachmentPromptPrepare
	ClientStreamMessageAttachmentPromptCommit
	ClientStreamMessageAttachmentPromptCancel
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
	TransferID               []byte
	DisplayName              string
	DeclaredContentType      string
	DeclaredExtension        string
	TotalSizeBytes           uint64
	TotalSHA256              []byte
	OffsetBytes              uint64
	ChunkSHA256              []byte
	ActionID                 []byte
	TransferIDs              [][]byte
	Prompt                   string
	AppendEnter              bool
}

// AgentStream owns connection-local ordering state after Hello negotiation and,
// when negotiated, binds that connection to one resumable PTY generation.
type AgentStream struct {
	mu sync.Mutex

	connectionID           []byte
	peerMaxEnvelopeSize    uint32
	nextOutbound           uint64
	nextInbound            uint64
	highestInbound         uint64
	highestPeerAck         uint64
	pendingPingSequences   []uint64
	sessionResume          bool
	terminalResizeEnd      bool
	sessionProcessExit     bool
	controlError           bool
	controlHealth          bool
	attachmentTransfer     bool
	attachmentPromptAction bool
	sessionBound           bool
	sessionID              []byte
	sessionGeneration      uint64
}

func NewAgentStream(negotiated NegotiatedHello) (*AgentStream, error) {
	if len(negotiated.ConnectionID) != connectionIDBytes {
		return nil, fmt.Errorf("connection ID must be %d bytes", connectionIDBytes)
	}
	if negotiated.PeerMaxEnvelopeBytes == 0 {
		return nil, errors.New("peer max envelope bytes must be positive")
	}
	return &AgentStream{
		connectionID:           append([]byte(nil), negotiated.ConnectionID...),
		peerMaxEnvelopeSize:    negotiated.PeerMaxEnvelopeBytes,
		nextOutbound:           2,
		nextInbound:            2,
		highestInbound:         1,
		sessionResume:          negotiated.HasCapability(CapabilitySessionResume),
		terminalResizeEnd:      negotiated.HasCapability(CapabilityTerminalResizeEnd),
		sessionProcessExit:     negotiated.HasCapability(CapabilitySessionProcessExit),
		controlError:           negotiated.HasCapability(CapabilityControlError),
		controlHealth:          negotiated.HasCapability(CapabilityControlHealth),
		attachmentTransfer:     negotiated.HasCapability(CapabilityAttachmentTransfer),
		attachmentPromptAction: negotiated.HasCapability(CapabilityAttachmentPromptAction),
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
	case *vibebridgev1.Envelope_Ping:
		if !s.controlHealth {
			return ClientStreamMessage{}, errors.New("control health was not negotiated")
		}
		message.Kind = ClientStreamMessagePing
	case *vibebridgev1.Envelope_AttachmentBegin:
		if !s.attachmentTransfer {
			return ClientStreamMessage{}, errors.New("attachment transfer was not negotiated")
		}
		if payload.AttachmentBegin == nil || !validAttachmentTransferID(payload.AttachmentBegin.TransferId) ||
			payload.AttachmentBegin.TotalSizeBytes == 0 || len(payload.AttachmentBegin.TotalSha256) != sha256.Size {
			return ClientStreamMessage{}, errors.New("AttachmentBegin has invalid transfer metadata")
		}
		message.Kind = ClientStreamMessageAttachmentBegin
		message.TransferID = append([]byte(nil), payload.AttachmentBegin.TransferId...)
		message.DisplayName = payload.AttachmentBegin.DisplayName
		message.DeclaredContentType = payload.AttachmentBegin.DeclaredContentType
		message.DeclaredExtension = payload.AttachmentBegin.DeclaredExtension
		message.TotalSizeBytes = payload.AttachmentBegin.TotalSizeBytes
		message.TotalSHA256 = append([]byte(nil), payload.AttachmentBegin.TotalSha256...)
	case *vibebridgev1.Envelope_AttachmentChunk:
		if !s.attachmentTransfer {
			return ClientStreamMessage{}, errors.New("attachment transfer was not negotiated")
		}
		if payload.AttachmentChunk == nil || !validAttachmentTransferID(payload.AttachmentChunk.TransferId) ||
			len(payload.AttachmentChunk.Data) == 0 || len(payload.AttachmentChunk.ChunkSha256) != sha256.Size {
			return ClientStreamMessage{}, errors.New("AttachmentChunk has invalid transfer metadata")
		}
		message.Kind = ClientStreamMessageAttachmentChunk
		message.TransferID = append([]byte(nil), payload.AttachmentChunk.TransferId...)
		message.OffsetBytes = payload.AttachmentChunk.OffsetBytes
		message.Data = append([]byte(nil), payload.AttachmentChunk.Data...)
		message.ChunkSHA256 = append([]byte(nil), payload.AttachmentChunk.ChunkSha256...)
	case *vibebridgev1.Envelope_AttachmentComplete:
		if !s.attachmentTransfer {
			return ClientStreamMessage{}, errors.New("attachment transfer was not negotiated")
		}
		if payload.AttachmentComplete == nil || !validAttachmentTransferID(payload.AttachmentComplete.TransferId) {
			return ClientStreamMessage{}, errors.New("AttachmentComplete has invalid transfer metadata")
		}
		message.Kind = ClientStreamMessageAttachmentComplete
		message.TransferID = append([]byte(nil), payload.AttachmentComplete.TransferId...)
	case *vibebridgev1.Envelope_AttachmentCancel:
		if !s.attachmentTransfer {
			return ClientStreamMessage{}, errors.New("attachment transfer was not negotiated")
		}
		if payload.AttachmentCancel == nil || !validAttachmentTransferID(payload.AttachmentCancel.TransferId) {
			return ClientStreamMessage{}, errors.New("AttachmentCancel has invalid transfer metadata")
		}
		message.Kind = ClientStreamMessageAttachmentCancel
		message.TransferID = append([]byte(nil), payload.AttachmentCancel.TransferId...)
	case *vibebridgev1.Envelope_AttachmentPromptPrepare:
		if !s.attachmentPromptAction {
			return ClientStreamMessage{}, errors.New("attachment prompt action was not negotiated")
		}
		prepare := payload.AttachmentPromptPrepare
		if prepare == nil || !validAttachmentPromptActionID(prepare.ActionId) ||
			len(prepare.TransferIds) == 0 || len(prepare.TransferIds) > maxAttachmentPromptTransfers ||
			len(prepare.Prompt) == 0 || len(prepare.Prompt) > MaxAttachmentPromptBytes {
			return ClientStreamMessage{}, errors.New("AttachmentPromptPrepare has invalid action metadata")
		}
		message.Kind = ClientStreamMessageAttachmentPromptPrepare
		message.ActionID = append([]byte(nil), prepare.ActionId...)
		message.TransferIDs = make([][]byte, len(prepare.TransferIds))
		for index, transferID := range prepare.TransferIds {
			if !validAttachmentTransferID(transferID) {
				return ClientStreamMessage{}, errors.New("AttachmentPromptPrepare has invalid transfer metadata")
			}
			message.TransferIDs[index] = append([]byte(nil), transferID...)
		}
		message.Prompt = prepare.Prompt
		message.AppendEnter = prepare.AppendEnter
	case *vibebridgev1.Envelope_AttachmentPromptCommit:
		if !s.attachmentPromptAction {
			return ClientStreamMessage{}, errors.New("attachment prompt action was not negotiated")
		}
		if payload.AttachmentPromptCommit == nil || !validAttachmentPromptActionID(payload.AttachmentPromptCommit.ActionId) {
			return ClientStreamMessage{}, errors.New("AttachmentPromptCommit has invalid action metadata")
		}
		message.Kind = ClientStreamMessageAttachmentPromptCommit
		message.ActionID = append([]byte(nil), payload.AttachmentPromptCommit.ActionId...)
	case *vibebridgev1.Envelope_AttachmentPromptCancel:
		if !s.attachmentPromptAction {
			return ClientStreamMessage{}, errors.New("attachment prompt action was not negotiated")
		}
		if payload.AttachmentPromptCancel == nil || !validAttachmentPromptActionID(payload.AttachmentPromptCancel.ActionId) {
			return ClientStreamMessage{}, errors.New("AttachmentPromptCancel has invalid action metadata")
		}
		message.Kind = ClientStreamMessageAttachmentPromptCancel
		message.ActionID = append([]byte(nil), payload.AttachmentPromptCancel.ActionId...)
	default:
		return ClientStreamMessage{}, errors.New("client envelope contains an unsupported payload")
	}

	// Peer acknowledgement is safe to retain once the envelope itself has been
	// validated; payload sequence is committed only after its side effect succeeds.
	s.highestPeerAck = envelope.Acknowledge
	return message, nil
}

func validAttachmentTransferID(transferID []byte) bool {
	return len(transferID) > 0 && len(transferID) <= maxAttachmentTransferIDBytes
}

func validAttachmentPromptActionID(actionID []byte) bool {
	return len(actionID) > 0 && len(actionID) <= maxAttachmentPromptActionIDBytes
}

// CommitClientMessage advances ordering after the caller successfully applies
// the side effect of a message returned by DecodeClientMessage.
func (s *AgentStream) CommitClientMessage(message ClientStreamMessage) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if message.Sequence != s.nextInbound {
		return fmt.Errorf("client sequence %d cannot be committed; expected %d", message.Sequence, s.nextInbound)
	}
	s.highestInbound = message.Sequence
	s.nextInbound++
	if message.Kind == ClientStreamMessagePing {
		s.pendingPingSequences = append(s.pendingPingSequences, message.Sequence)
	}
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

// EncodeAttachmentPromptPreview reports the exact trusted preview for a
// prepared action or the durable state of an already committed action.
func (s *AgentStream) EncodeAttachmentPromptPreview(actionID []byte, disposition vibebridgev1.AttachmentPromptDisposition, preview string, appendEnter bool, sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.attachmentPromptAction {
		return nil, errors.New("attachment prompt action was not negotiated")
	}
	if s.sessionResume && !s.sessionBound {
		return nil, errors.New("session must be bound before AttachmentPromptPreview")
	}
	if !validAttachmentPromptActionID(actionID) {
		return nil, errors.New("attachment prompt action ID is invalid")
	}
	switch disposition {
	case vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_PREPARED:
		if preview == "" || len(preview) > MaxAttachmentPromptPreviewBytes {
			return nil, errors.New("prepared attachment prompt preview is invalid")
		}
	case vibebridgev1.AttachmentPromptDisposition_ATTACHMENT_PROMPT_DISPOSITION_COMMITTED:
		if preview != "" || appendEnter {
			return nil, errors.New("committed attachment prompt preview must be empty")
		}
	default:
		return nil, errors.New("attachment prompt disposition is invalid")
	}
	return s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_AttachmentPromptPreview{AttachmentPromptPreview: &vibebridgev1.AttachmentPromptPreview{
		ActionId:    append([]byte(nil), actionID...),
		Disposition: disposition,
		Preview:     preview,
		AppendEnter: appendEnter,
	}}}, sentAt)
}

// EncodeError encodes an allowlisted application failure. Unlike terminal
// traffic, a resumable stream may report a session-start or active-session error
// before it binds; that envelope carries empty session metadata.
func (s *AgentStream) EncodeError(code vibebridgev1.ErrorCode, sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.controlError {
		return nil, errors.New("control error was not negotiated")
	}
	switch code {
	case vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_SESSION_ALREADY_ACTIVE,
		vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_RESIZE_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_UNSUPPORTED_MESSAGE,
		vibebridgev1.ErrorCode_ERROR_CODE_ATTACHMENT_TRANSFER_FAILED,
		vibebridgev1.ErrorCode_ERROR_CODE_ATTACHMENT_PROMPT_ACTION_FAILED:
	default:
		return nil, errors.New("error code is invalid")
	}
	if s.sessionResume && !s.sessionBound && code != vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED && code != vibebridgev1.ErrorCode_ERROR_CODE_SESSION_ALREADY_ACTIVE {
		return nil, errors.New("only session start or active-session errors are valid before session binding")
	}
	return s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_Error{Error: &vibebridgev1.Error{
		Code: code,
	}}}, sentAt)
}

// EncodePong responds to one committed ordered Ping. It rejects unnegotiated
// streams and resumable streams that have not bound a session.
func (s *AgentStream) EncodePong(sentAt time.Time) ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.controlHealth {
		return nil, errors.New("control health was not negotiated")
	}
	if s.sessionResume && !s.sessionBound {
		return nil, errors.New("session must be bound before Pong")
	}
	if len(s.pendingPingSequences) == 0 {
		return nil, errors.New("Pong requires a committed Ping")
	}
	encoded, err := s.encodeLocked(&vibebridgev1.Envelope{Payload: &vibebridgev1.Envelope_Pong{Pong: &vibebridgev1.Pong{}}}, sentAt)
	if err != nil {
		return nil, err
	}
	s.pendingPingSequences = s.pendingPingSequences[1:]
	return encoded, nil
}

// UsesControlHealth reports whether control.health_v1 was negotiated for this
// physical connection.
func (s *AgentStream) UsesControlHealth() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlHealth
}

// UsesControlError reports whether control.error_v1 was negotiated for this
// physical connection.
func (s *AgentStream) UsesControlError() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.controlError
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
