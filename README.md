# VibeBridge

VibeBridge maps a local PTY session to a phone browser. It is designed for controlling local AI CLI tools such as Codex or Claude Code from a mobile device without streaming the desktop screen.

Long-term product boundaries, lifecycle rules, and security invariants are documented in [docs/product-spec.md](docs/product-spec.md).

## Development

Build the frontend first:

```powershell
pnpm --dir web install
pnpm --dir web build
```

Run the Go server:

```powershell
go run ./cmd/vibebridge
```

The server prints local and LAN URLs plus a QR code. Scan the LAN URL from a phone on the same Wi-Fi.

`--addr` defaults to `0.0.0.0:8787` so a phone on the LAN can connect. This listens on every network interface, so only run it on a trusted private network. Do not expose VibeBridge directly to the public internet: it has no HTTPS/WSS termination or public-facing authentication.

## Common Options

```powershell
go run ./cmd/vibebridge --cmd "pwsh -NoLogo -NoExit -NoProfile"
go run ./cmd/vibebridge --cmd "powershell.exe -NoLogo -NoExit"
go run ./cmd/vibebridge --cmd "codex"
go run ./cmd/vibebridge --addr "0.0.0.0:8787"
go run ./cmd/vibebridge --reconnect-timeout 90s
go run ./cmd/vibebridge --idle-timeout 30m
go run ./cmd/vibebridge --diagnose
```

Set `--idle-timeout 0` to disable idle cleanup.

`--diagnose` checks the configured command, HTTP listen port, frontend build, private LAN addresses, and Windows Firewall guidance without starting a PTY or generating a session token.

## Connection and Security

- Each run creates a cryptographically random session token. WebSocket connections without that token are rejected.
- Browser WebSocket connections must be same-origin. Native clients without an `Origin` header remain supported.
- One browser controls a PTY at a time. A short disconnect keeps the session alive for `--reconnect-timeout`.
- The server sends WebSocket Ping control frames every four minutes. Browsers reply with Pong automatically, preventing idle connections from being closed by the five-minute read deadline.
- Sending `Ctrl+C` to the VibeBridge process or receiving `SIGTERM` closes the active PTY before the HTTP server exits.
- `GET /status?token=...` reports the session state, start time, last activity time, and configured timeouts without exposing terminal output or the configured command.

Terminal output is sent as WebSocket binary frames so ANSI sequences and raw PTY bytes are preserved. Structured input, resize, exit, error, and status messages use JSON text frames.

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
pnpm --dir web test
pnpm --dir web build
```

Before using a release, run this real-device check on the target Windows machine:

1. Start `go run ./cmd/vibebridge --cmd "codex"` and pair an Android or iOS browser over the trusted LAN.
2. Verify colored CLI output, Chinese input composition, `Send + Enter`, `Insert only`, `Esc`, `Ctrl+C`, Tab, arrows, and clipboard paste.
3. Rotate the device and open/close the soft keyboard; the terminal must resize without corrupting the CLI layout.
4. Test terminal search, copy selection, clear view, direct keyboard input, font-size changes, quick prompts, and recent history.
5. Lock the device or switch networks briefly, confirm the retry countdown, then reconnect before the configured timeout; the same PTY should resume.
6. Click End and confirm that the local `codex` process exits. Repeat by stopping VibeBridge with Ctrl+C.

## Single Binary Build

Generate the frontend build, then compile with the `embed` tag:

```powershell
pnpm --dir web build
go build -tags embed -o bin/vibebridge.exe ./cmd/vibebridge
```

The resulting binary contains the React frontend and does not require `web/dist` at runtime.

## Current Scope

- One active browser client at a time.
- PTY stays alive across short WebSocket disconnects.
- Explicit End closes the PTY session.
- Idle timeout cleans up abandoned sessions.
- Access is protected by a per-run session token in the QR URL.
