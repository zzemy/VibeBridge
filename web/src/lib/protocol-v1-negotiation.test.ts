import { create, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
} from "../gen/vibebridge/v1/envelope_pb";
import {
  acceptAgentHello,
  createClientHello,
  protocolV1MaxEnvelopeBytes,
  terminalBinaryOutputCapability,
} from "./protocol-v1";

const connectionId = Uint8Array.from({ length: 16 }, (_, index) => index);

function agentHello(overrides?: { role?: PeerRole; connectionId?: Uint8Array; minimumMinor?: number }) {
  const version = (minor = 0) => create(ProtocolVersionSchema, { major: 1, minor });
  return toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: overrides?.connectionId ?? connectionId,
    sequence: 1n,
    sentAt: timestampFromDate(new Date("2026-07-13T10:00:00Z")),
    payload: {
      case: "hello",
      value: create(HelloSchema, {
        peerRole: overrides?.role ?? PeerRole.AGENT,
        supportedVersions: create(ProtocolVersionRangeSchema, {
          minimum: version(overrides?.minimumMinor),
          maximum: version(overrides?.minimumMinor),
        }),
        capabilities: [terminalBinaryOutputCapability],
        maxEnvelopeBytes: protocolV1MaxEnvelopeBytes,
      }),
    },
  }));
}

describe("Protocol V1 Hello negotiation", () => {
  test("creates a canonical client Hello", () => {
    const negotiated = acceptAgentHello(agentHello(), connectionId);
    expect(negotiated).toMatchObject({
      protocolMajor: 1,
      protocolMinor: 0,
      maxEnvelopeBytes: protocolV1MaxEnvelopeBytes,
    });
    expect(negotiated.capabilities.has(terminalBinaryOutputCapability)).toBe(true);
    expect(createClientHello(connectionId, new Date("2026-07-13T10:00:00Z"))).toBeInstanceOf(Uint8Array);
  });

  test.each([
    ["wrong peer role", agentHello({ role: PeerRole.CLIENT })],
    ["connection ID mismatch", agentHello({ connectionId: Uint8Array.from({ length: 16 }, () => 255) })],
    ["incompatible version", agentHello({ minimumMinor: 1 })],
  ])("rejects %s", (_name, encoded) => {
    expect(() => acceptAgentHello(encoded, connectionId)).toThrow();
  });
});
