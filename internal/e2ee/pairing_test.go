package e2ee

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/flynn/noise"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

const descriptorDomainForTest = "VibeBridge device descriptor v1\x00"

type pairingVector struct {
	Suite                     string `json:"suite"`
	ClientSigningSeed         string `json:"client_signing_seed"`
	ClientStaticPrivateKey    string `json:"client_static_private_key"`
	ClientEphemeralPrivateKey string `json:"client_ephemeral_private_key"`
	AgentSigningSeed          string `json:"agent_signing_seed"`
	AgentStaticPrivateKey     string `json:"agent_static_private_key"`
	AgentEphemeralPrivateKey  string `json:"agent_ephemeral_private_key"`
	BootstrapSecret           string `json:"bootstrap_secret"`
	RelayTicket               string `json:"relay_ticket"`
	Context                   string `json:"context"`
	ClientSignedDescriptor    string `json:"client_signed_descriptor"`
	AgentSignedDescriptor     string `json:"agent_signed_descriptor"`
	NoiseMessage1             string `json:"noise_message_1"`
	NoiseMessage2             string `json:"noise_message_2"`
	NoiseMessage3             string `json:"noise_message_3"`
	HandshakeHash             string `json:"handshake_hash"`
	SAS                       string `json:"sas"`
	TransportAssociatedData   string `json:"transport_associated_data"`
	ClientToAgentPlaintext    string `json:"client_to_agent_plaintext"`
	ClientToAgentCiphertext   string `json:"client_to_agent_ciphertext"`
	AgentToClientPlaintext    string `json:"agent_to_client_plaintext"`
	AgentToClientCiphertext   string `json:"agent_to_client_ciphertext"`
}

type vectorInputs struct {
	context         *vibebridgev1.HandshakeContext
	client          *vibebridgev1.SignedDeviceDescriptor
	agent           *vibebridgev1.SignedDeviceDescriptor
	clientStatic    []byte
	agentStatic     []byte
	clientEphemeral []byte
	agentEphemeral  []byte
	secret          []byte
	relayTicket     []byte
	clientSeed      []byte
	agentSeed       []byte
}

func TestPairingXXpsk0RoundTripAndTransport(t *testing.T) {
	inputs := testVectorInputs(t)
	initiatorResult, responderResult, _, _, _ := completePairing(t, inputs)
	defer initiatorResult.Transport.Close()
	defer responderResult.Transport.Close()

	if !bytes.Equal(initiatorResult.HandshakeHash, responderResult.HandshakeHash) || initiatorResult.SAS != responderResult.SAS {
		t.Fatal("pairing peers derived different transcript hash or SAS")
	}
	if !proto.Equal(initiatorResult.Peer, inputs.agent) || !proto.Equal(responderResult.Peer, inputs.client) {
		t.Fatal("pairing results contain the wrong peer descriptors")
	}

	ad := []byte("VibeBridge transport vector aad v1")
	clientMessage := []byte("start Codex on the home PC")
	ciphertext, err := initiatorResult.Transport.Encrypt(clientMessage, ad)
	if err != nil {
		t.Fatalf("encrypt client message: %v", err)
	}
	plaintext, err := responderResult.Transport.Decrypt(ciphertext, ad)
	if err != nil || !bytes.Equal(plaintext, clientMessage) {
		t.Fatalf("decrypt client message = %q, %v", plaintext, err)
	}
	agentMessage := []byte("Codex session started")
	ciphertext, err = responderResult.Transport.Encrypt(agentMessage, ad)
	if err != nil {
		t.Fatalf("encrypt Agent message: %v", err)
	}
	plaintext, err = initiatorResult.Transport.Decrypt(ciphertext, ad)
	if err != nil || !bytes.Equal(plaintext, agentMessage) {
		t.Fatalf("decrypt Agent message = %q, %v", plaintext, err)
	}
	if counter, _ := initiatorResult.Transport.SendCounter(); counter != 1 {
		t.Fatalf("initiator send counter = %d, want 1", counter)
	}
	if counter, _ := initiatorResult.Transport.ReceiveCounter(); counter != 1 {
		t.Fatalf("initiator receive counter = %d, want 1", counter)
	}
}

func TestPairingMatchesCrossLanguageVector(t *testing.T) {
	inputs := testVectorInputs(t)
	initiatorResult, responderResult, start, response, finish := completePairing(t, inputs)
	defer initiatorResult.Transport.Close()
	defer responderResult.Transport.Close()

	associatedData := []byte("VibeBridge transport vector aad v1")
	clientPlaintext := []byte("start Codex on the home PC")
	clientCiphertext, err := initiatorResult.Transport.Encrypt(clientPlaintext, associatedData)
	if err != nil {
		t.Fatalf("encrypt vector client message: %v", err)
	}
	if _, err := responderResult.Transport.Decrypt(clientCiphertext, associatedData); err != nil {
		t.Fatalf("decrypt vector client message: %v", err)
	}
	agentPlaintext := []byte("Codex session started")
	agentCiphertext, err := responderResult.Transport.Encrypt(agentPlaintext, associatedData)
	if err != nil {
		t.Fatalf("encrypt vector Agent message: %v", err)
	}
	if _, err := initiatorResult.Transport.Decrypt(agentCiphertext, associatedData); err != nil {
		t.Fatalf("decrypt vector Agent message: %v", err)
	}

	vector := pairingVector{
		Suite:                     "Noise_XXpsk0_25519_ChaChaPoly_BLAKE2b",
		ClientSigningSeed:         hex.EncodeToString(inputs.clientSeed),
		ClientStaticPrivateKey:    hex.EncodeToString(inputs.clientStatic),
		ClientEphemeralPrivateKey: hex.EncodeToString(inputs.clientEphemeral),
		AgentSigningSeed:          hex.EncodeToString(inputs.agentSeed),
		AgentStaticPrivateKey:     hex.EncodeToString(inputs.agentStatic),
		AgentEphemeralPrivateKey:  hex.EncodeToString(inputs.agentEphemeral),
		BootstrapSecret:           hex.EncodeToString(inputs.secret),
		RelayTicket:               hex.EncodeToString(inputs.relayTicket),
		Context:                   hexMessage(t, inputs.context),
		ClientSignedDescriptor:    hexMessage(t, inputs.client),
		AgentSignedDescriptor:     hexMessage(t, inputs.agent),
		NoiseMessage1:             hex.EncodeToString(start.NoiseMessage),
		NoiseMessage2:             hex.EncodeToString(response.NoiseMessage),
		NoiseMessage3:             hex.EncodeToString(finish.NoiseMessage),
		HandshakeHash:             hex.EncodeToString(initiatorResult.HandshakeHash),
		SAS:                       initiatorResult.SAS,
		TransportAssociatedData:   hex.EncodeToString(associatedData),
		ClientToAgentPlaintext:    hex.EncodeToString(clientPlaintext),
		ClientToAgentCiphertext:   hex.EncodeToString(clientCiphertext),
		AgentToClientPlaintext:    hex.EncodeToString(agentPlaintext),
		AgentToClientCiphertext:   hex.EncodeToString(agentCiphertext),
	}
	encoded, err := json.MarshalIndent(vector, "", "  ")
	if err != nil {
		t.Fatalf("encode pairing vector: %v", err)
	}
	encoded = append(encoded, '\n')
	path := filepath.Join("..", "..", "proto", "vibebridge", "v1", "testdata", "pairing_handshake_vector.json")
	if os.Getenv("VIBEBRIDGE_UPDATE_GOLDEN") == "1" {
		if err := os.WriteFile(path, encoded, 0o644); err != nil {
			t.Fatalf("update pairing vector: %v", err)
		}
	}
	golden, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read pairing vector: %v", err)
	}
	if !bytes.Equal(encoded, golden) {
		t.Fatalf("pairing vector differs from %s; set VIBEBRIDGE_UPDATE_GOLDEN=1 to update", path)
	}
}

func TestPairingRejectsWrongSecretAlteredContextAndMalformedFrames(t *testing.T) {
	t.Run("wrong bootstrap secret", func(t *testing.T) {
		inputs := testVectorInputs(t)
		wrong := append([]byte(nil), inputs.secret...)
		wrong[0] ^= 0xff
		initiator := newTestInitiator(t, inputs, wrong)
		start, err := initiator.Start()
		if err != nil {
			t.Fatalf("start malicious handshake: %v", err)
		}
		if _, _, _, err := acceptTestStart(inputs, start); !errors.Is(err, ErrInvalidHandshake) {
			t.Fatalf("wrong secret error = %v, want ErrInvalidHandshake", err)
		}
	})

	t.Run("altered context", func(t *testing.T) {
		inputs := testVectorInputs(t)
		initiator := newTestInitiator(t, inputs, inputs.secret)
		start, err := initiator.Start()
		if err != nil {
			t.Fatalf("start handshake: %v", err)
		}
		start.Context.RelayTicketHash[0] ^= 0x01
		if _, _, _, err := acceptTestStart(inputs, start); !errors.Is(err, ErrInvalidHandshake) {
			t.Fatalf("altered context error = %v, want ErrInvalidHandshake", err)
		}
	})

	t.Run("truncated and oversized", func(t *testing.T) {
		inputs := testVectorInputs(t)
		initiator := newTestInitiator(t, inputs, inputs.secret)
		start, err := initiator.Start()
		if err != nil {
			t.Fatalf("start handshake: %v", err)
		}
		start.NoiseMessage = start.NoiseMessage[:8]
		if _, _, _, err := acceptTestStart(inputs, start); !errors.Is(err, ErrInvalidHandshake) {
			t.Fatalf("truncated frame error = %v, want ErrInvalidHandshake", err)
		}
		start.NoiseMessage = make([]byte, maxNoiseMessageBytes+1)
		if _, _, _, err := acceptTestStart(inputs, start); !errors.Is(err, ErrInvalidHandshake) {
			t.Fatalf("oversized frame error = %v, want ErrInvalidHandshake", err)
		}
	})
}

func TestPairingRejectsUnexpectedOrderDuplicateAndDescriptorMismatch(t *testing.T) {
	inputs := testVectorInputs(t)
	initiator := newTestInitiator(t, inputs, inputs.secret)
	if _, _, err := initiator.Finish(&vibebridgev1.PairingHandshakeResponse{NoiseMessage: []byte{1}}); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("finish before start error = %v, want ErrHandshakeState", err)
	}
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("start handshake: %v", err)
	}
	if _, err := initiator.Start(); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("duplicate start error = %v, want ErrHandshakeState", err)
	}
	responder, response, _, err := acceptTestStart(inputs, start)
	if err != nil {
		t.Fatalf("accept start: %v", err)
	}
	_, finish, err := initiator.Finish(response)
	if err != nil {
		t.Fatalf("finish initiator: %v", err)
	}
	if _, err := responder.Finish(finish); err != nil {
		t.Fatalf("finish responder: %v", err)
	}
	if _, err := responder.Finish(finish); !errors.Is(err, ErrHandshakeState) {
		t.Fatalf("duplicate finish error = %v, want ErrHandshakeState", err)
	}

	mutated := proto.Clone(inputs.client).(*vibebridgev1.SignedDeviceDescriptor)
	mutated.DeviceDescriptor.DisplayName = "Attacker"
	_, err = NewPairingInitiator(PairingInitiatorConfig{
		Context: inputs.context, Client: mutated, Agent: inputs.agent,
		StaticPrivateKey: inputs.clientStatic, BootstrapSecret: inputs.secret,
	})
	if err == nil {
		t.Fatal("mutated signed descriptor was accepted")
	}
	wrongStatic := append([]byte(nil), inputs.clientStatic...)
	wrongStatic[0] ^= 0xff
	_, err = NewPairingInitiator(PairingInitiatorConfig{
		Context: inputs.context, Client: inputs.client, Agent: inputs.agent,
		StaticPrivateKey: wrongStatic, BootstrapSecret: inputs.secret,
	})
	if err == nil {
		t.Fatal("mismatched static private key was accepted")
	}

	incompatible := proto.Clone(inputs.client).(*vibebridgev1.SignedDeviceDescriptor)
	incompatible.DeviceDescriptor.SupportedVersions.Minimum.Major = 2
	incompatible.DeviceDescriptor.SupportedVersions.Maximum.Major = 2
	resignDescriptor(t, incompatible, inputs.clientSeed)
	_, err = NewPairingInitiator(PairingInitiatorConfig{
		Context: inputs.context, Client: incompatible, Agent: inputs.agent,
		StaticPrivateKey: inputs.clientStatic, BootstrapSecret: inputs.secret,
	})
	if err == nil {
		t.Fatal("descriptor excluding the transcript protocol version was accepted")
	}
}

func TestResponderRejectsNoiseStaticThatDiffersFromSignedClient(t *testing.T) {
	inputs := testVectorInputs(t)
	prologue, err := pairingPrologue(inputs.context)
	if err != nil {
		t.Fatalf("create prologue: %v", err)
	}
	wrongStatic := sequence(0xe0, 32)
	wrongPublic := publicKey(t, wrongStatic)
	malicious, err := noise.NewHandshakeState(noise.Config{
		CipherSuite: pairingCipherSuite, Random: bytes.NewReader(inputs.clientEphemeral), Pattern: noise.HandshakeXX,
		Initiator: true, Prologue: prologue, PresharedKey: inputs.secret, PresharedKeyPlacement: 0,
		StaticKeypair: noise.DHKey{Private: wrongStatic, Public: wrongPublic},
	})
	if err != nil {
		t.Fatalf("initialize malicious handshake: %v", err)
	}
	payload, err := proto.MarshalOptions{Deterministic: true}.Marshal(&vibebridgev1.PairingInitiatorPayload{Client: inputs.client})
	if err != nil {
		t.Fatalf("encode malicious payload: %v", err)
	}
	message1, _, _, err := malicious.WriteMessage(nil, payload)
	if err != nil {
		t.Fatalf("write malicious message one: %v", err)
	}
	start := &vibebridgev1.PairingHandshakeStart{Context: inputs.context, NoiseMessage: message1}
	responder, response, _, err := acceptTestStart(inputs, start)
	if err != nil {
		t.Fatalf("accept malicious start before static is revealed: %v", err)
	}
	if _, _, _, err := malicious.ReadMessage(nil, response.NoiseMessage); err != nil {
		t.Fatalf("read responder message: %v", err)
	}
	finishPayload, _ := proto.MarshalOptions{Deterministic: true}.Marshal(&vibebridgev1.PairingFinishPayload{InitiatorDeviceId: inputs.context.InitiatorDeviceId})
	message3, _, _, err := malicious.WriteMessage(nil, finishPayload)
	if err != nil {
		t.Fatalf("write malicious message three: %v", err)
	}
	if _, err := responder.Finish(&vibebridgev1.PairingHandshakeFinish{NoiseMessage: message3}); !errors.Is(err, ErrInvalidHandshake) {
		t.Fatalf("mismatched Noise static error = %v, want ErrInvalidHandshake", err)
	}
}

func TestTransportRejectsReplayAndAssociatedDataMismatchFailClosed(t *testing.T) {
	inputs := testVectorInputs(t)
	initiatorResult, responderResult, _, _, _ := completePairing(t, inputs)
	defer initiatorResult.Transport.Close()
	defer responderResult.Transport.Close()

	ciphertext, err := initiatorResult.Transport.Encrypt([]byte("one"), []byte("correct"))
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if _, err := responderResult.Transport.Decrypt(ciphertext, []byte("wrong")); err == nil {
		t.Fatal("associated-data mismatch was accepted")
	}
	if _, err := responderResult.Transport.Decrypt(ciphertext, []byte("correct")); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("retry after authentication failure = %v, want ErrTransportClosed", err)
	}
	if _, err := responderResult.Transport.Encrypt([]byte("reply"), nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("send after authentication failure = %v, want ErrTransportClosed", err)
	}

	inputs = testVectorInputs(t)
	initiatorResult, responderResult, _, _, _ = completePairing(t, inputs)
	defer initiatorResult.Transport.Close()
	defer responderResult.Transport.Close()
	if _, err := responderResult.Transport.Decrypt(make([]byte, 15), nil); err == nil {
		t.Fatal("malformed transport frame was accepted")
	}
	if _, err := responderResult.Transport.Encrypt([]byte("reply"), nil); !errors.Is(err, ErrTransportClosed) {
		t.Fatalf("send after malformed frame = %v, want ErrTransportClosed", err)
	}

	inputs = testVectorInputs(t)
	initiatorResult, responderResult, _, _, _ = completePairing(t, inputs)
	defer initiatorResult.Transport.Close()
	defer responderResult.Transport.Close()
	ciphertext, _ = initiatorResult.Transport.Encrypt([]byte("one"), nil)
	if _, err := responderResult.Transport.Decrypt(ciphertext, nil); err != nil {
		t.Fatalf("first decrypt: %v", err)
	}
	if _, err := responderResult.Transport.Decrypt(ciphertext, nil); err == nil {
		t.Fatal("ciphertext replay was accepted")
	}
}

func completePairing(t *testing.T, inputs vectorInputs) (*PairingResult, *PairingResult, *vibebridgev1.PairingHandshakeStart, *vibebridgev1.PairingHandshakeResponse, *vibebridgev1.PairingHandshakeFinish) {
	t.Helper()
	initiator := newTestInitiator(t, inputs, inputs.secret)
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("start pairing: %v", err)
	}
	responder, response, pending, err := acceptTestStart(inputs, start)
	if err != nil {
		t.Fatalf("accept pairing start: %v", err)
	}
	if !proto.Equal(pending, inputs.client) {
		t.Fatal("pending descriptor differs from signed client descriptor")
	}
	initiatorResult, finish, err := initiator.Finish(response)
	if err != nil {
		t.Fatalf("finish pairing initiator: %v", err)
	}
	responderResult, err := responder.Finish(finish)
	if err != nil {
		t.Fatalf("finish pairing responder: %v", err)
	}
	return initiatorResult, responderResult, start, response, finish
}

func newTestInitiator(t *testing.T, inputs vectorInputs, secret []byte) *PairingInitiator {
	t.Helper()
	initiator, err := NewPairingInitiator(PairingInitiatorConfig{
		Context: inputs.context, Client: inputs.client, Agent: inputs.agent,
		StaticPrivateKey: inputs.clientStatic, BootstrapSecret: secret,
		Random: bytes.NewReader(inputs.clientEphemeral),
	})
	if err != nil {
		t.Fatalf("create initiator: %v", err)
	}
	return initiator
}

func acceptTestStart(inputs vectorInputs, start *vibebridgev1.PairingHandshakeStart) (*PairingResponder, *vibebridgev1.PairingHandshakeResponse, *vibebridgev1.SignedDeviceDescriptor, error) {
	return AcceptPairingStart(PairingResponderConfig{
		ExpectedContext: inputs.context, Agent: inputs.agent, StaticPrivateKey: inputs.agentStatic,
		BootstrapSecret: inputs.secret, Random: bytes.NewReader(inputs.agentEphemeral),
	}, start)
}

func testVectorInputs(t *testing.T) vectorInputs {
	t.Helper()
	clientSeed := sequence(0x20, 32)
	agentSeed := sequence(0x00, 32)
	clientStatic := sequence(0x40, 32)
	agentStatic := sequence(0x60, 32)
	clientID := sequence(0x10, 16)
	agentID := sequence(0x00, 16)
	client := signedDescriptor(t, clientID, "My Phone", "web", vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT, clientSeed, clientStatic, time.Date(2026, 7, 15, 9, 0, 0, 123_000_000, time.UTC))
	agent := signedDescriptor(t, agentID, "Home PC", "windows", vibebridgev1.DeviceClass_DEVICE_CLASS_AGENT, agentSeed, agentStatic, time.Date(2026, 7, 15, 8, 0, 0, 123_000_000, time.UTC))
	relayTicket := []byte("relay-ticket-vector-v1")
	context, err := NewPairingContext(clientID, agentID, sequence(0xc0, 16), relayTicket)
	if err != nil {
		t.Fatalf("create vector context: %v", err)
	}
	return vectorInputs{
		context: context, client: client, agent: agent, clientStatic: clientStatic, agentStatic: agentStatic,
		clientEphemeral: sequence(0x80, 32), agentEphemeral: sequence(0xa0, 32), secret: sequence(0xd0, 32),
		relayTicket: relayTicket, clientSeed: clientSeed, agentSeed: agentSeed,
	}
}

func signedDescriptor(t *testing.T, deviceID []byte, name, platform string, class vibebridgev1.DeviceClass, signingSeed, staticPrivate []byte, createdAt time.Time) *vibebridgev1.SignedDeviceDescriptor {
	t.Helper()
	signingKey := ed25519.NewKeyFromSeed(signingSeed)
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId: deviceID, DisplayName: name, Platform: platform, DeviceClass: class,
		SigningPublicKey: signingKey.Public().(ed25519.PublicKey), KeyAgreementPublicKey: publicKey(t, staticPrivate),
		CreatedAt: timestamppb.New(createdAt), KeyVersion: 1,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: 1}, Maximum: &vibebridgev1.ProtocolVersion{Major: 1},
		},
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	message := append([]byte(descriptorDomainForTest), encoded...)
	return &vibebridgev1.SignedDeviceDescriptor{DeviceDescriptor: descriptor, Signature: ed25519.Sign(signingKey, message)}
}

func resignDescriptor(t *testing.T, signed *vibebridgev1.SignedDeviceDescriptor, signingSeed []byte) {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(signed.DeviceDescriptor)
	if err != nil {
		t.Fatalf("encode descriptor for signing: %v", err)
	}
	message := append([]byte(descriptorDomainForTest), encoded...)
	signed.Signature = ed25519.Sign(ed25519.NewKeyFromSeed(signingSeed), message)
}

func publicKey(t *testing.T, private []byte) []byte {
	t.Helper()
	key, err := ecdh.X25519().NewPrivateKey(private)
	if err != nil {
		t.Fatalf("parse X25519 private key: %v", err)
	}
	return key.PublicKey().Bytes()
}

func hexMessage(t *testing.T, message proto.Message) string {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatalf("encode fixture message: %v", err)
	}
	return hex.EncodeToString(encoded)
}

func sequence(start byte, length int) []byte {
	value := make([]byte, length)
	for index := range value {
		value[index] = start + byte(index)
	}
	return value
}
