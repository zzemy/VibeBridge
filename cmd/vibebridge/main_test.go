package main

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentservice"
)

func TestIsWildcardAddress(t *testing.T) {
	cases := []struct {
		address string
		want    bool
	}{
		{address: "0.0.0.0:8787", want: true},
		{address: "[::]:8787", want: true},
		{address: "127.0.0.1:8787", want: false},
		{address: "not-an-address", want: false},
	}

	for _, testCase := range cases {
		t.Run(testCase.address, func(t *testing.T) {
			if got := isWildcardAddress(testCase.address); got != testCase.want {
				t.Fatalf("isWildcardAddress(%q) = %t, want %t", testCase.address, got, testCase.want)
			}
		})
	}
}

func TestValidateCommand(t *testing.T) {
	if err := validateCommand([]string{os.Args[0]}); err != nil {
		t.Fatalf("validate current executable: %v", err)
	}
	if err := validateCommand([]string{"vibebridge-command-that-does-not-exist"}); err == nil {
		t.Fatal("missing command passed validation")
	}
}

func TestRunDiagnosticsReportsExpandedPreflight(t *testing.T) {
	var output bytes.Buffer
	options := startupOptions{
		addr:      "127.0.0.1:0",
		webDir:    t.TempDir(),
		command:   []string{os.Args[0]},
		profileID: "test-profile",
	}
	if err := runDiagnostics(options, false, &output); err != nil {
		t.Fatalf("run diagnostics: %v", err)
	}

	for _, expected := range []string{
		"host platform ",
		`launch profile "test-profile" executable is available`,
		"launch working directory uses the current directory",
		"127.0.0.1:0 is available for the HTTP listener",
		"frontend build not found",
		"listener is loopback-only; phones on the LAN cannot connect",
	} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("diagnostic output did not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestRunDiagnosticsReportsWorkspaceWithoutExposingItsPath(t *testing.T) {
	workingDirectory := t.TempDir()
	var output bytes.Buffer
	options := startupOptions{
		addr:             "127.0.0.1:0",
		webDir:           t.TempDir(),
		command:          []string{os.Args[0]},
		profileID:        "test-profile",
		workspaceID:      "private-repo",
		workspaceRoot:    workingDirectory,
		workingDirectory: workingDirectory,
	}
	if err := runDiagnostics(options, false, &output); err != nil {
		t.Fatalf("run diagnostics: %v", err)
	}
	if !strings.Contains(output.String(), `workspace "private-repo" root and launch working directory are available`) {
		t.Fatalf("workspace diagnostic was not reported:\n%s", output.String())
	}
	if strings.Contains(output.String(), workingDirectory) {
		t.Fatalf("workspace path was exposed in diagnostic output:\n%s", output.String())
	}
}

func TestRunDiagnosticsDoesNotExposeUnavailableWorkspacePath(t *testing.T) {
	workspaceRoot := t.TempDir()
	workingDirectory := filepath.Join(workspaceRoot, "private-working-directory")
	var output bytes.Buffer
	err := runDiagnostics(startupOptions{
		addr:             "127.0.0.1:0",
		webDir:           t.TempDir(),
		command:          []string{os.Args[0]},
		workspaceID:      "private-repo",
		workspaceRoot:    workspaceRoot,
		workingDirectory: workingDirectory,
	}, false, &output)
	if err == nil {
		t.Fatal("diagnostics accepted an unavailable workspace path")
	}
	if strings.Contains(output.String(), workingDirectory) || strings.Contains(output.String(), "private-working-directory") {
		t.Fatalf("workspace path was exposed in diagnostic output:\n%s", output.String())
	}
}

func TestNetworkDiagnosticsUsesPlatformFirewallGuidance(t *testing.T) {
	checks := networkDiagnostics("192.168.1.10:8787")
	if len(checks) != 2 {
		t.Fatalf("network diagnostic checks = %d, want 2", len(checks))
	}
	if checks[0].status != "ok" || !strings.Contains(checks[0].message, "private LAN listener") {
		t.Fatalf("private listener diagnostic = %#v", checks[0])
	}

	wantFirewall := "host firewall"
	if runtime.GOOS == "windows" {
		wantFirewall = "Windows Firewall"
	}
	if checks[1].status != "check" || !strings.Contains(checks[1].message, wantFirewall) {
		t.Fatalf("firewall diagnostic = %#v, want %q guidance", checks[1], wantFirewall)
	}
}

func TestRunDiagnosticsReportsAllHardFailures(t *testing.T) {
	workingDirectory := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(workingDirectory, nil, 0o600); err != nil {
		t.Fatalf("write working-directory fixture: %v", err)
	}

	var output bytes.Buffer
	err := runDiagnostics(startupOptions{
		addr:             "invalid-listener-address",
		webDir:           t.TempDir(),
		command:          []string{"vibebridge-command-that-does-not-exist"},
		workingDirectory: workingDirectory,
	}, false, &output)
	if err == nil || err.Error() != "3 diagnostic check(s) failed" {
		t.Fatalf("runDiagnostics error = %v, want three failed checks", err)
	}
	if failures := strings.Count(output.String(), "[fail]"); failures != 3 {
		t.Fatalf("diagnostic failures = %d, want 3:\n%s", failures, output.String())
	}
	for _, expected := range []string{"was not found in PATH", "is not a directory", "HTTP listener"} {
		if !strings.Contains(output.String(), expected) {
			t.Fatalf("diagnostic output did not contain %q:\n%s", expected, output.String())
		}
	}
}

func TestBackgroundServiceDiagnosticClassifiesInstallationState(t *testing.T) {
	cases := []struct {
		name       string
		status     agentservice.InstallationStatus
		err        error
		wantStatus string
		wantText   string
	}{
		{name: "installed", status: agentservice.InstallationStatus{Installed: true}, wantStatus: "ok", wantText: "is installed"},
		{name: "not installed", wantStatus: "check", wantText: "is not installed"},
		{name: "unsupported", err: agentservice.ErrUnsupported, wantStatus: "check", wantText: "not supported"},
		{name: "query error", err: errors.New("query failed"), wantStatus: "warn", wantText: "could not be inspected"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			check := backgroundServiceDiagnosticFor(testCase.status, testCase.err)
			if check.status != testCase.wantStatus || !strings.Contains(check.message, testCase.wantText) || check.err != nil {
				t.Fatalf("background service diagnostic = %#v", check)
			}
		})
	}
}

func TestResolveStartupOptionsUsesStructuredProfile(t *testing.T) {
	workspace := t.TempDir()
	configPath := filepath.Join(t.TempDir(), "config.json")
	content := `{
		"version": 2,
		"listen_address": "127.0.0.1:9000",
		"web_directory": "custom-web",
		"reconnect_timeout": "2m",
		"idle_timeout": "0s",
		"disable_legacy_protocol": true,
		"workspaces": [{"id":"repo","label":"Repository","root":"` + filepath.ToSlash(workspace) + `"}],
		"default_profile": "codex",
		"profiles": [{
			"id": "codex",
			"label": "Codex",
			"executable": "codex",
			"args": ["--model", "gpt 5"],
			"workspace_id": "repo",
			"environment_allowlist": ["PATH", "MISSING"]
		}]
	}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	lookup := func(name string) (string, bool) {
		if name == "PATH" {
			return "test-path", true
		}
		return "", false
	}

	options, err := resolveStartupOptions(startupOptions{
		addr: "0.0.0.0:8787", webDir: "web/dist", commandLine: "legacy",
		reconnectTimeout: 90 * time.Second, idleTimeout: 30 * time.Minute,
	}, configPath, "", map[string]bool{}, lookup)
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	if !reflect.DeepEqual(options.command, []string{"codex", "--model", "gpt 5"}) {
		t.Fatalf("command = %q, want structured profile arguments", options.command)
	}
	if options.profileID != "codex" || options.workspaceID != "repo" {
		t.Fatalf("profile/workspace ID = %q/%q, want codex/repo", options.profileID, options.workspaceID)
	}
	if options.workingDirectory != workspace {
		t.Fatalf("working directory = %q, want %q", options.workingDirectory, workspace)
	}
	if options.workspaceRoot != options.workingDirectory {
		t.Fatalf("workspace root = %q, want canonical root %q", options.workspaceRoot, options.workingDirectory)
	}
	if options.addr != "127.0.0.1:9000" || options.webDir != "custom-web" {
		t.Fatalf("configured address/web = %q/%q", options.addr, options.webDir)
	}
	if options.reconnectTimeout != 2*time.Minute || options.idleTimeout != 0 {
		t.Fatalf("configured timeouts = %v/%v", options.reconnectTimeout, options.idleTimeout)
	}
	if !options.disableLegacyProtocol {
		t.Fatal("configured disable_legacy_protocol was not applied")
	}
	if !reflect.DeepEqual(options.environment, []string{"PATH=test-path"}) {
		t.Fatalf("environment = %q, want allowlisted existing value", options.environment)
	}
}

func TestResolveStartupOptionsPreservesExplicitCLIOverrides(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"listen_address":"127.0.0.1:9000","web_directory":"configured-web","reconnect_timeout":"2m","idle_timeout":"0s","disable_legacy_protocol":true,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	options, err := resolveStartupOptions(startupOptions{
		addr:                  "127.0.0.1:7000",
		webDir:                "cli-web",
		commandLine:           "custom --arg",
		reconnectTimeout:      time.Minute,
		idleTimeout:           15 * time.Minute,
		disableLegacyProtocol: false,
	}, configPath, "", map[string]bool{
		"addr": true, "web-dir": true, "cmd": true, "reconnect-timeout": true, "idle-timeout": true, "disable-legacy-protocol": true,
	}, os.LookupEnv)
	if err != nil {
		t.Fatalf("resolve options: %v", err)
	}
	if options.addr != "127.0.0.1:7000" || options.webDir != "cli-web" {
		t.Fatalf("CLI address/web overrides = %q/%q", options.addr, options.webDir)
	}
	if options.reconnectTimeout != time.Minute || options.idleTimeout != 15*time.Minute {
		t.Fatalf("CLI timeout overrides = %v/%v", options.reconnectTimeout, options.idleTimeout)
	}
	if options.disableLegacyProtocol {
		t.Fatal("explicit CLI false did not override disable_legacy_protocol")
	}
	if !reflect.DeepEqual(options.command, []string{"custom", "--arg"}) {
		t.Fatalf("CLI command override = %q", options.command)
	}
	if options.profileID != "" || options.environment != nil {
		t.Fatalf("legacy command unexpectedly used profile state: profile=%q environment=%q", options.profileID, options.environment)
	}
}

func TestResolveStartupOptionsSelectsRequestedProfile(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"},{"id":"codex","label":"Codex","executable":"codex","args":["--help"]}]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	options, err := resolveStartupOptions(startupOptions{}, configPath, "codex", nil, os.LookupEnv)
	if err != nil {
		t.Fatalf("resolve requested profile: %v", err)
	}
	if options.profileID != "codex" || !reflect.DeepEqual(options.command, []string{"codex", "--help"}) {
		t.Fatalf("requested profile = %q/%q", options.profileID, options.command)
	}
}

func TestResolveStartupOptionsRejectsProfileConflicts(t *testing.T) {
	if _, err := resolveStartupOptions(startupOptions{}, "", "codex", nil, os.LookupEnv); err == nil {
		t.Fatal("profile without config was accepted")
	}

	configPath := filepath.Join(t.TempDir(), "config.json")
	content := `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`
	if err := os.WriteFile(configPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	if _, err := resolveStartupOptions(startupOptions{commandLine: "pwsh"}, configPath, "shell", map[string]bool{"cmd": true}, os.LookupEnv); err == nil {
		t.Fatal("explicit command and profile were accepted together")
	}
	if _, err := resolveStartupOptions(startupOptions{}, configPath, "missing", nil, os.LookupEnv); err == nil {
		t.Fatal("unknown profile was accepted")
	}
}

func TestValidateWorkingDirectory(t *testing.T) {
	if err := validateWorkingDirectory(t.TempDir()); err != nil {
		t.Fatalf("validate directory: %v", err)
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := validateWorkingDirectory(file); err == nil {
		t.Fatal("regular file accepted as working directory")
	}
}
