//go:build windows

package agentservice

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type fakeExitError int

func (err fakeExitError) Error() string { return "task command failed" }
func (err fakeExitError) ExitCode() int { return int(err) }

func TestInstallWindowsCreatesLeastPrivilegeHiddenLogonTask(t *testing.T) {
	root := t.TempDir()
	options := InstallOptions{
		Executable:       filepath.Join(root, "VibeBridge App", "vibebridge.exe"),
		ConfigPath:       filepath.Join(root, "config file.json"),
		ProfileID:        "codex",
		RuntimeStatePath: filepath.Join(root, "state", "runtime.json"),
		WorkingDirectory: root,
	}
	var calls [][]string
	var definition string
	runner := func(args ...string) ([]byte, error) {
		calls = append(calls, append([]string(nil), args...))
		switch args[0] {
		case "/Query":
			return nil, fakeExitError(1)
		case "/Create":
			content, err := os.ReadFile(args[4])
			if err != nil {
				t.Fatalf("read temporary task definition: %v", err)
			}
			definition = string(content)
		}
		return nil, nil
	}
	if err := installWindows(options, runner); err != nil {
		t.Fatalf("install Windows task: %v", err)
	}
	if len(calls) != 3 || calls[1][0] != "/Create" || calls[2][0] != "/Run" {
		t.Fatalf("task scheduler calls = %#v, want query/create/run", calls)
	}
	for _, expected := range []string{
		"<LogonType>InteractiveToken</LogonType>",
		"<RunLevel>LeastPrivilege</RunLevel>",
		"<Hidden>true</Hidden>",
		"<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>",
		"<RestartOnFailure>",
		"<Interval>PT1M</Interval>",
		"<Count>3</Count>",
		"<Command>" + options.Executable + "</Command>",
		`--background --config &#34;` + options.ConfigPath + `&#34;`,
		`--service-state ` + options.RuntimeStatePath,
		`--profile codex`,
	} {
		if !strings.Contains(definition, expected) {
			t.Fatalf("task definition does not contain %q:\n%s", expected, definition)
		}
	}
}

func TestInstallWindowsDoesNotReplaceExistingTaskWithoutForce(t *testing.T) {
	root := t.TempDir()
	options := InstallOptions{
		Executable:       filepath.Join(root, "vibebridge.exe"),
		ConfigPath:       filepath.Join(root, "config.json"),
		RuntimeStatePath: filepath.Join(root, "runtime.json"),
		WorkingDirectory: root,
	}
	calls := 0
	runner := func(args ...string) ([]byte, error) {
		calls++
		return nil, nil
	}
	if err := installWindows(options, runner); err == nil || !strings.Contains(err.Error(), "already installed") {
		t.Fatalf("existing task error = %v", err)
	}
	if calls != 1 {
		t.Fatalf("task scheduler calls = %d, want query only", calls)
	}
}

func TestInstallWindowsForceStopsExistingTaskBeforeReplacement(t *testing.T) {
	root := t.TempDir()
	options := InstallOptions{
		Executable:       filepath.Join(root, "vibebridge.exe"),
		ConfigPath:       filepath.Join(root, "config.json"),
		RuntimeStatePath: filepath.Join(root, "runtime.json"),
		WorkingDirectory: root,
		Force:            true,
	}
	var actions []string
	runner := func(args ...string) ([]byte, error) {
		actions = append(actions, args[0])
		return nil, nil
	}
	if err := installWindows(options, runner); err != nil {
		t.Fatalf("force install Windows task: %v", err)
	}
	want := []string{"/Query", "/End", "/Create", "/Run"}
	if strings.Join(actions, ",") != strings.Join(want, ",") {
		t.Fatalf("task scheduler actions = %v, want %v", actions, want)
	}
}

func TestQuoteWindowsArgumentPreservesSpacesQuotesAndTrailingSlashes(t *testing.T) {
	cases := map[string]string{
		"plain":             "plain",
		"two words":         `"two words"`,
		`value"quoted`:      `"value\"quoted"`,
		`C:\Program Files\`: `"C:\Program Files\\"`,
		"":                  `""`,
	}
	for input, want := range cases {
		if got := quoteWindowsArgument(input); got != want {
			t.Fatalf("quoteWindowsArgument(%q) = %q, want %q", input, got, want)
		}
	}
}
