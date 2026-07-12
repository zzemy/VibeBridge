package server

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeTerminalLauncher struct {
	command []string
	launch  terminalLaunch
	err     error
}

func (l *fakeTerminalLauncher) Start(command []string) (terminalLaunch, error) {
	l.command = append([]string(nil), command...)
	return l.launch, l.err
}

type blockingWaiter struct {
	done <-chan struct{}
	err  error
}

func (w blockingWaiter) Wait() error {
	<-w.done
	return w.err
}

func TestNewPTYSessionUsesTerminalLauncherContract(t *testing.T) {
	wait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	command := []string{"codex", "--help"}

	session, err := newPTYSession(command, 0, systemClock{}, launcher, nil)
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if !reflect.DeepEqual(launcher.command, command) {
		t.Fatalf("launcher command = %q, want %q", launcher.command, command)
	}
	if session.lifecycle.state != sessionStateDetached {
		t.Fatalf("state = %q, want detached", session.lifecycle.state)
	}

	close(wait)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("session did not observe fake process exit")
	}
}

func TestNewPTYSessionReturnsLauncherFailure(t *testing.T) {
	wantErr := errors.New("launch failed")
	launcher := &fakeTerminalLauncher{err: wantErr}

	session, err := newPTYSession([]string{"codex"}, 0, systemClock{}, launcher, nil)
	if session != nil {
		t.Fatal("session created after launcher failure")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
