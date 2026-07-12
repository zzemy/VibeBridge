//go:build windows

package server

import (
	"bytes"
	"fmt"
	"regexp"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentlog"
	"golang.org/x/sys/windows"
)

var childPIDPattern = regexp.MustCompile(`VIBEBRIDGE_CHILD_PID=(\d+)`)

func TestWindowsConPTYTerminateClosesProcessTree(t *testing.T) {
	const childLifetime = 2 * time.Minute
	command := []string{
		"powershell.exe",
		"-NoLogo",
		"-NoProfile",
		"-Command",
		fmt.Sprintf(
			`$child = Start-Process powershell.exe -ArgumentList '-NoLogo','-NoProfile','-Command','Start-Sleep -Seconds %d' -PassThru; Write-Output "VIBEBRIDGE_CHILD_PID=$($child.Id)"; Wait-Process -Id $child.Id`,
			int(childLifetime.Seconds()),
		),
	}

	session, err := newPTYSession(terminalLaunchRequest{Command: command}, 0, systemClock{}, ptyTerminalLauncher{}, nil, sessionTelemetry{})
	if err != nil {
		t.Fatalf("start Windows ConPTY session: %v", err)
	}
	t.Cleanup(func() { session.terminateWithReason(agentlog.ReasonAgentShutdown) })

	childPID := waitForChildPID(t, session, 10*time.Second)
	childHandle, err := windows.OpenProcess(
		windows.SYNCHRONIZE|windows.PROCESS_TERMINATE,
		false,
		uint32(childPID),
	)
	if err != nil {
		t.Fatalf("open child process %d: %v", childPID, err)
	}
	t.Cleanup(func() {
		_ = windows.TerminateProcess(childHandle, 1)
		_, _ = windows.WaitForSingleObject(childHandle, 5_000)
		windows.CloseHandle(childHandle)
	})

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
		t.Fatal("ConPTY session did not finish after concurrent termination")
	}

	if err := waitForProcessExit(childHandle, childPID, 5*time.Second); err != nil {
		t.Fatal(err)
	}
}

func waitForChildPID(t *testing.T, session *ptySession, timeout time.Duration) int {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		session.mu.Lock()
		output := string(bytes.Join(session.replay.snapshot(), nil))
		session.mu.Unlock()

		match := childPIDPattern.FindStringSubmatch(output)
		if len(match) == 2 {
			pid, err := strconv.Atoi(match[1])
			if err != nil {
				t.Fatalf("parse child process ID %q: %v", match[1], err)
			}
			return pid
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatal("timed out waiting for child process ID from ConPTY output")
	return 0
}

func waitForProcessExit(handle windows.Handle, pid int, timeout time.Duration) error {
	event, err := windows.WaitForSingleObject(handle, uint32(timeout.Milliseconds()))
	if err != nil {
		return fmt.Errorf("wait for child process %d: %w", pid, err)
	}
	if event != windows.WAIT_OBJECT_0 {
		return fmt.Errorf("child process %d remained alive after ConPTY cleanup", pid)
	}
	return nil
}
