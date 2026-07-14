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

Content type is detected from bytes and compared with the declared type and extension. The Agent uses a fixed, cross-platform declaration table rather than OS MIME registries:

- `png` maps to `image/png`; `jpg`/`jpeg` to `image/jpeg`; `webp` to `image/webp`; `gif` to `image/gif`; `pdf` to `application/pdf`.
- `txt`/`log` map to `text/plain`; Markdown accepts `text/markdown` or the common `text/plain` fallback; JSON, YAML, TOML, and CSV require their explicitly allowlisted structured media types.
- MIME parameters are rejected except UTF-8 charset declarations for text. Extensions are normalized to an allowlisted lowercase suffix and never inferred from the display name.

Completion checks the first 512 bytes with Go's deterministic content sniffer plus format-specific header checks. Text is also validated incrementally across every chunk for complete UTF-8 encoding and NUL exclusion; HTML, SVG, XML, and script-like active markup detected at the content boundary are rejected rather than reclassified as text. V1 does not claim full image/PDF decoding, malware scanning, or JSON/YAML/TOML/CSV syntax validation.

## Limits

Default safety limits:

- 25 MB per file.
- 100 MB temporary attachment data per session.
- 10 files per prompt action.
- 256 KiB session-manager chunk policy ceiling before encryption. With the current 64 KiB Protocol V1 envelope ceiling, the staged browser client emits at most 48 KiB data chunks and reduces that ceiling for a peer's smaller negotiated envelope so protobuf metadata remains within the limit.
- Four active transfers per session-side manager by default, plus a bounded aggregate across each device and Agent; completed bytes remain reserved until session cleanup.

Self-hosted operators may lower limits. Raising them requires local disk and relay policy checks.

## Resume

V1 may restart small failed transfers from zero. Resumable transfers are added when real usage justifies complexity.

The protocol already includes transfer identifier, byte offset, chunk hash, and total hash so resume can be added compatibly. The V1 manager currently requires an exact next offset, rejects duplicate or gapped chunks without advancing state, and restarts cancelled transfers from zero. A resume implementation must reject mixed file generations and stale partial data.

A `SessionStaging` permits exactly one live transfer manager so parallel managers cannot bypass session quota. The manager reserves declared bytes before creating transfer state, keeps successful files charged across manager reopen until session cleanup, and releases failed or cancelled reservations only after partial removal succeeds. `Complete` is idempotent after publication, while `Cancel` is idempotent and never removes an already-published file. Connection/session owners must close the manager before running staging cleanup. Stable errors do not include remote transfer IDs, display names, or local paths.

## Protocol and Session Ownership

The Protocol V1 Agent decoder accepts begin/chunk/complete/cancel envelopes only when the peer declared `attachment.transfer_v1`, which requires `terminal.sequenced_io_v1` and `control.error_v1`. It performs wire-shape checks and clones all binary fields before returning them to the session layer. Policy, quota, offset, content, and integrity decisions remain centralized in the transfer manager.

A workspace-bound PTY session lazily creates at most one transfer manager and reuses it for all attachment messages. The server applies the manager side effect before committing the inbound sequence and sending its acknowledgement. A rejected operation therefore leaves that client sequence uncommitted, abandons any active partial for that transfer, and returns only the allowlisted `ATTACHMENT_TRANSFER_FAILED` error; transfer IDs, display names, paths, and underlying filesystem errors are not sent to the client. Session exit prevents manager recreation, closes the manager, and only then removes staging. If manager close or staging cleanup fails, the session retains the boundary and emits only the privacy-safe `session.cleanup_failed` diagnostic event.

This remains a dark tracer bullet rather than a user-facing feature. A capability-gated browser flow now provides allowlisted file and camera selection, image preview, explicit review/confirmation, incremental SHA-256 over 1 MiB reads before `AttachmentBegin`, and downward-sized checksummed transfer chunks. The immutable browser `File` is read a second time for transfer because the protocol requires the total hash before any bytes are accepted; cancellation is checked between bounded hash reads. It permits one attachment operation in flight and resolves begin, chunk, complete, or cancel only when the Agent's cumulative acknowledgement covers that operation's client sequence. Progress therefore reports Agent-acknowledged chunk bytes, and the success notice appears only after the Agent has committed the complete/verification side effect. An `ATTACHMENT_TRANSFER_FAILED` whose acknowledgement is exactly one before the pending sequence identifies that operation; any rejection or correlation mismatch poisons the physical stream, abandons the active partial on the Agent, reconnects with fresh sequence state, and requires a whole-file retry.

Cancellation is best effort. Cancelling while an operation is awaiting acknowledgement closes the connection to avoid reusing uncertain sequence state, and files completed earlier in a multi-file action remain staged until session cleanup. A disconnect before an acknowledgement can still leave the client unable to determine whether the Agent committed that operation; outcome reconciliation and chunk resume are not implemented. The browser-side 10-file check is only a usability guard until trusted prompt-action context exists. The production Agent Hello and browser Client Hello still do not advertise `attachment.transfer_v1`, and the App keeps the prepared flow disabled. Explicit multi-file action cancellation semantics, trusted prompt-action enforcement, tool-adapter path delivery, per-device/Agent aggregate limits, crash recovery scans, no-workspace sandbox staging, and end-to-end/real-device validation remain before advertisement is enabled.

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
