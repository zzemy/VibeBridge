package server

import (
	"bytes"
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
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/agentlog"
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
	"github.com/zzemy/VibeBridge/internal/workspace"
	"google.golang.org/protobuf/proto"
)

const (
	maxBufferedOutputBytes = 1024 * 1024
	bufferedOutputMaxAge   = 2 * time.Minute
	pongWait               = 5 * time.Minute
	pingPeriod             = 4 * time.Minute
	protocolHelloTimeout   = 5 * time.Second
)

type Config struct {
	SessionToken          string
	WebDir                string
	StaticFS              fs.FS
	Command               []string
	WorkingDirectory      string
	WorkspaceRoot         string
	Environment           []string
	ReconnectTimeout      time.Duration
	IdleTimeout           time.Duration
	DisableLegacyProtocol bool
	Logger                agentlog.Logger
}

type Server struct {
	config   Config
	upgrader websocket.Upgrader

	mu                    sync.Mutex
	session               *ptySession
	nextSessionGeneration uint64
	clock                 clock
	launcher              terminalLauncher
	logger                agentlog.Logger
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
			Subprotocols:    []string{protocolv1.WebSocketSubprotocol},
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
	if s.config.DisableLegacyProtocol && !offersWebSocketSubprotocol(r, protocolv1.WebSocketSubprotocol) {
		w.Header().Set("Upgrade", "websocket")
		writeJSON(w, http.StatusUpgradeRequired, map[string]string{"error": "Protocol V1 is required"})
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

	writer := &websocketWriter{conn: conn, now: s.clock.Now}
	if conn.Subprotocol() == protocolv1.WebSocketSubprotocol {
		if err := s.negotiateProtocolV1(writer, conn); err != nil {
			writeProtocolClose(conn, "Protocol V1 negotiation failed")
			return
		}
	}

	var attachRequest protocolv1.ClientStreamMessage
	if writer.usesSessionResume() {
		attachRequest, err = s.readProtocolV1Attach(writer, conn)
		if err != nil {
			writer.closeProtocol("Invalid Protocol V1 AttachSession")
			return
		}
	}

	session, created, err := s.getOrCreateSession()
	if err != nil {
		_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED)
		writeClose(conn)
		return
	}

	attached := false
	if writer.usesSessionResume() {
		attached, err = session.attachProtocolV1(writer, attachRequest, created)
	} else {
		attached = session.attach(writer)
	}
	if !attached {
		_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_SESSION_ALREADY_ACTIVE)
		writeClose(conn)
		return
	}
	defer session.detach(writer, s.config.ReconnectTimeout)
	if err != nil {
		writer.closeProtocol("Protocol V1 session attachment failed")
		return
	}
	defer s.keepConnectionAlive(writer)()

	s.readClientMessages(session, writer, conn)
	writeClose(conn)
}

func offersWebSocketSubprotocol(r *http.Request, want string) bool {
	for _, offered := range websocket.Subprotocols(r) {
		if offered == want {
			return true
		}
	}
	return false
}

func (s *Server) negotiateProtocolV1(writer *websocketWriter, conn *websocket.Conn) error {
	conn.SetReadLimit(int64(protocolv1.MaxEnvelopeBytes))
	_ = conn.SetReadDeadline(time.Now().Add(protocolHelloTimeout))
	messageType, encoded, err := conn.ReadMessage()
	if err != nil {
		return fmt.Errorf("read client Hello: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return errors.New("client Hello must use a binary WebSocket message")
	}

	negotiated, err := protocolv1.AcceptClientHello(encoded)
	if err != nil {
		return err
	}
	if s.config.DisableLegacyProtocol {
		if err := requireCurrentClientCapabilities(negotiated); err != nil {
			return err
		}
	} else if !negotiated.HasCapability(protocolv1.CapabilityTerminalBinaryOutput) {
		return fmt.Errorf("client does not support required capability %q", protocolv1.CapabilityTerminalBinaryOutput)
	}
	response, err := protocolv1.NewAgentHello(negotiated.ConnectionID, negotiated.Major, negotiated.Minor, s.clock.Now())
	if err != nil {
		return err
	}
	responseBytes, err := proto.MarshalOptions{Deterministic: true}.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode Agent Hello: %w", err)
	}
	if len(responseBytes) > int(negotiated.PeerMaxEnvelopeBytes) {
		return errors.New("Agent Hello exceeds the client envelope limit")
	}
	if err := writer.writeBinary(responseBytes); err != nil {
		return fmt.Errorf("write Agent Hello: %w", err)
	}
	if negotiated.HasCapability(protocolv1.CapabilityTerminalSequencedIO) {
		stream, err := protocolv1.NewAgentStream(negotiated)
		if err != nil {
			return fmt.Errorf("initialize Protocol V1 stream: %w", err)
		}
		writer.enableProtocolV1(stream)
	}
	return conn.SetReadDeadline(time.Now().Add(pongWait))
}

func requireCurrentClientCapabilities(negotiated protocolv1.NegotiatedHello) error {
	required := [...]string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilityTerminalResizeEnd,
		protocolv1.CapabilitySessionProcessExit,
		protocolv1.CapabilitySessionResume,
		protocolv1.CapabilityControlError,
		protocolv1.CapabilityControlHealth,
	}
	for _, capability := range required {
		if !negotiated.HasCapability(capability) {
			return fmt.Errorf("client does not support required capability %q", capability)
		}
	}
	return nil
}

func (s *Server) readProtocolV1Attach(writer *websocketWriter, conn *websocket.Conn) (protocolv1.ClientStreamMessage, error) {
	_ = conn.SetReadDeadline(time.Now().Add(protocolHelloTimeout))
	messageType, encoded, err := conn.ReadMessage()
	if err != nil {
		return protocolv1.ClientStreamMessage{}, fmt.Errorf("read AttachSession: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return protocolv1.ClientStreamMessage{}, errors.New("AttachSession must use a binary WebSocket message")
	}
	message, err := writer.decodeProtocolV1ClientMessage(encoded)
	if err != nil {
		return protocolv1.ClientStreamMessage{}, err
	}
	if message.Kind != protocolv1.ClientStreamMessageAttachSession {
		return protocolv1.ClientStreamMessage{}, errors.New("first stream message must contain AttachSession")
	}
	if err := conn.SetReadDeadline(time.Now().Add(pongWait)); err != nil {
		return protocolv1.ClientStreamMessage{}, err
	}
	return message, nil
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

func (s *Server) getOrCreateSession() (*ptySession, bool, error) {
	if len(s.config.Command) == 0 {
		return nil, false, errors.New("no command configured")
	}

	s.mu.Lock()
	current := s.session
	s.mu.Unlock()

	if current != nil && !current.isEnded() {
		return current, false, nil
	}

	workingDirectory := s.config.WorkingDirectory
	if s.config.WorkspaceRoot != "" {
		revalidatedDirectory, err := workspace.RevalidateDirectory(s.config.WorkspaceRoot, workingDirectory)
		if err != nil {
			return nil, false, fmt.Errorf("revalidate workspace launch directory: %w", err)
		}
		workingDirectory = revalidatedDirectory
	}

	correlationID, err := newSessionCorrelationID()
	if err != nil {
		return nil, false, fmt.Errorf("create session correlation ID: %w", err)
	}
	protocolSessionID, err := newProtocolSessionID()
	if err != nil {
		return nil, false, fmt.Errorf("create protocol session ID: %w", err)
	}
	session, err := newPTYSession(
		terminalLaunchRequest{Command: s.config.Command, WorkingDirectory: workingDirectory, Environment: s.config.Environment},
		s.config.IdleTimeout,
		s.clock,
		s.launcher,
		s.clearSession,
		sessionTelemetry{correlationID: correlationID, logger: s.logger},
	)
	if err != nil {
		return nil, false, err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	if s.session != nil && !s.session.isEnded() {
		session.terminateWithReason(agentlog.ReasonSuperseded)
		return s.session, false, nil
	}
	if s.nextSessionGeneration == ^uint64(0) {
		session.terminateWithReason(agentlog.ReasonSuperseded)
		return nil, false, errors.New("session generation exhausted")
	}
	s.nextSessionGeneration++
	session.sessionID = protocolSessionID
	session.generation = s.nextSessionGeneration
	s.session = session
	return session, true, nil
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
		if writer.usesProtocolV1() {
			messageType, encoded, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if messageType == websocket.BinaryMessage {
				streamMessage, err := writer.decodeProtocolV1ClientMessage(encoded)
				if err != nil {
					writer.closeProtocol("Invalid Protocol V1 message")
					return
				}
				switch streamMessage.Kind {
				case protocolv1.ClientStreamMessageTerminalInput:
					if err := session.writeInputBytes(streamMessage.Data); err != nil {
						_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED)
						return
					}
					if err := writer.commitProtocolV1ClientMessage(streamMessage, true); err != nil {
						return
					}
				case protocolv1.ClientStreamMessageTerminalResize:
					if err := session.resize(int(streamMessage.Columns), int(streamMessage.Rows)); err != nil {
						_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_RESIZE_FAILED)
						return
					}
					if err := writer.commitProtocolV1ClientMessage(streamMessage, true); err != nil {
						return
					}
				case protocolv1.ClientStreamMessageEndSession:
					if err := writer.commitProtocolV1ClientMessage(streamMessage, false); err != nil {
						return
					}
					_ = writer.writeBinary([]byte("ending session\r\n"))
					session.terminateWithReason(agentlog.ReasonExplicitEnd)
					return
				case protocolv1.ClientStreamMessageAcknowledgement:
					if err := writer.commitProtocolV1ClientMessage(streamMessage, false); err != nil {
						return
					}
				case protocolv1.ClientStreamMessagePing:
					if err := writer.respondProtocolV1Ping(streamMessage); err != nil {
						return
					}
				default:
					writer.closeProtocol("Unsupported Protocol V1 message")
					return
				}
				continue
			}
			if messageType != websocket.TextMessage || json.Unmarshal(encoded, &msg) != nil || msg.Type == "input" {
				writer.closeProtocol("Invalid transitional control message")
				return
			}
			if writer.usesTerminalResizeEnd() && (msg.Type == "resize" || msg.Type == "exit") {
				writer.closeProtocol("Negotiated resize/end controls must use Protocol V1 envelopes")
				return
			}
			if writer.usesControlHealth() && msg.Type == "ping" {
				writer.closeProtocol("Negotiated health checks must use Protocol V1 envelopes")
				return
			}
		} else if err := conn.ReadJSON(&msg); err != nil {
			return
		}

		switch msg.Type {
		case "input":
			if err := session.writeInput(msg.Data); err != nil {
				_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED)
				return
			}
		case "exit":
			_ = writer.writeBinary([]byte("ending session\r\n"))
			session.terminateWithReason(agentlog.ReasonExplicitEnd)
			return
		case "resize":
			if msg.Cols > 0 && msg.Cols <= protocolv1.MaxTerminalDimension && msg.Rows > 0 && msg.Rows <= protocolv1.MaxTerminalDimension {
				if err := session.resize(msg.Cols, msg.Rows); err != nil {
					_ = writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_RESIZE_FAILED)
					return
				}
			}
		case "ping":
			if err := writer.writeJSON(ServerMessage{Type: "pong"}); err != nil {
				return
			}
		default:
			if err := writer.writeError(vibebridgev1.ErrorCode_ERROR_CODE_UNSUPPORTED_MESSAGE); err != nil {
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

	outputMu           sync.Mutex
	mu                 sync.Mutex
	client             *websocketWriter
	replay             replayBuffer
	sessionID          []byte
	generation         uint64
	resumeCheckpoint   bool
	lastDetachedOutput uint64
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
	s.outputMu.Lock()
	defer s.outputMu.Unlock()

	s.mu.Lock()
	if !s.beginAttachLocked(writer) {
		s.mu.Unlock()
		return false
	}
	buffered := s.replay.drain()
	s.mu.Unlock()

	for _, chunk := range buffered {
		if err := writer.writeBinary(chunk); err != nil {
			return false
		}
	}
	return true
}

func (s *ptySession) attachProtocolV1(writer *websocketWriter, request protocolv1.ClientStreamMessage, created bool) (bool, error) {
	s.outputMu.Lock()
	defer s.outputMu.Unlock()

	s.mu.Lock()
	if !s.beginAttachLocked(writer) {
		s.mu.Unlock()
		return false, nil
	}
	buffered, replayComplete := s.replay.drainWithStatus()
	disposition := s.resumeDispositionLocked(request, created, replayComplete)
	sessionID := append([]byte(nil), s.sessionID...)
	generation := s.generation
	s.mu.Unlock()

	if err := writer.commitProtocolV1Attach(request, sessionID, generation, disposition); err != nil {
		return true, err
	}
	for _, chunk := range buffered {
		if err := writer.writeBinary(chunk); err != nil {
			return true, err
		}
	}
	return true, nil
}

func (s *ptySession) beginAttachLocked(writer *websocketWriter) bool {
	if s.client != nil || !s.lifecycle.attach() {
		return false
	}
	if s.detachTimer != nil {
		s.detachTimer.Stop()
		s.detachTimer = nil
	}
	s.client = writer
	s.lastActivityAt = s.clock.Now()
	s.resetIdleTimerLocked()
	s.logEvent(agentlog.EventSessionAttached, agentlog.State(s.lifecycle.state), "", "")
	return true
}

func (s *ptySession) resumeDispositionLocked(request protocolv1.ClientStreamMessage, created, replayComplete bool) vibebridgev1.ResumeDisposition {
	fresh := len(request.SessionID) == 0 && request.SessionGeneration == 0 && request.LastAcknowledgedSequence == 0
	if created && fresh {
		return vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_FRESH
	}
	identityMatches := bytes.Equal(request.SessionID, s.sessionID) && request.SessionGeneration == s.generation
	if !created && identityMatches && s.resumeCheckpoint && replayComplete && request.LastAcknowledgedSequence == s.lastDetachedOutput {
		return vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED
	}
	return vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED
}

func (s *ptySession) detach(writer *websocketWriter, timeout time.Duration) {
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
	s.mu.Lock()
	if s.client != writer {
		s.mu.Unlock()
		return
	}
	s.client = nil
	if writer.usesSessionResume() {
		s.resumeCheckpoint = true
		s.lastDetachedOutput = writer.highestProtocolV1OutboundSequence()
	} else {
		s.resumeCheckpoint = false
		s.lastDetachedOutput = 0
	}
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
	return s.writeInputBytes([]byte(input))
}

func (s *ptySession) writeInputBytes(input []byte) error {
	for len(input) > 0 {
		written, err := s.terminal.Write(input)
		if err != nil {
			return err
		}
		if written <= 0 || written > len(input) {
			return io.ErrShortWrite
		}
		input = input[written:]
	}
	s.touchActivity()
	s.resetIdleTimer()
	return nil
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
	s.outputMu.Lock()
	defer s.outputMu.Unlock()
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

	s.outputMu.Lock()
	s.mu.Lock()
	if !s.lifecycle.finish(err) {
		s.mu.Unlock()
		s.outputMu.Unlock()
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
	processExitOutcome := vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_SUCCESS
	if s.lifecycle.state == sessionStateFailed {
		outcome = agentlog.OutcomeFailure
		processExitOutcome = vibebridgev1.ProcessExitOutcome_PROCESS_EXIT_OUTCOME_FAILURE
	}
	s.logEvent(agentlog.EventSessionEnded, agentlog.State(s.lifecycle.state), reason, outcome)
	s.mu.Unlock()

	if client != nil {
		legacyMessage := "process exited"
		if err != nil {
			legacyMessage = err.Error()
		}
		_ = client.writeProcessExit(processExitOutcome, legacyMessage)
		client.close()
	}
	s.outputMu.Unlock()

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
	conn       *websocket.Conn
	mu         sync.Mutex
	now        func() time.Time
	protocolV1 *protocolv1.AgentStream
}

func (w *websocketWriter) writeJSON(value ServerMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.conn.WriteJSON(value)
}

func (w *websocketWriter) writeError(code vibebridgev1.ErrorCode) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.protocolV1 == nil || !w.protocolV1.UsesControlError() {
		message, err := stableErrorMessage(code)
		if err != nil {
			return err
		}
		return w.conn.WriteJSON(ServerMessage{Type: "error", Data: message})
	}
	encoded, err := w.protocolV1.EncodeError(code, w.currentTime())
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, encoded)
}

func stableErrorMessage(code vibebridgev1.ErrorCode) (string, error) {
	switch code {
	case vibebridgev1.ErrorCode_ERROR_CODE_SESSION_START_FAILED:
		return "could not start terminal session", nil
	case vibebridgev1.ErrorCode_ERROR_CODE_SESSION_ALREADY_ACTIVE:
		return "session already active", nil
	case vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_INPUT_FAILED:
		return "could not write terminal input", nil
	case vibebridgev1.ErrorCode_ERROR_CODE_TERMINAL_RESIZE_FAILED:
		return "could not resize terminal", nil
	case vibebridgev1.ErrorCode_ERROR_CODE_UNSUPPORTED_MESSAGE:
		return "unsupported message type", nil
	default:
		return "", errors.New("stable error code is invalid")
	}
}

func (w *websocketWriter) writeProcessExit(outcome vibebridgev1.ProcessExitOutcome, legacyMessage string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.protocolV1 == nil || !w.protocolV1.UsesSessionProcessExit() {
		return w.conn.WriteJSON(ServerMessage{Type: "exit", Data: legacyMessage})
	}
	encoded, err := w.protocolV1.EncodeProcessExit(outcome, w.currentTime())
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, encoded)
}

func (w *websocketWriter) writeBinary(value []byte) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.protocolV1 == nil {
		return w.conn.WriteMessage(websocket.BinaryMessage, value)
	}
	for len(value) > 0 {
		encoded, consumed, err := w.protocolV1.EncodeTerminalOutputChunk(value, w.currentTime())
		if err != nil {
			return err
		}
		if err := w.conn.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
			return err
		}
		value = value[consumed:]
	}
	return nil
}

func (w *websocketWriter) enableProtocolV1(stream *protocolv1.AgentStream) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.protocolV1 = stream
}

func (w *websocketWriter) usesSessionResume() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protocolV1 != nil && w.protocolV1.UsesSessionResume()
}

func (w *websocketWriter) usesControlHealth() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protocolV1 != nil && w.protocolV1.UsesControlHealth()
}

func (w *websocketWriter) usesTerminalResizeEnd() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protocolV1 != nil && w.protocolV1.UsesTerminalResizeEnd()
}

func (w *websocketWriter) usesProtocolV1() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protocolV1 != nil
}

func (w *websocketWriter) decodeProtocolV1ClientMessage(encoded []byte) (protocolv1.ClientStreamMessage, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.protocolV1.DecodeClientMessage(encoded)
}

func (w *websocketWriter) commitProtocolV1Attach(message protocolv1.ClientStreamMessage, sessionID []byte, generation uint64, disposition vibebridgev1.ResumeDisposition) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.protocolV1.CommitClientMessage(message); err != nil {
		return err
	}
	if err := w.protocolV1.BindSession(sessionID, generation); err != nil {
		return err
	}
	encoded, err := w.protocolV1.EncodeSessionStatus(disposition, w.currentTime())
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, encoded)
}

func (w *websocketWriter) highestProtocolV1OutboundSequence() uint64 {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.protocolV1 == nil {
		return 0
	}
	return w.protocolV1.HighestOutboundSequence()
}

func (w *websocketWriter) respondProtocolV1Ping(message protocolv1.ClientStreamMessage) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.protocolV1.CommitClientMessage(message); err != nil {
		return err
	}
	encoded, err := w.protocolV1.EncodePong(w.currentTime())
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, encoded)
}

func (w *websocketWriter) commitProtocolV1ClientMessage(message protocolv1.ClientStreamMessage, sendAcknowledgement bool) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if err := w.protocolV1.CommitClientMessage(message); err != nil {
		return err
	}
	if !sendAcknowledgement {
		return nil
	}
	encoded, err := w.protocolV1.EncodeAcknowledgement(w.currentTime())
	if err != nil {
		return err
	}
	return w.conn.WriteMessage(websocket.BinaryMessage, encoded)
}

func (w *websocketWriter) closeProtocol(message string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	writeProtocolClose(w.conn, message)
}

func (w *websocketWriter) currentTime() time.Time {
	if w.now == nil {
		return time.Now()
	}
	return w.now()
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

func writeProtocolClose(conn *websocket.Conn, message string) {
	_ = conn.WriteControl(
		websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseProtocolError, message),
		time.Now().Add(time.Second),
	)
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
