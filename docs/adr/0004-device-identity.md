# ADR-0004: Use Pairwise Device Keys as the Authorization Root

- Status: Proposed pending cryptographic review
- Date: 2026-07-12

## Context

The current per-run URL token is sufficient for trusted-LAN MVP pairing but not long-term remote access, revocation, multi-device ownership, or E2EE authentication. The product may later add user accounts, but it must remain self-hostable and local-only capable.

## Decision

Use locally generated device identities as the root of terminal authorization:

- Ed25519 signing identity.
- X25519 key-agreement identity.
- Expiring single-use QR bootstrap pairing.
- Pairwise authorization records and revocation epochs stored by the Agent.
- Optional accounts for discovery and synchronization, never as a replacement for device-key authentication.

## Rationale

- Local-only mode remains independent of a cloud account.
- Relay or account compromise does not directly authorize a terminal session.
- Device revocation and multi-device identity are explicit.
- Self-hosted and community-hosted modes share the same trust model.

## Alternatives

### Account Token as Primary Authorization

Simpler centralized management, but creates a high-impact cloud trust dependency and weakens local-only operation.

### Long-Lived Shared Pairing Secret

Easy to implement but difficult to rotate, attribute, and revoke per device.

### Client TLS Certificates Only

Strong transport authentication but awkward browser/PWA lifecycle and insufficient by itself for product pairing, relay tickets, and message-level E2EE.

## Consequences

- Secure device-key storage is required on every platform.
- Lost-device and lost-all-devices recovery must be explicit.
- Pairing protocol and revocation deserve external review.
- Account design remains simpler because it handles discovery rather than terminal trust.

## Reconsider When

- Formal review finds the pairwise graph unmanageable for individual multi-device use.
- Platform secure-storage limitations make key lifecycle unreliable.
- A standards-based multi-device protocol provides better audited properties without harming self-hosting.
