package main

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

type diagnosticCheck struct {
	status  string
	message string
	err     error
}

func runDiagnostics(options startupOptions, hasEmbeddedAssets bool, output io.Writer) error {
	checks := collectDiagnostics(options, hasEmbeddedAssets)
	failures := 0
	for _, check := range checks {
		if check.err == nil {
			fmt.Fprintf(output, "[%s] %s\n", check.status, check.message)
			continue
		}
		failures++
		fmt.Fprintf(output, "[fail] %s: %v\n", check.message, check.err)
	}
	if failures > 0 {
		return fmt.Errorf("%d diagnostic check(s) failed", failures)
	}
	return nil
}

func collectDiagnostics(options startupOptions, hasEmbeddedAssets bool) []diagnosticCheck {
	checks := []diagnosticCheck{platformDiagnostic()}

	commandMessage := "configured command is available"
	if options.profileID != "" {
		commandMessage = fmt.Sprintf("launch profile %q executable is available", options.profileID)
	}
	checks = append(checks, diagnosticCheck{
		status:  "ok",
		message: commandMessage,
		err:     validateCommand(options.command),
	})

	workingDirectoryMessage := "launch working directory uses the current directory"
	if options.workingDirectory != "" {
		workingDirectoryMessage = "launch working directory is available"
	}
	checks = append(checks, diagnosticCheck{
		status:  "ok",
		message: workingDirectoryMessage,
		err:     validateWorkingDirectory(options.workingDirectory),
	})

	listener, err := net.Listen("tcp", options.addr)
	if err == nil {
		_ = listener.Close()
	}
	checks = append(checks, diagnosticCheck{
		status:  "ok",
		message: fmt.Sprintf("%s is available for the HTTP listener", options.addr),
		err:     err,
	})

	switch {
	case hasEmbeddedAssets:
		checks = append(checks, diagnosticCheck{status: "ok", message: "frontend assets are embedded in this binary"})
	case fileExists(filepath.Join(options.webDir, "index.html")):
		checks = append(checks, diagnosticCheck{status: "ok", message: fmt.Sprintf("frontend build found in %s", options.webDir)})
	default:
		checks = append(checks, diagnosticCheck{status: "warn", message: fmt.Sprintf("frontend build not found in %s; run pnpm --dir web build", options.webDir)})
	}

	checks = append(checks, networkDiagnostics(options.addr)...)
	return checks
}

func platformDiagnostic() diagnosticCheck {
	platform := runtime.GOOS + "/" + runtime.GOARCH
	if runtime.GOOS == "windows" {
		return diagnosticCheck{status: "ok", message: fmt.Sprintf("host platform %s uses the supported Windows PTY process-tree adapter", platform)}
	}
	return diagnosticCheck{status: "warn", message: fmt.Sprintf("host platform %s is not yet a declared supported PTY platform", platform)}
}

func networkDiagnostics(addr string) []diagnosticCheck {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return []diagnosticCheck{{status: "warn", message: "listener exposure could not be classified because the address is invalid"}}
	}

	if isLoopbackHost(host) {
		return []diagnosticCheck{{status: "warn", message: "listener is loopback-only; phones on the LAN cannot connect"}}
	}

	checks := make([]diagnosticCheck, 0, 2)
	if host == "" || host == "0.0.0.0" || host == "::" {
		hosts := lanIPv4Hosts()
		if len(hosts) == 0 {
			checks = append(checks, diagnosticCheck{status: "warn", message: "no private LAN IPv4 address was detected"})
		} else {
			for _, lanHost := range hosts {
				checks = append(checks, diagnosticCheck{status: "ok", message: fmt.Sprintf("private LAN address detected: %s", lanHost)})
			}
		}
	} else if ip := net.ParseIP(host); ip != nil && ip.IsPrivate() {
		checks = append(checks, diagnosticCheck{status: "ok", message: fmt.Sprintf("private LAN listener address configured: %s", host)})
	} else {
		checks = append(checks, diagnosticCheck{status: "warn", message: "listener is not a private or loopback IP; use VibeBridge only on a trusted private network"})
	}

	if runtime.GOOS == "windows" {
		checks = append(checks, diagnosticCheck{status: "check", message: "Windows Firewall must allow private-network access to the selected executable"})
	} else {
		checks = append(checks, diagnosticCheck{status: "check", message: "the host firewall must allow private-network access to the selected executable"})
	}
	return checks
}

func isWildcardAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && (host == "0.0.0.0" || host == "::")
}

func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func validateWorkingDirectory(path string) error {
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("working directory %q is not available: %w", path, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("working directory %q is not a directory", path)
	}
	return nil
}

func validateCommand(command []string) error {
	if len(command) == 0 {
		return fmt.Errorf("cmd must not be empty")
	}
	if _, err := exec.LookPath(command[0]); err != nil {
		return fmt.Errorf("command %q was not found in PATH", command[0])
	}
	return nil
}
