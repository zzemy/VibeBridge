package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
