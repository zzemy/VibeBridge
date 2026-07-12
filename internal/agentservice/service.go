package agentservice

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
)

const TaskName = "VibeBridge Agent"

const taskURI = `\VibeBridge\Agent`

var ErrUnsupported = errors.New("user-scoped background service is not supported on this platform")

type InstallOptions struct {
	Executable       string
	ConfigPath       string
	ProfileID        string
	RuntimeStatePath string
	WorkingDirectory string
	Force            bool
}

type InstallationStatus struct {
	Installed bool
}

func (options InstallOptions) validate() error {
	if !filepath.IsAbs(options.Executable) {
		return errors.New("service executable path must be absolute")
	}
	if !filepath.IsAbs(options.ConfigPath) {
		return errors.New("service config path must be absolute")
	}
	if !filepath.IsAbs(options.RuntimeStatePath) {
		return errors.New("service runtime state path must be absolute")
	}
	if options.WorkingDirectory == "" || !filepath.IsAbs(options.WorkingDirectory) {
		return errors.New("service working directory must be absolute")
	}
	for name, value := range map[string]string{
		"executable":        options.Executable,
		"config":            options.ConfigPath,
		"runtime state":     options.RuntimeStatePath,
		"working directory": options.WorkingDirectory,
		"profile":           options.ProfileID,
	} {
		if strings.ContainsRune(value, '\x00') {
			return fmt.Errorf("service %s contains a NUL byte", name)
		}
	}
	return nil
}
