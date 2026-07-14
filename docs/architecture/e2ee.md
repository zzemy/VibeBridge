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

Device descriptors and five-minute bootstrap invitations are canonical Protobuf messages with deterministic Go/TypeScript vectors. The Agent has persistent Ed25519/X25519 identity material and replay-safe invitation consumption. The initial pairing cryptographic core is now implemented in Go and browser TypeScript using `Noise_XXpsk0_25519_ChaChaPoly_BLAKE2b`, including exact transcript binding, directional transport keys, transcript-derived SAS, and shared positive and negative vectors.

This cryptographic core is not yet an end-user pairing path. The phone-facing `/pairing/v1` framing, local Agent approval, atomic authorization/invitation consumption, browser key persistence, and authenticated paired-session handshake remain unfinished. The invitation verification code authenticates the QR payload only; the independent Noise SAS must be shown during local Agent approval. Remote exposure remains prohibited until those boundaries and the review gates below are complete.

## Primitive Baseline

Implemented pairing baseline:

- Ed25519 for signed device descriptors and revocation events.
- X25519 for static and ephemeral Diffie-Hellman contributions.
- The Noise framework's HKDF construction with BLAKE2b for pairing chaining and transcript hashing.
- ChaCha20-Poly1305 for pairing handshake and post-handshake transport AEAD.
- SHA-256 for relay-ticket transcript binding and the domain-separated human SAS derivation.

Go uses `github.com/flynn/noise`; the browser uses `noise-handshake` with `@noble/curves` as its X25519 adapter. BLAKE2b is the fixed suite hash because it is the interoperable hash exposed by the selected browser library, not a user-configurable preference.

Algorithm identifiers are versioned and negotiated from a small allowlist. There is no generic user-configurable cipher string.

## Handshakes

### Initial Pairing

The phone knows the signed Agent descriptor from the QR code and possesses a 32-byte bootstrap secret. Initial pairing uses exactly `Noise_XXpsk0_25519_ChaChaPoly_BLAKE2b`; the bootstrap secret is mixed at `psk0`. Both peers also contribute static X25519 keys and fresh ephemeral X25519 keys.

The authenticated message sequence is:

1. Message one encrypts the phone's signed descriptor under the PSK-bound transcript.
2. Message two carries the Agent static key and encrypted signed Agent descriptor.
3. Message three carries the phone static key and encrypted initiator-device-ID confirmation.

The Noise prologue is the domain string `VibeBridge pairing prologue v1\0` followed by deterministic `HandshakeContext` bytes. That context binds schema and protocol versions, both device IDs, SHA-256 of the exact relay ticket (including empty direct-mode tickets), pairing intent, and invitation ID. The phone checks that the responder Noise static equals the signed QR descriptor key; the Agent performs the corresponding check for the phone descriptor. These checks prevent route substitution and unknown-key-share acceptance.

The displayed SAS is derived only after all three messages complete:

```text
SHA256("VibeBridge pairing SAS v1\0" || 64-byte Noise handshake hash)
```

The first 64 bits are interpreted big-endian, reduced modulo `1_000_000`, zero-padded, and displayed as `123-456`. The current golden vector produces `710-268`.

### Established Device Session

Paired devices use an authenticated Noise IK-style handshake based on known X25519 static keys. The encrypted handshake payload includes signed device descriptors, key versions, revocation epochs, protocol ranges, capabilities, and the relay ticket hash.

## Session Keys

- Every transport connection derives fresh directional keys.
- Logical PTY sessions derive subkeys from the connection secret and session identifier.
- Attachment transfers derive independent subkeys so cancellation or compromise does not reuse terminal nonces.
- Nonces are generated from direction-specific monotonic counters, never random reuse-prone values.
- The browser wrapper permits at most `2^32` messages per direction because the selected JavaScript cipher implementation only encodes a 32-bit nonce; the connection must re-handshake before that limit.
- Rekey after a time or byte threshold and after transport migration.
- Key material is zeroed where runtime and library APIs make that meaningful. Go and JavaScript runtimes and their dependencies cannot guarantee deterministic heap zeroization.

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
