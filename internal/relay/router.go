package relay

import (
	"errors"
	"io"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

var (
	ErrRouteFull      = errors.New("relay route is at capacity")
	ErrRouteEndpoint  = errors.New("relay route endpoint is already occupied")
	ErrRouteMismatch  = errors.New("relay route id does not match the ticket")
	ErrRouteUnknown   = errors.New("relay route is not registered")
	ErrPeerClosed     = errors.New("relay peer is closed")
	ErrPeerMismatched = errors.New("relay peer does not match the route endpoint")
	ErrPeerMissing    = errors.New("relay peer endpoint has not been joined yet")
	ErrRouterStopped  = errors.New("relay router has been stopped")
)

// Peer is the byte pipe the router pushes and pulls from. The
// underlying WebSocket is wrapped so the router does not have to know
// about gorilla/websocket internals. Writes are non-blocking: when
// the per-peer outbound buffer is full, Forward returns
// ErrPeerClosed so the caller can close both sides of the route.
type Peer interface {
	io.Reader
	io.Closer
	// Forward writes plaintext to the peer. The call must not block
	// past the configured per-peer buffer size; if the buffer is full
	// the implementation must close itself and return ErrPeerClosed.
	Forward(plaintext []byte) error
	// Endpoint reports which side of the route this peer is bound to.
	Endpoint() vibebridgev1.RelayEndpoint
	// RouteID is the id of the route this peer is bound to. The
	// router uses it as the table key but the peer also exposes it
	// for diagnostic hooks.
	RouteID() []byte
	// DeviceID is the device id of the peer that presented the
	// ticket. It is exposed for logging and re-validation; the
	// router does not enforce uniqueness on it.
	DeviceID() []byte
}

// pendingBufferSlots is the per-route outbound buffer depth. Two
// slots per peer side is enough to keep a small interactive pipe
// flowing without giving a single slow consumer an unbounded queue
// to fill.
const pendingBufferSlots = 4

// defaultRouteIdleTimeout is how long a half-joined route is
// allowed to sit waiting for its other peer before the sweeper
// reaps it. A route that never gets a second peer is an orphan:
// the issuer has handed out a ticket but the recipient never
// showed up, and the relay is wasting memory holding the slot.
const defaultRouteIdleTimeout = 5 * time.Minute

// defaultRouteMaxLifetime is the hard ceiling on how long a
// route can live, even if both peers stay busy. Tickets are
// already short-lived; capping the relay's view of the route at
// the same order of magnitude keeps the relay's table small even
// under reconnect storms or runaway clients.
const defaultRouteMaxLifetime = 30 * time.Minute

// defaultSweepInterval is how often the sweeper scans the route
// table. The interval is well under the smallest timeout so a
// reaped route is closed within a small bounded delay.
const defaultSweepInterval = 30 * time.Second

// RouterConfig tunes the lifecycle policy of a Router. The zero
// value is valid and produces a router with the package defaults.
type RouterConfig struct {
	// IdleTimeout drops a route that has been silent for at
	// least this long. A route counts as silent from the moment
	// it is created until both peers have joined; once both
	// peers are present the timer is reset on every successful
	// Forward. A zero value falls back to
	// defaultRouteIdleTimeout.
	IdleTimeout time.Duration
	// MaxLifetime caps the absolute age of a route. A zero
	// value falls back to defaultRouteMaxLifetime. Set to a
	// negative value to disable the cap.
	MaxLifetime time.Duration
	// SweepInterval controls how often the background sweeper
	// runs. A zero value falls back to defaultSweepInterval.
	SweepInterval time.Duration
	// Now is the clock used for all expiry decisions. Tests
	// inject a fake clock; production leaves it nil and gets
	// time.Now.
	Now func() time.Time
}

// SweepResult is the count of routes the sweeper closed during a
// single Sweep call. It is exposed so tests and observability
// hooks can observe reaper activity without polling internals.
type SweepResult struct {
	IdleClosed    int
	ExpiredClosed int
}

// route is the per-route slot held inside Router. A route tracks
// the two peer ends, the small outbound buffer the server uses to
// pass bytes between them, and the timestamps the sweeper uses to
// reap orphan or expired routes.
type route struct {
	id            string
	connectionCap uint32
	agent         Peer
	client        Peer
	createdAt     time.Time
	lastActivity  time.Time
}

func (r *route) occupied() int {
	count := 0
	if r.agent != nil {
		count++
	}
	if r.client != nil {
		count++
	}
	return count
}

func (r *route) otherOf(peer Peer) Peer {
	if peer == nil {
		return nil
	}
	switch peer.Endpoint() {
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT:
		return r.client
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT:
		return r.agent
	}
	return nil
}

// Router joins verified Peers to a shared route and forwards
// bytes between them. The router is safe for concurrent use.
type Router struct {
	mu       sync.Mutex
	routes   map[string]*route
	stopped  bool
	idle     time.Duration
	maxLife  time.Duration
	sweepInt time.Duration
	now      func() time.Time
}

// NewRouter returns an empty Router using the package defaults
// for idle timeout, max lifetime, and sweep interval. Equivalent
// to NewRouterWithConfig(RouterConfig{}).
func NewRouter() *Router {
	return NewRouterWithConfig(RouterConfig{})
}

// NewRouterWithConfig returns a Router whose lifecycle policy is
// driven by cfg. The router is safe for concurrent use; no
// background goroutines run until StartSweeper is called.
func NewRouterWithConfig(cfg RouterConfig) *Router {
	now := cfg.Now
	if now == nil {
		now = time.Now
	}
	idle := cfg.IdleTimeout
	if idle <= 0 {
		idle = defaultRouteIdleTimeout
	}
	maxLife := cfg.MaxLifetime
	if maxLife == 0 {
		maxLife = defaultRouteMaxLifetime
	}
	sweepInt := cfg.SweepInterval
	if sweepInt <= 0 {
		sweepInt = defaultSweepInterval
	}
	return &Router{
		routes:   make(map[string]*route),
		idle:     idle,
		maxLife:  maxLife,
		sweepInt: sweepInt,
		now:      now,
	}
}

// StartSweeper launches a background goroutine that periodically
// calls Sweep to reap orphan and expired routes. The goroutine
// exits when the returned stop function is called. StartSweeper
// is a no-op if the router is already stopped.
func (router *Router) StartSweeper() (stop func()) {
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(router.sweepInt)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				router.Sweep()
			}
		}
	}()
	return func() {
		select {
		case <-done:
		default:
			close(done)
		}
	}
}

// Sweep walks the route table and closes any route that has been
// idle for longer than the configured IdleTimeout or that has
// lived longer than the configured MaxLifetime. The result counts
// the closed routes by reason. Safe to call concurrently with
// Join, Leave, and Forward.
func (router *Router) Sweep() SweepResult {
	router.mu.Lock()
	if router.stopped {
		router.mu.Unlock()
		return SweepResult{}
	}
	now := router.now()
	var toClose []*route
	for _, rt := range router.routes {
		switch {
		case router.maxLife > 0 && now.Sub(rt.createdAt) >= router.maxLife:
			toClose = append(toClose, rt)
		case router.idle > 0 && now.Sub(rt.lastActivity) >= router.idle:
			toClose = append(toClose, rt)
		}
	}
	for _, rt := range toClose {
		delete(router.routes, rt.id)
	}
	router.mu.Unlock()
	var result SweepResult
	for _, rt := range toClose {
		if router.maxLife > 0 && now.Sub(rt.createdAt) >= router.maxLife {
			result.ExpiredClosed++
		} else {
			result.IdleClosed++
		}
		router.closeRoute(rt)
	}
	return result
}

// Stop closes every peer in every route and prevents future joins.
// Idempotent. If a sweeper is running its stop function should be
// called before Stop so the goroutine exits before the map is
// cleared.
func (router *Router) Stop() {
	router.mu.Lock()
	if router.stopped {
		router.mu.Unlock()
		return
	}
	router.stopped = true
	routes := router.routes
	router.routes = make(map[string]*route)
	router.mu.Unlock()
	for _, r := range routes {
		router.closeRoute(r)
	}
}

// Join attaches a peer to its route. The ticket determines the
// route id and endpoint, and the ticket's MaxConnections field is
// the absolute cap on the number of peers in the route. The caller
// must already have verified the ticket; the router does no
// signature work of its own.
//
// On success, Join returns a route handle and the other peer already
// joined to it (if any). If the route is empty the second return is
// nil. If the join would exceed the cap, the route is left
// untouched and ErrRouteFull is returned.
func (router *Router) Join(ticket *Ticket, peer Peer) (*Route, Peer, error) {
	if router == nil {
		return nil, nil, ErrRouterStopped
	}
	if ticket == nil {
		return nil, nil, ErrRouteMismatch
	}
	if peer == nil {
		return nil, nil, ErrPeerClosed
	}
	routeID := string(ticket.RouteID())
	router.mu.Lock()
	if router.stopped {
		router.mu.Unlock()
		return nil, nil, ErrRouterStopped
	}
	rt, ok := router.routes[routeID]
	if !ok {
		// New route. Allocate an outbound buffer sized to
		// max_connections (clamped to a minimum so a tiny
		// max_connections still gives us a small pipe).
		cap := ticket.MaxConnections()
		if cap < 2 {
			cap = 2
		}
		now := router.now()
		rt = &route{
			id:            routeID,
			connectionCap: cap,
			createdAt:     now,
			lastActivity:  now,
		}
		router.routes[routeID] = rt
	}
	if rt.occupied() >= int(rt.connectionCap) {
		router.mu.Unlock()
		return nil, nil, ErrRouteFull
	}
	switch ticket.Endpoint() {
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT:
		if rt.agent != nil {
			router.mu.Unlock()
			return nil, nil, ErrRouteEndpoint
		}
		rt.agent = peer
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT:
		if rt.client != nil {
			router.mu.Unlock()
			return nil, nil, ErrRouteEndpoint
		}
		rt.client = peer
	default:
		router.mu.Unlock()
		return nil, nil, ErrRouteMismatch
	}
	other := rt.otherOf(peer)
	wrapped := &Route{
		id:       routeID,
		endpoint: ticket.Endpoint(),
		owner:    peer,
		router:   router,
	}
	router.mu.Unlock()
	return wrapped, other, nil
}

// Leave detaches a peer from its route and closes any remaining
// peers. It is safe to call on a peer that was never joined (no-op).
//
// The peer passed in is always closed: even when the call originates
// from this peer going away (its own connection is dropping) the
// close is a harmless idempotent signal, and when the call originates
// from the router deciding to drop a slow peer (see Route.Forward) the
// close is the only thing that actually stops that peer from
// receiving more bytes.
func (router *Router) Leave(peer Peer) {
	if router == nil || peer == nil {
		return
	}
	routeID := string(peer.RouteID())
	router.mu.Lock()
	rt, ok := router.routes[routeID]
	if !ok {
		router.mu.Unlock()
		return
	}
	var other Peer
	switch peer.Endpoint() {
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT:
		other = rt.client
		rt.agent = nil
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT:
		other = rt.agent
		rt.client = nil
	}
	shouldClose := rt.agent == nil || rt.client == nil
	if shouldClose {
		delete(router.routes, routeID)
	}
	router.mu.Unlock()
	if shouldClose {
		_ = peer.Close()
		if other != nil {
			_ = other.Close()
		}
	}
}

func (router *Router) closeRoute(rt *route) {
	if rt.agent != nil {
		_ = rt.agent.Close()
		rt.agent = nil
	}
	if rt.client != nil {
		_ = rt.client.Close()
		rt.client = nil
	}
}

// Route is a handle returned by Router.Join. It exposes the route id
// to the caller and gives a small ergonomic surface over the raw
// router.
type Route struct {
	id       string
	endpoint vibebridgev1.RelayEndpoint
	owner    Peer
	router   *Router
}

// ID returns the route id string. It is the same id the ticket was
// bound to.
func (route *Route) ID() string {
	return route.id
}

// Endpoint returns the endpoint of the peer that was joined to
// produce this handle.
func (route *Route) Endpoint() vibebridgev1.RelayEndpoint {
	return route.endpoint
}

// Forward pushes plaintext from the owner peer to the other peer on
// the route. The underlying call is non-blocking; on a full buffer
// the router closes the route so neither peer can be starved by
// the other. Forward is the only public write path the application
// uses; the router does not need to know what the bytes mean.
//
// If the other side of the route has not been joined yet the
// message cannot be delivered. Forward returns ErrPeerMissing in
// that case; the byte is dropped. The caller is expected to keep
// reading so the connection stays open and the route can still be
// completed when the second peer arrives.
func (route *Route) Forward(plaintext []byte) error {
	if route == nil || route.router == nil {
		return ErrRouterStopped
	}
	route.router.mu.Lock()
	rt, ok := route.router.routes[route.id]
	if !ok {
		route.router.mu.Unlock()
		return ErrRouteUnknown
	}
	dest := rt.otherOf(route.owner)
	if dest == nil {
		route.router.mu.Unlock()
		return ErrPeerMissing
	}
	rt.lastActivity = route.router.now()
	route.router.mu.Unlock()
	// Non-blocking send into the per-route buffer pool. If the
	// destination is too slow to drain it, close the route so the
	// slow consumer is forced to disconnect instead of letting the
	// relay's buffers grow without bound.
	if err := dest.Forward(plaintext); err != nil {
		// A peer returning any error from Forward is treated
		// as a closed consumer: drop the route so the slow or
		// dead side cannot keep the relay's buffers growing.
		route.router.Leave(dest)
		return err
	}
	return nil
}
