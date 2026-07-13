import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  AcknowledgementSchema,
  EnvelopeSchema,
  TerminalOutputSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";
import {
  ProtocolV1ClientStream,
  protocolV1MaxEnvelopeBytes,
} from "./protocol-v1";

const connectionId = Uint8Array.from({ length: 16 }, (_, index) => index);
const sentAt = new Date("2026-07-13T12:00:00Z");

function agentEnvelope(sequence: bigint, acknowledge: bigint, payload: Envelope["payload"]) {
  return toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId,
    sequence,
    acknowledge,
    sentAt: timestampFromDate(sentAt),
    payload,
  }));
}

describe("Protocol V1 sequenced terminal stream", () => {
  test("sequences terminal input, output, and acknowledgements", () => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);

    const input = fromBinary(EnvelopeSchema, stream.createTerminalInput("yes\r", sentAt));
    expect(input.sequence).toBe(2n);
    expect(input.acknowledge).toBe(1n);
    expect(input.payload.case).toBe("terminalInput");
    if (input.payload.case !== "terminalInput") throw new Error("expected terminal input");
    expect(new TextDecoder().decode(input.payload.value.data)).toBe("yes\r");

    const output = agentEnvelope(2n, 2n, {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, { data: new TextEncoder().encode("ready\r\n") }),
    });
    const message = stream.acceptAgentMessage(output);
    expect(message.type).toBe("terminal-output");
    if (message.type !== "terminal-output") throw new Error("expected terminal output");
    expect(Array.from(message.data)).toEqual(Array.from(new TextEncoder().encode("ready\r\n")));

    const acknowledgement = fromBinary(EnvelopeSchema, stream.createAcknowledgement(sentAt));
    expect(acknowledgement.sequence).toBe(3n);
    expect(acknowledgement.acknowledge).toBe(2n);
    expect(acknowledgement.payload.case).toBe("acknowledgement");
  });

  test.each([
    ["duplicate sequence", 1n, 0n],
    ["sequence gap", 3n, 0n],
    ["acknowledges unsent message", 2n, 2n],
  ])("rejects %s", (_name, sequence, acknowledge) => {
    const stream = new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes);
    const encoded = agentEnvelope(sequence, acknowledge, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    });
    expect(() => stream.acceptAgentMessage(encoded)).toThrow();
  });
});
