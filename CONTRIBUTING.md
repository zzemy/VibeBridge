# Contributing to VibeBridge

VibeBridge is pre-release security-sensitive software. Keep changes focused, reviewable, and backed by observable behavior.

## Before starting

- Read [docs/product-spec.md](docs/product-spec.md) for current behavior and [docs/roadmap.md](docs/roadmap.md) for sequencing.
- Discuss breaking protocol, trust-model, storage, deployment, or dependency changes in an issue before implementation.
- Report vulnerabilities through [SECURITY.md](SECURITY.md), never through a public issue.

## Local setup

Requirements:

- Go version declared in `go.mod`.
- Node.js 24.
- pnpm 11.5.2.
- Windows 11 for real ConPTY and phone-on-LAN validation.

```powershell
pnpm --dir web install --frozen-lockfile
pnpm --dir web proto:lint
pnpm --dir web build
go test ./...
go run ./cmd/vibebridge --diagnose
```

## Change workflow

1. Branch from the latest `main` with a short name such as `fix/process-cleanup`.
2. Add a regression test for defects and behavior-focused tests for new logic.
3. Keep protocol, configuration, security, and user-flow documentation synchronized.
4. Run the smallest relevant checks while developing, then the full checks below before opening a PR.
5. Use a focused Conventional Commit such as `fix(agent): terminate Windows process tree`.

## Required checks

```powershell
go mod verify
go vet ./...
go test ./...
pnpm --dir web proto:lint
pnpm --dir web proto:generate
git status --short -- gen/go web/src/gen # expected: no output
pnpm --dir web test
pnpm --dir web build
```

Generated protocol packages are committed and must match `proto/vibebridge/v1`. Update the shared binary vector only for an intentional wire-contract change by running `$env:VIBEBRIDGE_UPDATE_GOLDEN = "1"; go test ./internal/protocol; Remove-Item Env:\VIBEBRIDGE_UPDATE_GOLDEN`, then review the schema and fixture together. On Windows, `go test ./...` includes a real ConPTY process-tree cleanup regression. UI changes must also be checked on relevant portrait and landscape viewports. Changes to pairing, reconnect, terminal input, or cleanup require the real-device path in the README.

## Pull requests

Describe the user-visible result, reason, validation evidence, risk, and rollback. Keep generated output, tokens, logs, screenshots containing private data, and unrelated formatting out of commits. Maintainers may request smaller commits when a change combines unrelated concerns.
