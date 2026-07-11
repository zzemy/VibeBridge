# Threat Model

## Security Objective

A paired personal device may control a developer-owned Local Agent. Unpaired devices, relay operators, network observers, malicious websites, and revoked devices must not gain terminal or attachment access.

This threat model covers the Local Agent, mobile/PWA client, relay, optional Control API, pairing, sessions, attachments, updates, and operational telemetry.

## Protected Assets

- Terminal input and output.
- Source code, repository metadata, and local file paths.
- AI CLI credentials and environment variables.
- Device private keys and session keys.
- Pairing bootstrap secrets and relay tickets.
- Uploaded images and files.
- Device graph and revocation state.
- Update signing keys and release metadata.

## Trust Boundaries

```text
Phone OS secure storage
        |
Mobile client process
        | E2EE boundary
Untrusted network / relay / proxies
        | E2EE boundary
Local Agent process
        |
PTY process and local workspace
```

The relay and Control API are not trusted with session plaintext. The local operating system and paired endpoint processes are trusted within their documented permissions.

## Adversaries

- A device on the same LAN scanning or replaying pairing URLs.
- A malicious website attempting cross-site WebSocket control.
- A network observer or TLS-terminating intermediary.
- A compromised or malicious relay operator.
- A stolen but locked phone.
- A previously paired and later revoked phone.
- Malicious uploaded files and crafted file names.
- A dependency or update supply-chain attacker.
- An abusive user consuming community relay resources.
- Local malware already running as the same OS user.

V1 cannot protect the user from fully compromised endpoint operating systems. It must limit persistence, exposure, and lateral impact.

## Threats and Required Controls

### Pairing Interception or Replay

Threat: an attacker captures a QR code or bootstrap URL and pairs first or reuses it.

Controls:

- At least 128 bits of random bootstrap entropy.
- Short expiry and one successful use.
- Agent identity fingerprint inside the QR payload.
- User confirmation displaying both device names and fingerprints.
- Atomic consume operation and replay log.
- No long-lived bearer token in the final URL.

### Relay Impersonation and Traffic Inspection

Threat: a relay reads or modifies terminal data.

Controls:

- TLS for transport plus independent end-to-end authenticated encryption.
- Client verifies Agent static device identity.
- Authenticated transcript binds protocol version, relay ticket, and both device identities.
- Monotonic message sequence and AEAD authentication reject modification and replay.

### Cross-Site Browser Control

Threat: an untrusted origin opens a WebSocket or upload request using ambient credentials.

Controls:

- Exact Origin validation.
- No cookie-only authorization for session control.
- CSRF protection for browser HTTP mutations.
- SameSite and Secure cookie policy if accounts are introduced.
- Capability tokens scoped to device, session, action, and expiry.

### Stolen Phone

Threat: a person with the phone opens an existing session.

Controls:

- Device private key in Keychain/Keystore.
- Optional or required biometric gate for control actions.
- Short UI unlock timeout.
- Remote device revocation.
- Push notifications omit sensitive content.

### Revoked Device Reconnection

Threat: a revoked device uses cached tickets or session material.

Controls:

- Short-lived relay and session tickets.
- Revocation epoch included in authorization.
- Agent checks local revocation before every new handshake.
- Established sessions close when a newer signed revocation state is received.

### Session Confusion or Hijack

Threat: a client attaches to the wrong PTY or replaces another controller.

Controls:

- Unpredictable session identifiers bound to Agent identity.
- Explicit ownership and active-controller state.
- Attach requires authenticated device authorization.
- Control transfer requires visible approval or deterministic policy.
- Resume uses sequence acknowledgements and session generation numbers.

### Attachment Path Traversal

Threat: crafted names overwrite source, configuration, or executable files.

Controls:

- Server-generated storage names.
- Original name stored as display metadata only.
- Canonical path containment check after join and before every file operation.
- No archive extraction in V1.
- No executable launch or shell interpolation.
- Session quota, file quota, checksum, and partial-file cleanup.

### Malicious Attachment Content

Threat: the user or a compromised phone uploads active content that later executes.

Controls:

- Content-type detection independent of browser MIME.
- Allowlist for product-supported attachment flows.
- Files remain data until the user/tool explicitly processes them.
- Platform malware scanning is optional and documented, not silently claimed.
- Web previews use sandboxing and never render arbitrary HTML directly.

### Resource Exhaustion

Threat: large frames, slow readers, reconnect storms, or attachment floods exhaust Agent or relay resources.

Controls:

- Frame, queue, session, bandwidth, attachment, and connection limits.
- Backpressure and cancellation.
- Per-device and per-source relay quotas.
- Exponential reconnect backoff with jitter.
- Bounded terminal replay buffer by bytes, not chunk count alone.
- Metrics that exclude content.

### Update Compromise

Threat: malicious release metadata or binaries compromise every Agent.

Controls:

- Offline-protected release root keys and delegated signing keys.
- Signed metadata with version, hash, length, platform, expiry, and rollback protection.
- Native platform code signing where available.
- Reproducible build documentation and published checksums.
- Staged rollout, rollback, and manual update path.

### Sensitive Logging

Threat: logs or telemetry store terminal text, paths, tokens, or attachment names.

Controls:

- Structured allowlist logging rather than redaction after capture.
- Stable opaque identifiers.
- No request bodies or raw protocol frames in production logs.
- Documented telemetry schema and retention.
- Tests that fail when prohibited fields reach logging adapters.

## Abuse Model for Community Relay

The community relay can be abused even when it cannot decrypt payloads.

Required controls:

- Proof of valid device pairing or account authorization before allocation.
- Short-lived signed tickets.
- Per-device connection and bandwidth limits.
- Global frame and attachment relay limits.
- Automated expiration of orphaned routes.
- Operator ability to block abusive opaque device identifiers.
- No content inspection as a substitute for protocol-level abuse control.

## Security Verification

- Unit tests for token expiry, replay, sequence, path containment, and cleanup.
- Protocol fuzzing for all untrusted decoders.
- Integration tests with malicious origins, frames, filenames, and reconnect patterns.
- Cryptographic test vectors shared across Go and TypeScript implementations.
- Dependency and secret scanning in CI.
- External security review before stable community relay operation.
- A public `SECURITY.md` with supported versions and private disclosure instructions.

## Residual Risks

- Endpoint malware can observe plaintext inside the trusted process boundary.
- The relay sees timing, sizes, IP addresses, and routing identifiers.
- Push providers see device tokens and delivery metadata.
- Tool adapters may expose additional data and require separate review.
- Account recovery can weaken device-key security if designed carelessly.

These risks must be documented in user-facing privacy and deployment guidance.
