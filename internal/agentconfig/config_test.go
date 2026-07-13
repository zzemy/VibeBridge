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
