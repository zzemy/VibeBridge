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
			name:  "empty route id",
			input: func() IssueInput { i := validIssueInput(nil); i.RouteID = nil; return i }(),
		},
		{
			name:  "wrong length device id",
			input: func() IssueInput { i := validIssueInput(nil); i.DeviceID = bytesRepeat(1, 4); return i }(),
		},
		{
			name:  "unspecified endpoint",
			input: func() IssueInput { i := validIssueInput(nil); i.Endpoint = vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_UNSPECIFIED; return i }(),
		},
		{
			name:  "zero max connections",
			input: func() IssueInput { i := validIssueInput(nil); i.MaxConnections = 0; return i }(),
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := issuer.Issue(tc.input); err == nil {
				t.Fatalf("expected an error, got nil")
			}
		})
	}
}

func TestVerifyRejectsBadShape(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ticket.TicketId = ticket.TicketId[:8]
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketMalformed) {
		t.Fatalf("expected ErrTicketMalformed, got %v", err)
	}
}

func TestVerifyRejectsBadSignature(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	ticket.IssuerSignature[0] ^= 0xff
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
	clock.Advance(10 * time.Minute)
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketExpired) {
		t.Fatalf("expected ErrTicketExpired, got %v", err)
	}
}

func TestVerifyRejectsReplay(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
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

func TestVerifyAcceptsSecondIssuer(t *testing.T) {
	_, publicA, clock := newTicketTestPair(t)
	issuerB, publicB, _ := newTicketTestPair(t)
	verifier := newTicketTestVerifier(publicA, clock)
	verifier.issuers[string(publicB)] = publicB
	ticket, err := issuerB.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("verify: %v", err)
	}
}

func TestVerifyRejectsUnknownIssuer(t *testing.T) {
	issuerA, _, clock := newTicketTestPair(t)
	// Configure the verifier with a public key that did not sign
	// the ticket so the signature check has no match.
	_, publicOther, _ := newTicketTestPair(t)
	verifier := newTicketTestVerifier(publicOther, clock)
	ticket, err := issuerA.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketSignature) {
		t.Fatalf("expected ErrTicketSignature, got %v", err)
	}
}

func TestVerifyRejectsVersionMismatch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
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

func TestIssueStampsIssuerEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	input := validIssueInput(clock)
	input.IssuerEpoch = 7
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if ticket.IssuerEpoch != 7 {
		t.Fatalf("expected ticket.IssuerEpoch=7, got %d", ticket.IssuerEpoch)
	}
	verified, err := verifier.Verify(ticket)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if verified.IssuerEpoch() != 7 {
		t.Fatalf("expected verified.IssuerEpoch()=7, got %d", verified.IssuerEpoch())
	}
}

func TestIssueIssuerEpochZeroLeavesTicketUnstamped(t *testing.T) {
	issuer, _, _ := newTicketTestPair(t)
	ticket, err := issuer.Issue(validIssueInput(nil))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	// An unset IssuerEpoch is a valid opt-out: a relay without a
	// gate can still verify the ticket. The Authorizer layer is
	// what rejects the zero case when a gate is on.
	if ticket.IssuerEpoch != 0 {
		t.Fatalf("expected ticket.IssuerEpoch=0 by default, got %d", ticket.IssuerEpoch)
	}
}

func TestVerifyWithoutAuthorizerAcceptsZeroEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("verify with no authorizer should accept zero epoch, got %v", err)
	}
}

func TestVerifyWithAuthorizerAcceptsFreshEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	verifier.SetAuthorizer(staticAuthorizer(0, nil))
	input := validIssueInput(clock)
	input.IssuerEpoch = 5
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("expected accept, got %v", err)
	}
}

func TestVerifyWithAuthorizerRejectsOlderEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	verifier.SetAuthorizer(staticAuthorizer(10, errors.New("revoked")))
	input := validIssueInput(clock)
	input.IssuerEpoch = 3
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketRevoked) {
		t.Fatalf("expected ErrTicketRevoked, got %v", err)
	}
}

func TestVerifyWithAuthorizerRejectsZeroEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	verifier.SetAuthorizer(staticAuthorizer(0, errors.New("no epoch")))
	ticket, err := issuer.Issue(validIssueInput(clock))
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketRevoked) {
		t.Fatalf("expected ErrTicketRevoked for zero-epoch ticket, got %v", err)
	}
}

func TestVerifyAuthorizerRollsBackReplayOnReject(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	calls := 0
	verifier.SetAuthorizer(func(_ []byte, _ uint64) error {
		calls++
		return errors.New("deny")
	})
	input := validIssueInput(clock)
	input.IssuerEpoch = 1
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); !errors.Is(err, ErrTicketRevoked) {
		t.Fatalf("expected ErrTicketRevoked, got %v", err)
	}
	// A revocation rejection must not poison the replay set:
	// flipping the authorizer to allow should let the same ticket
	// through. (In practice the ticket id is unique so this only
	// matters for the test's intent.)
	verifier.SetAuthorizer(nil)
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("expected the same ticket to verify after authorizer cleared, got %v", err)
	}
	if calls != 1 {
		t.Fatalf("expected authorizer to be called once, got %d", calls)
	}
}

func TestVerifyAuthorizerPassesDeviceIDAndEpoch(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	var seenDevice []byte
	var seenEpoch uint64
	verifier.SetAuthorizer(func(deviceID []byte, issuerEpoch uint64) error {
		seenDevice = append([]byte(nil), deviceID...)
		seenEpoch = issuerEpoch
		return nil
	})
	input := validIssueInput(clock)
	input.IssuerEpoch = 11
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("verify: %v", err)
	}
	if string(seenDevice) != string(input.DeviceID) {
		t.Fatalf("authorizer saw device %x, want %x", seenDevice, input.DeviceID)
	}
	if seenEpoch != 11 {
		t.Fatalf("authorizer saw epoch %d, want 11", seenEpoch)
	}
}

func TestSetAuthorizerNilDisablesGate(t *testing.T) {
	issuer, public, clock := newTicketTestPair(t)
	verifier := newTicketTestVerifier(public, clock)
	verifier.SetAuthorizer(staticAuthorizer(10, errors.New("revoked")))
	verifier.SetAuthorizer(nil)
	if got := verifier.Authorizer(); got != nil {
		t.Fatalf("expected nil authorizer, got %T", got)
	}
	input := validIssueInput(clock)
	input.IssuerEpoch = 3
	ticket, err := issuer.Issue(input)
	if err != nil {
		t.Fatalf("issue: %v", err)
	}
	if _, err := verifier.Verify(ticket); err != nil {
		t.Fatalf("verify with nil authorizer should accept, got %v", err)
	}
}

// staticAuthorizer returns an Authorizer that always responds with
// the supplied result. It is a tiny helper for tests that do not
// care about the device id or epoch, only the verdict.
func staticAuthorizer(_ uint64, result error) Authorizer {
	return func(_ []byte, _ uint64) error {
		return result
	}
}
