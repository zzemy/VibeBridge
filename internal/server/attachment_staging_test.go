package server

import (
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWorkspaceSessionOwnsAndCleansStagingDirectory(t *testing.T) {
	workspaceRoot := t.TempDir()
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "")
	wait := make(chan struct{})
	launcher := &fakeTerminalLauncher{launch: terminalLaunch{
		terminal:    &countingPTY{},
		processTree: &countingProcessTree{},
		cancel:      func() {},
		waiter:      blockingWaiter{done: wait},
	}}
	server := New(Config{
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	server.launcher = launcher

	session, created, err := server.getOrCreateSession()
	if err != nil || !created {
		t.Fatalf("create workspace session = %t/%v", created, err)
	}
	if session.staging == nil {
		t.Fatal("workspace session has no staging directory")
	}
	wantPath := filepath.Join(canonicalRoot, ".vibebridge", "uploads", hex.EncodeToString(session.sessionID))
	if session.staging.Path() != wantPath {
		t.Fatalf("staging path = %q, want %q", session.staging.Path(), wantPath)
	}
	if err := os.WriteFile(filepath.Join(session.staging.Path(), "partial.upload"), []byte("partial"), 0o600); err != nil {
		t.Fatalf("create staged file: %v", err)
	}

	close(wait)
	select {
	case <-session.done:
	case <-time.After(time.Second):
		t.Fatal("workspace PTY did not end")
	}
	if _, err := os.Lstat(wantPath); !os.IsNotExist(err) {
		t.Fatalf("session staging remains after process exit: %v", err)
	}
	if err := session.staging.Cleanup(); err != nil {
		t.Fatalf("repeated session staging cleanup: %v", err)
	}
}

func TestWorkspaceSessionRemovesStagingWhenPTYLaunchFails(t *testing.T) {
	workspaceRoot := t.TempDir()
	canonicalRoot, canonicalWorkingDirectory := validatedWorkspacePaths(t, workspaceRoot, "")
	launcher := &fakeTerminalLauncher{err: errors.New("launch failed")}
	server := New(Config{
		Command:          []string{"fake"},
		WorkspaceRoot:    canonicalRoot,
		WorkingDirectory: canonicalWorkingDirectory,
	})
	server.launcher = launcher

	session, created, err := server.getOrCreateSession()
	if err == nil || session != nil || created {
		t.Fatalf("failed PTY launch created session = %v/%t/%v", session, created, err)
	}
	uploadsRoot := filepath.Join(canonicalRoot, ".vibebridge", "uploads")
	entries, readErr := os.ReadDir(uploadsRoot)
	if readErr != nil && !os.IsNotExist(readErr) {
		t.Fatalf("read uploads root after failed launch: %v", readErr)
	}
	if len(entries) != 0 {
		t.Fatalf("failed PTY launch left %d staging entries", len(entries))
	}
}
