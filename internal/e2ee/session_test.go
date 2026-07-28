package e2ee

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// sessionTestDescriptorDomain mirrors deviceidentity.descriptorSignatureDomain.
// It is duplicated here so the e2ee test can build valid signed descriptors
// without taking a dependency on the deviceidentity internal API.
const sessionTestDescriptorDomain = "VibeBridge device descriptor v1\x00"

type testIdentity struct {
	descriptor   *vibebridgev1.SignedDeviceDescriptor
	signingKey   ed25519.PrivateKey
	staticPublic []byte
	staticPriv   []byte
}

func newTestIdentity(t *testing.T, class vibebridgev1.DeviceClass, keyVersion uint32) *testIdentity {
	t.Helper()
	if keyVersion == 0 {
		keyVersion = 1
	}
	signingPublic, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	staticPrivate, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate static key: %v", err)
	}
	deviceID := make([]byte, deviceidentity.DeviceIDBytes)
	if _, err := rand.Read(deviceID); err != nil {
		t.Fatalf("generate device id: %v", err)
	}
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId:              deviceID,
		DisplayName:           "Test Device",
		Platform:              "test",
		DeviceClass:           class,
		SigningPublicKey:      signingPublic,
		KeyAgreementPublicKey: staticPrivate.PublicKey().Bytes(),
		CreatedAt:             timestamppb.New(time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)),
		KeyVersion:            keyVersion,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: protocol.CurrentMajor, Minor: protocol.CurrentMinor},
			Maximum: &vibebridgev1.ProtocolVersion{Major: protocol.CurrentMajor, Minor: protocol.CurrentMinor},
		},
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	message := append([]byte(nil), sessionTestDescriptorDomain...)
	message = append(message, encoded...)
	signature := ed25519.Sign(signingKey, message)
	signed := &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: signature}
	if err := deviceidentity.VerifySignedDescriptor(signed); err != nil {
		t.Fatalf("self-verify signed descriptor: %v", err)
	}
	return &testIdentity{
		descriptor:   signed,
		signingKey:   signingKey,
		staticPublic: staticPrivate.PublicKey().Bytes(),
		staticPriv:   staticPrivate.Bytes(),
	}
}

// rebuildDescriptor re-signs identity.descriptor with the supplied signing
// key. Used by tests that want to swap in a new key agreement key, change
// the key version, or otherwise mutate the descriptor while keeping the
// signature valid for the new bytes.
func rebuildDescriptor(t *testing.T, descriptor *vibebridgev1.DeviceDescriptor, signingKey ed25519.PrivateKey) *vibebridgev1.SignedDeviceDescriptor {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	message := append([]byte(nil), sessionTestDescriptorDomain...)
	message = append(message, encoded...)
	signature := ed25519.Sign(signingKey, message)
	signed := &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: signature}
	if err := deviceidentity.VerifySignedDescriptor(signed); err != nil {
		t.Fatalf("re-verify signed descriptor: %v", err)
	}
	return signed
}

type testAuthorizer struct {
	devices map[string]*vibebridgev1.AuthorizedDevice
	epoch   uint64
}

func newTestAuthorizer() *testAuthorizer {
	return &testAuthorizer{devices: make(map[string]*vibebridgev1.AuthorizedDevice)}
}

func (a *testAuthorizer) AuthorizedDevice(deviceID []byte) (*vibebridgev1.AuthorizedDevice, error) {
	view, ok := a.devices[string(deviceID)]
	if !ok {
		return nil, deviceidentity.ErrDeviceNotFound
	}
	return view, nil
}

func (a *testAuthorizer) RevocationEpoch() uint64 {
	return a.epoch
}

func (a *testAuthorizer) add(view *vibebridgev1.AuthorizedDevice) {
	a.devices[string(view.Device.DeviceDescriptor.DeviceId)] = view
}

func authorizedView(t *testing.T, identity *testIdentity, version uint64, state vibebridgev1.DeviceAuthorizationState, epoch uint64) *vibebridgev1.AuthorizedDevice {
	t.Helper()
	now := time.Date(2026, 7, 15, 0, 0, 0, 0, time.UTC)
	view := &vibebridgev1.AuthorizedDevice{
		Device:               identity.descriptor,
		AuthorizationVersion: version,
		State:                state,
		AuthorizedAt:         timestamppb.New(now),
		RevocationEpoch:      epoch,
	}
	if state == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED {
		view.RevokedAt = timestamppb.New(now)
	}
	return view
}

func newSessionInitiatorFor(t *testing.T, client *testIdentity, agent *testIdentity, knownEpoch uint64) *SessionInitiator {
	t.Helper()
	context, err := NewSessionContext(client.descriptor.DeviceDescriptor.DeviceId, agent.descriptor.DeviceDescriptor.DeviceId, nil)
	if err != nil {
		t.Fatalf("build session context: %v", err)
	}
	initiator, err := NewSessionInitiator(SessionInitiatorConfig{
		Context:          context,
		Client:           client.descriptor,
		Agent:            agent.descriptor,
		StaticPrivateKey: client.staticPriv,
		KnownEpoch:       knownEpoch,
		Capabilities:     []string{"terminal.connect", "terminal.resize"},
		Random:           rand.Reader,
	})
	if err != nil {
		t.Fatalf("build session initiator: %v", err)
	}
	return initiator
}

func newSessionResponderConfig(agent *testIdentity, authorizer Authorizer) SessionResponderConfig {
	return SessionResponderConfig{
		Authorizer:      authorizer,
		Agent:           agent.descriptor,
		StaticPrivateKey: agent.staticPriv,
		Random:          rand.Reader,
	}
}

func completeSession(t *testing.T, client *testIdentity, agent *testIdentity, authorizer Authorizer) (*SessionResult, *SessionView) {
	t.Helper()
	initiator := newSessionInitiatorFor(t, client, agent, 0)
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	responder, response, peer, err := AcceptSessionStart(newSessionResponderConfig(agent, authorizer), start)
	if err != nil {
		t.Fatalf("session accept: %v", err)
	}
	defer responder.Close()
	if !proto.Equal(peer, client.descriptor) {
		t.Fatalf("responder peer mismatch")
	}
	view, err := responder.View()
	if err != nil {
		t.Fatalf("session view: %v", err)
	}
	result, err := initiator.Finish(response)
	if err != nil {
		t.Fatalf("session finish: %v", err)
	}
	if len(result.HandshakeHash) != 64 {
		t.Fatalf("session handshake hash must be 64 bytes, got %d", len(result.HandshakeHash))
	}
	if !bytes.Equal(result.HandshakeHash, view.HandshakeHash) {
		t.Fatalf("session handshake hash mismatch between initiator and responder")
	}
	if !bytes.Equal(result.Peer.DeviceDescriptor.DeviceId, agent.descriptor.DeviceDescriptor.DeviceId) {
		t.Fatalf("initiator peer mismatch")
	}
	return result, view
}

func runSessionStart(t *testing.T, client *testIdentity, agent *testIdentity, authorizer Authorizer) error {
	t.Helper()
	initiator := newSessionInitiatorFor(t, client, agent, 0)
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	_, _, _, err = AcceptSessionStart(newSessionResponderConfig(agent, authorizer), start)
	return err
}

func TestSessionIKRoundTripAndTransport(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, client, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 3))
	authorizer.epoch = 3

	result, view := completeSession(t, client, agent, authorizer)
	defer result.Transport.Close()
	defer view.Transport.Close()

	if result.RevocationEpoch != 3 {
		t.Fatalf("expected responder to report current epoch 3, got %d", result.RevocationEpoch)
	}
	if view.AuthorizationVersion != 7 {
		t.Fatalf("expected authorization version 7, got %d", view.AuthorizationVersion)
	}
	if view.RevocationEpoch != 3 {
		t.Fatalf("expected revocation epoch 3, got %d", view.RevocationEpoch)
	}
	if len(result.Capabilities) != 2 || result.Capabilities[0] != "terminal.connect" {
		t.Fatalf("expected capabilities forwarded to initiator, got %v", result.Capabilities)
	}
	if len(view.Capabilities) != 2 || view.Capabilities[0] != "terminal.connect" {
		t.Fatalf("expected capabilities captured on responder, got %v", view.Capabilities)
	}

	ad := []byte("VibeBridge transport vector aad v1")
	clientMessage := []byte("start Codex on the home PC")
	clientCipher, err := result.Transport.Encrypt(clientMessage, ad)
	if err != nil {
		t.Fatalf("encrypt client message: %v", err)
	}
	agentPlain, err := view.Transport.Decrypt(clientCipher, ad)
	if err != nil {
		t.Fatalf("decrypt client message on agent: %v", err)
	}
	if !bytes.Equal(agentPlain, clientMessage) {
		t.Fatalf("agent decrypted %q, want %q", agentPlain, clientMessage)
	}
	agentCipher, err := view.Transport.Encrypt(clientMessage, ad)
	if err != nil {
		t.Fatalf("encrypt agent message: %v", err)
	}
	clientPlain, err := result.Transport.Decrypt(agentCipher, ad)
	if err != nil {
		t.Fatalf("decrypt agent message on client: %v", err)
	}
	if !bytes.Equal(clientPlain, clientMessage) {
		t.Fatalf("client decrypted %q, want %q", clientPlain, clientMessage)
	}
}

func TestSessionRejectsUnknownDevice(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	if err := runSessionStart(t, client, agent, authorizer); !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for unknown device, got %v", err)
	}
}

func TestSessionRejectsRevokedDevice(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, client, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED, 3))
	if err := runSessionStart(t, client, agent, authorizer); !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for revoked device, got %v", err)
	}
}

func TestSessionRejectsKeyVersionRegression(t *testing.T) {
	original := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 2)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, original, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 0))

	// Same id, same signing key, but a downgraded KeyVersion. Re-sign so the
	// signature still validates.
	downgraded := proto.Clone(original.descriptor.DeviceDescriptor).(*vibebridgev1.DeviceDescriptor)
	downgraded.KeyVersion = 1
	signed := rebuildDescriptor(t, downgraded, original.signingKey)
	client := *original
	client.descriptor = signed

	if err := runSessionStart(t, &client, agent, authorizer); !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for key version regression, got %v", err)
	}
}

func TestSessionRejectsKeyAgreementMismatch(t *testing.T) {
	original := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, original, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 0))

	// Same id, same signing key, but a different X25519 static key. The wire
	// Noise_PeerStatic binding diverges from what the store recorded.
	spoofed := proto.Clone(original.descriptor.DeviceDescriptor).(*vibebridgev1.DeviceDescriptor)
	freshStatic, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("regenerate static key: %v", err)
	}
	spoofed.KeyAgreementPublicKey = freshStatic.PublicKey().Bytes()
	signed := rebuildDescriptor(t, spoofed, original.signingKey)
	spoofedIdentity := *original
	spoofedIdentity.descriptor = signed
	spoofedIdentity.staticPublic = freshStatic.PublicKey().Bytes()
	spoofedIdentity.staticPriv = freshStatic.Bytes()

	if err := runSessionStart(t, &spoofedIdentity, agent, authorizer); !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for key-agreement mismatch, got %v", err)
	}
}

func TestSessionRejectsSigningKeyChange(t *testing.T) {
	original := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, original, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 0))

	// Same id, same static key, but a fresh signing key that signs a new
	// descriptor. The on-wire signing public key now diverges from the
	// stored one, so the post-handshake signature check must reject.
	rebound := proto.Clone(original.descriptor.DeviceDescriptor).(*vibebridgev1.DeviceDescriptor)
	freshVerify, freshSigning, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("regenerate signing key: %v", err)
	}
	rebound.SigningPublicKey = freshVerify
	signed := rebuildDescriptor(t, rebound, freshSigning)
	reboundIdentity := *original
	reboundIdentity.descriptor = signed
	reboundIdentity.signingKey = freshSigning

	if err := runSessionStart(t, &reboundIdentity, agent, authorizer); !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for signing key change, got %v", err)
	}
}

func TestSessionRejectsFutureEpoch(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, client, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 5))
	authorizer.epoch = 5

	initiator := newSessionInitiatorFor(t, client, agent, 99)
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	_, _, _, err = AcceptSessionStart(newSessionResponderConfig(agent, authorizer), start)
	if !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for future epoch, got %v", err)
	}
}

func TestSessionRespondsWithCurrentEpochWhenClientIsBehind(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, client, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 2))
	authorizer.epoch = 9

	result, view := completeSession(t, client, agent, authorizer)
	defer result.Transport.Close()
	defer view.Transport.Close()
	if result.RevocationEpoch != 9 {
		t.Fatalf("expected initiator to learn current epoch 9, got %d", result.RevocationEpoch)
	}
}

func TestSessionRejectsPairingContext(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)

	// Pairing context carries a non-empty invitation_id; session context
	// requires the field to be empty. NewSessionInitiator must reject it.
	invitationID := make([]byte, 16)
	if _, err := rand.Read(invitationID); err != nil {
		t.Fatalf("generate invitation id: %v", err)
	}
	pairingTicket := make([]byte, 16)
	if _, err := rand.Read(pairingTicket); err != nil {
		t.Fatalf("generate pairing ticket: %v", err)
	}
	pairingContext, err := NewPairingContext(client.descriptor.DeviceDescriptor.DeviceId, agent.descriptor.DeviceDescriptor.DeviceId, invitationID, pairingTicket)
	if err != nil {
		t.Fatalf("build pairing context: %v", err)
	}
	_, err = NewSessionInitiator(SessionInitiatorConfig{
		Context:          pairingContext,
		Client:           client.descriptor,
		Agent:            agent.descriptor,
		StaticPrivateKey: client.staticPriv,
		Random:           rand.Reader,
	})
	if err == nil {
		t.Fatal("expected NewSessionInitiator to reject a pairing-intent context")
	}
}

func TestSessionRejectsSelfHandshake(t *testing.T) {
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	context, err := NewSessionContext(agent.descriptor.DeviceDescriptor.DeviceId, agent.descriptor.DeviceDescriptor.DeviceId, nil)
	if err == nil {
		t.Fatal("expected NewSessionContext to reject self-handshake")
	}
	if context != nil {
		t.Fatalf("expected nil context, got %v", context)
	}
}

func TestSessionRejectsCrossAgentResponse(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	otherAgent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, client, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 0))

	initiator := newSessionInitiatorFor(t, client, agent, 0)
	if _, err := initiator.Start(); err != nil {
		t.Fatalf("initiator start: %v", err)
	}
	// Build a parallel handshake for the other agent and feed its response
	// into the original initiator. The PeerStatic binding diverges so the
	// initiator must reject the foreign response.
	otherContext, err := NewSessionContext(client.descriptor.DeviceDescriptor.DeviceId, otherAgent.descriptor.DeviceDescriptor.DeviceId, nil)
	if err != nil {
		t.Fatalf("build other context: %v", err)
	}
	otherInitiator, err := NewSessionInitiator(SessionInitiatorConfig{
		Context:          otherContext,
		Client:           client.descriptor,
		Agent:            otherAgent.descriptor,
		StaticPrivateKey: client.staticPriv,
		Random:           rand.Reader,
	})
	if err != nil {
		t.Fatalf("build other initiator: %v", err)
	}
	otherStart, err := otherInitiator.Start()
	if err != nil {
		t.Fatalf("other start: %v", err)
	}
	_, otherResponse, _, err := AcceptSessionStart(newSessionResponderConfig(otherAgent, authorizer), otherStart)
	if err != nil {
		t.Fatalf("other accept: %v", err)
	}
	_, err = initiator.Finish(otherResponse)
	if !errors.Is(err, ErrInvalidSessionHandshake) {
		t.Fatalf("expected ErrInvalidSessionHandshake for cross-agent response, got %v", err)
	}
}

func TestSessionAcceptSessionStartRejectsNilAuthorizer(t *testing.T) {
	client := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	initiator := newSessionInitiatorFor(t, client, agent, 0)
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("session start: %v", err)
	}
	_, _, _, err = AcceptSessionStart(SessionResponderConfig{
		Authorizer:      nil,
		Agent:           agent.descriptor,
		StaticPrivateKey: agent.staticPriv,
		Random:          rand.Reader,
	}, start)
	if !errors.Is(err, ErrSessionAuthorization) {
		t.Fatalf("expected ErrSessionAuthorization for nil authorizer, got %v", err)
	}
}

func TestSessionAcceptsKeyVersionUpgradeOnWire(t *testing.T) {
	original := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, 1)
	agent := newTestIdentity(t, vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, 1)
	authorizer := newTestAuthorizer()
	authorizer.add(authorizedView(t, original, 7, vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_AUTHORIZED, 0))

	// Client presents a higher KeyVersion than the stored record. The store
	// is the source of truth for the device's key history; the on-wire
	// KeyVersion is informational. The handshake must succeed.
	upgraded := proto.Clone(original.descriptor.DeviceDescriptor).(*vibebridgev1.DeviceDescriptor)
	upgraded.KeyVersion = 5
	signed := rebuildDescriptor(t, upgraded, original.signingKey)
	client := *original
	client.descriptor = signed

	result, view := completeSession(t, &client, agent, authorizer)
	defer result.Transport.Close()
	defer view.Transport.Close()
}
