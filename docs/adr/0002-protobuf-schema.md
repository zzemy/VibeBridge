# ADR-0002: Use Protobuf and Buf for the Shared Protocol

- Status: Accepted for V1 planning
- Date: 2026-07-12

## Context

The current protocol uses hand-written JSON types and raw binary terminal frames. The product needs Go, TypeScript, web, mobile, relay, version negotiation, attachments, resumption, and compatibility checks.

## Decision

Define the canonical protocol in Protocol Buffers under `proto/vibebridge/v1`. Use Buf for linting, generation, and breaking-change checks. Generate Go and TypeScript packages from pinned tooling.

## Rationale

- Field-number evolution is well documented.
- Compact binary encoding suits frequent terminal messages.
- Cross-language code generation reduces drift.
- Buf provides enforceable schema quality and compatibility gates.
- Future third-party clients are not tied to TypeScript or Go source types.

## Alternatives

### JSON Schema

Human-readable and easy to inspect, but larger frames, weaker generated enum/oneof behavior, and more opportunities for cross-language interpretation drift.

### MessagePack or CBOR

Compact but still requires a separate canonical schema and compatibility discipline. Adds flexibility where the product needs stricter contracts.

### FlatBuffers or Cap'n Proto

Useful for zero-copy/high-throughput systems, but terminal traffic does not justify their complexity and ecosystem tradeoffs.

## Consequences

- Generated code becomes a build artifact with drift checks.
- Developers must follow reserved-field and compatibility rules.
- Debug tooling should decode envelopes into safe JSON views.
- Encryption wraps serialized Protobuf; schemas do not provide security by themselves.

## Reconsider When

- Required browser/runtime support becomes impractical.
- Generated TypeScript ergonomics block development despite generator evaluation.
- Performance measurements demonstrate a material encoding bottleneck.
