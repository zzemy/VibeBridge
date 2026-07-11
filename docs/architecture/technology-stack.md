# Technology Stack and Selection Criteria

## Selection Policy

Technology choices are evaluated against security, maintainability, cross-platform support, self-hosting, contributor accessibility, migration cost, and measured performance.

The project does not standardize a tool merely because it is fashionable. Every major choice has an ADR and a reconsideration trigger.

## Recommended Stack

| Layer | Target choice | Current state | Why | Reconsider when |
| --- | --- | --- | --- | --- |
| Local Agent | Go | Go monolith | PTY/process control, concurrency, single binaries, cross-platform | Platform reliability or maintainer capacity proves inadequate |
| Relay | Go | Not implemented | Shared protocol/security code, efficient connection handling, self-hosting | Another runtime provides measured operational benefit |
| Control API | Go with Protobuf-derived HTTP/RPC contracts | Not implemented | Shared models and one operational language | Public API ergonomics require a separate gateway |
| Session transport | WSS/WebSocket | WebSocket | Broad browser/proxy support and operational simplicity | Measurements justify WebTransport/WebRTC |
| Protocol schema | Protobuf + Buf | Hand-written JSON and raw frames | Generated Go/TS types and breaking checks | Tooling blocks supported clients |
| E2EE | Noise-style reviewed handshake with X25519/Ed25519 and AEAD | TLS/token only | Pairwise device trust and relay confidentiality | Security review selects a stronger standard design |
| Web UI | React + TypeScript + Vite | Implemented | Existing investment, ecosystem, xterm integration | Maintainability or performance misses targets |
| Design system | Tailwind tokens + Radix primitives | Partial | Accessible primitives and controlled styling | Native clients require separate platform components |
| Terminal | xterm.js | Implemented | Mature ANSI terminal across web and WebView | Real-device performance or accessibility fails |
| Mobile shell | Capacitor | Not implemented | Reuse web terminal while adding native APIs | Native rewrite has measured quality advantage |
| Agent state | Versioned files, then SQLite where justified | Flags/in-memory | Simple first, migrations when structured state grows | Concurrency and query needs require earlier SQLite |
| Control metadata | PostgreSQL | Not implemented | Durable relational device/revocation/account data | Product avoids hosted discovery entirely |
| Relay presence | In-memory, optional Redis at horizontal scale | Not implemented | Avoid infrastructure until multiple instances need coordination | Multi-instance routing requires shared state |
| Update security | TUF-inspired signed metadata + platform signing | Manual build | Rollback and key-compromise resilience | Reviewed platform framework provides stronger fit |
| Observability | OpenTelemetry-compatible metrics/traces and structured logs | Basic console output | Vendor-neutral, privacy-controlled operations | Operational simplicity requires a smaller beta setup |
| Web tests | Vitest + Testing Library + Playwright | Vitest and Playwright checks | Behavior and real-browser coverage | No current reason |
| Go tests | Standard tests, fuzzing, race/platform integration | Standard tests | Native ecosystem and failure-path coverage | No current reason |
| CI/CD | GitHub Actions with reproducible scripts | Not established | Repository hosting integration and open visibility | Project hosting changes |
| Packaging | Signed native binaries, OCI relay images, app-store mobile packages | Embedded Windows binary | Appropriate distribution per component | Supported platforms change |

## Go Version Policy

- Support the latest stable Go release used by CI and one previous release when dependencies allow it.
- Do not set a future or unavailable Go version in `go.mod` for released branches.
- Platform integration tests, not compilation alone, determine OS support.
- Keep third-party PTY and WebSocket libraries behind internal interfaces.

## Frontend Workspace

When mobile packaging and generated protocol packages arrive, convert `web` into a pnpm workspace without introducing a monorepo task framework until build orchestration requires it.

Target packages:

```text
apps/web
apps/mobile
packages/protocol-ts
packages/ui
packages/session-core
```

Shared session logic must remain independent of DOM and Capacitor APIs. Native and browser adapters implement storage, notifications, camera, and device-key operations.

## Control API Style

Control/discovery APIs should use Protobuf-derived contracts over standard HTTPS. Evaluate Connect RPC because it supports browser and Go clients while retaining Protobuf schemas. Use plain REST endpoints when they are simpler for operator integrations.

The real-time PTY protocol remains separate from account/control APIs so control-plane changes cannot destabilize terminal transport.

Reference: [Connect documentation](https://connectrpc.com/docs/).

## Cryptographic Implementation Selection

The architecture selects protocol properties, not an unaudited package by name.

Before choosing Go and mobile libraries:

- Verify active maintenance and supported platforms.
- Review constant-time and secure-storage behavior.
- Require compatible test-vector support.
- Confirm license compatibility.
- Build a replaceable internal interface.
- Complete an external design review before stable remote release.

## Database Introduction Gate

Do not add PostgreSQL, Redis, or SQLite preemptively.

- SQLite enters the Agent when durable device graph, migrations, and attachment/session metadata no longer fit clear versioned files.
- PostgreSQL enters the Control API with durable multi-device discovery or hosted revocation state.
- Redis enters only when horizontally scaled relays require shared ephemeral routing.

Every datastore requires backup, migration, retention, and self-hosting documentation.

## Performance Strategy

Baseline before optimization:

- Agent memory per idle and active session.
- Input-to-output latency direct and relayed.
- Terminal render throughput on representative phones.
- Replay-buffer memory and reconnect time.
- Attachment throughput, CPU, battery, and relay queue behavior.

Technology changes require before/after measurements and rollback plans. Microbenchmarks alone do not justify architectural migration.

## Dependency Exit Strategy

Critical dependencies have an explicit abstraction and exit plan:

- PTY library behind platform terminal interface.
- WebSocket library behind transport interface.
- Crypto library behind handshake/session interfaces plus test vectors.
- Secure storage behind platform key interface.
- Relay presence behind a small lease/routing interface.
- Update framework behind signed metadata verification interface.

This prevents library abandonment from forcing product-wide rewrites.
