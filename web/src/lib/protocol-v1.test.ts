import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
  TerminalOutputSchema,
} from "../gen/vibebridge/v1/envelope_pb";

const goldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/hello_envelope.bin");
const terminalOutputGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/terminal_output_envelope.bin");

function goldenHelloEnvelope() {
  const version = () => create(ProtocolVersionSchema, { major: 1, minor: 0 });
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sequence: 1n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:00.123Z")),
    payload: {
      case: "hello",
      value: create(HelloSchema, {
        peerRole: PeerRole.CLIENT,
        supportedVersions: create(ProtocolVersionRangeSchema, {
          minimum: version(),
          maximum: version(),
        }),
        capabilities: ["session.resume_v1", "terminal.binary_output"],
        maxEnvelopeBytes: 65_536,
      }),
    },
  });
}

function goldenTerminalOutputEnvelope() {
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sequence: 42n,
    acknowledge: 17n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:01.456Z")),
    payload: {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, {
        data: new TextEncoder().encode("\u001b[32mready\u001b[0m\r\n"),
      }),
    },
  });
}

describe("Protocol V1 golden vectors", () => {
  test("encodes and decodes the shared Hello envelope", () => {
    const golden = new Uint8Array(readFileSync(goldenPath));
    const expected = goldenHelloEnvelope();

    expect(fromBinary(EnvelopeSchema, golden)).toEqual(expected);
    expect(Array.from(toBinary(EnvelopeSchema, expected))).toEqual(Array.from(golden));
  });

  test("encodes and decodes the shared terminal output envelope", () => {
    const golden = new Uint8Array(readFileSync(terminalOutputGoldenPath));
    const expected = goldenTerminalOutputEnvelope();

    const decoded = fromBinary(EnvelopeSchema, golden);
    expect(decoded).toMatchObject({ sequence: 42n, acknowledge: 17n });
    expect(decoded.payload.case).toBe("terminalOutput");
    if (decoded.payload.case !== "terminalOutput") throw new Error("expected terminal output");
    if (expected.payload.case !== "terminalOutput") throw new Error("expected terminal output fixture");
    expect(Array.from(decoded.payload.value.data)).toEqual(Array.from(expected.payload.value.data));
    expect(Array.from(toBinary(EnvelopeSchema, expected))).toEqual(Array.from(golden));
  });
});
