package server

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
)

var pairedTestTime = time.Date(2026, time.July, 29, 12, 0, 0, 0, time.UTC)

type testClient struct {
	signingKey ed25519.PrivateKey
	signed     *vibebridgev1.SignedDeviceDescriptor
	deviceID   []byte
}

func newTestClient(t *testing.T, name string, idByte byte) testClient {
	t.Helper()
	_, signingKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test signing key: %v", err)
	}
	agreementKey, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate test agreement key: %v", err)
	}
	signed, err := deviceidentity.NewSignedDescriptor(deviceidentity.DescriptorOptions{
		DeviceID:              bytes.Repeat([]byte{idByte}, deviceidentity.DeviceIDBytes),
		DisplayName:           name,
		Platform:              "web",
		DeviceClass:           vibebridgev1.DeviceClass_DEVICE_CLASS_CLIENT,
		SigningPublicKey:      signingKey.Public().(ed25519.PublicKey),
		KeyAgreementPublicKey: agreementKey.PublicKey().Bytes(),
		CreatedAt:             pairedTestTime,
		KeyVersion:            1,
		ProtocolMajor:         1,
		ProtocolMinor:         0,
	}, signingKey)
	if err != nil {
		t.Fatalf("sign test client descriptor: %v", err)
	}
	return testClient{signingKey: signingKey, signed: signed, deviceID: signed.DeviceDescriptor.DeviceId}
}

func newPairedTestServer(t *testing.T, requirePaired bool) (*Server, *deviceidentity.Store) {
	t.Helper()
	path := fmt.Sprintf("%s/identity-%d.json", t.TempDir(), time.Now().UnixNano())
	store, err := deviceidentity.LoadOrCreate(deviceidentity.Options{
		Path:        path,
		DisplayName: "Paired Test Agent",
		Platform:    "test",
		Now:         func() time.Time { return pairedTestTime },
	})
	if err != nil {
		t.Fatalf("create device store: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	server := New(Config{
		SessionToken:         "legacy-token",
		RequirePairedSession: requirePaired,
		DeviceStore:          store,
	})
	return server, store
}

func (c testClient) buildChallenge(t *testing.T, nonceHex string) http.Header {
	t.Helper()
	signature, err := signSessionChallenge(c.signingKey, nonceHex)
	if err != nil {
		t.Fatalf("sign session challenge: %v", err)
	}
	header := http.Header{}
	header.Set(pairedDeviceHeader, hex.EncodeToString(c.deviceID))
	header.Set(pairedSessionNonceHeader, nonceHex)
	header.Set(pairedDeviceSignatureHeader, base64.StdEncoding.EncodeToString(signature))
	return header
}

func fetchNonce(t *testing.T, baseURL string) (string, int) {
	t.Helper()
	response, err := http.Get(baseURL + "/pairing/session-nonce")
	if err != nil {
		t.Fatalf("fetch session nonce: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("session nonce status = %d, want 200", response.StatusCode)
	}
	var body struct {
		Nonce     string `json:"nonce"`
		ExpiresIn int    `json:"expires_in"`
	}
	if err := json.NewDecoder(response.Body).Decode(&body); err != nil {
		t.Fatalf("decode session nonce: %v", err)
	}
	if _, err := hex.DecodeString(body.Nonce); err != nil || len(body.Nonce) != sessionNonceBytes*2 {
		t.Fatalf("session nonce hex length = %d, want %d", len(body.Nonce), sessionNonceBytes*2)
	}
	return body.Nonce, body.ExpiresIn
}

func dialPaired(t *testing.T, testServer *httptest.Server, header http.Header, withSubprotocol bool) (int, error) {
	t.Helper()
	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws"
	// Copy the shared DefaultDialer instead of mutating it: assigning
	// Subprotocols on the package-level instance would leak the v1 offer into
	// every later dial in this test binary (including legacy-client tests).
	dialer := *websocket.DefaultDialer
	if withSubprotocol {
		dialer.Subprotocols = []string{protocolv1.WebSocketSubprotocol}
	}
	conn, response, err := dialer.Dial(wsURL, header)
	if conn != nil {
		_ = conn.Close()
	}
	status := 0
	if response != nil {
		status = response.StatusCode
	}
	return status, err
}

func TestPairedSessionEndpointIssuesNonce(t *testing.T) {
	server, _ := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	nonce, expiresIn := fetchNonce(t, testServer.URL)
	if expiresIn != int(sessionNonceDefaultTTL.Seconds()) {
		t.Fatalf("expires_in = %d, want %d", expiresIn, int(sessionNonceDefaultTTL.Seconds()))
	}
	if nonce == "" {
		t.Fatal("nonce is empty")
	}
}

func TestPairedSessionAcceptsAuthorizedDevice(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x10)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	nonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, nonce)

	status, err := dialPaired(t, testServer, header, true)
	if err != nil {
		t.Fatalf("paired dial failed: %v (status %d)", err, status)
	}
}

func TestPairedSessionRejectsRevokedDevice(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x11)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	if _, err := store.Revoke(client.deviceID); err != nil {
		t.Fatalf("revoke client: %v", err)
	}
	nonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, nonce)

	status, err := dialPaired(t, testServer, header, true)
	if err == nil {
		t.Fatalf("revoked dial succeeded (status %d)", status)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("revoked status = %d, want 401", status)
	}
}

func TestPairedSessionRejectsExpiredNonce(t *testing.T) {
	nonces := newNonceStore(time.Now, 5*time.Millisecond)
	nonceHex, _, err := nonces.issue("127.0.0.1:1234")
	if err != nil {
		t.Fatalf("issue nonce: %v", err)
	}
	time.Sleep(30 * time.Millisecond)
	if err := nonces.consume(nonceHex, "127.0.0.1:1234"); !errors.Is(err, errPairedNonceUnknown) {
		t.Fatalf("expired nonce error = %v, want errPairedNonceUnknown", err)
	}
}

func TestPairedSessionRejectsNonceReplay(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x12)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	nonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, nonce)

	firstStatus, firstErr := dialPaired(t, testServer, header, true)
	if firstErr != nil {
		t.Fatalf("first dial failed: %v (status %d)", firstErr, firstStatus)
	}
	secondStatus, secondErr := dialPaired(t, testServer, header, true)
	if secondErr == nil {
		t.Fatalf("replay dial succeeded (status %d)", secondStatus)
	}
	if secondStatus != http.StatusUnauthorized {
		t.Fatalf("replay status = %d, want 401", secondStatus)
	}
}

func TestPairedSessionRejectsCrossOrigin(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x13)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	nonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, nonce)
	header.Set("Origin", "http://attacker.invalid")

	status, err := dialPaired(t, testServer, header, true)
	if err == nil {
		t.Fatalf("cross-origin dial succeeded (status %d)", status)
	}
	if status != http.StatusForbidden {
		t.Fatalf("cross-origin status = %d, want 403", status)
	}
}

func TestPairedSessionRejectsUnknownDevice(t *testing.T) {
	server, _ := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Stranger", 0x14)
	nonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, nonce)

	status, err := dialPaired(t, testServer, header, true)
	if err == nil {
		t.Fatalf("unknown device dial succeeded (status %d)", status)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("unknown device status = %d, want 401", status)
	}
}

func TestPairedSessionRejectsWrongSignature(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x15)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	otherNonce, _ := fetchNonce(t, testServer.URL)
	header := client.buildChallenge(t, otherNonce)

	secondNonce, _ := fetchNonce(t, testServer.URL)
	header.Set(pairedSessionNonceHeader, secondNonce)

	status, err := dialPaired(t, testServer, header, true)
	if err == nil {
		t.Fatalf("mismatched signature dial succeeded (status %d)", status)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("mismatched signature status = %d, want 401", status)
	}
}

func TestPairedSessionRejectsMissingHeaders(t *testing.T) {
	server, store := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	client := newTestClient(t, "Browser", 0x16)
	if _, err := store.Authorize(client.signed); err != nil {
		t.Fatalf("authorize client: %v", err)
	}
	header := http.Header{}

	status, err := dialPaired(t, testServer, header, true)
	if err == nil {
		t.Fatalf("missing headers dial succeeded (status %d)", status)
	}
	if status != http.StatusUnauthorized {
		t.Fatalf("missing headers status = %d, want 401", status)
	}
}

func TestPairedSessionAcceptsLegacyToken(t *testing.T) {
	server, _ := newPairedTestServer(t, true)
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws?token=legacy-token"
	conn, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if conn != nil {
		_ = conn.Close()
	}
	if err != nil {
		t.Fatalf("legacy token dial failed: %v", err)
	}
	if response.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("legacy token status = %d, want 101", response.StatusCode)
	}
}

func TestPairedSessionEndpointUnavailableWithoutDeviceStore(t *testing.T) {
	server := New(Config{SessionToken: "token"})
	testServer := httptest.NewServer(server.Handler())
	defer testServer.Close()

	parsed, err := url.Parse(testServer.URL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	nonces, err := http.Get("http://" + parsed.Host + "/pairing/session-nonce")
	if err != nil {
		t.Fatalf("fetch session nonce: %v", err)
	}
	defer nonces.Body.Close()
	io.Copy(io.Discard, nonces.Body)
	// The route is not registered when the gate is nil, so the catch-all handler serves.
	// We do not assert a specific status: the contract is only that the endpoint is not paired-aware.
	if nonces.StatusCode == http.StatusOK {
		t.Fatal("session-nonce endpoint returned 200 without a device store")
	}
}
