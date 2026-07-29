package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
)

// dialAndReadAgentHello opens a V1 WebSocket connection, sends a client Hello,
// and returns the Agent Hello envelope the server publishes in response. The
// caller controls the credentials via headers: pass nil for the legacy-token
// path or a populated header set to exercise paired-session authorization.
func dialAndReadAgentHello(t *testing.T, testServer *httptest.Server, header http.Header) *vibebridgev1.Hello {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
	if header == nil {
		wsURL += "?token=legacy-token"
	}
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{protocolv1.WebSocketSubprotocol}
	conn, response, err := dialer.Dial(wsURL, header)
	if err != nil {
		if response != nil {
			t.Fatalf("dial failed: %v (status %d)", err, response.StatusCode)
		}
		t.Fatalf("dial failed: %v", err)
	}
	defer conn.Close()

	clientEnvelope := clientHelloForIdentityTest()
	encoded, err := proto.Marshal(clientEnvelope)
	if err != nil {
		t.Fatalf("marshal client Hello: %v", err)
	}
	if err := conn.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		t.Fatalf("write client Hello: %v", err)
	}
	_, agentBytes, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("read Agent Hello: %v", err)
	}
	agentEnvelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(agentBytes, agentEnvelope); err != nil {
		t.Fatalf("unmarshal Agent Hello: %v", err)
	}
	hello := agentEnvelope.GetHello()
	if hello == nil {
		t.Fatal("Agent envelope has no Hello payload")
	}
	return hello
}

func clientHelloForIdentityTest() *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ProtocolMinor: 0,
		ConnectionId:  bytes.Repeat([]byte{0x42}, 16),
		Sequence:      1,
		Payload: &vibebridgev1.Envelope_Hello{Hello: &vibebridgev1.Hello{
			PeerRole: vibebridgev1.PeerRole_PEER_ROLE_CLIENT,
			SupportedVersions: &vibebridgev1.ProtocolVersionRange{
				Minimum: &vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
				Maximum: &vibebridgev1.ProtocolVersion{Major: 1, Minor: 0},
			},
			Capabilities:     []string{protocolv1.CapabilityTerminalBinaryOutput, protocolv1.CapabilityTerminalSequencedIO},
			MaxEnvelopeBytes: protocolv1.MaxEnvelopeBytes,
		}},
	}
}

func TestAgentHelloAdvertisesDeviceIdentity(t *testing.T) {
	server, store := newPairedTestServer(t, false)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	hello := dialAndReadAgentHello(t, testServer, nil)

	descriptor, err := store.Descriptor()
	if err != nil {
		t.Fatalf("load Agent descriptor: %v", err)
	}
	wantDeviceID := descriptor.GetDeviceDescriptor().GetDeviceId()
	if !bytes.Equal(hello.GetDeviceId(), wantDeviceID) {
		t.Fatalf("device id = %x, want %x", hello.GetDeviceId(), wantDeviceID)
	}
	if hello.GetPublicKeyFingerprint() == "" {
		t.Fatal("public key fingerprint is empty")
	}
	if hello.GetRevocationEpoch() != store.RevocationEpoch() {
		t.Fatalf("revocation epoch = %d, want %d", hello.GetRevocationEpoch(), store.RevocationEpoch())
	}
}

func TestAgentHelloOmitsDeviceIdentityWithoutDeviceStore(t *testing.T) {
	server := New(Config{SessionToken: "legacy-token"})
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	hello := dialAndReadAgentHello(t, testServer, nil)

	if len(hello.GetDeviceId()) != 0 {
		t.Fatalf("device id = %x, want empty", hello.GetDeviceId())
	}
	if hello.GetPublicKeyFingerprint() != "" {
		t.Fatalf("public key fingerprint = %q, want empty", hello.GetPublicKeyFingerprint())
	}
	if hello.GetRevocationEpoch() != 0 {
		t.Fatalf("revocation epoch = %d, want 0", hello.GetRevocationEpoch())
	}
}

func TestAgentHelloRevocationEpochAdvancesAfterRevoke(t *testing.T) {
	server, store := newPairedTestServer(t, false)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	first := dialAndReadAgentHello(t, testServer, nil)
	initialEpoch := first.GetRevocationEpoch()

	client := newTestClient(t, "Browser", 0x21)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	if _, err := store.Revoke(client.deviceID); err != nil {
		t.Fatalf("revoke client: %v", err)
	}

	second := dialAndReadAgentHello(t, testServer, nil)
	if second.GetRevocationEpoch() <= initialEpoch {
		t.Fatalf("revocation epoch = %d, want > %d", second.GetRevocationEpoch(), initialEpoch)
	}
}
