package agentconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/zzemy/VibeBridge/internal/workspace"
)

const (
	MinimumVersion = 1
	CurrentVersion = 2
	maxConfigBytes = 1024 * 1024
)

var (
	profileIDPattern       = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	environmentNamePattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)
)

type File struct {
	Version               int                    `json:"version"`
	ListenAddress         string                 `json:"listen_address,omitempty"`
	WebDirectory          string                 `json:"web_directory,omitempty"`
	ReconnectTimeout      string                 `json:"reconnect_timeout,omitempty"`
	IdleTimeout           string                 `json:"idle_timeout,omitempty"`
	DisableLegacyProtocol bool                   `json:"disable_legacy_protocol,omitempty"`
	Workspaces            []workspace.Definition `json:"workspaces,omitempty"`
	DefaultProfile        string                 `json:"default_profile"`
	Profiles              []LaunchProfile        `json:"profiles"`

	workspacesConfigured bool
}

type LaunchProfile struct {
	ID                   string   `json:"id"`
	Label                string   `json:"label"`
	Executable           string   `json:"executable"`
	Args                 []string `json:"args,omitempty"`
	WorkspaceID          string   `json:"workspace_id,omitempty"`
	WorkingDirectory     string   `json:"working_directory,omitempty"`
	EnvironmentAllowlist []string `json:"environment_allowlist,omitempty"`

	workspaceIDConfigured bool
}

func Load(path string) (File, error) {
	file, err := os.Open(path)
	if err != nil {
		return File{}, fmt.Errorf("open config %q: %w", path, err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return File{}, fmt.Errorf("stat config %q: %w", path, err)
	}
	if info.Size() > maxConfigBytes {
		return File{}, fmt.Errorf("config %q exceeds the %d byte limit", path, maxConfigBytes)
	}

	content, err := io.ReadAll(io.LimitReader(file, maxConfigBytes+1))
	if err != nil {
		return File{}, fmt.Errorf("read config %q: %w", path, err)
	}
	if len(content) > maxConfigBytes {
		return File{}, fmt.Errorf("config %q exceeds the %d byte limit", path, maxConfigBytes)
	}

	var config File
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&config); err != nil {
		return File{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := ensureJSONEnd(decoder); err != nil {
		return File{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := markWorkspaceFieldPresence(content, &config); err != nil {
		return File{}, fmt.Errorf("decode config %q: %w", path, err)
	}
	if err := config.validate(filepath.Dir(path)); err != nil {
		return File{}, fmt.Errorf("validate config %q: %w", path, err)
	}
	return config, nil
}

func markWorkspaceFieldPresence(content []byte, config *File) error {
	var fields struct {
		Workspaces json.RawMessage `json:"workspaces"`
		Profiles   []struct {
			WorkspaceID json.RawMessage `json:"workspace_id"`
		} `json:"profiles"`
	}
	if err := json.Unmarshal(content, &fields); err != nil {
		return err
	}
	config.workspacesConfigured = fields.Workspaces != nil
	for index := range fields.Profiles {
		if index < len(config.Profiles) {
			config.Profiles[index].workspaceIDConfigured = fields.Profiles[index].WorkspaceID != nil
		}
	}
	return nil
}

func ensureJSONEnd(decoder *json.Decoder) error {
	var extra json.RawMessage
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		if err == nil {
			return errors.New("multiple JSON values are not allowed")
		}
		return err
	}
	return nil
}

func (c File) Validate() error {
	copy := c
	return copy.validate("")
}

func (c *File) validate(baseDirectory string) error {
	if c.Version < MinimumVersion || c.Version > CurrentVersion {
		return fmt.Errorf("unsupported version %d; supported versions are %d through %d", c.Version, MinimumVersion, CurrentVersion)
	}
	if c.Version == MinimumVersion && c.usesWorkspaceFields() {
		return fmt.Errorf("workspaces and workspace_id require config version %d", CurrentVersion)
	}
	if strings.TrimSpace(c.DefaultProfile) == "" {
		return errors.New("default_profile must not be empty")
	}
	if len(c.Profiles) == 0 {
		return errors.New("profiles must contain at least one launch profile")
	}
	if err := validateDuration("reconnect_timeout", c.ReconnectTimeout, false); err != nil {
		return err
	}
	if err := validateDuration("idle_timeout", c.IdleTimeout, true); err != nil {
		return err
	}

	workspaceRegistry, err := workspace.NewRegistry(c.Workspaces, baseDirectory)
	if err != nil {
		return err
	}
	c.Workspaces = workspaceRegistry.Definitions()

	seen := make(map[string]struct{}, len(c.Profiles))
	defaultFound := false
	for index := range c.Profiles {
		profile := &c.Profiles[index]
		if err := profile.validate(baseDirectory, workspaceRegistry); err != nil {
			return fmt.Errorf("profiles[%d]: %w", index, err)
		}
		if _, exists := seen[profile.ID]; exists {
			return fmt.Errorf("duplicate profile id %q", profile.ID)
		}
		seen[profile.ID] = struct{}{}
		if profile.ID == c.DefaultProfile {
			defaultFound = true
		}
	}
	if !defaultFound {
		return fmt.Errorf("default_profile %q does not reference a configured profile", c.DefaultProfile)
	}
	return nil
}

func (c File) usesWorkspaceFields() bool {
	if c.workspacesConfigured || len(c.Workspaces) > 0 {
		return true
	}
	for _, profile := range c.Profiles {
		if profile.workspaceIDConfigured || profile.WorkspaceID != "" {
			return true
		}
	}
	return false
}

func (c File) Profile(id string) (LaunchProfile, bool) {
	for _, profile := range c.Profiles {
		if profile.ID == id {
			return profile, true
		}
	}
	return LaunchProfile{}, false
}

func (c File) Workspace(id string) (workspace.Definition, bool) {
	for _, definition := range c.Workspaces {
		if definition.ID == id {
			return definition, true
		}
	}
	return workspace.Definition{}, false
}

func (p *LaunchProfile) validate(baseDirectory string, workspaceRegistry workspace.Registry) error {
	if !profileIDPattern.MatchString(p.ID) {
		return fmt.Errorf("id %q must match %s", p.ID, profileIDPattern)
	}
	p.Label = strings.TrimSpace(p.Label)
	if p.Label == "" {
		return errors.New("label must not be empty")
	}
	p.Executable = strings.TrimSpace(p.Executable)
	if p.Executable == "" {
		return errors.New("executable must not be empty")
	}
	for index, arg := range p.Args {
		if strings.ContainsRune(arg, '\x00') {
			return fmt.Errorf("args[%d] contains a NUL byte", index)
		}
	}
	if p.WorkspaceID != "" {
		workingDirectory, err := workspaceRegistry.ResolveDirectory(p.WorkspaceID, p.WorkingDirectory)
		if err != nil {
			return fmt.Errorf("workspace_id %q: %w", p.WorkspaceID, err)
		}
		p.WorkingDirectory = workingDirectory
	} else if p.WorkingDirectory != "" {
		if baseDirectory != "" && !filepath.IsAbs(p.WorkingDirectory) {
			p.WorkingDirectory = filepath.Join(baseDirectory, p.WorkingDirectory)
		}
		absolute, err := filepath.Abs(p.WorkingDirectory)
		if err != nil {
			return fmt.Errorf("resolve working_directory: %w", err)
		}
		p.WorkingDirectory = filepath.Clean(absolute)
	}
	seenEnvironment := make(map[string]struct{}, len(p.EnvironmentAllowlist))
	for index, name := range p.EnvironmentAllowlist {
		if !environmentNamePattern.MatchString(name) {
			return fmt.Errorf("environment_allowlist[%d] %q is not a valid environment variable name", index, name)
		}
		key := strings.ToUpper(name)
		if _, exists := seenEnvironment[key]; exists {
			return fmt.Errorf("environment_allowlist contains duplicate %q", name)
		}
		seenEnvironment[key] = struct{}{}
	}
	return nil
}

func validateDuration(name string, value string, allowZero bool) error {
	if value == "" {
		return nil
	}
	duration, err := time.ParseDuration(value)
	if err != nil {
		return fmt.Errorf("%s %q is invalid: %w", name, value, err)
	}
	if duration < 0 || (!allowZero && duration == 0) {
		return fmt.Errorf("%s must be %s", name, map[bool]string{true: "zero or greater", false: "greater than zero"}[allowZero])
	}
	return nil
}

func (c File) ParsedReconnectTimeout() (time.Duration, bool) {
	if c.ReconnectTimeout == "" {
		return 0, false
	}
	duration, _ := time.ParseDuration(c.ReconnectTimeout)
	return duration, true
}

func (c File) ParsedIdleTimeout() (time.Duration, bool) {
	if c.IdleTimeout == "" {
		return 0, false
	}
	duration, _ := time.ParseDuration(c.IdleTimeout)
	return duration, true
}
