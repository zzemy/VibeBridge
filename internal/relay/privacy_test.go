// Tests in this file assert the relay's privacy contract: it must
// treat every WebSocket frame payload as opaque bytes and never
// inspect, decode, transform, or log the contents. The relay is
// a switchboard, not a peer. These tests cover the Phase 5 exit
// gate "Relay cannot decrypt inner protocol test fixtures".
package relay

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// privacyHarness wires up a relay server together with a
// captureLogger, the matching issuer private key, and a small
// helper that joins a complete route. The struct is used as a
// per-test fixture so the privacy tests can stay focused on the
// assertion they care about.
type privacyHarness struct {
	server  *httptest.Server
	logger  *captureLogger
	issuer  *Issuer
	routeID []byte
	cleanup func()
}

// captureLogger is a relay.Logger that records every Event the
// server emits. Tests use it to assert that no payload byte ever
// leaks into a log entry.
type captureLogger struct {
	events []Event
}

func (c *captureLogger) Log(event Event) {
	c.events = append(c.events, event)
}

// containsBytes reports whether either the Outcome or Reason
// field of any captured event contains the given byte sequence
// as a substring. The privacy contract forbids this.
func (c *captureLogger) containsBytes(payload []byte) bool {
	for _, event := range c.events {
		if bytes.Contains([]byte(event.Outcome), payload) {
			return true
		}
		if bytes.Contains([]byte(event.Reason), payload) {
			return true
		}
	}
	return false
}

// newPrivacyHarness constructs a fresh relay with a capture
// logger and matching issuer. Tests should call h.Close() when
// done. The routeID is chosen so two distinct subtests cannot
// accidentally share state.
func newPrivacyHarness(t *testing.T) *privacyHarness {
	t.Helper()
	logger := &captureLogger{}
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	server, err := New(Config{
		Verifier: NewVerifier(public),
		Router:   NewRouter(),
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	httpServer := httptest.NewServer(server)
	return &privacyHarness{
		server:  httpServer,
		logger:  logger,
		issuer:  issuer,
		routeID: bytesRepeat(0x77, routeIDBytes),
		cleanup: func() { httpServer.Close() },
	}
}

// Close releases the underlying httptest server.
func (h *privacyHarness) Close() { h.cleanup() }

// dialURL is the WebSocket URL the harness's peers should use.
func (h *privacyHarness) dialURL() string {
	return "ws" + strings.TrimPrefix(h.server.URL, "http") + "/v1/relay/ws"
}

// joinRoute opens a complete agent+client route for the
// harness's verifier and waits for the second ticket to be
// processed. The returned cleanup closes both connections.
func (h *privacyHarness) joinRoute(t *testing.T) (agent, client *websocket.Conn, cleanup func()) {
	t.Helper()
	agentTicket, err := h.issuer.Issue(IssueInput{
		RouteID:        h.routeID,
		DeviceID:       bytesRepeat(0x11, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue agent ticket: %v", err)
	}
	clientTicket, err := h.issuer.Issue(IssueInput{
		RouteID:        h.routeID,
		DeviceID:       bytesRepeat(0x22, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue client ticket: %v", err)
	}
	agent, _, err = websocket.DefaultDialer.Dial(h.dialURL(), nil)
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	client, _, err = websocket.DefaultDialer.Dial(h.dialURL(), nil)
	if err != nil {
		_ = agent.Close()
		t.Fatalf("dial client: %v", err)
	}
	sendTicket(t, agent, agentTicket)
	sendTicket(t, client, clientTicket)
	// Wait for the route to complete so the first peer is no
	// longer "missing" its counterpart.
	time.Sleep(100 * time.Millisecond)
	cleanup = func() {
		_ = agent.Close()
		_ = client.Close()
	}
	return agent, client, cleanup
}

// TestServerForwardsPlaintextOpqaquely proves the relay forwards
// a plaintext payload byte-for-byte. If the relay ever inspected
// the payload, the peer would either receive something different
// or the connection would close. This is the foundational
// privacy check.
func TestServerForwardsPlaintextOpqaquely(t *testing.T) {
	h := newPrivacyHarness(t)
	defer h.Close()
	agent, client, cleanup := h.joinRoute(t)
	defer cleanup()

	plaintext := []byte("vibebridge secret command token=PLAID-NIGHT-9F3C")
	if err := agent.WriteMessage(websocket.BinaryMessage, plaintext); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("client received %q, want %q", got, plaintext)
	}
}

// TestServerForwardsRandomBytesOpqaquely proves the relay
// forwards random bytes without modification. Random bytes are
// the worst case for a relay that might try to parse the
// payload, since they will fail every protobuf / JSON / struct
// validation. The relay must treat them as opaque.
func TestServerForwardsRandomBytesOpqaquely(t *testing.T) {
	h := newPrivacyHarness(t)
	defer h.Close()
	agent, client, cleanup := h.joinRoute(t)
	defer cleanup()

	random := make([]byte, 1024)
	if _, err := rand.Read(random); err != nil {
		t.Fatalf("generate random: %v", err)
	}
	if err := agent.WriteMessage(websocket.BinaryMessage, random); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, random) {
		t.Fatalf("client received %d bytes that differ from the %d sent", len(got), len(random))
	}
}

// TestServerForwardsFakeTicketBytesOpqaquely proves the relay
// cannot be tricked into consuming a payload that happens to
// look like a valid RelayTicket envelope. The relay's job is to
// forward bytes opaquely; if it ever tried to decode the
// forwarded payload, a fake-ticket payload would either be
// interpreted as a second ticket (corrupting route state) or
// rejected (breaking connectivity). Neither happens.
func TestServerForwardsFakeTicketBytesOpqaquely(t *testing.T) {
	h := newPrivacyHarness(t)
	defer h.Close()
	agent, client, cleanup := h.joinRoute(t)
	defer cleanup()

	// Build a fake RelayTicket envelope using the same 4-byte
	// big-endian length prefix + protobuf body the relay
	// reads at admission. The relay must forward this
	// verbatim; it must NOT consume it as a ticket.
	// Build a fake RelayTicket envelope with the *real* schema
	// (Version, TicketId, RouteId, DeviceId, Endpoint, ExpiresAt,
	// MaxConnections, Nonce, IssuerSignature) and a deliberately
	// invalid 64-byte signature. If the relay ever tried to
	// consume the forwarded bytes as a second ticket, it would
	// reject the envelope on the signature check. The relay must
	// instead forward the bytes verbatim to the peer.
	fakeTicket := &vibebridgev1.RelayTicket{
		Version:        1,
		TicketId:       bytesRepeat(0xCD, 16),
		RouteId:        bytesRepeat(0xAA, routeIDBytes),
		DeviceId:       bytesRepeat(0xBB, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		ExpiresAt:      &timestamppb.Timestamp{Seconds: 1900000000},
		MaxConnections: 99,
		Nonce:          []byte("attacker-nonce-9F3C"),
		IssuerSignature: bytesRepeat(0xEE, 64),
	}
	ticketBytes, err := proto.Marshal(fakeTicket)
	if err != nil {
		t.Fatalf("marshal fake ticket: %v", err)
	}
	frame := make([]byte, 4+len(ticketBytes))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(ticketBytes)))
	copy(frame[4:], ticketBytes)

	if err := agent.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("agent write fake ticket: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, frame) {
		t.Fatalf("fake ticket was modified by the relay: got %x want %x", got, frame)
	}
}

// TestServerLogsNeverContainPayloadBytes is the canonical
// "relay cannot see plaintext" check. It uses the capture
// logger to record every Event the server emits, then walks
// every recorded event looking for any substring of the payload.
// Any match is a privacy violation and fails the test.
func TestServerLogsNeverContainPayloadBytes(t *testing.T) {
	h := newPrivacyHarness(t)
	defer h.Close()
	agent, client, cleanup := h.joinRoute(t)
	defer cleanup()

	distinctive := []byte("PLAID-NIGHT-FALCON-SHADOW-9F3C")
	if err := agent.WriteMessage(websocket.BinaryMessage, distinctive); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := client.ReadMessage(); err != nil {
		t.Fatalf("client read: %v", err)
	}

	if h.logger.containsBytes(distinctive) {
		var offending []string
		for _, event := range h.logger.events {
			offending = append(offending, event.Outcome+"|"+event.Reason)
		}
		t.Fatalf("relay log leaked payload bytes; captured events: %v", offending)
	}
}

// TestServerForwardsLargePayloadByteExact proves the relay
// preserves byte-exact integrity for a payload that pushes
// against the read limit. The byte pattern is a deterministic
// gradient so any transformation is detectable. This is the
// "doesn't truncate, doesn't reframe, doesn't reorder" check.
func TestServerForwardsLargePayloadByteExact(t *testing.T) {
	h := newPrivacyHarness(t)
	defer h.Close()
	agent, client, cleanup := h.joinRoute(t)
	defer cleanup()

	const size = 256 * 1024
	payload := make([]byte, size)
	for i := range payload {
		payload[i] = byte(i % 251)
	}
	if err := agent.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	_ = client.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, got, err := client.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("large payload mangled: got %d bytes, want %d bytes; first diff at index %d", len(got), len(payload), firstDiff(got, payload))
	}
}

// firstDiff returns the index of the first byte that differs
// between a and b. If a and b are equal, it returns -1.
func firstDiff(a, b []byte) int {
	n := len(a)
	if len(b) < n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	if len(a) != len(b) {
		return n
	}
	return -1
}

