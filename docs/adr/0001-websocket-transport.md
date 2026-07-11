# ADR-0001: Use WebSocket as the V1 Session Transport

- Status: Accepted for V1 planning
- Date: 2026-07-12

## Context

VibeBridge needs bidirectional low-latency terminal/control transport across browsers, native mobile shells, local networks, reverse proxies, and self-hosted relays. Candidate technologies include WebSocket, WebTransport/HTTP/3, WebRTC data channels, SSH, and custom QUIC.

## Decision

Use WSS/WebSocket for V1 direct and relayed session transport. Run the same encrypted Protobuf envelope protocol over both paths.

E2EE is independent of TLS and WebSocket. Relay routing and session semantics do not depend on WebSocket-specific application behavior beyond ordered message delivery.

## Rationale

- Broad browser, mobile WebView, proxy, and load-balancer support.
- Existing implementation experience and operational tooling.
- Ordered delivery matches terminal/control requirements.
- Community self-hosting is straightforward.
- Terminal traffic is small enough that WebSocket overhead is not the limiting factor.

## Alternatives

### WebRTC Data Channels

Potential peer-to-peer connectivity and NAT traversal, but introduces signaling, ICE/TURN operations, unordered/reliable mode complexity, and mobile background variance. Reconsider for high-volume direct attachment transfer after measurement.

### WebTransport or Custom QUIC

Offers streams and modern transport behavior, but browser/proxy/self-host maturity and operational complexity do not justify V1 adoption. Reconsider when support and observed head-of-line blocking warrant it.

### SSH

Strong general remote-shell solution, but does not provide the VibeBridge product protocol, mobile attachment workflow, device graph, relay semantics, or browser-friendly transport.

## Consequences

- Relay must enforce bounded queues because one slow ordered stream can back up.
- Terminal and attachment priorities need application-level scheduling.
- Transport migration performs a new E2EE handshake.
- Large-file performance may eventually justify a second transport.

## Reconsider When

- Measured relay or file-transfer latency cannot meet targets.
- WebSocket proxy restrictions cause material connection failure.
- WebTransport reaches required platform and self-hosting maturity.
- WebRTC direct transfer materially reduces relay cost or improves experience.
