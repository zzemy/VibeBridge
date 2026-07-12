package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentservice"
)

type fakeServiceManager struct {
	installation  agentservice.InstallationStatus
	runtimePath   string
	installErr    error
	uninstallErr  error
	queryErr      error
	installedWith *agentservice.InstallOptions
	uninstalled   bool
}

func (manager *fakeServiceManager) Install(options agentservice.InstallOptions) error {
	manager.installedWith = &options
	return manager.installErr
}

func (manager *fakeServiceManager) Uninstall() error {
	manager.uninstalled = true
	return manager.uninstallErr
}

func (manager *fakeServiceManager) QueryInstallation() (agentservice.InstallationStatus, error) {
	return manager.installation, manager.queryErr
}

func (manager *fakeServiceManager) RuntimeStatePath() (string, error) {
	return manager.runtimePath, nil
}

func TestRunServiceCommandHelpListsSupportedActions(t *testing.T) {
	var output bytes.Buffer
	if err := runServiceCommand([]string{"--help"}, &output, &output, &fakeServiceManager{}, os.Args[0]); err != nil {
		t.Fatalf("service help: %v", err)
	}
	for _, action := range []string{"install", "status", "uninstall"} {
		if !strings.Contains(output.String(), action) {
			t.Fatalf("service help does not contain %q: %s", action, output.String())
		}
	}
}

func TestRunServiceInstallValidatesConfigAndUsesStableAbsolutePaths(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	launchExecutable, err := json.Marshal(os.Args[0])
	if err != nil {
		t.Fatalf("encode launch executable: %v", err)
	}
	config := fmt.Sprintf(`{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":%s},{"id":"codex","label":"Codex","executable":%s}]}`, launchExecutable, launchExecutable)
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	executable := filepath.Join(root, "vibebridge.exe")
	if err := os.WriteFile(executable, nil, 0o700); err != nil {
		t.Fatalf("write executable fixture: %v", err)
	}
	manager := &fakeServiceManager{runtimePath: filepath.Join(root, "state", "runtime.json")}
	var output bytes.Buffer
	if err := runServiceCommand([]string{"install", "--config", configPath, "--profile", "codex", "--force"}, &output, &output, manager, executable); err != nil {
		t.Fatalf("install service: %v", err)
	}
	if manager.installedWith == nil {
		t.Fatal("service manager did not receive install options")
	}
	options := *manager.installedWith
	if options.Executable != executable || options.ConfigPath != configPath || options.ProfileID != "codex" || !options.Force {
		t.Fatalf("install options = %#v", options)
	}
	if options.WorkingDirectory != root || options.RuntimeStatePath != manager.runtimePath {
		t.Fatalf("install paths = %#v", options)
	}
	if !strings.Contains(output.String(), "requested an immediate start") {
		t.Fatalf("install output = %q", output.String())
	}
}

func TestRunServiceInstallRejectsUnknownProfileBeforeExternalChange(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	config := `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	manager := &fakeServiceManager{runtimePath: filepath.Join(root, "runtime.json")}
	err := runServiceCommand([]string{"install", "--config", configPath, "--profile", "missing"}, &bytes.Buffer{}, &bytes.Buffer{}, manager, os.Args[0])
	if err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("unknown profile error = %v", err)
	}
	if manager.installedWith != nil {
		t.Fatal("external install was attempted for an invalid profile")
	}
}

func TestRunServiceInstallRejectsUnavailableLaunchExecutableBeforeExternalChange(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	config := `{"version":1,"default_profile":"missing","profiles":[{"id":"missing","label":"Missing","executable":"vibebridge-command-that-does-not-exist"}]}`
	if err := os.WriteFile(configPath, []byte(config), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	serviceExecutable := filepath.Join(root, "vibebridge.exe")
	if err := os.WriteFile(serviceExecutable, nil, 0o700); err != nil {
		t.Fatalf("write service executable: %v", err)
	}
	manager := &fakeServiceManager{runtimePath: filepath.Join(root, "runtime.json")}
	err := runServiceCommand([]string{"install", "--config", configPath}, &bytes.Buffer{}, &bytes.Buffer{}, manager, serviceExecutable)
	if err == nil || !strings.Contains(err.Error(), "was not found") {
		t.Fatalf("missing launch executable error = %v", err)
	}
	if manager.installedWith != nil {
		t.Fatal("external install was attempted with an unavailable launch executable")
	}
}

func TestRunServiceStatusProbesAgentAndPrintsCurrentAccessURL(t *testing.T) {
	const token = "runtime-token"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/status" || request.URL.Query().Get("token") != token {
			http.Error(writer, "unauthorized", http.StatusUnauthorized)
			return
		}
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse test server URL: %v", err)
	}

	statePath := filepath.Join(t.TempDir(), "runtime.json")
	state := agentservice.RuntimeState{
		Version:       agentservice.CurrentRuntimeStateVersion,
		PID:           4321,
		StartedAt:     time.Date(2026, 7, 12, 8, 30, 0, 0, time.UTC),
		ListenAddress: parsed.Host,
		SessionToken:  token,
	}
	if err := agentservice.WriteRuntimeState(statePath, state); err != nil {
		t.Fatalf("write runtime state: %v", err)
	}
	manager := &fakeServiceManager{
		installation: agentservice.InstallationStatus{Installed: true},
		runtimePath:  statePath,
	}
	var output bytes.Buffer
	if err := runServiceCommand([]string{"status"}, &output, &output, manager, os.Args[0]); err != nil {
		t.Fatalf("service status: %v", err)
	}
	for _, expected := range []string{"running (PID 4321", server.URL + "/?token=" + token} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("status output does not contain %q: %s", expected, output.String())
		}
	}
}

func TestRunServiceStatusDoesNotExposeTokenForStaleState(t *testing.T) {
	const token = "stale-secret-token"
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	state := agentservice.RuntimeState{
		Version:       agentservice.CurrentRuntimeStateVersion,
		PID:           4321,
		StartedAt:     time.Now().UTC(),
		ListenAddress: "127.0.0.1:1",
		SessionToken:  token,
	}
	if err := agentservice.WriteRuntimeState(statePath, state); err != nil {
		t.Fatalf("write stale runtime state: %v", err)
	}
	manager := &fakeServiceManager{
		installation: agentservice.InstallationStatus{Installed: true},
		runtimePath:  statePath,
	}
	var output bytes.Buffer
	if err := runServiceCommand([]string{"status"}, &output, &output, manager, os.Args[0]); err != nil {
		t.Fatalf("stale service status: %v", err)
	}
	if !strings.Contains(output.String(), "runtime state is stale") {
		t.Fatalf("stale status output = %q", output.String())
	}
	if strings.Contains(output.String(), token) {
		t.Fatalf("stale status exposed token: %q", output.String())
	}
}

func TestRunServiceStatusDistinguishesNotInstalledAndStopped(t *testing.T) {
	var output bytes.Buffer
	notInstalled := &fakeServiceManager{runtimePath: filepath.Join(t.TempDir(), "runtime.json")}
	if err := runServiceCommand([]string{"status"}, &output, &output, notInstalled, os.Args[0]); err != nil {
		t.Fatalf("not-installed status: %v", err)
	}
	if !strings.Contains(output.String(), "not installed") {
		t.Fatalf("not-installed output = %q", output.String())
	}

	output.Reset()
	stopped := &fakeServiceManager{
		installation: agentservice.InstallationStatus{Installed: true},
		runtimePath:  filepath.Join(t.TempDir(), "missing-runtime.json"),
	}
	if err := runServiceCommand([]string{"status"}, &output, &output, stopped, os.Args[0]); err != nil {
		t.Fatalf("stopped status: %v", err)
	}
	if !strings.Contains(output.String(), "installed, but not running") {
		t.Fatalf("stopped output = %q", output.String())
	}
}

func TestRunServiceUninstallRemovesSensitiveRuntimeState(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "runtime.json")
	if err := os.WriteFile(statePath, []byte(`{"corrupt":"state"}`), 0o600); err != nil {
		t.Fatalf("write runtime state fixture: %v", err)
	}
	manager := &fakeServiceManager{runtimePath: statePath}
	var output bytes.Buffer
	if err := runServiceCommand([]string{"uninstall"}, &output, &output, manager, os.Args[0]); err != nil {
		t.Fatalf("uninstall service: %v", err)
	}
	if !manager.uninstalled {
		t.Fatal("service manager was not asked to uninstall")
	}
	if _, err := os.Stat(statePath); !os.IsNotExist(err) {
		t.Fatalf("runtime state remains after uninstall: %v", err)
	}
}

func TestTemporaryGoRunExecutableDetection(t *testing.T) {
	temporary := filepath.Join(os.TempDir(), "go-build123", "b001", "exe", "vibebridge.exe")
	if !isTemporaryGoRunExecutable(temporary) {
		t.Fatalf("temporary go run executable %q was not detected", temporary)
	}
	stable := filepath.Join(t.TempDir(), "release", "vibebridge.exe")
	if isTemporaryGoRunExecutable(stable) {
		t.Fatalf("stable executable %q was classified as go run output", stable)
	}
}
