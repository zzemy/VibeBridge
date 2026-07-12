//go:build windows

package agentservice

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"
)

type fakeExitError int

func (err fakeExitError) Error() string { return "task command failed" }
func (err fakeExitError) ExitCode() int { return int(err) }

func decodeTaskDefinition(t *testing.T, content []byte) string {
	t.Helper()
	if len(content) < 2 || content[0] != 0xff || content[1] != 0xfe {
		t.Fatalf("task definition does not start with a UTF-16LE BOM: % x", content[:min(len(content), 4)])
	}
	if (len(content)-2)%2 != 0 {
		t.Fatalf("UTF-16LE task definition has odd byte length: %d", len(content))
	}
	codeUnits := make([]uint16, (len(content)-2)/2)
	for index := range codeUnits {
		codeUnits[index] = binary.LittleEndian.Uint16(content[2+index*2:])
	}
	definition := string(utf16.Decode(codeUnits))
	if !strings.HasPrefix(definition, `<?xml version="1.0" encoding="UTF-16"?>`) {
		t.Fatalf("task definition has an unexpected XML declaration: %q", definition[:min(len(definition), 80)])
	}
	return definition
}

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
			definition = decodeTaskDefinition(t, content)
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
