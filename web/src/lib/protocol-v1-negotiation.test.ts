import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
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
  attachmentPromptActionCapability,
  attachmentTransferCapability,
  controlErrorCapability,
  controlHealthCapability,
  createClientHello,
  protocolV1MaxEnvelopeBytes,
  sessionProcessExitCapability,
  sessionResumeCapability,
  terminalBinaryOutputCapability,
  terminalResizeEndCapability,
  terminalSequencedIoCapability,
} from "./protocol-v1";

const connectionId = Uint8Array.from({ length: 16 }, (_, index) => index);

function agentHello(overrides?: { role?: PeerRole; connectionId?: Uint8Array; minimumMinor?: number; capabilities?: string[] }) {
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
        capabilities: overrides?.capabilities ?? [terminalBinaryOutputCapability],
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
    const clientHello = fromBinary(EnvelopeSchema, createClientHello(connectionId, new Date("2026-07-13T10:00:00Z")));
    expect(clientHello.payload.case).toBe("hello");
    if (clientHello.payload.case !== "hello") throw new Error("expected client Hello");
    expect(clientHello.payload.value.capabilities).toContain(terminalSequencedIoCapability);
    expect(clientHello.payload.value.capabilities).toContain(terminalResizeEndCapability);
    expect(clientHello.payload.value.capabilities).toContain(sessionProcessExitCapability);
    expect(clientHello.payload.value.capabilities).toContain(sessionResumeCapability);
    expect(clientHello.payload.value.capabilities).toContain(controlErrorCapability);
    expect(clientHello.payload.value.capabilities).toContain(controlHealthCapability);
    expect(clientHello.payload.value.capabilities).not.toContain(attachmentTransferCapability);
    expect(clientHello.payload.value.capabilities).not.toContain(attachmentPromptActionCapability);
  });

  test("rejects prompt-action advertisement without attachment transfer", () => {
    expect(() => createClientHello(connectionId, new Date("2026-07-13T10:00:00Z"), {
      attachmentPromptAction: true,
    })).toThrow(`${attachmentPromptActionCapability} requires ${attachmentTransferCapability}`);
  });

  test("accepts attachment transfer with its ordered error dependencies", () => {
    const negotiated = acceptAgentHello(agentHello({
      capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, controlErrorCapability, attachmentTransferCapability],
    }), connectionId);

    expect(negotiated.capabilities.has(attachmentTransferCapability)).toBe(true);
  });

  test("explicitly advertises the complete attachment prompt action dependency set", () => {
    const clientHello = fromBinary(EnvelopeSchema, createClientHello(connectionId, new Date("2026-07-13T10:00:00Z"), {
      attachmentTransfer: true,
      attachmentPromptAction: true,
    }));
    if (clientHello.payload.case !== "hello") throw new Error("expected client Hello");

    expect(clientHello.payload.value.capabilities).toEqual(expect.arrayContaining([
      terminalSequencedIoCapability,
      controlErrorCapability,
      attachmentTransferCapability,
      attachmentPromptActionCapability,
    ]));

    const negotiated = acceptAgentHello(agentHello({
      capabilities: [
        terminalBinaryOutputCapability,
        terminalSequencedIoCapability,
        controlErrorCapability,
        attachmentTransferCapability,
        attachmentPromptActionCapability,
      ],
    }), connectionId);
    expect(negotiated.capabilities.has(attachmentPromptActionCapability)).toBe(true);
  });

  test.each([
    ["wrong peer role", agentHello({ role: PeerRole.CLIENT })],
    ["connection ID mismatch", agentHello({ connectionId: Uint8Array.from({ length: 16 }, () => 255) })],
    ["incompatible version", agentHello({ minimumMinor: 1 })],
    ["resize/end without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, terminalResizeEndCapability] })],
    ["process exit without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, sessionProcessExitCapability] })],
    ["control error without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, controlErrorCapability] })],
    ["control health without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, controlHealthCapability] })],
    ["attachment transfer without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, attachmentTransferCapability] })],
    ["attachment transfer without control error", agentHello({ capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, attachmentTransferCapability] })],
    ["attachment prompt action without sequenced I/O", agentHello({ capabilities: [terminalBinaryOutputCapability, attachmentPromptActionCapability] })],
    ["attachment prompt action without transfer", agentHello({ capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, controlErrorCapability, attachmentPromptActionCapability] })],
    ["attachment prompt action without control error", agentHello({ capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, attachmentTransferCapability, attachmentPromptActionCapability] })],
  ])("rejects %s", (_name, encoded) => {
    expect(() => acceptAgentHello(encoded, connectionId)).toThrow();
  });
});
