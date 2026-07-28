package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"sync"
	"testing"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

type ticketTestClock struct {
	mu sync.Mutex
	t  time.Time
}

func (clock *ticketTestClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.t
}

func (clock *ticketTestClock) Advance(d time.Duration) {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	clock.t = clock.t.Add(d)
}

func newTicketTestPair(t *testing.T) (*Issuer, ed25519.PublicKey, *ticketTestClock) {
	t.Helper()
	public, private, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generate ed25519 keypair: %v", err)
	}
	issuer, err := NewIssuer(private)
	if err != nil {
		t.Fatalf("new issuer: %v", err)
	}
	clock := &ticketTestClock{t: time.Date(2026, 7, 28, 12, 0, 0, 0, time.UTC)}
	issuer.now = clock.Now
	issuer.random = rand.Read
	return issuer, public, clock
}

// newTicketTestVerifier builds a Verifier that shares the issuer's
// test clock. This is the right shape for tests that need the
// issuer and verifier to agree on time.
func newTicketTestVerifier(public ed25519.PublicKey, clock *ticketTestClock) *Verifier {
	verifier := NewVerifier(public)
	verifier.now = clock.Now
	verifier.replayClock = clock.Now
	return verifier
}

func validIssueInput(clock *ticketTestClock) IssueInput {
	return IssueInput{
		RouteID:        bytesRepeat(0xAB, routeIDBytes),
		DeviceID:       bytesRepeat(0xCD, 16),
		Endpoint:       vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		MaxConnections: 2,
		Lifetime:       2 * time.Minute,
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func TestIssueAndVerifyRoundTrip(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ticket.Version != CurrentTicketVersion {
		t.Fatalf("unexpected ticket version: %d", ticket.Version)
	}
	verified, err := verifier.Verify(ticket)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.Endpoint() != vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT {
		t.Fatalf("endpoint mismatch")
	}
	if verified.MaxConnections() != 2 {
		t.Fatalf("max connections mismatch: %d", verified.MaxConnections())
	}
}

func TestIssueRejectsBadInput(t *testing.T) {
	issuer, _, _ := newTicketTestPair(t)
	cases := []struct {
		name  string
		input IssueInput
	}{
		{
			name: "short route id",
			input: IssueInput{
				RouteID: bytesRepeat(0x01, 8),
				DeviceID: bytesRepeat(0xCD, 16),
				Endpoint: vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
				MaxConnections: 2,
				Lifetime: time.Minute,
			},
		},
		{
			name: "short device id",
			input: IssueInput{
				RouteID: bytesRepeat(0xAB, routeIDBytes),
				DeviceID: bytesRepeat(0x01, 8),
				Endpoint: vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
				MaxConnections: 2,
				Lifetime: time.Minute,
			},
		},
		{
			name: "unspecified endpoint",
			input: IssueInput{
				RouteID: bytesRepeat(0xAB, routeIDBytes),
				DeviceID: bytesRepeat(0xCD, 16),
				Endpoint: vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_UNSPECIFIED,
				MaxConnections: 2,
				Lifetime: time.Minute,
			},
		},
		{
			name: "zero max connections",
			input: IssueInput{
				RouteID: bytesRepeat(0xAB, routeIDBytes),
				DeviceID: bytesRepeat(0xCD, 16),
				Endpoint: vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT,
				MaxConnections: 0,
				Lifetime: time.Minute,
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := issuer.Issue(tc.input); err == nil {
				t.Fatalf("expected error for case %q", tc.name)
			}
		})
	}
}

func TestVerifyRejectsBadSignature(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// Tamper with the device id; signature must no longer verify.
	ticket.DeviceId = bytesRepeat(0xFF, len(ticket.DeviceId))
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("expected ErrTicketSignature, got %v", err)
	}
}

func TestVerifyRejectsUnknownIssuer(t *testing.T) {
	issuer, _, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(nil, clock)
	verifier.issuers = map[string]ed25519.PublicKey{}
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("expected ErrTicketSignature, got %v", err)
	}
}

func TestVerifyRejectsExpired(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	clock.Advance(3 * time.Minute)
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expected ErrTicketExpired, got %v", err)
	}
}

func TestVerifyRejectsReplay(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketReplayed) {
		t.Fatalf("expected ErrTicketReplayed, got %v", err)
	}
}

func TestVerifyRejectsWrongVersion(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ticket.Version = 99
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketVersion) {
		t.Fatalf("expected ErrTicketVersion, got %v", err)
	}
}

func TestVerifySweepsExpiredReplay(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if size := verifier.ReplaySize(); size != 1 {
		t.Fatalf("expected replay size 1, got %d", size)
	}
	clock.Advance(ticketReplayTTL + time.Minute)
	verifier.SweepNow()
	if size := verifier.ReplaySize(); size != 0 {
		t.Fatalf("expected replay size 0 after sweep, got %d", size)
	}
}
