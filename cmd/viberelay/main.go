// Command viberelay is the entry point for the VibeBridge relay. The relay
// is a stateless WebSocket switchboard that authenticates peers with
// short-lived signed RelayTickets and forwards opaque transport bytes
// between the AGENT and CLIENT halves of a single route. It never
// inspects the application payloads it forwards and never persists
// ticket plaintext.
//
// The relay is designed to be runnable on its own host (often on the
// same LAN as the Agent, or on a small public VM). It only needs an
// Ed25519 signing key whose public half is shared with whoever mints
// tickets (typically the Agent or a control plane).
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/zzemy/VibeBridge/internal/agentlog"
	"github.com/zzemy/VibeBridge/internal/relay"
)

const (
	// defaultListenAddr is the address the relay binds to when no
	// --addr is provided. The relay's port is intentionally separate
	// from the Agent's default (8787) so the two can share a host.
	defaultListenAddr = "0.0.0.0:8788"
	// defaultAdminAddr is the address the ticket-issuance control
	// plane binds to when no --admin-addr is provided. It is on a
	// separate port and defaults to loopback so a fresh install is
	// not exposed to the LAN until the operator opts in.
	defaultAdminAddr = "127.0.0.1:8789"
	// shutdownTimeout bounds the wait for in-flight connections to
	// finish during a graceful shutdown.
	shutdownTimeout = 10 * time.Second
)

func main() {
	if err := run(os.Args[1:]); err != nil && !errors.Is(err, flag.ErrHelp) {
		log.Fatal(err)
	}
}

func run(args []string) error {
	eventLogger := agentlog.NewJSON(os.Stderr)
	flags := flag.NewFlagSet("viberelay", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	addr := flags.String("addr", defaultListenAddr, "listen address for the relay WebSocket endpoint")
	issuerKeyPath := flags.String("issuer-key", "", "path to a file holding the 64-byte Ed25519 private key used to sign and verify relay tickets; created if missing")
	allowOverwrite := flags.Bool("issuer-key-overwrite", false, "allow recreating --issuer-key if the file already exists")
	var allowedOrigins allowedOriginsFlag
	flags.Var(&allowedOrigins, "allowed-origin", "origin the relay will accept WebSocket upgrades from; repeat the flag for multiple origins (default: same-origin only)")
	adminAddr := flags.String("admin-addr", defaultAdminAddr, "listen address for the ticket issuance control plane; empty disables the control plane")
	adminToken := flags.String("admin-token", "", "bearer token required by the control plane; empty disables auth (rely on the listener bind address as the trust boundary)")
	diagnose := flags.Bool("diagnose", false, "validate configuration and exit without starting the listener")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}

	keyPath, err := resolveIssuerKeyPath(*issuerKeyPath)
	if err != nil {
		return err
	}
	private, err := loadOrCreateIssuerKey(keyPath, *allowOverwrite)
	if err != nil {
		return fmt.Errorf("load issuer key: %w", err)
	}
	issuer, err := relay.NewIssuer(private)
	if err != nil {
		return fmt.Errorf("initialize issuer: %w", err)
	}
	verifier := relay.NewVerifier(issuer.PublicKey())
	logger := newRelayLogger(eventLogger)

	if *diagnose {
		return runDiagnostics(*addr, *adminAddr, keyPath, issuer.PublicKey())
	}

	server, err := relay.New(relay.Config{
		Verifier:       verifier,
		Logger:         logger,
		AllowedOrigins: buildOriginAllowList(allowedOrigins),
	})
	if err != nil {
		return fmt.Errorf("initialize relay server: %w", err)
	}

	listener, err := net.Listen("tcp", *addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", *addr, err)
	}
	listenAddress := listener.Addr().String()
	httpServer := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}

	if isWildcardAddress(listenAddress) {
		fmt.Fprintln(os.Stderr, "Warning: relay listens on all network interfaces. Use --allowed-origin to restrict peer origins on untrusted networks.")
	}

	eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStarting, State: agentlog.StateConnected})
	fmt.Printf("viberelay listening on %s\n", listenAddress)
	fmt.Printf("issuer public key: %x\n", issuer.PublicKey())
	if keyPath != "" {
		fmt.Printf("issuer key: %s\n", keyPath)
	}

	errCh := make(chan error, 2)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()

	// Ticket issuance control plane. Optional: an empty --admin-addr
	// disables it entirely (useful for hardened deployments where
	// the issuer lives in a separate process).
	var (
		adminHTTPServer *http.Server
	)
	if *adminAddr != "" {
		admin, err := relay.NewAdmin(issuer)
		if err != nil {
			return fmt.Errorf("initialize relay admin: %w", err)
		}
		adminListener, err := net.Listen("tcp", *adminAddr)
		if err != nil {
			return fmt.Errorf("listen on admin address %s: %w", *adminAddr, err)
		}
		adminListenAddr := adminListener.Addr().String()
		adminHTTPServer = &http.Server{
			Handler:           admin.Handler(*adminToken),
			ReadHeaderTimeout: 10 * time.Second,
		}
		go func() {
			errCh <- adminHTTPServer.Serve(adminListener)
		}()
		fmt.Printf("viberelay admin listening on %s\n", adminListenAddr)
		if isWildcardAddress(adminListenAddr) {
			fmt.Fprintln(os.Stderr, "Warning: ticket issuance control plane listens on all network interfaces. Set --admin-token or bind to a trusted address.")
		}
		if *adminToken == "" {
			fmt.Fprintln(os.Stderr, "Warning: --admin-token is empty; the control plane is unauthenticated. Restrict via --admin-addr.")
		}
	}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)

	select {
	case sig := <-stop:
		fmt.Printf("\nreceived %s, shutting down\n", sig)
		eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopping, Reason: agentlog.ReasonSignal})
	case err := <-errCh:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopping, Reason: agentlog.ReasonListenerError, Outcome: agentlog.OutcomeFailure})
			return fmt.Errorf("server error: %w", err)
		}
		eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopping, Reason: agentlog.ReasonListenerClosed})
	}

	ctx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopped, Outcome: agentlog.OutcomeFailure})
		return fmt.Errorf("shutdown relay: %w", err)
	}
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopped, Outcome: agentlog.OutcomeFailure})
		return fmt.Errorf("shutdown http: %w", err)
	}
	if adminHTTPServer != nil {
		adminShutdownCtx, adminShutdownCancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer adminShutdownCancel()
		if err := adminHTTPServer.Shutdown(adminShutdownCtx); err != nil {
			eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopped, Outcome: agentlog.OutcomeFailure})
			return fmt.Errorf("shutdown admin: %w", err)
		}
	}
	eventLogger.Log(agentlog.Event{Name: agentlog.EventAgentStopped, Outcome: agentlog.OutcomeSuccess})
	return nil
}

// resolveIssuerKeyPath returns the absolute path the issuer key should
// be read from / written to. An empty input falls back to a file under
// the user's home directory so a fresh install can run without
// configuration.
func resolveIssuerKeyPath(input string) (string, error) {
	if input != "" {
		abs, err := filepath.Abs(input)
		if err != nil {
			return "", fmt.Errorf("resolve issuer key path: %w", err)
		}
		return abs, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory for default issuer key: %w", err)
	}
	return filepath.Join(home, ".viberelay", "issuer.key"), nil
}

// loadOrCreateIssuerKey reads a 64-byte Ed25519 private key from path,
// creating a fresh one if the file does not exist. When the file
// already exists and allowOverwrite is false, the file is loaded as-is.
func loadOrCreateIssuerKey(path string, allowOverwrite bool) (ed25519.PrivateKey, error) {
	if data, err := os.ReadFile(path); err == nil {
		if len(data) != ed25519.PrivateKeySize {
			return nil, fmt.Errorf("issuer key %s is %d bytes, expected %d", path, len(data), ed25519.PrivateKeySize)
		}
		return ed25519.PrivateKey(data), nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, fmt.Errorf("create issuer key directory: %w", err)
	}
	_, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate issuer key: %w", err)
	}
	if err := os.WriteFile(path, private, 0o600); err != nil {
		return nil, fmt.Errorf("write issuer key %s: %w", path, err)
	}
	return private, nil
}

// allowedOriginsFlag is a flag.Value that collects every occurrence of
// --allowed-origin into a single slice. The stdlib's flag package has
// no built-in string-slice helper so we keep this tiny adapter here.
type allowedOriginsFlag []string

func (a *allowedOriginsFlag) String() string {
	if a == nil {
		return ""
	}
	return strings.Join(*a, ",")
}

func (a *allowedOriginsFlag) Set(value string) error {
	if value == "" {
		return errors.New("allowed origin must not be empty")
	}
	*a = append(*a, value)
	return nil
}

// buildOriginAllowList turns the repeated --allowed-origin flag into
// the slice the relay expects. A nil result tells the relay to use the
// built-in same-origin check.
func buildOriginAllowList(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" {
			continue
		}
		out = append(out, value)
	}
	return out
}

// runDiagnostics prints the resolved configuration without binding the
// listener. It is the --diagnose flag handler.
func runDiagnostics(addr, adminAddr, keyPath string, public ed25519.PublicKey) error {
	fmt.Printf("viberelay diagnose\n")
	fmt.Printf("  listen address:   %s\n", addr)
	if adminAddr != "" {
		fmt.Printf("  admin address:    %s\n", adminAddr)
	} else {
		fmt.Printf("  admin address:    (disabled)\n")
	}
	if keyPath != "" {
		fmt.Printf("  issuer key:       %s\n", keyPath)
	}
	fmt.Printf("  issuer pubkey:    %x\n", public)
	return nil
}

// isWildcardAddress mirrors the helper in cmd/vibebridge so the relay
// can warn operators when it binds to a public interface.
func isWildcardAddress(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	return host == "" || host == "0.0.0.0" || host == "::"
}

// relayLogger adapts relay's narrow Event surface to the Agent's
// structured logger so the relay contributes to the same JSON log
// stream as the rest of VibeBridge. The relay's own Event struct is
// deliberately minimal: only outcome / reason are populated.
type relayLogger struct {
	logger agentlog.Logger
}

func newRelayLogger(logger agentlog.Logger) *relayLogger {
	return &relayLogger{logger: logger}
}

func (l *relayLogger) Log(event relay.Event) {
	name := agentlog.EventAgentStarting // generic lifecycle fallback
	if event.Outcome == relay.OutcomeSuccess {
		name = agentlog.EventAgentStopped
	} else if event.Outcome == relay.OutcomeFailure {
		name = agentlog.EventAgentStopping
	}
	l.logger.Log(agentlog.Event{
		Name:    name,
		Reason:  agentlog.Reason(event.Reason),
		Outcome: agentlog.Outcome(event.Outcome),
	})
}
