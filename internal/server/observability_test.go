package server

import (
	"encoding/hex"
	"errors"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentlog"
)

type recordingEventLogger struct {
	mu     sync.Mutex
	events []agentlog.Event
}

func (l *recordingEventLogger) Log(event agentlog.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, event)
}

func (l *recordingEventLogger) snapshot() []agentlog.Event {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]agentlog.Event(nil), l.events...)
}

func TestSessionLifecycleLogsOnlyOpaqueMetadata(t *testing.T) {
	wait := make(chan struct{})
	var cancelOnce sync.Once
	logger := &recordingEventLogger{}
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel: func() {
			cancelOnce.Do(func() { close(wait) })
		},
		waiter: blockingWaiter{done: wait},
	}}

	const (
		correlationID = "0123456789abcdef0123456789abcdef"
		secretToken   = "secret-session-token"
		secretCommand = "secret-command-argument"
		secretPath    = `C:\private\workspace`
		secretEnv     = "API_KEY=secret-value"
	)
	session, err := newPTYSession(
		terminalLaunchRequest{
			Command:          []string{"codex", secretCommand, secretToken},
			WorkingDirectory: secretPath,
			Environment:      []string{secretEnv},
		},
		0,
		systemClock{},
		launcher,
		nil,
		nil,
		sessionTelemetry{correlationID: correlationID, logger: logger},
	)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}

	session.terminateWithReason(agentlog.ReasonExplicitEnd)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not finish")
	}

	events := logger.snapshot()
	gotNames := make([]agentlog.Name, 0, len(events))
	for _, event := range events {
		gotNames = append(gotNames, event.Name)
		if event.SessionID != correlationID {
			t.Fatalf("session ID = %q, want opaque correlation ID", event.SessionID)
		}
		serialized := strings.Join([]string{string(event.Name), event.SessionID, string(event.State), string(event.Reason), string(event.Outcome)}, " ")
		for _, sensitive := range []string{secretToken, secretCommand, secretPath, secretEnv} {
			if strings.Contains(serialized, sensitive) {
				t.Fatalf("structured event leaked sensitive value %q: %#v", sensitive, event)
			}
		}
	}
	wantNames := []agentlog.Name{
		agentlog.EventSessionStarted,
		agentlog.EventSessionEnding,
		agentlog.EventSessionEnded,
	}
	if !reflect.DeepEqual(gotNames, wantNames) {
		t.Fatalf("event names = %q, want %q", gotNames, wantNames)
	}
	ended := events[len(events)-1]
	if ended.State != agentlog.State(sessionStateEnded) || ended.Reason != agentlog.ReasonExplicitEnd || ended.Outcome != agentlog.OutcomeSuccess {
		t.Fatalf("ended event = %#v", ended)
	}
}

func TestUnexpectedProcessFailureLogsOnlySafeOutcome(t *testing.T) {
	wait := make(chan struct{})
	close(wait)
	logger := &recordingEventLogger{}
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait, err: errors.New("private raw process failure")},
	}}

	session, err := newPTYSession(
		terminalLaunchRequest{Command: []string{"codex"}},
		0,
		systemClock{},
		launcher,
		nil,
		nil,
		sessionTelemetry{correlationID: "opaque-session-id", logger: logger},
	)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not observe process failure")
	}

	events := logger.snapshot()
	ended := events[len(events)-1]
	if ended.Name != agentlog.EventSessionEnded || ended.State != agentlog.StateFailed || ended.Reason != agentlog.ReasonProcessExit || ended.Outcome != agentlog.OutcomeFailure {
		t.Fatalf("failed process event = %#v", ended)
	}
	if strings.Contains(strings.Join([]string{string(ended.Name), ended.SessionID, string(ended.State), string(ended.Reason), string(ended.Outcome)}, " "), "private raw process failure") {
		t.Fatalf("failed process event leaked raw error: %#v", ended)
	}
}

func TestSessionAttachAndDetachEventsReportStateTransitions(t *testing.T) {
	logger := &recordingEventLogger{}
	session := &ptySession{
		done:      make(chan struct{}),
		clock:     systemClock{},
		lifecycle: sessionLifecycle{state: sessionStateDetached},
		replay:    newReplayBuffer(maxBufferedOutputBytes, bufferedOutputMaxAge, time.Now),
		telemetry: sessionTelemetry{correlationID: "opaque-session-id", logger: logger},
	}
	writer := &websocketWriter{}

	if !session.attach(writer) {
		t.Fatal("detached session did not attach")
	}
	session.detach(writer, time.Hour)
	t.Cleanup(func() {
		session.mu.Lock()
		defer session.mu.Unlock()
		if session.detachTimer != nil {
			session.detachTimer.Stop()
		}
	})

	events := logger.snapshot()
	want := []agentlog.Event{
		{Name: agentlog.EventSessionAttached, SessionID: "opaque-session-id", State: agentlog.StateConnected},
		{Name: agentlog.EventSessionDetached, SessionID: "opaque-session-id", State: agentlog.StateDetached},
	}
	if !reflect.DeepEqual(events, want) {
		t.Fatalf("attach/detach events = %#v, want %#v", events, want)
	}
}

func TestNewSessionCorrelationIDIsOpaqueAndUnique(t *testing.T) {
	first, err := newSessionCorrelationID()
	if err != nil {
		t.Fatalf("create first correlation ID: %v", err)
	}
	second, err := newSessionCorrelationID()
	if err != nil {
		t.Fatalf("create second correlation ID: %v", err)
	}
	if first == second {
		t.Fatal("correlation IDs were not unique")
	}
	for _, value := range []string{first, second} {
		decoded, err := hex.DecodeString(value)
		if err != nil || len(decoded) != 16 {
			t.Fatalf("correlation ID %q is not 128-bit hexadecimal", value)
		}
	}
}
