package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/binary"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

func dialRelay(t *testing.T, address string) *websocket.Conn {
	t.Helper()
	rawURL := "ws" + strings.TrimPrefix(address, "http") + "/v1/relay/ws"
	connection, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	return connection
}

func sendTicket(t *testing.T, connection *websocket.Conn, ticket *vibebridgev1.RelayTicket) {
	t.Helper()
	body, err := proto.Marshal(ticket)
	if err != nil {
		t.Fatalf("marshal ticket: %v", err)
	}
	frame := make([]byte, 4+len(body))
	binary.BigEndian.PutUint32(frame[:4], uint32(len(body)))
	copy(frame[4:], body)
	if err := connection.WriteMessage(websocket.BinaryMessage, frame); err != nil {
		t.Fatalf("write ticket: %v", err)
	}
}

func buildRelayServer(t *testing.T, issuers ...ed25519.PublicKey) (*httptest.Server, *Server) {
	t.Helper()
	router := NewRouter()
	server, err := New(Config{
		Verifier: NewVerifier(issuers...),
		Router:   router,
		Logger:   Discard(),
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server)
	return httpServer, server
}

func newIntegrationKeyPair(t *testing.T) (ed25519.PublicKey, ed25519.PrivateKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return public, private
}

func TestServerEndToEndForward(t *testing.T) {
	public, private := newIntegrationKeyPair(t)
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	routeID := bytesRepeat(0x42, routeIDBytes)
	agentTicket, err := issuer.Issue(IssueInput{
		RouteID:        routeID,
		DeviceID:       bytesRepeat(0x11, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue agent: %v", err)
	}
	clientTicket, err := issuer.Issue(IssueInput{
		RouteID:        routeID,
		DeviceID:       bytesRepeat(0x22, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue client: %v", err)
	}

	httpServer, _ := buildRelayServer(t, public)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http")
	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/v1/relay/ws", nil)
	if err != nil {
		t.Fatalf("dial agent: %v", err)
	}
	defer agentConn.Close()
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL+"/v1/relay/ws", nil)
	if err != nil {
		t.Fatalf("dial client: %v", err)
	}
	defer clientConn.Close()

	sendTicket(t, agentConn, agentTicket)
	sendTicket(t, clientConn, clientTicket)

	// Give the server a brief moment to process both tickets and
	// join the peers to the route. The relay is an in-memory
	// switchboard so the work is bounded; without this pause the
	// test can race the server and lose the first payload to a
	// still-empty route (the relay drops forward messages whose
	// destination has not yet been joined, by design).
	time.Sleep(100 * time.Millisecond)

	// Forward agent -> client.
	payload := []byte("vibebridge-test-payload")
	if err := agentConn.WriteMessage(websocket.BinaryMessage, payload); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	_ = clientConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, got, err := clientConn.ReadMessage()
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if mt != websocket.BinaryMessage {
		t.Fatalf("unexpected client message type: %d", mt)
	}
	if string(got) != string(payload) {
		t.Fatalf("client received %q, want %q", got, payload)
	}

	// Forward client -> agent.
	ack := []byte("vibebridge-ack")
	if err := clientConn.WriteMessage(websocket.BinaryMessage, ack); err != nil {
		t.Fatalf("client write: %v", err)
	}
	_ = agentConn.SetReadDeadline(time.Now().Add(2 * time.Second))
	mt, got, err = agentConn.ReadMessage()
	if err != nil {
		t.Fatalf("agent read: %v", err)
	}
	if string(got) != string(ack) {
		t.Fatalf("agent received %q, want %q", got, ack)
	}
}

func TestServerRejectsInvalidTicket(t *testing.T) {
	public, private := newIntegrationKeyPair(t)
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	// Issue a valid ticket but configure the relay with a *different*
	// issuer key so the signature fails.
	decoyPublic, decoyPrivate := newIntegrationKeyPair(t)
	_ = decoyPublic
	_ = decoyPrivate
	// Use the decoy issuer to sign the ticket.
	decoyIssuer, err := NewIssuer(decoyPrivate)
	if err != nil {
		t.Fatalf("decoy issuer: %v", err)
	}
	ticket, err := decoyIssuer.Issue(IssueInput{
		RouteID:        bytesRepeat(0x55, routeIDBytes),
		DeviceID:       bytesRepeat(0x33, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue decoy: %v", err)
	}
	_ = issuer
	_ = public

	httpServer, _ := buildRelayServer(t, public)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/relay/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	// The server should close the connection after rejecting the
	// ticket. We cannot rely on the next WriteMessage to fail
	// immediately because the kernel may accept the bytes into its
	// send buffer before the close handshake has been observed; the
	// reliable signal is the read side of the WebSocket returning an
	// error when the server closes. We start a reader goroutine and
	// wait for it to exit before attempting the write.
	closed := make(chan struct{})
	go func() {
		defer close(closed)
		for {
			if _, _, err := conn.ReadMessage(); err != nil {
				return
			}
		}
	}()
	sendTicket(t, conn, ticket)

	select {
	case <-closed:
	case <-time.After(2 * time.Second):
		t.Fatalf("server did not close connection after ticket rejection")
	}

	// Subsequent writes should now fail because the server has
	// closed the WebSocket.
	_ = conn.SetWriteDeadline(time.Now().Add(2 * time.Second))
	if err := conn.WriteMessage(websocket.BinaryMessage, []byte("x")); err == nil {
		t.Fatalf("expected write to fail after ticket rejection")
	}
}

func TestServerRejectsOversizedTicket(t *testing.T) {
	public, _ := newIntegrationKeyPair(t)
	httpServer, _ := buildRelayServer(t, public)
	defer httpServer.Close()

	wsURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/relay/ws"
	conn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()
	frame := make([]byte, 4+maxTicketBytes+1)
	binary.BigEndian.PutUint32(frame[:4], uint32(maxTicketBytes+1))
	_ = conn.WriteMessage(websocket.BinaryMessage, frame)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, _, err := conn.ReadMessage(); err == nil {
		t.Fatalf("expected close after oversized ticket")
	}
}

func TestServerRespectsCustomOrigin(t *testing.T) {
	public, _ := newIntegrationKeyPair(t)
	router := NewRouter()
	server, err := New(Config{
		Verifier:      NewVerifier(public),
		Router:        router,
		Logger:        Discard(),
		AllowedOrigins: []string{"https://allowed.example.com"},
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	u, _ := url.Parse(httpServer.URL)
	wsURL := "ws://" + u.Host + "/v1/relay/ws"
	header := http.Header{}
	header.Set("Origin", "https://blocked.example.com")
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, header)
	if err == nil {
		t.Fatalf("expected origin rejection")
	}
	if resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("expected status 403, got %v", resp)
	}
}

func TestServerShutdownStopsAccepting(t *testing.T) {
	public, _ := newIntegrationKeyPair(t)
	httpServer, server := buildRelayServer(t, public)
	defer httpServer.Close()

	_ = server.Shutdown(nil)
	// New connections should be rejected with 503.
	u, _ := url.Parse(httpServer.URL)
	wsURL := "ws://" + u.Host + "/v1/relay/ws"
	_, resp, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err == nil {
		t.Fatalf("expected dial to fail after shutdown")
	}
	if resp == nil || resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected status 503, got %v", resp)
	}
}


func TestServerEchoesClientSubprotocol(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	server, err := New(Config{
		Verifier: NewVerifier(priv.Public().(ed25519.PublicKey)),
	})
	if err != nil {
		t.Fatalf("new relay: %v", err)
	}
	httpServer := httptest.NewServer(server)
	defer httpServer.Close()
	defer server.Shutdown(nil)

	u, _ := url.Parse(httpServer.URL)
	wsURL := "ws://" + u.Host + "/v1/relay/ws"

	// Dial with a subprotocol the relay should echo back.
	dialer := websocket.Dialer{Subprotocols: []string{"vibebridge.v1"}}
	conn, resp, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer conn.Close()

	if resp.Header.Get("Sec-WebSocket-Protocol") != "vibebridge.v1" {
		t.Fatalf("expected subprotocol vibebridge.v1, got %q",
			resp.Header.Get("Sec-WebSocket-Protocol"))
	}

	// Dial without a subprotocol — relay must not invent one.
	conn2, resp2, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial without subprotocol: %v", err)
	}
	defer conn2.Close()

	if resp2.Header.Get("Sec-WebSocket-Protocol") != "" {
		t.Fatalf("expected no subprotocol, got %q",
			resp2.Header.Get("Sec-WebSocket-Protocol"))
	}
}

