package relayclient_test

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zzemy/VibeBridge/internal/relayclient"
)

// wsTestServer creates a WebSocket test server that accepts one connection
// and returns it. The server URL is ws://<addr>/ws.
func wsTestServer(t *testing.T) (*httptest.Server, chan *websocket.Conn) {
	t.Helper()
	connCh := make(chan *websocket.Conn, 1)
	upgrader := websocket.Upgrader{CheckOrigin: func(r *http.Request) bool { return true }}
	mux := http.NewServeMux()
	mux.HandleFunc("/ws", func(w http.ResponseWriter, r *http.Request) {
		c, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Errorf("upgrade: %v", err)
			return
		}
		connCh <- c
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(func() { srv.Close() })
	return srv, connCh
}

func wsURL(srv *httptest.Server) string {
	return "ws" + strings.TrimPrefix(srv.URL, "http") + "/ws"
}

// TestBridgeWebSocketPreservesMessageBoundaries verifies that distinct
// WebSocket messages on the local side arrive as distinct frames on the
// remote relay side, and vice versa.
func TestBridgeWebSocketPreservesMessageBoundaries(t *testing.T) {
	f := newRelayFixture(t)
	agentTicket, clientTicket := f.mintPair(t, time.Minute)

	wsSrv, wsConnCh := wsTestServer(t)

	agentStream := f.dial(t, agentTicket)
	defer agentStream.Close()

	clientStream := f.dial(t, clientTicket)
	defer clientStream.Close()
	waitForJoin()

	wsLocal, _, err := websocket.DefaultDialer.Dial(wsURL(wsSrv), nil)
	if err != nil {
		t.Fatalf("dial local ws: %v", err)
	}
	defer wsLocal.Close()

	wsServerSide := <-wsConnCh
	defer wsServerSide.Close()

	bridgeErr := make(chan error, 1)
	go func() {
		bridgeErr <- relayclient.BridgeWebSocket(wsServerSide, agentStream)
	}()

	// local WS → client relay: 3 distinct messages, 3 distinct frames.
	messages := [][]byte{
		[]byte("msg-one"),
		[]byte("msg-two-with-more-bytes"),
		[]byte("three"),
	}
	for _, msg := range messages {
		if err := wsLocal.WriteMessage(websocket.BinaryMessage, msg); err != nil {
			t.Fatalf("ws write: %v", err)
		}
	}
	for i, expected := range messages {
		buf := make([]byte, 256)
		n, err := readFrame(clientStream, buf, 5*time.Second)
		if err != nil {
			t.Fatalf("client read frame %d: %v", i, err)
		}
		if !bytes.Equal(buf[:n], expected) {
			t.Fatalf("frame %d: expected %q, got %q", i, expected, buf[:n])
		}
	}

	// client relay → local WS: 2 distinct messages, 2 distinct WS messages.
	replyMessages := [][]byte{
		[]byte("reply-alpha"),
		[]byte("reply-beta-longer"),
	}
	for _, msg := range replyMessages {
		if _, err := clientStream.Write(msg); err != nil {
			t.Fatalf("client write: %v", err)
		}
	}
	for i, expected := range replyMessages {
		_ = wsLocal.SetReadDeadline(time.Now().Add(5 * time.Second))
		_, payload, err := wsLocal.ReadMessage()
		if err != nil {
			t.Fatalf("ws read message %d: %v", i, err)
		}
		if !bytes.Equal(payload, expected) {
			t.Fatalf("ws message %d: expected %q, got %q", i, expected, payload)
		}
	}

	_ = clientStream.Close()
	select {
	case <-bridgeErr:
	case <-time.After(10 * time.Second):
		t.Fatal("bridge did not return after client close")
	}
}

// TestBridgeWebSocketRejectsNilArgs verifies nil arguments are rejected.
func TestBridgeWebSocketRejectsNilArgs(t *testing.T) {
	// nil local
	if err := relayclient.BridgeWebSocket(nil, nil); err == nil {
		t.Fatal("expected error for nil local")
	}
	// nil remote — need a real *websocket.Conn for local
	wsSrv, wsConnCh := wsTestServer(t)
	wsLocal, _, err := websocket.DefaultDialer.Dial(wsURL(wsSrv), nil)
	if err != nil {
		t.Fatalf("dial local ws: %v", err)
	}
	defer wsLocal.Close()
	wsServerSide := <-wsConnCh
	defer wsServerSide.Close()
	if err := relayclient.BridgeWebSocket(wsServerSide, nil); err == nil {
		t.Fatal("expected error for nil remote")
	}
}

// TestManagerConnectWebSocketBridgesRelayToWS verifies the full lifecycle:
// provision → connect via WebSocket → bidirectional messaging → cleanup.
func TestManagerConnectWebSocketBridgesRelayToWS(t *testing.T) {
	mgr, f := newManagerFixture(t)
	clientTicket, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	wsSrv, wsConnCh := wsTestServer(t)

	connectErr := make(chan error, 1)
	go func() {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL(wsSrv), nil)
		if err != nil {
			connectErr <- err
			return
		}
		connectErr <- mgr.ConnectWebSocket(context.Background(), routeID, wsConn)
	}()

	wsServerSide := <-wsConnCh
	defer wsServerSide.Close()

	time.Sleep(200 * time.Millisecond)

	clientStream := f.dial(t, clientTicket)
	defer clientStream.Close()
	waitForJoin()

	// Client → WS server: message boundary preserved.
	msg1 := []byte("ws-bridge-test-payload")
	if _, err := clientStream.Write(msg1); err != nil {
		t.Fatalf("client write: %v", err)
	}
	_ = wsServerSide.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, payload, err := wsServerSide.ReadMessage()
	if err != nil {
		t.Fatalf("ws server read: %v", err)
	}
	if !bytes.Equal(payload, msg1) {
		t.Fatalf("expected %q, got %q", msg1, payload)
	}

	// WS server → client: message boundary preserved.
	msg2 := []byte("reply-from-ws-server")
	if err := wsServerSide.WriteMessage(websocket.BinaryMessage, msg2); err != nil {
		t.Fatalf("ws server write: %v", err)
	}
	buf := make([]byte, 256)
	n, err := readFrame(clientStream, buf, 5*time.Second)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(buf[:n], msg2) {
		t.Fatalf("expected %q, got %q", msg2, buf[:n])
	}

	active := mgr.ActiveRoutes()
	if len(active) != 1 {
		t.Fatalf("expected 1 active route, got %d", len(active))
	}

	_ = clientStream.Close()
	select {
	case <-connectErr:
	case <-time.After(10 * time.Second):
		t.Fatal("ConnectWebSocket did not return after client close")
	}

	active = mgr.ActiveRoutes()
	if len(active) != 0 {
		t.Fatalf("expected 0 active routes after disconnect, got %d", len(active))
	}
}

// TestManagerConnectWebSocketRejectsUnknownRoute verifies unknown route rejection.
func TestManagerConnectWebSocketRejectsUnknownRoute(t *testing.T) {
	mgr, _ := newManagerFixture(t)
	wsSrv, wsConnCh := wsTestServer(t)

	go func() {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL(wsSrv), nil)
		if err != nil {
			return
		}
		_ = wsConn.Close()
	}()
	wsConn := <-wsConnCh
	defer wsConn.Close()

	err := mgr.ConnectWebSocket(context.Background(), "deadbeef", wsConn)
	if err == nil {
		t.Fatal("expected error for unknown route ID")
	}
	if !contains(err.Error(), "no pending ticket") {
		t.Fatalf("error %q does not mention pending ticket", err)
	}
}

// TestManagerConnectWebSocketContextCancel verifies context cancellation
// closes the connection and returns.
func TestManagerConnectWebSocketContextCancel(t *testing.T) {
	mgr, f := newManagerFixture(t)
	_, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	wsSrv, wsConnCh := wsTestServer(t)

	ctx, cancel := context.WithCancel(context.Background())
	connectErr := make(chan error, 1)
	go func() {
		wsConn, _, err := websocket.DefaultDialer.Dial(wsURL(wsSrv), nil)
		if err != nil {
			connectErr <- err
			return
		}
		connectErr <- mgr.ConnectWebSocket(ctx, routeID, wsConn)
	}()

	wsServerSide := <-wsConnCh
	defer wsServerSide.Close()

	time.Sleep(200 * time.Millisecond)
	if len(mgr.ActiveRoutes()) != 1 {
		t.Fatalf("expected 1 active route, got %d", len(mgr.ActiveRoutes()))
	}

	cancel()
	select {
	case <-connectErr:
	case <-time.After(10 * time.Second):
		t.Fatal("ConnectWebSocket did not return after context cancel")
	}

	if len(mgr.ActiveRoutes()) != 0 {
		t.Fatalf("expected 0 active routes after cancel, got %d", len(mgr.ActiveRoutes()))
	}
}
