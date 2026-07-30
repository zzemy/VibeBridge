package relay

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"google.golang.org/protobuf/proto"
)

const (
	// maxTicketBytes is the largest single WebSocket message the
	// server will accept as a ticket. Real tickets are well under
	// 1 KiB; 8 KiB is a comfortable ceiling that still rejects
	// obvious abuse.
	maxTicketBytes = 8 * 1024
	// readLimit is the maximum WebSocket message size after ticket
	// admission. 1 MiB matches the size of a single Noise
	// transport record; larger frames are split by the noise
	// framing anyway.
	readLimit = 1 * 1024 * 1024
	// writeWait bounds a single WebSocket write so a stuck peer
	// cannot pin a writer goroutine indefinitely.
	writeWait = 5 * time.Second
	// pongWait is how long the server waits for a pong before
	// declaring the connection dead.
	pongWait = 60 * time.Second
	// pingPeriod is how often the server sends application-level
	// pings. Must be smaller than pongWait.
	pingPeriod = (pongWait * 9) / 10
	// handshakeDeadline is the total budget the server gives a
	// peer to present a valid ticket after the WebSocket
	// handshake completes.
	handshakeDeadline = 10 * time.Second
	// defaultMaxConnections is the global concurrency ceiling
	// the server applies when Config.MaxConnections is left
	// zero. Tuned for a community relay serving many phones
	// through a single Go process; self-hosted deployments
	// can raise the cap via Config.
	defaultMaxConnections = 4096
)

// Config configures a Server. The Verifier is required; the Router
// and Logger are optional and default to a fresh Router and a
// discard logger.
type Config struct {
	// Verifier is consulted on every WebSocket upgrade. It must
	// be safe for concurrent use.
	Verifier *Verifier
	// Router is the route table the server joins peers into. If
	// nil, the server constructs a fresh one using RouterConfig.
	Router *Router
	// RouterConfig is forwarded to NewRouterWithConfig when the
	// server constructs a default Router. Ignored if Router is
	// supplied.
	RouterConfig RouterConfig
	// Logger receives relay lifecycle events. nil is treated as
	// a discard logger.
	Logger Logger
	// AllowedOrigins is the list of origins the upgrader accepts.
	// A nil list is interpreted as the local server only. An
	// empty list is interpreted as no allowed origins (every
	// upgrade rejected).
	AllowedOrigins []string
	// MaxConnections caps the number of concurrent WebSocket
	// connections the server will accept. A zero value falls
	// back to defaultMaxConnections. Negative values disable
	// the cap (use with care; the server has no other global
	// concurrency limit).
	MaxConnections int
}

// Server is the HTTP entry point for the relay. Its zero value is
// not usable; construct one with New.
type Server struct {
	config      Config
	upgrader    websocket.Upgrader
	router      *Router
	verifier    *Verifier
	logger      Logger
	conns       sync.WaitGroup
	// acceptMu serializes the "check stopping + conns.Add" sequence at
	// the top of ServeHTTP. Without it, Shutdown's conns.Wait() can race
	// against a concurrent ServeHTTP that read stopping=false and is
	// about to call conns.Add(1) — the race detector flags the
	// WaitGroup's internal state as a data race. The mutex is held only
	// for the check+Add critical section; upgrader.Upgrade and the rest
	// of the handler run with the lock released.
	acceptMu    sync.Mutex
	stopping    atomic.Bool
	activeConns atomic.Int64
	maxConns    int64
	ownsRouter  bool
	stopSweeper func()
}

// New returns a Server with the supplied configuration. The
// configuration is copied by value; later mutations of Config do
// not affect the running server.
func New(config Config) (*Server, error) {
	if config.Verifier == nil {
		return nil, errors.New("relay server requires a Verifier")
	}
	if config.Router == nil {
		config.Router = NewRouterWithConfig(config.RouterConfig)
	}
	if config.Logger == nil {
		config.Logger = discardLogger{}
	}
	max := int64(config.MaxConnections)
	if max == 0 {
		max = defaultMaxConnections
	}
	server := &Server{
		config:     config,
		verifier:   config.Verifier,
		router:     config.Router,
		logger:     config.Logger,
		maxConns:   max,
		ownsRouter: config.Router == nil,
		upgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin:     buildOriginChecker(config.AllowedOrigins),
		},
	}
	// Always start a sweeper so orphan and expired routes get
	// reaped. stopSweeper is wired into Shutdown.
	server.stopSweeper = server.router.StartSweeper()
	return server, nil
}

// Router returns the router the server hands connections to. It is
// exposed for tests and for callers that want to share one router
// across multiple servers.
func (server *Server) Router() *Router {
	return server.router
}

// ActiveConnections returns the number of WebSocket connections the
// server is currently servicing. Exposed for observability hooks
// and quota tests.
func (server *Server) ActiveConnections() int {
	return int(server.activeConns.Load())
}

// SweepRoutes runs an immediate sweep of the route table and
// returns the result. Exposed for tests and for external schedulers
// that want to drive the sweeper off the background ticker.
func (server *Server) SweepRoutes() SweepResult {
	return server.router.Sweep()
}

// ServeHTTP implements http.Handler. The server accepts WebSocket
// upgrades, validates the presented ticket, and joins the peer to
// its route. Non-WebSocket requests receive 405.
func (server *Server) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	// acceptMu pairs with Shutdown: while we hold the lock, Shutdown
	// cannot reach conns.Wait(). This means every ServeHTTP that
	// passed the stopping check has already called conns.Add(1) before
	// the lock is released, and Shutdown's Wait will block on the
	// matching Done from the corresponding handleConnection.
	server.acceptMu.Lock()
	if server.stopping.Load() {
		server.acceptMu.Unlock()
		http.Error(writer, "relay is shutting down", http.StatusServiceUnavailable)
		return
	}
	server.conns.Add(1)
	server.acceptMu.Unlock()

	if !websocket.IsWebSocketUpgrade(request) {
		server.conns.Done()
		http.Error(writer, "expected WebSocket upgrade", http.StatusMethodNotAllowed)
		return
	}
	if server.maxConns > 0 && server.activeConns.Add(1) > server.maxConns {
		server.conns.Done()
		server.activeConns.Add(-1)
		server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonAtCapacity})
		http.Error(writer, "relay is at capacity", http.StatusServiceUnavailable)
		return
	}
	// Echo the client's requested subprotocol verbatim. The relay is a
	// transparent switchboard; both peers on a route negotiate their own
	// application-level protocol (e.g. vibebridge.v1) and the relay must
	// not silently strip the subprotocol, or the client may fall back to
	// a legacy protocol path.
	upgrader := server.upgrader
	upgrader.Subprotocols = websocket.Subprotocols(request)
	connection, err := upgrader.Upgrade(writer, request, nil)
	if err != nil {
		server.conns.Done()
		server.activeConns.Add(-1)
		server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
		return
	}
	go server.handleConnection(request.Context(), connection)
}

// Shutdown stops accepting new connections, joins every existing
// connection, and closes the router. Safe to call from a signal
// handler; further ServeHTTP calls return 503.
func (server *Server) Shutdown(ctx context.Context) error {
	if !server.stopping.CompareAndSwap(false, true) {
		return nil
	}
	if server.stopSweeper != nil {
		server.stopSweeper()
	}
	// Drain any in-flight ServeHTTP that read stopping=false before we
	// flipped it. acceptMu pairs with the same lock inside ServeHTTP:
	// once we acquire it here, every ServeHTTP that could Add(1) has
	// either already Add(1)'d or rejected, and any ServeHTTP that
	// arrives after the unlock sees stopping=true and returns 503
	// without ever calling Add(1). From this point the conns counter
	// is stable and Wait is race-free.
	server.acceptMu.Lock()
	server.acceptMu.Unlock()
	server.conns.Wait()
	if server.ownsRouter {
		server.router.Stop()
	}
	return nil
}

// ListenAndServe is a small convenience wrapper around http.Server
// for callers that want a one-line setup.
func (server *Server) ListenAndServe(address string) error {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return err
	}
	httpServer := &http.Server{
		Handler:           server,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-context.Background().Done()
		_ = httpServer.Close()
	}()
	return httpServer.Serve(listener)
}

func (server *Server) handleConnection(ctx context.Context, connection *websocket.Conn) {
	defer server.conns.Done()
	defer server.activeConns.Add(-1)
	defer connection.Close()
	connection.SetReadLimit(readLimit)
	_ = connection.SetReadDeadline(time.Now().Add(handshakeDeadline))
	ticket, err := server.readTicket(connection)
	if err != nil {
		server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
		_ = connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "invalid ticket"),
			time.Now().Add(writeWait))
		return
	}
	verified, err := server.verifier.Verify(ticket)
	if err != nil {
		server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
		_ = connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(websocket.ClosePolicyViolation, "ticket rejected"),
			time.Now().Add(writeWait))
		return
	}
	peer := newWebSocketPeer(connection, verified, server.logger)
	joined, other, err := server.router.Join(verified, peer)
	if err != nil {
		server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
		closeCode := websocket.CloseTryAgainLater
		switch err {
		case ErrRouteFull:
			closeCode = websocket.CloseTryAgainLater
		case ErrRouteEndpoint:
			closeCode = websocket.ClosePolicyViolation
		default:
			closeCode = websocket.CloseInternalServerErr
		}
		_ = connection.WriteControl(websocket.CloseMessage,
			websocket.FormatCloseMessage(closeCode, "route join rejected"),
			time.Now().Add(writeWait))
		return
	}
	// Disable the read deadline; the per-peer pump manages
	// heartbeats from here on.
	_ = connection.SetReadDeadline(time.Time{})
	_ = ctx
	_ = other
	server.runPeer(ctx, connection, peer, joined)
}

func (server *Server) readTicket(connection *websocket.Conn) (*vibebridgev1.RelayTicket, error) {
	messageType, payload, err := connection.ReadMessage()
	if err != nil {
		return nil, fmt.Errorf("read first frame: %w", err)
	}
	if messageType != websocket.BinaryMessage {
		return nil, fmt.Errorf("first frame must be binary, got %d", messageType)
	}
	if len(payload) < 4 {
		return nil, errors.New("first frame too short for ticket length prefix")
	}
	if len(payload) > maxTicketBytes+4 {
		return nil, fmt.Errorf("first frame exceeds %d bytes", maxTicketBytes)
	}
	length := binary.BigEndian.Uint32(payload[:4])
	if int(length) > len(payload)-4 {
		return nil, errors.New("ticket length prefix larger than payload")
	}
	ticketBytes := payload[4 : 4+length]
	ticket := new(vibebridgev1.RelayTicket)
	if err := proto.Unmarshal(ticketBytes, ticket); err != nil {
		return nil, fmt.Errorf("decode ticket: %w", err)
	}
	return ticket, nil
}

func (server *Server) runPeer(ctx context.Context, connection *websocket.Conn, peer *webSocketPeer, route *Route) {
	defer server.router.Leave(peer)
	stopPings := server.startPinger(connection)
	defer close(stopPings)
	// Read pump: take each WebSocket frame, hand its payload to
	// the route for forwarding. The relay does not need to
	// understand the application protocol.
	for {
		messageType, payload, err := connection.ReadMessage()
		if err != nil {
			if !isExpectedClose(err) {
				server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
			}
			return
		}
		if messageType != websocket.BinaryMessage {
			continue
		}
		if err := route.Forward(payload); err != nil {
			switch {
			case errors.Is(err, ErrPeerMissing):
				// The other endpoint of the route has not
				// been joined yet. Drop the message but keep
				// the connection open so the route can be
				// completed when the other peer arrives.
				continue
			case errors.Is(err, ErrPeerClosed):
				// Route was dropped because a peer refused
				// to accept the forward (backpressure).
			default:
				server.logger.Log(Event{Outcome: OutcomeFailure, Reason: ReasonListenerError})
			}
			return
		}
	}
}

func (server *Server) startPinger(connection *websocket.Conn) chan struct{} {
	stop := make(chan struct{})
	connection.SetPongHandler(func(string) error {
		return connection.SetReadDeadline(time.Now().Add(pongWait))
	})
	go func() {
		ticker := time.NewTicker(pingPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				_ = connection.SetWriteDeadline(time.Now().Add(writeWait))
				if err := connection.WriteMessage(websocket.PingMessage, nil); err != nil {
					return
				}
			}
		}
	}()
	return stop
}

func isExpectedClose(err error) bool {
	if err == nil {
		return true
	}
	if websocket.IsCloseError(err,
		websocket.CloseNormalClosure,
		websocket.CloseGoingAway,
		websocket.CloseNoStatusReceived) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	return false
}

// webSocketPeer adapts a gorilla/websocket connection to the
// router's Peer interface.
type webSocketPeer struct {
	connection   *websocket.Conn
	ticket       *Ticket
	writeMu      sync.Mutex
	pendingSlots chan []byte
	closed       atomic.Bool
}

func newWebSocketPeer(connection *websocket.Conn, ticket *Ticket, _ Logger) *webSocketPeer {
	return &webSocketPeer{
		connection:   connection,
		ticket:       ticket,
		pendingSlots: make(chan []byte, 2),
	}
}

func (peer *webSocketPeer) Read(buffer []byte) (int, error) {
	_, payload, err := peer.connection.ReadMessage()
	if err != nil {
		return 0, err
	}
	if len(payload) > len(buffer) {
		// Caller's buffer is too small for this frame. Drop
		// the frame: the application is using a buffer that
		// cannot hold a single message. The caller may retry
		// with a larger buffer.
		return 0, io.ErrShortBuffer
	}
	copy(buffer, payload)
	return len(payload), nil
}

func (peer *webSocketPeer) Forward(plaintext []byte) error {
	if peer.closed.Load() {
		return ErrPeerClosed
	}
	peer.writeMu.Lock()
	defer peer.writeMu.Unlock()
	_ = peer.connection.SetWriteDeadline(time.Now().Add(writeWait))
	if err := peer.connection.WriteMessage(websocket.BinaryMessage, plaintext); err != nil {
		peer.closed.Store(true)
		_ = peer.connection.Close()
		return ErrPeerClosed
	}
	return nil
}

func (peer *webSocketPeer) Close() error {
	if peer.closed.Swap(true) {
		return nil
	}
	return peer.connection.Close()
}

func (peer *webSocketPeer) Endpoint() vibebridgev1.RelayEndpoint {
	return peer.ticket.Endpoint()
}

func (peer *webSocketPeer) RouteID() []byte {
	return peer.ticket.RouteID()
}

func (peer *webSocketPeer) DeviceID() []byte {
	return peer.ticket.DeviceID()
}

// Logger is the small lifecycle interface the relay uses. The
// relay does not log payload bytes; it only reports connection
// outcomes. The agentlog.Logger satisfies this contract when its
// Event names are mapped to relay events; we keep a local copy to
// avoid creating a package-level dependency on agentlog.
type Logger interface {
	Log(Event)
}

// Event is the relay's privacy-safe log payload. Only the
// structural fields are populated; no bytes from a ticket or
// websocket frame ever appear in a relay log.
type Event struct {
	Outcome string
	Reason  string
}

// Outcome/Reason values are kept as plain strings so this file does
// not import agentlog. Callers can adapt a relay Logger to the
// agentlog interface.
const (
	OutcomeFailure = "failure"
	OutcomeSuccess = "success"

	ReasonAgentShutdown    = "agent_shutdown"
	ReasonAtCapacity       = "at_capacity"
	ReasonExplicitEnd      = "explicit_end"
	ReasonIdleTimeout      = "idle_timeout"
	ReasonListenerClosed   = "listener_closed"
	ReasonListenerError    = "listener_error"
	ReasonProcessExit      = "process_exit"
	ReasonReconnectExpired = "reconnect_expired"
	ReasonRouteIdleTimeout = "route_idle_timeout"
	ReasonRouteMaxLifetime = "route_max_lifetime"
	ReasonSignal           = "signal"
	ReasonSuperseded       = "superseded"
)

type discardLogger struct{}

func (discardLogger) Log(Event) {}

// Discard is the relay's no-op Logger. It is exported for tests
// and for callers that want to silence the relay without
// importing agentlog.
func Discard() Logger { return discardLogger{} }

func buildOriginChecker(allowed []string) func(*http.Request) bool {
	// No list: only same-origin requests are accepted.
	if allowed == nil {
		return func(request *http.Request) bool {
			origin := request.Header.Get("Origin")
			if origin == "" {
				return true
			}
			host := request.Host
			if host == "" {
				return false
			}
			originURL, err := parseOrigin(origin)
			if err != nil {
				return false
			}
			return originURL.Host == host
		}
	}
	// Empty list: nothing is accepted.
	if len(allowed) == 0 {
		return func(*http.Request) bool { return false }
	}
	allow := make(map[string]struct{}, len(allowed))
	for _, entry := range allowed {
		allow[entry] = struct{}{}
	}
	return func(request *http.Request) bool {
		_, ok := allow[request.Header.Get("Origin")]
		return ok
	}
}

func parseOrigin(origin string) (*urlLike, error) {
	// Tiny URL-shape parser to avoid pulling in net/url for one
	// host comparison.
	if len(origin) < 8 {
		return nil, errors.New("origin too short")
	}
	scheme := origin[:5]
	rest := origin[5:]
	if scheme == "https" {
		rest = rest[3:]
	} else if scheme == "http:" {
		rest = rest[4:]
	} else {
		return nil, errors.New("unsupported origin scheme")
	}
	if len(rest) == 0 {
		return nil, errors.New("origin missing host")
	}
	return &urlLike{Host: rest}, nil
}

type urlLike struct {
	Host string
}
