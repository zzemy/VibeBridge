# Protocol Design

## Goals

- One semantic protocol across direct and relayed transports.
- Generated Go and TypeScript types from one schema source.
- Explicit version and capability negotiation.
- Ordered, resumable terminal streams with bounded replay.
- Extensible attachments and tool adapters without breaking old clients.
- Safe decoding of untrusted input with strict size limits.

## Schema Technology

Use Protocol Buffers edition/proto3 schemas managed with Buf. Generate Go and TypeScript packages in CI and during development.

Reasons:

- Stable field-number compatibility rules.
- Compact binary messages suitable for terminal traffic.
- Mature Go tooling and reliable TypeScript generation.
- Schema linting and breaking-change detection.
- Language-neutral protocol for future clients.

JSON remains available only for diagnostics, human-readable local APIs, and development tooling. It is not the canonical session protocol.

References:

- [Protocol Buffers updating rules](https://protobuf.dev/programming-guides/proto3/#updating)
- [Buf breaking-change detection](https://buf.build/docs/breaking/)

## Layering

```text
WebSocket message
  OuterEnvelope
    routing metadata required by relay
    encrypted InnerEnvelope bytes

InnerEnvelope
  protocol version
  connection/session generation
  message sequence and acknowledgement
  typed payload
```

Direct connections may still use the outer envelope so both transports exercise identical code paths.

## Outer Envelope

The relay-visible envelope contains only:

- Protocol routing version.
- Route identifier.
- Direction or destination device identifier in opaque form.
- Ciphertext length.
- Ciphertext.

It must not contain workspace names, commands, terminal text, attachment names, or user prompts.

## Inner Envelope

Illustrative schema:

```proto
message Envelope {
  uint32 protocol_major = 1;
  uint32 protocol_minor = 2;
  bytes connection_id = 3;
  bytes session_id = 4;
  uint64 session_generation = 5;
  uint64 sequence = 6;
  uint64 acknowledge = 7;
  google.protobuf.Timestamp sent_at = 8;

  oneof payload {
    Hello hello = 20;
    StartSession start_session = 21;
    AttachSession attach_session = 22;
    TerminalInput terminal_input = 23;
    TerminalOutput terminal_output = 24;
    TerminalResize terminal_resize = 25;
    SessionStatus session_status = 26;
    EndSession end_session = 27;
    AttachmentBegin attachment_begin = 28;
    AttachmentChunk attachment_chunk = 29;
    AttachmentComplete attachment_complete = 30;
    AttachmentCancel attachment_cancel = 31;
    Error error = 32;
    Acknowledgement acknowledgement = 33;
    ProcessExit process_exit = 34;
  }
}
```

Exact fields are finalized in `proto/vibebridge/v1` and reviewed through ADRs and compatibility tests.

## Version Negotiation

- `protocol_major` changes only for incompatible framing or security semantics.
- `protocol_minor` adds backward-compatible fields and message types.
- Peers exchange supported major/minor ranges and capabilities before session mutation.
- Unknown fields are preserved by Protobuf runtimes where supported and ignored semantically.
- Unknown enum values are handled explicitly rather than mapped to unsafe defaults.
- Removed fields are reserved by number and name.
- CI compares the schema against the latest stable release and rejects accidental breaking changes.

Current migration behavior:

- The browser offers the `vibebridge.v1` WebSocket subprotocol. If the Agent selects it, the client sends a binary protobuf `Hello` as the first message and the Agent returns its binary `Hello` before any PTY is created.
- Both peers validate protocol version `1.0`, the 16-byte connection identifier, peer role, sequence metadata, advertised envelope limit, and capability names. The Agent additionally requires `terminal.binary_output` before starting or attaching a session.
- A negotiation failure closes the WebSocket with protocol error code `1002` and does not mutate session state. If the peer does not select `vibebridge.v1`, the existing legacy JSON/raw-binary path remains available during migration.
- If both peers advertise `terminal.sequenced_io_v1`, terminal input and output use binary protobuf envelopes. Hello is sequence `1` in each direction; subsequent connection-local messages increase monotonically, reject gaps and duplicates, and carry the highest contiguous peer acknowledgement. An explicit `Acknowledgement` payload advances acknowledgement when no data message is available for piggybacking.
- Agent output is split on actual protobuf envelope size to honor the lower negotiated peer limit without dropping PTY bytes. Terminal input is limited to 32 KiB and every decoder retains the 64 KiB local envelope ceiling.
- If both peers also advertise `session.resume_v1`, the first client stream message is `AttachSession` at sequence `2`, followed by the Agent's `SessionStatus` at sequence `2`. Fresh attachment uses empty session metadata and cursor zero; resume sends the assigned 16-byte session ID, positive generation, and the highest Agent sequence processed on the previous physical connection. Terminal traffic starts at sequence `3` after attachment.
- Sequence and acknowledgement state is physical-connection-local: each WebSocket restarts with Hello sequence `1`. Detached raw PTY output is re-encoded into new sequence `3+` envelopes rather than preserving old wire sequence numbers.
- The Agent returns `RESUMED` only for an exact identity, generation, detach-checkpoint, cursor, and complete-replay match. Byte or time eviction makes replay incomplete. A new PTY returns `FRESH`; every other attachment returns `RESYNC_REQUIRED`, causing the browser to reset stale terminal state, explain the truncation, and render any retained replay tail. Each new PTY gets a random protocol session ID and a monotonically increasing in-process generation.
- If both peers also advertise `terminal.resize_end_v1`, client resize and explicit end controls use ordered `TerminalResize` and `EndSession` envelopes. Advertising it without `terminal.sequenced_io_v1` is an invalid capability combination. Columns and rows are integers from 1 through 65,535. Once negotiated, JSON resize/end controls are protocol errors; without the capability, their transitional JSON adapter remains available for older peers.
- If both peers also advertise `session.process_exit_v1`, the Agent reports terminal completion as an ordered `ProcessExit` with a `SUCCESS` or `FAILURE` outcome. Advertising it without `terminal.sequenced_io_v1` is invalid. The outcome follows the final session lifecycle state, so an explicit end remains successful even if process termination returns an expected host error; raw host errors are never included. Without the capability, process exit retains its transitional JSON adapter.
- If both peers also advertise `control.error_v1`, the Agent reports application failures as ordered `Error` envelopes containing only a known `ErrorCode`. Advertising it without `terminal.sequenced_io_v1` is invalid. A resumable connection may receive a fatal startup or occupied-session error before `SessionStatus`; that envelope has empty session metadata and does not bind the stream. Once negotiated, a JSON error is a protocol violation. Without the capability, the Agent uses a JSON adapter with the same fixed safe display text.
- Negotiation, framing, sequence, acknowledgement, unsupported protobuf payload, and session-metadata failures close the WebSocket with protocol code `1002`; they are not represented as application `Error` payloads. Application ping/pong remains on the transitional JSON adapter.

Support policy:

- Stable clients support the current major version and at least the previous two minor releases.
- Agents should remain compatible with the previous stable mobile release during staged updates.
- Relay outer-envelope support may span more versions because it does not interpret inner payloads.

## Capabilities

Capabilities describe optional behavior, not product authorization.

Examples:

- `terminal.binary_output`
- `terminal.sequenced_io_v1`
- `terminal.resize_end_v1`
- `session.process_exit_v1`
- `session.resume_v1`
- `control.error_v1`
- `attachment.chunked_v1`
- `attachment.image_preview_v1`
- `tool.codex_adapter_v1`
- `notification.waiting_input_v1`

Required capabilities are declared before starting a flow. A client must not infer support from version alone.

## Ordering and Resume

- Each direction has a monotonically increasing 64-bit sequence scoped to one physical connection.
- Acknowledgements report the highest contiguous sequence processed on that connection.
- The Agent keeps at most the newest 1 MiB and two minutes of detached terminal output; any byte or time eviction marks replay incomplete.
- Resume includes the prior session identifier, generation, and highest Agent sequence the client processed. The cursor must exactly match the detach checkpoint; the Agent never claims recovery for output whose processing cannot be established.
- `SessionStatus` is ordered before detached replay and live PTY output. If identity, generation, checkpoint, cursor, or replay completeness does not match, the Agent returns `RESYNC_REQUIRED`; the client clears terminal state and explains that history was truncated before rendering the retained tail.
- A new PTY receives a new random session ID and increments the in-process session generation so stale clients cannot attach to a replacement session using old state.

## Error Model

The current negotiated `control.error_v1` payload is intentionally enum-only. `Error` contains one allowlisted `ErrorCode`; it does not carry free-form text, retry metadata, correlation identifiers, or implementation details. The current codes are:

- `SESSION_START_FAILED`
- `SESSION_ALREADY_ACTIVE`
- `TERMINAL_INPUT_FAILED`
- `TERMINAL_RESIZE_FAILED`
- `UNSUPPORTED_MESSAGE`

`UNSPECIFIED` and unknown enum values are rejected. The legacy JSON adapter maps each valid code to fixed safe wire text, and the browser derives safe user-facing copy from the code. `SESSION_ALREADY_ACTIVE` retains the existing actionable browser copy that another browser controls the session. Raw third-party errors, stack traces, commands, terminal contents, private paths, SDK responses, and host process details never cross this boundary.

Application failures use `Error`; protocol and capability violations use a WebSocket protocol close. Only `SESSION_START_FAILED` or `SESSION_ALREADY_ACTIVE` may precede binding on a resume-enabled stream; that ordered envelope carries an empty session ID and generation zero. Errors emitted after binding carry the bound session metadata. Future retry classification or correlation fields require an explicit compatible schema and capability update rather than free-form data in the current payload.

## Limits

Every decoder receives configured maximums:

- Outer frame size.
- Inner envelope size.
- Terminal input and output payload size.
- Terminal dimensions (currently 1 through 65,535 columns and rows).
- Attachment chunk size.
- In-flight messages and bytes.
- Unacknowledged replay bytes.
- Per-session and per-device message rate.

Limits are negotiated downward where needed but never above the local security maximum.

## Testing

- Golden cross-language encode/decode vectors.
- Buf lint and breaking checks.
- Fuzzing for Go decoders and framing logic.
- Property tests for sequence, acknowledgement, resume, and duplicate delivery.
- Compatibility matrix across released Agent and client versions.
- Relay tests proving it cannot parse inner payloads.
