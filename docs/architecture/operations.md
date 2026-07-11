# Operations, Maintenance, and Release Strategy

## Service Objectives

Initial community relay objectives:

- Monthly relay availability target: 99.5% during beta.
- Successful authorized connection target: 99% excluding offline endpoints.
- Regional terminal relay latency target: p95 below 250 ms input-to-echo contribution from VibeBridge infrastructure.
- No silent attachment corruption.
- Agent crash-free session target: above 99% during public beta.

These are engineering targets, not contractual SLAs.

## Observability

Use OpenTelemetry-compatible instrumentation with an allowlist schema.

Reference: [OpenTelemetry documentation](https://opentelemetry.io/docs/).

Metrics:

- Connection attempts and outcomes by safe category.
- Active routes, sessions, queue bytes, and reconnect duration.
- Protocol and Agent version compatibility.
- Attachment bytes, duration, cancellation, and integrity failure counts.
- PTY lifecycle and cleanup outcomes.
- Update success, rollback, and version adoption.

Logs:

- Structured JSON in services.
- Opaque device/session correlation IDs.
- No terminal, prompt, path, attachment-name, key, or token fields.
- Local verbose diagnostics are explicitly enabled and still content safe.

Traces:

- Trace control-plane and relay lifecycle only.
- Do not attach raw frames or decrypted payloads.

## Release Channels

- `nightly`: automated builds, no compatibility promise.
- `beta`: migration-tested public testing.
- `stable`: signed releases within documented compatibility window.

Agent, relay, web, and mobile versions are independently identifiable even when released together.

## Versioning

- Semantic versioning for user-facing components.
- Protocol major/minor version independent from application semver.
- Database and local-state schema versions with forward migrations.
- Stable releases document minimum compatible Agent/client/relay versions.
- Deprecations require at least one stable release warning unless fixing a critical security issue.

## Signed Updates

Use a TUF-inspired signed metadata model with root, targets, snapshot, and timestamp roles or a reviewed implementation of The Update Framework.

Reference: [The Update Framework](https://theupdateframework.io/).

Requirements:

- Threshold-protected offline root keys.
- Short-lived online metadata roles.
- Hash and length verification.
- Expiry and rollback protection.
- Platform code signing.
- Atomic install and previous-version rollback.
- Reproducible-build guidance and public checksums.

## Dependency Management

- Automated dependency update PRs with tests.
- Lockfiles committed.
- Dependency license and vulnerability checks.
- Cryptographic and PTY dependencies receive explicit owner review.
- Avoid core behavior depending on abandoned libraries without an internal abstraction and migration plan.
- Generated code version is pinned and reproducible.

## Database Operations

If PostgreSQL is introduced:

- Migrations are additive before destructive.
- Services remain compatible with previous schema during rolling deploy.
- Backups and restore drills are documented.
- Terminal and attachment payloads never enter the database.
- Self-hosted migration commands are deterministic and versioned.

## Incident Response

Severity examples:

- Critical: E2EE bypass, update signing compromise, unauthorized terminal control.
- High: revocation failure, persistent child process across security boundary, cross-user route confusion.
- Medium: relay outage, attachment corruption detected, incompatible release rollout.
- Low: UI regression with available workaround.

Response process includes containment, key/ticket revocation where needed, patched release, public advisory, migration instructions, and post-incident review.

## Security Support

- Add `SECURITY.md` before public remote beta.
- Document supported versions and disclosure contact.
- Avoid discussing exploitable details publicly before a fix window.
- Publish CVE/advisory information when appropriate.

## CI Gates

- Go tests, race tests where supported, formatting, linting, and vulnerability scan.
- TypeScript typecheck, lint, unit tests, build, and Playwright tests.
- Protobuf lint, generation drift, breaking check, and cross-language vectors.
- Container build and vulnerability scan.
- Embedded binary and platform packaging.
- Upgrade/rollback smoke test.
- Secret scan and generated-artifact check.

## Maintenance Ownership

Each subsystem has an explicit maintainer and compatibility owner:

- Protocol and cryptography.
- Agent and PTY platforms.
- Relay/control services.
- Web/mobile client.
- Release/update infrastructure.
- Security response and privacy schema.

An unsupported subsystem cannot silently remain in stable release scope.
