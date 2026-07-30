package relayclient_test

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/internal/relayclient"
)

// newManagerFixture creates a relay fixture and a Manager wired to it.
// It reuses the relayFixture from client_test.go.
func newManagerFixture(t *testing.T) (*relayclient.Manager, *relayFixture) {
	t.Helper()
	f := newRelayFixture(t)
	mgr, err := relayclient.NewManager(relayclient.ManagerConfig{
		RelayURL:   f.wsEndpoint,
		Issuer:     f.issuer,
		AgentID:    f.agentID,
		Dialer:     relayclient.Dialer{},
	})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	t.Cleanup(func() { mgr.Close() })
	return mgr, f
}

func TestManagerProvisionReturnsClientTicket(t *testing.T) {
	mgr, f := newManagerFixture(t)
	clientTicket, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}
	if clientTicket == nil {
		t.Fatal("client ticket must not be nil")
	}
	if routeID == "" {
		t.Fatal("route ID must not be empty")
	}
	if len(clientTicket.RouteId) != 16 {
		t.Fatalf("client ticket route ID must be 16 bytes, got %d", len(clientTicket.RouteId))
	}
	pending := mgr.PendingRoutes()
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending route, got %d", len(pending))
	}
	if pending[0] != routeID {
		t.Fatalf("pending route %q does not match returned route %q", pending[0], routeID)
	}
}

func TestManagerProvisionRejectsBadClientID(t *testing.T) {
	mgr, _ := newManagerFixture(t)
	_, _, err := mgr.Provision(bytes.Repeat([]byte{0x33}, 15))
	if err == nil {
		t.Fatal("expected error for 15-byte client ID")
	}
}

func TestManagerNewRejectsBadConfig(t *testing.T) {
	_, f := newManagerFixture(t)

	tests := []struct {
		name   string
		config relayclient.ManagerConfig
		errMsg string
	}{
		{
			name:   "empty relay URL",
			config: relayclient.ManagerConfig{Issuer: f.issuer, AgentID: f.agentID},
			errMsg: "RelayURL",
		},
		{
			name:   "nil issuer",
			config: relayclient.ManagerConfig{RelayURL: f.wsEndpoint, AgentID: f.agentID},
			errMsg: "Issuer",
		},
		{
			name:   "short agent ID",
			config: relayclient.ManagerConfig{RelayURL: f.wsEndpoint, Issuer: f.issuer, AgentID: bytes.Repeat([]byte{0x01}, 10)},
			errMsg: "AgentID",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := relayclient.NewManager(tt.config)
			if err == nil {
				t.Fatalf("expected error containing %q", tt.errMsg)
			}
			if !contains(err.Error(), tt.errMsg) {
				t.Fatalf("error %q does not contain %q", err.Error(), tt.errMsg)
			}
		})
	}
}

func TestManagerConnectBridgesRelayToPipe(t *testing.T) {
	mgr, f := newManagerFixture(t)
	clientTicket, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	// Create a local pipe pair. One end goes to the Manager (as the
	// "local" transport), the other is what the test reads/writes to
	// simulate the Agent's PTY side.
	agentEnd, ptyEnd := net.Pipe()
	defer agentEnd.Close()
	defer ptyEnd.Close()

	// Start the Agent-side bridge in a goroutine.
	connectErr := make(chan error, 1)
	go func() {
		connectErr <- mgr.Connect(context.Background(), routeID, agentEnd)
	}()

	// Give the Agent's Connect goroutine time to dial the relay
	// before the client connects.
	time.Sleep(200 * time.Millisecond)

	// Dial the relay from the client side using the client ticket.
	clientStream := f.dial(t, clientTicket)
	defer clientStream.Close()

	// Write from the client, read on the PTY side.
	message := []byte("hello from client")
	if _, err := clientStream.Write(message); err != nil {
		t.Fatalf("client write: %v", err)
	}
	buf := make([]byte, 256)
	if err := ptyEnd.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatalf("set read deadline: %v", err)
	}
	n, err := ptyEnd.Read(buf)
	if err != nil {
		t.Fatalf("pty read: %v", err)
	}
	if !bytes.Equal(buf[:n], message) {
		t.Fatalf("expected %q, got %q", message, buf[:n])
	}

	// Write from the PTY side, read on the client.
	reply := []byte("hello from agent")
	if _, err := ptyEnd.Write(reply); err != nil {
		t.Fatalf("pty write: %v", err)
	}
	clientBuf := make([]byte, 256)
	n, err = readFrame(clientStream, clientBuf, 5*time.Second)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(clientBuf[:n], reply) {
		t.Fatalf("expected %q, got %q", reply, clientBuf[:n])
	}

	// Verify the route is active while connected.
	active := mgr.ActiveRoutes()
	if len(active) != 1 {
		t.Fatalf("expected 1 active route, got %d", len(active))
	}

	// Close client to end the bridge.
	clientStream.Close()

	// Wait for Connect to return.
	select {
	case err := <-connectErr:
		// Bridge should return an error (EOF or closed pipe) — that's normal.
		_ = err
	case <-time.After(10 * time.Second):
		t.Fatal("Connect did not return after client disconnect")
	}

	// Route should be cleaned up.
	active = mgr.ActiveRoutes()
	if len(active) != 0 {
		t.Fatalf("expected 0 active routes after disconnect, got %d", len(active))
	}
}

func TestManagerConnectRejectsUnknownRoute(t *testing.T) {
	mgr, _ := newManagerFixture(t)
	agentEnd, ptyEnd := net.Pipe()
	defer agentEnd.Close()
	defer ptyEnd.Close()

	err := mgr.Connect(context.Background(), "deadbeef", agentEnd)
	if err == nil {
		t.Fatal("expected error for unknown route ID")
	}
	if !contains(err.Error(), "no pending ticket") {
		t.Fatalf("error %q does not mention pending ticket", err.Error())
	}
}

func TestManagerConnectRejectsDoubleConnect(t *testing.T) {
	mgr, f := newManagerFixture(t)
	_, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	// First Connect consumes the pending ticket and blocks.
	agentEnd1, ptyEnd1 := net.Pipe()
	defer agentEnd1.Close()
	defer ptyEnd1.Close()
	go func() { _ = mgr.Connect(context.Background(), routeID, agentEnd1) }()

	// Give the first Connect time to consume the ticket.
	time.Sleep(100 * time.Millisecond)

	// Second Connect should fail because the ticket was already consumed.
	agentEnd2, ptyEnd2 := net.Pipe()
	defer agentEnd2.Close()
	defer ptyEnd2.Close()
	err = mgr.Connect(context.Background(), routeID, agentEnd2)
	if err == nil {
		t.Fatal("expected error for double connect")
	}
}

func TestManagerCancelRoute(t *testing.T) {
	mgr, f := newManagerFixture(t)
	_, routeID, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision: %v", err)
	}

	agentEnd, ptyEnd := net.Pipe()
	defer agentEnd.Close()
	defer ptyEnd.Close()

	connectErr := make(chan error, 1)
	go func() {
		connectErr <- mgr.Connect(context.Background(), routeID, agentEnd)
	}()

	// Wait for the route to become active.
	if !waitForCondition(2*time.Second, func() bool {
		return len(mgr.ActiveRoutes()) == 1
	}) {
		t.Fatal("route did not become active")
	}

	// Cancel the route.
	mgr.CancelRoute(routeID)

	select {
	case err := <-connectErr:
		_ = err // context cancellation is expected
	case <-time.After(5 * time.Second):
		t.Fatal("Connect did not return after CancelRoute")
	}

	if len(mgr.ActiveRoutes()) != 0 {
		t.Fatalf("expected 0 active routes after cancel, got %d", len(mgr.ActiveRoutes()))
	}
}

func TestManagerCloseDropsAllConnections(t *testing.T) {
	mgr, f := newManagerFixture(t)

	// Provision two routes.
	_, route1, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision 1: %v", err)
	}
	_, route2, err := mgr.Provision(f.clientID)
	if err != nil {
		t.Fatalf("provision 2: %v", err)
	}

	// Connect route1.
	agentEnd1, ptyEnd1 := net.Pipe()
	defer agentEnd1.Close()
	defer ptyEnd1.Close()
	connectErr := make(chan error, 1)
	go func() {
		connectErr <- mgr.Connect(context.Background(), route1, agentEnd1)
	}()

	if !waitForCondition(2*time.Second, func() bool {
		return len(mgr.ActiveRoutes()) == 1
	}) {
		t.Fatal("route1 did not become active")
	}

	// Close the manager.
	if err := mgr.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	// Route1 connect should return.
	select {
	case <-connectErr:
	case <-time.After(5 * time.Second):
		t.Fatal("route1 connect did not return after Close")
	}

	// No active or pending routes.
	if len(mgr.ActiveRoutes()) != 0 {
		t.Fatalf("expected 0 active routes, got %d", len(mgr.ActiveRoutes()))
	}
	if len(mgr.PendingRoutes()) != 0 {
		t.Fatalf("expected 0 pending routes, got %d", len(mgr.PendingRoutes()))
	}

	// Route2 ticket should be gone — Connect should fail.
	agentEnd2, ptyEnd2 := net.Pipe()
	defer agentEnd2.Close()
	defer ptyEnd2.Close()
	if err := mgr.Connect(context.Background(), route2, agentEnd2); err == nil {
		t.Fatal("expected error connecting route2 after Close")
	}
}

func TestManagerProvisionGeneratesUniqueRouteIDs(t *testing.T) {
	mgr, f := newManagerFixture(t)
	seen := make(map[string]bool)
	for i := 0; i < 50; i++ {
		_, routeID, err := mgr.Provision(f.clientID)
		if err != nil {
			t.Fatalf("provision %d: %v", i, err)
		}
		if seen[routeID] {
			t.Fatalf("duplicate route ID %s at iteration %d", routeID, i)
		}
		seen[routeID] = true
	}
}

func TestManagerRelayURL(t *testing.T) {
	mgr, f := newManagerFixture(t)
	if mgr.RelayURL() != f.wsEndpoint {
		t.Fatalf("expected %q, got %q", f.wsEndpoint, mgr.RelayURL())
	}
}

// --- helpers ---

func contains(s, substr string) bool {
	return bytes.Contains([]byte(s), []byte(substr))
}

func waitForCondition(timeout time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// Ensure net.Pipe implements io.ReadWriteCloser.
var _ io.ReadWriteCloser = (net.Conn)(nil)
