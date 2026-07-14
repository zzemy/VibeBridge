# VibeBridge

[![CI](https://github.com/zzemy/VibeBridge/actions/workflows/ci.yml/badge.svg)](https://github.com/zzemy/VibeBridge/actions/workflows/ci.yml)
[![License](https://img.shields.io/badge/license-Apache--2.0-blue.svg)](LICENSE)

Control local AI CLI sessions such as Codex and Claude Code from your phone over a trusted LAN. VibeBridge connects a mobile browser to a local PTY without streaming your desktop or sending terminal content through a hosted service.

> [!WARNING]
> VibeBridge is early-stage software for trusted private networks. It does not provide HTTPS/WSS termination or public-facing authentication. Do not expose it directly to the public internet.

## What Works Today

- Mobile terminal access with ANSI and raw PTY output preserved over WebSocket binary frames.
- Prompt composition, direct keyboard input, clipboard paste, search, reconnect feedback, and portrait/landscape layouts.
- Short-disconnect resume, byte/time-bounded replay, idle cleanup, and idempotent PTY process-tree termination.
- Versioned workspace roots and launch profiles, privacy-safe lifecycle logs, and local diagnostics.
- A least-privilege, user-scoped Windows background Agent installed through Task Scheduler.
- Canonical Protocol V1 Protobuf schemas, generated Go/TypeScript packages, golden vectors, and compatibility CI.

The browser endpoint now negotiates Protocol V1. When both peers support sequenced I/O and `session.resume_v1`, terminal traffic, acknowledgements, session attachment, and bounded reconnect replay use ordered Protobuf envelopes with explicit `FRESH`, `RESUMED`, or `RESYNC_REQUIRED` results. Peers that also negotiate `terminal.resize_end_v1` carry terminal resize and explicit end controls in the same ordered stream, `session.process_exit_v1` reports a safe final process outcome, `control.error_v1` reports allowlisted application failures without exposing host errors, and `control.health_v1` sequences application Ping/Pong independently of WebSocket transport keepalive. Older peers retain safe JSON adapters during this staged transition.

## Platform Status

| Capability | Windows 11 | macOS / Linux |
| --- | --- | --- |
| Foreground local Agent and PTY | Tested | Implementation available; release validation incomplete |
| User-scoped background installation | Supported through Task Scheduler | Not yet supported |
| Public-internet relay | Not available | Not available |

No packaged release is published yet; build the project from source. See the [roadmap](docs/roadmap.md) for release gates and planned remote-access work.

## Quick Start

Requirements: Go and the pnpm version declared in [`web/package.json`](web/package.json).

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web build
go run ./cmd/vibebridge --cmd "codex"
```

The Agent prints local and LAN URLs plus a QR code. Scan the LAN URL from a phone connected to the same trusted Wi-Fi network. The default listener is `0.0.0.0:8787`, which binds every network interface.

For a different shell or CLI, replace `codex` with the executable you want to run. Use `go run ./cmd/vibebridge --diagnose` to check the host, listener, frontend build, configuration, and firewall guidance without starting a PTY.

Product, architecture, security, ADR, roadmap, dependency, and release documentation is organized in [docs/index.md](docs/index.md). See [CONTRIBUTING.md](CONTRIBUTING.md) before submitting changes and [SECURITY.md](SECURITY.md) for vulnerability reporting.

## Development

Protocol V1 schemas are canonical under `proto/vibebridge/v1`; generated Go and TypeScript files are committed but never edited manually. Regenerate them with the pinned project-local Buf tool:

```powershell
pnpm --dir web proto:lint
pnpm --dir web proto:generate
```

## Common Options

```powershell
go run ./cmd/vibebridge --cmd "pwsh -NoLogo -NoExit -NoProfile"
go run ./cmd/vibebridge --cmd "powershell.exe -NoLogo -NoExit"
go run ./cmd/vibebridge --cmd "codex"
go run ./cmd/vibebridge --addr "0.0.0.0:8787"
go run ./cmd/vibebridge --reconnect-timeout 90s
go run ./cmd/vibebridge --idle-timeout 30m
go run ./cmd/vibebridge --disable-legacy-protocol
go run ./cmd/vibebridge --diagnose
```

Set `--idle-timeout 0` to disable idle cleanup.

`--diagnose` reports the host PTY support status, user-scoped background Agent installation, configured command or launch profile, selected workspace/working directory, HTTP listen port, frontend build, listener exposure, private LAN addresses, and platform-appropriate firewall guidance without starting a PTY or generating a session token. It runs all independent checks before returning a failure summary, so one run can reveal multiple configuration problems.

## Local Configuration and Launch Profiles

Copy [`config.example.json`](config.example.json) and start a configured profile:

```powershell
Copy-Item config.example.json config.local.json
go run ./cmd/vibebridge --config config.local.json
go run ./cmd/vibebridge --config config.local.json --profile codex
go run ./cmd/vibebridge --config config.local.json --profile claude --diagnose
```

The configuration format is explicitly versioned. Version 1 remains supported for listener/static-asset settings, reconnect and idle durations, the optional `disable_legacy_protocol` migration gate, a default profile, and structured launch profiles. Version 2 adds the local workspace registry and `workspace_id` profile binding. Each workspace declares a stable lowercase ID, display label, and existing directory root. Relative roots are resolved from the configuration file, symlinks and Windows junctions are resolved to a canonical absolute root, and duplicate canonical roots are rejected.

A profile may bind to a configured workspace with `workspace_id`. Its empty `working_directory` defaults to that root; a relative value is resolved from the workspace, and an absolute value is accepted only when its canonical directory remains inside the workspace. Profiles without `workspace_id` retain the previous behavior, including resolving relative working directories from the configuration file. Arguments are passed directly to the executable without shell interpolation. The optional local `tool_adapter` defaults to `generic`; it is reserved for Agent-generated attachment prompt actions, and unknown adapters are rejected before launch. Profile sessions inherit only environment variables named in `environment_allowlist`; missing variables are omitted. Include variables required by the selected tool, such as `PATH`, `PATHEXT`, `SYSTEMROOT`, `TEMP`, `TMP`, and `USERPROFILE` on Windows.

Each workspace-bound PTY session reserves `.vibebridge/uploads/<session-id>/` for temporary attachment staging and removes that session directory after the PTY ends or fails to launch when cleanup succeeds; a cleanup failure retains the boundary and emits a privacy-safe diagnostic event. Physical names come from an Agent-generated opaque ID, and link escapes are rejected. Protocol V1 includes acknowledged begin/chunk/complete/cancel/discard transfer envelopes behind `attachment.transfer_v1` and trusted prepare/preview/commit/cancel prompt actions behind `attachment.prompt_action_v1`. Only acknowledged completed transfer IDs enter a prompt action; the Agent resolves their relative paths, uses the profile-selected generic adapter to generate exact terminal text, and returns that preview plus the effective `append_enter` choice for explicit user confirmation. The browser retains one session-local action ID across reconnect retries, while the Agent makes commit exactly once and cancel leaves staged files available. Failures use the stable `ATTACHMENT_TRANSFER_FAILED` or `ATTACHMENT_PROMPT_ACTION_FAILED` code without exposing host paths. If a physical connection closes before an operation acknowledgement arrives, the browser rebinds to the same PTY and queries the session-local transfer outcome (`UNKNOWN`, `ACTIVE` with its committed offset, `COMPLETED`, or `CANCELLED`) before resolving or safely replaying that operation; responses never include attachment metadata or paths. If any file in one selection fails or the user aborts, the browser discards all started IDs, including completed files. A lost discard acknowledgement is reconciled ID-by-ID and the whole idempotent discard is replayed if any file remains; cleanup failure preserves the transfer error and leaves session-end removal as the explicit fallback. This remains a dark flow: the production Agent Hello and browser Client Hello do not advertise either attachment capability, and the App feature flag is off. Aggregate limits, crash recovery, no-workspace staging, reviewed Codex/Claude adapters, and end-to-end/real-device validation remain incomplete. Add `/.vibebridge/` to every registered repository's ignore rules; this repository already includes it.

Unknown fields, unsupported versions, workspace fields in a Version 1 file, duplicate profile/workspace IDs, duplicate canonical workspace roots, missing or non-directory workspace roots, workspace boundary escapes, invalid durations, missing default profiles, invalid environment names, and configuration files larger than 1 MiB are rejected. New Agents accept both Version 1 and Version 2 files. To roll back to an older Agent that predates workspace support, retain or restore a Version 1 file without `workspaces` or `workspace_id`. Command-line `--addr`, `--web-dir`, `--reconnect-timeout`, `--idle-timeout`, and explicit `--disable-legacy-protocol=true|false` values override configured values. An explicit `--cmd` preserves the legacy flow and overrides the configured default profile; `--cmd` and `--profile` cannot be combined.

Legacy compatibility is enabled by default. Set `disable_legacy_protocol` to `true` or pass `--disable-legacy-protocol` only after all deployed clients support the complete current Protocol V1 capability set. In that mode, clients without the `vibebridge.v1` WebSocket subprotocol receive HTTP `426` and V1 clients missing a required current capability receive WebSocket close `1002`; both checks happen before PTY/session creation.

## Windows Background Agent

Windows can install the built VibeBridge executable as a hidden, user-scoped Task Scheduler task. The task uses the current user's interactive token with `LeastPrivilege`, starts at sign-in, prevents duplicate instances, and retries a failed process up to three times. It is not a privileged system service. macOS and Linux background installation remain explicitly unsupported until their packaging gates are complete.

First build or download a durable executable and keep both that executable and its configuration at stable paths. `go run` output is temporary and is rejected by the installer.

```powershell
pnpm --dir web build
go build -tags embed -trimpath -o bin/vibebridge.exe ./cmd/vibebridge
Copy-Item config.example.json config.local.json

$agent = (Resolve-Path .\bin\vibebridge.exe).Path
$config = (Resolve-Path .\config.local.json).Path
& $agent service install --config $config
& $agent service status --qr
```

Use `--profile <id>` during installation to select a non-default launch profile. Replacing an existing task requires an explicit `--force`; the old instance is stopped before the new definition starts.

```powershell
& $agent service install --config $config --profile codex --force
& $agent service status
& $agent service uninstall
```

The installer starts the Agent immediately and at future sign-ins. `service status` probes the local authenticated status endpoint, distinguishes installed/stopped/stale/running states, and prints the current connection URLs only when the Agent responds. `service uninstall` stops and removes the task but does not delete the executable or configuration.

The background Agent stores its current PID, start time, listener, and random per-run token in `%LOCALAPPDATA%\VibeBridge\runtime.json`. The file is written atomically under the current user's local application-data directory and removed on graceful shutdown. It is sensitive runtime state: do not copy, publish, or commit it. Structured lifecycle logs still exclude the token, command, paths, terminal content, and environment values.

## Connection and Security

- Each run creates a cryptographically random session token. WebSocket connections without that token are rejected.
- Browser WebSocket connections must be same-origin. Native clients without an `Origin` header remain supported.
- One browser controls a PTY at a time. A short disconnect keeps the session alive for `--reconnect-timeout`.
- The server sends WebSocket Ping control frames every four minutes. Browsers reply with Pong automatically, preventing idle connections from being closed by the five-minute read deadline.
- Sending `Ctrl+C` to the VibeBridge process or receiving `SIGTERM` closes the active PTY before the HTTP server exits.
- `GET /status?token=...` reports the session state, start time, last activity time, and configured timeouts without exposing terminal output or the configured command.

Terminal bytes and ANSI sequences are preserved in WebSocket binary frames. Negotiated Protocol V1 peers carry terminal input, terminal output, acknowledgements, `AttachSession`, and `SessionStatus` in binary Protobuf envelopes. Every physical WebSocket starts new sequence state; a reconnect supplies the prior session identity, generation, and highest processed Agent sequence. If the exact checkpoint or complete bounded replay is unavailable, the Agent returns `RESYNC_REQUIRED` and the browser clears stale terminal history before showing the retained tail. Peers that negotiate `terminal.resize_end_v1` also send `TerminalResize` and `EndSession` as ordered Protobuf envelopes; dimensions must be integers from 1 through 65,535. Without that capability, resize and explicit end retain their JSON adapter. Peers that negotiate `session.process_exit_v1` receive an ordered `ProcessExit` with only `SUCCESS` or `FAILURE`; the host process error is never sent in that payload. Peers that negotiate `control.error_v1` receive ordered `Error` envelopes containing only an allowlisted code; the browser derives safe user-facing copy from each code. A startup or occupied-session error may arrive before `SessionStatus` with empty session metadata, and it does not bind the resumable stream. Peers that negotiate `control.health_v1` exchange ordered empty `Ping` and `Pong` envelopes after resume-enabled session binding, while non-resumable ordered streams may use them after Hello; the Agent's Pong acknowledgement covers the client Ping. Peers without these capabilities retain safe JSON adapters. Protocol framing, negotiation, sequence, and metadata failures close with WebSocket code `1002`; negotiated controls must not fall back to JSON. Legacy peers continue to use raw binary output and JSON input.

## Privacy-Safe Lifecycle Logs

The Agent writes JSON lifecycle events to stderr for local diagnostics. The logging schema is allowlisted to `event`, an opaque random `session_id`, `state`, `reason`, and `outcome`; standard timestamp, level, and message fields are added by Go's structured logger. Empty fields are omitted.

Events cover Agent startup/shutdown; session start, attach, detach, ending, and completion; and privacy-safe session cleanup failure. Logs deliberately exclude the session token, command and arguments, terminal or prompt content, paths, environment values, client addresses, and browser origins. Process failures are represented only by the safe `failure` outcome; the raw process error is not added to structured events.

## Mobile Input

- The prompt editor stores its draft in `sessionStorage`, scoped to the active pairing URL. The draft survives refreshes and backgrounding in the same browser tab, and is removed when the tab session ends.
- `Send + Enter` writes the prompt and confirms it. `Insert only` writes the prompt without appending Enter.
- Prompts and clipboard pastes are limited to 8,000 characters. The UI reports when the limit is reached or clipboard access is denied.
- Ending a session requires confirmation because it terminates the local AI CLI and PTY.

## Terminal Workbench

- Tap the terminal or use the keyboard button to send direct keyboard input to the PTY. The prompt composer remains the safer option for long mobile input.
- The terminal toolbar supports search, copy selection, clear view, keyboard focus, and persistent font-size controls.
- A visible three-second reconnect countdown explains when the browser will retry. Successful recovery is written into the terminal and shown as operation feedback.
- Quick prompts cover review, testing, blocker analysis, and progress summaries. The eight most recent submitted prompts are available for reuse within the current browser-tab session.
- The header shows idle/connected/detached/ended state and session runtime. Recent activity and manual Retry controls make recovery paths visible.
- Short portrait viewports keep the command controls visible. Short landscape viewports switch to a terminal-left, controls-right layout so the terminal cannot collapse out of view.

## Verification

```powershell
go test ./...
pnpm --dir web proto:lint
pnpm --dir web proto:generate
git status --short -- gen/go web/src/gen # expected: no output
pnpm --dir web test
pnpm --dir web build
```

Before using a release, run this real-device check on the target Windows machine:

1. Start `go run ./cmd/vibebridge --cmd "codex"` and pair an Android or iOS browser over the trusted LAN.
2. Verify colored CLI output, Chinese input composition, `Send + Enter`, `Insert only`, `Esc`, `Ctrl+C`, Tab, arrows, and clipboard paste.
3. Rotate the device and open/close the soft keyboard; the terminal must resize without corrupting the CLI layout.
4. Test terminal search, copy selection, clear view, direct keyboard input, font-size changes, quick prompts, and recent history.
5. Lock the device or switch networks briefly, confirm the retry countdown, then reconnect before the configured timeout; the same PTY identity and generation should resume without duplicated output. If the 1 MiB/two-minute replay bounds are exceeded, confirm the browser clears stale history and explains that terminal history was truncated.
6. In an intentionally attachment-enabled validation build, select at least two files, cancel or interrupt the later transfer, and confirm the whole started batch (including an earlier completed file) disappears from `.vibebridge/uploads/<session-id>/`. Repeat while briefly interrupting the network during discard acknowledgement; same-PTY reconnect must reconcile or replay cleanup without exposing a path, while a new PTY must report session-end cleanup as the fallback.
7. Click End and confirm that the local `codex` process exits. Repeat by stopping VibeBridge with Ctrl+C.

## Single Binary Build

Generate the frontend build, then compile with the `embed` tag:

```powershell
pnpm --dir web build
go build -tags embed -o bin/vibebridge.exe ./cmd/vibebridge
```

The resulting binary contains the React frontend and does not require `web/dist` at runtime.

## Current Limitations

- One browser client can control a session at a time.
- Access uses a per-run token in the QR URL, but transport is not encrypted by VibeBridge itself.
- The browser does not yet schedule Protocol V1 application health probes.
- Public relay, native mobile clients, file attachments, and packaged releases are roadmap work, not current features.

## License

VibeBridge is licensed under the [Apache License 2.0](LICENSE).
