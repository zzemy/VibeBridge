// Package relay implements the VibeBridge relay: a stateless WebSocket
// switchboard that authenticates peers with a short-lived signed
// RelayTicket and forwards opaque transport bytes between the AGENT and
// CLIENT halves of a single route.
//
// The relay never inspects the application payloads it forwards, never
// persists ticket plaintext, and never holds the per-session Noise
// transport. The Agent and Client terminate their own end-to-end session
// on top of the relay's byte stream, so the relay's only job is to put
// the two sockets on the same route and keep slow peers from filling
// memory.
package relay

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"fmt"
	"sync"
	"time"

	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
	"github.com/zzemy/VibeBridge/internal/deviceidentity"
	"google.golang.org/protobuf/proto"
)

const (
	// CurrentTicketVersion is the only ticket version this relay accepts.
	// Tickets with any other version are rejected before signature
	// verification so a future issuer cannot downgrade us.
	CurrentTicketVersion uint32 = 1
	// ticketIDBytes matches the natural length of a random ticket id.
	ticketIDBytes = 16
	// routeIDBytes matches the natural length of a route id.
	routeIDBytes = 16
	// ticketNonceBytes prevents two tickets with otherwise-identical
	// fields from sharing a signature.
	ticketNonceBytes = 16
	// ticketReplayTTL bounds how long a consumed ticket id is remembered.
	// Equal to the maximum ticket lifetime the relay is willing to honour
	// so an attacker cannot replay a ticket after the relay would have
	// otherwise dropped it for age.
	ticketReplayTTL = 10 * time.Minute
	// replaySweepInterval is how often the replay set trims expired
	// entries. The interval is well under ticketReplayTTL so the working
	// set stays bounded even at high ticket volume.
	replaySweepInterval = time.Minute
)

var (
	ErrTicketVersion     = errors.New("relay ticket version is not supported")
	ErrTicketSignature   = errors.New("relay ticket signature is invalid")
	ErrTicketExpired     = errors.New("relay ticket has expired")
	ErrTicketMalformed   = errors.New("relay ticket is malformed")
	ErrTicketUnspecified = errors.New("relay ticket endpoint is unspecified")
	ErrTicketReplayed    = errors.New("relay ticket has already been used")
	ErrTicketIssued      = errors.New("relay ticket is not yet issuable")
	// ErrTicketRevoked is returned by Verify when an Authorizer is
	// configured and the ticket's issuer_epoch is below the live
	// RevocationEpoch (ADR-0006 revocation gate). The ticket itself is
	// well-formed; the underlying device was revoked after the ticket
	// was signed.
	ErrTicketRevoked = errors.New("relay ticket backing device has been revoked")
)

// Authorizer is the optional callback the Verifier consults after
// signature and replay checks to confirm a ticket's backing device is
// still authorized (ADR-0006). The default value is nil, in which case
// no revocation check is performed and the Verifier accepts every
// well-formed ticket the issuer signs.
//
// The callback receives the device id and issuer epoch the ticket was
// minted under. It must return nil to allow the ticket, or a non-nil
// error to reject it; the Verifier rewrites any non-nil return to
// ErrTicketRevoked so callers do not have to map bespoke error
// strings. Implementations should be quick (sub-millisecond): the
// Verifier holds a replay-set entry open for the duration of the call
// and a slow Authorizer would let a flood of bad tickets fill memory.
type Authorizer func(deviceID []byte, issuerEpoch uint64) error

// Ticket is the in-memory wrapper around a verified RelayTicket. It is
// produced by Verifier.Verify and consumed by Router.Join.
type Ticket struct {
	wire    *vibebridgev1.RelayTicket
	issuer  ed25519.PublicKey
	expires time.Time
}

// ID returns the ticket id bytes. The result is the original protobuf
// field, not a hash.
func (t *Ticket) ID() []byte {
	return append([]byte(nil), t.wire.TicketId...)
}

// RouteID returns the route id bytes that this ticket is bound to.
func (t *Ticket) RouteID() []byte {
	return append([]byte(nil), t.wire.RouteId...)
}

// Endpoint returns whether this ticket is for the agent side or the
// client side of the route.
func (t *Ticket) Endpoint() vibebridgev1.RelayEndpoint {
	return t.wire.Endpoint
}

// DeviceID returns the device id this ticket is bound to.
func (t *Ticket) DeviceID() []byte {
	return append([]byte(nil), t.wire.DeviceId...)
}

// MaxConnections returns the connection cap the issuer stamped on the
// ticket. The relay refuses to join a peer that would push the route
// past this number.
func (t *Ticket) MaxConnections() uint32 {
	return t.wire.MaxConnections
}

// ExpiresAt returns the absolute expiration timestamp the issuer set.
func (t *Ticket) ExpiresAt() time.Time {
	return t.expires
}

// Issuer returns the public key the ticket verified under. The
// Verifier carries the set of acceptable issuer keys, so this is the
// exact key that signed the ticket.
func (t *Ticket) Issuer() ed25519.PublicKey {
	return t.issuer
}

// IssuerEpoch returns the Agent-side revocation epoch the issuer stamped
// on the ticket at mint time (ADR-0006). A zero value means the ticket
// was issued without an epoch stamp.
func (t *Ticket) IssuerEpoch() uint64 {
	return t.wire.IssuerEpoch
}

// Issuer signs RelayTickets. The relay itself is the Verifier; the
// Issuer lives in a separate process (typically the Agent or a control
// plane) that hands tickets to clients out of band.
type Issuer struct {
	private ed25519.PrivateKey
	public  ed25519.PublicKey
	now     func() time.Time
	random  func(b []byte) (int, error)
}

// NewIssuer returns an Issuer backed by the supplied Ed25519 key pair.
// The same key pair must be configured on the Verifier or every ticket
// will be rejected.
func NewIssuer(private ed25519.PrivateKey) (*Issuer, error) {
	if l := len(private); l != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("issuer private key must be %d bytes, got %d", ed25519.PrivateKeySize, l)
	}
	public := private.Public().(ed25519.PublicKey)
	return &Issuer{
		private: private,
		public:  public,
		now:     time.Now,
		random:  rand.Read,
	}, nil
}

// PublicKey returns the Ed25519 public key the Verifier must be
// configured with so tickets issued by this Issuer are accepted.
func (issuer *Issuer) PublicKey() ed25519.PublicKey {
	return issuer.public
}

// Issue builds and signs a new RelayTicket. The supplied fields are
// validated for basic shape (route/device id length, endpoint range)
// and an Ed25519 signature is produced over the deterministic
// protobuf encoding of every field except issuer_signature.
func (issuer *Issuer) Issue(input IssueInput) (*vibebridgev1.RelayTicket, error) {
	if len(input.RouteID) != routeIDBytes {
		return nil, fmt.Errorf("route id must be %d bytes, got %d", routeIDBytes, len(input.RouteID))
	}
	if len(input.DeviceID) != deviceidentity.DeviceIDBytes {
		return nil, fmt.Errorf("device id must be %d bytes, got %d", deviceidentity.DeviceIDBytes, len(input.DeviceID))
	}
	switch input.Endpoint {
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT:
	default:
		return nil, ErrTicketUnspecified
	}
	if input.MaxConnections == 0 {
		return nil, errors.New("max connections must be greater than zero")
	}
	now := issuer.now().UTC()
	lifetime := input.Lifetime
	if lifetime <= 0 {
		lifetime = 2 * time.Minute
	}
	expires := now.Add(lifetime)
	if !input.ExpiresAt.IsZero() {
		expires = input.ExpiresAt.UTC()
	}
	ticketID, err := issuer.randomBytes(ticketIDBytes)
	if err != nil {
		return nil, fmt.Errorf("generate ticket id: %w", err)
	}
	nonce, err := issuer.randomBytes(ticketNonceBytes)
	if err != nil {
		return nil, fmt.Errorf("generate ticket nonce: %w", err)
	}
	ticket := &vibebridgev1.RelayTicket{
		Version:        CurrentTicketVersion,
		TicketId:       ticketID,
		RouteId:        append([]byte(nil), input.RouteID...),
		Endpoint:       input.Endpoint,
		DeviceId:       append([]byte(nil), input.DeviceID...),
		ExpiresAt:      timestamppbNew(expires),
		MaxConnections: input.MaxConnections,
		Nonce:          nonce,
		IssuerEpoch:    input.IssuerEpoch,
	}
	signature, err := signTicket(issuer.private, ticket)
	if err != nil {
		return nil, err
	}
	ticket.IssuerSignature = signature
	return ticket, nil
}

// IssueInput captures the parameters an Issuer needs to mint a
// ticket. Either ExpiresAt or Lifetime should be set; if Lifetime is
// positive, it is added to the current time. If both are zero a
// default 2-minute lifetime is used.
//
// IssuerEpoch is the Agent-side device-identity revocation epoch the
// issuer observed at mint time (ADR-0006). A zero value means the
// ticket was issued without an epoch stamp; a relay with a revocation
// Authorizer configured will treat such tickets as unauthorized. The
// Agent passes the current store.RevocationEpoch() here when minting.
type IssueInput struct {
	RouteID        []byte
	DeviceID       []byte
	Endpoint       vibebridgev1.RelayEndpoint
	MaxConnections uint32
	Lifetime       time.Duration
	ExpiresAt      time.Time
	IssuerEpoch    uint64
}

func (issuer *Issuer) randomBytes(n int) ([]byte, error) {
	if issuer.random == nil {
		return nil, errors.New("issuer random source is not configured")
	}
	buf := make([]byte, n)
	if _, err := issuer.random(buf); err != nil {
		return nil, err
	}
	return buf, nil
}

// Verifier validates RelayTickets and tracks consumed ticket ids so a
// ticket cannot be presented twice. It accepts tickets signed by any
// public key in the configured allow list. A zero Verifier rejects
// every ticket.
//
// An optional Authorizer (SetAuthorizer) plugs a revocation gate on top
// of the signature check (ADR-0006). When set, every accepted ticket
// must also pass Authorizer; otherwise the wire is consulted only for
// shape, signature, and replay.
type Verifier struct {
	issuers     map[string]ed25519.PublicKey
	now         func() time.Time
	replay      map[string]time.Time
	replayMu    sync.Mutex
	replayClock func() time.Time
	authorizer  Authorizer
}

// SetAuthorizer wires an optional revocation gate on top of the
// signature check (ADR-0006). The callback runs after every signature
// and replay check; passing nil disables the gate and reverts to the
// legacy wire-only verification policy. The Verifier is not safe for
// concurrent use with SetAuthorizer while a Verify call is in flight;
// wire the authorizer once at construction and treat the resulting
// Verifier as immutable.
func (verifier *Verifier) SetAuthorizer(authorizer Authorizer) {
	verifier.authorizer = authorizer
}

// Authorizer reports the active revocation gate, or nil when no gate
// is configured. The accessor is exposed for tests and observability.
func (verifier *Verifier) Authorizer() Authorizer {
	return verifier.authorizer
}

// NewVerifier returns a Verifier that accepts tickets signed by any
// of the supplied issuer public keys. The order is irrelevant; a
// ticket is accepted if any key verifies its signature.
func NewVerifier(issuers ...ed25519.PublicKey) *Verifier {
	clock := time.Now
	verifier := &Verifier{
		issuers:     make(map[string]ed25519.PublicKey, len(issuers)),
		now:         clock,
		replay:      make(map[string]time.Time),
		replayClock: clock,
	}
	for _, key := range issuers {
		if l := len(key); l != ed25519.PublicKeySize {
			continue
		}
		verifier.issuers[string(key)] = key
	}
	return verifier
}

// Verify checks every aspect of a presented ticket: shape, version,
// signature, expiration, and replay. On success the returned Ticket
// also marks the ticket id as consumed; subsequent calls with the
// same ticket return ErrTicketReplayed.
func (verifier *Verifier) Verify(wire *vibebridgev1.RelayTicket) (*Ticket, error) {
	if wire == nil {
		return nil, ErrTicketMalformed
	}
	if wire.Version != CurrentTicketVersion {
		return nil, ErrTicketVersion
	}
	if len(wire.TicketId) != ticketIDBytes {
		return nil, ErrTicketMalformed
	}
	if len(wire.RouteId) != routeIDBytes {
		return nil, ErrTicketMalformed
	}
	if len(wire.DeviceId) != deviceidentity.DeviceIDBytes {
		return nil, ErrTicketMalformed
	}
	if len(wire.Nonce) != ticketNonceBytes {
		return nil, ErrTicketMalformed
	}
	switch wire.Endpoint {
	case vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT,
		vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT:
	default:
		return nil, ErrTicketUnspecified
	}
	if wire.MaxConnections == 0 {
		return nil, ErrTicketMalformed
	}
	if len(wire.IssuerSignature) != ed25519.SignatureSize {
		return nil, ErrTicketSignature
	}
	now := verifier.now().UTC()
	if wire.ExpiresAt == nil {
		return nil, ErrTicketMalformed
	}
	expires := wire.ExpiresAt.AsTime()
	if expires.IsZero() {
		return nil, ErrTicketMalformed
	}
	// Apply a small clock-skew allowance so a slow issuer clock does
	// not invalidate a freshly minted ticket. 30 seconds is enough for
	// any NTP-synced host but small enough to keep the replay window
	// from growing unbounded.
	skewAllowance := 30 * time.Second
	if now.Add(skewAllowance).Before(expires) == false && now.After(expires) {
		return nil, ErrTicketExpired
	}
	// Pre-mint tickets (expires_at in the future relative to the
	// relay clock) are allowed only inside the same skew window.
	if expires.After(now.Add(ticketReplayTTL)) {
		return nil, ErrTicketIssued
	}
	if _, ok := verifier.checkAndRecordReplay(wire.TicketId); !ok {
		return nil, ErrTicketReplayed
	}
	issuer, err := verifier.verifySignature(wire)
	if err != nil {
		// Roll back the replay entry we just inserted so a transient
		// signature failure does not permanently lock a future, valid
		// ticket that happened to share an id (negligible probability
		// but the rollback is cheap).
		verifier.forgetReplay(wire.TicketId)
		return nil, err
	}
	if verifier.authorizer != nil {
		if authErr := verifier.authorizer(wire.DeviceId, wire.IssuerEpoch); authErr != nil {
			// Same rollback as signature failure: a rejected ticket
			// must not burn the ticket id. A future, well-formed ticket
			// with a different nonce/route must still be accepted.
			verifier.forgetReplay(wire.TicketId)
			return nil, ErrTicketRevoked
		}
	}
	return &Ticket{
		wire:    proto.Clone(wire).(*vibebridgev1.RelayTicket),
		issuer:  issuer,
		expires: expires,
	}, nil
}

func (verifier *Verifier) checkAndRecordReplay(ticketID []byte) (time.Time, bool) {
	key := string(ticketID)
	now := verifier.replayClock()
	verifier.replayMu.Lock()
	defer verifier.replayMu.Unlock()
	verifier.sweepLocked(now)
	if _, ok := verifier.replay[key]; ok {
		// A previously verified ticket is still being tracked.
		// Subsequent identical ticket ids continue to be rejected
		// until the entry ages out.
		return time.Time{}, false
	}
	verifier.replay[key] = now
	return now, true
}

func (verifier *Verifier) forgetReplay(ticketID []byte) {
	key := string(ticketID)
	verifier.replayMu.Lock()
	defer verifier.replayMu.Unlock()
	delete(verifier.replay, key)
}

func (verifier *Verifier) sweepLocked(now time.Time) {
	if len(verifier.replay) == 0 {
		return
	}
	cutoff := now.Add(-ticketReplayTTL)
	for key, recorded := range verifier.replay {
		if recorded.Before(cutoff) {
			delete(verifier.replay, key)
		}
	}
}

// SweepNow trims expired replay entries immediately. The Verifier
// sweeps lazily on every Verify call; callers do not need to invoke
// this, but exposing it makes the replay set's lifetime observable
// in tests.
func (verifier *Verifier) SweepNow() {
	verifier.replayMu.Lock()
	defer verifier.replayMu.Unlock()
	verifier.sweepLocked(verifier.replayClock())
}

// ReplaySize returns the number of ticket ids currently tracked. This
// is exposed for diagnostics and tests.
func (verifier *Verifier) ReplaySize() int {
	verifier.replayMu.Lock()
	defer verifier.replayMu.Unlock()
	return len(verifier.replay)
}

func (verifier *Verifier) verifySignature(wire *vibebridgev1.RelayTicket) (ed25519.PublicKey, error) {
	body, err := encodeTicketBody(wire)
	if err != nil {
		return nil, ErrTicketSignature
	}
	signature := append([]byte(nil), wire.IssuerSignature...)
	for _, publicKey := range verifier.issuers {
		if ed25519.Verify(publicKey, body, signature) {
			return publicKey, nil
		}
	}
	return nil, ErrTicketSignature
}

// encodeTicketBody returns the deterministic protobuf encoding of a
// ticket with the signature field cleared. The result is what the
// issuer actually signed and what the verifier must reproduce.
func encodeTicketBody(ticket *vibebridgev1.RelayTicket) ([]byte, error) {
	clone := proto.Clone(ticket).(*vibebridgev1.RelayTicket)
	clone.IssuerSignature = nil
	return proto.MarshalOptions{Deterministic: true}.Marshal(clone)
}

// signTicket produces the Ed25519 signature the verifier expects over
// the body bytes. The signature is returned without being attached
// to the supplied ticket so callers can decide whether to mutate the
// original or hand back a clone.
func signTicket(private ed25519.PrivateKey, ticket *vibebridgev1.RelayTicket) ([]byte, error) {
	body, err := encodeTicketBody(ticket)
	if err != nil {
		return nil, fmt.Errorf("encode ticket body: %w", err)
	}
	return ed25519.Sign(private, body), nil
}

// ticketDigest is exposed for logging hooks that want a stable,
// non-sensitive handle on a ticket. It is the SHA-256 of the body
// bytes and never exposes the plaintext of any ticket field.
func ticketDigest(ticket *vibebridgev1.RelayTicket) [32]byte {
	body, err := encodeTicketBody(ticket)
	if err != nil {
		return sha256.Sum256([]byte("relay-ticket-encode-error"))
	}
	return sha256.Sum256(body)
}

// ticketFingerprint returns a 4-byte hex string for log lines. It is
// derived from ticketDigest so two tickets with the same field
// values produce the same fingerprint.
func ticketFingerprint(ticket *vibebridgev1.RelayTicket) string {
	digest := ticketDigest(ticket)
	return fmt.Sprintf("%x", digest[:4])
}
