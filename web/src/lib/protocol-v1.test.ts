import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  AttachmentBeginSchema,
  AttachmentDiscardSchema,
  AttachmentPromptDisposition,
  AttachmentPromptPreviewSchema,
  AttachmentTransferDisposition,
  AttachmentTransferStatusSchema,
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
  TerminalOutputSchema,
} from "../gen/vibebridge/v1/envelope_pb";

const goldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/hello_envelope.bin");
const terminalOutputGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/terminal_output_envelope.bin");
const attachmentBeginGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/attachment_begin_envelope.bin");
const attachmentPromptPreviewGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/attachment_prompt_preview_envelope.bin");
const attachmentTransferStatusGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/attachment_transfer_status_envelope.bin");
const attachmentDiscardGoldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/attachment_discard_envelope.bin");

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

function goldenAttachmentBeginEnvelope() {
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sessionId: Uint8Array.from([
      0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
      0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
    ]),
    sessionGeneration: 7n,
    sequence: 9n,
    acknowledge: 4n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:02.789Z")),
    payload: {
      case: "attachmentBegin",
      value: create(AttachmentBeginSchema, {
        transferId: Uint8Array.from([0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf]),
        displayName: "diagram.png",
        declaredContentType: "image/png",
        declaredExtension: "png",
        totalSizeBytes: 12_345n,
        totalSha256: Uint8Array.from({ length: 32 }, (_, index) => index),
      }),
    },
  });
}

function goldenAttachmentTransferStatusEnvelope() {
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sessionId: Uint8Array.from([
      0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
      0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
    ]),
    sessionGeneration: 7n,
    sequence: 11n,
    acknowledge: 10n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:03.345Z")),
    payload: {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId: Uint8Array.from([0xc0, 0xc1, 0xc2, 0xc3, 0xc4, 0xc5, 0xc6, 0xc7]),
        disposition: AttachmentTransferDisposition.ACTIVE,
        nextOffsetBytes: 49_152n,
      }),
    },
  });
}

function goldenAttachmentDiscardEnvelope() {
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sessionId: Uint8Array.from([
      0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
      0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
    ]),
    sessionGeneration: 7n,
    sequence: 12n,
    acknowledge: 11n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:04.678Z")),
    payload: {
      case: "attachmentDiscard",
      value: create(AttachmentDiscardSchema, {
        transferIds: [
          Uint8Array.from([0xd0, 0xd1, 0xd2, 0xd3, 0xd4, 0xd5, 0xd6, 0xd7]),
          Uint8Array.from([0xe0, 0xe1, 0xe2, 0xe3, 0xe4, 0xe5, 0xe6, 0xe7]),
        ],
      }),
    },
  });
}

function goldenAttachmentPromptPreviewEnvelope() {
  return create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: Uint8Array.from([
      0x00, 0x11, 0x22, 0x33, 0x44, 0x55, 0x66, 0x77,
      0x88, 0x99, 0xaa, 0xbb, 0xcc, 0xdd, 0xee, 0xff,
    ]),
    sessionId: Uint8Array.from([
      0x10, 0x11, 0x12, 0x13, 0x14, 0x15, 0x16, 0x17,
      0x18, 0x19, 0x1a, 0x1b, 0x1c, 0x1d, 0x1e, 0x1f,
    ]),
    sessionGeneration: 7n,
    sequence: 10n,
    acknowledge: 9n,
    sentAt: timestampFromDate(new Date("2026-07-12T08:00:03.012Z")),
    payload: {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId: Uint8Array.from([0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7]),
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect evidence\n\nUse the following local files:\n- `../.vibebridge/uploads/session/file.txt`",
        appendEnter: true,
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

  test("encodes and decodes the shared attachment begin envelope", () => {
    const golden = new Uint8Array(readFileSync(attachmentBeginGoldenPath));
    const expected = goldenAttachmentBeginEnvelope();

    expect(fromBinary(EnvelopeSchema, golden)).toEqual(expected);
    expect(Array.from(toBinary(EnvelopeSchema, expected))).toEqual(Array.from(golden));
  });

  test("encodes and decodes the shared attachment transfer status envelope", () => {
    const golden = new Uint8Array(readFileSync(attachmentTransferStatusGoldenPath));
    const expected = goldenAttachmentTransferStatusEnvelope();

    expect(fromBinary(EnvelopeSchema, golden)).toEqual(expected);
    expect(Array.from(toBinary(EnvelopeSchema, expected))).toEqual(Array.from(golden));
  });

  test("encodes and decodes the shared attachment discard envelope", () => {
    const golden = new Uint8Array(readFileSync(attachmentDiscardGoldenPath));
    const expected = goldenAttachmentDiscardEnvelope();

    expect(fromBinary(EnvelopeSchema, golden)).toEqual(expected);
    expect(Array.from(toBinary(EnvelopeSchema, expected))).toEqual(Array.from(golden));
  });

  test("encodes and decodes the shared attachment prompt preview envelope", () => {
    const golden = new Uint8Array(readFileSync(attachmentPromptPreviewGoldenPath));
    const expected = goldenAttachmentPromptPreviewEnvelope();

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
