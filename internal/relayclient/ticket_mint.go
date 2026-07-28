package relayclient

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"time"

	"github.com/zzemy/VibeBridge/internal/relay"
	vibebridgev1 "github.com/zzemy/VibeBridge/gen/go/vibebridge/v1"
)

// PairInput collects the parameters needed to mint matching Agent and
// Client tickets for one route. Both tickets share the same RouteId
// and lifetime so the relay pairs them on the same line.
type PairInput struct {
	// RouteID is the 16-byte route identifier both tickets bind to.
	// The relay matches Agent and Client halves by this id.
	RouteID []byte
	// AgentID is the Agent device id stamped on the Agent ticket.
	// Must be 16 bytes (deviceidentity.DeviceIDBytes).
	AgentID []byte
	// ClientID is the Client device id stamped on the Client ticket.
	// Must be 16 bytes (deviceidentity.DeviceIDBytes).
	ClientID []byte
	// Lifetime bounds the ticket validity. The verifier rejects
	// tickets with a lifetime longer than relay's internal
	// replay window (10 minutes); Pair returns an error in that
	// case rather than silently producing unusable tickets.
	Lifetime time.Duration
	// MaxConnections is the per-ticket cap the relay will enforce.
	// Zero defaults to 1: the route is strictly one Agent, one
	// Client. Self-hosted relays may want a higher cap for
	// multi-device clients; the typical community relay should
	// keep this at 1.
	MaxConnections uint32
	// IssuedAt overrides the issue timestamp for testing. Zero
	// means "now" in the Issuer's clock.
	IssuedAt time.Time
}

// Pair mints matched Agent and Client tickets for the same route.
// The two tickets share RouteId, ExpiresAt, and MaxConnections but
// carry distinct ticket ids, distinct nonces, and the corresponding
// endpoint / device id. The Issuer signature is regenerated per
// ticket so the verifier accepts either half independently.
//
// Pair is the standard way to provision a relay session from inside
// the Agent: mint the pair, hand the Client ticket to the remote
// (typically via a QR code), then Dial the relay with the Agent
// ticket and Bridge the result to the local transport.
func Pair(issuer *relay.Issuer, in PairInput) (agentTicket, clientTicket *vibebridgev1.RelayTicket, err error) {
	if issuer == nil {
		return nil, nil, errors.New("relay issuer must not be nil")
	}
	if len(in.RouteID) != 16 {
		return nil, nil, fmt.Errorf("relay route id must be 16 bytes, got %d", len(in.RouteID))
	}
	if len(in.AgentID) != 16 {
		return nil, nil, fmt.Errorf("relay agent device id must be 16 bytes, got %d", len(in.AgentID))
	}
	if len(in.ClientID) != 16 {
		return nil, nil, fmt.Errorf("relay client device id must be 16 bytes, got %d", len(in.ClientID))
	}
	if in.Lifetime <= 0 {
		return nil, nil, errors.New("relay ticket lifetime must be positive")
	}
	if in.Lifetime > 10*time.Minute {
		// The verifier tracks consumed ticket ids for 10 minutes
		// (relay.ticketReplayTTL). Anything longer is rejected
		// at the verifier, so refuse here.
		return nil, nil, fmt.Errorf("relay ticket lifetime %s exceeds verifier replay window", in.Lifetime)
	}
	maxConn := in.MaxConnections
	if maxConn == 0 {
		maxConn = 1
	}
	expiresAt := time.Time{}
	if !in.IssuedAt.IsZero() {
		expiresAt = in.IssuedAt.Add(in.Lifetime)
	}
	// The Issuer's Issue method already mints the ticket id and
	// nonce internally, so Pair only needs to vary the endpoint
	// and device id between the two tickets.
	mint := func(endpoint vibebridgev1.RelayEndpoint, deviceID []byte) (*vibebridgev1.RelayTicket, error) {
		return issuer.Issue(relay.IssueInput{
			RouteID:        in.RouteID,
			DeviceID:       deviceID,
			Endpoint:       endpoint,
			MaxConnections: maxConn,
			Lifetime:       in.Lifetime,
			ExpiresAt:      expiresAt,
		})
	}
	agentTicket, err = mint(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_AGENT, in.AgentID)
	if err != nil {
		return nil, nil, fmt.Errorf("mint agent ticket: %w", err)
	}
	clientTicket, err = mint(vibebridgev1.RelayEndpoint_RELAY_ENDPOINT_CLIENT, in.ClientID)
	if err != nil {
		return nil, nil, fmt.Errorf("mint client ticket: %w", err)
	}
	return agentTicket, clientTicket, nil
}

// PublicKeyBytes returns the issuer's public key as a 32-byte slice
// suitable for handing to a remote Verifier. The returned slice is a
// fresh copy; the caller owns it and may serialise it however it
// likes (typically hex or base64 inside the agentconfig).
func PublicKeyBytes(issuer *relay.Issuer) []byte {
	if issuer == nil {
		return nil
	}
	pub := issuer.PublicKey()
	out := make([]byte, len(pub))
	copy(out, pub)
	return out
}

// ParsePublicKey is the dual of PublicKeyBytes: it builds a
// ed25519.PublicKey from the same 32-byte representation so a
// Verifier can be configured with the public half of an agent's
// issuer key.
func ParsePublicKey(raw []byte) (ed25519.PublicKey, error) {
	if len(raw) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("relay public key must be %d bytes, got %d", ed25519.PublicKeySize, len(raw))
	}
	out := make(ed25519.PublicKey, ed25519.PublicKeySize)
	copy(out, raw)
	return out, nil
}
