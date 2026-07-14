package server

import (
	"strings"
	"testing"
)

func TestSessionRejectsUnknownToolAdapterBeforePTYLaunch(t *testing.T) {
	launcher := &fakeTerminalLauncher{}
	server := New(Config{
		Command:     []string{"fake"},
		ToolAdapter: "codex",
	})
	server.launcher = launcher

	session, created, err := server.getOrCreateSession()
	if err == nil || !strings.Contains(err.Error(), "initialize tool adapter") {
		t.Fatalf("getOrCreateSession() error = %v, want tool adapter initialization error", err)
	}
	if session != nil || created {
		t.Fatalf("invalid tool adapter created session = %v/%t", session, created)
	}
	if calls := launcher.calls.Load(); calls != 0 {
		t.Fatalf("PTY launcher calls = %d, want 0", calls)
	}
}
