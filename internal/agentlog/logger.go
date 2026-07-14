package agentlog

import (
	"context"
	"io"
	"log/slog"
)

// Name identifies a stable, privacy-safe Agent event.
type Name string

const (
	EventAgentStarting        Name = "agent.starting"
	EventAgentStopping        Name = "agent.stopping"
	EventAgentStopped         Name = "agent.stopped"
	EventSessionStarted       Name = "session.started"
	EventSessionAttached      Name = "session.attached"
	EventSessionDetached      Name = "session.detached"
	EventSessionEnding        Name = "session.ending"
	EventSessionEnded         Name = "session.ended"
	EventSessionCleanupFailed Name = "session.cleanup_failed"
)

// State, Reason, and Outcome are deliberately closed vocabularies at call
// sites. They describe lifecycle metadata without carrying user content.
type State string
type Reason string
type Outcome string

const (
	StateConnected State = "connected"
	StateDetached  State = "detached"
	StateEnded     State = "ended"
	StateEnding    State = "ending"
	StateFailed    State = "failed"

	ReasonAgentShutdown    Reason = "agent_shutdown"
	ReasonExplicitEnd      Reason = "explicit_end"
	ReasonIdleTimeout      Reason = "idle_timeout"
	ReasonListenerClosed   Reason = "listener_closed"
	ReasonListenerError    Reason = "listener_error"
	ReasonProcessExit      Reason = "process_exit"
	ReasonReconnectExpired Reason = "reconnect_expired"
	ReasonSignal           Reason = "signal"
	ReasonSuperseded       Reason = "superseded"

	OutcomeFailure Outcome = "failure"
	OutcomeSuccess Outcome = "success"
)

// Event is the complete logging allowlist. Raw errors, terminal content,
// commands, paths, tokens, network addresses, and environment values do not
// belong in Agent logs.
type Event struct {
	Name      Name
	SessionID string
	State     State
	Reason    Reason
	Outcome   Outcome
}

// Logger accepts only the fixed Event schema so service code cannot attach
// arbitrary potentially sensitive fields.
type Logger interface {
	Log(Event)
}

type jsonLogger struct {
	logger *slog.Logger
}

// NewJSON creates a structured logger suitable for local service diagnostics.
func NewJSON(output io.Writer) Logger {
	return &jsonLogger{logger: slog.New(slog.NewJSONHandler(output, nil))}
}

func (l *jsonLogger) Log(event Event) {
	attributes := []slog.Attr{slog.String("event", string(event.Name))}
	if event.SessionID != "" {
		attributes = append(attributes, slog.String("session_id", event.SessionID))
	}
	if event.State != "" {
		attributes = append(attributes, slog.String("state", string(event.State)))
	}
	if event.Reason != "" {
		attributes = append(attributes, slog.String("reason", string(event.Reason)))
	}
	if event.Outcome != "" {
		attributes = append(attributes, slog.String("outcome", string(event.Outcome)))
	}
	l.logger.LogAttrs(context.Background(), slog.LevelInfo, "VibeBridge event", attributes...)
}

type discardLogger struct{}

func (discardLogger) Log(Event) {}

// Discard returns a no-op logger for tests and embedded use.
func Discard() Logger {
	return discardLogger{}
}
