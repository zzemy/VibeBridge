# VibeBridge Product Specification

## Purpose

VibeBridge exposes one local PTY session to a phone browser on a trusted private network. It is intended for controlling terminal-based AI development tools such as Codex and Claude Code without streaming the desktop.

The product optimizes for low-latency text interaction, reliable session recovery, and safe cleanup of a powerful local terminal session.

## Product Boundaries

VibeBridge provides:

- One local command running inside a PTY, selected from a validated local launch profile or the compatibility `--cmd` option.
- One active browser controller at a time.
- Terminal output, direct keyboard input, prompt composition, shortcuts, resize, reconnect, and explicit session termination.
- A per-run pairing token delivered through the printed URL and QR code.
- Versioned local configuration, canonical workspace registry, and structured launch profiles with explicit workspace, working-directory, and environment inheritance policy.
- Local diagnostics, privacy-safe structured lifecycle logs, and session status without exposing terminal contents.

VibeBridge does not provide:

- Desktop or screen sharing.
- Remote file browsing or editing.
- Multi-user collaboration.
- Public-internet authentication or transport security.
- Persistent server-side session history.

Public deployment is outside the current scope and requires HTTPS/WSS, durable authentication, stricter access policy, and an explicit threat model.

## System Model

The system has three participants:

1. The phone browser renders the terminal and collects user input.
2. The Go server owns the HTTP/WebSocket endpoints and PTY lifecycle.
3. The local CLI process performs the actual work on the host machine.

PTY output is sent to the browser as binary WebSocket frames so terminal bytes and ANSI sequences are preserved. When both peers negotiate `terminal.sequenced_io_v1`, terminal input/output and acknowledgements are protobuf envelopes with connection-local ordering. When they also negotiate `session.resume_v1`, `AttachSession` and `SessionStatus` bind each physical connection to a resumable PTY generation. Negotiated `terminal.resize_end_v1` adds ordered resize and explicit end controls, `session.process_exit_v1` adds an ordered final process outcome, `control.error_v1` adds ordered allowlisted application failures without exposing host errors, and `control.health_v1` adds ordered application Ping/Pong after resume-enabled session binding. Peers without those capabilities retain safe JSON adapters. Otherwise the staged legacy path uses raw binary output and JSON input.

## Session Lifecycle

A session moves through these observable states:

```text
idle -> connected -> detached -> connected
                  -> idle
connected -> ended -> idle
```

- `idle`: no PTY process exists.
- `connected`: a browser controls the active PTY.
- `detached`: the browser disconnected, but the PTY is retained during the reconnect window.
- `ended`: the process is exiting and its resources are being cleaned up.

Only one browser may attach at a time. A reconnect must reuse the existing PTY rather than starting a second process. Explicit End, idle expiry, reconnect expiry, server shutdown, and process exit must all release the PTY exactly once.

## Security Invariants

- WebSocket and status requests require the per-run session token.
- Browser WebSocket upgrades must be same-origin.
- The default listener is suitable only for a trusted private network.
- Terminal output, prompt contents, full tokens, commands, paths, environment values, client addresses, browser origins, and private configuration must not be written to server logs. Lifecycle logs use an allowlisted JSON schema and opaque random session correlation IDs.
- Status responses may expose lifecycle timestamps and timeout configuration, but not terminal contents or the configured command.
- A disconnected or abandoned session must be terminated after its configured timeout.
- While detached, only the newest 1 MiB and two minutes of terminal output are retained in memory for reconnect replay; discarding any older bytes makes replay incomplete and requires explicit client resynchronization.
- Public exposure must not be presented as supported without HTTPS/WSS and additional authentication.

## Interaction Principles

- The terminal remains the primary surface and must stay visible on short portrait and landscape viewports.
- Long mobile input uses an editable prompt composer; direct terminal input remains available for interactive CLI use.
- Risky operations such as ending the PTY require confirmation.
- Connection loss, retry timing, recovery, process exit, and disabled controls must be visible to the user.
- Drafts and recent prompts are browser-session data and must not become server-side history.
- Keyboard shortcuts, search, copy, clear, font sizing, and resize must not alter the underlying PTY contract.

## Protocol Contract

The browser offers WebSocket subprotocol `vibebridge.v1`. When selected, both peers exchange protobuf `Hello` envelopes before PTY creation. If both advertise `terminal.sequenced_io_v1`, binary protobuf envelopes carry `TerminalInput`, `TerminalOutput`, and `Acknowledgement` payloads with monotonically increasing connection-local sequence numbers. If both also advertise `terminal.resize_end_v1`, `TerminalResize` and `EndSession` use that ordered stream; columns and rows must be integers from 1 through 65,535. If both advertise `session.process_exit_v1`, the Agent sends an ordered `ProcessExit` whose outcome is `SUCCESS` or `FAILURE`; the capability requires `terminal.sequenced_io_v1`, and the payload never contains a raw host process error. If both advertise `control.error_v1`, the Agent sends ordered `Error` envelopes containing one known `ErrorCode`; the capability also requires `terminal.sequenced_io_v1`. If both advertise `attachment.transfer_v1`, it additionally requires `terminal.sequenced_io_v1` and `control.error_v1`; attachment transfer failures use the allowlisted `ATTACHMENT_TRANSFER_FAILED` code. If both also advertise `attachment.prompt_action_v1`, that capability requires sequenced I/O, attachment transfer, and stable errors. The browser prepares one reconnect-stable action from acknowledged transfer IDs without supplying host paths; the Agent resolves the staged relative paths and returns the exact terminal preview plus effective Enter behavior. The user must explicitly commit or cancel that preview. Reconnects retry prepare, commit, or cancel with the same action ID, and the Agent applies a committed action exactly once; failures use only `ATTACHMENT_PROMPT_ACTION_FAILED`. Codes cover session start failure, an already active controller, terminal input failure, terminal resize failure, attachment transfer failure, attachment prompt action failure, and unsupported transitional messages. The browser derives safe user-facing copy from each code, while peers without the capability receive fixed safe wire text through the JSON adapter. Protocol negotiation, framing, sequence, acknowledgement, payload, and session-metadata violations remain WebSocket protocol closes rather than application errors. If both advertise `session.resume_v1`, the client sends `AttachSession` as sequence 2 and normally waits for the Agent's sequence-2 `SessionStatus` before terminal traffic. A fatal startup or occupied-session `Error` may instead arrive at sequence 2 with empty session metadata; it does not bind the stream. A fresh request carries no session identity and cursor zero; a reconnect carries the previously assigned 16-byte session ID, positive generation, and the highest Agent sequence successfully processed on the previous physical connection. Hello and stream sequence state restart on every WebSocket; detached output is re-encoded from sequence 3 on the new connection.

A reconnect is `RESUMED` only when the identity and generation match the retained PTY, a prior detach checkpoint exists, the resume cursor exactly equals the Agent's previous highest outbound sequence, and the byte/time-bounded replay is complete. A newly created PTY returns `FRESH`. Every other attachment returns `RESYNC_REQUIRED`; the browser resets its terminal view, explains that history was truncated, and then renders any retained replay tail. Each newly created PTY receives a new random session ID and a monotonically increasing in-process generation, so stale identities cannot silently attach to a replacement. Older peers fall back to the staged legacy terminal adapter after Hello, and connections without the subprotocol remain fully legacy-compatible.

Legacy compatibility remains enabled by default. Operators can disable it through `disable_legacy_protocol` or `--disable-legacy-protocol`; the Agent then accepts only `vibebridge.v1` clients advertising the complete current terminal, sequencing, resize/end, process-exit, resume, error, and health capability set. Rejection occurs before Agent Hello and PTY/session creation, without changing lifecycle or PTY internals.

Transitional browser-to-server JSON controls:

- `input`: terminal input data, only on the legacy terminal path.
- `resize`: terminal columns and rows when `terminal.resize_end_v1` is not negotiated.
- `exit`: explicit PTY termination request when `terminal.resize_end_v1` is not negotiated.
- `ping`: application-level health check when `control.health_v1` is not negotiated.

Transitional server-to-browser messages:

- Binary frames: raw PTY output only on the legacy terminal path.
- `error`: fixed safe error text only when `control.error_v1` is not negotiated.
- `exit`: process exit state when `session.process_exit_v1` is not negotiated.
- `pong`: response to an application-level ping when `control.health_v1` is not negotiated.

When `control.health_v1` is negotiated, application Ping/Pong uses empty ordered protobuf envelopes and the Agent Pong acknowledges the client Ping. The server separately sends WebSocket Ping control frames to keep idle connections alive; transport keepalive does not replace or share semantics with the application health exchange.

The attachment transfer and trusted prompt-action capabilities remain dark: neither production peer advertises them, and the browser App feature flag remains disabled until the remaining attachment safety and release gates are complete.

## Acceptance Requirements

A release is acceptable when:

- Invalid tokens and cross-origin browser connections are rejected.
- PTY output, ANSI rendering, direct input, composed prompts, control keys, and resize work on the target Windows environment.
- Temporary disconnects resume the same PTY, while expired disconnects clean it up.
- Explicit End and server shutdown do not leave child processes behind.
- Repeated cleanup cannot close the same ConPTY handle twice.
- Desktop, short portrait, and short landscape layouts keep the terminal and required controls usable.
- Go tests, frontend tests, production frontend build, and embedded binary build pass.

Operational commands and the current manual device checklist live in the repository [README](../README.md).

The current local product is the foundation for the broader open-source V1 described in [product-v1-prd.md](product-v1-prd.md).
