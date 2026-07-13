package server

import (
	"testing"
	"time"
)

type manualClock struct {
	now    time.Time
	timers []*manualTimer
}

func (c *manualClock) Now() time.Time {
	return c.now
}

func (c *manualClock) After(time.Duration) <-chan time.Time {
	return make(chan time.Time)
}

func (c *manualClock) AfterFunc(duration time.Duration, callback func()) timer {
	timer := &manualTimer{duration: duration, callback: callback, active: true}
	c.timers = append(c.timers, timer)
	return timer
}

type manualTimer struct {
	duration time.Duration
	callback func()
	active   bool
}

func (t *manualTimer) Stop() bool {
	wasActive := t.active
	t.active = false
	return wasActive
}

func (t *manualTimer) Reset(duration time.Duration) bool {
	wasActive := t.active
	t.duration = duration
	t.active = true
	return wasActive
}

func (t *manualTimer) fire() {
	if !t.active {
		return
	}
	t.active = false
	t.callback()
}

func TestIdleExpiryUsesInjectedTimerAndEndsSession(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)}
	session := newTimerTestSession(clock, sessionStateDetached)
	session.idleTimeout = 30 * time.Minute

	session.resetIdleTimer()
	if len(clock.timers) != 1 || clock.timers[0].duration != 30*time.Minute {
		t.Fatalf("idle timer = %#v, want one 30 minute timer", clock.timers)
	}
	clock.timers[0].fire()

	if session.lifecycle.state != sessionStateEnding {
		t.Fatalf("state = %q, want ending", session.lifecycle.state)
	}
	if got := session.replay.drain(); len(got) != 1 || string(got[0]) != "idle timeout reached; ending session\r\n" {
		t.Fatalf("idle output = %q, want timeout notice", got)
	}
}

func TestReconnectExpiryUsesInjectedTimerAndEndsSession(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)}
	session := newTimerTestSession(clock, sessionStateConnected)
	writer := &websocketWriter{}
	session.client = writer

	session.detach(writer, 90*time.Second)
	if session.lifecycle.state != sessionStateDetached {
		t.Fatalf("state after detach = %q, want detached", session.lifecycle.state)
	}
	if len(clock.timers) != 1 || clock.timers[0].duration != 90*time.Second {
		t.Fatalf("reconnect timer = %#v, want one 90 second timer", clock.timers)
	}
	clock.timers[0].fire()

	if session.lifecycle.state != sessionStateEnding {
		t.Fatalf("state = %q, want ending", session.lifecycle.state)
	}
}

func TestReattachStopsReconnectTimer(t *testing.T) {
	clock := &manualClock{now: time.Date(2026, time.July, 12, 12, 0, 0, 0, time.UTC)}
	session := newTimerTestSession(clock, sessionStateConnected)
	firstWriter := &websocketWriter{}
	session.client = firstWriter
	session.detach(firstWriter, 90*time.Second)

	if !session.attach(&websocketWriter{}) {
		t.Fatal("failed to reattach session")
	}
	clock.timers[0].fire()
	if session.lifecycle.state != sessionStateConnected {
		t.Fatalf("state = %q, want connected after stopped timer fires", session.lifecycle.state)
	}
}

func newTimerTestSession(clock clock, state sessionState) *ptySession {
	done := make(chan struct{})
	close(done)
	return &ptySession{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		done:        done,
		clock:       clock,
		lifecycle:   sessionLifecycle{state: state},
		replay:      newReplayBuffer(1024, time.Hour, clock.Now),
	}
}
