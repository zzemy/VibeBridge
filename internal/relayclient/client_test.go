package relayclient_test

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/relay"
	"github.com/zzemy/VibeBridge/internal/relayclient"
)

// captureLogger satisfies the relay.Logger interface and is safe for
// concurrent use. The privacy test asserts that no payload bytes
// ever appear in any log line.
type captureLogger struct {
	mu     sync.Mutex
	events []string
}

func (l *captureLogger) Log(evt relay.Event) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.events = append(l.events, fmt.Sprintf("%s/%s", evt.Outcome, evt.Reason))
}

func (l *captureLogger) snapshot() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]string, len(l.events))
	copy(out, l.events)
	return out
}

type relayFixture struct {
	server     *httptest.Server
	issuer     *relay.Issuer
	logger     *captureLogger
	agentID    []byte
	clientID   []byte
	routeID    []byte
	wsEndpoint string
}

func newRelayFixture(t *testing.T) *relayFixture {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	issuer, err := relay.NewIssuer(priv)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	logger := &captureLogger{}
	server, err := relay.New(relay.Config{
		Verifier: relay.NewVerifier(priv.Public().(ed25519.PublicKey)),
		Logger:   logger,
	})
	if err != nil {
		t.Fatalf("new relay server: %v", err)
	}
	httpSrv := httptest.NewServer(server)
	t.Cleanup(func() { httpSrv.Close() })
	return &relayFixture{
		server:     httpSrv,
		issuer:     issuer,
		logger:     logger,
		agentID:    bytes.Repeat([]byte{0x11}, 16),
		clientID:   bytes.Repeat([]byte{0x22}, 16),
		routeID:    bytes.Repeat([]byte{0xAB}, 16),
		wsEndpoint: "ws" + strings.TrimPrefix(httpSrv.URL, "http") + "/v1/relay/ws",
	}
}

func (f *relayFixture) mintPair(t *testing.T, lifetime time.Duration) (*vibebridgev1.RelayTicket, *vibebridgev1.RelayTicket) {
	t.Helper()
	agent, client, err := relayclient.Pair(f.issuer, relayclient.PairInput{
		RouteID:  f.routeID,
		AgentID:  f.agentID,
		ClientID: f.clientID,
		Lifetime: lifetime,
	})
	if err != nil {
		t.Fatalf("mint pair: %v", err)
	}
	return agent, client
}

func (f *relayFixture) dial(t *testing.T, ticket *vibebridgev1.RelayTicket) *relayclient.Stream {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	stream, err := relayclient.Dial(ctx, f.wsEndpoint, ticket)
	if err != nil {
		t.Fatalf("dial relay: %v", err)
	}
	return stream
}

func TestRoundTrip(t *testing.T) {
	f := newRelayFixture(t)
	agentTicket, clientTicket := f.mintPair(t, time.Minute)
	agent := f.dial(t, agentTicket)
	defer agent.Close()
	client := f.dial(t, clientTicket)
	defer client.Close()
	waitForJoin()

	// Agent -> Client
	agentPayload := []byte("ping from agent")
	if _, err := agent.Write(agentPayload); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	clientBuf := make([]byte, 16*1024)
	n, err := readFrame(client, clientBuf, 2*time.Second)
	if err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(clientBuf[:n], agentPayload) {
		t.Fatalf("client got %q, want %q", clientBuf[:n], agentPayload)
	}

	// Client -> Agent
	clientPayload := []byte("pong from client")
	if _, err := client.Write(clientPayload); err != nil {
		t.Fatalf("client write: %v", err)
	}
	agentBuf := make([]byte, 16*1024)
	n, err = readFrame(agent, agentBuf, 2*time.Second)
	if err != nil {
		t.Fatalf("agent read: %v", err)
	}
	if !bytes.Equal(agentBuf[:n], clientPayload) {
		t.Fatalf("agent got %q, want %q", agentBuf[:n], clientPayload)
	}
}

func TestBridgeEndToEnd(t *testing.T) {
	f := newRelayFixture(t)
	agentTicket, clientTicket := f.mintPair(t, time.Minute)

	// Dial the relay from the agent side and use net.Pipe() as the
	// local "transport" the bridge will pump into. The remote side
	// of the pipe is read by the test goroutine that pretends to
	// be the local Agent transport.
	localPipe, remotePipe := net.Pipe()
	defer localPipe.Close()
	defer remotePipe.Close()

	agent := f.dial(t, agentTicket)
	defer agent.Close()

	// Dial the client BEFORE anything is written: the relay drops
	// forward messages whose destination has not joined the route,
	// so a payload written before the client joins would be lost.
	client := f.dial(t, clientTicket)
	defer client.Close()
	waitForJoin()

	// Spin up the bridge in a goroutine. It owns both ends and
	// returns when either side closes.
	bridgeErr := make(chan error, 1)
	go func() {
		bridgeErr <- relayclient.Bridge(context.Background(), localPipe, agent)
	}()

	// Pretend the local Agent transport wrote something to the
	// pipe; the bridge should forward it over the relay to the
	// client.
	fromLocal := []byte("bridge sends upstream")
	if _, err := remotePipe.Write(fromLocal); err != nil {
		t.Fatalf("local pipe write: %v", err)
	}

	// The client should receive exactly fromLocal.
	got := make([]byte, len(fromLocal))
	if _, err := readFull(client, got, 2*time.Second); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, fromLocal) {
		t.Fatalf("client got %q, want %q", got, fromLocal)
	}

	// Close the local pipe to drain the bridge. Both io.Copy
	// goroutines will see EOF, the bridge returns, and we can
	// verify it surfaced no error.
	_ = remotePipe.Close()
	_ = localPipe.Close()
	select {
	case err := <-bridgeErr:
		if err != nil {
			t.Fatalf("bridge returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("bridge did not return after local close")
	}
}

func TestConnectAndBridge(t *testing.T) {
	f := newRelayFixture(t)
	agentTicket, clientTicket := f.mintPair(t, time.Minute)

	// Dial the client BEFORE anything is written: the relay drops
	// forward messages whose destination has not joined the route.
	client := f.dial(t, clientTicket)
	defer client.Close()

	localPipe, remotePipe := net.Pipe()
	defer remotePipe.Close()

	done := make(chan error, 1)
	go func() {
		done <- relayclient.ConnectAndBridge(context.Background(), relayclient.Dialer{}, f.wsEndpoint, agentTicket, localPipe)
	}()
	// Give the agent dial inside ConnectAndBridge time to land on
	// the route alongside the client.
	waitForJoin()

	// The agent's local transport writes upstream; the bridge
	// forwards it over the relay to the client.
	upstream := []byte("upstream via connect")
	if _, err := remotePipe.Write(upstream); err != nil {
		t.Fatalf("local pipe write: %v", err)
	}

	got := make([]byte, len(upstream))
	if _, err := readFull(client, got, 2*time.Second); err != nil {
		t.Fatalf("client read: %v", err)
	}
	if !bytes.Equal(got, upstream) {
		t.Fatalf("client got %q, want %q", got, upstream)
	}

	// Close to drain.
	_ = remotePipe.Close()
	_ = localPipe.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("connect+bridge returned %v, want nil", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("connect+bridge did not return")
	}
}

func TestNilTicketDial(t *testing.T) {
	f := newRelayFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := relayclient.Dial(ctx, f.wsEndpoint, nil); err == nil {
		t.Fatalf("expected error dialing with nil ticket, got nil")
	}
}

func TestShortRouteIDPair(t *testing.T) {
	f := newRelayFixture(t)
	_, _, err := relayclient.Pair(f.issuer, relayclient.PairInput{
		RouteID:  []byte{0x01, 0x02},
		AgentID:  f.agentID,
		ClientID: f.clientID,
		Lifetime: time.Minute,
	})
	if err == nil {
		t.Fatalf("expected error minting pair with short route id, got nil")
	}
}

func TestLifetimeTooLong(t *testing.T) {
	f := newRelayFixture(t)
	_, _, err := relayclient.Pair(f.issuer, relayclient.PairInput{
		RouteID:  f.routeID,
		AgentID:  f.agentID,
		ClientID: f.clientID,
		Lifetime: 30 * time.Minute,
	})
	if err == nil {
		t.Fatalf("expected error minting pair with 30m lifetime, got nil")
	}
}

func TestPrivacyNoPlaintextInLogs(t *testing.T) {
	f := newRelayFixture(t)
	agentTicket, clientTicket := f.mintPair(t, time.Minute)
	agent := f.dial(t, agentTicket)
	defer agent.Close()
	client := f.dial(t, clientTicket)
	defer client.Close()
	waitForJoin()

	// Distinct payloads we can grep for in the captured log. The
	// logger interface is the relay's privacy-safe contract: it
	// only sees Outcome/Reason, never bytes. If either of these
	// substrings ever appears in the events slice the relay has
	// leaked a payload.
	secretA := []byte("SECRET-AGENT-TO-CLIENT-deadbeef")
	secretB := []byte("SECRET-CLIENT-TO-AGENT-cafebabe")
	if _, err := agent.Write(secretA); err != nil {
		t.Fatalf("agent write: %v", err)
	}
	if _, err := client.Write(secretB); err != nil {
		t.Fatalf("client write: %v", err)
	}
	// Drain both sides so the relay actually forwards the
	// frames through its router, then close cleanly.
	bufA := make([]byte, len(secretA))
	if _, err := readFull(client, bufA, 2*time.Second); err != nil {
		t.Fatalf("client read: %v", err)
	}
	bufB := make([]byte, len(secretB))
	if _, err := readFull(agent, bufB, 2*time.Second); err != nil {
		t.Fatalf("agent read: %v", err)
	}
	if !bytes.Equal(bufA, secretA) {
		t.Fatalf("client got %q, want %q", bufA, secretA)
	}
	if !bytes.Equal(bufB, secretB) {
		t.Fatalf("agent got %q, want %q", bufB, secretB)
	}

	// Now the privacy assertion: the relay's events must not
	// contain either secret in any form.
	for _, evt := range f.logger.snapshot() {
		if strings.Contains(evt, string(secretA)) || strings.Contains(evt, string(secretB)) {
			t.Fatalf("relay logger leaked payload bytes: %s", evt)
		}
	}
}

// readFrame blocks until a frame is available on the stream or the
// deadline elapses, and returns the number of payload bytes copied
// into buf. It is a thin wrapper around the Stream's blocking
// Read so tests get a clear error on timeout instead of a hang.
func readFrame(s *relayclient.Stream, buf []byte, timeout time.Duration) (int, error) {
	type result struct {
		n   int
		err error
	}
	ch := make(chan result, 1)
	go func() {
		n, err := s.Read(buf)
		ch <- result{n, err}
	}()
	select {
	case r := <-ch:
		return r.n, r.err
	case <-time.After(timeout):
		_ = s.Close()
		return 0, fmt.Errorf("read timed out after %s", timeout)
	}
}

// readFull reads exactly len(buf) bytes from the stream, blocking
// per frame.
func readFull(s *relayclient.Stream, buf []byte, timeout time.Duration) (int, error) {
	total := 0
	for total < len(buf) {
		// Per-frame timeout so a partial read does not
		// consume the whole test budget.
		n, err := readFrame(s, buf[total:], timeout)
		total += n
		if err != nil {
			if err == io.EOF && total == len(buf) {
				return total, nil
			}
			return total, err
		}
	}
	return total, nil
}

// waitForJoin sleeps briefly so the in-process relay has time to
// process the two tickets and join the peers onto the same route.
// Without it, the first payload races the server's join and is
// dropped (the relay drops forward messages whose destination has
// not yet been joined, by design). 150ms is generous for an
// in-memory switchboard and keeps test latency low.
func waitForJoin() { time.Sleep(150 * time.Millisecond) }
