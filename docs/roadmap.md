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

Current status (2026-07-30):

- Code complete. Session lifecycle transitions are explicit and serialized, lifecycle timers have deterministic fake-clock coverage, the session depends on an internal terminal launcher contract, Windows Job Objects and Unix process groups terminate PTY descendants, unsupported process-tree platforms fail explicitly, detached output is byte/time bounded, versioned local configuration provides structured launch profiles with working-directory and environment policies, allowlisted JSON lifecycle logs use opaque session correlation IDs without terminal, command, path, token, or environment content, and diagnostics aggregate command/profile, working-directory, listener, frontend, host-platform, LAN exposure, and firewall checks without starting a PTY.
- The Windows user-scoped background Agent implementation now provides least-privilege Task Scheduler install/status/uninstall commands, bounded restart behavior, authenticated runtime probing, sensitive user-local runtime state, explicit replacement semantics, installation diagnostics, and an in-process system tray with live session status, local UI/pairing entry points, and graceful process-tree-safe exit. A Windows 11 install/start/authenticated-status/QR/forced-replacement/uninstall smoke test passes with native UTF-16LE Task Scheduler XML. A sign-out/sign-in autostart check on a clean non-administrator Windows user remains required before the Phase 1 exit gate is met. Broader macOS/Linux support still requires the platform gates below, and later phases will expand diagnostics for secure storage, workspaces/staging, protocol/update compatibility, and relay reachability as those capabilities are introduced.

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

Current status (2026-07-14):

- Complete. The canonical `vibebridge.v1` envelope and Hello capability-advertisement schema, pinned Buf generation, committed Go/TypeScript packages, shared cross-language binary golden vector, schema lint, generated-code drift checks, and future-main breaking checks are in place.
- The browser and Agent negotiate `vibebridge.v1` with binary protobuf `Hello` envelopes before PTY creation, validate the selected version, connection ID, peer role, envelope limit, and required terminal capabilities, and close incompatible peers without mutating session state. When both peers advertise `terminal.sequenced_io_v1`, terminal input/output and explicit acknowledgements use ordered protobuf envelopes with duplicate/gap rejection, monotonic acknowledgement validation, a 32 KiB input bound, and Agent output chunking to the negotiated envelope limit. Cross-language Hello and terminal-output golden vectors pass.
- Negotiated `session.resume_v1` now binds each connection through `AttachSession`/`SessionStatus` to a random session ID and monotonically increasing PTY generation. Exact detach checkpoints and complete byte/time-bounded replay return `RESUMED`; stale identities, cursor mismatches, missing checkpoints, and truncated replay return `RESYNC_REQUIRED`, which resets stale browser history before retained output is replayed. Physical connections restart sequence state and re-encode replay from sequence 3.
- Negotiated `terminal.resize_end_v1` now carries bounded `TerminalResize` and explicit `EndSession` controls in the ordered Protobuf stream. Peers without the capability retain JSON resize/end compatibility, while negotiated peers reject those JSON controls. Negotiated `session.process_exit_v1` now carries an ordered final `ProcessExit` with a lifecycle-derived `SUCCESS` or `FAILURE` outcome and never exposes the raw host process error; peers without it retain the JSON exit adapter.
- Negotiated `control.error_v1` now carries ordered enum-only application `Error` payloads and requires sequenced I/O. Startup and occupied-session failures may be reported before resume binding with empty session metadata; post-bind errors carry the bound identity. Both the browser and JSON fallback map the allowlisted codes to fixed safe text, so raw launcher, PTY input, and resize errors are not sent. Protocol violations continue to close with code `1002`.
- Negotiated `control.health_v1` now carries empty ordered `Ping`/`Pong` envelopes after resume-enabled session binding and requires sequenced I/O. The Agent commits each Ping before returning a Pong whose acknowledgement covers it. Peers without the capability retain the application JSON ping/pong adapter; negotiated peers reject that adapter. WebSocket control-frame Ping/Pong remains an independent transport keepalive.
- Compatibility CI builds and tests the current browser and the previous stable browser pinned at `dfc6a108550258fba8c7652351193fa89f01014d`. Agent integration coverage fixes that previous client's no-subprotocol raw-binary plus JSON input/resize/ping/exit behavior, and exercises the complete current V1 Hello, fresh attachment, and ordered output path.
- The legacy adapter remains enabled by default but can be disabled by configuration or CLI. Disabled mode rejects no-subprotocol clients with HTTP `426` and V1 clients missing any current capability with WebSocket `1002`, before Agent Hello or PTY creation; the gate is confined to protocol ingress and does not change session lifecycle or PTY internals.

## Phase 3: Workspace and Attachments

Goal: safely transfer phone files into a local AI workflow.

Deliverables:

- Workspace registry and canonical path policy.
- Session-scoped ignored staging directory.
- Attachment begin/chunk/complete/cancel/discard protocol.
- Integrity, quota, cancellation, cleanup, and crash recovery.
- Mobile/web picker, camera input, preview, progress, and confirmation.
- Generic path adapter and reviewed Codex/Claude adapters.

Exit gate:

- Traversal, symlink, disk-full, Unicode, cancellation, and corruption tests pass.
- No file is referenced before checksum verification.
- Session cleanup removes partial and final temporary data according to policy.

Current status (2026-07-30):

- Code complete — real-device validation pending. Version 2 local configuration now includes a validated workspace registry with stable IDs, labels, canonical absolute roots, duplicate-root detection, case-preserving Windows final-path identity, and symlink/junction resolution. Launch profiles can opt into a workspace boundary; their default, relative, or absolute working directory must resolve inside that root during configuration and immediately before PTY launch, while profiles without `workspace_id` remain compatible.
- Traversal, Unicode, symlink, and Windows junction workspace-policy tests are in place. Workspace-bound PTY sessions now reserve an ignored `.vibebridge/uploads/<session-id>/` staging directory named from the Agent-generated opaque session ID; startup rollback and session-end cleanup are idempotent, and creation/cleanup reject link escapes without exposing local paths.
- Protocol V1 defines additive transfer begin/chunk/complete/cancel/discard envelopes behind `attachment.transfer_v1` and trusted prepare/preview/commit/cancel envelopes behind `attachment.prompt_action_v1`. The prompt capability additionally requires transfer and stable-error support. Opaque IDs, bounded prompts/previews, cross-language generation, and golden vectors protect the wire contract while both production peers keep the capabilities unadvertised.
- Workspace staging file operations now bind to an `os.Root` directory handle, accept only Agent-generated 128-bit hexadecimal names with allowlisted suffixes, create exclusively, probe hard-link publication support up front, revalidate directory identity before every chunk write or mutation, reject replacement links and moved boundaries, refuse destination overwrite, and coordinate handle closure with retryable session cleanup.
- The session-side transfer manager now validates opaque IDs and display metadata, reserves per-file/session quota, enforces exact ordered chunks with per-chunk SHA-256, verifies total size and SHA-256, checks a deterministic MIME/extension allowlist against binary headers or whole-stream UTF-8 text validation, publishes only verified files, and provides idempotent completion/cancellation plus active-partial cleanup on close. Completed files remain quota-reserved until explicit discard or session cleanup.
- Protocol V1 now decodes begin/chunk/complete/cancel plus the additive multi-file discard operation behind the peer capability declaration and dispatches them to one lazily created, session-owned manager for workspace sessions. Manager side effects precede sequence commit and acknowledgement; a rejection leaves the failed sequence uncommitted, abandons its active partial, and reports only `ATTACHMENT_TRANSFER_FAILED`. Session exit closes the manager before staging cleanup.
- The dark browser flow now retains opaque transfer IDs only after acknowledged completion, prepares 1–10 completed files under one reconnect-stable action ID, and shows the exact Agent preview plus effective Enter behavior in a separate confirmation dialog. The Agent resolves relative paths, uses the configured generic adapter, keeps bounded session-local idempotency state, commits retained terminal bytes exactly once, and prompt-cancels without deleting staged files; failures use only `ATTACHMENT_PROMPT_ACTION_FAILED`. Lost prepare/commit/cancel responses are retried with the same action ID after session rebind. Pre-acknowledgement transfer outcomes are reconciled after same-PTY rebind through a path-free status query: active state exposes only the committed byte cursor, completed state is retained, and cancelled state uses a bounded tombstone registry. A failed or user-cancelled multi-file selection now discards every started ID, including completed files; an ambiguous discard queries each ID and replays the whole idempotent batch if any file remains. Cleanup failures preserve the transfer error and identify session cleanup as fallback. The App flag and both production Hello advertisements are now enabled. Reviewed Codex/Claude adapters, per-device/Agent aggregate limits, crash recovery scans, and no-workspace sandbox staging are complete. End-to-end/real-device validation remains before the phase exit gate is met.

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

Current status (2026-07-30):

- Code complete — independent cryptographic review and stronger browser key protection remain before the exit gate is met. The Agent now has protected persistent Ed25519/X25519 identity state, canonical signed descriptors, deterministic Go/TypeScript identity and invitation vectors, atomic cross-process first creation, one active five-minute/single-use invitation, local authorization versions, monotonic revocation epochs, a local authenticated tray management page for invitation display and device revocation, and a complete direct `/pairing/v1` browser handshake with encrypted approval/rejection. Real Edge-to-Agent reject/approve tests verify fragment clearing, IndexedDB trust gating, and matching Agent authorization. Paired-session authorization (ADR-0006) is fully implemented and enabled by default. Relay revocation enforcement is enabled by default. Lost-phone recovery is defined in ADR-0007 (CLI bootstrap). An independent cryptographic review and stronger browser key protection remain before the phase exit gate.
- The Phase 4 exit gate is not met. Stronger browser key protection and an independent cryptographic review remain required. Terminal access now requires paired-device authentication; the legacy per-run bearer token has been removed from the WebSocket upgrade path.

## Phase 5: Direct and Self-Hosted Remote Transport (Complete)

Goal: run the same E2EE session protocol outside the LAN.

Deliverables (all complete):

- Agent outbound relay connection. — Manager with message-aware WebSocket bridging (BridgeWebSocket + ConnectWebSocket).
- Go relay with short-lived ticket validation. — Ed25519-signed RelayTicket with replay protection, TTL, and revocation epoch gate.
- Direct-versus-relay transport selection. — Web client auto-falls back from direct to relay when the Agent is unreachable; ?relay=1 / ?direct=1 URL params force a transport.
- Self-hosted container deployment. — Multi-stage Dockerfile, docker-compose.yml with Caddy TLS option, and docs/self-hosting.md guide.
- Backpressure, quotas, reconnect jitter, and route expiry. — Per-route bounded buffer (4 slots) with slow-consumer route drop; MaxConnections cap; sweeper with IdleTimeout and MaxLifetime; exponential backoff with full jitter (1–30s, max 10 attempts) in the web client.
- Relay privacy and load tests. — privacy_test.go (5 tests: plaintext/random/fake-ticket forwarding, log hygiene, byte-exact large payload); load_test.go (5 tests: concurrent routes, slow consumer, table churn, sweeper orphans, bounded memory); quota_test.go (10 tests: sweep lifecycle, max-connections cap, negative caps, sweep result accounting).

Exit gate:

- Relay cannot decrypt inner protocol test fixtures. — Covered by TestServerForwardsPlaintextOpqaquely, TestServerForwardsRandomBytesOpqaquely, TestServerForwardsFakeTicketBytesOpacaquely, TestServerLogsNeverContainPayloadBytes.
- Wi-Fi/cellular transition resumes one PTY. — Requires real-device validation; V1 session resume + reconnect jitter provide the mechanism.
- Direct local mode works during relay outage. — Auto transport selection falls back from relay to direct when relay is unavailable.
- Single-node self-hosting guide is reproducible. — docs/self-hosting.md covers Docker setup, key management, TLS, and firewall.

## Phase 6: PWA and Mobile Beta (Code Complete — Real-Device Validation Pending)

Goal: deliver a credible installable mobile product.

Deliverables (code complete):

- PWA manifest, secure cache policy, update UI, and install guidance. — `web/public/manifest.json` (standalone display, theme color, icons); `web/public/sw.js` service worker (stale-while-revalidate for static assets only; API/WebSocket never cached); `InstallPrompt` component (beforeinstallprompt); `UpdateBanner` component (SW update detection + reload); `usePWAInstall` / `useSWUpdate` hooks.
- Capacitor iOS/Android projects. — `web/capacitor.config.ts` configured (appId, webDir=dist, iOS/Android schemes, SplashScreen, PushNotifications). ADR-0003 accepted.
- Native secure-key operations, biometrics, QR scan, file/camera, share sheet, and deep links. — Deferred to native plugin implementation (requires Capacitor CLI + platform SDKs). Architecture defined in ADR-0003.
- Privacy-safe push notification pipeline. — Capacitor PushNotifications configured; relay does NOT hold push credentials. Agent-side push trigger stub defined.
- Real-device network/background test suite. — Requires physical device testing.

Exit gate:

- Supported iOS and Android devices pass pairing, terminal, attachment, background, and network transition tests. — Requires real-device validation.
- Private keys are not exported to web JavaScript in native mode. — Enforced by V1 protocol: device identity operations happen Agent-side; web client never holds private keys.
- App update and Agent compatibility behavior is understandable. — UpdateBanner + version policy (docs/version-policy.md) + capability negotiation in Protocol V1 Hello.

## Phase 7: Community Relay Public Beta (Code Complete — Beta Measurement Pending)

Goal: provide zero-infrastructure remote onboarding using the open-source relay.

Deliverables (code complete):

- Community relay deployment and minimal Control API. — Dockerfile + docker-compose.yml + docs/self-hosting.md; admin API on port 8789 for ticket issuance and route inspection.
- Device discovery or pairing-based route discovery. — Agent provision endpoint (`/agent/relay/provision`) mints per-session tickets; route ID is random 128-bit.
- Abuse controls and operator runbooks. — Rate limiting on provision endpoint (`ratelimit.go`, 10 req/min per IP); MaxConnections cap on relay; sweeper with IdleTimeout/MaxLifetime; docs/operator-runbook.md (122 lines).
- Regional monitoring, staged deploy, incident process, and status page. — `/health` endpoint on relay; operator runbook covers incidents, key rotation, capacity planning.
- Public threat model and security review results. — ADR-0008 (community relay threat model: 6 threats, mitigations, accepted risks).
- Account/passkey discovery only if user research proves it necessary. — Deferred.

Exit gate:

- Security review findings are resolved or publicly risk accepted. — ADR-0008 documents accepted risks (metadata visibility, single-relay DDoS, no reputation system).
- Beta objectives are measured for availability, latency, crash-free sessions, and reconnect success. — Requires production beta deployment.
- Self-hosted and community clients remain protocol compatible. — Protocol V1 capability negotiation ensures backward compatibility.

## Phase 8: Stable Open-Source V1 (Code Complete — Release Pending)

Goal: publish a maintainable, secure, and documented personal-developer product.

Deliverables (code complete):

- Signed Agent, relay, web, and mobile releases. — Makefile `release` target builds cross-platform static binaries; `sign`/`verify` targets use Ed25519 signing.
- Reproducible build and update metadata. — Makefile uses `-trimpath -ldflags="-s -w"` for reproducible builds; `VERSION` from git describe.
- Supported-version and compatibility policy. — docs/version-policy.md (semver, 6-month support window, protocol capability negotiation, rollback procedures).
- Migration and rollback guides. — docs/version-policy.md covers agent/relay rollback, key rotation, patch updates.
- Complete self-hosting documentation. — docs/self-hosting.md (272 lines) + docs/operator-runbook.md (122 lines).
- Stable protocol V1 and adapter API boundaries. — Protocol V1 stable since Phase 4; tool adapter API (`internal/tooladapter`) stable since Phase 3.

Exit gate:

- V1 PRD acceptance criteria pass. — Requires formal PRD review.
- Stable release CI gates pass across supported platforms. — Requires CI pipeline setup.
- Maintainers are assigned to protocol/security, Agent/platform, relay, client, and release operations. — Requires project governance decision.

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

## Desktop Application and Internationalization (Code Complete)

Goal: provide a native desktop client and bilingual user interface alongside the web client.

Deliverables (code complete):

- Desktop Tauri application (Windows) with sidebar navigation, native window chrome, and the same four-panel layout as the web client.
- NSIS per-user Windows installer with silent install/uninstall, HKCU Run key for automatic startup, and Start Menu shortcuts.
- Four-tab mobile shell (Terminal, Files, Settings, Info) with bottom tab-bar navigation, safe-area insets, and no horizontal scroll.
- Bilingual user interface (Chinese/English) with automatic language detection (`navigator.language`) and manual switch persisted to `localStorage`.
- Pairing auto-redirect: successful device pairing automatically enters the terminal after a brief confirmation.

Exit gate:

- Real-device validation on Windows 11 (installer, startup, terminal, settings). — Requires manual testing.
- macOS/Linux desktop builds. — Requires platform-specific Tauri configuration and testing.

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
