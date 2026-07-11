# Relay Service

## Role

The relay connects devices that cannot reach each other directly. It routes opaque E2EE envelopes and enforces resource policy. It is not a terminal server, transcript store, file host, or source-code processor.

## Technology Direction

- Go service for consistency with the Agent and strong concurrency support.
- WSS/WebSocket listener for V1.
- Stateless encrypted payload handling.
- PostgreSQL for durable control metadata only when account/discovery features require it.
- Redis or compatible ephemeral store only when multiple relay instances need shared presence and routing.
- OpenTelemetry-compatible metrics and traces with strict content exclusion.

## Connection Model

1. Agent establishes an outbound authenticated relay connection and registers an opaque route.
2. Client obtains a short-lived signed relay ticket.
3. Client connects and presents the ticket.
4. Relay joins the two authorized route endpoints.
5. Devices complete E2EE handshake through the relay.
6. Relay forwards bounded outer envelopes without parsing inner plaintext.

## Ticket Requirements

- Signed by a configured issuer.
- Contains opaque route, source/destination class, expiry, nonce, maximum connection count, and resource limits.
- Valid for minutes, not days.
- Single-use or tightly replay bounded.
- Hash is bound into the E2EE handshake.
- Self-hosted deployments can issue tickets locally.

## Backpressure

- Per-connection bounded send queue by bytes.
- Read pauses when downstream queue crosses a soft limit.
- Connection closes with an overload code at the hard limit.
- Attachment bandwidth cannot starve terminal control messages; logical priority queues remain bounded.
- Slow consumer metrics use opaque route identifiers.

## Abuse Controls

- Maximum frame size and frame rate.
- Concurrent route and connection limits.
- Per-device and per-source bandwidth windows.
- Ticket issuance limits and anomaly detection.
- Automatic expiry for orphaned routes.
- Operator blocklist for opaque device/account identifiers.
- No plaintext content inspection.

Limits protect shared infrastructure but are not product payment tiers.

## Availability

Initial target:

- Single region.
- Multiple instances only after load requires it.
- Rolling deployment with connection draining.
- Established sessions receive a reconnect signal rather than silent loss when possible.
- Direct connections continue independently of relay health.

Later multi-region selection uses measured latency and Agent/client policy. Cross-region session migration is deferred until regional reliability data justifies it.

## Self-Hosting

The relay ships with:

- Container image and pinned version tags.
- Minimal single-node configuration.
- TLS termination guidance.
- Ticket issuer configuration.
- Health, readiness, and metrics endpoints.
- Resource-limit defaults.
- Upgrade and rollback notes.

Self-hosted clients use explicit endpoint trust and cannot be silently redirected by the community service.

## Relay Persistence

Never persist:

- Terminal payload ciphertext beyond in-flight buffers.
- Attachment ciphertext beyond in-flight buffers unless a future resumable-store design is separately approved.
- Session keys.
- Full IP histories.

May persist:

- Signed device/public descriptors where Control API is enabled.
- Revocation events.
- Ticket issuance and abuse counters.
- Aggregated operational metrics.

## Observability

Allowed dimensions:

- Region, version, protocol major, transport state.
- Connection counts, handshake outcome category, bytes, queue depth, duration.
- Stable opaque error code.

Forbidden dimensions:

- Prompt or terminal text.
- Workspace, command, attachment name, or repository path.
- Full tokens or public keys.

## Testing

- Load tests with slow readers, reconnect storms, and attachment traffic.
- Chaos tests for instance termination, packet delay, and presence-store outage.
- Privacy test demonstrating only outer metadata is observable.
- Ticket replay, expiry, substitution, and quota tests.
- Compatibility tests against multiple Agent/client versions.
