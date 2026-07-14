# Attachment Design

## Goal

Move a phone-selected image or file into the local workspace safely, then make it available to the active AI CLI without turning VibeBridge into a remote file manager.

## Supported V1 Flow

1. Client selects, captures, or receives a shared file.
2. Client reads metadata and computes a checksum while streaming.
3. User reviews preview, size, type, target Agent, workspace, and session.
4. Client sends `AttachmentBegin` over the E2EE session.
5. Agent validates policy and reserves quota.
6. Client sends bounded encrypted chunks with offset and checksum state.
7. Agent writes to a temporary file in the session staging directory.
8. Agent verifies final size and checksum, then atomically publishes the generated final name without replacing an existing entry.
9. Client asks the selected tool adapter to reference the staged relative path.
10. Cleanup policy removes the file at session end or explicit user action.

## Storage Boundary

Default location:

```text
<workspace>/.vibebridge/uploads/<session-id>/<generated-id>.<safe-ext>
```

- `.vibebridge/` is added to Git ignore guidance.
- The Agent generates the physical name as a 128-bit lowercase hexadecimal identifier plus a policy-allowlisted suffix.
- Original name is display metadata and never used directly for path construction.
- Workspace roots come only from the validated local registry; the remote client selects an advertised opaque ID and never supplies a filesystem root.
- Canonical resolved paths must remain under the session staging root. Registry validation at startup does not replace containment and no-follow checks before every attachment file operation.
- The Agent binds file operations to an `os.Root` directory handle, revalidates the workspace, staging path, and directory identity before create, chunk write, rename, and removal, and fails closed if the boundary moves or is replaced.
- Creates are exclusive, publication uses a no-replace hard link before removing the partial name, and remove is idempotent. The Agent probes hard-link support before accepting transfers for a staging filesystem and fails closed rather than falling back to an overwriting rename. Session cleanup refuses to run while a directory handle is open and remains retryable after the owner closes it.
- Partial files use a non-executable temporary suffix.
- These checks constrain remote path input and detect replacement before each operation. As documented in the threat model, they do not protect a fully compromised endpoint or a same-user local process that races filesystem syscalls.
- No archive extraction in V1.

When no workspace is selected, the Agent uses an OS-local application data directory and tells the tool adapter the permitted path. Sandbox access must be checked before the transfer begins.

## Types

Initial product allowlist:

- PNG, JPEG, WebP, and GIF images.
- Plain text, logs, Markdown, JSON, YAML, TOML, CSV.
- PDF.

Office documents and archives require separate preview and security decisions. Executables, scripts, installers, and disk images are not automatically referenced by V1 attachment flows.

Content type is detected from bytes and compared with the declared type and extension.

## Limits

Default safety limits:

- 25 MB per file.
- 100 MB temporary attachment data per session.
- 10 files per prompt action.
- 256 KiB maximum protocol chunk before encryption.
- Bounded concurrent transfers per device and Agent.

Self-hosted operators may lower limits. Raising them requires local disk and relay policy checks.

## Resume

V1 may restart small failed transfers from zero. Resumable transfers are added when real usage justifies complexity.

The protocol already includes transfer identifier, byte offset, chunk hash, and total hash so resume can be added compatibly. A resume implementation must reject mixed file generations and stale partial data.

## Tool Adapters

Generic fallback:

- Insert a relative local path and explicit instruction into the prompt composer.

Codex adapter:

- Use documented image/file reference behavior supported by the active Codex surface.
- Never assume a command-line flag can modify an already-running session.

Claude adapter:

- Use only documented CLI attachment behavior or the generic path fallback.

Adapters return a preview of the exact prompt/control action before sending.

## Preview Security

- Images decode using browser/native image decoders with size limits.
- Text previews cap bytes and escape content.
- PDF previews use a sandboxed viewer or no inline preview initially.
- HTML and SVG are not rendered as active content.
- Attachment metadata is not included in push notifications by default.

## Cleanup

- Explicit delete removes the final and partial files.
- Session end schedules cleanup after a short configurable grace period.
- Agent startup scans for abandoned staging directories older than policy.
- Cleanup is idempotent and never follows symlinks outside the staging root.
- Failed cleanup appears in diagnostics without logging original file names.

## Integrity and Testing

- End-to-end checksum verified before availability.
- Size and hash mismatch removes partial file.
- Tests cover traversal, Unicode normalization, reserved Windows names, symlinks, disk full, duplicate chunks, cancellation, and Agent restart.
- Fuzz file metadata and path-validation inputs.
