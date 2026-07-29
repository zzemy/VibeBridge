// Epoch gate helpers for RelayTicket verification (ADR-0006).
//
// The relay is transport-only: it never inspects application payloads
// and never persists ticket plaintext. The single extra bit of state
// it consults for the revocation gate is the live RevocationEpoch of
// the Agent's device-identity store. Any Authorizer built on top of
// Store.RevocationEpoch is correct: a ticket was minted at the moment
// the store read N; if the store now reads M > N, the device was
// revoked between mint and present and the ticket must be rejected,
// even though the issuer signature still verifies.
//
// Per-device precision (a ticket for an unrevoked device is not
// rejected when a sibling was revoked) is intentionally not exposed
// here yet: the global epoch is the conservative correct answer, and
// the per-device policy can be added on top of the same Authorizer
// type without changing the wire.

package relay

import (
	"errors"
	"fmt"
)

// EpochSource is anything that can report the current revocation
// epoch. *deviceidentity.Store satisfies this at compile time.
type EpochSource interface {
	RevocationEpoch() uint64
}

// StoreAuthorizer returns an Authorizer that accepts a ticket only when
// the supplied epoch is greater than or equal to the live
// RevocationEpoch of the Agent device-identity store. Tickets stamped
// with a smaller epoch were minted before a revocation happened and
// are rejected with ErrTicketRevoked; tickets that do not carry an
// epoch (IssuerEpoch == 0) are also rejected because the relay has no
// evidence the issuer consulted the store.
//
// A nil source returns an Authorizer that rejects every ticket.
func StoreAuthorizer(source EpochSource) Authorizer {
	return func(deviceID []byte, issuerEpoch uint64) error {
		if source == nil {
			return errors.New("relay authorizer: nil device-identity store")
		}
		if issuerEpoch == 0 {
			return errors.New("relay authorizer: no issuer epoch stamped on ticket")
		}
		if issuerEpoch < source.RevocationEpoch() {
			return fmt.Errorf("relay authorizer: ticket backing device %x has been revoked", deviceID)
		}
		return nil
	}
}
