package server

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/gorilla/websocket"
)

type Config struct {
	SessionToken string
	WebDir       string
	Command      []string
}

type Server struct {
	config   Config
	upgrader websocket.Upgrader
	active   atomic.Bool
}

type ClientMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
	Cols int    `json:"cols,omitempty"`
	Rows int    `json:"rows,omitempty"`
}

type ServerMessage struct {
	Type string `json:"type"`
	Data string `json:"data,omitempty"`
}

func New(config Config) *Server {
	return &Server{
		config: config,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleWebSocket(w http.ResponseWriter, r *http.Request) {
	if !s.validToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session token"})
		return
	}
	if !s.active.CompareAndSwap(false, true) {
		writeJSON(w, http.StatusConflict, map[string]string{"error": "session already active"})
		return
	}
	defer s.active.Store(false)

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()

	conn.SetReadLimit(64 * 1024)
	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(5 * time.Minute))
	})

	writer := websocketWriter{conn: conn}
	if err := s.bridgeCommand(r.Context(), &writer, conn); err != nil {
		_ = writer.writeJSON(ServerMessage{Type: "error", Data: err.Error()})
	}
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"),
		time.Now().Add(time.Second),
	)
}

func (s *Server) bridgeCommand(ctx context.Context, writer *websocketWriter, conn *websocket.Conn) error {
	if len(s.config.Command) == 0 {
		return errors.New("no command configured")
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	terminal, err := pty.New()
	if err != nil {
		return err
	}
	defer terminal.Close()

	cmd := terminal.CommandContext(ctx, s.config.Command[0], s.config.Command[1:]...)
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		return err
	}

	waitCh := make(chan error, 1)
	waitDone := make(chan struct{})
	go func() {
		waitCh <- cmd.Wait()
		close(waitDone)
	}()
	defer func() {
		cancel()
		select {
		case <-waitDone:
		case <-time.After(2 * time.Second):
		}
	}()

	if err := writer.writeJSON(ServerMessage{Type: "output", Data: "started PTY shell: " + strings.Join(s.config.Command, " ") + "\r\n"}); err != nil {
		return err
	}

	go streamOutput(writer, terminal)

	for {
		select {
		case err := <-waitCh:
			if err != nil {
				_ = writer.writeJSON(ServerMessage{Type: "exit", Data: err.Error()})
			} else {
				_ = writer.writeJSON(ServerMessage{Type: "exit", Data: "process exited"})
			}
			return nil
		default:
		}

		var msg ClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return nil
		}

		switch msg.Type {
		case "input":
			if _, err := io.WriteString(terminal, msg.Data); err != nil {
				return err
			}
		case "exit":
			_ = writer.writeJSON(ServerMessage{Type: "output", Data: "ending session\r\n"})
			return nil
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				if err := terminal.Resize(msg.Cols, msg.Rows); err != nil {
					return err
				}
			}
		case "ping":
			if err := writer.writeJSON(ServerMessage{Type: "pong"}); err != nil {
				return err
			}
		default:
			if err := writer.writeJSON(ServerMessage{Type: "error", Data: "unsupported message type"}); err != nil {
				return err
			}
		}
	}
}

type websocketWriter struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (w *websocketWriter) writeJSON(value ServerMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(value)
}

func streamOutput(writer *websocketWriter, reader io.Reader) {
	buffer := make([]byte, 4096)
	for {
		n, err := reader.Read(buffer)
		if n > 0 {
			if writeErr := writer.writeJSON(ServerMessage{Type: "output", Data: string(buffer[:n])}); writeErr != nil {
				return
			}
		}
		if err != nil {
			return
		}
	}
}

func (s *Server) handleStatic(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && strings.HasPrefix(r.URL.Path, "/ws") {
		http.NotFound(w, r)
		return
	}

	if s.config.WebDir == "" {
		writeFallback(w)
		return
	}

	indexPath := filepath.Join(s.config.WebDir, "index.html")
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		writeFallback(w)
		return
	}

	fileServer := http.FileServer(http.Dir(s.config.WebDir))
	fileServer.ServeHTTP(w, r)
}

func (s *Server) validToken(r *http.Request) bool {
	token := r.URL.Query().Get("token")
	return token != "" && token == s.config.SessionToken
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeFallback(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`<!doctype html>
<html lang="en">
<head>
  <meta charset="utf-8">
  <meta name="viewport" content="width=device-width, initial-scale=1">
  <title>VibeBridge</title>
  <style>
    body { margin: 0; min-height: 100vh; display: grid; place-items: center; background: #09090b; color: #fafafa; font-family: ui-sans-serif, system-ui, sans-serif; }
    main { max-width: 32rem; padding: 2rem; }
    code { color: #a7f3d0; }
  </style>
</head>
<body>
  <main>
    <h1>VibeBridge backend is running</h1>
    <p>Build the frontend with <code>pnpm --dir web build</code>, or run Vite dev server during development.</p>
  </main>
</body>
</html>`))
}
