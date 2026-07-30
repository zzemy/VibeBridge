package relayclient

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	"github.com/zzemy/VibeBridge/internal/relay"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// DefaultTicketLifetime is the default validity window for relay
// tickets minted by the Manager. Five minutes gives the operator
// enough time to scan a QR code or paste a deep link without
// approaching the relay verifier's 10-minute replay window.
const DefaultTicketLifetime = 5 * time.Minute

// ManagerConfig configures the relay connection manager.
type ManagerConfig struct {
	// RelayURL is the WebSocket URL of the relay server
	// (e.g., "wss://relay.example.com/v1/relay/ws").
	RelayURL string
	// Issuer mints relay tickets. Required.
	Issuer *relay.Issuer
	// AgentID is the Agent's device ID (16 bytes). Required.
	AgentID []byte
	// RevocationEpoch is the current device-identity revocation
	// epoch, stamped onto minted tickets so relays with
	// --require-revocation-check can detect post-mint revocations.
	// Zero means no epoch (legacy tickets).
	RevocationEpoch uint64
	// TicketLifetime defaults to DefaultTicketLifetime if zero.
	TicketLifetime time.Duration
	// Dialer customizes the WebSocket dial. Zero value uses defaults.
	Dialer Dialer
}

// Manager coordinates outbound relay connections. It mints ticket
// pairs, tracks pending and active routes, and bridges relay streams
// to local transports. Manager is safe for concurrent use.
type Manager struct {
	config  ManagerConfig
	mu      sync.Mutex
	pending map[string]*vibebridgev1.RelayTicket // route ID hex -> agent ticket
	active  map[string]*managedConnection        // route ID hex -> active connection
}

type managedConnection struct {
	routeID string
	cancel  context.CancelFunc
	done    chan struct{}
}

// NewManager creates a relay connection manager.
func NewManager(config ManagerConfig) (*Manager, error) {
	if config.RelayURL == "" {
		return nil, errors.New("relay manager: RelayURL must not be empty")
	}
	if config.Issuer == nil {
		return nil, errors.New("relay manager: Issuer must not be nil")
	}
	if len(config.AgentID) != 16 {
		return nil, fmt.Errorf("relay manager: AgentID must be 16 bytes, got %d", len(config.AgentID))
	}
	if config.TicketLifetime <= 0 {
		config.TicketLifetime = DefaultTicketLifetime
	}
	return &Manager{
		config:  config,
		pending: make(map[string]*vibebridgev1.RelayTicket),
		active:  make(map[string]*managedConnection),
	}, nil
}

// Provision mints a ticket pair for a new relay session. The agent
// ticket is stored internally; the client ticket is returned for the
// remote peer (typically delivered via QR code or deep link).
// clientDeviceID must be 16 bytes.
func (m *Manager) Provision(clientDeviceID []byte) (clientTicket *vibebridgev1.RelayTicket, routeIDHex string, err error) {
	if len(clientDeviceID) != 16 {
		return nil, "", fmt.Errorf("relay manager: client device ID must be 16 bytes, got %d", len(clientDeviceID))
	}
	routeID := make([]byte, 16)
	if _, err := rand.Read(routeID); err != nil {
		return nil, "", fmt.Errorf("relay manager: generate route ID: %w", err)
	}
	routeIDHex = hex.EncodeToString(routeID)
	agentTicket, clientTicket, err := Pair(m.config.Issuer, PairInput{
		RouteID:        routeID,
		AgentID:        m.config.AgentID,
		ClientID:       clientDeviceID,
		Lifetime:       m.config.TicketLifetime,
		MaxConnections: 1,
		IssuerEpoch:    m.config.RevocationEpoch,
	})
	if err != nil {
		return nil, "", fmt.Errorf("relay manager: mint tickets: %w", err)
	}
	m.mu.Lock()
	m.pending[routeIDHex] = agentTicket
	m.mu.Unlock()
	return clientTicket, routeIDHex, nil
}

// Connect dials the relay with the agent ticket for the given route
// and bridges the resulting stream to local. The call blocks until
// the connection closes (either side EOF, error, or context
// cancellation). After the connection closes, the route is cleaned
// up and the pending ticket is consumed.
//
// Callers typically run Connect in a goroutine and use the route ID
// to track or cancel the connection via Close.
func (m *Manager) Connect(ctx context.Context, routeIDHex string, local io.ReadWriteCloser) error {
	m.mu.Lock()
	agentTicket, ok := m.pending[routeIDHex]
	if !ok {
		m.mu.Unlock()
		return fmt.Errorf("relay manager: no pending ticket for route %s", routeIDHex)
	}
	delete(m.pending, routeIDHex)
	if _, exists := m.active[routeIDHex]; exists {
		m.mu.Unlock()
		return fmt.Errorf("relay manager: route %s is already connected", routeIDHex)
	}
	connCtx, cancel := context.WithCancel(ctx)
	conn := &managedConnection{
		routeID: routeIDHex,
		cancel:  cancel,
		done:    make(chan struct{}),
	}
	m.active[routeIDHex] = conn
	m.mu.Unlock()

	// Watch for context cancellation. Bridge ignores ctx, so we
	// close local to unblock the io.Copy pumps when the context
	// is cancelled (CancelRoute or Close).
	go func() {
		select {
		case <-connCtx.Done():
			_ = local.Close()
		case <-conn.done:
		}
	}()

	err := ConnectAndBridge(connCtx, m.config.Dialer, m.config.RelayURL, agentTicket, local)

	cancel()
	close(conn.done)
	m.mu.Lock()
	delete(m.active, routeIDHex)
	m.mu.Unlock()

	return err
}

// CancelRoute cancels an active relay connection by route ID. If the
// route is not active, this is a no-op. The connection's Connect call
// will return with a context-cancellation error.
func (m *Manager) CancelRoute(routeIDHex string) {
	m.mu.Lock()
	conn, ok := m.active[routeIDHex]
	m.mu.Unlock()
	if !ok {
		return
	}
	conn.cancel()
}

// Close drops all active and pending relay connections. It cancels
// the context of each active connection and clears the pending
// ticket pool. Active Connect callers will receive a
// context-cancellation error.
func (m *Manager) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, conn := range m.active {
		conn.cancel()
	}
	m.active = make(map[string]*managedConnection)
	m.pending = make(map[string]*vibebridgev1.RelayTicket)
	return nil
}

// ActiveRoutes returns the hex-encoded route IDs of active relay
// connections (routes where Connect is currently running).
func (m *Manager) ActiveRoutes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := make([]string, 0, len(m.active))
	for routeID := range m.active {
		routes = append(routes, routeID)
	}
	return routes
}

// PendingRoutes returns the hex-encoded route IDs of provisioned but
// not yet connected routes.
func (m *Manager) PendingRoutes() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	routes := make([]string, 0, len(m.pending))
	for routeID := range m.pending {
		routes = append(routes, routeID)
	}
	return routes
}

// RelayURL returns the relay server URL the manager is configured to
// dial.
func (m *Manager) RelayURL() string {
	return m.config.RelayURL
}
