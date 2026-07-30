# VibeBridge Relay Operator Runbook

This runbook covers day-to-day operations for a VibeBridge relay
deployment: health checks, common incidents, key rotation, and
capacity planning.

## Health Checks

### Quick health

```bash
curl http://localhost:8788/health
```

Returns `200 OK` when the relay is accepting connections. A `503`
means the relay is shutting down or at capacity.

### Active routes

```bash
curl -H "Authorization: Bearer $VIBERELAY_ADMIN_TOKEN" \
  http://localhost:8789/admin/routes
```

Returns the count of active and pending routes. A growing pending
count with low active count may indicate clients dialing the relay
but never completing the handshake (misconfigured agent).

## Common Incidents

### Clients cannot connect

1. Check relay health (`/health`).
2. Check firewall rules on port 8788 (WebSocket) and 8789 (admin).
3. Verify the agent's `--relay-url` points to the correct relay
   address (including `wss://` for TLS).
4. Check the agent's `--relay-issuer-key` matches the relay's
   issuer public key. The relay logs its issuer public key on
   startup.
5. If using TLS, verify certificate validity.

### Relay at capacity

The relay enforces `--max-connections` (default: 1000). When
exceeded, new connections receive `503 Service Unavailable`.

1. Check active route count via the admin API.
2. If legitimate traffic, increase `--max-connections` and restart.
3. If abuse, enable rate limiting on the agent's provision endpoint
   (see "Abuse Controls" below).

### Issuer key lost

Losing the issuer key means all outstanding tickets are
unverifiable. Clients must re-provision.

1. Stop the relay: `docker compose down viberelay`.
2. Delete the key volume: `docker volume rm vibebridge_relay-data`.
3. Start the relay: `docker compose up -d viberelay`.
4. Copy the new issuer public key from the logs.
5. Distribute the new key to all agents.

### Memory growth

The relay uses bounded per-route buffers (4 slots × ~1 MiB).
Memory growth beyond expected levels indicates:

1. Too many concurrent routes — increase capacity or add relays.
2. A bug in the sweeper — check logs for sweep errors.

## Key Rotation

### Issuer key rotation

1. Generate a new key: `openssl rand -hex 32 > new-issuer.key`.
   (The relay accepts raw 32-byte Ed25519 private keys.)
2. Stop the relay.
3. Replace `/data/issuer.key` in the container volume.
4. Restart the relay.
5. Update all agents with the new issuer public key.

Old tickets minted with the previous key are immediately invalid.
Clients will reconnect with fresh tickets.

## Abuse Controls

### Rate limiting on provision endpoint

The agent's `/agent/relay/provision` endpoint is rate-limited per
client IP (default: 10 requests per minute). Configure via:

```bash
vibebridge --relay-provision-rate 10 --relay-provision-burst 20
```

### Route TTL

Tickets have a bounded lifetime (default: 5 minutes). The relay's
sweeper reaps routes that have been idle for longer than
`--route-idle-timeout` (default: 5 minutes).

### Blocked origins

Use `--allowed-origin` on the relay to restrict which web origins
can establish WebSocket connections:

```bash
viberelay --allowed-origin=https://agent.example.com \
          --allowed-origin=https://staging.example.com
```

## Capacity Planning

| Metric | Default | Typical | High-load |
|--------|---------|---------|-----------|
| Max connections | 1000 | 200-500 | 5000+ |
| Per-route buffer | 4 frames | 2-3 in flight | 4 (full) |
| Memory per route | ~4 MiB | ~1 MiB | ~4 MiB |
| Sweep interval | 30s | 30s | 15s |

Monitor: `docker stats viberelay` for memory and CPU. Alert if
memory exceeds 256 MiB (the default compose limit).
