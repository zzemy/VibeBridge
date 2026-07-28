// Package relayclient is the Agent-side outbound client for the
// VibeBridge relay. The relay is a stateless WebSocket switchboard that
// authenticates peers with a short-lived signed RelayTicket; the client
// in this package dials the relay, presents an Agent-endpoint ticket, and
// exposes the resulting byte stream as an io.ReadWriteCloser the rest of
// the Agent can splice a local WebSocket to.
//
// The client is intentionally tiny: it has no opinion about the
// application protocol. Bytes handed to Stream.Write become one binary
// WebSocket frame, and bytes read from Stream.Read are the next binary
// frame the relay forwarded from the other side of the route. Higher
// layers terminate their own end-to-end session — a Noise IK handshake
// with a relay-ticket-binding prologue — and the relay sees only opaque
// ciphertext.
//
// Concurrency model:
//
//   - One read pump goroutine owns the WebSocket read side and pushes
//     full frames into an internal channel. Read pulls from that channel
//     and surfaces a single frame per call.
//   - Writes are serialized with a mutex, matching gorilla/websocket's
//     single-writer requirement.
//   - Close or context cancellation tears down the underlying connection
//     and signals the read pump so a blocked Read returns.
package relayclient
