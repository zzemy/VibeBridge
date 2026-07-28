package relay

import (
	"bytes"
	"errors"
	"io"
	"sync"
	"testing"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// fakePeer is a minimal Peer implementation backed by channels. The
// router test only needs Read (return EOF on close) and Forward
// (deliver to an outbound channel).
type fakePeer struct {
	endpoint  vibebridgev1.RelayEndpoint
	routeID   []byte
	deviceID  []byte
	outbound  chan []byte
	closeOnce sync.Once
	closed    chan struct{}
}

func newFakePeer(endpoint vibebridgev1.RelayEndpoint, routeID, deviceID []byte) *fakePeer {
	return &fakePeer{
		endpoint: endpoint,
		routeID:  routeID,
		deviceID: deviceID,
		outbound: make(chan []byte, 16),
		closed:   make(chan struct{}),
	}
}

func (peer *fakePeer) Read(_ []byte) (int, error) { return 0, io.EOF }

func (peer *fakePeer) Forward(plaintext []byte) error {
	select {
	case <-peer.closed:
		return ErrPeerClosed
	default:
	}
	select {
	case peer.outbound <- append([]byte(nil), plaintext...):
		return nil
	default:
		// Simulate a slow consumer: the relay should treat this
		// as a closed peer.
		return ErrPeerClosed
	}
}

func (peer *fakePeer) Close() error {
	peer.closeOnce.Do(func() { close(peer.closed) })
	return nil
}

func (peer *fakePeer) Endpoint() vibebridgev1.RelayEndpoint { return peer.endpoint }
func (peer *fakePeer) RouteID() []byte                       { return peer.routeID }
func (peer *fakePeer) DeviceID() []byte                      { return peer.deviceID }

func newTestTicket(endpoint vibebridgev1.RelayEndpoint, maxConnections uint32) *Ticket {
	return &Ticket{
		wire: &vibebridgev1.RelayTicket{
			Version:        CurrentTicketVersion,
			TicketId:       bytesRepeat(0x11, ticketIDBytes),
			RouteId:        bytesRepeat(0x22, routeIDBytes),
			Endpoint:       endpoint,
			DeviceId:       bytesRepeat(0x33, 16),
			MaxConnections: maxConnections,
		},
	}
}

func TestRouterJoinTwoPeersAndForward(t *testing.T) {
	router := NewRouter()
	defer router.Stop()
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x44, 16))

	first, other, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent)
	if err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if other != nil {
		t.Fatalf("expected no other peer on first join, got %v", other)
	}
	second, other, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client)
	if err != nil {
		t.Fatalf("client join: %v", err)
	}
	if other != agent {
		t.Fatalf("expected agent as other peer, got %v", other)
	}

	payload := []byte("hello client")
	if err := first.Forward(payload); err != nil {
		t.Fatalf("agent→client forward: %v", err)
	}
	select {
	case got := <-client.outbound:
		if !bytes.Equal(got, payload) {
			t.Fatalf("client received %q, want %q", got, payload)
		}
	default:
		t.Fatalf("client did not receive forwarded payload")
	}

	if err := second.Forward([]byte("ack")); err != nil {
		t.Fatalf("client→agent forward: %v", err)
	}
	select {
	case got := <-agent.outbound:
		if !bytes.Equal(got, []byte("ack")) {
			t.Fatalf("agent received %q, want %q", got, []byte("ack"))
		}
	default:
		t.Fatalf("agent did not receive forwarded payload")
	}
}

func TestRouterRejectsDuplicateEndpoint(t *testing.T) {
	router := NewRouter()
	defer router.Stop()
	first := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	second := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x55, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 4), first); err != nil {
		t.Fatalf("first join: %v", err)
	}
	_, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 4), second)
	if !errors.Is(err, ErrRouteEndpoint) {
		t.Fatalf("expected ErrRouteEndpoint, got %v", err)
	}
}

func TestRouterRejectsWhenAtCapacity(t *testing.T) {
	router := NewRouter()
	defer router.Stop()
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	extra := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x66, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}
	_, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), extra)
	if !errors.Is(err, ErrRouteFull) {
		t.Fatalf("expected ErrRouteFull, got %v", err)
	}
}

func TestRouterLeaveClosesRoute(t *testing.T) {
	router := NewRouter()
	defer router.Stop()
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}
	router.Leave(agent)
	// After the agent leaves, the route is destroyed; a second
	// client join should create a fresh route.
	second := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x77, 16))
	_, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), second)
	if err != nil {
		t.Fatalf("second join after leave: %v", err)
	}
}

func TestRouterStopClosesAllPeers(t *testing.T) {
	router := NewRouter()
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent); err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}
	router.Stop()
	// A second Stop is a no-op.
	router.Stop()
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2),
		newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, bytesRepeat(0x22, routeIDBytes), bytesRepeat(0x33, 16))); !errors.Is(err, ErrRouterStopped) {
		t.Fatalf("expected ErrRouterStopped, got %v", err)
	}
}

// TestRouterBackpressureClosesRoute verifies that the router drops
// the route the moment a peer refuses a forward. The fakePeer model
// fits a slow consumer by returning ErrPeerClosed when its outbound
// channel is full; the router is expected to tear the route down so
// neither side can keep accumulating state.
func TestRouterBackpressureClosesRoute(t *testing.T) {
	router := NewRouter()
	defer router.Stop()
	routeID := bytesRepeat(0x22, routeIDBytes)
	agent := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		routeID, bytesRepeat(0x33, 16))
	client := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		routeID, bytesRepeat(0x44, 16))

	route, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, 2), agent)
	if err != nil {
		t.Fatalf("agent join: %v", err)
	}
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), client); err != nil {
		t.Fatalf("client join: %v", err)
	}

	// Fill the client's outbound channel so the next forward fails.
	for i := 0; i < cap(client.outbound); i++ {
		if err := route.Forward([]byte("flood")); err != nil {
			t.Fatalf("forward %d unexpectedly failed: %v", i, err)
		}
	}

	// One more forward must fail and must also tear down the route.
	if err := route.Forward([]byte("overflow")); !errors.Is(err, ErrPeerClosed) {
		t.Fatalf("expected ErrPeerClosed, got %v", err)
	}

	// The router closes both peers when the route is dropped, so
	// the client and agent peers must be observed as closed.
	select {
	case <-client.closed:
	default:
		t.Fatalf("expected client peer to be closed after backpressure")
	}
	select {
	case <-agent.closed:
	default:
		t.Fatalf("expected agent peer to be closed after backpressure")
	}

	// A fresh join on the same route id must succeed because the
	// router dropped the old route when it closed the peers.
	fresh := newFakePeer(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
		routeID, bytesRepeat(0x55, 16))
	if _, _, err := router.Join(newTestTicket(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, 2), fresh); err != nil {
		t.Fatalf("fresh join after backpressure: %v", err)
	}
}
