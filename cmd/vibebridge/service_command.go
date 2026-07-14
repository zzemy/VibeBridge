package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/zzemy/VibeBridge/internal/agentservice"
)

type serviceManager interface {
	Install(agentservice.InstallOptions) error
	Uninstall() error
	QueryInstallation() (agentservice.InstallationStatus, error)
	RuntimeStatePath() (string, error)
}

type platformServiceManager struct{}

func (platformServiceManager) Install(options agentservice.InstallOptions) error {
	return agentservice.Install(options)
}

func (platformServiceManager) Uninstall() error {
	return agentservice.Uninstall()
}

func (platformServiceManager) QueryInstallation() (agentservice.InstallationStatus, error) {
	return agentservice.QueryInstallation()
}

func (platformServiceManager) RuntimeStatePath() (string, error) {
	return agentservice.DefaultRuntimeStatePath()
}

func runServiceCommand(args []string, output io.Writer, errorOutput io.Writer, manager serviceManager, executable string) error {
	if len(args) == 0 {
		writeServiceUsage(errorOutput)
		return errors.New("service command requires install, status, or uninstall")
	}
	switch args[0] {
	case "help", "-h", "--help":
		writeServiceUsage(output)
		return nil
	case "install":
		return runServiceInstall(args[1:], output, errorOutput, manager, executable)
	case "status":
		return runServiceStatus(args[1:], output, errorOutput, manager)
	case "uninstall":
		return runServiceUninstall(args[1:], output, errorOutput, manager)
	default:
		return fmt.Errorf("unknown service command %q; expected install, status, or uninstall", args[0])
	}
}

func writeServiceUsage(output io.Writer) {
	fmt.Fprintln(output, "Usage: vibebridge service <install|status|uninstall> [options]")
	fmt.Fprintln(output, "  install   create and start the user-scoped background Agent")
	fmt.Fprintln(output, "  status    probe the installed Agent and show connection URLs")
	fmt.Fprintln(output, "  uninstall stop and remove the background Agent task")
}

func runServiceInstall(args []string, output io.Writer, errorOutput io.Writer, manager serviceManager, executable string) error {
	flags := flag.NewFlagSet("service install", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	configPath := flags.String("config", "", "absolute or relative path to the versioned Agent configuration")
	profileID := flags.String("profile", "", "optional launch profile ID")
	force := flags.Bool("force", false, "replace an existing VibeBridge background task")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("service install does not accept positional arguments: %v", flags.Args())
	}
	if *configPath == "" {
		return errors.New("service install requires --config")
	}

	absoluteConfig, err := filepath.Abs(*configPath)
	if err != nil {
		return fmt.Errorf("resolve service config path: %w", err)
	}
	launchOptions, err := resolveStartupOptions(startupOptions{
		addr:             "0.0.0.0:8787",
		webDir:           "web/dist",
		commandLine:      defaultCommandLine(),
		reconnectTimeout: 90 * time.Second,
		idleTimeout:      30 * time.Minute,
	}, absoluteConfig, *profileID, nil, os.LookupEnv)
	if err != nil {
		return err
	}
	if err := validateCommand(launchOptions.command); err != nil {
		return err
	}
	if err := validateStartupWorkingDirectory(launchOptions); err != nil {
		return err
	}
	absoluteExecutable, err := filepath.Abs(executable)
	if err != nil {
		return fmt.Errorf("resolve service executable path: %w", err)
	}
	if isTemporaryGoRunExecutable(absoluteExecutable) {
		return errors.New("service install cannot use the temporary `go run` executable; build a durable VibeBridge binary first")
	}
	info, err := os.Stat(absoluteExecutable)
	if err != nil {
		return fmt.Errorf("service executable is not available: %w", err)
	}
	if info.IsDir() {
		return errors.New("service executable path points to a directory")
	}
	runtimeStatePath, err := manager.RuntimeStatePath()
	if err != nil {
		return fmt.Errorf("resolve service runtime state path: %w", err)
	}

	options := agentservice.InstallOptions{
		Executable:       absoluteExecutable,
		ConfigPath:       absoluteConfig,
		ProfileID:        *profileID,
		RuntimeStatePath: runtimeStatePath,
		WorkingDirectory: filepath.Dir(absoluteConfig),
		Force:            *force,
	}
	if err := manager.Install(options); err != nil {
		return err
	}
	fmt.Fprintln(output, "Installed the user-scoped VibeBridge background Agent and requested an immediate start.")
	fmt.Fprintln(output, "Use the VibeBridge tray icon, or run `vibebridge service status --qr` as a terminal fallback.")
	return nil
}

func runServiceStatus(args []string, output io.Writer, errorOutput io.Writer, manager serviceManager) error {
	flags := flag.NewFlagSet("service status", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	showQR := flags.Bool("qr", false, "display a QR code for the current connection URL")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("service status does not accept positional arguments: %v", flags.Args())
	}

	installation, err := manager.QueryInstallation()
	if err != nil {
		return err
	}
	if !installation.Installed {
		fmt.Fprintln(output, "VibeBridge background Agent: not installed")
		return nil
	}

	statePath, err := manager.RuntimeStatePath()
	if err != nil {
		return fmt.Errorf("resolve service runtime state path: %w", err)
	}
	state, err := agentservice.LoadRuntimeState(statePath)
	if errors.Is(err, os.ErrNotExist) {
		fmt.Fprintln(output, "VibeBridge background Agent: installed, but not running")
		return nil
	}
	if err != nil {
		return fmt.Errorf("read background Agent runtime state: %w", err)
	}
	if err := probeRuntimeState(state); err != nil {
		fmt.Fprintf(output, "VibeBridge background Agent: installed, but its runtime state is stale (%v)\n", err)
		return nil
	}

	fmt.Fprintf(output, "VibeBridge background Agent: running (PID %d, started %s)\n", state.PID, state.StartedAt.Local().Format(time.RFC3339))
	urls, err := accessURLs(state.ListenAddress, state.SessionToken)
	if err != nil {
		return fmt.Errorf("build background Agent access URLs: %w", err)
	}
	for _, accessURL := range urls {
		fmt.Fprintf(output, "Open: %s\n", accessURL)
	}
	if *showQR && len(urls) > 0 {
		fmt.Fprintln(output, "Scan this QR code from your phone:")
		qrterminal.GenerateHalfBlock(urls[0], qrterminal.L, output)
	}
	return nil
}

func runServiceUninstall(args []string, output io.Writer, errorOutput io.Writer, manager serviceManager) error {
	flags := flag.NewFlagSet("service uninstall", flag.ContinueOnError)
	flags.SetOutput(errorOutput)
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("service uninstall does not accept positional arguments: %v", flags.Args())
	}
	if err := manager.Uninstall(); err != nil {
		return err
	}
	statePath, err := manager.RuntimeStatePath()
	if err != nil {
		return fmt.Errorf("background task was removed, but its runtime state path could not be resolved: %w", err)
	}
	if err := agentservice.RemoveRuntimeState(statePath); err != nil {
		return fmt.Errorf("background task was removed, but its runtime state could not be cleared: %w", err)
	}
	fmt.Fprintln(output, "Uninstalled the user-scoped VibeBridge background Agent.")
	return nil
}

func isTemporaryGoRunExecutable(path string) bool {
	temporaryDirectory, err := filepath.Abs(os.TempDir())
	if err != nil {
		return false
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	relative, err := filepath.Rel(temporaryDirectory, absolutePath)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(relative), "/") {
		if strings.HasPrefix(strings.ToLower(part), "go-build") {
			return true
		}
	}
	return false
}

func probeRuntimeState(state agentservice.RuntimeState) error {
	host, port, err := net.SplitHostPort(state.ListenAddress)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	endpoint := url.URL{Scheme: "http", Host: net.JoinHostPort(host, port), Path: "/status"}
	query := endpoint.Query()
	query.Set("token", state.SessionToken)
	endpoint.RawQuery = query.Encode()

	client := http.Client{Timeout: time.Second}
	response, err := client.Get(endpoint.String())
	if err != nil {
		return errors.New("authenticated status endpoint is unreachable")
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("status endpoint returned HTTP %d", response.StatusCode)
	}
	return nil
}
