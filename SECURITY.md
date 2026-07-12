# Security Policy

VibeBridge controls a local terminal and must be treated as security-sensitive software. The current pre-release build is intended only for trusted private networks. Do not expose it directly to the public internet; it does not provide public-facing HTTPS/WSS termination or durable authentication.

## Supported versions

VibeBridge has no stable release yet. Security fixes are applied to the latest commit on `main`; older commits and development branches are not supported. A stable-version support window will be published before V1.

## Reporting a vulnerability

Do not open a public issue or discussion with vulnerability details.

1. Check the repository Security page for the **Report a vulnerability** button and use GitHub private vulnerability reporting when available.
2. If private reporting is not enabled, open a public issue containing only a request for a private security contact. Do not include affected endpoints, reproduction steps, logs, tokens, terminal content, source code, or exploit details. A maintainer will create a private GitHub Security Advisory and invite you.
3. Include affected commit/version, impact, prerequisites, a minimal reproduction, and suggested mitigations only in the private report.

Maintainers will acknowledge a private report within 3 business days, provide an initial assessment within 7 business days when possible, and coordinate disclosure after a fix is available. These are response targets, not guarantees.

## Sensitive data

Reports and diagnostics must omit full session tokens, credentials, prompts, terminal output, repository contents, private paths, and personal data. Use synthetic values and sanitized logs.

## Security boundaries

The current trust model and known risks are documented in [docs/architecture/threat-model.md](docs/architecture/threat-model.md). Reports about unsupported public deployment are still useful when they expose a bug in a stated invariant, but public-internet hardening is not yet a supported product claim.
