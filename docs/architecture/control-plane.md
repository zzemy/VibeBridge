# Control Plane Design

## Role

The optional Control Plane supports community-hosted and multi-device product workflows without becoming the root authority for terminal access.

It manages:

- Optional user discovery identity.
- Public device descriptors and presence hints.
- Signed relay-ticket issuance.
- Revocation-event synchronization.
- Push-notification routing metadata.
- Protocol and release compatibility policy.
- Community-service abuse controls.

It does not receive terminal plaintext, attachment plaintext, device private keys, session traffic keys, workspace paths, commands, or prompt history.

## Trust Separation

```text
Account / passkey / OIDC
        |
Control Plane authorization
        |
short-lived relay ticket
        |
Relay route allocation

Paired device keys ----------------------------------+
        |                                             |
        +------ E2EE terminal authorization ----------+
```

An account or relay ticket lets a device request routing resources. The Agent still requires a valid paired-device handshake before starting or attaching to a terminal session.

## Services

### Identity API

- Passkey-first account registration where hosted accounts are enabled.
- OIDC integration for self-hosted operators.
- Session management, recovery, and security notifications.
- Accounts remain optional for local-only mode.

### Device Directory

- Stores signed public device descriptors.
- Stores encrypted display/discovery metadata where practical.
- Returns only devices authorized by the account/device graph.
- Tracks compatibility and last-seen metadata with bounded retention.

### Ticket Issuer

- Issues short-lived signed relay tickets.
- Applies route, connection, bandwidth, and expiry policy.
- Includes nonce and authorization version.
- Does not issue terminal authorization.

### Revocation Service

- Stores signed revocation events.
- Delivers the latest revocation epoch to online Agents and clients.
- Provides append-only ordering and idempotent consumption.
- Cannot unilaterally re-authorize a device revoked locally by an Agent.

### Notification Broker

- Maps opaque device identifiers to platform push tokens.
- Sends privacy-safe event categories.
- Removes invalid push tokens.
- Never receives terminal text or attachment metadata for notification rendering.

### Compatibility Service

- Publishes minimum and recommended versions.
- Blocks known-vulnerable protocol combinations when required.
- Provides signed update metadata location.
- Keeps compatibility policy separate from binary delivery.

## Data Model

Illustrative durable entities:

- `accounts`
- `account_identities`
- `devices`
- `device_descriptors`
- `device_authorizations`
- `revocation_events`
- `relay_ticket_events`
- `push_endpoints`
- `release_policies`

Terminal sessions are not durable Control Plane entities unless an opaque short-lived route record is operationally required. No transcript or attachment table exists.

## Authentication

- Hosted web/mobile account sessions use passkeys and short-lived sessions.
- Native clients bind account login to the device signing key.
- Agents authenticate Control Plane requests with device signatures and scoped tokens.
- Self-hosted deployments may use configured OIDC or accountless pairing-only operation.
- Administrative endpoints use a separate operator identity and authorization boundary.

## Privacy and Retention

- Device IP addresses are retained only as needed for security and operations.
- Last-seen timestamps use bounded retention.
- Display names are user-controlled; encrypt at rest where lookup is not required.
- Relay ticket events store outcome and opaque identifiers, not payload details.
- Account deletion removes hosted discovery metadata and push endpoints while local Agents retain locally controlled device state until the user removes it.

## Availability Behavior

- Existing E2EE sessions may continue during Control Plane outage according to ticket and revocation freshness policy.
- New community-relay routes may fail closed when tickets cannot be issued.
- Local/direct sessions remain available.
- Self-hosted deployments can combine Control Plane and relay for simplicity without combining trust semantics in code.

## Implementation Direction

- Go service with Protobuf-derived HTTPS APIs.
- PostgreSQL for durable metadata.
- Explicit migrations and rolling compatibility.
- Transactional ticket nonce and revocation updates.
- Background jobs for expiry, push cleanup, and retention.
- No message queue until delivery volume or reliability requires one; PostgreSQL-backed jobs are sufficient initially.

## Abuse and Recovery

- Rate-limit account, device, IP, and ticket issuance.
- Require verified device/account state before community resource allocation.
- Support operator suspension of opaque identifiers without decrypting content.
- Account recovery cannot mint a paired-device identity silently; terminal access still requires new Agent pairing or approval by a trusted device.
- High-risk recovery events notify existing devices.

## Self-Hosting

- Accountless mode is supported.
- Optional OIDC configuration is documented.
- Relay and Control Plane may run in one deployment but expose separate modules and data access.
- PostgreSQL is the only required durable service for the full self-hosted Control Plane target.
- Community-specific branding, analytics, or operator policy must not fork the protocol.

## Testing

- Authorization tests proving account access cannot bypass device pairing.
- Ticket expiry, replay, nonce, and quota tests.
- Revocation ordering and stale-state tests.
- Account recovery abuse tests.
- Retention and deletion tests.
- Control Plane outage tests preserving direct/local operation.
