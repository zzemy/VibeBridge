# Self-hosting the VibeBridge relay

This guide walks a single operator through running the VibeBridge
relay (`viberelay`) on their own host — typically a small public VM
or a LAN box that both the agent and the client browser can reach.

The relay is the only network-reachable piece of VibeBridge. The
agent and the browser connect to it; everything else runs on the
agent's machine. The relay never inspects the bytes it forwards and
never persists ticket plaintext, so a compromised relay cannot
recover prior session content — but it can mint routes for anyone
who can present a valid ticket, which is why this guide spends time
on TLS, token rotation, and key backup.

## What the relay does

- Authenticates each peer with a short-lived Ed25519-signed
  `RelayTicket`. The relay verifies the signature on its own
  keypair; the agent (or a separate control plane) mints tickets on
  a different keypair that the relay has been told to trust.
- Joins the two halves of a route — an `AGENT` peer and a `CLIENT`
  peer — and forwards opaque WebSocket bytes between them. The
  relay is a switchboard, not a proxy for the application payload.
- Optionally exposes a small HTTP control plane on a separate port
  that lets an authorized caller mint tickets at runtime. Most
  production deployments keep the agent-side mint and the relay's
  verifier on the same keypair and disable the control plane
  entirely.

The relay does **not** store route state across restarts: each
route is dropped when the container stops. In-flight sessions will
reconnect with a fresh ticket.

## Prerequisites

- Docker Engine 24+ and the Compose plugin (`docker compose`).
- A reachable host with a DNS name (or static IP) that both the
  agent and the browser can resolve. TLS is optional but strongly
  recommended for anything beyond a local LAN.
- An Ed25519 keypair the relay can use to verify tickets. The
  image generates one on first start; the same keypair's public
  half must be distributed to whoever mints tickets.

The relay image builds from this repository, so the host only
needs the Docker toolchain — no Go toolchain is required.

## Quick start

```bash
# 1. Clone the repository (or copy the Dockerfile, .dockerignore,
#    docker-compose.yml, and the gen/, internal/, cmd/ trees).
git clone https://github.com/zzemy/VibeBridge.git
cd VibeBridge

# 2. Mint a 32-byte bearer token for the control plane. The
#    control plane is what lets an authorized caller mint tickets
#    at runtime; treat this token like a database password.
echo "VIBERELAY_ADMIN_TOKEN=$(openssl rand -hex 32)" > .env

# 3. Edit docker-compose.yml and replace
#      --allowed-origin=https://agent.example.com
#    with the origin your browser hits the agent under. Repeat the
#    flag for additional origins.

# 4. Build and start the relay.
docker compose up -d viberelay

# 5. Grab the issuer public key from the logs and hand it to
#    whoever mints tickets.
docker compose logs viberelay | grep "issuer public key"
```

The first `docker compose up` builds the image locally
(`viberelay/viberelay:local`). Subsequent restarts reuse the cached
layer; the only change between deploys is the issuer key if you
mount a different `relay-data` volume.

## Verifying the deployment

`docker compose ps` should show `viberelay` as `healthy` within
about fifteen seconds of starting. The healthcheck runs
`viberelay --diagnose` every thirty seconds; the binary reads the
issuer key and exits 0, so an unhealthy status means the binary
could not start (wrong bind address, corrupt key, etc.).

To smoke-test the WebSocket endpoint without a full agent:

```bash
# Replace relay.example.com with the host's DNS name. The 101
# Switching Protocols response is the success signal.
curl -i \
  -H "Connection: Upgrade" \
  -H "Upgrade: websocket" \
  -H "Sec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==" \
  -H "Sec-WebSocket-Version: 13" \
  http://relay.example.com:8788/v1/relay/ws
```

A real handshake will return `101` and immediately close because
the relay expects a `RelayTicket` to follow. The presence of `101`
is enough to confirm the listener is bound and the network path
is open.

## Configuration reference

| Flag                  | Default              | Purpose                                                                                  |
|-----------------------|----------------------|------------------------------------------------------------------------------------------|
| `--addr`              | `0.0.0.0:8788`       | WebSocket switchboard bind address.                                                       |
| `--admin-addr`        | `127.0.0.1:8789`     | Ticket-issuance control plane bind address. Empty disables the control plane.            |
| `--admin-token`       | empty                | Bearer token the control plane requires. Empty disables auth (rely on the bind address).  |
| `--issuer-key`        | `~/.viberelay/issuer.key` | Path to the 64-byte Ed25519 private key. Created if missing.                       |
| `--issuer-key-overwrite` | `false`           | Allow recreating the issuer key if the file already exists. Use with care.               |
| `--allowed-origin`    | same-origin only     | WebSocket origin allowlist. Repeat the flag for multiple origins.                        |
| `--diagnose`          | `false`              | Print resolved config and exit. Used by the compose healthcheck.                         |

The `docker-compose.yml` file pins the most security-sensitive
values explicitly so they are visible in `git diff` after an
upgrade: `--admin-addr`, `--admin-token` (via the `.env` file),
`--allowed-origin`, and `--issuer-key`. Adjust them in the compose
file, not by overriding the container's command line.

## Distributing the issuer public key

The relay prints its issuer public key on stdout every time it
starts, formatted as 64 lowercase hex bytes. The agent (or a
separate control plane) needs that exact byte string to mint
tickets the relay will accept.

Two common deployment shapes:

- **Same host mints and verifies**: the agent runs on the same
  machine as the relay. The agent can read the issuer key file
  directly (mount the `relay-data` volume into the agent
  container) and mint locally. The control plane stays disabled.
- **Separate control plane**: the agent runs on a different host
  from the relay. A control plane mints tickets on a private key
  the relay has been told to trust via its verifier. The relay
  image only knows about its own key; multi-issuer support lives
  in the `relay.NewVerifier(issuers ...)` constructor and is out
  of scope for this single-node guide.

The `relay-data` named volume holds `issuer.key`. Back it up like
a TLS private key. Restoring the same key preserves every
outstanding ticket; restoring a different key invalidates them
all and requires re-issuing.

## TLS termination

The relay speaks plaintext WebSocket. Anything reachable from the
public internet should sit behind a TLS-terminating reverse proxy.
The compose file ships a commented-out `caddy` service that does
this with a one-line `Caddyfile`:

```caddyfile
{$CADDY_DOMAIN} {
    reverse_proxy viberelay:8788
}
```

The Caddy image requests a Let's Encrypt certificate
automatically, so the only operator input is the `CADDY_DOMAIN`
environment variable. After enabling the `caddy` service, point
the agent at `wss://relay.example.com` instead of
`ws://relay.example.com:8788`.

For Nginx or HAProxy the equivalent config is a TCP stream
proxy: the WebSocket upgrade is plaintext on the wire between
the proxy and the relay, and TLS only flows between the client
and the proxy. Do not attempt to terminate WebSocket at a
layer-7 HTTP proxy that buffers requests — the relay will treat
the buffered body as a malformed ticket.

## Hardening checklist

- **Bind the control plane to loopback** unless you have a
  specific reason to expose it. The default `--admin-addr` is
  `127.0.0.1:8789` for a reason; if you change it, set a strong
  `--admin-token` and treat the network path as untrusted.
- **Set `--allowed-origin` to the exact origin your agent
  serves from.** The relay will reject WebSocket upgrades from
  any other origin. Multiple origins are allowed via repeated
  flags; a wildcard is not.
- **Rotate `VIBERELAY_ADMIN_TOKEN`** on a schedule. Rotation is
  safe to do at any time; outstanding tickets remain valid
  because they are signed with the issuer key, not the admin
  token.
- **Back up `relay-data`**. The volume contains the single
  piece of state the relay needs to keep across restarts.
- **Cap resource usage.** The compose file ships with a 1 vCPU
  / 256 MB ceiling. viberelay is a small Go binary and
  comfortably handles thousands of concurrent routes well
  under that envelope; raise the limits only if `docker stats`
  shows them binding.
- **Watch the logs.** The relay emits structured JSON to
  stderr; the compose file caps each log file at 10 MB and
  keeps three rotations. A log shipper is recommended for
  anything beyond a personal deployment.

## Upgrading

```bash
git pull
docker compose build viberelay
docker compose up -d viberelay
```

The `build` step pulls the latest Go source and rebuilds the
image. The `up -d` step recreates the container, which
preserves the `relay-data` volume and therefore the issuer
key. In-flight routes are dropped during the recreate window;
the agent and browser reconnect with a fresh ticket.

## Backup and restore

```bash
# Backup. The volume is just a single Ed25519 key file, so a
# tarball is fine.
docker run --rm \
  -v vibebridge_relay-data:/data:ro \
  -v "$PWD":/backup \
  alpine:3.20 \
  tar -czf /backup/relay-data-$(date -I).tar.gz /data

# Restore.
docker run --rm \
  -v vibebridge_relay-data:/data \
  -v "$PWD":/backup \
  alpine:3.20 \
  tar -xzf /backup/relay-data-2026-07-28.tar.gz -C /
```

Replace `vibebridge_relay-data` with the actual volume name if
you have changed the project name in `docker compose`. The
restore is safe to do while the container is running, but the
relay keeps the issuer key in memory and will not pick up the
new file until the next restart.

## Troubleshooting

- **`actively refused` on every dial** — the relay container is
  not running, or the host firewall is blocking 8788. Check
  `docker compose ps` and the host's `iptables` / `nftables` /
  cloud security group rules.
- **Tickets rejected with `signature mismatch`** — the agent
  and the relay are using different issuer keys. Re-run
  `docker compose logs viberelay | grep "issuer public key"`
  and confirm the agent's configured public key matches the
  relay's output byte for byte.
- **Tickets rejected with `ticket issued too far in the
  future`** — the agent's clock is skewed. The verifier
  rejects any ticket whose `ExpiresAt` is more than ten
  minutes ahead of the relay's clock. Synchronize both hosts
  with NTP.
- **Healthcheck flapping** — the `viberelay --diagnose` call
  is failing. Run it manually inside the container
  (`docker compose exec viberelay viberelay --diagnose ...`)
  to see the error message.
- **Routes close every few minutes** — the relay's idle
  sweeper is doing its job. The default idle timeout is thirty
  minutes; long-lived idle sessions will be reaped. Raise
  the timeout if you need longer-lived idle routes, but be
  aware the trade-off is a larger route table.

## See also

- `docs/architecture/` — how the relay fits into the rest of
  VibeBridge.
- `docs/dependencies.md` — the exact Go module versions baked
  into the image.
- `docs/release-checklist.md` — what to verify before
  publishing a new relay release.
