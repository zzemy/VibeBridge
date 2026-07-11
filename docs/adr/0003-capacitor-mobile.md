# ADR-0003: Reuse React/xterm Through Capacitor for Mobile V1

- Status: Accepted for V1 planning
- Date: 2026-07-12

## Context

The current React/xterm interface already implements the core terminal workflow. A product mobile client needs app-store packaging, camera, file picker, share sheet, secure key operations, biometrics, notifications, deep links, and better background integration.

## Decision

Keep React and xterm.js as the shared product UI. Deliver an installable PWA and package the same client in Capacitor for iOS and Android. Implement security-sensitive native capabilities through narrow plugins.

## Rationale

- Reuses the most mature part of the current product.
- xterm.js behavior remains consistent across web and mobile.
- Avoids maintaining separate terminal renderers.
- Capacitor provides native distribution and APIs without blocking web/self-hosted clients.
- Shared protocol and state logic can be tested once.

## Alternatives

### React Native

Good native ecosystem, but terminal rendering would still need a WebView or a new renderer, increasing duplication and migration risk.

### Fully Native Swift/Kotlin

Maximum platform control but doubles UI implementation and slows open-source contribution. Reserve for capabilities proven impossible in the shared layer.

### PWA Only

Lowest maintenance, but secure key storage, background behavior, notifications, share sheet, and app-store distribution are weaker or inconsistent.

## Consequences

- Web-origin security remains important inside the native shell.
- Native plugins require independent security review and platform tests.
- Bundle updates and native releases need a coordinated compatibility policy.
- Web UI performance must be measured on lower-end phones.

## Reconsider When

- xterm/WebView performance misses defined targets on supported devices.
- Accessibility cannot meet requirements.
- Native background or notification constraints cannot be solved safely.
- Platform UX diverges enough that shared UI causes persistent quality loss.
