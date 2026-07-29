# ADR-0006: Bind Terminal Session Authorization to Paired Device Identity

- Status: Proposed pending cryptographic review (companion to ADR-0004)
- Date: 2026-07-29

## Context

The current Agent accepts terminal WebSocket upgrades on a single gate: `Server.handleWebSocket` calls `s.validToken(r)` (internal/server/server.go), checking the per-run URL bearer token. The `vibebridge.v1` subprotocol is offered by the upgrader (internal/server/server.go, Subprotocols) and `negotiateProtocolV1` runs when the client selects it, but nothing on the wire binds the WebSocket session to a paired device identity. ADR-0004 already named pairwise device keys as the authorization root and left the binding between an open session and a device as open work.

Three concrete gaps follow from this:

1. A bearer token that escapes the URL, browser history, screenshots, or a logged shell grants full terminal access. Revocation requires rotating the per-run token and does not target the device.
2. There is no server-side proof that a WS upgrade came from a device the Agent has actually authorized. The relay forwards bytes, so the same gap exists for remote sessions.
3. Phase 4 exit criteria require pairing replay and race tests, revoked-device rejection, and lost-phone recovery; none can be tested against a binding that does not exist.

## Decision

Treat the paired device identity as the authorization root for terminal sessions and require an Ed25519-signed upgrade challenge on the `vibebridge.v1` subprotocol path. Legacy URL bearer tokens are kept only as a deliberate, documented compatibility mode.

Concretely:

- Every terminal WebSocket upgrade MUST include a `VibeBridge-Device` header carrying the device ID (32 bytes, hex) and a `VibeBridge-Device-Signature` header carrying an Ed25519 signature over a server-issued per-upgrade nonce. The nonce is delivered by the Agent immediately before the upgrade through a short-lived JSON endpoint, and is single-use, bound to the upgrade's remote address, and expires in 30 seconds.
- The server verifies the signature against the public signing key inside the `SignedDeviceDescriptor` recorded by `deviceidentity.Store.Authorize` and rejects the upgrade if the device is not in `DEVICE_AUTHORIZATION_STATE_AUTHORIZED` or if `RevocationEpoch()` has moved past the device's record.
- On the `vibebridge.v1` subprotocol the binding is enforced unconditionally. On the legacy path (`DisableLegacyProtocol == false`) the Agent also accepts the paired-device challenge; the URL bearer token remains accepted as a fallback. Default configuration enables paired-device required for the V1 subprotocol and continues to accept the legacy token until `DisableLegacyProtocol` is set by the operator or by the tray management page.
- `negotiateProtocolV1` is extended to publish the bound device ID, public key fingerprint, and the revocation epoch observed at upgrade time in its hello envelope. The relay forwards the headers unchanged, so remote sessions are gated by the same challenge.
- The browser stores the device key in IndexedDB, gated by a same-origin check and (when the platform allows) backed by `crypto.subtle` non-extractable keys. The mobile shell uses the platform secure store via a narrow Capacitor plugin. The CLI client reads the device identity from the existing `deviceidentity.Store` path.
- Phased migration: (1) introduce the verifier and the nonce endpoint behind a `RequirePairedSession` flag that defaults to false; (2) flip the default to true once the tray page, mobile shell, and CLI client all use the challenge and `go test -race` covers acceptance, revocation rejection, replay rejection, cross-origin rejection, and epoch-mismatch rejection; (3) flip `DisableLegacyProtocol` to true in a subsequent minor release and remove the URL token path.

## Rationale

- The `deviceidentity.Store` already owns the authorization graph and revocation epoch with atomic updates and overflow checks. Binding the upgrade to it adds no new trust root and reuses tests like `TestAuthorizeRevokeAndReloadDeviceGraph`.
- A per-upgrade signed nonce blocks replay, binds the signature to this specific connection attempt, and lets the server reject without holding per-connection state.
- Headers travel through the relay unchanged, so remote sessions get the same gate without changing the relay protocol or needing a second E2EE handshake.
- Keeping the legacy token path behind an explicit flag preserves a documented recovery mode (lost phone, new device) until lost-phone recovery ships in a later ADR.
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
- Lost-phone recovery is blocked until a separate ADR defines the recovery flow. Until then, an operator who loses every paired device must enable the legacy token mode (or wipe the Agent identity) to recover access. This limitation is explicit in the recovery ADR.
- Relay enforcement follows from header forwarding; the relay itself does not learn new state, but a relay that strips the headers must be detected and rejected by the server.
- External cryptographic review covers this ADR together with ADR-0004; the two are not independent decisions.

## Reconsider When

- IndexedDB key extraction becomes a realistic browser threat and a stronger client key store is required before the migration can advance to step 3.
- The relay cannot forward custom headers without changes, requiring a second transport or envelope-level device authentication.
- Cryptographic review finds a flaw in the nonce construction, the binding to the upgrade remote address, or the revocation-epoch check.
- Lost-phone recovery proves to be a frequent operational need and warrants a faster in-place recovery path than the legacy token mode currently offers.
