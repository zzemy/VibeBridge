package server

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/zzemy/VibeBridge/internal/agentlog"
)

const (
	maxBufferedOutputBytes = 1024 * 1024
	bufferedOutputMaxAge   = 2 * time.Minute
	pongWait               = 5 * time.Minute
	pingPeriod             = 4 * time.Minute
)

type Config struct {
	SessionToken     string
	WebDir           string
	StaticFS         fs.FS
	Command          []string
	WorkingDirectory string
	Environment      []string
	ReconnectTimeout time.Duration
	IdleTimeout      time.Duration
	Logger           agentlog.Logger
}

type Server struct {
	config   Config
	upgrader websocket.Upgrader

	mu       sync.Mutex
	session  *ptySession
	clock    clock
	launcher terminalLauncher
	logger   agentlog.Logger
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

type SessionStatus struct {
	State                   string `json:"state"`
	StartedAt               string `json:"started_at,omitempty"`
	LastActivityAt          string `json:"last_activity_at,omitempty"`
	ReconnectTimeoutSeconds int64  `json:"reconnect_timeout_seconds"`
	IdleTimeoutSeconds      int64  `json:"idle_timeout_seconds"`
}

func New(config Config) *Server {
	if config.ReconnectTimeout <= 0 {
		config.ReconnectTimeout = 90 * time.Second
	}

	logger := config.Logger
	if logger == nil {
		logger = agentlog.Discard()
	}

	server := &Server{
		config:   config,
		clock:    systemClock{},
		launcher: ptyTerminalLauncher{},
		logger:   logger,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  1024,
			WriteBufferSize: 1024,
		},
	}
	server.upgrader.CheckOrigin = server.sameOrigin
	return server
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.handleHealthz)
	mux.HandleFunc("/status", s.handleStatus)
	mux.HandleFunc("/ws", s.handleWebSocket)
	mux.HandleFunc("/", s.handleStatic)
	return mux
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if !s.validToken(r) {
		writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "invalid session token"})
		return
	}
	writeJSON(w, http.StatusOK, s.sessionStatus())
}

func (s *Server) sessionStatus() SessionStatus {
	status := SessionStatus{
		State:                   "idle",
		ReconnectTimeoutSeconds: int64(s.config.ReconnectTimeout.Seconds()),
		IdleTimeoutSeconds:      int64(s.config.IdleTimeout.Seconds()),
	}

	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session == nil {
		return status
	}

	session.mu.Lock()
	defer session.mu.Unlock()
	status.StartedAt = session.startedAt.UTC().Format(time.RFC3339)
	status.LastActivityAt = session.lastActivityAt.UTC().Format(time.RFC3339)
	status.State = session.lifecycle.publicState()
	return status
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
	_ = conn.SetReadDeadline(time.Now().Add(pongWait))
	conn.SetPongHandler(func(string) error {
		return conn.SetReadDeadline(time.Now().Add(pongWait))
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
	defer s.keepConnectionAlive(writer)()

	s.readClientMessages(session, writer, conn)
	writeClose(conn)
}

func (s *Server) sameOrigin(r *http.Request) bool {
	origin := r.Header.Get("Origin")
	if origin == "" {
		// Non-browser WebSocket clients do not send Origin headers.
		return true
	}

	parsed, err := url.Parse(origin)
	return err == nil && parsed.Host == r.Host
}

func (s *Server) keepConnectionAlive(writer *websocketWriter) func() {
	return keepConnectionAlive(writer, pingPeriod)
}

func keepConnectionAlive(writer *websocketWriter, interval time.Duration) func() {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				if err := writer.writePing(); err != nil {
					return
				}
			}
		}
	}()

	return func() { close(done) }
}

// Close ends the active PTY before the HTTP server shuts down.
func (s *Server) Close() {
	s.mu.Lock()
	session := s.session
	s.mu.Unlock()
	if session != nil {
		session.terminateWithReason(agentlog.ReasonAgentShutdown)
	}
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

	correlationID, err := newSessionCorrelationID()
	if err != nil {
		return nil, fmt.Errorf("create session correlation ID: %w", err)
	}
	session, err := newPTYSession(
		terminalLaunchRequest{Command: s.config.Command, WorkingDirectory: s.config.WorkingDirectory, Environment: s.config.Environment},
		s.config.IdleTimeout,
		s.clock,
		s.launcher,
		s.clearSession,
		sessionTelemetry{correlationID: correlationID, logger: s.logger},
	)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil && !s.session.isEnded() {
		session.terminateWithReason(agentlog.ReasonSuperseded)
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
			_ = writer.writeBinary([]byte("ending session\r\n"))
			session.terminateWithReason(agentlog.ReasonExplicitEnd)
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
	command     []string
	terminal    terminal
	processTree processTree
	cancel      func()
	done        chan struct{}
	onDone      func(*ptySession)

	mu                 sync.Mutex
	client             *websocketWriter
	replay             replayBuffer
	lifecycle          sessionLifecycle
	detachTimer        timer
	idleTimeout        time.Duration
	idleTimer          timer
	startedAt          time.Time
	lastActivityAt     time.Time
	clock              clock
	resourcesCloseOnce sync.Once
	resourcesCloseErr  error
	telemetry          sessionTelemetry
	endReason          agentlog.Reason
}

func newPTYSession(request terminalLaunchRequest, idleTimeout time.Duration, sessionClock clock, launcher terminalLauncher, onDone func(*ptySession), telemetry sessionTelemetry) (*ptySession, error) {
	if sessionClock == nil {
		sessionClock = systemClock{}
	}
	if launcher == nil {
		launcher = ptyTerminalLauncher{}
	}
	launched, err := launcher.Start(request)
	if err != nil {
		return nil, err
	}

	now := sessionClock.Now()
	session := &ptySession{
		command:        request.Command,
		terminal:       launched.terminal,
		processTree:    launched.processTree,
		cancel:         launched.cancel,
		done:           make(chan struct{}),
		onDone:         onDone,
		replay:         newReplayBuffer(maxBufferedOutputBytes, bufferedOutputMaxAge, sessionClock.Now),
		lifecycle:      newSessionLifecycle(),
		idleTimeout:    idleTimeout,
		startedAt:      now,
		lastActivityAt: now,
		clock:          sessionClock,
		telemetry:      telemetry,
	}
	session.lifecycle.started()
	session.logEvent(agentlog.EventSessionStarted, agentlog.State(session.lifecycle.state), "", "")
	session.replay.append([]byte("started PTY shell: " + strings.Join(request.Command, " ") + "\r\n"))
	session.resetIdleTimer()

	go session.streamOutput()
	go session.waitForExit(launched.waiter)

	return session, nil
}

func (s *ptySession) attach(writer *websocketWriter) bool {
	s.mu.Lock()
	if s.client != nil || !s.lifecycle.attach() {
		s.mu.Unlock()
		return false
	}

	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}

	s.client = writer
	s.lastActivityAt = s.clock.Now()
	s.resetIdleTimerLocked()
	buffered := s.replay.drain()
	s.logEvent(agentlog.EventSessionAttached, agentlog.State(s.lifecycle.state), "", "")
	s.mu.Unlock()

	for _, chunk := range buffered {
		if err := writer.writeBinary(chunk); err != nil {
			return false
		}
	}
	return true
}

func (s *ptySession) detach(writer *websocketWriter, timeout time.Duration) {
	s.mu.Lock()
	if s.client != writer {
		s.mu.Unlock()
		return
	}
	s.client = nil
	if !s.lifecycle.detach() {
		s.mu.Unlock()
		return
	}
	s.logEvent(agentlog.EventSessionDetached, agentlog.State(s.lifecycle.state), "", "")
	if timeout <= 0 {
		s.mu.Unlock()
		go s.terminateWithReason(agentlog.ReasonReconnectExpired)
		return
	}

	if s.detachTimer != nil {
		s.detachTimer.Stop()
	}
	s.detachTimer = s.clock.AfterFunc(timeout, func() {
		s.terminateWithReason(agentlog.ReasonReconnectExpired)
	})
	s.mu.Unlock()
}

func (s *ptySession) isEnded() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lifecycle.done()
}

func (s *ptySession) writeInput(input string) error {
	_, err := io.WriteString(s.terminal, input)
	if err == nil {
		s.touchActivity()
		s.resetIdleTimer()
	}
	return err
}

func (s *ptySession) resize(cols int, rows int) error {
	err := s.terminal.Resize(cols, rows)
	if err == nil {
		s.touchActivity()
		s.resetIdleTimer()
	}
	return err
}

func (s *ptySession) terminateWithReason(reason agentlog.Reason) {
	s.mu.Lock()
	if !s.lifecycle.beginEnding() {
		s.mu.Unlock()
		return
	}
	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	s.endReason = reason
	s.logEvent(agentlog.EventSessionEnding, agentlog.State(s.lifecycle.state), reason, "")
	s.mu.Unlock()

	s.cancel()
	_ = s.closeResources()
	select {
	case <-s.done:
	case <-s.clock.After(2 * time.Second):
	}
}

func (s *ptySession) closeResources() error {
	s.resourcesCloseOnce.Do(func() {
		var processTreeErr error
		if s.processTree != nil {
			processTreeErr = s.processTree.Close()
		}
		s.resourcesCloseErr = errors.Join(processTreeErr, s.terminal.Close())
	})
	return s.resourcesCloseErr
}

func (s *ptySession) streamOutput() {
	buffer := make([]byte, 4096)
	for {
		n, err := s.terminal.Read(buffer)
		if n > 0 {
			s.deliverOutput(append([]byte(nil), buffer[:n]...))
		}
		if err != nil {
			return
		}
	}
}

func (s *ptySession) deliverOutput(chunk []byte) {
	s.mu.Lock()
	if !s.lifecycle.acceptsOutput() {
		s.mu.Unlock()
		return
	}

	s.lastActivityAt = s.clock.Now()
	client := s.client
	if client == nil {
		s.replay.append(chunk)
		s.mu.Unlock()
		return
	}
	s.mu.Unlock()

	_ = client.writeBinary(chunk)
}

func (s *ptySession) touchActivity() {
	s.mu.Lock()
	s.lastActivityAt = s.clock.Now()
	s.mu.Unlock()
}

func (s *ptySession) waitForExit(cmd interface{ Wait() error }) {
	err := cmd.Wait()

	s.mu.Lock()
	if !s.lifecycle.finish(err) {
		s.mu.Unlock()
		return
	}
	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}
	if s.idleTimer != nil {
		s.idleTimer.Stop()
		s.idleTimer = nil
	}
	client := s.client
	s.client = nil
	reason := s.endReason
	if reason == "" {
		reason = agentlog.ReasonProcessExit
	}
	outcome := agentlog.OutcomeSuccess
	if s.lifecycle.state == sessionStateFailed {
		outcome = agentlog.OutcomeFailure
	}
	s.logEvent(agentlog.EventSessionEnded, agentlog.State(s.lifecycle.state), reason, outcome)
	s.mu.Unlock()

	if client != nil {
		if err != nil {
			_ = client.writeJSON(ServerMessage{Type: "exit", Data: err.Error()})
		} else {
			_ = client.writeJSON(ServerMessage{Type: "exit", Data: "process exited"})
		}
		client.close()
	}

	_ = s.closeResources()
	close(s.done)
	if s.onDone != nil {
		s.onDone(s)
	}
}

func (s *ptySession) resetIdleTimer() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resetIdleTimerLocked()
}

func (s *ptySession) resetIdleTimerLocked() {
	if s.idleTimeout <= 0 || s.lifecycle.done() || s.lifecycle.state == sessionStateEnding {
		return
	}
	if s.idleTimer == nil {
		s.idleTimer = s.clock.AfterFunc(s.idleTimeout, s.expireIdle)
		return
	}
	s.idleTimer.Reset(s.idleTimeout)
}

func (s *ptySession) expireIdle() {
	s.deliverOutput([]byte("idle timeout reached; ending session\r\n"))
	s.terminateWithReason(agentlog.ReasonIdleTimeout)
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

func (w *websocketWriter) writeBinary(value []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteMessage(websocket.BinaryMessage, value)
}

func (w *websocketWriter) writePing() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteControl(websocket.PingMessage, nil, time.Now().Add(time.Second))
}

func (w *websocketWriter) close() {
	w.mu.Lock()
	defer w.mu.Unlock()
	writeClose(w.conn)
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
		s.handleEmbeddedStatic(w, r)
		return
	}

	indexPath := filepath.Join(s.config.WebDir, "index.html")
	if _, err := os.Stat(indexPath); errors.Is(err, os.ErrNotExist) {
		s.handleEmbeddedStatic(w, r)
		return
	}

	fileServer := http.FileServer(http.Dir(s.config.WebDir))
	fileServer.ServeHTTP(w, r)
}

func (s *Server) handleEmbeddedStatic(w http.ResponseWriter, r *http.Request) {
	if s.config.StaticFS == nil {
		writeFallback(w)
		return
	}

	fileServer := http.FileServer(http.FS(s.config.StaticFS))
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
