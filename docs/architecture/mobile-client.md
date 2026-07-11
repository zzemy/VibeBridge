# Mobile Client Design

## Strategy

Keep one React/xterm product UI and package it in stages:

1. Responsive web client for rapid iteration.
2. Installable PWA for home-screen and browser testing.
3. Capacitor shell for app-store distribution and native capabilities.

A React Native or fully native rewrite is not the default plan because xterm.js, terminal selection, ANSI rendering, and the existing interface are already web-native. Reevaluate only if measured performance, accessibility, or platform integration cannot meet targets.

Reference: [Capacitor documentation](https://capacitorjs.com/docs).

## Shared Web Layer

Owns:

- Terminal rendering and toolbar.
- Prompt composer, quick prompts, and history.
- Session and device state UI.
- Protocol state machine and generated TypeScript types.
- Attachment preview and transfer UI.
- Responsive portrait, landscape, tablet, and desktop layouts.

The web layer does not directly store long-term private keys in localStorage or expose native secrets to arbitrary JavaScript.

## Native Bridge

Capacitor plugins provide narrow capabilities:

- Key generation and signing/key-agreement operations.
- Biometric unlock.
- Camera and file picker.
- Share sheet receive flow.
- Push notification token and presentation.
- Deep links and QR scanning.
- Background connection hints and network-state events.
- Secure app version and device metadata.

Plugins expose operation results, not raw private keys.

## PWA Mode

PWA remains useful for open-source accessibility and self-hosted deployments.

Requirements:

- Manifest, icons, standalone display, and update UX.
- App shell caching only; never cache terminal streams, tokens, attachments, or status responses.
- Strict Content Security Policy and no third-party runtime scripts.
- Clear warning that browser key storage and background behavior provide lower assurance than native packaging.
- HTTPS required outside localhost.

Reference: [MDN installable PWA guidance](https://developer.mozilla.org/en-US/docs/Web/Progressive_web_apps/Guides/Making_PWAs_installable).

## Navigation

Primary views:

- Device list.
- Device detail and launch profiles.
- Active/recent sessions.
- Terminal workspace.
- Attachment review.
- Pairing and device security.
- Settings and diagnostics.

The terminal workspace remains the first screen after selecting an active session; it is not hidden behind a dashboard.

## Mobile Interaction

- Prompt composer is the default long-text input.
- Direct terminal keyboard remains one action away.
- Common approvals and control keys are thumb reachable.
- Soft keyboard appearance cannot hide End, reconnect state, or prompt send.
- Landscape uses split layout when vertical space is constrained.
- Selection, search, copy, font size, and scrollback remain stable during resize.
- Haptics confirm destructive or transfer-complete actions where appropriate.

## Notifications

Notifications are event summaries, not terminal mirrors.

Allowed examples:

- "A session is waiting for input."
- "Session completed."
- "Connection could not be restored."
- "Attachment transfer failed."

Payload contains opaque device/session identifiers. The app fetches encrypted details after unlock.

## Offline and Background Behavior

- App shell and device list metadata may be available offline.
- Terminal input is never queued silently while offline.
- User-authored prompts remain drafts until a live authenticated session exists.
- Background suspension displays last known state as stale.
- Reconnect uses exponential backoff with jitter and respects OS network signals.
- Push notification does not imply the Agent is currently reachable.

## Accessibility

- Semantic labels for terminal controls and state.
- Dynamic type within tested bounds without layout loss.
- Screen-reader-friendly non-terminal summaries for session state.
- Reduced-motion support.
- Hardware keyboard support on tablets and phones.
- Sufficient target sizes and contrast.

## Testing

- Vitest component and state-machine tests.
- Playwright web viewport and interaction tests.
- Capacitor integration tests on real iOS and Android devices.
- Network transition, lock/background, biometric, deep-link, and notification tests.
- Terminal performance tests with large output and rapid updates.
