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
    ResizeTerminal resize_terminal = 25;
    SessionStatus session_status = 26;
    EndSession end_session = 27;
    AttachmentBegin attachment_begin = 28;
    AttachmentChunk attachment_chunk = 29;
    AttachmentComplete attachment_complete = 30;
    AttachmentCancel attachment_cancel = 31;
    Error error = 32;
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

Support policy:

- Stable clients support the current major version and at least the previous two minor releases.
- Agents should remain compatible with the previous stable mobile release during staged updates.
- Relay outer-envelope support may span more versions because it does not interpret inner payloads.

## Capabilities

Capabilities describe optional behavior, not product authorization.

Examples:

- `terminal.binary_output`
- `session.resume_v1`
- `attachment.chunked_v1`
- `attachment.image_preview_v1`
- `tool.codex_adapter_v1`
- `notification.waiting_input_v1`

Required capabilities are declared before starting a flow. A client must not infer support from version alone.

## Ordering and Resume

- Each direction has a monotonically increasing 64-bit sequence.
- Acknowledgements report the highest contiguous sequence processed.
- Agent keeps a bounded byte-based replay buffer for terminal output.
- Resume includes session identifier, generation, and last acknowledged sequence.
- If replay is unavailable, the Agent returns `RESYNC_REQUIRED`; the client clears terminal state and requests a current snapshot or explains that history was truncated.
- A new PTY increments session generation so stale clients cannot attach to a replacement session using old sequence state.

## Error Model

Errors contain:

- Stable machine code.
- Retry classification: retryable, user action, permanent, incompatible.
- Safe display message.
- Optional retry delay.
- Opaque correlation identifier.

Errors never include raw third-party errors, stack traces, commands, terminal contents, or private paths over remote transports.

Core error families:

- Authentication and revocation.
- Protocol and capability mismatch.
- Session not found, occupied, ended, or expired.
- PTY start, input, resize, and cleanup failure.
- Attachment validation, quota, integrity, and storage failure.
- Relay unavailable, overloaded, or ticket expired.

## Limits

Every decoder receives configured maximums:

- Outer frame size.
- Inner envelope size.
- Terminal input and output payload size.
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
