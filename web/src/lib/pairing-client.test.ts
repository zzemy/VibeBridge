import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { afterEach, beforeEach, describe, expect, test, vi } from "vitest";

import {
  PairingApprovalSchema,
  PairingApprovalStatus,
  PairingHandshakeResponseSchema,
} from "../gen/vibebridge/v1/handshake_pb";
import { PairingInvitationSchema, type SignedDeviceDescriptor } from "../gen/vibebridge/v1/identity_pb";
import { pairPhone } from "./pairing-client";

vi.mock("./e2ee-pairing", () => ({
  newPairingContext() { return {}; },
  PairingInitiator: class {
    readonly agent: SignedDeviceDescriptor;
    constructor(config: { agent: SignedDeviceDescriptor }) { this.agent = config.agent; }
    start() { return createMockStart(); }
    finish() {
      return {
        finish: createMockFinish(),
        result: {
          peer: this.agent,
          sas: "123-456",
          handshakeHash: new Uint8Array(64),
          transport: {
            decrypt(value: Uint8Array) { return value; },
            close() {},
          },
        },
      };
    }
    close() {}
  },
}));

let finalStatus = PairingApprovalStatus.APPROVED;

class FakePairingWebSocket extends EventTarget {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  readonly CONNECTING = 0;
  readonly OPEN = 1;
  readonly CLOSING = 2;
  readonly CLOSED = 3;
  binaryType: BinaryType = "blob";
  readyState = FakePairingWebSocket.CONNECTING;
  sendCount = 0;

  readonly url: string;
  readonly protocols?: string | string[];

  constructor(url: string, protocols?: string | string[]) {
    super();
    this.url = url;
    this.protocols = protocols;
    queueMicrotask(() => {
      this.readyState = FakePairingWebSocket.OPEN;
      this.dispatchEvent(new Event("open"));
    });
  }

  send(_data: string | ArrayBufferLike | Blob | ArrayBufferView) {
    this.sendCount += 1;
    if (this.sendCount === 1) {
      const response = toBinary(PairingHandshakeResponseSchema, create(PairingHandshakeResponseSchema, {
        noiseMessage: Uint8Array.of(1),
      }));
      queueMicrotask(() => this.message(response));
      return;
    }
    const pending = toBinary(PairingApprovalSchema, create(PairingApprovalSchema, {
      status: PairingApprovalStatus.PENDING,
    }));
    const final = toBinary(PairingApprovalSchema, create(PairingApprovalSchema, {
      status: finalStatus,
      authorizationVersion: finalStatus === PairingApprovalStatus.APPROVED ? 9n : 0n,
    }));
    queueMicrotask(() => this.message(pending));
    window.setTimeout(() => this.message(final), 0);
  }

  close() {
    this.readyState = FakePairingWebSocket.CLOSED;
  }

  message(bytes: Uint8Array) {
    this.dispatchEvent(new MessageEvent("message", { data: bytes.slice().buffer }));
  }
}

function createMockStart() {
  return { $typeName: "vibebridge.v1.PairingHandshakeStart", context: undefined, noiseMessage: Uint8Array.of(1) } as const;
}

function createMockFinish() {
  return { $typeName: "vibebridge.v1.PairingHandshakeFinish", noiseMessage: Uint8Array.of(2) } as const;
}

function invitation() {
  return fromBinary(PairingInvitationSchema, new Uint8Array(readFileSync(resolve(
    process.cwd(),
    "../proto/vibebridge/v1/testdata/pairing_invitation.bin",
  ))));
}

beforeEach(() => {
  finalStatus = PairingApprovalStatus.APPROVED;
  vi.stubGlobal("WebSocket", FakePairingWebSocket);
});

afterEach(() => vi.unstubAllGlobals());

describe("browser pairing client approval gate", () => {
  test("persists Agent trust only after a positive encrypted approval", async () => {
    const pairInvitation = invitation();
    if (pairInvitation.agent === undefined) throw new Error("golden Agent is missing");
    const trustAgent = vi.fn(async (_descriptor: SignedDeviceDescriptor, _version: bigint) => {});
    const states: string[] = [];

    const outcome = await pairPhone({
      invitation: pairInvitation,
      websocketUrl: "ws://192.168.20.5:8787/pairing/v1",
      identity: {
        descriptor: pairInvitation.agent,
        signingPrivateSeed: new Uint8Array(32),
        staticPrivateKey: new Uint8Array(32),
      },
      trustStore: { trustAgent },
      onStatus: (status) => states.push(status.state),
    });

    expect(outcome).toEqual({ state: "approved", sas: "123-456", authorizationVersion: 9n });
    expect(states).toEqual(["connecting", "handshaking", "pending"]);
    expect(trustAgent).toHaveBeenCalledOnce();
    expect(trustAgent.mock.calls[0]?.[1]).toBe(9n);
  });

  test("does not persist trust after an authenticated rejection", async () => {
    finalStatus = PairingApprovalStatus.REJECTED;
    const pairInvitation = invitation();
    if (pairInvitation.agent === undefined) throw new Error("golden Agent is missing");
    const trustAgent = vi.fn(async (_descriptor: SignedDeviceDescriptor, _version: bigint) => {});

    const outcome = await pairPhone({
      invitation: pairInvitation,
      websocketUrl: "ws://192.168.20.5:8787/pairing/v1",
      identity: {
        descriptor: pairInvitation.agent,
        signingPrivateSeed: new Uint8Array(32),
        staticPrivateKey: new Uint8Array(32),
      },
      trustStore: { trustAgent },
    });

    expect(outcome).toEqual({ state: "rejected", sas: "123-456" });
    expect(trustAgent).not.toHaveBeenCalled();
  });
});
