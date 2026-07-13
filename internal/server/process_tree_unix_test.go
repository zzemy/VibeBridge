//go:build darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package server

import (
	"bytes"
	"errors"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentlog"
	"golang.org/x/sys/unix"
)

var unixChildPIDPattern = regexp.MustCompile(`VIBEBRIDGE_CHILD_PID=(\d+)`)

func TestUnixPTYTerminateClosesProcessGroup(t *testing.T) {
	command := []string{
		"/bin/sh",
		"-c",
		`sleep 120 & child=$!; printf 'VIBEBRIDGE_CHILD_PID=%s\n' "$child"; wait "$child"`,
	}

	session, err := newPTYSession(terminalLaunchRequest{Command: command}, 0, systemClock{}, ptyTerminalLauncher{}, nil, nil, sessionTelemetry{})
	if err != nil {
		t.Fatalf("start Unix PTY session: %v", err)
	}
	t.Cleanup(func() { session.terminateWithReason(agentlog.ReasonAgentShutdown) })

	childPID := waitForUnixChildPID(t, session, 10*time.Second)
	t.Cleanup(func() { _ = unix.Kill(childPID, unix.SIGKILL) })

	var terminators sync.WaitGroup
	for range 4 {
		terminators.Add(1)
		go func() {
			defer terminators.Done()
			session.terminateWithReason(agentlog.ReasonExplicitEnd)
		}()
	}
	terminators.Wait()

	select {
	case <-session.done:
	case <-time.After(10 * time.Second):
		t.Fatal("Unix PTY session did not finish after concurrent termination")
	}

	if err := waitForUnixProcessExit(childPID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func waitForUnixChildPID(t *testing.T, session *ptySession, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		output := string(bytes.Join(session.replay.snapshot(), nil))
		session.mu.Unlock()

		match := unixChildPIDPattern.FindStringSubmatch(output)
		if len(match) == 2 {
			pid, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatalf("parse child process ID %q: %v", match[1], err)
			}
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for child process ID from Unix PTY output")
	return 0
}

func waitForUnixProcessExit(pid int, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		err := unix.Kill(pid, 0)
		if errors.Is(err, unix.ESRCH) {
			return nil
		}
		if err != nil && !errors.Is(err, unix.EPERM) {
			return err
		}
		time.Sleep(25 * time.Millisecond)
	}
	return errors.New("child process remained alive after Unix PTY cleanup")
}
