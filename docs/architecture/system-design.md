# Target System Design

## Scope

This document describes the target architecture for the open-source personal-developer product. The current repository is an early Local Agent and web-client implementation. Migration is incremental; this document is not a claim that every component exists today.

## Logical Architecture

```text
Mobile App / PWA
  |  device identity, E2EE session
  |\
  | \ direct LAN connection when available
  |  \
  |   Local Agent -> PTY -> Codex / Claude Code / shell
  |
  +---- WSS ---- Community or self-hosted Relay ---- WSS ---- Local Agent
                    |
                    +-- optional Control API
                        device discovery, relay tickets,
                        revocation metadata, compatibility
```

The same encrypted application protocol runs over direct and relayed transports. Transport selection must not change session semantics.

## Components

### Local Agent

- Runs as a user-scoped background process.
- Owns device keys, pairing, workspaces, launch profiles, PTYs, attachment staging, and cleanup.
- Maintains outbound relay connectivity when remote access is enabled.
- Exposes a loopback/local-network endpoint for direct clients.
- Never sends plaintext terminal or attachment content to the relay.

### Client

- Uses React and xterm.js for the shared terminal experience.
- Runs as web/PWA and inside a Capacitor mobile shell.
- Owns the phone device key, biometric gate, session UI, attachment selection, and notification presentation.
- Negotiates direct or relayed transport and verifies Agent identity before showing terminal data.

### Relay

- Routes opaque encrypted envelopes between authenticated devices.
- Enforces ticket expiry, connection quotas, frame limits, backpressure, and idle cleanup.
- Remains stateless for terminal content.
- Uses external presence coordination only when horizontally scaled.

### Control API

The first product can use pairing-derived device discovery. A small Control API becomes useful for official community hosting, multi-device discovery, revocation delivery, release compatibility, and abuse control.

Control-plane identity and E2EE device identity remain separate. Compromise of account metadata must not reveal session plaintext.

## Deployment Modes

### Local Only

The client connects directly to the Agent on a trusted network. No relay or account is required.

### Community Relay

The Agent and phone make outbound WSS connections to the official open-source relay deployment. Device keys provide E2EE identity; short-lived relay tickets provide routing authorization.

### Self-Hosted

The developer deploys the same relay and optional Control API. Clients select a custom endpoint and trust configuration explicitly.

### Private Overlay

Tailscale, WireGuard, or another private overlay may provide direct reachability. VibeBridge treats it as a direct transport and does not depend on a specific vendor.

## Data Ownership

| Data | Owner | Durable location | Relay visibility |
| --- | --- | --- | --- |
| Source code | Developer | Local workspace | None |
| CLI credentials | Developer/tool | Local machine | None |
| Device private key | Device | OS secure storage | None |
| Device public key | User/device graph | Peers and optional Control API | Public key only |
| Terminal stream | Session peers | Memory and bounded local buffer | Ciphertext and size metadata |
| Attachment | Developer | Session staging directory | Ciphertext and transfer metadata |
| Revocation state | Device graph | Peers and optional Control API | Device identifiers and version |
| Operational telemetry | Operator | Logs/metrics | Privacy-filtered metadata only |

## Target Repository Structure

```text
cmd/
  vibebridge-agent/
  vibebridge-relay/
  vibebridge-control/
internal/
  agent/
  relay/
  control/
  crypto/
  protocol/
  platform/
proto/
  vibebridge/v1/
web/
  src/
mobile/
  capacitor.config.ts
packages/
  protocol-ts/
deploy/
  docker/
  helm/
docs/
```

The repository remains a modular monorepo until independent release cadence or access boundaries justify splitting it. Generated protocol packages are not edited manually.

## Session Flow

1. Client discovers an Agent directly or through relay presence.
2. Client obtains or presents a short-lived transport ticket.
3. Client and Agent perform an authenticated E2EE handshake.
4. Peers negotiate protocol version and capabilities.
5. Client requests a session start or attach.
6. Agent creates or returns exactly one logical session and PTY.
7. Sequenced encrypted envelopes carry terminal and control data.
8. Disconnect retains the PTY for the reconnect policy.
9. Reattach proves device identity and resumes from acknowledged sequence numbers.
10. Exit or expiry performs idempotent process, PTY, key, buffer, and attachment cleanup.

## Scaling Strategy

Start with one relay region and one process type. Terminal traffic is low bandwidth and latency sensitive; operational simplicity matters more than premature multi-region routing.

Scale in this order:

1. Multiple relay instances behind one regional load balancer.
2. Redis or equivalent ephemeral presence routing only when instances need coordination.
3. PostgreSQL for durable control metadata and revocation delivery.
4. Regional relays selected by latency and device policy.
5. Cross-region handoff only after a measured need.

No terminal payload is persisted to a database.

## Failure Design

- Relay unavailable: direct connection remains possible; UI reports remote transport unavailable.
- Client disappears: Agent retains PTY for bounded time.
- Agent disappears: client marks session unavailable and does not invent recovery.
- Control API unavailable: established E2EE sessions continue until ticket expiry where safe.
- Version mismatch: capability negotiation rejects unsupported combinations before PTY mutation.
- Slow consumer: bounded queue, backpressure, and explicit disconnect prevent memory growth.
- Disk full during attachment: transfer fails before prompt insertion and removes partial data.
- Upgrade failure: Agent rolls back before accepting new sessions.

## Migration from Current Code

1. Extract current server session logic into an `internal/agent/session` boundary.
2. Introduce protocol envelopes while preserving the current local web endpoint.
3. Generate Go and TypeScript protocol types from Protobuf.
4. Replace per-run URL token identity with device pairing for the new protocol, while retaining legacy local mode during migration.
5. Add attachment staging and lifecycle before remote transfer.
6. Add relay transport behind the same session interface.
7. Package PWA/Capacitor only after protocol and key storage are stable.

Each migration step must leave local-only operation runnable and tested.
