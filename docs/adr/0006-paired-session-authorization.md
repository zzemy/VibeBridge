# ADR-0006: Bind Terminal Session Authorization to Paired Device Identity

- Status: Accepted (Implementation Complete)
- Date: 2026-07-29
- Last Updated: 2026-07-30

## Context

The current Agent accepts terminal WebSocket upgrades on a single gate: `Server.handleWebSocket` calls `s.validToken(r)` (internal/server/server.go), checking the per-run URL bearer token. The `vibebridge.v1` subprotocol is offered by the upgrader (internal/server/server.go, Subprotocols) and `negotiateProtocolV1` runs when the client selects it, but nothing on the wire binds the WebSocket session to a paired device identity. ADR-0004 already named pairwise device keys as the authorization root and left the binding between an open session and a device as open work.

Three concrete gaps follow from this:

1. A bearer token that escapes the URL, browser history, screenshots, or a logged shell grants full terminal access. Revocation requires rotating the per-run token and does not target the device.
2. There is no server-side proof that a WS upgrade came from a device the Agent has actually authorized. The relay forwards bytes, so the same gap exists for remote sessions.
3. Phase 4 exit criteria require pairing replay and race tests, revoked-device rejection, and lost-phone recovery; none can be tested against a binding that does not exist.

## Decision

Treat the paired device identity as the authorization root for terminal sessions and require an Ed25519-signed upgrade challenge on the `vibebridge.v1` subprotocol path. Legacy URL bearer tokens have been removed from the WebSocket upgrade path entirely.

Concretely:

- Every terminal WebSocket upgrade MUST include a `VibeBridge-Device` header carrying the device ID (16 bytes / 32 hex characters; the wire format is hex-encoded) and a `VibeBridge-Device-Signature` header carrying an Ed25519 signature over a server-issued per-upgrade nonce. The nonce is delivered by the Agent immediately before the upgrade through a short-lived JSON endpoint, and is single-use, bound to the host that fetched it (port-agnostic so the same browser can open a fresh TCP connection for the upgrade), and expires in 30 seconds.
- The server verifies the signature against the public signing key inside the `SignedDeviceDescriptor` recorded by `deviceidentity.Store.Authorize` and rejects the upgrade if the device is not in `DEVICE_AUTHORIZATION_STATE_AUTHORIZED` or if `RevocationEpoch()` has moved past the device's record.
- On the `vibebridge.v1` subprotocol the binding is enforced unconditionally. Legacy URL bearer tokens are no longer accepted on the WebSocket upgrade path. The `/status` and `/pairing/web-session` HTTP endpoints still accept the bootstrap token as a pairing secret, but it cannot open a terminal session.
- Browser clients that cannot set custom WebSocket headers use a query-parameter transport (`vb-device`, `vb-nonce`, `vb-sig`) or the `/pairing/web-session` endpoint, which signs the nonce on behalf of the local web client using the Agent's own device key.
- `negotiateProtocolV1` publishes the bound device ID, public key fingerprint, and the revocation epoch observed at upgrade time in its hello envelope. The relay forwards the headers unchanged, so remote sessions are gated by the same challenge.
- The relay enforces a revocation gate: tickets carry the issuer's `RevocationEpoch` at mint time, and the relay rejects tickets whose epoch is stale relative to its local `deviceidentity.Store`.
- The browser stores the device key in IndexedDB, gated by a same-origin check and (when the platform allows) backed by `crypto.subtle` non-extractable keys. The mobile shell uses the platform secure store via a narrow Capacitor plugin. The CLI client reads the device identity from the existing `deviceidentity.Store` path.

## Implementation History

The migration was completed in the following sequence (all commits on `main`):

1. **Verifier + nonce endpoint introduced** (default OFF): `pairedsession.go` with 30s TTL nonce store, `pairedSessionGate.verify`, 11 tests covering accepted/replayed/expired/cross-origin/unknown/wrong-sig/missing-headers/legacy-token paths.
2. **CLI flag `--require-paired-session`** wired through `main.go` and `agentconfig`. Fixed a bug where `DeviceStore` was not passed to `server.Config`.
3. **Hello envelope extended**: `device_id`, `public_key_fingerprint`, `revocation_epoch` fields added to the Agent hello. `NewAgentHello` gains an `AgentIdentity` parameter.
4. **Relay revocation gate**: `RelayTicket` gains `issuer_epoch` field. Relay compares ticket epoch against local `deviceidentity.Store.RevocationEpoch()`. Zero-epoch tickets rejected.
5. **Web client migration**: query-param transport (`vb-device`/`vb-nonce`/`vb-sig`) and `/pairing/web-session` endpoint for browser WebSocket compatibility. `TerminalApp.tsx` updated to fetch web-session credentials before connecting.
6. **Default ON**: `RequirePairedSession` hardcoded `true` in `main.go`.
7. **Legacy token removed from WS upgrade**: `authorizedForUpgrade` no longer checks `validToken` when `RequirePairedSession` is true.
8. **Escape hatch removed**: `--require-paired-session` and `--disable-legacy-protocol` CLI flags and config fields deleted. `Config` struct fields retained for test injection only.
9. **Relay revocation gate default ON**: `--require-revocation-check` defaults to true; `--identity-store` falls back to `deviceidentity.DefaultPath()` for same-machine deployments.

## Rationale

- The `deviceidentity.Store` already owns the authorization graph and revocation epoch with atomic updates and overflow checks. Binding the upgrade to it adds no new trust root and reuses tests like `TestAuthorizeRevokeAndReloadDeviceGraph`.
- A per-upgrade signed nonce blocks replay, binds the signature to this specific connection attempt, and lets the server reject without holding per-connection state.
- Headers travel through the relay unchanged, so remote sessions get the same gate without changing the relay protocol or needing a second E2EE handshake.
- The decision does not depend on a future account system. Accounts remain a discovery layer, consistent with ADR-0004.

## Alternatives

### Reuse the Pairing Bootstrap Secret for Session Re-authentication

Avoids generating a new nonce, but the bootstrap secret has a 5 minute TTL and is destroyed after pairing completes. Reusing it would require keeping it around longer and contradicts the single-use property of pairing.

### Mutually Authenticated TLS with Client Certificates

Strong transport authentication, but client certificates are awkward for browsers and PWAs, do not survive self-hosting IP changes, and do not provide message-level authentication for E2EE frames.

### Long-Lived Browser-Stored Bearer Token Rotated by the Agent

Simpler to implement, but rotates less often than devices change, leaves a high-value secret in IndexedDB, and conflates device identity with session identity. Revocation still has to enumerate all issued tokens.

## Consequences

- The `vibebridge.v1` hello envelope gains three fields (device ID, public key fingerprint, observed revocation epoch). Protobuf compatibility rules require reserving field numbers and documenting the addition in the schema changelog.
- The server gains a `deviceidentity.Store` dependency. The dependency must be wired through `cmd/vibebridge/main.go` and exposed for tests. Loss of the store path fails closed.
- The tray management page, mobile shell, and CLI client must each call the nonce endpoint and sign the response before opening the WebSocket. Each surface needs its own test for missing headers, expired nonce, and revoked key.
- Lost-phone recovery is addressed in ADR-0007, which defines a CLI-based recovery flow using local filesystem access as the recovery trust root.
- Relay enforcement follows from header forwarding and the epoch-stamped ticket; the relay itself does not learn new state, but a relay that strips the headers must be detected and rejected by the server.
- External cryptographic review covers this ADR together with ADR-0004; the two are not independent decisions.

## Reconsider When

- IndexedDB key extraction becomes a realistic browser threat and a stronger client key store is required.
- The relay cannot forward custom headers without changes, requiring a second transport or envelope-level device authentication.
- Cryptographic review finds a flaw in the nonce construction, the host binding, or the revocation-epoch check.
- Lost-phone recovery (ADR-0007) proves insufficient and a faster in-place recovery path is needed.
