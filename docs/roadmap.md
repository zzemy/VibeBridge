# VibeBridge Technical and Product Roadmap

## Roadmap Rules

- Security and protocol foundations precede community remote access.
- Every phase leaves local-only mode usable.
- New complexity requires an observable product or reliability benefit.
- Migration gates are behavioral, not based only on code completion.
- Unsupported platforms and experimental features are labeled explicitly.

## Phase 0: Planning and Baseline

Goal: establish reliable current behavior and freeze the migration baseline.

Deliverables:

- Product V1 PRD, vision, architecture documents, ADRs, and threat model.
- Current protocol and session behavior documented.
- Windows ConPTY regression coverage, including idempotent cleanup.
- Frontend workflow and multi-viewport baseline.
- `SECURITY.md`, contribution guide, release checklist, and issue templates.
- Dependency inventory and license review.

Exit gate:

- Documentation has no unresolved contradiction about trust, data ownership, or deployment modes.
- Existing local mode passes automated and real ConPTY smoke tests.

Current status (2026-07-12):

- Complete. Planning/architecture documents, project policies/templates, CI, dependency/license review, and a real Windows ConPTY process-tree cleanup regression are in place.
- The project is licensed under Apache-2.0 and GitHub private vulnerability reporting is enabled.

## Phase 1: Local Agent Modularization

Goal: turn the current executable into a maintainable Local Agent without changing the user workflow.

Deliverables:

- Extract session state machine and internal PTY interface.
- Add deterministic clock/timer tests and platform adapters.
- Replace chunk-count replay limit with byte/time bounds.
- Introduce versioned local configuration and launch profiles.
- Add user-scoped background-service installation and diagnostics.
- Establish privacy-safe structured logging.

Exit gate:

- Explicit end, process exit, idle expiry, reconnect expiry, and Agent shutdown use one idempotent cleanup path.
- Windows process-tree tests prove no child remains.
- Current browser client works through the new Agent boundary.

Current status (2026-07-12):

- In progress. Session lifecycle transitions are explicit and serialized, lifecycle timers have deterministic fake-clock coverage, the session depends on an internal terminal launcher contract, Windows Job Objects and Unix process groups terminate PTY descendants, unsupported process-tree platforms fail explicitly, detached output is byte/time bounded, versioned local configuration provides structured launch profiles with working-directory and environment policies, allowlisted JSON lifecycle logs use opaque session correlation IDs without terminal, command, path, token, or environment content, and diagnostics aggregate command/profile, working-directory, listener, frontend, host-platform, LAN exposure, and firewall checks without starting a PTY.
- The Windows user-scoped background Agent implementation now provides least-privilege Task Scheduler install/status/uninstall commands, bounded restart behavior, authenticated runtime probing, sensitive user-local runtime state, explicit replacement semantics, and installation diagnostics. A Windows 11 install/start/authenticated-status/QR/forced-replacement/uninstall smoke test passes with native UTF-16LE Task Scheduler XML. A sign-out/sign-in autostart check on a clean non-administrator Windows user remains required before Phase 1 is declared complete. Broader macOS/Linux support still requires the platform gates below, and later phases will expand diagnostics for secure storage, workspaces/staging, protocol/update compatibility, and relay reachability as those capabilities are introduced.

## Phase 2: Protocol V1

Goal: establish a generated, versioned application protocol while preserving local mode.

Deliverables:

- `proto/vibebridge/v1` schema and Buf configuration.
- Generated Go and TypeScript packages.
- Hello/capability negotiation.
- Sequenced terminal input/output and acknowledgement.
- Resume and `RESYNC_REQUIRED` behavior.
- Stable error model and size limits.
- Legacy JSON adapter during migration.

Exit gate:

- Cross-language golden vectors pass.
- Breaking-change CI is active.
- Current and previous client builds pass compatibility tests.
- Legacy protocol can be disabled without changing session internals.

Current status (2026-07-13):

- In progress. The canonical `vibebridge.v1` envelope and Hello capability-advertisement schema, pinned Buf generation, committed Go/TypeScript packages, shared cross-language binary golden vector, schema lint, generated-code drift checks, and future-main breaking checks are in place.
- The browser and Agent negotiate `vibebridge.v1` with binary protobuf `Hello` envelopes before PTY creation, validate the selected version, connection ID, peer role, envelope limit, and required terminal capabilities, and close incompatible peers without mutating session state. When both peers advertise `terminal.sequenced_io_v1`, terminal input/output and explicit acknowledgements use ordered protobuf envelopes with duplicate/gap rejection, monotonic acknowledgement validation, a 32 KiB input bound, and Agent output chunking to the negotiated envelope limit. Cross-language Hello and terminal-output golden vectors pass.
- Negotiated `session.resume_v1` now binds each connection through `AttachSession`/`SessionStatus` to a random session ID and monotonically increasing PTY generation. Exact detach checkpoints and complete byte/time-bounded replay return `RESUMED`; stale identities, cursor mismatches, missing checkpoints, and truncated replay return `RESYNC_REQUIRED`, which resets stale browser history before retained output is replayed. Physical connections restart sequence state and re-encode replay from sequence 3. Older negotiated peers and peers that do not select the subprotocol retain the legacy adapter for staged compatibility. Stable V1 control/error messages, previous-client compatibility coverage, and a removable legacy boundary remain in progress.

## Phase 3: Workspace and Attachments

Goal: safely transfer phone files into a local AI workflow.

Deliverables:

- Workspace registry and canonical path policy.
- Session-scoped ignored staging directory.
- Attachment begin/chunk/complete/cancel protocol.
- Integrity, quota, cancellation, cleanup, and crash recovery.
- Mobile/web picker, camera input, preview, progress, and confirmation.
- Generic path adapter and reviewed Codex/Claude adapters.

Exit gate:

- Traversal, symlink, disk-full, Unicode, cancellation, and corruption tests pass.
- No file is referenced before checksum verification.
- Session cleanup removes partial and final temporary data according to policy.

## Phase 4: Device Identity and Pairing

Goal: replace URL bearer identity with revocable paired devices.

Deliverables:

- Platform secure-storage abstraction.
- Device descriptors and key lifecycle.
- Expiring single-use QR pairing.
- Pairwise authorization graph and local revocation.
- New E2EE handshake test vectors.
- Device management UI.
- Legacy local token mode retained behind explicit compatibility setting.

Exit gate:

- Pairing replay and race tests pass.
- Revoked device cannot create a new session.
- Lost-phone recovery is documented and tested.
- Cryptographic design has an independent review before remote exposure.

## Phase 5: Direct and Self-Hosted Remote Transport

Goal: run the same E2EE session protocol outside the LAN.

Deliverables:

- Agent outbound relay connection.
- Go relay with short-lived ticket validation.
- Direct-versus-relay transport selection.
- Self-hosted container deployment.
- Backpressure, quotas, reconnect jitter, and route expiry.
- Relay privacy and load tests.

Exit gate:

- Relay cannot decrypt inner protocol test fixtures.
- Wi-Fi/cellular transition resumes one PTY.
- Direct local mode works during relay outage.
- Single-node self-hosting guide is reproducible.

## Phase 6: PWA and Mobile Beta

Goal: deliver a credible installable mobile product.

Deliverables:

- PWA manifest, secure cache policy, update UI, and install guidance.
- Capacitor iOS/Android projects.
- Native secure-key operations, biometrics, QR scan, file/camera, share sheet, and deep links.
- Privacy-safe push notification pipeline.
- Real-device network/background test suite.

Exit gate:

- Supported iOS and Android devices pass pairing, terminal, attachment, background, and network transition tests.
- Private keys are not exported to web JavaScript in native mode.
- App update and Agent compatibility behavior is understandable.

## Phase 7: Community Relay Public Beta

Goal: provide zero-infrastructure remote onboarding using the open-source relay.

Deliverables:

- Community relay deployment and minimal Control API.
- Device discovery or pairing-based route discovery.
- Abuse controls and operator runbooks.
- Regional monitoring, staged deploy, incident process, and status page.
- Public threat model and security review results.
- Account/passkey discovery only if user research proves it necessary.

Exit gate:

- Security review findings are resolved or publicly risk accepted.
- Beta objectives are measured for availability, latency, crash-free sessions, and reconnect success.
- Self-hosted and community clients remain protocol compatible.

## Phase 8: Stable Open-Source V1

Goal: publish a maintainable, secure, and documented personal-developer product.

Deliverables:

- Signed Agent, relay, web, and mobile releases.
- Reproducible build and update metadata.
- Supported-version and compatibility policy.
- Migration and rollback guides.
- Complete self-hosting documentation.
- Stable protocol V1 and adapter API boundaries.

Exit gate:

- V1 PRD acceptance criteria pass.
- Stable release CI gates pass across supported platforms.
- Maintainers are assigned to protocol/security, Agent/platform, relay, client, and release operations.

## Cross-Cutting Workstreams

### Security

Threat model updates, protocol review, dependency scanning, fuzzing, key lifecycle, disclosure process, and update signing run across every phase.

### Quality

Unit, integration, compatibility, platform, real-device, load, chaos, and visual tests grow with the relevant boundary.

### Documentation

Every public behavior has operator and user documentation. ADRs change when decisions change; old ADRs are superseded rather than rewritten silently.

### Performance

Measure Agent memory, terminal latency, relay queueing, attachment throughput, mobile rendering, and battery use. Optimize only against captured profiles and defined targets.

### Maintenance

Dependencies, supported OS versions, protocol compatibility, release channels, telemetry schema, and incident response are maintained as product features.

## Explicit Deferrals

- Team and enterprise administration.
- Billing and feature tiers.
- Cloud code execution.
- Persistent cloud transcripts.
- Generic remote file manager.
- Desktop GUI control.
- Multi-region relay before single-region evidence.
- WebRTC/WebTransport before WebSocket measurements.
- Native UI rewrite before Capacitor evidence.
