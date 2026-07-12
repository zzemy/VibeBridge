package agentservice

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRuntimeStateRoundTripAndOwnerCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state", "runtime.json")
	want := RuntimeState{
		Version:       CurrentRuntimeStateVersion,
		PID:           1234,
		StartedAt:     time.Date(2026, 7, 12, 8, 30, 0, 0, time.UTC),
		ListenAddress: "127.0.0.1:8787",
		SessionToken:  "test-token",
	}
	if err := WriteRuntimeState(path, want); err != nil {
		t.Fatalf("write runtime state: %v", err)
	}
	got, err := LoadRuntimeState(path)
	if err != nil {
		t.Fatalf("load runtime state: %v", err)
	}
	if got != want {
		t.Fatalf("runtime state = %#v, want %#v", got, want)
	}

	if err := ClearRuntimeState(path, 9999); err != nil {
		t.Fatalf("clear state for a different process: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("different process removed runtime state: %v", err)
	}
	if err := ClearRuntimeState(path, want.PID); err != nil {
		t.Fatalf("clear owner runtime state: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("runtime state still exists after owner cleanup: %v", err)
	}
}

func TestRuntimeStateReplacementWaitsForOwnerCleanup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runtime.json")
	oldState := RuntimeState{
		Version:       CurrentRuntimeStateVersion,
		PID:           1234,
		StartedAt:     time.Date(2026, 7, 12, 8, 30, 0, 0, time.UTC),
		ListenAddress: "127.0.0.1:8787",
		SessionToken:  "old-token",
	}
	newState := oldState
	newState.PID = 5678
	newState.StartedAt = oldState.StartedAt.Add(time.Minute)
	newState.SessionToken = "new-token"
	if err := WriteRuntimeState(path, oldState); err != nil {
		t.Fatalf("write old runtime state: %v", err)
	}

	cleanupStarted := make(chan struct{})
	releaseCleanup := make(chan struct{})
	cleanupDone := make(chan error, 1)
	go func() {
		cleanupDone <- withRuntimeStateLock(path, func() error {
			close(cleanupStarted)
			<-releaseCleanup
			return os.Remove(path)
		})
	}()
	<-cleanupStarted

	writeStarted := make(chan struct{})
	writeDone := make(chan error, 1)
	go func() {
		close(writeStarted)
		writeDone <- WriteRuntimeState(path, newState)
	}()
	<-writeStarted
	select {
	case err := <-writeDone:
		t.Fatalf("replacement bypassed owner cleanup lock: %v", err)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseCleanup)
	if err := <-cleanupDone; err != nil {
		t.Fatalf("finish owner cleanup: %v", err)
	}
	if err := <-writeDone; err != nil {
		t.Fatalf("write replacement runtime state: %v", err)
	}
	got, err := LoadRuntimeState(path)
	if err != nil {
		t.Fatalf("load replacement runtime state: %v", err)
	}
	if got != newState {
		t.Fatalf("runtime state = %#v, want replacement %#v", got, newState)
	}
}

func TestLoadRuntimeStateRejectsInvalidBoundaryData(t *testing.T) {
	cases := map[string]string{
		"unknown version": `{"version":2,"pid":1,"started_at":"2026-07-12T08:30:00Z","listen_address":"127.0.0.1:8787","session_token":"token"}`,
		"unknown field":   `{"version":1,"pid":1,"started_at":"2026-07-12T08:30:00Z","listen_address":"127.0.0.1:8787","session_token":"token","extra":true}`,
		"invalid address": `{"version":1,"pid":1,"started_at":"2026-07-12T08:30:00Z","listen_address":"bad","session_token":"token"}`,
		"missing token":   `{"version":1,"pid":1,"started_at":"2026-07-12T08:30:00Z","listen_address":"127.0.0.1:8787","session_token":""}`,
		"multiple values": `{"version":1,"pid":1,"started_at":"2026-07-12T08:30:00Z","listen_address":"127.0.0.1:8787","session_token":"token"} {}`,
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "runtime.json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("write fixture: %v", err)
			}
			if _, err := LoadRuntimeState(path); err == nil {
				t.Fatal("invalid runtime state loaded successfully")
			}
		})
	}
}

func TestInstallOptionsRequireAbsoluteStablePaths(t *testing.T) {
	valid := InstallOptions{
		Executable:       filepath.Join(t.TempDir(), "vibebridge.exe"),
		ConfigPath:       filepath.Join(t.TempDir(), "config.json"),
		RuntimeStatePath: filepath.Join(t.TempDir(), "runtime.json"),
		WorkingDirectory: t.TempDir(),
	}
	if err := valid.validate(); err != nil {
		t.Fatalf("valid install options: %v", err)
	}
	invalid := valid
	invalid.ConfigPath = "config.json"
	if err := invalid.validate(); err == nil {
		t.Fatal("relative config path was accepted")
	}
}
