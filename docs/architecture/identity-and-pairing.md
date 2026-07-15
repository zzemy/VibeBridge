# Identity and Pairing

## Current Implementation Boundary (2026-07-15)

The Local Agent now generates and persists one random 16-byte device ID, Ed25519 signing key, and X25519 static key. Its signed descriptor and the pairing invitation have canonical Protobuf definitions plus shared Go/TypeScript binary vectors. Windows stores the versioned identity/authorization state at `%LOCALAPPDATA%\VibeBridge\identity.json` using current-user DPAPI with purpose-bound optional entropy; `--identity-store` accepts an absolute override. First creation is serialized across processes, writes are atomic, and malformed, unknown-version, or undecryptable state fails closed. The current macOS/Linux source-build fallback relies on an owner-only mode-`0600` file and is lower assurance than a platform keychain.

The Agent can issue one five-minute invitation at a time. Each contains a 128-bit invitation ID and 256-bit bootstrap secret, a new invitation supersedes the old one, and successful consumption is atomic and replay-safe. The full invitation is encoded only in the URL fragment. The Agent also persists client authorization versions and monotonic revocation epochs; its local authenticated management page can list and revoke devices.

The cross-language `Noise_XXpsk0_25519_ChaChaPoly_BLAKE2b` pairing core is implemented and validated against one shared transcript and directional-transport vector. The direct `/pairing/v1` path now accepts the binary-Protobuf handshake, derives the encrypted approval transport, and exposes the request to the local Agent tray/management UI. The browser removes the invitation fragment before decoding and persists a trusted Agent only after encrypted `APPROVED` with a positive authorization version. Relay and remote terminal control remain future work; the direct listener must not be exposed publicly.

## Identity Layers

VibeBridge separates three concepts:

- User discovery identity: optional account or self-hosted identity used to find devices.
- Device identity: cryptographic identity of one Agent or client installation.
- Session authorization: short-lived permission to start or attach to one session.

An account never replaces device-key authentication. This separation keeps self-hosting possible and limits control-plane compromise.

## Device Keys

Each device generates:

- An Ed25519 signing key for device statements, revocations, and key rotation.
- An X25519 static key for authenticated key agreement.
- A random opaque device identifier independent of hardware serial numbers.

The signed device descriptor contains public keys, device identifier, display metadata, creation time, key version, and supported protocol range.

Private keys are non-exportable where platform APIs allow it:

- iOS: Keychain/Secure Enclave-backed storage where compatible.
- Android: Keystore-backed storage.
- Windows: DPAPI or CNG-backed protected storage.
- macOS: Keychain.
- Linux: Secret Service when available, with an explicit file-permission fallback.
- PWA/browser MVP: Ed25519/X25519 private material and signed descriptors are stored in IndexedDB, documented as lower assurance than native secure storage. Hardware-backed or non-extractable browser key storage remains future work.

## Pairing Bootstrap

The Agent creates an expiring, single-use pairing record and QR payload containing:

- Pairing version.
- Agent opaque device identifier.
- Agent public key fingerprint or descriptor.
- At least 128 bits of random bootstrap secret.
- Relay or direct-discovery hints.
- Expiry timestamp.
- A QR-payload verification checksum; the separate approval SAS is derived only after the complete Noise transcript.

The QR code is a bootstrap capability, not a permanent credential.

## Pairing Flow

1. Phone scans and validates QR structure and expiry.
2. Phone connects directly or through a pairing relay route.
3. Peers run `Noise_XXpsk0` with the QR bootstrap secret at `psk0`, binding both device IDs, invitation ID, protocol version, intent, and relay-ticket hash into the prologue.
4. Phone sends its signed descriptor in encrypted message one; both peers verify the Noise static key against the corresponding signed descriptor.
5. Both screens show device names and the transcript-derived `123-456` SAS.
6. User confirms on at least the Agent side; higher-assurance mode confirms both.
7. Agent atomically consumes the pairing record and stores the phone descriptor.
8. Phone stores the Agent descriptor.
9. Both sides derive no permanent secret solely from the QR token; future sessions use device keys.

Exact handshake construction is defined in the E2EE document and validated through published test vectors.

## Device Graph

For an individual user, every Agent stores its authorized client descriptors and revocation epochs locally. Optional account discovery may mirror public descriptors and signed revocation events.

No central service is the sole source of authority for an Agent. The Agent can continue local-only operation using its local device graph.

## Revocation

- Each authorized client has a monotonically increasing authorization version.
- Agent local revocation is immediately authoritative.
- Signed revocation events may synchronize through the Control API.
- New handshakes include the latest known revocation epoch.
- Relay tickets expire quickly so revoked devices lose routing access without long cache windows.
- Established sessions terminate when an applicable newer revocation event is accepted.

## Key Rotation

- Devices rotate X25519 transport keys without changing user-visible identity when the Ed25519 identity key signs the transition.
- Identity-key rotation requires approval from an already trusted device or a new pairing ceremony.
- Lost-all-devices recovery is intentionally separate from normal rotation and may require re-pairing each Agent.
- Old keys have explicit not-after times and remain only for a bounded overlap window.

## Optional Accounts

Accounts may improve device discovery and recovery but are not required for cryptographic trust.

If introduced:

- Prefer passkeys and OIDC over password-only authentication.
- Account records store public device descriptors and encrypted discovery metadata.
- Account sessions cannot directly authorize terminal access without a paired device key.
- Self-hosted deployments can disable accounts or use configured OIDC.
- Account deletion does not silently delete local repositories or transcripts because those remain local.

## Privacy

- Device identifiers are random and scoped to VibeBridge.
- Hardware serial numbers, usernames, and full hostnames are not uploaded by default.
- Display names are user-controlled and encrypted where relay routing does not require them.
- Pairing logs record opaque identifiers, outcome, and timestamp, not bootstrap secrets.

## Verification

- Deterministic pairing transcript test vectors.
- Expiry, replay, race, and atomic-consume tests.
- Revocation propagation and stale-ticket tests.
- Platform secure-storage integration tests.
- Lost-phone and lost-Agent recovery drills.
