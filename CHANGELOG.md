# Changelog

All notable changes to VibeBridge are documented in this file.

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and the project adheres to [Semantic Versioning](https://semver.org/).

## [1.0.0] — 2026-07-31

Initial public release.

### Added

- **Terminal access from mobile browser.** Real-time terminal over WebSocket
  with PTY allocation, resize support, and binary output. Works over the local
  network without any cloud relay.
- **End-to-end encrypted device pairing.** Noise Protocol handshake with
  Ed25519 device identities. Pairing invitations are short-lived,
  single-use, and bound to the issuing agent.
- **Protocol V1 with capability negotiation.** Client and agent exchange Hello
  envelopes listing required and supported capabilities. Incompatible clients
  are rejected with a clear error.
- **Attachment transfer.** Bidirectional file transfer with per-session and
  cross-session aggregate byte limits, sandbox staging for no-workspace mode,
  and crash-recovery cleanup of orphaned staging directories. Adapters for
  generic, Codex, and Claude tool integrations.
- **Self-hosted relay.** Community-deployable relay server for remote access
  without port forwarding. Relay tickets carry an issuer epoch for revocation
  checking; the gate is on by default with automatic identity-store discovery.
- **Desktop Tauri application (Windows).** Native window with sidebar
  navigation, system tray, and the same four-panel layout (terminal, files,
  settings, info) as the web client. NSIS per-user installer with silent
  install/uninstall and HKCU Run key for automatic startup.
- **Four-tab mobile shell.** Bottom tab-bar navigation (Terminal, Files,
  Settings, Info) with safe-area insets and no horizontal scroll.
- **Bilingual user interface.** Chinese and English with automatic language
  detection (`navigator.language`) and manual switch persisted to
  `localStorage`.
- **Pairing auto-redirect.** After successful device pairing, the browser
  automatically enters the terminal instead of showing a static success page.
- **PWA support.** Installable web app with service worker, install prompt,
  and update banner for offline-capable mobile use.
- **Device identity store.** Ed25519 key persistence with revocation epochs.
  Lost-phone recovery via local CLI bootstrap (`vibebridge recover --list`,
  `--revoke-all`, `--authorize-new`).
- **Paired session authentication (ADR-0006).** WebSocket upgrade requires
  device signature over a server-issued single-use nonce. Legacy URL token
  path removed; escape hatch removed from user-facing CLI.
- **Relay revocation gate (ADR-0006).** Relay tickets embed the issuer's
  revocation epoch; revoked devices are rejected at relay time.
- **Reconnect jitter.** Exponential backoff with jitter for WebSocket
  reconnection to prevent thundering-herd reconnects.
- **Subprotocol echo and transport selection.** Protocol negotiation echo and
  automatic transport selection for relay connections.
- **Message-aware WebSocket bridge.** Relay bridge that inspects message
  boundaries instead of raw byte streaming.
- **Crash recovery and sandbox staging.** Startup cleanup of stale staging
  directories; sandbox staging in `os.TempDir()` when no workspace is
  configured.

### Security

- Paired session gate: Ed25519-signed nonce, 30-second TTL, single-use,
  host-only binding, 4096-entry cap. Default ON.
- Relay revocation gate: epoch-embedded tickets, default ON with automatic
  identity-store discovery (`deviceidentity.DefaultPath()`).
- Legacy URL token removed from WebSocket upgrade path.
- Escape hatch (`--require-paired-session`, `--disable-legacy-protocol`)
  removed from user-facing CLI and config; `server.Config` fields retained for
  testing only.
- Lost-phone recovery (ADR-0007): local CLI bootstrap bypasses WebSocket auth;
  trust root is local filesystem access.

### Dependencies

- Go 1.26.5, golang.org/x/crypto v0.52.0
- React 19, TypeScript 7.0.2, Vite 8.1.4, Tailwind CSS 4
- Tauri 2.x, Rust stable
- xterm.js 6, buf 1.71.0 (proto codegen)

### Known Limitations

- Native mobile clients (iOS/Android) are not yet available; mobile use relies
  on the installable PWA.
- macOS and Linux desktop builds are not yet provided.
- Independent cryptographic review of the paired-session and relay revocation
  gates is pending.
- Community relay public testing has not been conducted.
