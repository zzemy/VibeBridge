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
	"time"

	pty "github.com/aymanbagabas/go-pty"
	"github.com/gorilla/websocket"
)

const maxBufferedOutputChunks = 256

type Config struct {
	SessionToken     string
	WebDir           string
	Command          []string
	ReconnectTimeout time.Duration
}

type Server struct {
	config   Config
	upgrader websocket.Upgrader

	mu      sync.Mutex
	session *ptySession
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
	if config.ReconnectTimeout <= 0 {
		config.ReconnectTimeout = 90 * time.Second
	}

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

	writer := &websocketWriter{conn: conn}
	session, err := s.getOrCreateSession()
	if err != nil {
		_ = writer.writeJSON(ServerMessage{Type: "error", Data: err.Error()})
		writeClose(conn)
		return
	}

	if !session.attach(writer) {
		_ = writer.writeJSON(ServerMessage{Type: "error", Data: "session already active"})
		writeClose(conn)
		return
	}
	defer session.detach(writer, s.config.ReconnectTimeout)

	s.readClientMessages(session, writer, conn)
	writeClose(conn)
}

func (s *Server) getOrCreateSession() (*ptySession, error) {
	if len(s.config.Command) == 0 {
		return nil, errors.New("no command configured")
	}

	s.mu.Lock()
	current := s.session
	s.mu.Unlock()

	if current != nil && !current.isEnded() {
		return current, nil
	}

	session, err := newPTYSession(s.config.Command, s.clearSession)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil && !s.session.isEnded() {
		session.terminate()
		return s.session, nil
	}
	s.session = session
	return session, nil
}

func (s *Server) clearSession(session *ptySession) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session == session {
		s.session = nil
	}
}

func (s *Server) readClientMessages(session *ptySession, writer *websocketWriter, conn *websocket.Conn) {
	for {
		var msg ClientMessage
		if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "input":
			if err := session.writeInput(msg.Data); err != nil {
				_ = writer.writeJSON(ServerMessage{Type: "error", Data: err.Error()})
				return
			}
		case "exit":
			_ = writer.writeJSON(ServerMessage{Type: "output", Data: "ending session\r\n"})
			session.terminate()
			return
		case "resize":
			if msg.Cols > 0 && msg.Rows > 0 {
				if err := session.resize(msg.Cols, msg.Rows); err != nil {
					_ = writer.writeJSON(ServerMessage{Type: "error", Data: err.Error()})
					return
				}
			}
		case "ping":
			if err := writer.writeJSON(ServerMessage{Type: "pong"}); err != nil {
				return
			}
		default:
			if err := writer.writeJSON(ServerMessage{Type: "error", Data: "unsupported message type"}); err != nil {
				return
			}
		}
	}
}

type ptySession struct {
	command  []string
	terminal pty.Pty
	cancel   context.CancelFunc
	done     chan struct{}
	onDone   func(*ptySession)

	mu          sync.Mutex
	client      *websocketWriter
	buffer      []string
	ended       bool
	detachTimer *time.Timer
}

func newPTYSession(command []string, onDone func(*ptySession)) (*ptySession, error) {
	terminal, err := pty.New()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := terminal.CommandContext(ctx, command[0], command[1:]...)
	cmd.Env = os.Environ()

	if err := cmd.Start(); err != nil {
		cancel()
		_ = terminal.Close()
		return nil, err
	}

	session := &ptySession{
		command:  command,
		terminal: terminal,
		cancel:   cancel,
		done:     make(chan struct{}),
		onDone:   onDone,
		buffer:   []string{"started PTY shell: " + strings.Join(command, " ") + "\r\n"},
	}

	go session.streamOutput()
	go session.waitForExit(cmd)

	return session, nil
}

func (s *ptySession) attach(writer *websocketWriter) bool {
	s.mu.Lock()
	if s.ended || s.client != nil {
		s.mu.Unlock()
		return false
	}

	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}

	s.client = writer
	buffered := append([]string(nil), s.buffer...)
	s.buffer = nil
	s.mu.Unlock()

	for _, chunk := range buffered {
		if err := writer.writeJSON(ServerMessage{Type: "output", Data: chunk}); err != nil {
			return false
		}
	}
	return true
}

func (s *ptySession) detach(writer *websocketWriter, timeout time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.client != writer {
		return
	}
	s.client = nil

	if s.ended {
		return
	}
	if timeout <= 0 {
		go s.terminate()
		return
	}

	if s.detachTimer != nil {
		s.detachTimer.Stop()
	}
	s.detachTimer = time.AfterFunc(timeout, s.terminate)
}

func (s *ptySession) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ended
}

func (s *ptySession) writeInput(input string) error {
	_, err := io.WriteString(s.terminal, input)
	return err
}

func (s *ptySession) resize(cols int, rows int) error {
	return s.terminal.Resize(cols, rows)
}

func (s *ptySession) terminate() {
	s.cancel()
	_ = s.terminal.Close()
	select {
	case <-s.done:
	case <-time.After(2 * time.Second):
	}
}

func (s *ptySession) streamOutput() {
	buffer := make([]byte, 4096)
	for {
		n, err := s.terminal.Read(buffer)
		if n > 0 {
			s.deliverOutput(string(buffer[:n]))
		}
		if err != nil {
			return
		}
	}
}

func (s *ptySession) deliverOutput(chunk string) {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}

	client := s.client
	if client == nil {
		s.buffer = append(s.buffer, chunk)
		if len(s.buffer) > maxBufferedOutputChunks {
			s.buffer = s.buffer[len(s.buffer)-maxBufferedOutputChunks:]
		}
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	_ = client.writeJSON(ServerMessage{Type: "output", Data: chunk})
}

func (s *ptySession) waitForExit(cmd interface{ Wait() error }) {
	err := cmd.Wait()

	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}
	client := s.client
	s.client = nil
	s.mu.Unlock()

	if client != nil {
		if err != nil {
			_ = client.writeJSON(ServerMessage{Type: "exit", Data: err.Error()})
		} else {
			_ = client.writeJSON(ServerMessage{Type: "exit", Data: "process exited"})
		}
	}

	_ = s.terminal.Close()
	close(s.done)
	if s.onDone != nil {
		s.onDone(s)
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

func writeClose(conn *websocket.Conn) {
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "session ended"),
		time.Now().Add(time.Second),
	)
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
