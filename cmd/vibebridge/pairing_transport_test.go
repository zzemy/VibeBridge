package main

import (
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/e2ee"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"github.com/zzemy/VibeBridge/internal/pairingflow"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPairingWebSocketCompletesEncryptedLocalApproval(t *testing.T) {
	handler, invitations, flows, identity := testPairingTransportHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()
	invitation, err := invitations.Create([]string{server.URL + pairingTransportPath})
	if err != nil {
		t.Fatalf("create invitation: %v", err)
	}
	phone, phonePrivate := testTransportPhone(t)
	contextMessage, err := e2ee.NewPairingContext(
		phone.DeviceDescriptor.DeviceId,
		invitation.Agent.DeviceDescriptor.DeviceId,
		invitation.InvitationId,
		nil,
	)
	if err != nil {
		t.Fatalf("create pairing context: %v", err)
	}
	initiator, err := e2ee.NewPairingInitiator(e2ee.PairingInitiatorConfig{
		Context:          contextMessage,
		Client:           phone,
		Agent:            invitation.Agent,
		StaticPrivateKey: phonePrivate,
		BootstrapSecret:  invitation.BootstrapSecret,
	})
	if err != nil {
		t.Fatalf("create phone handshake: %v", err)
	}
	start, err := initiator.Start()
	if err != nil {
		t.Fatalf("start phone handshake: %v", err)
	}

	connection := dialPairingTransport(t, server.URL, server.URL)
	defer connection.Close()
	writeProtoFrame(t, connection, start)
	response := new(vibebridgev1.PairingHandshakeResponse)
	readProtoFrame(t, connection, response)
	phoneResult, finish, err := initiator.Finish(response)
	if err != nil {
		t.Fatalf("finish phone handshake: %v", err)
	}
	defer phoneResult.Transport.Close()
	writeProtoFrame(t, connection, finish)

	pendingCiphertext := readBinaryFrame(t, connection)
	pending := decryptTransportApproval(t, phoneResult.Transport, pendingCiphertext)
	if pending.Status != vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_PENDING {
		t.Fatalf("first encrypted status = %s, want pending", pending.Status)
	}
	current, ok := flows.Current()
	if !ok || current.SAS != phoneResult.SAS || current.DisplayName != "My Phone" {
		t.Fatalf("local pending flow = %#v/%t, phone SAS %q", current, ok, phoneResult.SAS)
	}
	statusResponse, err := http.Get(server.URL + localPairingStatusPath + "?token=local-token")
	if err != nil {
		t.Fatalf("query local pairing status: %v", err)
	}
	defer statusResponse.Body.Close()
	var localStatus localPairingStatus
	if err := json.NewDecoder(statusResponse.Body).Decode(&localStatus); err != nil {
		t.Fatalf("decode local pairing status: %v", err)
	}
	if statusResponse.StatusCode != http.StatusOK || localStatus.FlowID != current.FlowID || localStatus.SAS != phoneResult.SAS {
		t.Fatalf("local pairing status = %d/%#v", statusResponse.StatusCode, localStatus)
	}
	pageResponse, err := http.Get(server.URL + localPairingPath + "?token=local-token")
	if err != nil {
		t.Fatalf("open local pairing page: %v", err)
	}
	page, _ := io.ReadAll(pageResponse.Body)
	pageResponse.Body.Close()
	if !strings.Contains(string(page), "Approve My Phone") || !strings.Contains(string(page), phoneResult.SAS) {
		t.Fatalf("local approval page did not show authenticated peer and SAS: %s", page)
	}
	form := url.Values{"flow_id": []string{current.FlowID}}
	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	approvalResponse, err := client.PostForm(server.URL+localPairingApprovePath+"?token=local-token", form)
	if err != nil {
		t.Fatalf("post local approval: %v", err)
	}
	approvalResponse.Body.Close()
	if approvalResponse.StatusCode != http.StatusSeeOther {
		t.Fatalf("local approval HTTP status = %d, want 303", approvalResponse.StatusCode)
	}
	approvedCiphertext := readBinaryFrame(t, connection)
	approved := decryptTransportApproval(t, phoneResult.Transport, approvedCiphertext)
	if approved.Status != vibebridgev1.PairingApprovalStatus_PAIRING_APPROVAL_STATUS_APPROVED || approved.AuthorizationVersion == 0 {
		t.Fatalf("final encrypted status = %v", approved)
	}
	record, err := identity.AuthorizedDevice(phone.DeviceDescriptor.DeviceId)
	if err != nil || record.AuthorizationVersion != approved.AuthorizationVersion {
		t.Fatalf("authorized phone = %v/%v", record, err)
	}
}

func TestPairingWebSocketRequiresSameOriginSubprotocolAndBinaryFrames(t *testing.T) {
	handler, _, _, _ := testPairingTransportHandler(t)
	server := httptest.NewServer(handler)
	defer server.Close()

	websocketURL := "ws" + strings.TrimPrefix(server.URL, "http") + pairingTransportPath
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{pairingflow.WebSocketSubprotocol}
	headers := http.Header{"Origin": []string{"https://attacker.example"}}
	connection, response, err := dialer.Dial(websocketURL, headers)
	if connection != nil {
		connection.Close()
	}
	if err == nil || response == nil || response.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-origin dial = connection %v response %v error %v", connection, response, err)
	}

	connection = dialPairingTransport(t, server.URL, server.URL)
	defer connection.Close()
	if err := connection.WriteMessage(websocket.TextMessage, []byte("not protobuf")); err != nil {
		t.Fatalf("write text frame: %v", err)
	}
	_, _, err = connection.ReadMessage()
	closeError, ok := err.(*websocket.CloseError)
	if !ok || closeError.Code != websocket.CloseProtocolError {
		t.Fatalf("text frame close error = %v, want protocol close", err)
	}
}

func testPairingTransportHandler(t *testing.T) (http.Handler, *pairing.Manager, *pairingflow.Coordinator, *deviceidentity.Store) {
	t.Helper()
	identity, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        filepath.Join(t.TempDir(), "identity.json"),
		DisplayName: "Home PC",
		Platform:    "windows",
	})
	if err != nil {
		t.Fatalf("create Agent identity: %v", err)
	}
	t.Cleanup(identity.Close)
	invitations, err := pairing.New(pairing.Config{Agent: identity})
	if err != nil {
		t.Fatalf("create invitation manager: %v", err)
	}
	t.Cleanup(invitations.Close)
	flows, err := pairingflow.New(pairingflow.Config{Invitations: invitations, Identity: identity})
	if err != nil {
		t.Fatalf("create flow coordinator: %v", err)
	}
	handler, err := newAgentHTTPHandler(http.NotFoundHandler(), "127.0.0.1:8787", "local-token", invitations, identity, flows)
	if err != nil {
		t.Fatalf("create Agent HTTP handler: %v", err)
	}
	return handler, invitations, flows, identity
}

func dialPairingTransport(t *testing.T, serverURL, origin string) *websocket.Conn {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{pairingflow.WebSocketSubprotocol}
	headers := http.Header{"Origin": []string{origin}}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(serverURL, "http")+pairingTransportPath, headers)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("dial pairing WebSocket (HTTP %d): %v", status, err)
	}
	if connection.Subprotocol() != pairingflow.WebSocketSubprotocol {
		connection.Close()
		t.Fatalf("negotiated subprotocol = %q", connection.Subprotocol())
	}
	_ = connection.SetReadDeadline(time.Now().Add(5 * time.Second))
	return connection
}

func writeProtoFrame(t *testing.T, connection *websocket.Conn, message proto.Message) {
	t.Helper()
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(message)
	if err != nil {
		t.Fatalf("encode frame: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		t.Fatalf("write frame: %v", err)
	}
}

func readProtoFrame(t *testing.T, connection *websocket.Conn, message proto.Message) {
	t.Helper()
	encoded := readBinaryFrame(t, connection)
	if err := proto.Unmarshal(encoded, message); err != nil {
		t.Fatalf("decode frame: %v", err)
	}
}

func readBinaryFrame(t *testing.T, connection *websocket.Conn) []byte {
	t.Helper()
	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read frame: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("frame type = %d, want binary", messageType)
	}
	return encoded
}

func decryptTransportApproval(t *testing.T, transport *e2ee.Transport, ciphertext []byte) *vibebridgev1.PairingApproval {
	t.Helper()
	plaintext, err := transport.Decrypt(ciphertext, []byte(pairingflow.ApprovalAssociatedData))
	if err != nil {
		t.Fatalf("decrypt approval: %v", err)
	}
	approval := new(vibebridgev1.PairingApproval)
	if err := proto.Unmarshal(plaintext, approval); err != nil {
		t.Fatalf("decode approval: %v", err)
	}
	return approval
}

func testTransportPhone(t *testing.T) (*vibebridgev1.SignedDeviceDescriptor, []byte) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate signing key: %v", err)
	}
	agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate agreement key: %v", err)
	}
	deviceID := make([]byte, deviceidentity.DeviceIDBytes)
	if _, err := rand.Read(deviceID); err != nil {
		t.Fatalf("generate device ID: %v", err)
	}
	descriptor := &vibebridgev1.DeviceDescriptor{
		DeviceId:              deviceID,
		DisplayName:           "My Phone",
		Platform:              "web",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		SigningPublicKey:      publicKey,
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             timestamppb.Now(),
		KeyVersion:            1,
		SupportedVersions: &vibebridgev1.ProtocolVersionRange{
			Minimum: &vibebridgev1.ProtocolVersion{Major: 1},
			Maximum: &vibebridgev1.ProtocolVersion{Major: 1},
		},
	}
	encoded, err := proto.MarshalOptions{Deterministic: true}.Marshal(descriptor)
	if err != nil {
		t.Fatalf("encode descriptor: %v", err)
	}
	signedBytes := append([]byte("VibeBridge device descriptor v1\x00"), encoded...)
	return &vibebridgev1.SignedDeviceDescriptor{
		DeviceDescriptor: descriptor,
		Signature:        ed25519.Sign(privateKey, signedBytes),
	}, agreementKey.Bytes()
}
