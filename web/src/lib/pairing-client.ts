import { fromBinary, toBinary } from "@bufbuild/protobuf";

import {
  PairingApprovalSchema,
  PairingApprovalStatus,
  PairingHandshakeFinishSchema,
  PairingHandshakeResponseSchema,
  PairingHandshakeStartSchema,
  type PairingApproval,
  type PairingHandshakeResponse,
} from "../gen/vibebridge/v1/handshake_pb";
import type { PairingInvitation, SignedDeviceDescriptor } from "../gen/vibebridge/v1/identity_pb";
import type { BrowserDeviceIdentity } from "./device-identity-store";
import { newPairingContext, PairingInitiator, type PairingTransport } from "./e2ee-pairing";
import { assertNoUnknownFields } from "./strict-protobuf";

const pairingWebSocketSubprotocol = "vibebridge.pairing.v1";
const maxPairingFrameBytes = 64 * 1024;
const approvalAssociatedData = new TextEncoder().encode("VibeBridge pairing approval v1\0");

export type PairingClientStatus =
  | { state: "connecting" }
  | { state: "handshaking" }
  | { state: "pending"; sas: string };

export type PairingOutcome =
  | { state: "approved"; sas: string; authorizationVersion: bigint }
  | { state: "rejected"; sas: string };

export type PairingTrustStore = {
  trustAgent(descriptor: SignedDeviceDescriptor, authorizationVersion: bigint, pairedAt?: Date): Promise<void>;
};

export type PairPhoneOptions = {
  invitation: PairingInvitation;
  websocketUrl: string;
  identity: BrowserDeviceIdentity;
  trustStore: PairingTrustStore;
  signal?: AbortSignal;
  onStatus?: (status: PairingClientStatus) => void;
};

export async function pairPhone(options: PairPhoneOptions): Promise<PairingOutcome> {
  const signedAgent = options.invitation.agent;
  const agent = signedAgent?.deviceDescriptor;
  const client = options.identity.descriptor.deviceDescriptor;
  if (signedAgent === undefined || agent === undefined || client === undefined) throw new Error("Pairing identities are incomplete");
  if (options.signal?.aborted) throw new Error("Pairing was cancelled");

  const context = newPairingContext(
    client.deviceId,
    agent.deviceId,
    options.invitation.invitationId,
    new Uint8Array(),
  );
  const initiator = new PairingInitiator({
    context,
    client: options.identity.descriptor,
    agent: signedAgent,
    staticPrivateKey: options.identity.staticPrivateKey,
    bootstrapSecret: options.invitation.bootstrapSecret,
  });
  let transport: PairingTransport | null = null;
  let socket: WebSocket | null = null;

  try {
    options.onStatus?.({ state: "connecting" });
    socket = new WebSocket(options.websocketUrl, pairingWebSocketSubprotocol);
    socket.binaryType = "arraybuffer";
    await waitForOpen(socket, options.signal, expirationMilliseconds(options.invitation));

    options.onStatus?.({ state: "handshaking" });
    const start = initiator.start();
    sendBinary(socket, toBinary(PairingHandshakeStartSchema, start));
    const responseBytes = await receiveBinary(socket, options.signal, expirationMilliseconds(options.invitation));
    const response = decodeHandshakeResponse(responseBytes);
    const finished = initiator.finish(response);
    transport = finished.result.transport;
    sendBinary(socket, toBinary(PairingHandshakeFinishSchema, finished.finish));

    const pendingBytes = await receiveBinary(socket, options.signal, expirationMilliseconds(options.invitation));
    const pending = decryptApproval(transport, pendingBytes);
    if (pending.status !== PairingApprovalStatus.PENDING || pending.authorizationVersion !== 0n) {
      throw new Error("Agent sent an invalid pending approval");
    }
    options.onStatus?.({ state: "pending", sas: finished.result.sas });

    const finalBytes = await receiveBinary(socket, options.signal, expirationMilliseconds(options.invitation));
    const finalApproval = decryptApproval(transport, finalBytes);
    if (finalApproval.status === PairingApprovalStatus.REJECTED && finalApproval.authorizationVersion === 0n) {
      return { state: "rejected", sas: finished.result.sas };
    }
    if (finalApproval.status !== PairingApprovalStatus.APPROVED || finalApproval.authorizationVersion <= 0n) {
      throw new Error("Agent sent an invalid final approval");
    }

    // Trust is established only after the Agent's authenticated, encrypted and
    // durably-versioned approval has arrived.
    await options.trustStore.trustAgent(
      finished.result.peer,
      finalApproval.authorizationVersion,
      new Date(),
    );
    return {
      state: "approved",
      sas: finished.result.sas,
      authorizationVersion: finalApproval.authorizationVersion,
    };
  } finally {
    initiator.close();
    transport?.close();
    if (socket !== null && socket.readyState < WebSocket.CLOSING) socket.close(1000, "pairing client ended");
  }
}

function decryptApproval(transport: PairingTransport, ciphertext: Uint8Array) {
  const plaintext = transport.decrypt(ciphertext, approvalAssociatedData);
  return decodeApproval(plaintext);
}

function sendBinary(socket: WebSocket, value: Uint8Array) {
  if (value.byteLength === 0 || value.byteLength > maxPairingFrameBytes || socket.readyState !== WebSocket.OPEN) {
    throw new Error("Pairing transport is unavailable");
  }
  socket.send(value.slice().buffer);
}

function waitForOpen(socket: WebSocket, signal: AbortSignal | undefined, expiresAt: number): Promise<void> {
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => finish(new Error("Pairing invitation has expired")), timeoutDelay(expiresAt));
    const onOpen = () => finish();
    const onError = () => finish(new Error("Could not connect to the Agent"));
    const onClose = () => finish(new Error("Agent closed the pairing connection"));
    const onAbort = () => finish(new Error("Pairing was cancelled"));
    const finish = (error?: Error) => {
      window.clearTimeout(timeout);
      socket.removeEventListener("open", onOpen);
      socket.removeEventListener("error", onError);
      socket.removeEventListener("close", onClose);
      signal?.removeEventListener("abort", onAbort);
      if (error === undefined) resolve(); else reject(error);
    };
    socket.addEventListener("open", onOpen, { once: true });
    socket.addEventListener("error", onError, { once: true });
    socket.addEventListener("close", onClose, { once: true });
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function receiveBinary(socket: WebSocket, signal: AbortSignal | undefined, expiresAt: number): Promise<Uint8Array> {
  return new Promise((resolve, reject) => {
    const timeout = window.setTimeout(() => finish(undefined, new Error("Pairing invitation has expired")), timeoutDelay(expiresAt));
    const onMessage = (event: MessageEvent<unknown>) => {
      const bytes = binaryMessage(event.data);
      if (bytes === null || bytes.byteLength === 0 || bytes.byteLength > maxPairingFrameBytes) {
        finish(undefined, new Error("Agent sent an invalid pairing frame"));
        return;
      }
      finish(bytes);
    };
    const onError = () => finish(undefined, new Error("Pairing connection failed"));
    const onClose = () => finish(undefined, new Error("Agent closed the pairing connection"));
    const onAbort = () => finish(undefined, new Error("Pairing was cancelled"));
    const finish = (value?: Uint8Array, error?: Error) => {
      window.clearTimeout(timeout);
      socket.removeEventListener("message", onMessage);
      socket.removeEventListener("error", onError);
      socket.removeEventListener("close", onClose);
      signal?.removeEventListener("abort", onAbort);
      if (error !== undefined) reject(error); else if (value !== undefined) resolve(value);
    };
    socket.addEventListener("message", onMessage, { once: true });
    socket.addEventListener("error", onError, { once: true });
    socket.addEventListener("close", onClose, { once: true });
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

function binaryMessage(value: unknown): Uint8Array | null {
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return Uint8Array.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
  return null;
}

function decodeHandshakeResponse(bytes: Uint8Array): PairingHandshakeResponse {
  try {
    const response = fromBinary(PairingHandshakeResponseSchema, bytes);
    assertNoUnknownFields(response);
    return response;
  } catch {
    throw new Error("Agent handshake response is invalid");
  }
}

function decodeApproval(bytes: Uint8Array): PairingApproval {
  try {
    const approval = fromBinary(PairingApprovalSchema, bytes);
    assertNoUnknownFields(approval);
    return approval;
  } catch {
    throw new Error("Encrypted pairing approval is invalid");
  }
}

function expirationMilliseconds(invitation: PairingInvitation): number {
  const timestamp = invitation.expiresAt;
  if (timestamp === undefined) return Date.now();
  return Number(timestamp.seconds * 1_000n + BigInt(Math.floor(timestamp.nanos / 1_000_000)));
}

function timeoutDelay(expiresAt: number): number {
  return Math.max(0, Math.min(2_147_483_647, expiresAt - Date.now()));
}
