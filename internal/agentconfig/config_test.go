package agentconfig

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLoadValidConfigAndResolveProfile(t *testing.T) {
	path := writeConfig(t, `{
		"version": 1,
		"listen_address": "127.0.0.1:8787",
		"reconnect_timeout": "2m",
		"idle_timeout": "0s",
		"disable_legacy_protocol": true,
		"default_profile": "codex",
		"profiles": [{
			"id": "codex",
			"label": "Codex",
			"executable": "codex",
			"args": ["--model", "gpt 5"],
			"working_directory": ".",
			"environment_allowlist": ["PATH", "USERPROFILE"]
		}]
	}`)

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	profile, ok := config.Profile("codex")
	if !ok {
		t.Fatal("default profile not found")
	}
	if profile.Args[1] != "gpt 5" {
		t.Fatalf("structured argument = %q, want %q", profile.Args[1], "gpt 5")
	}
	if !filepath.IsAbs(profile.WorkingDirectory) {
		t.Fatalf("working directory = %q, want absolute path", profile.WorkingDirectory)
	}
	if profile.WorkingDirectory != filepath.Dir(path) {
		t.Fatalf("working directory = %q, want config directory %q", profile.WorkingDirectory, filepath.Dir(path))
	}
	if timeout, ok := config.ParsedReconnectTimeout(); !ok || timeout != 2*time.Minute {
		t.Fatalf("reconnect timeout = %v/%t, want 2m/true", timeout, ok)
	}
	if !config.DisableLegacyProtocol {
		t.Fatal("disable_legacy_protocol was not loaded")
	}
}

func TestLoadRejectsInvalidConfigBoundaries(t *testing.T) {
	cases := map[string]string{
		"unknown version":       `{"version":2,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"unknown field":         `{"version":1,"unknown":true,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"duplicate id":          `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"},{"id":"shell","label":"Other","executable":"cmd"}]}`,
		"missing default":       `{"version":1,"default_profile":"codex","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"invalid profile id":    `{"version":1,"default_profile":"Bad ID","profiles":[{"id":"Bad ID","label":"Shell","executable":"pwsh"}]}`,
		"invalid environment":   `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","environment_allowlist":["BAD=NAME"]}]}`,
		"duplicate environment": `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","environment_allowlist":["PATH","Path"]}]}`,
		"zero reconnect":        `{"version":1,"reconnect_timeout":"0s","default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"negative idle":         `{"version":1,"idle_timeout":"-1s","default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"multiple values":       `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]} {}`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, content)); err == nil {
				t.Fatal("invalid config loaded successfully")
			}
		})
	}
}

func TestLoadLimitsConfigSize(t *testing.T) {
	content := strings.Repeat(" ", 1024*1024) + `{}`
	if _, err := Load(writeConfig(t, content)); err == nil {
		t.Fatal("oversized config loaded successfully")
	}
}

func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestLoadWorkspaceProfileUsesCanonicalWorkspaceBoundary(t *testing.T) {
	configDirectory := t.TempDir()
	workspaceRoot := filepath.Join(configDirectory, "工作区")
	workingDirectory := filepath.Join(workspaceRoot, "src")
	if err := os.MkdirAll(workingDirectory, 0o700); err != nil {
		t.Fatalf("create workspace directories: %v", err)
	}
	path := filepath.Join(configDirectory, "config.json")
	content := `{
		"version": 1,
		"workspaces": [{"id":"repo","label":"  Main Repo  ","root":"工作区"}],
		"default_profile": "codex",
		"profiles": [{
			"id": "codex",
			"label": "Codex",
			"executable": "codex",
			"workspace_id": "repo",
			"working_directory": "src"
		}]
	}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	workspace, ok := config.Workspace("repo")
	if !ok {
		t.Fatal("workspace was not found")
	}
	wantRoot, err := filepath.EvalSymlinks(workspaceRoot)
	if err != nil {
		t.Fatalf("canonicalize expected workspace root: %v", err)
	}
	if workspace.Root != filepath.Clean(wantRoot) || workspace.Label != "Main Repo" {
		t.Fatalf("workspace = %#v, want canonical root %q and trimmed label", workspace, filepath.Clean(wantRoot))
	}
	profile, ok := config.Profile("codex")
	if !ok {
		t.Fatal("profile was not found")
	}
	wantWorkingDirectory, err := filepath.EvalSymlinks(workingDirectory)
	if err != nil {
		t.Fatalf("canonicalize expected working directory: %v", err)
	}
	if profile.WorkspaceID != "repo" || profile.WorkingDirectory != filepath.Clean(wantWorkingDirectory) {
		t.Fatalf("profile workspace/working directory = %q/%q, want repo/%q", profile.WorkspaceID, profile.WorkingDirectory, filepath.Clean(wantWorkingDirectory))
	}
}

func TestLoadWorkspaceProfileDefaultsWorkingDirectoryToRoot(t *testing.T) {
	configDirectory := t.TempDir()
	workspaceRoot := filepath.Join(configDirectory, "repo")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}
	path := filepath.Join(configDirectory, "config.json")
	content := `{"version":1,"workspaces":[{"id":"repo","label":"Repo","root":"repo"}],"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","workspace_id":"repo"}]}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	config, err := Load(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	workspace, _ := config.Workspace("repo")
	profile, _ := config.Profile("shell")
	if profile.WorkingDirectory != workspace.Root {
		t.Fatalf("profile working directory = %q, want workspace root %q", profile.WorkingDirectory, workspace.Root)
	}
}

func TestLoadRejectsInvalidWorkspaceBindings(t *testing.T) {
	configDirectory := t.TempDir()
	workspaceRoot := filepath.Join(configDirectory, "repo")
	if err := os.Mkdir(workspaceRoot, 0o700); err != nil {
		t.Fatalf("create workspace: %v", err)
	}

	cases := map[string]string{
		"duplicate workspace id":      `{"version":1,"workspaces":[{"id":"repo","label":"Repo","root":"repo"},{"id":"repo","label":"Other","root":"."}],"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"duplicate canonical root":    `{"version":1,"workspaces":[{"id":"repo","label":"Repo","root":"repo"},{"id":"other","label":"Other","root":"repo/."}],"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh"}]}`,
		"unknown workspace":           `{"version":1,"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","workspace_id":"missing"}]}`,
		"working directory traversal": `{"version":1,"workspaces":[{"id":"repo","label":"Repo","root":"repo"}],"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","workspace_id":"repo","working_directory":".."}]}`,
		"missing workspace root":      `{"version":1,"workspaces":[{"id":"repo","label":"Repo","root":"missing"}],"default_profile":"shell","profiles":[{"id":"shell","label":"Shell","executable":"pwsh","workspace_id":"repo"}]}`,
	}

	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(configDirectory, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
				t.Fatalf("write config: %v", err)
			}
			if _, err := Load(path); err == nil {
				t.Fatal("invalid workspace configuration loaded successfully")
			}
		})
	}
}
