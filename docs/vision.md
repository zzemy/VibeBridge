# VibeBridge Long-Term Product Vision

## North Star

VibeBridge becomes the trusted mobile control plane for developer-owned AI coding sessions.

A developer should be able to leave the desk, open a phone, and continue the exact local session with the same repository, credentials, tools, and process state. VibeBridge should feel purpose-built for AI-assisted development rather than like a generic remote desktop or SSH client.

## Strategic Position

VibeBridge is not an AI model, cloud IDE, source host, or remote-desktop product. It connects a personal mobile client to a developer-controlled execution environment.

The durable product advantages are:

- Local execution and ownership of source code and credentials.
- A mobile interaction model designed around AI CLI workflows.
- Tool independence across Codex, Claude Code, shells, and future terminal agents.
- End-to-end encrypted remote access without inbound router configuration.
- An open protocol and self-hostable infrastructure.
- Reliable process and session continuity across network and client transitions.

## Three-Year Direction

### Horizon 1: Reliable Personal Session Control

- Windows-first Local Agent with production-grade PTY lifecycle management.
- Installable PWA and mobile beta.
- Expiring device pairing, session recovery, and attachment transfer.
- Same-network direct transport and encrypted community relay.
- Codex, Claude Code, and shell launch profiles.

### Horizon 2: Multi-Device Personal Workspace

- Multiple personal computers and phones under one user-controlled device graph.
- Cross-device session discovery, handoff, revocation, and key rotation.
- macOS and Linux Agents with platform-specific reliability guarantees.
- Better task-state detection and privacy-safe notifications.
- Stable self-hosted relay and control-plane deployment.

### Horizon 3: Extensible Developer Control Plane

- Public tool-adapter SDK for agent-specific attachments, approvals, and status.
- Automation hooks that remain local and user-controlled.
- Optional personal account discovery without replacing device-key trust.
- Measured transport optimizations for high-latency and restricted networks.
- Compatibility commitments that let third-party clients and relays participate.

Team administration and enterprise control are not assumed. They require a separate product decision rather than silently changing the personal-device trust model.

## Product Invariants

These constraints survive technology changes:

1. User code and CLI execution remain on user-controlled machines.
2. Relay operators cannot decrypt terminal or attachment payloads.
3. No inbound public port is required for default remote access.
4. Every controlling device is identifiable and revocable.
5. A logical session owns at most one PTY and one active controller.
6. Cleanup is deterministic and idempotent.
7. Protocol evolution preserves a documented compatibility window.
8. The core remains open source and self-hostable.
9. Telemetry never includes terminal contents, prompts, source, or attachments.
10. Mobile usability is a core product requirement, not a responsive afterthought.

## Definition of Technical Quality

"Optimal" means the best verified tradeoff for the product stage, not the newest technology.

A technology choice is acceptable when it:

- Has a clear owner and maintenance path.
- Reduces security or operational risk.
- Can be tested under realistic failure conditions.
- Supports migration without rewriting every client.
- Works in self-hosted and community-hosted deployments.
- Has measurable advantages over the simpler alternative.

Complexity such as WebRTC, QUIC, multi-region consensus, or native UI rewrites is introduced only after evidence shows the existing approach cannot meet a defined service objective.

## Experience Targets

- Installation to first local session: under five minutes for a typical developer.
- Paired-device remote connection: under ten seconds on a healthy network.
- Input-to-terminal echo through relay: target p95 below 250 ms within a region.
- Session recovery after a short network switch: automatic and understandable.
- Attachment transfer: visible progress, cancellation, integrity verification, and no repository pollution.
- Device revocation: effective before the next session authorization.
- Upgrade: failure leaves a runnable previous Agent version.

## Open-Source Commitments

- Protocol schemas, cryptographic design, threat model, Agent, clients, and relay are public.
- Hosted services use the same protocol as self-hosted deployments.
- No private extension is required for core remote-session behavior.
- Security reports have a documented private disclosure path.
- Releases include checksums, signatures, changelogs, migration notes, and compatibility ranges.
