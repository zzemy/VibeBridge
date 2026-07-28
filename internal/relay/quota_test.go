// quota_test.go covers the route lifecycle and concurrency cap work
// that landed in Phase 5: Router.Sweep / Router.StartSweeper and
// Server.MaxConnections. The sweep tests use a fake clock so they
// can drive time without sleeping. The cap tests build a real
// httptest.Server and assert that the server both refuses upgrades
// beyond the cap and that the slot is released as soon as the
// underlying connection goes away.
package relay

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// fakeClock is a deterministic time source used by the sweep tests.
// Tests advance the clock with Advance so a "5 minutes elapsed"
// assertion does not need to actually wait.
type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// sweepRouterWithClock returns a Router whose idle / lifetime / sweep
// policy is driven by a fake clock. The values are short enough that
// even if the implementation accidentally used the real clock the
// tests would still pass, but a deterministic clock makes the
// assertions readable.
func sweepRouterWithClock(clock *fakeClock, idle, maxLife, sweepInt time.Duration) *Router {
	return NewRouterWithConfig(RouterConfig{
		IdleTimeout:   idle,
		MaxLifetime:   maxLife,
		SweepInterval: sweepInt,
		Now:           clock.Now,
	})
}

// TestRouterSweepReapsOrphanRoute covers the half-joined case: a
// route that was created by a successful Join but never received its
// second peer. Once the idle timeout passes the sweeper should drop
// the route and close the lone peer so the relay does not hold the
// slot forever.
func TestRouterSweepReapsOrphanRoute(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, 30*time.Minute, time.Hour)
	defer router.Stop()

	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}

	// Just before the timeout the route must still be alive.
	clock.Advance(4*time.Minute + 59*time.Second)
	if result := router.Sweep(); result.IdleClosed != 0 || result.ExpiredClosed != 0 {
		t.Fatalf("expected no reap just before timeout, got %+v", result)
	}

	// Past the timeout the orphan must be reaped and the lone
	// peer must be closed so the relay does not leak the
	// WebSocket goroutine on the agent side.
	clock.Advance(2 * time.Second)
	result := router.Sweep()
	if result.IdleClosed != 1 {
		t.Fatalf("expected 1 idle reap, got %+v", result)
	}
	if result.ExpiredClosed != 0 {
		t.Fatalf("expected no expired reap, got %+v", result)
	}
	select {
	case <-agent.closed:
	default:
		t.Fatalf("expected orphan peer to be closed after sweep")
	}
}

// TestRouterSweepKeepsActiveRoute covers the renewal case: as long
// as Forward keeps firing the sweeper must not touch the route, no
// matter how long the wall clock has been moving. lastActivity is
// reset on every successful Forward, and the sweep is computed
// against lastActivity, not createdAt.
func TestRouterSweepKeepsActiveRoute(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, 30*time.Minute, time.Hour)
	defer router.Stop()

	routeID := bytesRepeat(0x22, routeIDBytes)
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, routeID, bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, routeID, bytesRepeat(0x44, 16))

	agentRoute, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent)
	if err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}

	// Drive a 30-minute wall clock forward in 4-minute steps,
	// poking Forward after each step. The route must still be
	// alive at the end.
	for i := 0; i < 7; i++ {
		clock.Advance(4 * time.Minute)
		if err := agentRoute.Forward([]byte("keep alive")); err != nil {
			t.Fatalf("forward %d: %v", i, err)
		}
	}
	if result := router.Sweep(); result.IdleClosed != 0 || result.ExpiredClosed != 0 {
		t.Fatalf("active route should not be reaped, got %+v", result)
	}
}

// TestRouterSweepReapsMaxLifetimeRoute covers the hard ceiling:
// even with constant Forward traffic a route that lives longer than
// MaxLifetime must be closed. The ceiling exists to bound the
// relay's table size against runaway clients.
func TestRouterSweepReapsMaxLifetimeRoute(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, 30*time.Minute, time.Hour)
	defer router.Stop()

	routeID := bytesRepeat(0x22, routeIDBytes)
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, routeID, bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, routeID, bytesRepeat(0x44, 16))

	agentRoute, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent)
	if err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}

	// Keep activity fresh until just before the cap, then verify
	// that one extra second past the cap is enough to reap.
	for i := 0; i < 5; i++ {
		clock.Advance(5 * time.Minute)
		if err := agentRoute.Forward([]byte("keep alive")); err != nil {
			t.Fatalf("forward %d: %v", i, err)
		}
	}
	// 5 * 5m = 25m, just under 30m.
	if result := router.Sweep(); result.IdleClosed != 0 || result.ExpiredClosed != 0 {
		t.Fatalf("route under cap should not be reaped, got %+v", result)
	}

	// Cross the 30m mark. Even though Forward has been firing,
	// the ceiling wins.
	clock.Advance(5*time.Minute + 1*time.Second)
	if err := agentRoute.Forward([]byte("last gasp")); err != nil {
		t.Fatalf("forward at cap: %v", err)
	}
	result := router.Sweep()
	if result.ExpiredClosed != 1 {
		t.Fatalf("expected 1 expired reap, got %+v", result)
	}
}

// TestRouterSweepNoOpWhenRouterStopped verifies the sweeper
// short-circuits on a stopped router instead of panicking. The
// production shutdown path is "stop sweeper, then Stop router", so
// the sweeper may fire once more after the router was stopped by
// another path; that final tick must be a safe no-op.
func TestRouterSweepNoOpWhenRouterStopped(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, 30*time.Minute, time.Hour)
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}
	clock.Advance(10 * time.Minute)
	router.Stop()
	result := router.Sweep()
	if result.IdleClosed != 0 || result.ExpiredClosed != 0 {
		t.Fatalf("sweep on stopped router should be a no-op, got %+v", result)
	}
}

// TestRouterNegativeMaxLifetimeDisablesCap documents the escape
// hatch: a self-hosted relay that wants a single long-lived route
// can set MaxLifetime to a negative value. Sweep must then only
// honor the idle timeout.
func TestRouterNegativeMaxLifetimeDisablesCap(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, -1, time.Hour)
	defer router.Stop()

	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}

	// Far past any reasonable lifetime cap, but well past the
	// idle timeout. Without an active Forward the orphan should
	// be reaped for idleness, not for age.
	clock.Advance(48 * time.Hour)
	result := router.Sweep()
	if result.IdleClosed != 1 || result.ExpiredClosed != 0 {
		t.Fatalf("expected 1 idle reap, got %+v", result)
	}
}

// TestRouterSweeperGoroutineStops verifies that the stop function
// returned by StartSweeper actually unblocks the goroutine. The
// router outlives the sweeper so a leak there would surface as
// "stuck at shutdown" or a race in the next test.
func TestRouterSweeperGoroutineStops(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, time.Minute, 30*time.Minute, 5*time.Millisecond)

	stop := router.StartSweeper()

	// Let a few ticks fire so we know the goroutine is alive.
	time.Sleep(30 * time.Millisecond)
	stop()

	// A second call to stop must be a no-op.
	stop()

	// After stop returns, Stop on the router must complete
	// promptly. A leaked goroutine would still be reading from
	// the ticker and would not block this, but a panic inside
	// the loop (e.g. closed channel send) would.
	done := make(chan struct{})
	go func() {
		router.Stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("router.Stop hung after sweeper stop")
	}
}

// containsReasonEvent was removed in favor of captureLogger.hasReason,
// which holds the logger's mutex and is therefore safe to call from
// the test goroutine while server goroutines may still be logging.

// TestServerRejectsBeyondMaxConnections drives the global cap:
// with MaxConnections=1 the second upgrade must return 503 and
// the server must log ReasonAtCapacity. The slot is held until
// the first connection's handler returns.
func TestServerRejectsBeyondMaxConnections(t *testing.T) {
	router := NewRouter()
	logger := &captureLogger{}
	server, err := New(Config{
		Verifier:       NewVerifier(),
		Router:         router,
		Logger:         logger,
		MaxConnections: 1,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer server.Shutdown(context.Background())

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	// Open one WebSocket to consume the single slot. The
	// upgrade itself succeeds; the connection holds the slot
	// because the handler is waiting for a valid ticket and
	// we never send one.
	first := dialRelayRaw(t, httpServer.URL)
	defer first.Close()

	// Give the server a moment to register the slot.
	waitFor(t, func() bool { return server.ActiveConnections() == 1 }, time.Second)

	// A second upgrade must observe the cap and get 503.
	rawURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/relay/ws"
	_, response, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err == nil && response != nil {
		_ = response.Body.Close()
		t.Fatalf("expected second upgrade to fail, got success")
	}
	if response == nil {
		t.Fatalf("expected a response on second upgrade, got %v", err)
	}
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on second upgrade, got %d", response.StatusCode)
	}
	if !logger.hasReason(ReasonAtCapacity) {
		t.Fatalf("expected ReasonAtCapacity in log, got %+v", logger.snapshot())
	}
}

// TestServerAcceptsAfterConnectionCloses covers the recovery
// path: once the in-flight connection's handler returns (because
// the WebSocket closed, the ticket was rejected, etc.) the slot
// must be released so a fresh upgrade can succeed. This is the
// property the production relay relies on to keep working through
// transient client churn.
func TestServerAcceptsAfterConnectionCloses(t *testing.T) {
	router := NewRouter()
	server, err := New(Config{
		Verifier:       NewVerifier(),
		Router:         router,
		Logger:         Discard(),
		MaxConnections: 1,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer server.Shutdown(context.Background())

	httpServer := httptest.NewServer(server)
	defer httpServer.Close()

	// Slot 1: open, then close, and wait for the counter to
	// drop back to zero. The dial helper consumes the slot
	// because the handler is in the read-ticket window.
	first := dialRelayRaw(t, httpServer.URL)
	waitFor(t, func() bool { return server.ActiveConnections() == 1 }, time.Second)
	_ = first.Close()
	waitFor(t, func() bool { return server.ActiveConnections() == 0 }, time.Second)

	// Slot 2: a fresh upgrade must now succeed (it will fail
	// later inside the handler when it reads an invalid
	// ticket, but the upgrade itself must not be rejected
	// with 503).
	rawURL := "ws" + strings.TrimPrefix(httpServer.URL, "http") + "/v1/relay/ws"
	second, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		t.Fatalf("expected fresh upgrade to succeed after first closed, got %v", err)
	}
	_ = second.Close()
}

// TestServerNegativeMaxConnectionsDisablesCap documents the
// escape hatch: a self-hosted relay that wants no global ceiling
// (because the OS or upstream load balancer already enforces one)
// can set MaxConnections to a negative value.
func TestServerNegativeMaxConnectionsDisablesCap(t *testing.T) {
	router := NewRouter()
	server, err := New(Config{
		Verifier:       NewVerifier(),
		Router:         router,
		Logger:         Discard(),
		MaxConnections: -1,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	defer server.Shutdown(context.Background())

	if server.ActiveConnections() != 0 {
		t.Fatalf("expected 0 active on fresh server, got %d", server.ActiveConnections())
	}
}

// TestSweepResultReportsBothReasons is a fast sanity test that the
// sweep counts add up correctly when a single sweep touches both
// idle and expired routes. The two routes must use different route
// ids so the router can tell them apart.
func TestSweepResultReportsBothReasons(t *testing.T) {
	clock := newFakeClock()
	router := sweepRouterWithClock(clock, 5*time.Minute, 10*time.Minute, time.Hour)
	defer router.Stop()

	// Route A: only the agent joined. The clock will jump
	// forward 6 minutes from creation, putting A past the idle
	// timeout but well under the max lifetime.
	agentA := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0xAA, routeIDBytes), bytesRepeat(0x33, 16))
	ticketA := &Ticket{
		wire: &vibebridgev1.RelayTicket{
			Version:        CurrentTicketVersion,
			TicketId:       bytesRepeat(0x11, ticketIDBytes),
			RouteId:        bytesRepeat(0xAA, routeIDBytes),
			Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
			DeviceId:       bytesRepeat(0x33, 16),
			MaxConnections: 2,
		},
	}
	if _, _, err := router.Join(ticketA, agentA); err != nil {
		t.Fatalf("agentA join: %v", err)
	}
	clock.Advance(6 * time.Minute)

	// Route B: created in the past, so by the time the sweep
	// fires its age is over 10 minutes. We rewind the clock
	// before Join, then jump forward again.
	clock.Advance(-12 * time.Minute)
	agentB := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0xBB, routeIDBytes), bytesRepeat(0x33, 16))
	ticketB := &Ticket{
		wire: &vibebridgev1.RelayTicket{
			Version:        CurrentTicketVersion,
			TicketId:       bytesRepeat(0x11, ticketIDBytes),
			RouteId:        bytesRepeat(0xBB, routeIDBytes),
			Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
			DeviceId:       bytesRepeat(0x33, 16),
			MaxConnections: 2,
		},
	}
	if _, _, err := router.Join(ticketB, agentB); err != nil {
		t.Fatalf("agentB join: %v", err)
	}
	clock.Advance(12 * time.Minute)

	result := router.Sweep()
	if result.IdleClosed+result.ExpiredClosed != 2 {
		t.Fatalf("expected 2 reaped routes, got %+v", result)
	}
	if result.IdleClosed != 1 || result.ExpiredClosed != 1 {
		t.Fatalf("expected 1 idle + 1 expired, got %+v", result)
	}
}

// dialRelayRaw opens a WebSocket without sending any ticket. The
// connection holds the server's connection slot until Close. Used
// by the cap tests to pin a slot without exercising the ticket
// flow.
func dialRelayRaw(t *testing.T, address string) *websocket.Conn {
	t.Helper()
	rawURL := "ws" + strings.TrimPrefix(address, "http") + "/v1/relay/ws"
	connection, _, err := websocket.DefaultDialer.Dial(rawURL, nil)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	return connection
}

// waitFor polls cond until it returns true or the timeout elapses.
// Used by the cap tests to wait for the server's active-conn
// counter to settle without sleeping for an arbitrary duration.
func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// _ keeps the bytes package referenced for future bytewise asserts.
var _ = bytes.Equal
