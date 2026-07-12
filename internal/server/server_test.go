package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/gorilla/websocket"
)

func TestHandlerHealthAndRejectsInvalidToken(t *testing.T) {
	app := New(Config{SessionToken: "expected-token"})
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	response, err := http.Get(testServer.URL + "/healthz")
	if err != nil {
		t.Fatalf("request health check: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		t.Fatalf("health status = %d, want %d", response.StatusCode, http.StatusOK)
	}

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws?token=wrong-token"
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if connection != nil {
		connection.Close()
	}
	if err == nil {
		t.Fatal("invalid token WebSocket connection succeeded")
	}
	if response == nil || response.StatusCode != http.StatusUnauthorized {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("invalid token status = %d, want %d", status, http.StatusUnauthorized)
	}
}

func TestStatusRequiresTokenAndReportsSessionState(t *testing.T) {
	app := New(Config{
		SessionToken:     "expected-token",
		ReconnectTimeout: 45 * time.Second,
		IdleTimeout:      10 * time.Minute,
	})
	now := time.Date(2026, time.July, 12, 8, 30, 0, 0, time.UTC)
	app.session = &ptySession{startedAt: now, lastActivityAt: now.Add(time.Minute), lifecycle: sessionLifecycle{state: sessionStateDetached}}
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	unauthorized, err := http.Get(testServer.URL + "/status")
	if err != nil {
		t.Fatalf("request unauthorized status: %v", err)
	}
	unauthorized.Body.Close()
	if unauthorized.StatusCode != http.StatusUnauthorized {
		t.Fatalf("unauthorized status = %d, want %d", unauthorized.StatusCode, http.StatusUnauthorized)
	}

	response, err := http.Get(testServer.URL + "/status?token=expected-token")
	if err != nil {
		t.Fatalf("request session status: %v", err)
	}
	defer response.Body.Close()
	var status SessionStatus
	if err := json.NewDecoder(response.Body).Decode(&status); err != nil {
		t.Fatalf("decode session status: %v", err)
	}
	if status.State != "detached" {
		t.Fatalf("session state = %q, want detached", status.State)
	}
	if status.ReconnectTimeoutSeconds != 45 || status.IdleTimeoutSeconds != 600 {
		t.Fatalf("timeouts = %d/%d, want 45/600", status.ReconnectTimeoutSeconds, status.IdleTimeoutSeconds)
	}
}

func TestSameOrigin(t *testing.T) {
	app := New(Config{})
	cases := []struct {
		name   string
		origin string
		want   bool
	}{
		{name: "native client", want: true},
		{name: "same host", origin: "http://relay.local:8787", want: true},
		{name: "different host", origin: "http://attacker.invalid", want: false},
		{name: "malformed origin", origin: "://bad", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "http://relay.local:8787/ws", nil)
			request.Host = "relay.local:8787"
			if testCase.origin != "" {
				request.Header.Set("Origin", testCase.origin)
			}
			if got := app.sameOrigin(request); got != testCase.want {
				t.Fatalf("sameOrigin() = %t, want %t", got, testCase.want)
			}
		})
	}
}

func TestWebSocketRejectsCrossOrigin(t *testing.T) {
	app := New(Config{SessionToken: "expected-token"})
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http") + "/ws?token=expected-token"
	requestHeader := http.Header{"Origin": []string{"http://attacker.invalid"}}
	connection, response, err := websocket.DefaultDialer.Dial(wsURL, requestHeader)
	if connection != nil {
		connection.Close()
	}
	if err == nil {
		t.Fatal("cross-origin WebSocket connection succeeded")
	}
	if response == nil || response.StatusCode != http.StatusForbidden {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("cross-origin status = %d, want %d", status, http.StatusForbidden)
	}
}

func TestWebSocketWriterWritesBinaryOutput(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			return
		}
		defer connection.Close()

		writer := websocketWriter{conn: connection}
		_ = writer.writeBinary([]byte{0x1b, '[', '3', '2', 'm', 'o', 'k'})
	}))
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	defer connection.Close()

	messageType, data, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read binary output: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("output frame type = %d, want binary", messageType)
	}
	want := []byte{0x1b, '[', '3', '2', 'm', 'o', 'k'}
	if !bytes.Equal(data, want) {
		t.Fatalf("output bytes = %q, want %q", data, want)
	}
}

func TestKeepConnectionAliveSendsPing(t *testing.T) {
	testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, err := websocket.Upgrade(w, r, nil, 1024, 1024)
		if err != nil {
			return
		}
		defer connection.Close()

		stop := keepConnectionAlive(&websocketWriter{conn: connection}, 10*time.Millisecond)
		defer stop()
		for {
			if _, _, err := connection.ReadMessage(); err != nil {
				return
			}
		}
	}))
	defer testServer.Close()

	wsURL := "ws" + strings.TrimPrefix(testServer.URL, "http")
	connection, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial WebSocket: %v", err)
	}
	defer connection.Close()

	pingReceived := make(chan struct{}, 1)
	connection.SetPingHandler(func(string) error {
		select {
		case pingReceived <- struct{}{}:
		default:
		}
		return nil
	})
	go func() {
		_, _, _ = connection.ReadMessage()
	}()

	select {
	case <-pingReceived:
	case <-time.After(time.Second):
		t.Fatal("did not receive a WebSocket Ping control frame")
	}
}

func TestTerminateUsesSingleLifecycleAndCleanupPath(t *testing.T) {
	terminal := &countingPTY{}
	processTree := &countingProcessTree{}
	done := make(chan struct{})
	close(done)
	session := &ptySession{
		terminal:    terminal,
		processTree: processTree,
		cancel:      func() {},
		done:        done,
		clock:       systemClock{},
		lifecycle:   sessionLifecycle{state: sessionStateDetached},
	}

	session.terminate()
	session.terminate()

	if session.lifecycle.state != sessionStateEnding {
		t.Fatalf("state = %q, want ending until process exit is observed", session.lifecycle.state)
	}
	if terminal.closeCalls != 1 || processTree.closeCalls != 1 {
		t.Fatalf("cleanup calls = terminal %d, process tree %d; want 1 each", terminal.closeCalls, processTree.closeCalls)
	}
}

func TestCloseResourcesIsIdempotent(t *testing.T) {
	terminal := &countingPTY{}
	processTree := &countingProcessTree{}
	session := &ptySession{terminal: terminal, processTree: processTree}

	if err := session.closeResources(); err != nil {
		t.Fatalf("first close: %v", err)
	}
	if err := session.closeResources(); err != nil {
		t.Fatalf("second close: %v", err)
	}
	if terminal.closeCalls != 1 {
		t.Fatalf("terminal close calls = %d, want 1", terminal.closeCalls)
	}
	if processTree.closeCalls != 1 {
		t.Fatalf("process-tree close calls = %d, want 1", processTree.closeCalls)
	}
}

type countingProcessTree struct {
	closeCalls int
}

func (tree *countingProcessTree) Close() error {
	tree.closeCalls++
	return nil
}

type countingPTY struct {
	closeCalls int
}

func (p *countingPTY) Close() error                                               { p.closeCalls++; return nil }
func (p *countingPTY) Command(string, ...string) *pty.Cmd                         { return nil }
func (p *countingPTY) CommandContext(context.Context, string, ...string) *pty.Cmd { return nil }
func (p *countingPTY) Fd() uintptr                                                { return 0 }
func (p *countingPTY) Name() string                                               { return "counting" }
func (p *countingPTY) Read([]byte) (int, error)                                   { return 0, io.EOF }
func (p *countingPTY) Resize(int, int) error                                      { return nil }
func (p *countingPTY) Write(data []byte) (int, error)                             { return len(data), nil }
