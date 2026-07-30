# ADR-0008: Community Relay Threat Model

- Status: Accepted
- Date: 2026-07-30

## Context

Phase 7 introduces a community relay that any user can deploy. The
relay is a privileged network intermediary: it sees both peers'
IPs, connection timing, and traffic volume. It does NOT see the
inner protocol content (E2EE via the V1 protocol), but it can
metadata-fingerprint sessions.

## Threats

### T1: Relay impersonation (MITM)

An attacker deploys a relay that looks like the user's expected
relay. The attacker cannot decrypt traffic (E2EE), but could drop
or delay connections, or attempt downgrade attacks.

**Mitigation**: The relay URL is configured on the agent, not
discovered dynamically. The agent pins the relay's identity via
the issuer public key. A mismatched relay cannot mint valid
tickets.

### T2: Ticket forgery

An attacker with network access to the relay tries to forge a
RelayTicket to join or hijack a route.

**Mitigation**: Tickets are Ed25519-signed by the agent's issuer
key. The relay verifies signatures before joining a route. Replay
is prevented by the ticket's short TTL (5 min) and one-time use
(nonce store).

### T3: Route hijacking

An attacker who somehow obtains a valid ticket for an existing
route tries to join as the second peer, displacing the legitimate
peer.

**Mitigation**: Routes are identified by a random 128-bit route ID.
The probability of guessing an existing route ID is 2^-128. The
relay only allows the first peer of each type (AGENT/CLIENT) to
join; a second peer of the same type is rejected.

### T4: Metadata correlation

The relay can observe: client IP, agent IP, connection duration,
traffic volume, and timing patterns. This metadata could be used
to correlate sessions or de-anonymize users.

**Mitigation**: The relay does not log IPs or traffic patterns by
default. Privacy tests (`privacy_test.go`) verify that log output
never contains payload bytes. For community relays, operators are
documented on what metadata their relay instance collects.

**Residual risk**: A malicious relay operator can still observe
connection metadata. This is accepted: the relay is a switchboard,
not an anonymization layer. Users who need metadata protection
should self-host their relay.

### T5: Abuse of free relay resources

An attacker floods the community relay with connection attempts,
exhausting the connection pool.

**Mitigation**: `MaxConnections` cap rejects excess connections
with 503. The agent's provision endpoint is rate-limited per IP.
The relay's sweeper reaps orphan and idle routes. Operator
runbook documents capacity tuning.

### T6: Relay key compromise

The relay's issuer private key is stolen. The attacker can now
mint valid tickets.

**Mitigation**: The issuer key is stored in a Docker volume with
`0600` permissions. Key rotation is documented in the operator
runbook. Revocation epoch (ADR-0006) provides a fast-path to
invalidate all outstanding tickets without changing the key.

## Accepted Risks

1. **Metadata visibility**: The relay sees IP addresses and
   traffic volume. This is inherent to the relay architecture and
   documented. E2EE protects content.

2. **Single relay DDoS**: A community relay can be DDoSed. The
   MaxConnections cap prevents resource exhaustion, but
   availability is not guaranteed. Users can self-host for
   guaranteed availability.

3. **No relay reputation system**: Phase 7 does not implement
   relay reputation or certification. Users choose their relay
   based on trust (self-hosted, community-recommended, or
   operator-provided).

## Implementation History

- 2026-07-30: ADR accepted. Rate limiting, MaxConnections, sweeper,
  privacy tests, and operator runbook implemented as part of
  Phase 5-7 work.
