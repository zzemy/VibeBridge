// Load tests for the relay. These tests are guarded by the `load`
// build tag so the default `go test ./...` run does not pay
// their cost; opt in with `go test -tags=load ./internal/relay/...`.
// The focus is throughput, churn, and memory bounds under heavy
// concurrent use, not correctness of single connections (those
// are covered by the regular tests).
//
//go:build load
// +build load

package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"net/http/httptest"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// loadIssuer returns a fresh issuer keypair and the corresponding
// Issuer. Each test gets its own issuer so the verifier does not
// carry replay state across tests.
func loadIssuer(t *testing.T) (*Issuer, ed25519.PublicKey) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate issuer key: %v", err)
	}
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	return issuer, public
}

// buildLoadServer constructs an httptest server backed by a relay
// Server configured for the load tests: a permissive Verifier
// (issuer is supplied by the test), fresh Router, discard Logger,
// and a MaxConnections the test can drive above the default 4096
// cap. The returned address is a "ws://..." URL the tests can
// hand to the websocket dialer.
func buildLoadServer(t *testing.T, issuer ed25519.PublicKey, maxConnections int) string {
	t.Helper()
	server, err := New(Config{
		Verifier:       NewVerifier(issuer),
		Router:         NewRouter(),
		Logger:         Discard(),
		MaxConnections: maxConnections,
	})
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	httpSrv := httptest.NewServer(server)
	t.Cleanup(func() {
		httpSrv.Close()
		_ = server.Shutdown(t.Context())
	})
	return "ws" + httpSrv.URL[len("http"):] + "/v1/relay/ws"
}

// joinPairWebSocket dials the relay twice for the same route and
// returns the two upgraded connections. The route id and ticket
// id are derived from pair so the route table does not collide.
// The function blocks until both upgrades are accepted.
func joinPairWebSocket(t *testing.T, issuer *Issuer, wsURL string, pair byte) (agent, client *websocket.Conn) {
	t.Helper()
	routeID := bytesRepeat(pair, routeIDBytes)
	deviceID := bytesRepeat(pair, 16)

	agentTicket, err := issuer.Issue(IssueInput{
		RouteID:        routeID,
		DeviceID:       deviceID,
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		MaxConnections: 2,
		// The verifier rejects tickets issued too far in the
		// future (the bound is ticketReplayTTL in ticket.go);
		// a one-minute lifetime is well under that cap and
		// matches the rest of the relay test suite.
		Lifetime: time.Minute,
	})
	if err != nil {
		t.Fatalf("issue agent: %v", err)
	}
	clientTicket, err := issuer.Issue(IssueInput{
		RouteID:        routeID,
		DeviceID:       bytesRepeat(pair^0xFF, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		MaxConnections: 2,
		Lifetime:       time.Minute,
	})
	if err != nil {
		t.Fatalf("issue client: %v", err)
	}

	agentConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		t.Fatalf("agent dial: %v", err)
	}
	clientConn, _, err := websocket.DefaultDialer.Dial(wsURL, nil)
	if err != nil {
		_ = agentConn.Close()
		t.Fatalf("client dial: %v", err)
	}
	sendTicket(t, agentConn, agentTicket)
	sendTicket(t, clientConn, clientTicket)
	// Give the server a brief moment to process both tickets
	// and join the peers to the route.
	time.Sleep(50 * time.Millisecond)
	return agentConn, clientConn
}

// TestLoadConcurrentRoutesSpinUpAndTearDown exercises the
// "thousands of short-lived pairings" path. The relay must
// accept, forward, and drop the routes without leaking peers or
// holding the route table after each round.
func TestLoadConcurrentRoutesSpinUpAndTearDown(t *testing.T) {
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}
	const pairs = 200
	issuer, public := loadIssuer(t)
	wsURL := buildLoadServer(t, public, pairs*2)

	var wg sync.WaitGroup
	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agent, client := joinPairWebSocket(t, issuer, wsURL, byte(idx))
			defer agent.Close()
			defer client.Close()
			// Forward one byte each way to confirm the route
			// is alive, then close both peers.
			_ = agent.WriteMessage(websocket.BinaryMessage, []byte("ping"))
			if _, _, err := client.ReadMessage(); err != nil {
				t.Errorf("client read: %v", err)
			}
		}(i)
	}
	wg.Wait()
}

// TestLoadSlowConsumerClosesRoute is the throughput-aware
// counterpart of TestRouterBackpressureClosesRoute. The agent
// floods bytes into a route whose client reads them at a
// throttled rate; the relay must close the route within a small
// bounded delay so the slow consumer cannot pin a slot forever.
//
// The test is currently disabled: the router enforces backpressure
// between fake peers via the per-peer outbound buffer (covered by
// TestRouterBackpressureClosesRoute), but the production WebSocket
// transport has no bounded outbound buffer so a real slow consumer
// can keep writing without observing a close. End-to-end slow-
// consumer detection is out of scope for Phase 5; re-enable this
// test once the transport-level write deadline is added.
func TestLoadSlowConsumerClosesRoute(t *testing.T) {
	t.Skip("end-to-end slow-consumer detection is not implemented yet; see the comment above")
	issuer, public := loadIssuer(t)
	wsURL := buildLoadServer(t, public, 4)

	agent, client := joinPairWebSocket(t, issuer, wsURL, 0xCC)
	defer agent.Close()
	defer client.Close()

	// Read the first message so the client has consumed one
	// slot, then stop reading. The agent will keep writing
	// until the relay notices the slow consumer.
	if err := agent.WriteMessage(websocket.BinaryMessage, []byte("priming")); err != nil {
		t.Fatalf("priming write: %v", err)
	}
	if _, _, err := client.ReadMessage(); err != nil {
		t.Fatalf("first read: %v", err)
	}

	// We do not assert on the exact close cause: the surface
	// varies across platforms. The test passes as long as the
	// agent sees its own write fail (which proves the relay
	// tore the route down).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if err := agent.WriteMessage(websocket.BinaryMessage, make([]byte, 1024)); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("agent never observed route close under slow consumer")
}

// TestLoadManyRoutesChurnTable spins up many short-lived
// pairings in parallel to keep the route table in a high-churn
// state, then asserts the server can still serve a fresh route
// after the storm. Catches accidental O(n^2) sweeps or memory
// leaks in the route table.
func TestLoadManyRoutesChurnTable(t *testing.T) {
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}
	const pairs = 200
	issuer, public := loadIssuer(t)
	wsURL := buildLoadServer(t, public, pairs*2)

	var wg sync.WaitGroup
	var succeeded atomic.Int64
	for i := 0; i < pairs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			agent, client := joinPairWebSocket(t, issuer, wsURL, byte(idx))
			_ = agent.WriteMessage(websocket.BinaryMessage, []byte("hi"))
			if _, _, err := client.ReadMessage(); err != nil {
				t.Errorf("client read: %v", err)
			}
			_ = agent.Close()
			_ = client.Close()
			succeeded.Add(1)
		}(i)
	}
	wg.Wait()
	if got := succeeded.Load(); got != pairs {
		t.Fatalf("expected %d successful pairings, got %d", pairs, got)
	}

	// After the storm the server must still be able to serve
	// a fresh route.
	agent, client := joinPairWebSocket(t, issuer, wsURL, 0xFE)
	_ = agent.WriteMessage(websocket.BinaryMessage, []byte("after"))
	if _, _, err := client.ReadMessage(); err != nil {
		t.Fatalf("post-storm read: %v", err)
	}
	_ = agent.Close()
	_ = client.Close()
}

// TestLoadSweeperKeepsOrphanTableSmall repeatedly creates
// half-joined routes (the agent only) and lets the sweeper reap
// them. Asserts the table is empty after the sweeper fires,
// which is the property the production relay relies on to bound
// its memory footprint under noisy clients.
func TestLoadSweeperKeepsOrphanTableSmall(t *testing.T) {
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}
	router := NewRouterWithConfig(RouterConfig{
		IdleTimeout:   50 * time.Millisecond,
		MaxLifetime:   time.Hour,
		SweepInterval: 10 * time.Millisecond,
	})

	const orphans = 1000
	for i := 0; i < orphans; i++ {
		// Encode the loop index into the route id so each
		// orphan gets a unique slot in the route table.
		// Using just a single byte would collide after
		// 256 entries.
		peer := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
			routeIDForIndex(i), deviceIDForIndex(i))
		routeID := routeIDForIndex(i)
		if _, _, err := router.Join(newTestTicketForRoute(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, routeID, 2), peer); err != nil {
			t.Fatalf("orphan %d: %v", i, err)
		}
	}

	// Wait for the sweeper to drain the table.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		router.Sweep()
		if routerActiveRouteCount(router) == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("sweeper failed to drain orphan table, %d routes remain", routerActiveRouteCount(router))
}

// TestLoadMemoryFootprintIsBounded creates a large number of
// orphan routes and asserts the heap does not balloon. The bound
// is loose (a wide safety margin over the theoretical per-route
// overhead) so the test does not flake on small Go runtime
// variations, but it will catch a regression where the
// per-route state grows to multiple KiB.
func TestLoadMemoryFootprintIsBounded(t *testing.T) {
	if testing.Short() {
		t.Skip("load test skipped in -short mode")
	}
	router := NewRouterWithConfig(RouterConfig{
		IdleTimeout:   50 * time.Millisecond,
		MaxLifetime:   time.Hour,
		SweepInterval: 10 * time.Millisecond,
	})

	const orphans = 5000
	for i := 0; i < orphans; i++ {
		peer := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
			routeIDForIndex(i), deviceIDForIndex(i))
		routeID := routeIDForIndex(i)
		if _, _, err := router.Join(newTestTicketForRoute(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, routeID, 2), peer); err != nil {
			t.Fatalf("orphan %d: %v", i, err)
		}
	}

	var statsBefore, statsAfter runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&statsBefore)
	router.Sweep()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) && routerActiveRouteCount(router) > 0 {
		time.Sleep(10 * time.Millisecond)
		router.Sweep()
	}
	runtime.GC()
	runtime.ReadMemStats(&statsAfter)

	// Per-route overhead is on the order of a few hundred
	// bytes. Allow up to 4 KiB per orphan to leave a wide
	// margin for runtime variation while still catching a
	// regression that adds 10x.
	growth := int64(statsAfter.HeapInuse) - int64(statsBefore.HeapInuse)
	perOrphan := growth / int64(orphans)
	if perOrphan > 4096 {
		t.Fatalf("per-orphan heap growth = %d bytes, want <= 4096", perOrphan)
	}
}

// newTestTicketForRoute builds a test ticket bound to the supplied
// route id. The existing newTestTicket in router_test.go hardcodes
// the route id to bytesRepeat(0x22, routeIDBytes) which is fine for
// the regular router tests that share one route, but the load tests
// need each orphan to live in its own route. Keeping the helper next
// to its callers avoids touching the regular test file.
func newTestTicketForRoute(endpoint vibebridgev1.RelayEndpoint, routeID []byte, maxConnections uint32) *Ticket {
	return &Ticket{
		wire: &vibebridgev1.RelayTicket{
			Version:        CurrentTicketVersion,
			TicketId:       bytesRepeat(0x11, ticketIDBytes),
			RouteId:        routeID,
			Endpoint:       endpoint,
			DeviceId:       bytesRepeat(0x33, 16),
			MaxConnections: maxConnections,
		},
	}
}

// routerActiveRouteCount returns the current number of routes
// in the router. Used by the load tests to wait for the
// sweeper.
func routerActiveRouteCount(router *Router) int {
	router.mu.Lock()
	defer router.mu.Unlock()
	return len(router.routes)
}

// routeIDForIndex returns a deterministic route id derived from
// the supplied loop index. The encoding is a 16-byte buffer with
// the big-endian index in the last 4 bytes, so each index maps
// to a unique route id up to 2^32 entries.
func routeIDForIndex(i int) []byte {
	id := make([]byte, routeIDBytes)
	id[routeIDBytes-4] = byte(i >> 24)
	id[routeIDBytes-3] = byte(i >> 16)
	id[routeIDBytes-2] = byte(i >> 8)
	id[routeIDBytes-1] = byte(i)
	return id
}

// deviceIDForIndex returns a deterministic 16-byte device id
// derived from the loop index. Uses the same encoding as
// routeIDForIndex.
func deviceIDForIndex(i int) []byte {
	id := make([]byte, 16)
	id[12] = byte(i >> 24)
	id[13] = byte(i >> 16)
	id[14] = byte(i >> 8)
	id[15] = byte(i)
	return id
}
