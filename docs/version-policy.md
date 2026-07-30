# VibeBridge Version and Compatibility Policy

## Versioning

VibeBridge follows [semantic versioning](https://semver.org/):

- **MAJOR** (e.g., 2.0.0): Breaking protocol or API changes.
  Migration guides provided. Old clients may not connect to new
  agents/relays.
- **MINOR** (e.g., 1.1.0): New features, backward-compatible.
  Old clients continue to work. New capabilities are advertised
  via Protocol V1 Hello negotiation.
- **PATCH** (e.g., 1.0.1): Bug fixes and security patches.
  No behavior changes.

## Supported Versions

The latest MINOR release of each MAJOR version is supported for
6 months after the next MINOR release. Security fixes are
backported to the supported version.

| Component | Current | Supported |
|-----------|---------|-----------|
| Protocol V1 | 1.0 | 1.x |
| Agent | 1.0.x | Latest 1.x |
| Relay | 1.0.x | Latest 1.x |
| Web client | 1.0.x | Latest 1.x |

## Protocol Compatibility

Protocol V1 uses capability negotiation: the client sends a Hello
envelope listing required capabilities; the agent responds with its
own Hello advertising supported capabilities. If the client requires
a capability the agent doesn't support, the connection is rejected
with a clear error.

This means:
- **Same major version**: always compatible. New capabilities are
  optional and negotiated.
- **Cross major version**: not guaranteed. A V2 client may not
  connect to a V1 agent.

## Migration Guides

### V1 → V2 (future)

When V2 is released, a migration guide will cover:
- New protocol requirements
- Agent configuration changes
- Relay upgrade steps
- Client update timeline
- Rollback procedure

### Patch updates

Patch releases are drop-in replacements. Stop the service, replace
the binary, restart.

## Rollback

### Agent rollback

1. Stop the agent: `systemctl stop vibebridge`.
2. Replace the binary with the previous version.
3. Restore the config file if changed.
4. Restart: `systemctl start vibebridge`.

The identity store and session state are forward-compatible within
a major version. Rolling back does not lose paired devices.

### Relay rollback

1. `docker compose down viberelay`.
2. Change the image tag in `docker-compose.yml` to the previous
   version.
3. `docker compose up -d viberelay`.

Routes are ephemeral; rollback causes all active sessions to
reconnect with fresh tickets, but no data is lost.

## Release Signing

All releases are signed with Ed25519. Verify a release:

```bash
vibebridge verify --release vibebridge-1.0.1-linux-amd64.tar.gz \
                   --signature vibebridge-1.0.1-linux-amd64.tar.gz.sig \
                   --public-key release-signing.pub
```

The release signing public key is published in the repository root
and on the project website.
