# VibeBridge Open-Source Product V1 PRD

- Status: Draft
- Target audience: Individual developers controlling their own computers
- Commercial model: Out of scope for V1
- License and distribution: Open source

## 1. Product Summary

VibeBridge lets an individual developer securely continue a local AI coding session from a phone. The developer keeps the repository, credentials, tools, and execution environment on their own computer while the phone provides a focused remote terminal and task-control experience.

The product supports terminal-based tools such as Codex, Claude Code, and ordinary shells. It does not replace those tools or move their execution into VibeBridge infrastructure.

## 2. Problem

AI coding sessions often continue longer than the developer remains at their desk. Existing remote-desktop products transmit an entire screen, are awkward on a phone, and expose more of the computer than the task requires. Plain terminal apps do not provide an AI-oriented mobile workflow, safe pairing, attachment transfer, or clear session recovery.

Developers need a way to:

- Check progress and respond to an AI CLI away from the desk.
- Send long prompts without fighting a mobile terminal keyboard.
- Approve, cancel, or redirect a task quickly.
- Share screenshots, logs, and documents from the phone with the local tool.
- Reach their computer outside the local network without opening inbound ports.
- Know which device and session they are controlling.

## 3. Product Goal

An individual developer can install VibeBridge on a computer, pair a phone, and securely control a local AI CLI from the phone on the same network or remotely.

The default experience must be understandable without terminal networking knowledge, router configuration, or manual certificate management.

## 4. V1 Principles

- Local execution: source code, credentials, commands, and AI CLI processes remain on the developer's computer.
- Outbound connectivity: the Local Agent initiates remote connections; users do not expose a home router port.
- Private by design: terminal contents and attachments are end-to-end encrypted when relayed.
- Explicit control: the user can identify, disconnect, and revoke every paired device.
- Tool independence: the terminal contract remains generic, with optional adapters for tool-specific capabilities.
- Mobile-first interaction: the terminal stays visible, while prompts, shortcuts, attachments, and approvals are optimized for touch.
- Open-source operation: core clients, Local Agent, protocol, and relay implementation are inspectable and self-hostable.
- No monetization scope: accounts, limits, or features must not be designed around payment tiers in V1.

## 5. Target User

The V1 user is an individual software developer who:

- Owns or controls the computer running the Local Agent.
- Uses Codex, Claude Code, or another interactive terminal tool.
- Wants to continue a session from a personal phone.
- May have more than one computer and phone.
- Accepts installing a local background agent.
- Does not need organization roles, team administration, or employee monitoring.

## 6. Non-Goals

V1 does not include:

- Team workspaces, organizations, roles, or shared device ownership.
- Enterprise audit, policy management, or identity-provider integration.
- Desktop screen sharing or arbitrary GUI remote control.
- A browser-based IDE or remote file explorer.
- Cloud execution of user repositories or AI tools.
- Permanent cloud storage of terminal transcripts or uploaded files.
- Public anonymous session links.
- Billing, subscriptions, paid limits, trials, or feature gating.
- A plugin marketplace or general workflow-automation platform.

## 7. Product Components

### 7.1 Local Agent

The Go-based Local Agent runs on the developer's computer and owns:

- PTY creation, resize, input, output, reconnect, and cleanup.
- AI CLI and shell launch profiles.
- Workspace selection and attachment staging.
- Device pairing and local device-key storage.
- The encrypted connection to a relay.
- Local diagnostics, logs, version reporting, and updates.

The Agent must not require an inbound public port.

### 7.2 Mobile Client

The mobile client provides:

- Computer and session selection.
- Terminal rendering and direct keyboard input.
- Prompt composer, shortcuts, quick prompts, and recent history.
- Image capture, file selection, previews, and transfer progress.
- Connection, task, and approval notifications.
- Device pairing, biometric unlock, and device revocation.

The first installable client may use the existing React interface as a PWA and a Capacitor-based mobile package. A native UI rewrite is not required for V1.

### 7.3 Relay Service

The relay enables remote access when the phone cannot directly reach the Local Agent. It is responsible for:

- Routing encrypted sessions between paired devices.
- Short-lived connection authorization.
- Presence and connection-state metadata.
- Backpressure, size limits, abuse protection, and relay health.

The relay must not receive plaintext terminal contents or attachment contents. The implementation must support self-hosting and replacement through configuration. V1 includes an official community relay so a new user can complete remote onboarding without deploying infrastructure. The hosted relay has no paid tier in V1 and must use the same public protocol as self-hosted deployments.

### 7.4 Shared Protocol

The shared protocol defines:

- Device identity and pairing messages.
- Session open, attach, detach, resume, and terminate messages.
- PTY input, output, resize, status, and error messages.
- Attachment metadata, chunks, completion, cancellation, and cleanup.
- Capability negotiation and protocol-version compatibility.

Protocol types should be generated or shared from one source to prevent drift between Go, web, and mobile clients.

## 8. Core User Journeys

### 8.1 Install and Pair

1. The developer installs and starts the Local Agent.
2. The Agent creates or loads its device key.
3. The Agent displays a short-lived QR pairing code.
4. The phone scans the code and shows the computer name and key fingerprint.
5. The developer confirms the pairing on the phone and computer.
6. Both devices store the paired public keys.
7. The computer appears in the phone's device list.

Pairing codes must expire and must not be reusable after successful pairing.

### 8.2 Start a Session

1. The developer selects a paired computer.
2. The phone shows available launch profiles and recent sessions.
3. The developer chooses a tool and workspace.
4. The Agent starts the PTY and returns session metadata.
5. The terminal opens with visible connection and security state.

The default profiles should cover the system shell, Codex, and Claude Code without hard-coding the product to one provider.

### 8.3 Continue an Existing Session

1. The phone reconnects after backgrounding, lock, or network change.
2. The Agent retains the PTY during the configured reconnect window.
3. Buffered output is replayed in order.
4. The UI clearly distinguishes reconnecting, resumed, ended, and replaced sessions.

A reconnect must never create a duplicate PTY for the same session.

### 8.4 Send an Image or File

1. The developer selects a file, takes a photo, or shares a file into VibeBridge.
2. The client shows the name, type, size, preview, and destination session.
3. The user confirms the transfer.
4. The file is encrypted and streamed to the Local Agent.
5. The Agent writes it to a session-scoped ignored directory inside the selected workspace.
6. The client inserts or sends a relative path reference to the active tool.
7. The Agent deletes the staged file according to the cleanup policy.

Tool adapters may improve the final attachment action. For example, a Codex adapter can use supported image or file-reference behavior, while the generic fallback sends a local relative path in the prompt.

### 8.5 End and Revoke

1. Ending a session requires confirmation.
2. The Agent terminates the process and closes the PTY exactly once.
3. Temporary attachments and session keys are cleaned up.
4. Revoking a phone invalidates future sessions without affecting other paired devices.

## 9. Functional Requirements

### 9.1 Devices and Identity

- One developer may pair multiple personal computers and phones.
- Every device has a locally generated asymmetric key pair.
- Private device keys are stored using operating-system secure storage when available.
- The UI shows device name, platform, last seen time, Agent version, and connection state.
- A user can rename, disconnect, and revoke a paired device.
- Pairing and session credentials are short-lived and replay resistant.

V1 may use device-based identity without requiring a hosted social-login account. The protocol must leave room for optional account-based discovery later without replacing device keys.

### 9.2 Connectivity

- Same-network direct connections remain supported.
- Remote connections use an outbound Agent connection and encrypted relay path.
- The client automatically selects direct or relayed transport without changing the terminal workflow.
- Transport changes must preserve the logical session when possible.
- The UI exposes whether a connection is direct, private-overlay, or relayed.
- The Local Agent supports explicit proxy configuration and network diagnostics.

### 9.3 Terminal Sessions

- One session has one owning developer and one active controlling client.
- A second paired client may request control, but must not silently replace the active client.
- PTY output preserves bytes and ANSI control sequences.
- Input, resize, search, copy, shortcuts, and composed prompts remain available.
- Session status includes start time, last activity, tool profile, workspace label, and transport state.
- Process exit and cleanup reasons use stable error categories.

### 9.4 Attachments

- V1 supports common image, text, log, Markdown, JSON, and PDF files.
- File names are sanitized and cannot select arbitrary local paths.
- The Agent validates size, count, extension, detected content type, and available disk space.
- Executable content is never launched automatically.
- Transfers support progress, cancellation, retry, and checksum verification.
- Attachment storage is ignored by Git and isolated by session.
- The default policy removes temporary attachments when the session ends.

Initial recommended limits:

- 25 MB per file.
- 100 MB total temporary attachment data per session.
- 10 attachments per prompt action.

Limits are safety defaults, not commercial restrictions.

### 9.5 Notifications

The client should notify the developer when:

- The AI CLI appears to be waiting for input or approval.
- A long-running session exits.
- A connection cannot be restored.
- An attachment transfer completes or fails.

Notification payloads must not contain terminal text, prompts, repository paths, or attachment names by default.

### 9.6 Updates and Compatibility

- The Agent reports its version and supported protocol range.
- Clients reject incompatible protocol versions with an actionable message.
- Agent updates are signed and verified before installation.
- Automatic updates must be opt-in during early open-source releases.
- A failed update must leave a runnable previous version.

## 10. Security and Privacy Requirements

- Pairing establishes device identity; a relay token alone is not sufficient authorization.
- Terminal and attachment payloads are encrypted end to end between paired devices.
- The relay sees only the metadata required to route and limit traffic.
- Device revocation takes effect without reinstalling the Local Agent.
- Sensitive values never appear in URLs after initial local pairing.
- Logs exclude terminal output, prompt text, full file names, repository paths, access tokens, and private keys.
- Local attachment paths cannot escape the staging directory.
- Browser origins, WebSocket upgrades, and upload requests are validated.
- Idle, detached, and abandoned sessions are cleaned up deterministically.
- Threat-model documentation is required before enabling the default remote relay in a stable release.

## 11. UX Requirements

- First successful same-network session should require no more than five minutes after installation.
- Pairing must explain which computer is being trusted and how to revoke it.
- The terminal remains the largest surface in portrait and landscape modes.
- Mobile controls remain usable with the soft keyboard visible.
- Connection loss always presents a visible recovery state and retry timing.
- Attachment transfer never sends a file before user confirmation.
- Destructive actions use explicit labels and confirmation.
- Empty, offline, incompatible-version, revoked-device, and failed-transfer states have recovery actions.
- Accessibility covers semantic controls, focus, keyboard use, readable contrast, and screen-reader labels.

## 12. Open-Source and Self-Hosting Requirements

- Local Agent, clients, protocol definitions, and relay service are developed in public repositories or public directories.
- A developer can run the product in local-only mode without a relay.
- The relay includes a documented self-hosted deployment path.
- The default client can use the official community relay without router or certificate configuration.
- Hosted-service configuration is separated from protocol and core product behavior.
- Telemetry is disabled by default until its schema, purpose, retention, and opt-out behavior are documented.
- Development fixtures and tests do not require proprietary hosted infrastructure.

## 13. Success Metrics

V1 success is measured by product reliability and usability, not revenue.

- Median time from installation to first connected session is under five minutes.
- At least 95% of temporary disconnects within the reconnect window resume the same PTY.
- No known path leaves an Agent-owned child process after session cleanup.
- Attachment checksum failures are detected rather than silently accepted.
- The relay cannot decrypt terminal or attachment payloads in security tests.
- Crash-free Local Agent sessions exceed 99% during public testing.
- Mobile portrait and landscape acceptance checks pass on current iOS and Android browser engines.

## 14. Release Phases

### Phase A: Local Product Foundation

- Preserve the current local terminal workflow.
- Introduce stable session, device, capability, and attachment protocol types.
- Add workspace-aware attachment staging and cleanup.
- Add installable PWA metadata and update behavior.

### Phase B: Paired Devices and Remote Transport

- Add persistent device keys and expiring QR pairing.
- Add direct-versus-relay transport negotiation.
- Implement the self-hostable encrypted relay.
- Add device list, revocation, and protocol compatibility UI.

### Phase C: Mobile Product Beta

- Package the React client with Capacitor for iOS and Android testing.
- Add camera, file picker, share sheet, secure key storage, biometrics, and notifications.
- Complete remote-session, attachment, and network-transition testing.

### Phase D: Open-Source V1

- Publish reproducible Agent, relay, web, and mobile builds.
- Publish threat model, self-hosting guide, protocol documentation, and update policy.
- Complete compatibility and recovery testing across supported platforms.

## 15. V1 Acceptance Criteria

V1 is complete when an individual developer can:

1. Install the Agent and pair a phone using an expiring QR code.
2. Start or resume Codex, Claude Code, or a shell session.
3. Control the session on the same network and through an encrypted remote relay.
4. Move between Wi-Fi and cellular data without starting a duplicate PTY.
5. Send a screenshot or supported document to the local workspace and reference it in the active conversation.
6. Receive privacy-safe task and connection notifications.
7. Revoke a paired phone and verify that it cannot reconnect.
8. Choose local-only mode, the official community relay, or a self-hosted relay without changing the terminal workflow.
9. End the session without leaving the child process or temporary attachment files behind.

## 16. Open Product Decisions

These decisions require prototypes or threat-model work before implementation is finalized:

- Whether V1 device discovery requires an optional user account or remains pairing-code only.
- The E2EE session-key exchange and multi-device key-rotation design.
- Direct transport technology and relay fallback behavior.
- Notification delivery for self-hosted deployments.
- Supported Agent platforms for the first stable release.
- Attachment content detection and platform-specific malware scanning boundaries.
- Whether Capacitor packaging ships in V1 or immediately after the PWA beta.

No open decision may introduce payment tiers or closed-source dependencies into the V1 core requirements without a separate product decision.
