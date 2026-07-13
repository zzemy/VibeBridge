package server

import (
	"bytes"
	"testing"
	"time"
)

func TestNewPTYIncrementsProtocolSessionGeneration(t *testing.T) {
	firstWait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &recordingPTY{writes: make(chan []byte, 1)},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: firstWait},
	}}
	server := New(Config{Command: []string{"fake"}})
	server.launcher = launcher

	first, created, err := server.getOrCreateSession()
	if err != nil || !created {
		t.Fatalf("create first PTY session = %t/%v", created, err)
	}
	if len(first.sessionID) != protocolSessionIDBytes || first.generation != 1 {
		t.Fatalf("first identity = %x/%d", first.sessionID, first.generation)
	}
	first.terminateWithReason("test replacement")
	close(firstWait)
	select {
	case <-first.done:
	case <-time.After(time.Second):
		t.Fatal("first PTY did not end")
	}
	deadline := time.Now().Add(time.Second)
	cleared := false
	for time.Now().Before(deadline) {
		server.mu.Lock()
		cleared = server.session == nil
		server.mu.Unlock()
		if cleared {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !cleared {
		t.Fatal("first PTY remained registered after cleanup")
	}

	secondWait := make(chan struct{})
	launcher.launch = terminalLaunch{
		terminal:    &recordingPTY{writes: make(chan []byte, 1)},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: secondWait},
	}
	second, created, err := server.getOrCreateSession()
	if err != nil || !created {
		t.Fatalf("create second PTY session = %t/%v", created, err)
	}
	if second.generation != 2 {
		t.Fatalf("second generation = %d, want 2", second.generation)
	}
	if len(second.sessionID) != protocolSessionIDBytes || bytes.Equal(second.sessionID, first.sessionID) {
		t.Fatalf("second session ID = %x, first = %x", second.sessionID, first.sessionID)
	}

	second.terminateWithReason("test cleanup")
	close(secondWait)
	select {
	case <-second.done:
	case <-time.After(time.Second):
		t.Fatal("second PTY did not end")
	}
}
