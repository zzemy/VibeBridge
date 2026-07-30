package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mdp/qrterminal/v3"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"github.com/zzemy/VibeBridge/internal/pairing"
	"github.com/zzemy/VibeBridge/internal/pairingflow"
)

func runRecoverCommand(args []string) error {
	flags := flag.NewFlagSet("vibebridge recover", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	identityStorePath := flags.String("identity-store", "", "path to the device identity store file")
	addr := flags.String("addr", "0.0.0.0:8787", "listen address for --authorize-new pairing server")
	list := flags.Bool("list", false, "list all authorized devices without modifying state")
	revokeAll := flags.Bool("revoke-all", false, "revoke every authorized device")
	authorizeNew := flags.Bool("authorize-new", false, "start a minimal pairing server to authorize a replacement device")
	yes := flags.Bool("yes", false, "skip confirmation prompt for --revoke-all")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments: %v", flags.Args())
	}
	count := 0
	for _, v := range []bool{*list, *revokeAll, *authorizeNew} {
		if v {
			count++
		}
	}
	if count == 0 {
		return fmt.Errorf("specify one of --list, --revoke-all, or --authorize-new")
	}
	if count > 1 {
		return fmt.Errorf("--list, --revoke-all, and --authorize-new are mutually exclusive")
	}
	resolvedPath := *identityStorePath
	if resolvedPath == "" {
		p, err := deviceidentity.DefaultPath()
		if err != nil {
			return fmt.Errorf("resolve device identity path: %w", err)
		}
		resolvedPath = p
	}
	switch {
	case *list:
		return recoverList(resolvedPath)
	case *revokeAll:
		return recoverRevokeAll(resolvedPath, *yes)
	case *authorizeNew:
		return recoverAuthorizeNew(resolvedPath, *addr)
	}
	return nil
}

func recoverList(identityPath string) error {
	store, err := deviceidentity.Load(deviceidentity.Options{Path: identityPath})
	if err != nil {
		return fmt.Errorf("open identity store: %w", err)
	}
	defer store.Close()
	devices, err := store.AuthorizedDevices(true)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	epoch := store.RevocationEpoch()
	fmt.Printf("Revocation epoch: %d\n", epoch)
	fmt.Printf("Authorized devices (%d total):\n", len(devices))
	for _, device := range devices {
		fingerprint, err := deviceidentity.Fingerprint(device.Device)
		if err != nil {
			fmt.Printf("  - [fingerprint error: %v]\n", err)
			continue
		}
		state := "authorized"
		if device.State == vibebridgev1.DeviceAuthorizationState_DEVICE_AUTHORIZATION_STATE_REVOKED {
			state = "revoked"
		}
		fmt.Printf("  - %s (%s) [%s] fingerprint=%s\n",
			device.Device.DeviceDescriptor.DisplayName,
			device.Device.DeviceDescriptor.Platform,
			state,
			fingerprint,
		)
	}
	if len(devices) == 0 {
		fmt.Println("  (none)")
	}
	return nil
}

func recoverRevokeAll(identityPath string, confirmed bool) error {
	if !confirmed {
		fmt.Println("This will revoke ALL authorized devices. Existing paired sessions will be terminated.")
		fmt.Print("Run with --yes to confirm: vibebridge recover --revoke-all --yes\n")
		return fmt.Errorf("confirmation required")
	}
	if _, err := os.Stat(identityPath); err != nil {
		return fmt.Errorf("identity store not found at %s: %w", identityPath, err)
	}
	store, err := deviceidentity.LoadOrCreate(deviceidentity.Options{Path: identityPath})
	if err != nil {
		return fmt.Errorf("open identity store: %w", err)
	}
	defer store.Close()
	devices, err := store.AuthorizedDevices(false)
	if err != nil {
		return fmt.Errorf("list authorized devices: %w", err)
	}
	if len(devices) == 0 {
		fmt.Println("No authorized devices to revoke.")
		return nil
	}
	fmt.Printf("Revoking %d device(s)...\n", len(devices))
	for _, device := range devices {
		if _, err := store.Revoke(device.Device.DeviceDescriptor.DeviceId); err != nil {
			return fmt.Errorf("revoke device %x: %w", device.Device.DeviceDescriptor.DeviceId, err)
		}
		fmt.Printf("  - revoked %s (%s)\n",
			device.Device.DeviceDescriptor.DisplayName,
			device.Device.DeviceDescriptor.Platform,
		)
	}
	fmt.Printf("Done. Revocation epoch is now %d.\n", store.RevocationEpoch())
	fmt.Println("Restart the Agent to apply changes.")
	return nil
}

func recoverAuthorizeNew(identityPath string, addr string) error {
	if _, err := os.Stat(identityPath); err != nil {
		return fmt.Errorf("identity store not found at %s: %w", identityPath, err)
	}
	store, err := deviceidentity.LoadOrCreate(deviceidentity.Options{Path: identityPath})
	if err != nil {
		return fmt.Errorf("open identity store: %w", err)
	}
	defer store.Close()
	pairingManager, err := pairing.New(pairing.Config{Agent: store})
	if err != nil {
		return fmt.Errorf("initialize pairing manager: %w", err)
	}
	defer pairingManager.Close()
	pairingFlows, err := pairingflow.New(pairingflow.Config{Invitations: pairingManager, Identity: store})
	if err != nil {
		return fmt.Errorf("initialize pairing flow coordinator: %w", err)
	}
	token, err := newSessionToken()
	if err != nil {
		return fmt.Errorf("create session token: %w", err)
	}
	dummyApp := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "Agent is in recovery mode; use the pairing page to authorize a new device.", http.StatusServiceUnavailable)
	})
	handler, err := newAgentHTTPHandler(dummyApp, addr, token, pairingManager, store, pairingFlows, nil)
	if err != nil {
		return fmt.Errorf("configure recovery handler: %w", err)
	}
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("start listener on %s: %w", addr, err)
	}
	defer listener.Close()
	listenAddress := listener.Addr().String()
	httpServer := &http.Server{
		Addr:              listenAddress,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
	}
	errCh := make(chan error, 1)
	go func() {
		errCh <- httpServer.Serve(listener)
	}()
	urls, _ := accessURLs(listenAddress, token)
	fmt.Printf("VibeBridge recovery pairing server listening on %s\n", listenAddress)
	fmt.Println("Open this URL on a browser on this machine to pair a new phone:")
	for _, u := range urls {
		fmt.Printf("  %s\n", u)
	}
	if len(urls) > 0 {
		fmt.Println("Scan this QR code:")
		qrterminal.GenerateHalfBlock(urls[0], qrterminal.L, os.Stdout)
	}
	fmt.Println("\nWaiting for a new device to be authorized...")
	fmt.Println("Press Ctrl-C to cancel.")
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(stop)
	poll := time.NewTicker(2 * time.Second)
	defer poll.Stop()
	initialCount := authorizedDeviceCount(store)
	for {
		select {
		case <-poll.C:
			current := authorizedDeviceCount(store)
			if current > initialCount {
				devices, _ := store.AuthorizedDevices(false)
				if len(devices) > 0 {
					last := devices[len(devices)-1]
					fmt.Printf("\nNew device authorized: %s (%s)\n",
						last.Device.DeviceDescriptor.DisplayName,
						last.Device.DeviceDescriptor.Platform,
					)
				}
				fmt.Println("Recovery complete. Stop this server and start the Agent normally.")
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				defer cancel()
				_ = httpServer.Shutdown(ctx)
				return nil
			}
		case sig := <-stop:
			fmt.Printf("\nReceived %s, shutting down recovery server.\n", sig)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			_ = httpServer.Shutdown(ctx)
			return nil
		case err := <-errCh:
			if err != nil && err != http.ErrServerClosed {
				return fmt.Errorf("recovery server error: %w", err)
			}
			return nil
		}
	}
}

func authorizedDeviceCount(store *deviceidentity.Store) int {
	devices, err := store.AuthorizedDevices(false)
	if err != nil {
		return 0
	}
	return len(devices)
}
