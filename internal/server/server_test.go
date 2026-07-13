package server

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/agentlog"
	protocolv1 "github.com/zzemy/VibeBridge/internal/protocol"
	"google.golang.org/protobuf/proto"
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

	session.terminateWithReason(agentlog.ReasonAgentShutdown)
	session.terminateWithReason(agentlog.ReasonAgentShutdown)

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

func (p *countingPTY) Close() error                   { p.closeCalls++; return nil }
func (p *countingPTY) Read([]byte) (int, error)       { return 0, io.EOF }
func (p *countingPTY) Resize(int, int) error          { return nil }
func (p *countingPTY) Write(data []byte) (int, error) { return len(data), nil }

func TestProtocolV1NegotiatesBeforeStartingSession(t *testing.T) {
	wait := make(chan struct{})
	terminal := &recordingPTY{writes: make(chan []byte, 1)}
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    terminal,
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
	app.launcher = launcher
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	connection := dialProtocolV1(t, testServer.URL)
	defer connection.Close()
	if launcher.calls.Load() != 0 {
		t.Fatalf("terminal launch calls before Hello = %d, want 0", launcher.calls.Load())
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, []string{protocolv1.CapabilityTerminalBinaryOutput})); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}

	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read Agent Hello: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("Agent Hello message type = %d, want binary", messageType)
	}
	envelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, envelope); err != nil {
		t.Fatalf("decode Agent Hello: %v", err)
	}
	if envelope.GetHello().GetPeerRole() != vibebridgev1.PeerRole_PEER_ROLE_AGENT {
		t.Fatalf("Agent Hello peer role = %v, want Agent", envelope.GetHello().GetPeerRole())
	}

	messageType, legacyOutput, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read legacy output after negotiated Hello: %v", err)
	}
	if messageType != websocket.BinaryMessage || !strings.Contains(string(legacyOutput), "started PTY shell") {
		t.Fatalf("legacy output type/data = %d/%q", messageType, legacyOutput)
	}
	if err := connection.WriteJSON(ClientMessage{Type: "input", Data: "legacy\r"}); err != nil {
		t.Fatalf("send legacy input after negotiated Hello: %v", err)
	}
	select {
	case written := <-terminal.writes:
		if string(written) != "legacy\r" {
			t.Fatalf("legacy PTY input = %q, want legacy\\r", written)
		}
	case <-time.After(time.Second):
		t.Fatal("legacy terminal input was not written to the PTY")
	}

	deadline := time.Now().Add(time.Second)
	for launcher.calls.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if launcher.calls.Load() != 1 {
		t.Fatalf("terminal launch calls after Hello = %d, want 1", launcher.calls.Load())
	}
	connection.Close()
	app.Close()
	close(wait)
}

func TestProtocolV1SequencesTerminalInputOutputAndAcknowledgement(t *testing.T) {
	wait := make(chan struct{})
	terminal := &recordingPTY{writes: make(chan []byte, 1)}
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    terminal,
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
	app.launcher = launcher
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	connection := dialProtocolV1(t, testServer.URL)
	capabilities := []string{protocolv1.CapabilityTerminalBinaryOutput, protocolv1.CapabilityTerminalSequencedIO}
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, connection) // Agent Hello.

	output := readProtocolEnvelope(t, connection)
	if output.Sequence != 2 || output.Acknowledge != 1 {
		t.Fatalf("terminal output sequence/ack = %d/%d, want 2/1", output.Sequence, output.Acknowledge)
	}
	if output.GetTerminalOutput() == nil {
		t.Fatalf("first stream payload = %T, want terminal output", output.Payload)
	}

	input := &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId:  []byte("0123456789abcdef"),
		Sequence:      2,
		Acknowledge:   2,
		Payload: &vibebridgev1.Envelope_TerminalInput{TerminalInput: &vibebridgev1.TerminalInput{
			Data: []byte("continue\r"),
		}},
	}
	encoded, err := proto.Marshal(input)
	if err != nil {
		t.Fatalf("marshal terminal input: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		t.Fatalf("send terminal input: %v", err)
	}

	select {
	case written := <-terminal.writes:
		if string(written) != "continue\r" {
			t.Fatalf("PTY input = %q, want continue\\r", written)
		}
	case <-time.After(time.Second):
		t.Fatal("terminal input was not written to the PTY")
	}

	acknowledgement := readProtocolEnvelope(t, connection)
	if acknowledgement.Sequence != 3 || acknowledgement.Acknowledge != 2 || acknowledgement.GetAcknowledgement() == nil {
		t.Fatalf("ack sequence/ack/payload = %d/%d/%T, want 3/2/Acknowledgement", acknowledgement.Sequence, acknowledgement.Acknowledge, acknowledgement.Payload)
	}

	connection.Close()
	close(wait)
	app.Close()
}

func TestProtocolV1ResumesDetachedSessionWithIdentityAndGeneration(t *testing.T) {
	wait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &recordingPTY{writes: make(chan []byte, 1)},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
	app.launcher = launcher
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()
	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilitySessionResume,
	}

	first := dialProtocolV1(t, testServer.URL)
	if err := first.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send first client Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, first)
	if err := first.WriteMessage(websocket.BinaryMessage, marshalClientAttach(t, nil, 0, 0)); err != nil {
		t.Fatalf("send fresh AttachSession: %v", err)
	}
	fresh := readProtocolEnvelope(t, first)
	if fresh.Sequence != 2 || fresh.Acknowledge != 2 || fresh.GetSessionStatus().GetResumeDisposition() != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_FRESH {
		t.Fatalf("fresh status sequence/ack/disposition = %d/%d/%v", fresh.Sequence, fresh.Acknowledge, fresh.GetSessionStatus().GetResumeDisposition())
	}
	if len(fresh.SessionId) != 16 || fresh.SessionGeneration == 0 {
		t.Fatalf("fresh session identity = %x/%d", fresh.SessionId, fresh.SessionGeneration)
	}
	initialOutput := readProtocolEnvelope(t, first)
	if initialOutput.Sequence != 3 || !strings.Contains(string(initialOutput.GetTerminalOutput().GetData()), "started PTY shell") {
		t.Fatalf("initial output sequence/data = %d/%q", initialOutput.Sequence, initialOutput.GetTerminalOutput().GetData())
	}
	if err := first.WriteMessage(websocket.BinaryMessage, marshalClientAcknowledgement(t, fresh.SessionId, fresh.SessionGeneration, 3, 3)); err != nil {
		t.Fatalf("acknowledge initial output: %v", err)
	}
	first.Close()
	waitForSessionState(t, app, sessionStateDetached)

	app.mu.Lock()
	session := app.session
	app.mu.Unlock()
	if session == nil {
		t.Fatal("session ended before resume")
	}
	session.deliverOutput([]byte("output while detached\r\n"))

	second := dialProtocolV1(t, testServer.URL)
	defer second.Close()
	if err := second.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send reconnect Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, second)
	if err := second.WriteMessage(websocket.BinaryMessage, marshalClientAttach(t, fresh.SessionId, fresh.SessionGeneration, 3)); err != nil {
		t.Fatalf("send resume AttachSession: %v", err)
	}
	resumed := readProtocolEnvelope(t, second)
	if resumed.GetSessionStatus().GetResumeDisposition() != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED {
		t.Fatalf("resume disposition = %v, want resumed", resumed.GetSessionStatus().GetResumeDisposition())
	}
	if !bytes.Equal(resumed.SessionId, fresh.SessionId) || resumed.SessionGeneration != fresh.SessionGeneration {
		t.Fatalf("resumed identity = %x/%d, want %x/%d", resumed.SessionId, resumed.SessionGeneration, fresh.SessionId, fresh.SessionGeneration)
	}
	replay := readProtocolEnvelope(t, second)
	if replay.Sequence != 3 || string(replay.GetTerminalOutput().GetData()) != "output while detached\r\n" {
		t.Fatalf("replayed output sequence/data = %d/%q", replay.Sequence, replay.GetTerminalOutput().GetData())
	}

	second.Close()
	close(wait)
	app.Close()
}

func TestProtocolV1ReportsResyncRequiredForStaleCursor(t *testing.T) {
	wait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &recordingPTY{writes: make(chan []byte, 1)},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
	app.launcher = launcher
	t.Cleanup(func() {
		close(wait)
		app.Close()
	})
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()
	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilitySessionResume,
	}

	first := dialProtocolV1(t, testServer.URL)
	if err := first.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send first client Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, first)
	if err := first.WriteMessage(websocket.BinaryMessage, marshalClientAttach(t, nil, 0, 0)); err != nil {
		t.Fatalf("send fresh AttachSession: %v", err)
	}
	fresh := readProtocolEnvelope(t, first)
	if fresh.GetSessionStatus().GetResumeDisposition() != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_FRESH {
		t.Fatalf("fresh disposition = %v, want fresh", fresh.GetSessionStatus().GetResumeDisposition())
	}
	initialOutput := readProtocolEnvelope(t, first)
	if initialOutput.Sequence != 3 {
		t.Fatalf("initial output sequence = %d, want 3", initialOutput.Sequence)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("close first connection: %v", err)
	}
	waitForSessionState(t, app, sessionStateDetached)

	app.mu.Lock()
	session := app.session
	app.mu.Unlock()
	if session == nil {
		t.Fatal("session ended before resynchronization")
	}
	session.deliverOutput([]byte("retained replay tail\r\n"))

	second := dialProtocolV1(t, testServer.URL)
	defer second.Close()
	if err := second.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send reconnect Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, second)
	if err := second.WriteMessage(websocket.BinaryMessage, marshalClientAttach(t, fresh.SessionId, fresh.SessionGeneration, 2)); err != nil {
		t.Fatalf("send stale resume AttachSession: %v", err)
	}
	status := readProtocolEnvelope(t, second)
	if status.Sequence != 2 || status.GetSessionStatus().GetResumeDisposition() != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED {
		t.Fatalf("status sequence/disposition = %d/%v, want 2/resync required", status.Sequence, status.GetSessionStatus().GetResumeDisposition())
	}
	replay := readProtocolEnvelope(t, second)
	if replay.Sequence != 3 || string(replay.GetTerminalOutput().GetData()) != "retained replay tail\r\n" {
		t.Fatalf("replay sequence/data = %d/%q, want 3/retained tail", replay.Sequence, replay.GetTerminalOutput().GetData())
	}
}

func TestResumeDispositionRequiresMatchingCompleteCheckpoint(t *testing.T) {
	sessionID := []byte("fedcba9876543210")
	baseSession := func() *ptySession {
		return &ptySession{
			sessionID:          append([]byte(nil), sessionID...),
			generation:         7,
			resumeCheckpoint:   true,
			lastDetachedOutput: 8,
		}
	}
	baseRequest := func() protocolv1.ClientStreamMessage {
		return protocolv1.ClientStreamMessage{
			Kind:                     protocolv1.ClientStreamMessageAttachSession,
			SessionID:                append([]byte(nil), sessionID...),
			SessionGeneration:        7,
			LastAcknowledgedSequence: 8,
		}
	}

	tests := []struct {
		name           string
		mutateSession  func(*ptySession)
		mutateRequest  func(*protocolv1.ClientStreamMessage)
		created        bool
		replayComplete bool
		want           vibebridgev1.ResumeDisposition
	}{
		{name: "matching checkpoint", replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESUMED},
		{name: "session ID mismatch", mutateRequest: func(request *protocolv1.ClientStreamMessage) { request.SessionID = []byte("0123456789abcdef") }, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "generation mismatch", mutateRequest: func(request *protocolv1.ClientStreamMessage) { request.SessionGeneration-- }, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "stale cursor", mutateRequest: func(request *protocolv1.ClientStreamMessage) { request.LastAcknowledgedSequence-- }, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "too-new cursor", mutateRequest: func(request *protocolv1.ClientStreamMessage) { request.LastAcknowledgedSequence++ }, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "truncated replay", replayComplete: false, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "missing checkpoint", mutateSession: func(session *ptySession) { session.resumeCheckpoint = false }, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
		{name: "stale identity after new PTY", created: true, replayComplete: true, want: vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			session := baseSession()
			request := baseRequest()
			if testCase.mutateSession != nil {
				testCase.mutateSession(session)
			}
			if testCase.mutateRequest != nil {
				testCase.mutateRequest(&request)
			}
			if got := session.resumeDispositionLocked(request, testCase.created, testCase.replayComplete); got != testCase.want {
				t.Fatalf("resume disposition = %v, want %v", got, testCase.want)
			}
		})
	}

	fresh := protocolv1.ClientStreamMessage{Kind: protocolv1.ClientStreamMessageAttachSession}
	if got := baseSession().resumeDispositionLocked(fresh, true, true); got != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_FRESH {
		t.Fatalf("new PTY fresh disposition = %v, want fresh", got)
	}
	if got := baseSession().resumeDispositionLocked(fresh, false, true); got != vibebridgev1.ResumeDisposition_RESUME_DISPOSITION_RESYNC_REQUIRED {
		t.Fatalf("existing PTY fresh disposition = %v, want resync required", got)
	}
}

func TestProtocolV1RejectsMissingAttachWithoutStartingSession(t *testing.T) {
	launcher := &fakeTerminalLauncher{}
	app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
	app.launcher = launcher
	testServer := httptest.NewServer(app.Handler())
	defer testServer.Close()

	connection := dialProtocolV1(t, testServer.URL)
	defer connection.Close()
	capabilities := []string{
		protocolv1.CapabilityTerminalBinaryOutput,
		protocolv1.CapabilityTerminalSequencedIO,
		protocolv1.CapabilitySessionResume,
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, marshalClientHello(t, 1, 0, capabilities)); err != nil {
		t.Fatalf("send client Hello: %v", err)
	}
	_ = readProtocolEnvelope(t, connection)
	invalid := &vibebridgev1.Envelope{
		ProtocolMajor: 1,
		ConnectionId:  []byte("0123456789abcdef"),
		Sequence:      2,
		Acknowledge:   1,
		Payload: &vibebridgev1.Envelope_TerminalInput{TerminalInput: &vibebridgev1.TerminalInput{
			Data: []byte("must not start"),
		}},
	}
	encoded, err := proto.Marshal(invalid)
	if err != nil {
		t.Fatalf("marshal invalid attachment: %v", err)
	}
	if err := connection.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
		t.Fatalf("send invalid attachment: %v", err)
	}
	if _, _, err := connection.ReadMessage(); err == nil {
		t.Fatal("connection remained open without AttachSession")
	}
	if launcher.calls.Load() != 0 {
		t.Fatalf("terminal launch calls = %d, want 0", launcher.calls.Load())
	}
}

func TestProtocolV1RejectsInvalidHelloWithoutStartingSession(t *testing.T) {
	tests := []struct {
		name         string
		major        uint32
		capabilities []string
	}{
		{name: "incompatible version", major: 2, capabilities: []string{protocolv1.CapabilityTerminalBinaryOutput}},
		{name: "missing required capability", major: 1},
	}

	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			launcher := &fakeTerminalLauncher{}
			app := New(Config{SessionToken: "expected-token", Command: []string{"fake"}})
			app.launcher = launcher
			testServer := httptest.NewServer(app.Handler())
			defer testServer.Close()

			connection := dialProtocolV1(t, testServer.URL)
			defer connection.Close()
			encoded := marshalClientHello(t, testCase.major, 0, testCase.capabilities)
			if err := connection.WriteMessage(websocket.BinaryMessage, encoded); err != nil {
				t.Fatalf("send invalid client Hello: %v", err)
			}
			if _, _, err := connection.ReadMessage(); err == nil {
				t.Fatal("connection remained open after invalid Hello")
			}
			if launcher.calls.Load() != 0 {
				t.Fatalf("terminal launch calls = %d, want 0", launcher.calls.Load())
			}
		})
	}
}

type recordingPTY struct {
	writes chan []byte
}

func (p *recordingPTY) Close() error             { return nil }
func (p *recordingPTY) Read([]byte) (int, error) { return 0, io.EOF }
func (p *recordingPTY) Resize(int, int) error    { return nil }
func (p *recordingPTY) Write(data []byte) (int, error) {
	p.writes <- append([]byte(nil), data...)
	return len(data), nil
}

func marshalClientAttach(t *testing.T, sessionID []byte, generation, lastAcknowledgedSequence uint64) []byte {
	t.Helper()
	envelope := newClientProtocolEnvelope(sessionID, generation, 2, 1)
	envelope.Payload = &vibebridgev1.Envelope_AttachSession{
		AttachSession: &vibebridgev1.AttachSession{LastAcknowledgedSequence: lastAcknowledgedSequence},
	}
	return marshalClientProtocolEnvelope(t, envelope)
}

func marshalClientAcknowledgement(t *testing.T, sessionID []byte, generation, sequence, acknowledge uint64) []byte {
	t.Helper()
	envelope := newClientProtocolEnvelope(sessionID, generation, sequence, acknowledge)
	envelope.Payload = &vibebridgev1.Envelope_Acknowledgement{
		Acknowledgement: &vibebridgev1.Acknowledgement{},
	}
	return marshalClientProtocolEnvelope(t, envelope)
}

func newClientProtocolEnvelope(sessionID []byte, generation, sequence, acknowledge uint64) *vibebridgev1.Envelope {
	return &vibebridgev1.Envelope{
		ProtocolMajor:     1,
		ConnectionId:      []byte("0123456789abcdef"),
		SessionId:         append([]byte(nil), sessionID...),
		SessionGeneration: generation,
		Sequence:          sequence,
		Acknowledge:       acknowledge,
	}
}

func marshalClientProtocolEnvelope(t *testing.T, envelope *vibebridgev1.Envelope) []byte {
	t.Helper()
	encoded, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal client Protocol V1 envelope: %v", err)
	}
	return encoded
}

func waitForSessionState(t *testing.T, app *Server, want sessionState) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		app.mu.Lock()
		session := app.session
		app.mu.Unlock()
		if session != nil {
			session.mu.Lock()
			state := session.lifecycle.state
			session.mu.Unlock()
			if state == want {
				return
			}
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatalf("session did not reach state %q", want)
}

func readProtocolEnvelope(t *testing.T, connection *websocket.Conn) *vibebridgev1.Envelope {
	t.Helper()
	messageType, encoded, err := connection.ReadMessage()
	if err != nil {
		t.Fatalf("read Protocol V1 envelope: %v", err)
	}
	if messageType != websocket.BinaryMessage {
		t.Fatalf("Protocol V1 message type = %d, want binary", messageType)
	}
	envelope := new(vibebridgev1.Envelope)
	if err := proto.Unmarshal(encoded, envelope); err != nil {
		t.Fatalf("decode Protocol V1 envelope: %v", err)
	}
	return envelope
}

func dialProtocolV1(t *testing.T, serverURL string) *websocket.Conn {
	t.Helper()
	dialer := *websocket.DefaultDialer
	dialer.Subprotocols = []string{protocolv1.WebSocketSubprotocol}
	wsURL := "ws" + strings.TrimPrefix(serverURL, "http") + "/ws?token=expected-token"
	connection, response, err := dialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("dial Protocol V1 WebSocket: %v", err)
	}
	if response.Header.Get("Sec-WebSocket-Protocol") != protocolv1.WebSocketSubprotocol {
		connection.Close()
		t.Fatalf("selected subprotocol = %q, want %q", response.Header.Get("Sec-WebSocket-Protocol"), protocolv1.WebSocketSubprotocol)
	}
	return connection
}

func marshalClientHello(t *testing.T, major, minor uint32, capabilities []string) []byte {
	t.Helper()
	version := &vibebridgev1.ProtocolVersion{Major: major, Minor: minor}
	envelope := &vibebridgev1.Envelope{
		ProtocolMajor: major,
		ProtocolMinor: minor,
		ConnectionId:  []byte("0123456789abcdef"),
		Sequence:      1,
		Payload: &vibebridgev1.Envelope_Hello{Hello: &vibebridgev1.Hello{
			PeerRole: vibebridgev1.PeerRole_PEER_ROLE_CLIENT,
			SupportedVersions: &vibebridgev1.ProtocolVersionRange{
				Minimum: version,
				Maximum: &vibebridgev1.ProtocolVersion{Major: major, Minor: minor},
			},
			Capabilities:     capabilities,
			MaxEnvelopeBytes: protocolv1.MaxEnvelopeBytes,
		}},
	}
	encoded, err := proto.Marshal(envelope)
	if err != nil {
		t.Fatalf("marshal client Hello: %v", err)
	}
	return encoded
}
