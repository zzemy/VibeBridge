// Package e2ee implements VibeBridge's authenticated Noise handshakes.
package e2ee

import (
	"bytes"
	"crypto/ecdh"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"

	"github.com/flynn/noise"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
)

const (
	pairingContextSchemaVersion = 1
	pairingPrologueDomain       = "VibeBridge pairing prologue v1\x00"
	pairingSASDomain            = "VibeBridge pairing SAS v1\x00"
	maxNoiseMessageBytes        = noise.MaxMsgLen
)

var (
	// ErrInvalidHandshake deliberately hides primitive-specific failures from protocol callers.
	ErrInvalidHandshake = errors.New("pairing handshake is invalid")
	ErrHandshakeState   = errors.New("pairing handshake is in an invalid state")
)

var pairingCipherSuite = noise.NewCipherSuite(noise.DH25519, noise.CipherChaChaPoly, noise.HashBLAKE2b)

type PairingInitiatorConfig struct {
	Context          *vibebridgev1.HandshakeContext
	Client           *vibebridgev1.SignedDeviceDescriptor
	Agent            *vibebridgev1.SignedDeviceDescriptor
	StaticPrivateKey []byte
	BootstrapSecret  []byte
	Random           io.Reader
}

type PairingResponderConfig struct {
	ExpectedContext  *vibebridgev1.HandshakeContext
	Agent            *vibebridgev1.SignedDeviceDescriptor
	StaticPrivateKey []byte
	BootstrapSecret  []byte
	Random           io.Reader
}

type PairingResult struct {
	Peer          *vibebridgev1.SignedDeviceDescriptor
	SAS           string
	HandshakeHash []byte
	Transport     *Transport
}

type PairingInitiator struct {
	handshake *noise.HandshakeState
	context   *vibebridgev1.HandshakeContext
	client    *vibebridgev1.SignedDeviceDescriptor
	agent     *vibebridgev1.SignedDeviceDescriptor
	private   []byte
	state     initiatorState
}

type initiatorState uint8

const (
	initiatorReady initiatorState = iota
	initiatorAwaitingResponse
	initiatorComplete
	initiatorFailed
)

type PairingResponder struct {
	handshake *noise.HandshakeState
	context   *vibebridgev1.HandshakeContext
	client    *vibebridgev1.SignedDeviceDescriptor
	private   []byte
	state     responderState
}

type responderState uint8

const (
	responderAwaitingFinish responderState = iota
	responderComplete
	responderFailed
)

// NewPairingContext creates the exact context authenticated by both peers.
func NewPairingContext(initiatorDeviceID, responderDeviceID, invitationID, relayTicket []byte) (*vibebridgev1.HandshakeContext, error) {
	ticketHash := sha256.Sum256(relayTicket)
	context := &vibebridgev1.HandshakeContext{
		SchemaVersion: pairingContextSchemaVersion,
		ProtocolVersion: &vibebridgev1.ProtocolVersion{
			Major: protocol.CurrentMajor,
			Minor: protocol.CurrentMinor,
		},
		InitiatorDeviceId: append([]byte(nil), initiatorDeviceID...),
		ResponderDeviceId: append([]byte(nil), responderDeviceID...),
		RelayTicketHash:   ticketHash[:],
		Intent:            vibebridgev1.HandshakeIntent_HANDSHAKE_INTENT_PAIR_DEVICE,
		InvitationId:      append([]byte(nil), invitationID...),
	}
	if err := validatePairingContext(context); err != nil {
		return nil, err
	}
	return context, nil
}

func NewPairingInitiator(config PairingInitiatorConfig) (*PairingInitiator, error) {
	if err := validatePairingConfig(config.Context, config.Client, config.Agent, config.StaticPrivateKey, config.BootstrapSecret); err != nil {
		return nil, err
	}
	if config.Client.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT ||
		config.Agent.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT {
		return nil, errors.New("pairing descriptors have invalid device classes")
	}
	if !descriptorSupportsVersion(config.Client, config.Context.ProtocolVersion) ||
		!descriptorSupportsVersion(config.Agent, config.Context.ProtocolVersion) {
		return nil, errors.New("pairing descriptor does not support the handshake protocol version")
	}
	if !bytes.Equal(config.Context.InitiatorDeviceId, config.Client.DeviceDescriptor.DeviceId) ||
		!bytes.Equal(config.Context.ResponderDeviceId, config.Agent.DeviceDescriptor.DeviceId) {
		return nil, errors.New("pairing context device IDs do not match descriptors")
	}
	privateKey, err := checkedStaticKey(config.StaticPrivateKey, config.Client.DeviceDescriptor.KeyAgreementPublicKey)
	if err != nil {
		return nil, err
	}
	prologue, err := pairingPrologue(config.Context)
	if err != nil {
		zero(privateKey)
		return nil, err
	}
	psk := append([]byte(nil), config.BootstrapSecret...)
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           pairingCipherSuite,
		Random:                config.Random,
		Pattern:               noise.HandshakeXX,
		Initiator:             true,
		Prologue:              prologue,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
		StaticKeypair: noise.DHKey{
			Private: privateKey,
			Public:  append([]byte(nil), config.Client.DeviceDescriptor.KeyAgreementPublicKey...),
		},
	})
	zero(psk)
	if err != nil {
		zero(privateKey)
		return nil, fmt.Errorf("initialize pairing handshake: %w", err)
	}
	return &PairingInitiator{
		handshake: handshake,
		context:   proto.Clone(config.Context).(*vibebridgev1.HandshakeContext),
		client:    proto.Clone(config.Client).(*vibebridgev1.SignedDeviceDescriptor),
		agent:     proto.Clone(config.Agent).(*vibebridgev1.SignedDeviceDescriptor),
		private:   privateKey,
		state:     initiatorReady,
	}, nil
}

func (initiator *PairingInitiator) Start() (*vibebridgev1.PairingHandshakeStart, error) {
	if initiator == nil || initiator.state != initiatorReady || initiator.handshake == nil {
		return nil, ErrHandshakeState
	}
	payload, err := marshalDeterministic(&vibebridgev1.PairingInitiatorPayload{Client: initiator.client})
	if err != nil {
		initiator.fail()
		return nil, err
	}
	message, _, _, err := initiator.handshake.WriteMessage(nil, payload)
	if err != nil || len(message) > maxNoiseMessageBytes {
		initiator.fail()
		return nil, ErrInvalidHandshake
	}
	initiator.state = initiatorAwaitingResponse
	return &vibebridgev1.PairingHandshakeStart{
		Context:      proto.Clone(initiator.context).(*vibebridgev1.HandshakeContext),
		NoiseMessage: append([]byte(nil), message...),
	}, nil
}

func (initiator *PairingInitiator) Finish(response *vibebridgev1.PairingHandshakeResponse) (*PairingResult, *vibebridgev1.PairingHandshakeFinish, error) {
	if initiator == nil || initiator.state != initiatorAwaitingResponse || initiator.handshake == nil {
		return nil, nil, ErrHandshakeState
	}
	if response == nil || len(response.NoiseMessage) == 0 || len(response.NoiseMessage) > maxNoiseMessageBytes {
		initiator.fail()
		return nil, nil, ErrInvalidHandshake
	}
	payloadBytes, first, second, err := initiator.handshake.ReadMessage(nil, append([]byte(nil), response.NoiseMessage...))
	if err != nil || first != nil || second != nil || !bytes.Equal(initiator.handshake.PeerStatic(), initiator.agent.DeviceDescriptor.KeyAgreementPublicKey) {
		initiator.fail()
		return nil, nil, ErrInvalidHandshake
	}
	payload := new(vibebridgev1.PairingResponderPayload)
	if err := unmarshalBounded(payloadBytes, payload); err != nil || payload.Agent == nil || !proto.Equal(payload.Agent, initiator.agent) {
		initiator.fail()
		return nil, nil, ErrInvalidHandshake
	}
	if err := validatePeerDescriptor(payload.Agent, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT,
		initiator.context.ResponderDeviceId, initiator.context.ProtocolVersion); err != nil {
		initiator.fail()
		return nil, nil, ErrInvalidHandshake
	}
	finishPayload, err := marshalDeterministic(&vibebridgev1.PairingFinishPayload{InitiatorDeviceId: initiator.context.InitiatorDeviceId})
	if err != nil {
		initiator.fail()
		return nil, nil, err
	}
	message, send, receive, err := initiator.handshake.WriteMessage(nil, finishPayload)
	if err != nil || send == nil || receive == nil || len(message) > maxNoiseMessageBytes {
		initiator.fail()
		return nil, nil, ErrInvalidHandshake
	}
	result, err := pairingResult(initiator.agent, initiator.handshake.ChannelBinding(), send, receive)
	if err != nil {
		initiator.fail()
		return nil, nil, err
	}
	initiator.state = initiatorComplete
	initiator.clearHandshakeSecrets()
	return result, &vibebridgev1.PairingHandshakeFinish{NoiseMessage: append([]byte(nil), message...)}, nil
}

// AcceptPairingStart authenticates XXpsk0 message one and prepares the response.
func AcceptPairingStart(config PairingResponderConfig, start *vibebridgev1.PairingHandshakeStart) (*PairingResponder, *vibebridgev1.PairingHandshakeResponse, *vibebridgev1.SignedDeviceDescriptor, error) {
	if start == nil || start.Context == nil || len(start.NoiseMessage) == 0 || len(start.NoiseMessage) > maxNoiseMessageBytes {
		return nil, nil, nil, ErrInvalidHandshake
	}
	if config.ExpectedContext == nil || !proto.Equal(config.ExpectedContext, start.Context) {
		return nil, nil, nil, ErrInvalidHandshake
	}
	if err := validatePairingContext(config.ExpectedContext); err != nil || errDescriptor(config.Agent) != nil ||
		config.Agent.DeviceDescriptor.DeviceClass != vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT ||
		!descriptorSupportsVersion(config.Agent, config.ExpectedContext.ProtocolVersion) ||
		!bytes.Equal(config.ExpectedContext.ResponderDeviceId, config.Agent.DeviceDescriptor.DeviceId) ||
		len(config.BootstrapSecret) != pairing.BootstrapSecretBytes {
		return nil, nil, nil, ErrInvalidHandshake
	}
	privateKey, err := checkedStaticKey(config.StaticPrivateKey, config.Agent.DeviceDescriptor.KeyAgreementPublicKey)
	if err != nil {
		return nil, nil, nil, err
	}
	prologue, err := pairingPrologue(config.ExpectedContext)
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, err
	}
	psk := append([]byte(nil), config.BootstrapSecret...)
	handshake, err := noise.NewHandshakeState(noise.Config{
		CipherSuite:           pairingCipherSuite,
		Random:                config.Random,
		Pattern:               noise.HandshakeXX,
		Initiator:             false,
		Prologue:              prologue,
		PresharedKey:          psk,
		PresharedKeyPlacement: 0,
		StaticKeypair: noise.DHKey{
			Private: privateKey,
			Public:  append([]byte(nil), config.Agent.DeviceDescriptor.KeyAgreementPublicKey...),
		},
	})
	zero(psk)
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, fmt.Errorf("initialize pairing handshake: %w", err)
	}
	payloadBytes, first, second, err := handshake.ReadMessage(nil, append([]byte(nil), start.NoiseMessage...))
	if err != nil || first != nil || second != nil {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidHandshake
	}
	payload := new(vibebridgev1.PairingInitiatorPayload)
	if err := unmarshalBounded(payloadBytes, payload); err != nil || payload.Client == nil ||
		validatePeerDescriptor(payload.Client, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
			config.ExpectedContext.InitiatorDeviceId, config.ExpectedContext.ProtocolVersion) != nil {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidHandshake
	}
	responsePayload, err := marshalDeterministic(&vibebridgev1.PairingResponderPayload{Agent: config.Agent})
	if err != nil {
		zero(privateKey)
		return nil, nil, nil, err
	}
	message, first, second, err := handshake.WriteMessage(nil, responsePayload)
	if err != nil || first != nil || second != nil || len(message) > maxNoiseMessageBytes {
		zero(privateKey)
		return nil, nil, nil, ErrInvalidHandshake
	}
	client := proto.Clone(payload.Client).(*vibebridgev1.SignedDeviceDescriptor)
	responder := &PairingResponder{
		handshake: handshake,
		context:   proto.Clone(config.ExpectedContext).(*vibebridgev1.HandshakeContext),
		client:    client,
		private:   privateKey,
		state:     responderAwaitingFinish,
	}
	return responder, &vibebridgev1.PairingHandshakeResponse{NoiseMessage: append([]byte(nil), message...)},
		proto.Clone(client).(*vibebridgev1.SignedDeviceDescriptor), nil
}

func (responder *PairingResponder) Finish(finish *vibebridgev1.PairingHandshakeFinish) (*PairingResult, error) {
	if responder == nil || responder.state != responderAwaitingFinish || responder.handshake == nil {
		return nil, ErrHandshakeState
	}
	if finish == nil || len(finish.NoiseMessage) == 0 || len(finish.NoiseMessage) > maxNoiseMessageBytes {
		responder.fail()
		return nil, ErrInvalidHandshake
	}
	payloadBytes, initiatorSend, initiatorReceive, err := responder.handshake.ReadMessage(nil, append([]byte(nil), finish.NoiseMessage...))
	if err != nil || initiatorSend == nil || initiatorReceive == nil ||
		!bytes.Equal(responder.handshake.PeerStatic(), responder.client.DeviceDescriptor.KeyAgreementPublicKey) {
		responder.fail()
		return nil, ErrInvalidHandshake
	}
	payload := new(vibebridgev1.PairingFinishPayload)
	if err := unmarshalBounded(payloadBytes, payload); err != nil || !bytes.Equal(payload.InitiatorDeviceId, responder.context.InitiatorDeviceId) {
		responder.fail()
		return nil, ErrInvalidHandshake
	}
	// Split returns initiator-send then responder-send regardless of local role.
	result, err := pairingResult(responder.client, responder.handshake.ChannelBinding(), initiatorReceive, initiatorSend)
	if err != nil {
		responder.fail()
		return nil, err
	}
	responder.state = responderComplete
	responder.clearHandshakeSecrets()
	return result, nil
}

func validatePairingConfig(context *vibebridgev1.HandshakeContext, client, agent *vibebridgev1.SignedDeviceDescriptor, private, secret []byte) error {
	if err := validatePairingContext(context); err != nil {
		return err
	}
	if err := errDescriptor(client); err != nil {
		return err
	}
	if err := errDescriptor(agent); err != nil {
		return err
	}
	if len(private) != deviceidentity.KeyAgreementBytes {
		return fmt.Errorf("static private key must be %d bytes", deviceidentity.KeyAgreementBytes)
	}
	if len(secret) != pairing.BootstrapSecretBytes {
		return fmt.Errorf("bootstrap secret must be %d bytes", pairing.BootstrapSecretBytes)
	}
	return nil
}

func validatePairingContext(context *vibebridgev1.HandshakeContext) error {
	if context == nil || context.SchemaVersion != pairingContextSchemaVersion || context.ProtocolVersion == nil ||
		context.ProtocolVersion.Major != protocol.CurrentMajor || context.ProtocolVersion.Minor != protocol.CurrentMinor ||
		len(context.InitiatorDeviceId) != deviceidentity.DeviceIDBytes || len(context.ResponderDeviceId) != deviceidentity.DeviceIDBytes ||
		bytes.Equal(context.InitiatorDeviceId, context.ResponderDeviceId) || len(context.RelayTicketHash) != sha256.Size ||
		context.Intent != vibebridgev1.HandshakeIntent_HANDSHAKE_INTENT_PAIR_DEVICE || len(context.InvitationId) != pairing.InvitationIDBytes {
		return errors.New("pairing handshake context is invalid")
	}
	return nil
}

func validatePeerDescriptor(signed *vibebridgev1.SignedDeviceDescriptor, class vibebridgev1.DeviceClass, deviceID []byte, version *vibebridgev1.ProtocolVersion) error {
	if err := errDescriptor(signed); err != nil {
		return err
	}
	if signed.DeviceDescriptor.DeviceClass != class || !bytes.Equal(signed.DeviceDescriptor.DeviceId, deviceID) ||
		!descriptorSupportsVersion(signed, version) {
		return errors.New("peer descriptor does not match the handshake context")
	}
	return nil
}

func descriptorSupportsVersion(signed *vibebridgev1.SignedDeviceDescriptor, version *vibebridgev1.ProtocolVersion) bool {
	if signed == nil || signed.DeviceDescriptor == nil || signed.DeviceDescriptor.SupportedVersions == nil || version == nil {
		return false
	}
	versions := signed.DeviceDescriptor.SupportedVersions
	return versions.Minimum != nil && versions.Maximum != nil && versions.Minimum.Major == version.Major &&
		versions.Maximum.Major == version.Major && versions.Minimum.Minor <= version.Minor && version.Minor <= versions.Maximum.Minor
}

func errDescriptor(signed *vibebridgev1.SignedDeviceDescriptor) error {
	if err := deviceidentity.VerifySignedDescriptor(signed); err != nil {
		return fmt.Errorf("verify signed device descriptor: %w", err)
	}
	return nil
}

func checkedStaticKey(private, expectedPublic []byte) ([]byte, error) {
	if len(private) != deviceidentity.KeyAgreementBytes {
		return nil, fmt.Errorf("static private key must be %d bytes", deviceidentity.KeyAgreementBytes)
	}
	copyOfPrivate := append([]byte(nil), private...)
	key, err := ecdh.X25519().NewPrivateKey(copyOfPrivate)
	if err != nil || !bytes.Equal(key.PublicKey().Bytes(), expectedPublic) {
		zero(copyOfPrivate)
		return nil, errors.New("static private key does not match the signed descriptor")
	}
	return copyOfPrivate, nil
}

func pairingPrologue(context *vibebridgev1.HandshakeContext) ([]byte, error) {
	if err := validatePairingContext(context); err != nil {
		return nil, err
	}
	encoded, err := marshalDeterministic(context)
	if err != nil {
		return nil, err
	}
	prologue := make([]byte, 0, len(pairingPrologueDomain)+len(encoded))
	prologue = append(prologue, pairingPrologueDomain...)
	prologue = append(prologue, encoded...)
	return prologue, nil
}

func pairingResult(peer *vibebridgev1.SignedDeviceDescriptor, hash []byte, send, receive *noise.CipherState) (*PairingResult, error) {
	if len(hash) != 64 || send == nil || receive == nil {
		return nil, ErrInvalidHandshake
	}
	return &PairingResult{
		Peer:          proto.Clone(peer).(*vibebridgev1.SignedDeviceDescriptor),
		SAS:           pairingSAS(hash),
		HandshakeHash: append([]byte(nil), hash...),
		Transport:     newTransport(send, receive),
	}, nil
}

func pairingSAS(handshakeHash []byte) string {
	message := make([]byte, 0, len(pairingSASDomain)+len(handshakeHash))
	message = append(message, pairingSASDomain...)
	message = append(message, handshakeHash...)
	digest := sha256.Sum256(message)
	value := binary.BigEndian.Uint64(digest[:8]) % 1_000_000
	return fmt.Sprintf("%03d-%03d", value/1000, value%1000)
}

func marshalDeterministic(message proto.Message) ([]byte, error) {
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		return nil, fmt.Errorf("encode pairing handshake payload: %w", err)
	}
	if len(encoded) > maxNoiseMessageBytes {
		return nil, errors.New("pairing handshake payload is too large")
	}
	return encoded, nil
}

func unmarshalBounded(encoded []byte, message proto.Message) error {
	if len(encoded) == 0 || len(encoded) > maxNoiseMessageBytes {
		return ErrInvalidHandshake
	}
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(encoded, message); err != nil {
		return ErrInvalidHandshake
	}
	return nil
}

func (initiator *PairingInitiator) fail() {
	initiator.state = initiatorFailed
	initiator.clearHandshakeSecrets()
}

func (initiator *PairingInitiator) clearHandshakeSecrets() {
	zero(initiator.private)
	initiator.private = nil
	initiator.handshake = nil
}

func (responder *PairingResponder) fail() {
	responder.state = responderFailed
	responder.clearHandshakeSecrets()
}

func (responder *PairingResponder) clearHandshakeSecrets() {
	zero(responder.private)
	responder.private = nil
	responder.handshake = nil
}

func zero(value []byte) {
	for index := range value {
		value[index] = 0
	}
}
