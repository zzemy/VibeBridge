package server

import (
	"errors"
	"testing"
)

func TestSessionLifecycleHappyPath(t *testing.T) {
	lifecycle := newSessionLifecycle()
	assertSessionState(t, lifecycle, sessionStateStarting)

	if !lifecycle.started() || !lifecycle.attach() {
		t.Fatal("failed to start and attach session")
	}
	assertSessionState(t, lifecycle, sessionStateConnected)

	if !lifecycle.detach() || !lifecycle.attach() {
		t.Fatal("failed to detach and reattach session")
	}
	if !lifecycle.beginEnding() || !lifecycle.finish(errors.New("process killed")) {
		t.Fatal("failed to end session")
	}
	assertSessionState(t, lifecycle, sessionStateEnded)
}

func TestSessionLifecycleRejectsInvalidAndRepeatedTransitions(t *testing.T) {
	lifecycle := newSessionLifecycle()
	if lifecycle.attach() {
		t.Fatal("attached a session before startup completed")
	}
	if !lifecycle.started() || lifecycle.started() {
		t.Fatal("startup transition was not single-use")
	}
	if lifecycle.detach() {
		t.Fatal("detached a session without a client")
	}
	if !lifecycle.beginEnding() || lifecycle.beginEnding() {
		t.Fatal("ending transition was not single-use")
	}
	if !lifecycle.finish(nil) || lifecycle.finish(nil) {
		t.Fatal("finish transition was not single-use")
	}
}

func TestSessionLifecycleMarksUnexpectedProcessFailure(t *testing.T) {
	lifecycle := newSessionLifecycle()
	lifecycle.started()

	if !lifecycle.finish(errors.New("unexpected exit")) {
		t.Fatal("failed to record process exit")
	}
	assertSessionState(t, lifecycle, sessionStateFailed)
	if lifecycle.publicState() != "ended" {
		t.Fatalf("public state = %q, want ended", lifecycle.publicState())
	}
}

func assertSessionState(t *testing.T, lifecycle sessionLifecycle, want sessionState) {
	t.Helper()
	if lifecycle.state != want {
		t.Fatalf("state = %q, want %q", lifecycle.state, want)
	}
}
