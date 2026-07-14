# End-to-End Encryption

## Objective

Only the paired client and Local Agent may read or modify terminal and attachment payloads. TLS protects network hops; E2EE protects against relay, proxy, logging, and control-plane compromise.

## Cryptographic Design Rules

- Use reviewed primitives and an established handshake framework.
- Do not invent ciphers, signature schemes, or ad hoc key derivation.
- Separate device identity keys, handshake keys, and per-session traffic keys.
- Bind protocol version, device identities, relay ticket, and session intent into the authenticated transcript.
- Publish protocol details and cross-language test vectors.
- Commission external review before stable remote relay release.

Reference: [Noise Protocol Framework](https://noiseprotocol.org/noise.html).

## Implementation Status (2026-07-15)

Device descriptors and five-minute bootstrap invitations are implemented as canonical Protobuf messages with deterministic Go/TypeScript vectors. The Agent has persistent Ed25519/X25519 identity material and replay-safe in-memory invitation consumption. No E2EE handshake, traffic encryption, phone-facing `/pairing/v1` transport, or paired-session authentication is implemented yet. The invitation verification code currently authenticates the QR payload only; the final short authentication string must be derived independently from the complete two-party Noise transcript and shown during Agent approval.

Remote exposure remains prohibited until the exact Noise pattern/PSK placement, browser-compatible implementation, transcript binding, directional counters, negative vectors, and cryptographic review gates below are complete.

## Primitive Baseline

Target baseline, subject to implementation-library review:

- Ed25519 for signed device descriptors and revocation events.
- X25519 for Diffie-Hellman key agreement.
- HKDF-SHA-256 for key derivation.
- ChaCha20-Poly1305 for transport AEAD, with AES-GCM allowed only through negotiated suites with hardware-backed benefit.
- SHA-256 or BLAKE2s according to the selected Noise suite and library support.

Algorithm identifiers are versioned and negotiated from a small allowlist. There is no generic user-configurable cipher string.

## Handshakes

### Initial Pairing

The phone knows the Agent public identity from the QR code and possesses a high-entropy bootstrap secret. Pairing uses a Noise pattern where the initiator authenticates the responder identity and mixes the bootstrap secret as a pre-shared input. The phone device descriptor is sent inside the protected transcript.

The exact pattern and PSK placement must be selected only after verifying library support, transcript properties, and published vectors. The design must prevent QR replay and unknown-key-share attacks.

### Established Device Session

Paired devices use an authenticated Noise IK-style handshake based on known X25519 static keys. The encrypted handshake payload includes signed device descriptors, key versions, revocation epochs, protocol ranges, capabilities, and the relay ticket hash.

## Session Keys

- Every transport connection derives fresh directional keys.
- Logical PTY sessions derive subkeys from the connection secret and session identifier.
- Attachment transfers derive independent subkeys so cancellation or compromise does not reuse terminal nonces.
- Nonces are generated from direction-specific monotonic counters, never random reuse-prone values.
- Rekey after a time or byte threshold and after transport migration.
- Key material is zeroed where runtime and library APIs make that meaningful.

## Message Protection

Authenticated associated data includes:

- Outer routing version.
- Direction.
- Connection identifier.
- Sequence number.
- Session generation.
- Cipher suite identifier.

Ciphertext replay, reordering beyond protocol allowance, duplicate sequence, and counter rollback are rejected.

## Transport Migration

Direct and relayed transports do not share raw traffic keys indefinitely. A migration performs a new authenticated handshake or a transcript-bound rekey. The logical session identifier remains stable, while the connection identifier and traffic keys change.

## Forward Secrecy

Ephemeral Diffie-Hellman contributions provide forward secrecy for session traffic even when a long-term device key is later compromised. Pairing and session transcripts must not rely only on static-static DH.

## Relay Tickets

Relay tickets authorize resource use and routing; they do not provide E2EE identity. The ticket hash is bound into the handshake so a relay cannot move a valid encrypted connection to a different route context without detection.

## Browser Security Boundary

PWA E2EE is limited by the browser origin: compromised same-origin JavaScript can access plaintext during use. Mitigations include strict CSP, Subresource Integrity where applicable, no third-party scripts, reproducible assets, short release chains, and transparent version display.

Native Capacitor builds provide stronger key storage but still execute the web UI. Native plugins must expose narrow cryptographic operations rather than exporting private keys into JavaScript.

## Recovery and Backup

V1 does not silently escrow device private keys to the community service. Device loss is recovered by revoking and pairing a replacement. Any future encrypted backup requires a separate threat model and user-controlled recovery secret.

## Verification

- Cross-language handshake and traffic test vectors.
- Negative tests for transcript tampering, replay, wrong identity, stale revocation, and ticket substitution.
- Nonce uniqueness and counter persistence tests.
- Fuzzing around framing before decryption and schema decoding after decryption.
- Dependency audit and pinned cryptographic implementations.
- Independent protocol review before stable remote availability.
