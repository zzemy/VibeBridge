package server

import (
	"errors"
	"reflect"
	"testing"
	"time"
)

type fakeTerminalLauncher struct {
	request terminalLaunchRequest
	launch  terminalLaunch
	err     error
}

func (l *fakeTerminalLauncher) Start(request terminalLaunchRequest) (terminalLaunch, error) {
	l.request = request
	l.request.Command = append([]string(nil), request.Command...)
	l.request.Environment = append([]string(nil), request.Environment...)
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

	session, err := newPTYSession(terminalLaunchRequest{Command: command, WorkingDirectory: `C:\workspace`, Environment: []string{"PATH=test"}}, 0, systemClock{}, launcher, nil, sessionTelemetry{})
	if err != nil {
		t.Fatalf("start session: %v", err)
	}
	if !reflect.DeepEqual(launcher.request.Command, command) {
		t.Fatalf("launcher command = %q, want %q", launcher.request.Command, command)
	}
	if launcher.request.WorkingDirectory != `C:\workspace` {
		t.Fatalf("working directory = %q, want %q", launcher.request.WorkingDirectory, `C:\workspace`)
	}
	if !reflect.DeepEqual(launcher.request.Environment, []string{"PATH=test"}) {
		t.Fatalf("environment = %q, want allowlisted values", launcher.request.Environment)
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

	session, err := newPTYSession(terminalLaunchRequest{Command: []string{"codex"}}, 0, systemClock{}, launcher, nil, sessionTelemetry{})
	if session != nil {
		t.Fatal("session created after launcher failure")
	}
	if !errors.Is(err, wantErr) {
		t.Fatalf("error = %v, want %v", err, wantErr)
	}
}
