# ADR-0007: Lost-Phone Recovery via Local CLI Bootstrap

- Status: Proposed
- Date: 2026-07-30
- Related: ADR-0006

## Context

ADR-0006 made paired-device authentication the sole gate for terminal WebSocket sessions. Legacy URL bearer tokens were removed from the upgrade path entirely. This creates a bootstrapping problem: if an operator loses every paired device (phone stolen, laptop wiped, etc.), they cannot access the Agent's management UI — which itself requires a paired session — to authorize a new device.

The Agent process runs on a machine the operator controls (self-hosted server, desktop, or Raspberry Pi). Local filesystem access to that machine is a stronger trust root than any network-delivered credential: an attacker with local filesystem access already owns the Agent. This ADR leverages that fact.

## Decision

Add a `vibebridge recover` CLI subcommand that operates directly on the `deviceidentity.Store` file, bypassing WebSocket authentication entirely. The command provides three operations:

1. **List**: Print all authorized devices (ID, fingerprint, authorization date, state). No side effects.
2. **Revoke-all**: Revoke every authorized device. Each revocation increments the global `RevocationEpoch`, invalidating all existing paired sessions and relay tickets atomically.
3. **Authorize-new**: Initiate a new pairing flow (same as initial setup), allowing the operator to pair a replacement device.

The recovery command requires no network access and no authentication — it reads and writes the identity store file directly. This is safe because:

- The operator already has local filesystem access to the machine running the Agent.
- An attacker with local filesystem access can already compromise the Agent; the recovery command adds no new attack surface.
- The `deviceidentity.Store` uses file-level locking and atomic writes, so concurrent access with the running Agent is safe.

### Invocation

```
vibebridge recover --list
vibebridge recover --revoke-all
vibebridge recover --authorize-new
```

The command detects if the Agent is running and warns the operator. `--revoke-all` requires confirmation. After `--revoke-all`, the operator runs `--authorize-new` which prints a pairing URL/code, same as initial setup.

### Why Not a Recovery Code?

A pre-generated recovery code (printed during initial setup) creates a second secret that must be stored, protected, and rotated. It is weaker than local filesystem access (can be photographed, copied, leaked) and adds complexity. Since the operator always has local access to recover, a code is unnecessary.

### Why Not a Temporary Network Recovery Endpoint?

A network-accessible recovery endpoint (even one gated by a recovery code) reintroduces the attack surface that ADR-0006 eliminated: a network path that bypasses device authentication. This contradicts the security model.

## Consequences

- The `vibebridge` binary gains a `recover` subcommand. Implementation reuses `deviceidentity.Store.Load` / `Revoke` / `Authorize` — no new store APIs needed.
- The operator must have local (or SSH) access to the machine running the Agent to recover. This is consistent with the self-hosted deployment model.
- After recovery, all previous paired sessions are invalidated (epoch bump). Any still-connected sessions are dropped on the next message.
- The recovery flow is documented in the operator guide alongside initial pairing.

## Reconsider When

- VibeBridge moves to a hosted/cloud deployment model where the operator does not have local filesystem access.
- A need emerges for remote recovery without local access (would require a fundamentally different trust model).
