package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	"github.com/zzemy/VibeBridge/internal/agentconfig"
	"github.com/zzemy/VibeBridge/internal/server"
)

func main() {
	addr := flag.String("addr", "0.0.0.0:8787", "HTTP listen address")
	webDir := flag.String("web-dir", "web/dist", "frontend static build directory")
	commandLine := flag.String("cmd", defaultCommandLine(), "command to run for each WebSocket session")
	reconnectTimeout := flag.Duration("reconnect-timeout", 90*time.Second, "how long to keep a detached PTY session alive")
	idleTimeout := flag.Duration("idle-timeout", 30*time.Minute, "how long to keep a PTY session alive without input; set 0 to disable")
	configPath := flag.String("config", "", "path to a versioned local Agent configuration file")
	profileID := flag.String("profile", "", "launch profile ID from --config")
	diagnose := flag.Bool("diagnose", false, "check command, network listener, and frontend assets without starting a session")
	flag.Parse()

	explicitFlags := make(map[string]bool)
	flag.Visit(func(value *flag.Flag) { explicitFlags[value.Name] = true })
	options, err := resolveStartupOptions(startupOptions{
		addr:             *addr,
		webDir:           *webDir,
		commandLine:      *commandLine,
		reconnectTimeout: *reconnectTimeout,
		idleTimeout:      *idleTimeout,
	}, *configPath, *profileID, explicitFlags, os.LookupEnv)
	if err != nil {
		log.Fatal(err)
	}
	if err := validateCommand(options.command); err != nil {
		log.Fatal(err)
	}
	if err := validateWorkingDirectory(options.workingDirectory); err != nil {
		log.Fatal(err)
	}
	staticFS := embeddedWebFS()
	if *diagnose {
		if err := runDiagnostics(options.addr, options.webDir, staticFS != nil, options.profileID); err != nil {
			log.Fatal(err)
		}
		return
	}

	token, err := newSessionToken()
	if err != nil {
		log.Fatalf("create session token: %v", err)
	}

	app := server.New(server.Config{
		SessionToken:     token,
		WebDir:           options.webDir,
		StaticFS:         staticFS,
		Command:          options.command,
		WorkingDirectory: options.workingDirectory,
		Environment:      options.environment,
		ReconnectTimeout: options.reconnectTimeout,
		IdleTimeout:      options.idleTimeout,
	})

	httpServer := &http.Server{
		Addr:              options.addr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	if isWildcardAddress(options.addr) {
		fmt.Fprintln(os.Stderr, "Warning: this server listens on all network interfaces. Only use it on a trusted private network.")
	}

	errCh := make(chan error, 1)
	go func() {
		printStartup(options.addr, token)
		errCh <- httpServer.ListenAndServe()
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	select {
	case sig := <-stop:
		fmt.Printf("\nreceived %s, shutting down\n", sig)
	case err := <-errCh:
		if err != nil && err != http.ErrServerClosed {
			log.Fatalf("server error: %v", err)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	app.Close()

	if err := httpServer.Shutdown(ctx); err != nil {
		log.Fatalf("shutdown server: %v", err)
	}
}

type startupOptions struct {
	addr             string
	webDir           string
	commandLine      string
	command          []string
	workingDirectory string
	environment      []string
	reconnectTimeout time.Duration
	idleTimeout      time.Duration
	profileID        string
}

func resolveStartupOptions(options startupOptions, configPath string, requestedProfile string, explicitFlags map[string]bool, lookupEnv func(string) (string, bool)) (startupOptions, error) {
	if configPath == "" {
		if requestedProfile != "" {
			return startupOptions{}, errors.New("--profile requires --config")
		}
		options.command = strings.Fields(options.commandLine)
		if len(options.command) == 0 {
			return startupOptions{}, errors.New("cmd must not be empty")
		}
		return options, nil
	}

	config, err := agentconfig.Load(configPath)
	if err != nil {
		return startupOptions{}, err
	}
	if !explicitFlags["addr"] && config.ListenAddress != "" {
		options.addr = config.ListenAddress
	}
	if !explicitFlags["web-dir"] && config.WebDirectory != "" {
		options.webDir = config.WebDirectory
	}
	if !explicitFlags["reconnect-timeout"] {
		if duration, ok := config.ParsedReconnectTimeout(); ok {
			options.reconnectTimeout = duration
		}
	}
	if !explicitFlags["idle-timeout"] {
		if duration, ok := config.ParsedIdleTimeout(); ok {
			options.idleTimeout = duration
		}
	}

	if explicitFlags["cmd"] {
		if requestedProfile != "" {
			return startupOptions{}, errors.New("--cmd and --profile cannot be used together")
		}
		options.command = strings.Fields(options.commandLine)
		if len(options.command) == 0 {
			return startupOptions{}, errors.New("cmd must not be empty")
		}
		return options, nil
	}

	selectedID := requestedProfile
	if selectedID == "" {
		selectedID = config.DefaultProfile
	}
	profile, ok := config.Profile(selectedID)
	if !ok {
		return startupOptions{}, fmt.Errorf("launch profile %q was not found", selectedID)
	}
	options.profileID = profile.ID
	options.command = append([]string{profile.Executable}, profile.Args...)
	options.workingDirectory = profile.WorkingDirectory
	options.environment = resolveEnvironment(profile.EnvironmentAllowlist, lookupEnv)
	return options, nil
}

func resolveEnvironment(allowlist []string, lookupEnv func(string) (string, bool)) []string {
	environment := make([]string, 0, len(allowlist))
	for _, name := range allowlist {
		if value, ok := lookupEnv(name); ok {
			environment = append(environment, name+"="+value)
		}
	}
	return environment
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

func isWildcardAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	return err == nil && (host == "0.0.0.0" || host == "::")
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

func runDiagnostics(addr string, webDir string, hasEmbeddedAssets bool, profileID string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen check for %s failed: %w", addr, err)
	}
	_ = listener.Close()

	if profileID == "" {
		fmt.Println("[ok] configured command is available")
	} else {
		fmt.Printf("[ok] launch profile %q executable is available\n", profileID)
	}
	fmt.Printf("[ok] %s is available for the HTTP listener\n", addr)
	if hasEmbeddedAssets {
		fmt.Println("[ok] frontend assets are embedded in this binary")
	} else if _, err := os.Stat(filepath.Join(webDir, "index.html")); err == nil {
		fmt.Printf("[ok] frontend build found in %s\n", webDir)
	} else {
		fmt.Printf("[warn] frontend build not found in %s; run pnpm --dir web build\n", webDir)
	}

	hosts := lanIPv4Hosts()
	if len(hosts) == 0 {
		fmt.Println("[warn] no private LAN IPv4 address was detected")
	} else {
		for _, host := range hosts {
			fmt.Printf("[ok] private LAN address detected: %s\n", host)
		}
	}
	fmt.Println("[check] Windows Firewall must allow private-network access to the selected executable")
	return nil
}

func newSessionToken() (string, error) {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw[:]), nil
}

func defaultCommandLine() string {
	if runtime.GOOS == "windows" {
		if _, err := exec.LookPath("pwsh"); err == nil {
			return "pwsh -NoLogo -NoExit -NoProfile"
		}
		return "powershell.exe -NoLogo -NoExit -NoProfile"
	}
	return "/bin/sh"
}

func printStartup(addr string, token string) {
	urls, err := accessURLs(addr, token)
	if err != nil {
		fmt.Printf("VibeBridge listening on %s\n", addr)
		fmt.Printf("Could not build access URL: %v\n", err)
		return
	}

	fmt.Printf("VibeBridge listening on %s\n", addr)
	fmt.Println("Preflight: command found, session token created, HTTP listener starting")
	for _, url := range urls {
		fmt.Printf("Open: %s\n", url)
	}
	if len(urls) == 0 {
		return
	}

	fmt.Println("Scan this QR code from your phone:")
	qrterminal.GenerateHalfBlock(urls[0], qrterminal.L, os.Stdout)
	fmt.Println("If the phone cannot connect, allow vibebridge.exe through Windows Firewall for private networks only.")
}

func accessURLs(addr string, token string) ([]string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	if host == "" {
		host = "0.0.0.0"
	}

	hosts := make([]string, 0, 3)
	if host == "0.0.0.0" || host == "::" {
		hosts = append(hosts, lanIPv4Hosts()...)
		hosts = append(hosts, "127.0.0.1")
	} else {
		hosts = append(hosts, host)
	}

	seen := make(map[string]bool, len(hosts))
	urls := make([]string, 0, len(hosts))
	for _, candidate := range hosts {
		if candidate == "" || seen[candidate] {
			continue
		}
		seen[candidate] = true
		urls = append(urls, "http://"+net.JoinHostPort(candidate, port)+"/?token="+token)
	}
	return urls, nil
}

func lanIPv4Hosts() []string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return nil
	}

	var hosts []string
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}

		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ip := addressIP(addr)
			if ip == nil {
				continue
			}
			ip4 := ip.To4()
			if ip4 == nil || ip4.IsLoopback() || !ip4.IsPrivate() {
				continue
			}
			hosts = append(hosts, ip4.String())
		}
	}
	return hosts
}

func addressIP(addr net.Addr) net.IP {
	switch value := addr.(type) {
	case *net.IPNet:
		return value.IP
	case *net.IPAddr:
		return value.IP
	default:
		return nil
	}
}
