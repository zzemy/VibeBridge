# ADR-0005: Keep Go Services in a Modular Monorepo

- Status: Accepted for V1 planning
- Date: 2026-07-12

## Context

The repository currently contains a Go server and React web client. The target system adds a Local Agent, relay, optional Control API, generated protocol packages, mobile shell, deployment assets, and platform adapters.

## Decision

Keep a modular monorepo. Implement the Local Agent, relay, and Control API in Go as separate commands with explicit internal package boundaries. Keep web/mobile packages in a pnpm workspace and generate protocol packages from the root schema.

## Rationale

- Atomic protocol and compatibility changes across components.
- Shared Go security/session code without network package publishing.
- One CI graph for cross-language vectors and embedded builds.
- Easier open-source contribution while the team and release cadence are small.
- Go remains a strong fit for background agents, PTY/process control, relay concurrency, and single binaries.

## Alternatives

### Split Repositories Immediately

Creates release coordination, issue tracking, and schema-version overhead before organizational boundaries require it.

### TypeScript Relay

Could share frontend language but offers little benefit for protocol generation and would duplicate Go operational/security code.

### Rust Agent and Relay

Offers memory safety and strong systems tooling, but rewriting the working Go Agent delays product validation. Reconsider specific security-sensitive components only with measured justification and maintainer capacity.

## Consequences

- Internal package dependency rules and CI ownership become important.
- The repository needs workspace-aware build tooling without adopting a heavy meta-framework prematurely.
- Release artifacts remain independently versioned despite one repository.
- Generated artifacts and platform-specific tests must stay organized.

## Reconsider When

- Components require independent access control or release cadence.
- Repository size materially harms build and contributor experience.
- A dedicated team owns a service with a stable external protocol boundary.
