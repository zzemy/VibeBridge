package e2ee

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
)

const (
	sessionContextSchemaVersion = 1
	sessionPrologueDomain       = "VibeBridge session prologue v1\x00"
)

// Authorizer exposes the Agent's view of a client authorization record. The
// store-level Store in internal/deviceidentity satisfies this interface.
type Authorizer interface {
	AuthorizedDevice(deviceID []byte) (*vibebridgev1.AuthorizedDevice, error)
	RevocationEpoch() uint64
}

var (
	// ErrInvalidSessionHandshake deliberately hides primitive-specific failures.
	ErrInvalidSessionHandshake = errors.New("session handshake is invalid")
	// ErrSessionHandshakeState marks use of a session handshake object after
	// completion, failure, or with the wrong method for the current state.
	ErrSessionHandshakeState = errors.New("session handshake is in an invalid state")
	// ErrSessionAuthorization marks an authorization gate rejection that
	// happened before or after the Noise transcript.
	ErrSessionAuthorization = errors.New("session is not authorized")
)

type SessionInitiatorConfig struct {
	Context          *vibebridgev1.HandshakeContext
	Client           *vibebridgev1.SignedDeviceDescriptor
	Agent            *vibebridgev1.SignedDeviceDescriptor
	StaticPrivateKey []byte
	KnownEpoch       uint64
	Capabilities     []string
	Random           io.Reader
}

type SessionResponderConfig struct {
	Authorizer      Authorizer
	Agent           *vibebridgev1.SignedDeviceDescriptor
	StaticPrivateKey []byte
	Random          io.Reader
}

type SessionResult struct {
	Peer                 *vibebridgev1.SignedDeviceDescriptor
	AuthorizationVersion uint64
	RevocationEpoch      uint64
	Capabilities         []string
	HandshakeHash        []byte
	Transport            *Transport
}

type sessionInitiatorState uint8

const (
	sessionInitiatorReady sessionInitiatorState = iota
	sessionInitiatorAwaitingResponse
	sessionInitiatorComplete
	sessionInitiatorFailed
)

type sessionResponderState uint8

const (
	sessionResponderReady sessionResponderState = iota
	sessionResponderComplete
	sessionResponderFailed
)

type SessionInitiator struct {
	handshake *noise.HandshakeState
	context   *vibebridgev1.HandshakeContext
	client    *vibebridgev1.SignedDeviceDescriptor
	agent     *vibebridgev1.SignedDeviceDescriptor
	known     uint64
	caps      []string
	private   []byte
	state     sessionInitiatorState
}

type SessionResponder struct {
	handshake    *noise.HandshakeState
	context      *vibebridgev1.HandshakeContext
	client       *vibebridgev1.SignedDeviceDescriptor
	authorized   *vibebridgev1.AuthorizedDevice
	capabilities []string
	send         *noise.CipherState
	receive      *noise.CipherState
	private      []byte
	state        sessionResponderState
}

// NewSessionContext creates the exact transcript context authenticated by
// both peers. relayTicket is the raw ticket bytes the Agent issued; pass nil
// for direct connections.
func NewSessionContext(initiatorDeviceID, responderDeviceID, relayTicket []byte) (*vibebridgev1.HandshakeContext, error) {
	ticketHash := sha256.Sum256(relayTicket)
	context := &vibebridgev1.HandshakeContext{
		SchemaVersion: sessionContextSchemaVersion,
		ProtocolVersion: &vibebridgev1.ProtocolVersion{
			Major: protocol.CurrentMajor,
			Minor: protocol.CurrentMinor,
		},
		InitiatorDeviceId: append([]byte(nil), initiatorDeviceID...),
		ResponderDeviceId: append([]byte(nil), responderDeviceID...),
		RelayTicketHash:   ticketHash[:],
		Intent:            vibebridgev1.HandshakeIntent_HANDSHAKE_INTENT_CONTROL_SESSION,
		InvitationId:      nil,
	}
	if err := validateSessionContext(context); err != nil {
		return nil, err
	}
	return context, nil
}

func NewSessionInitiator(config SessionInitiatorConfig) (*SessionInitiator, error) {
	if err := validateSessionInitiatorConfig(config); err != nil {
		return nil, err
	}
	if config.Client.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT ||
		config.Agent.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT {
		return nil, errors.New("session descriptors have invalid device classes")
	}
	if !descriptorSupportsVersion(config.Client, config.Context.ProtocolVersion) ||
		!descriptorSupportsVersion(config.Agent, config.Context.ProtocolVersion) {
		return nil, errors.New("session descriptor does not support the handshake protocol version")
	}
	if !bytes.Equal(config.Context.InitiatorDeviceId, config.Client.DeviceDescriptor.DeviceId) ||
		!bytes.Equal(config.Context.ResponderDeviceId, config.Agent.DeviceDescriptor.DeviceId) {
		return nil, errors.New("session context device IDs do not match descriptors")
	}
	privateKey, err := checkedStaticKey(config.StaticPrivateKey, config.Client.DeviceDescriptor.KeyAgreementPublicKey)
	if err != nil {
		return nil, err
	}
	prologue, err := sessionPrologue(config.Context)
	if err != nil {
		zero(privateKey)
		return nil, err
	}
	peerStatic := append([]byte(nil), config.Agent.DeviceDescriptor.KeyAgreementPublicKey...)
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: pairingCipherSuite,
		Random:      config.Random,
		Pattern:     noise.HandshakeIK,
		Initiator:   true,
		Prologue:    prologue,
		StaticKeypair: noise.DHKey{
			Private: privateKey,
			Public:  append([]byte(nil), config.Client.DeviceDescriptor.KeyAgreementPublicKey...),
		},
		PeerStatic: peerStatic,
	})
	if err != nil {
		zero(privateKey)
		return nil, fmt.Errorf("initialize session handshake: %w", err)
	}
	return &SessionInitiator{
		handshake: handshake,
		context:   proto.Clone(config.Context).(*vibebridgev1.HandshakeContext),
		client:    proto.Clone(config.Client).(*vibebridgev1.SignedDeviceDescriptor),
		agent:     proto.Clone(config.Agent).(*vibebridgev1.SignedDeviceDescriptor),
		known:     config.KnownEpoch,
		caps:      append([]string(nil), config.Capabilities...),
		private:   privateKey,
		state:     sessionInitiatorReady,
	}, nil
}

// Start emits the IK message one. The Agent is authenticated via the
// PeerStatic binding configured in NewSessionInitiator.
func (initiator *SessionInitiator) Start() (*vibebridgev1.SessionHandshakeStart, error) {
	if initiator == nil || initiator.state != sessionInitiatorReady || initiator.handshake == nil {
		return nil, ErrSessionHandshakeState
	}
	payload, err := marshalSessionDeterministic(&vibebridgev1.SessionInitiatorPayload{
		Client:               initiator.client,
		KnownRevocationEpoch: initiator.known,
		Capabilities:         append([]string(nil), initiator.caps...),
	})
	if err != nil {
		initiator.fail()
		return nil, err
	}
	message, _, _, err := initiator.handshake.WriteMessage(nil, payload)
	if err != nil || len(message) > maxNoiseMessageBytes {
		initiator.fail()
		return nil, ErrInvalidSessionHandshake
	}
	initiator.state = sessionInitiatorAwaitingResponse
	return &vibebridgev1.SessionHandshakeStart{
		Context:      proto.Clone(initiator.context).(*vibebridgev1.HandshakeContext),
		NoiseMessage: append([]byte(nil), message...),
	}, nil
}

// Finish consumes the Agent's response and returns the established transport.
func (initiator *SessionInitiator) Finish(response *vibebridgev1.SessionHandshakeResponse) (*SessionResult, error) {
	if initiator == nil || initiator.state != sessionInitiatorAwaitingResponse || initiator.handshake == nil {
		return nil, ErrSessionHandshakeState
	}
	if response == nil || len(response.NoiseMessage) == 0 || len(response.NoiseMessage) > maxNoiseMessageBytes {
		initiator.fail()
		return nil, ErrInvalidSessionHandshake
	}
	payloadBytes, send, receive, err := initiator.handshake.ReadMessage(nil, append([]byte(nil), response.NoiseMessage...))
	if err != nil || send == nil || receive == nil ||
		!bytes.Equal(initiator.handshake.PeerStatic(), initiator.agent.DeviceDescriptor.KeyAgreementPublicKey) {
		initiator.fail()
		return nil, ErrInvalidSessionHandshake
	}
	payload := new(vibebridgev1.SessionResponderPayload)
	if err := unmarshalSessionBounded(payloadBytes, payload); err != nil || payload.Agent == nil ||
		!proto.Equal(payload.Agent, initiator.agent) {
		initiator.fail()
		return nil, ErrInvalidSessionHandshake
	}
	if err := validatePeerDescriptor(payload.Agent, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT,
		initiator.context.ResponderDeviceId, initiator.context.ProtocolVersion); err != nil {
		initiator.fail()
		return nil, ErrInvalidSessionHandshake
	}
	hash := initiator.handshake.ChannelBinding()
	result, err := sessionResult(initiator.agent, hash, send, receive, payload.RevocationEpoch, initiator.caps)
	if err != nil {
		initiator.fail()
		return nil, err
	}
	initiator.state = sessionInitiatorComplete
	initiator.clearHandshakeSecrets()
	return result, nil
}

// Close releases any state held by a non-completed initiator.
func (initiator *SessionInitiator) Close() {
	if initiator == nil {
		return
	}
	if initiator.state != sessionInitiatorComplete {
		initiator.fail()
	}
}

// AcceptSessionStart authenticates IK message one and prepares the response.
// The Agent looks up the client's authorization record before even starting
// the noise transcript, so a revoked or unknown device is rejected with no
// key use.
func AcceptSessionStart(config SessionResponderConfig, start *vibebridgev1.SessionHandshakeStart) (*SessionResponder, *vibebridgev1.SessionHandshakeResponse, *vibebridgev1.SignedDeviceDescriptor, error) {
	if start == nil || start.Context == nil || len(start.NoiseMessage) == 0 || len(start.NoiseMessage) > maxNoiseMessageBytes {
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	if err := validateSessionContext(start.Context); err != nil {
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	if config.Authorizer == nil {
		return nil, nil, nil, ErrSessionAuthorization
	}
	if err := errDescriptor(config.Agent); err != nil ||
		config.Agent.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT ||
		!descriptorSupportsVersion(config.Agent, start.Context.ProtocolVersion) ||
		!bytes.Equal(start.Context.ResponderDeviceId, config.Agent.DeviceDescriptor.DeviceId) {
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	privateKey, err := checkedStaticKey(config.StaticPrivateKey, config.Agent.DeviceDescriptor.KeyAgreementPublicKey)
	if err != nil {
		return nil, nil, nil, err
	}
	authorized, err := config.Authorizer.AuthorizedDevice(start.Context.InitiatorDeviceId)
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	if authorized.State == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	if authorized.Device == nil || authorized.Device.DeviceDescriptor == nil {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	prologue, err := sessionPrologue(start.Context)
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, err
	}
	// IK pattern: the responder learns the initiator's static from the S
	// token in message one. Pre-loading PeerStatic would make the library
	// reject msg 1 with "rs is not nil" because it tries to overwrite rs
	// from the decrypted payload. The authorization check above already
	// gates the initiator's static key, so the post-handshake signature
	// verification closes the loop.
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: pairingCipherSuite,
		Random:      config.Random,
		Pattern:     noise.HandshakeIK,
		Initiator:   false,
		Prologue:    prologue,
		StaticKeypair: noise.DHKey{
			Private: privateKey,
			Public:  append([]byte(nil), config.Agent.DeviceDescriptor.KeyAgreementPublicKey...),
		},
	})
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, fmt.Errorf("initialize session handshake: %w", err)
	}
	payloadBytes, _, _, err := handshake.ReadMessage(nil, append([]byte(nil), start.NoiseMessage...))
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	payload := new(vibebridgev1.SessionInitiatorPayload)
	if err := unmarshalSessionBounded(payloadBytes, payload); err != nil || payload.Client == nil {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	if validatePeerDescriptor(payload.Client, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		start.Context.InitiatorDeviceId, start.Context.ProtocolVersion) != nil {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	if !bytes.Equal(payload.Client.DeviceDescriptor.KeyAgreementPublicKey, authorized.Device.DeviceDescriptor.KeyAgreementPublicKey) ||
		!bytes.Equal(payload.Client.DeviceDescriptor.DeviceId, authorized.Device.DeviceDescriptor.DeviceId) {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	if payload.Client.DeviceDescriptor.KeyVersion < authorized.Device.DeviceDescriptor.KeyVersion {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	if !bytes.Equal(payload.Client.DeviceDescriptor.SigningPublicKey, authorized.Device.DeviceDescriptor.SigningPublicKey) {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	if payload.KnownRevocationEpoch > config.Authorizer.RevocationEpoch() {
		zero(privateKey)
		return nil, nil, nil, ErrSessionAuthorization
	}
	responsePayload, err := marshalSessionDeterministic(&vibebridgev1.SessionResponderPayload{
		Agent:           config.Agent,
		RevocationEpoch: config.Authorizer.RevocationEpoch(),
	})
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, err
	}
	message, send, receive, err := handshake.WriteMessage(nil, responsePayload)
	if err != nil || send == nil || receive == nil || len(message) > maxNoiseMessageBytes {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidSessionHandshake
	}
	wireClient := proto.Clone(payload.Client).(*vibebridgev1.SignedDeviceDescriptor)
	// The noise library returns (c1, c2) in the same order from both
	// WriteMessage and ReadMessage. c1 is the initiator-to-responder
	// direction cipher; c2 is the responder-to-initiator. From the
	// responder's perspective, that means our send is c2 and our receive is
	// c1, so the values from WriteMessage must be swapped before they are
	// stored on the responder.
	responder := &SessionResponder{
		handshake:    handshake,
		context:      proto.Clone(start.Context).(*vibebridgev1.HandshakeContext),
		client:       wireClient,
		authorized:   authorized,
		capabilities: append([]string(nil), payload.Capabilities...),
		send:         receive,
		receive:      send,
		private:      privateKey,
		state:        sessionResponderComplete,
	}
	return responder, &vibebridgev1.SessionHandshakeResponse{NoiseMessage: append([]byte(nil), message...)},
		proto.Clone(wireClient).(*vibebridgev1.SignedDeviceDescriptor), nil
}

// Close releases any state held by a non-completed responder.
func (responder *SessionResponder) Close() {
	if responder == nil {
		return
	}
	if responder.state != sessionResponderComplete {
		responder.fail()
	}
}

// SessionView is the Agent-side view of an established session.
type SessionView struct {
	Peer                 *vibebridgev1.SignedDeviceDescriptor
	AuthorizationVersion uint64
	RevocationEpoch      uint64
	Capabilities         []string
	HandshakeHash        []byte
	Transport            *Transport
}

// View returns the established session state. It must be called exactly once;
// the returned Transport shares the underlying cipher states with the
// responder and is the only handle that should be used to encrypt or decrypt.
func (responder *SessionResponder) View() (*SessionView, error) {
	if responder == nil || responder.state != sessionResponderComplete || responder.handshake == nil {
		return nil, ErrSessionHandshakeState
	}
	return &SessionView{
		Peer:                 proto.Clone(responder.client).(*vibebridgev1.SignedDeviceDescriptor),
		AuthorizationVersion: responder.authorized.AuthorizationVersion,
		RevocationEpoch:      responder.authorized.RevocationEpoch,
		Capabilities:         append([]string(nil), responder.capabilities...),
		HandshakeHash:        append([]byte(nil), responder.handshake.ChannelBinding()...),
		Transport:            newTransport(responder.send, responder.receive),
	}, nil
}

func validateSessionInitiatorConfig(config SessionInitiatorConfig) error {
	if err := validateSessionContext(config.Context); err != nil {
		return err
	}
	if err := errDescriptor(config.Client); err != nil {
		return err
	}
	if err := errDescriptor(config.Agent); err != nil {
		return err
	}
	if len(config.StaticPrivateKey) != deviceidentity.KeyAgreementBytes {
		return fmt.Errorf("static private key must be %d bytes", deviceidentity.KeyAgreementBytes)
	}
	return nil
}

func validateSessionContext(context *vibebridgev1.HandshakeContext) error {
	if context == nil || context.SchemaVersion != sessionContextSchemaVersion || context.ProtocolVersion == nil ||
		context.ProtocolVersion.Major != protocol.CurrentMajor || context.ProtocolVersion.Minor != protocol.CurrentMinor ||
		len(context.InitiatorDeviceId) != deviceidentity.DeviceIDBytes || len(context.ResponderDeviceId) != deviceidentity.DeviceIDBytes ||
		bytes.Equal(context.InitiatorDeviceId, context.ResponderDeviceId) || len(context.RelayTicketHash) != sha256.Size ||
		context.Intent != vibebridgev1.HandshakeIntent_HANDSHAKE_INTENT_CONTROL_SESSION ||
		len(context.InvitationId) != 0 {
		return errors.New("session handshake context is invalid")
	}
	return nil
}

func sessionPrologue(context *vibebridgev1.HandshakeContext) ([]byte, error) {
	if err := validateSessionContext(context); err != nil {
		return nil, err
	}
	encoded, err := marshalSessionDeterministic(context)
	if err != nil {
		return nil, err
	}
	prologue := make([]byte, 0, len(sessionPrologueDomain)+len(encoded))
	prologue = append(prologue, sessionPrologueDomain...)
	prologue = append(prologue, encoded...)
	return prologue, nil
}

func sessionResult(peer *vibebridgev1.SignedDeviceDescriptor, hash []byte, send, receive *noise.CipherState, epoch uint64, caps []string) (*SessionResult, error) {
	if len(hash) != 64 || send == nil || receive == nil {
		return nil, ErrInvalidSessionHandshake
	}
	return &SessionResult{
		Peer:            proto.Clone(peer).(*vibebridgev1.SignedDeviceDescriptor),
		RevocationEpoch: epoch,
		Capabilities:    append([]string(nil), caps...),
		HandshakeHash:   append([]byte(nil), hash...),
		Transport:       newTransport(send, receive),
	}, nil
}

func marshalSessionDeterministic(message proto.Message) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode session handshake payload: %w", err)
	}
	if len(encoded) > maxNoiseMessageBytes {
		return nil, errors.New("session handshake payload is too large")
	}
	return encoded, nil
}

func unmarshalSessionBounded(encoded []byte, message proto.Message) error {
	if len(encoded) == 0 || len(encoded) > maxNoiseMessageBytes {
		return ErrInvalidSessionHandshake
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, message); err != nil || protocol.HasUnknownFields(message) {
		return ErrInvalidSessionHandshake
	}
	return nil
}

func (initiator *SessionInitiator) fail() {
	initiator.state = sessionInitiatorFailed
	initiator.clearHandshakeSecrets()
}

func (initiator *SessionInitiator) clearHandshakeSecrets() {
	zero(initiator.private)
	initiator.private = nil
	initiator.handshake = nil
}

func (responder *SessionResponder) fail() {
	responder.state = sessionResponderFailed
	responder.clearHandshakeSecrets()
}

func (responder *SessionResponder) clearHandshakeSecrets() {
	zero(responder.private)
	responder.private = nil
	responder.handshake = nil
	responder.send = nil
	responder.receive = nil
}
