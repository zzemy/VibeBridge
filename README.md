# VibeBridge

VibeBridge maps a local PTY session to a phone browser. It is designed for controlling local AI CLI tools such as Codex or Claude Code from a mobile device without streaming the desktop screen.

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

## Common Options

```powershell
go run ./cmd/vibebridge --cmd "pwsh -NoLogo -NoExit -NoProfile"
go run ./cmd/vibebridge --cmd "powershell.exe -NoLogo -NoExit"
go run ./cmd/vibebridge --cmd "codex"
go run ./cmd/vibebridge --addr "0.0.0.0:8787"
go run ./cmd/vibebridge --reconnect-timeout 90s
go run ./cmd/vibebridge --idle-timeout 30m
```

Set `--idle-timeout 0` to disable idle cleanup.

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
