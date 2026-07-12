# Local Agent Design

## Role

The Local Agent is the trusted endpoint on the developer's computer. It owns local execution and converts authenticated product actions into PTY, workspace, attachment, and lifecycle operations.

## Process Model

- User-scoped background service by default.
- One Agent process owns multiple logical sessions.
- Each session owns exactly one PTY process tree.
- Platform adapter controls process creation, signals, job/process groups, resize, and cleanup.
- Agent survives client disconnects and relay restarts.

System-wide privileged service installation is not required for V1.

## Internal Modules

```text
agent/
  app             composition and lifecycle
  identity        device keys, pairing, revocation
  transport       direct and relay connections
  protocol        generated types and session routing
  session         state machine and replay
  pty             platform-neutral contract
  platform        Windows/macOS/Linux adapters
  workspace       validated roots and launch profiles
  attachment      staging, quota, checksum, cleanup
  update          signed release checks and rollback
  diagnostics     privacy-safe status and support bundle
```

Dependencies point inward toward small domain interfaces. PTY libraries, key stores, relay clients, and update systems remain adapters.

## Session State Machine

States:

- `starting`
- `connected`
- `detached`
- `ending`
- `ended`
- `failed`

Transitions are serialized by a session actor or explicit mutex-protected state machine. Timers submit events rather than directly closing shared resources.

Cleanup uses one idempotent path for explicit end, process exit, reconnect expiry, idle expiry, update shutdown, and Agent shutdown.

## PTY Abstraction

The Agent defines its own minimal interface rather than leaking a third-party PTY type across modules:

```go
type Terminal interface {
    Read([]byte) (int, error)
    Write([]byte) (int, error)
    Resize(cols, rows int) error
    Signal(Signal) error
    Wait() ExitResult
    Close() error
}
```

Platform adapters must define process-tree semantics. Windows uses ConPTY plus Job Objects where supported; Unix uses process groups and PTY primitives. Dependency replacement remains possible because session code depends on the internal interface.

## Workspaces and Launch Profiles

- Workspace paths are explicit and canonicalized.
- Launch profiles define executable, argument templates, working-directory policy, environment allowlist, and tool adapter.
- No remote client supplies an unchecked executable path or environment.
- User-created profiles are stored locally and validated before launch.
- Sensitive environment values remain local and are never returned to clients.

## Local Storage

Recommended data layout:

```text
config/
  settings
  launch-profiles
state/
  device-descriptors
  revocations
  session-metadata
cache/
  update-downloads
workspace/.vibebridge/uploads/<session-id>/
```

Use SQLite only when structured migrations, concurrent reads, or durable device/session metadata justify it. Simple config remains in explicit versioned files. Root key material is stored through OS secure storage, not directly in SQLite.

## Replay Buffer

- Bound by total bytes and age, not chunk count.
- Stores raw output only in Agent memory by default.
- Tracks sequence ranges and truncation.
- Does not persist terminal transcripts across Agent restart in V1.
- Provides a clear resync response when requested output has expired.

## Diagnostics

The current local-only `--diagnose` preflight checks the host PTY support status, launch executable, working directory, HTTP listener, frontend assets, LAN exposure, and platform firewall guidance. It reports all independent failures in one run without creating a session token or starting a PTY.

As later phases add identity, workspaces, updates, and relay transport, diagnostics expand with those boundaries. The target command form is:

`vibebridge doctor` should check:

- Supported OS and PTY capability.
- Launch-profile executable resolution.
- Port and relay reachability.
- Secure-storage availability.
- Protocol and update compatibility.
- Workspace and staging-directory permissions.
- Firewall guidance.

Support bundles contain versions, opaque error codes, state transitions, and sanitized network diagnostics. Terminal and prompt content are excluded.

## Updates

- Check signed metadata over HTTPS.
- Download to a versioned staging location.
- Verify hash, length, signature, platform, and rollback version.
- Refuse new sessions during final switch, but do not kill active sessions without user consent except critical security policy.
- Replace atomically and preserve the previous binary.
- Health-check the new process and roll back on failure.

## Platform Roadmap

- Windows is the reference platform until ConPTY and process-tree tests are stable.
- macOS follows with Unix PTY/process-group adapter tests.
- Linux follows with distribution packaging and desktop secure-storage variance documented.
- Platform support is declared by tested versions, not assumed from compilation success.

## Testing

- State-machine unit tests with deterministic clocks.
- Fake PTY tests for cleanup, output, resize, and failure ordering.
- Platform integration tests that inspect child-process cleanup.
- Long-duration reconnect and idle tests.
- Upgrade rollback tests.
- Fuzz tests for local configuration and protocol inputs.
