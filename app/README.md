# VibeBridge Desktop App (Tauri 2.x)

Native desktop application wrapping the VibeBridge Go agent as a sidecar.

## Architecture

- **Frontend**: React + Vite + Tailwind (desktop dashboard UI)
- **Shell**: Tauri 2.x (Rust) — native window, system tray, sidecar management
- **Backend**: Go agent (unchanged) — launched as sidecar process

## Prerequisites

### Linux (WSL)
```bash
sudo apt install build-essential libwebkit2gtk-4.1-dev libssl-dev \
  libgtk-3-dev libayatana-appindicator3-dev librsvg2-dev
```

### Windows
- WebView2 runtime (pre-installed on Windows 10+)
- MSVC build tools

### Common
- Rust 1.75+ (`rustup`)
- Node.js 22+ / pnpm 11+
- Go 1.26+ (for building the agent sidecar binary)

## Development

```bash
# Install frontend deps
cd app && pnpm install

# Build Go agent sidecar binary (from repo root)
GOOS=linux go build -o app/src-tauri/binaries/vibebridge-agent ./cmd/vibebridge/

# Run Tauri dev mode
cd app && pnpm tauri dev
```

## Build

```bash
# Build Go agent for target platform
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -o app/src-tauri/binaries/vibebridge-agent-x86_64-pc-windows-msvc.exe ./cmd/vibebridge/

# Build Tauri app
cd app && pnpm tauri build
```

## Sidecar Binary Naming

Tauri requires sidecar binaries to include the target triple suffix:
- Linux: `vibebridge-agent-x86_64-unknown-linux-gnu`
- Windows: `vibebridge-agent-x86_64-pc-windows-msvc.exe`
- macOS: `vibebridge-agent-aarch64-apple-darwin` / `vibebridge-agent-x86_64-apple-darwin`

See `tauri.conf.json` → `bundle.externalBin`.
