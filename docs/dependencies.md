# Dependency Inventory and License Review

Review date: 2026-07-12

This inventory covers dependencies reachable from the current Go executable and production dependencies installed for the web client. Exact versions are authoritative in `go.mod`, `go.sum`, `web/package.json`, and `web/pnpm-lock.yaml`.

## Review commands

```powershell
go list -deps -f '{{with .Module}}{{if not .Main}}{{.Path}}|{{.Version}}{{end}}{{end}}' ./cmd/vibebridge | Sort-Object -Unique
pnpm --dir web licenses list --prod --json
go mod verify
go run golang.org/x/vuln/cmd/govulncheck@v1.6.0 ./...
pnpm --dir web install --frozen-lockfile
pnpm --dir web audit --prod --audit-level high
```

License names were checked from module/package metadata and bundled license files. New or upgraded dependencies must repeat the review and evaluate maintenance status, security advisories, binary size, platform support, and replacement cost.

## Go executable

| Module | Version | Role | License |
| --- | --- | --- | --- |
| `github.com/aymanbagabas/go-pty` | `v0.2.3` | Cross-platform PTY/ConPTY adapter | MIT |
| `github.com/creack/pty` | `v1.1.24` | Unix PTY backend used by `go-pty` | MIT |
| `github.com/gorilla/websocket` | `v1.5.3` | WebSocket transport | BSD-2-Clause |
| `github.com/mdp/qrterminal/v3` | `v3.2.1` | Terminal QR rendering | MIT |
| `github.com/u-root/u-root` | `v0.16.0` | Unix process/PTY support used by `go-pty` | BSD-3-Clause |
| `golang.org/x/crypto` | `v0.51.0` | Transitive SSH/terminal support | BSD-3-Clause |
| `golang.org/x/sys` | `v0.44.0` | Windows Job Objects and platform syscalls | BSD-3-Clause |
| `golang.org/x/term` | `v0.43.0` | Transitive terminal support | BSD-3-Clause |
| `rsc.io/qr` | `v0.2.0` | Transitive QR encoding | BSD-3-Clause |

`github.com/creack/pty` and `github.com/u-root/u-root` are reachable only in Unix builds; Windows builds use the ConPTY implementation and `golang.org/x/sys/windows`. The table is the union of Windows and Linux `go list -deps` results.

## Web production dependencies

| Package | Role | License |
| --- | --- | --- |
| `react`, `react-dom` | UI runtime | MIT |
| `@xterm/xterm`, `@xterm/addon-fit`, `@xterm/addon-search` | Terminal rendering and controls | MIT |
| `@radix-ui/react-alert-dialog` | Accessible end-session dialog | MIT |
| `lucide-react` | Icons | ISC |
| `class-variance-authority` | Component variants | Apache-2.0 |
| `clsx`, `tailwind-merge` | Class composition | MIT |

The production web dependency closure currently reports only MIT, ISC, Apache-2.0, and 0BSD license families. Build/test-only dependencies additionally include BSD-3-Clause, CC0-1.0, BlueOak-1.0.0, and MPL-2.0 packages; the MPL-2.0 Lightning CSS tooling is not shipped as source in the generated web bundle.

## Vulnerability review

On 2026-07-12, `govulncheck` reported zero reachable vulnerabilities (with findings present only in imported but uncalled code), and `pnpm audit --prod` reported no known vulnerabilities. These results are point-in-time evidence, not a substitute for scanning each release and dependency update.

## Review result

- No dependency with a known strong-copyleft license is reachable in the current distributed Go executable or web runtime.
- Current dependency licenses are permissive or file-level weak-copyleft build tooling and present no identified blocker for normal binary/web distribution.
- PTY and Windows process cleanup remain high-risk dependencies and require real ConPTY regression coverage on Windows CI.
- The repository itself has no root `LICENSE` yet. The project owner must select the project license before a release or external contribution can be represented as licensed open-source distribution; this review does not choose that policy.
