/**
 * Relay client for VibeBridge web client.
 *
 * When the web client cannot reach the Agent directly (e.g. the Agent is
 * behind NAT or on a different network), it can connect through a relay
 * server. The relay is a transparent WebSocket switchboard: the client
 * presents a ticket, the relay verifies it and joins two peers onto the
 * same route, then forwards binary messages verbatim.
 *
 * Flow:
 *  1. POST /agent/relay/provision → { route_id, client_ticket, relay_url }
 *  2. Connect WebSocket to relay_url with the V1 subprotocol
 *  3. Send first message: 4-byte big-endian length prefix + ticket bytes
 *  4. All subsequent binary messages are V1 protocol envelopes, forwarded
 *     transparently between the web client and the Agent.
 */

export interface RelayProvisionResult {
  route_id: string;
  client_ticket: string; // base64 (std encoding, from Go)
  relay_url: string;
}

/**
 * Provision a relay route by asking the Agent to mint a client ticket.
 * The Agent also dials its own relay connection in the background.
 */
export async function provisionRelayRoute(
  managementToken: string,
  clientDeviceIdBase64Url: string,
): Promise<RelayProvisionResult> {
  const resp = await fetch("/agent/relay/provision", {
    method: "POST",
    headers: {
      "Content-Type": "application/json",
      Authorization: `Bearer ${managementToken}`,
    },
    body: JSON.stringify({ client_device_id: clientDeviceIdBase64Url }),
  });
  if (!resp.ok) {
    throw new Error(`relay provision failed: ${resp.status}`);
  }
  return resp.json() as Promise<RelayProvisionResult>;
}

/**
 * Convert a hex string to base64url (no padding).
 */
export function hexToBase64Url(hex: string): string {
  const bytes = new Uint8Array(hex.length / 2);
  for (let i = 0; i < hex.length; i += 2) {
    bytes[i / 2] = parseInt(hex.substr(i, 2), 16);
  }
  let binary = "";
  for (const b of bytes) {
    binary += String.fromCharCode(b);
  }
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/**
 * Decode a standard base64 string to Uint8Array.
 */
function base64ToBytes(b64: string): Uint8Array {
  const binary = atob(b64);
  const bytes = new Uint8Array(binary.length);
  for (let i = 0; i < binary.length; i++) {
    bytes[i] = binary.charCodeAt(i);
  }
  return bytes;
}

/**
 * Adjust the relay URL scheme to match the page protocol, avoiding
 * mixed-content blocking when the web client is served over HTTPS.
 */
function adjustRelayUrl(relayUrl: string): string {
  if (window.location.protocol === "https:" && relayUrl.startsWith("ws://")) {
    return "wss://" + relayUrl.slice(5);
  }
  return relayUrl;
}

/**
 * Connect to a relay server and send the ticket frame.
 *
 * Returns a WebSocket that is ready for V1 protocol traffic. The ticket
 * frame is sent as the first "open" listener — because listeners fire in
 * registration order, any subsequent "open" listener (e.g. the V1 protocol
 * sending Hello) runs after the ticket has been queued.
 */
export function connectViaRelay(
  relayUrl: string,
  clientTicketBase64: string,
  subprotocol: string,
): WebSocket {
  const url = adjustRelayUrl(relayUrl);
  const socket = new WebSocket(url, [subprotocol]);
  socket.binaryType = "arraybuffer";

  socket.addEventListener("open", () => {
    const ticketBytes = base64ToBytes(clientTicketBase64);
    const frame = new Uint8Array(4 + ticketBytes.length);
    const view = new DataView(frame.buffer);
    view.setUint32(0, ticketBytes.length, false); // big-endian
    frame.set(ticketBytes, 4);
    socket.send(frame.buffer);
  });

  return socket;
}
