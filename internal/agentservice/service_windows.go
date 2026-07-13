//go:build windows

package agentservice

import (
	"encoding/binary"
	"encoding/xml"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strings"
	"unicode/utf16"
)

type taskCommandRunner func(args ...string) ([]byte, error)

func Install(options InstallOptions) error {
	return installWindows(options, runTaskScheduler)
}

func Uninstall() error {
	return uninstallWindows(runTaskScheduler)
}

func QueryInstallation() (InstallationStatus, error) {
	return queryWindowsInstallation(runTaskScheduler)
}

func installWindows(options InstallOptions, run taskCommandRunner) error {
	if err := options.validate(); err != nil {
		return err
	}
	definition, err := buildTaskDefinition(options)
	if err != nil {
		return err
	}
	status, err := queryWindowsInstallation(run)
	if err != nil {
		return err
	}
	if status.Installed && !options.Force {
		return errors.New("the VibeBridge background task is already installed; use --force to replace it")
	}
	if status.Installed {
		if output, err := run("/End", "/TN", TaskName); err != nil && !hasExitCode(err, 1) {
			return taskCommandError("stop existing", output, err)
		}
	}
	temporary, err := os.CreateTemp("", "vibebridge-task-*.xml")
	if err != nil {
		return fmt.Errorf("create temporary task definition: %w", err)
	}
	temporaryPath := temporary.Name()
	defer os.Remove(temporaryPath)
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("restrict temporary task definition: %w", err)
	}
	if _, err := temporary.Write(definition); err != nil {
		_ = temporary.Close()
		return fmt.Errorf("write temporary task definition: %w", err)
	}
	if err := temporary.Close(); err != nil {
		return fmt.Errorf("close temporary task definition: %w", err)
	}

	if output, err := run("/Create", "/TN", TaskName, "/XML", temporaryPath, "/F"); err != nil {
		return taskCommandError("create", output, err)
	}
	if output, err := run("/Run", "/TN", TaskName); err != nil {
		return taskCommandError("start", output, err)
	}
	return nil
}

func uninstallWindows(run taskCommandRunner) error {
	status, err := queryWindowsInstallation(run)
	if err != nil {
		return err
	}
	if !status.Installed {
		return nil
	}
	if output, err := run("/End", "/TN", TaskName); err != nil && !hasExitCode(err, 1) {
		return taskCommandError("stop", output, err)
	}
	if output, err := run("/Delete", "/TN", TaskName, "/F"); err != nil {
		return taskCommandError("delete", output, err)
	}
	return nil
}

func queryWindowsInstallation(run taskCommandRunner) (InstallationStatus, error) {
	output, err := run("/Query", "/TN", TaskName)
	if err == nil {
		return InstallationStatus{Installed: true}, nil
	}
	if hasExitCode(err, 1) {
		return InstallationStatus{}, nil
	}
	return InstallationStatus{}, taskCommandError("query", output, err)
}

type exitCoder interface {
	ExitCode() int
}

func hasExitCode(err error, code int) bool {
	var coder exitCoder
	return errors.As(err, &coder) && coder.ExitCode() == code
}

func runTaskScheduler(args ...string) ([]byte, error) {
	return exec.Command("schtasks.exe", args...).CombinedOutput()
}

func taskCommandError(action string, output []byte, err error) error {
	detail := strings.TrimSpace(string(output))
	if detail == "" {
		return fmt.Errorf("%s VibeBridge background task: %w", action, err)
	}
	return fmt.Errorf("%s VibeBridge background task: %w: %s", action, err, detail)
}

func buildTaskDefinition(options InstallOptions) ([]byte, error) {
	currentUser, err := user.Current()
	if err != nil {
		return nil, fmt.Errorf("resolve current Windows user: %w", err)
	}
	if currentUser.Uid == "" {
		return nil, errors.New("resolve current Windows user: SID is empty")
	}

	arguments := []string{"--background", "--config", options.ConfigPath, "--service-state", options.RuntimeStatePath}
	if options.ProfileID != "" {
		arguments = append(arguments, "--profile", options.ProfileID)
	}
	encodedArguments := make([]string, len(arguments))
	for index, argument := range arguments {
		encodedArguments[index] = quoteWindowsArgument(argument)
	}

	definition := scheduledTask{
		Version: "1.4",
		XMLNS:   "http://schemas.microsoft.com/windows/2004/02/mit/task",
		RegistrationInfo: taskRegistrationInfo{
			Description: "Starts the user-scoped VibeBridge Local Agent at sign-in.",
			URI:         taskURI,
		},
		Triggers: taskTriggers{LogonTrigger: taskLogonTrigger{Enabled: true, UserID: currentUser.Uid}},
		Principals: taskPrincipals{Principal: taskPrincipal{
			ID:        "Author",
			UserID:    currentUser.Uid,
			LogonType: "InteractiveToken",
			RunLevel:  "LeastPrivilege",
		}},
		Settings: taskSettings{
			MultipleInstancesPolicy:    "IgnoreNew",
			DisallowStartIfOnBatteries: false,
			StopIfGoingOnBatteries:     false,
			AllowHardTerminate:         true,
			StartWhenAvailable:         true,
			RunOnlyIfNetworkAvailable:  false,
			AllowStartOnDemand:         true,
			Enabled:                    true,
			Hidden:                     true,
			RunOnlyIfIdle:              false,
			WakeToRun:                  false,
			ExecutionTimeLimit:         "PT0S",
			Priority:                   7,
			RestartOnFailure: taskRestartOnFailure{
				Interval: "PT1M",
				Count:    3,
			},
		},
		Actions: taskActions{
			Context: "Author",
			Exec: taskExec{
				Command:          options.Executable,
				Arguments:        strings.Join(encodedArguments, " "),
				WorkingDirectory: options.WorkingDirectory,
			},
		},
	}
	content, err := xml.MarshalIndent(definition, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("encode task definition: %w", err)
	}
	header := `<?xml version="1.0" encoding="UTF-16"?>` + "\r\n"
	codeUnits := utf16.Encode([]rune(header + string(content)))
	encoded := make([]byte, 2+len(codeUnits)*2)
	encoded[0], encoded[1] = 0xff, 0xfe
	for index, codeUnit := range codeUnits {
		binary.LittleEndian.PutUint16(encoded[2+index*2:], codeUnit)
	}
	return encoded, nil
}

func quoteWindowsArgument(value string) string {
	if value != "" && !strings.ContainsAny(value, " \t\n\v\"") {
		return value
	}
	var result strings.Builder
	result.WriteByte('"')
	backslashes := 0
	for _, character := range value {
		switch character {
		case '\\':
			backslashes++
		case '"':
			result.WriteString(strings.Repeat("\\", backslashes*2+1))
			result.WriteRune(character)
			backslashes = 0
		default:
			result.WriteString(strings.Repeat("\\", backslashes))
			backslashes = 0
			result.WriteRune(character)
		}
	}
	result.WriteString(strings.Repeat("\\", backslashes*2))
	result.WriteByte('"')
	return result.String()
}

type scheduledTask struct {
	XMLName          xml.Name             `xml:"Task"`
	Version          string               `xml:"version,attr"`
	XMLNS            string               `xml:"xmlns,attr"`
	RegistrationInfo taskRegistrationInfo `xml:"RegistrationInfo"`
	Triggers         taskTriggers         `xml:"Triggers"`
	Principals       taskPrincipals       `xml:"Principals"`
	Settings         taskSettings         `xml:"Settings"`
	Actions          taskActions          `xml:"Actions"`
}

type taskRegistrationInfo struct {
	Description string `xml:"Description"`
	URI         string `xml:"URI"`
}

type taskTriggers struct {
	LogonTrigger taskLogonTrigger `xml:"LogonTrigger"`
}

type taskLogonTrigger struct {
	Enabled bool   `xml:"Enabled"`
	UserID  string `xml:"UserId"`
}

type taskPrincipals struct {
	Principal taskPrincipal `xml:"Principal"`
}

type taskPrincipal struct {
	ID        string `xml:"id,attr"`
	UserID    string `xml:"UserId"`
	LogonType string `xml:"LogonType"`
	RunLevel  string `xml:"RunLevel"`
}

type taskSettings struct {
	MultipleInstancesPolicy    string               `xml:"MultipleInstancesPolicy"`
	DisallowStartIfOnBatteries bool                 `xml:"DisallowStartIfOnBatteries"`
	StopIfGoingOnBatteries     bool                 `xml:"StopIfGoingOnBatteries"`
	AllowHardTerminate         bool                 `xml:"AllowHardTerminate"`
	StartWhenAvailable         bool                 `xml:"StartWhenAvailable"`
	RunOnlyIfNetworkAvailable  bool                 `xml:"RunOnlyIfNetworkAvailable"`
	AllowStartOnDemand         bool                 `xml:"AllowStartOnDemand"`
	Enabled                    bool                 `xml:"Enabled"`
	Hidden                     bool                 `xml:"Hidden"`
	RunOnlyIfIdle              bool                 `xml:"RunOnlyIfIdle"`
	WakeToRun                  bool                 `xml:"WakeToRun"`
	ExecutionTimeLimit         string               `xml:"ExecutionTimeLimit"`
	Priority                   int                  `xml:"Priority"`
	RestartOnFailure           taskRestartOnFailure `xml:"RestartOnFailure"`
}

type taskRestartOnFailure struct {
	Interval string `xml:"Interval"`
	Count    int    `xml:"Count"`
}

type taskActions struct {
	Context string   `xml:"Context,attr"`
	Exec    taskExec `xml:"Exec"`
}

type taskExec struct {
	Command          string `xml:"Command"`
	Arguments        string `xml:"Arguments"`
	WorkingDirectory string `xml:"WorkingDirectory"`
}
