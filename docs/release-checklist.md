# Release Checklist

This checklist applies to pre-release builds and becomes a hard gate for beta/stable channels as those channels are introduced.

## 1. Scope and compatibility

- [ ] Release version, channel, target commit, supported operating systems, and known limitations are explicit.
- [ ] User-visible changes and migrations are recorded in release notes.
- [ ] Protocol, local configuration, and stored-state compatibility ranges are documented when applicable.
- [ ] The project license has been selected and a root `LICENSE` file exists.

## 2. Source and dependency integrity

```powershell
git status --short
go mod verify
go vet ./...
go test ./...
pnpm --dir web install --frozen-lockfile
pnpm --dir web test
pnpm --dir web build
```

- [ ] The target commit is reviewed and the worktree is clean.
- [ ] CI passes on Windows and Linux.
- [ ] Dependency and license changes are reflected in [dependencies.md](dependencies.md).
- [ ] Vulnerability findings are resolved, accepted with rationale, or listed as release blockers.
- [ ] No secrets, private configuration, terminal content, source, or generated local artifacts are committed.

## 3. Product validation

- [ ] `go run ./cmd/vibebridge --diagnose` passes on the target Windows host.
- [ ] A durable embedded binary passes `service install`, `service status --qr`, sign-out/sign-in autostart, forced replacement, and `service uninstall` checks as a non-administrator Windows user.
- [ ] The complete real-device workflow in the README passes on supported phone/browser combinations.
- [ ] Explicit End, process exit, reconnect expiry, idle expiry, and Ctrl+C shutdown leave no PTY child process.
- [ ] A previous supported client/build still behaves according to the declared compatibility range.

## 4. Artifact validation

```powershell
pnpm --dir web build
go build -tags embed -trimpath -o bin/vibebridge.exe ./cmd/vibebridge
Get-FileHash -Algorithm SHA256 bin/vibebridge.exe
```

- [ ] The embedded binary starts without `web/dist` at runtime.
- [ ] Artifact name, size, SHA-256 checksum, Go version, and source commit are recorded.
- [ ] Malware/signing checks required for the selected channel pass.
- [ ] Installation and rollback are tested from a clean machine or VM.

## 5. Security and publication

- [ ] GitHub private vulnerability reporting is enabled and [SECURITY.md](../SECURITY.md) is current.
- [ ] Security-sensitive fixes have coordinated disclosure notes where required.
- [ ] Release notes repeat the trusted-private-network limitation until remote transport is supported.
- [ ] Checksums and compatibility/rollback notes are attached to the release.
- [ ] Stable releases are signed; an unsigned artifact must not be labeled stable.

## 6. Post-release

- [ ] Download and run the published artifact once.
- [ ] Verify checksums and links from the public release page.
- [ ] Monitor installation, startup, connection, and cleanup regressions.
- [ ] Record any rollback and open follow-up issues with owners.
