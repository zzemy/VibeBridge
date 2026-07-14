import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { describe, expect, test, vi } from "vitest";

import {
  AcknowledgementSchema,
  ErrorCode,
  ErrorSchema,
  AttachmentTransferDisposition,
  AttachmentTransferStatusSchema,
  EnvelopeSchema,
  TerminalOutputSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";
import { AcknowledgedAttachmentSender } from "./attachment-protocol";
import {
  ProtocolV1ClientStream,
  protocolV1MaxEnvelopeBytes,
  type AgentStreamMessage,
  type AttachmentBeginRequest,
  type AttachmentChunkRequest,
} from "./protocol-v1";

const connectionId = Uint8Array.from({ length: 16 }, (_, index) => index);
const transferId = Uint8Array.from({ length: 16 }, (_, index) => 16 + index);
const secondTransferId = Uint8Array.from({ length: 16 }, (_, index) => 32 + index);

function attachmentStream() {
  return new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
    attachmentTransfer: true,
    controlError: true,
  });
}

function beginRequest(): AttachmentBeginRequest {
  return {
    transferId,
    displayName: "notes.md",
    declaredContentType: "text/markdown",
    declaredExtension: "md",
    totalSizeBytes: 5n,
    totalSha256: new Uint8Array(32),
  };
}

function chunkRequest(): AttachmentChunkRequest {
  return {
    transferId,
    offsetBytes: 0n,
    data: new TextEncoder().encode("hello"),
    chunkSha256: new Uint8Array(32),
  };
}

function agentEnvelope(sequence: bigint, acknowledge: bigint, payload: Envelope["payload"]) {
  return toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId,
    sequence,
    acknowledge,
    payload,
  }));
}

function acceptAgentMessage(
  stream: ProtocolV1ClientStream,
  sender: AcknowledgedAttachmentSender,
  sequence: bigint,
  acknowledge: bigint,
  payload: Envelope["payload"],
): AgentStreamMessage {
  const message = stream.acceptAgentMessage(agentEnvelope(sequence, acknowledge, payload));
  sender.acceptAgentMessage(message);
  return message;
}

function acknowledgementPayload(): Envelope["payload"] {
  return { case: "acknowledgement", value: create(AcknowledgementSchema) };
}

function errorPayload(code: ErrorCode): Envelope["payload"] {
  return { case: "error", value: create(ErrorSchema, { code }) };
}

function onlySentEnvelope(sent: readonly Uint8Array[]) {
  const encoded = sent[0];
  if (!encoded || sent.length !== 1) {
    throw new Error(`expected exactly one sent envelope, received ${sent.length}`);
  }
  return fromBinary(EnvelopeSchema, encoded);
}

describe("AcknowledgedAttachmentSender", () => {
  test("keeps begin pending until a cumulative Agent acknowledgement covers it", async () => {
    const stream = attachmentStream();
    const sent: Uint8Array[] = [];
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery: vi.fn(),
    });
    let settled = false;

    const pending = sender.begin(beginRequest(), new AbortController().signal);
    void pending.then(() => { settled = true; });
    await Promise.resolve();

    expect(settled).toBe(false);
    expect(onlySentEnvelope(sent).sequence).toBe(2n);

    acceptAgentMessage(stream, sender, 2n, 2n, acknowledgementPayload());
    await pending;
    expect(settled).toBe(true);
  });

  test("rejects a concurrent operation without consuming another sequence", async () => {
    const stream = attachmentStream();
    const sent: Uint8Array[] = [];
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery: vi.fn(),
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    await expect(sender.chunk(chunkRequest(), new AbortController().signal)).rejects.toThrow("awaiting Agent acknowledgement");
    expect(sent).toHaveLength(1);

    acceptAgentMessage(stream, sender, 2n, 2n, acknowledgementPayload());
    await begin;

    const chunk = sender.chunk(chunkRequest(), new AbortController().signal);
    const encodedChunk = sent[1];
    if (!encodedChunk) throw new Error("expected a chunk envelope");
    expect(fromBinary(EnvelopeSchema, encodedChunk).sequence).toBe(3n);
    acceptAgentMessage(stream, sender, 3n, 3n, acknowledgementPayload());
    await chunk;
  });

  test("correlates an explicit attachment rejection and poisons the stream", async () => {
    const stream = attachmentStream();
    const sent: Uint8Array[] = [];
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const rejection = expect(begin).rejects.toThrow("Agent rejected the current attachment operation");
    acceptAgentMessage(stream, sender, 2n, 1n, errorPayload(ErrorCode.ATTACHMENT_TRANSFER_FAILED));

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
    await expect(sender.begin(beginRequest(), new AbortController().signal)).rejects.toThrow("recovering");
    sender.disconnect();
    expect(requestRecovery).toHaveBeenCalledTimes(1);
    expect(sent).toHaveLength(1);
  });

  test("fails closed when an attachment error does not match the pending sequence", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const rejection = expect(begin).rejects.toThrow("did not match the in-flight operation");
    acceptAgentMessage(stream, sender, 2n, 0n, errorPayload(ErrorCode.ATTACHMENT_TRANSFER_FAILED));

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("fails closed on an attachment error when no operation is pending", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });

    acceptAgentMessage(stream, sender, 2n, 1n, errorPayload(ErrorCode.ATTACHMENT_TRANSFER_FAILED));

    expect(requestRecovery).toHaveBeenCalledTimes(1);
    await expect(sender.begin(beginRequest(), new AbortController().signal)).rejects.toThrow("recovering");
  });

  test("does not abandon an already-sent operation when the UI aborts", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const controller = new AbortController();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });
    let settled = false;

    const begin = sender.begin(beginRequest(), controller.signal);
    void begin.finally(() => { settled = true; });
    controller.abort();
    await Promise.resolve();

    expect(settled).toBe(false);
    expect(requestRecovery).not.toHaveBeenCalled();
    acceptAgentMessage(stream, sender, 2n, 2n, acknowledgementPayload());
    await expect(begin).resolves.toBeUndefined();
    expect(requestRecovery).not.toHaveBeenCalled();
  });

  test("reconciles a chunk applied before its acknowledgement was lost", async () => {
    const stream = attachmentStream();
    const sent: Uint8Array[] = [];
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery: vi.fn(),
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    acceptAgentMessage(stream, sender, 2n, 2n, acknowledgementPayload());
    await begin;

    let settled = false;
    const chunk = sender.chunk(chunkRequest(), new AbortController().signal);
    void chunk.then(() => { settled = true; });
    sender.disconnect();
    await Promise.resolve();
    expect(settled).toBe(false);

    const reconnectedStream = attachmentStream();
    const reconnectedSent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, {
      send: (encoded) => reconnectedSent.push(encoded),
      requestRecovery: vi.fn(),
    });
    const statusRequest = onlySentEnvelope(reconnectedSent);
    expect(statusRequest.payload.case).toBe("attachmentTransferStatusRequest");
    if (statusRequest.payload.case !== "attachmentTransferStatusRequest") {
      throw new Error("expected an attachment transfer status request");
    }
    expect(statusRequest.payload.value.transferId).toEqual(transferId);

    const statusMessage = reconnectedStream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId,
        disposition: AttachmentTransferDisposition.ACTIVE,
        nextOffsetBytes: 5n,
      }),
    }));
    sender.acceptAgentMessage(statusMessage);

    await chunk;
    expect(settled).toBe(true);
    expect(reconnectedSent).toHaveLength(1);
  });

  test("reconciles lost begin, complete, and cancel acknowledgements without duplicate side effects", async () => {
    const cases = [
      {
        name: "begin",
        start: (sender: AcknowledgedAttachmentSender) => sender.begin(beginRequest(), new AbortController().signal),
        disposition: AttachmentTransferDisposition.ACTIVE,
      },
      {
        name: "complete",
        start: (sender: AcknowledgedAttachmentSender) => sender.complete(transferId, new AbortController().signal),
        disposition: AttachmentTransferDisposition.COMPLETED,
      },
      {
        name: "cancel",
        start: (sender: AcknowledgedAttachmentSender) => sender.cancel(transferId),
        disposition: AttachmentTransferDisposition.CANCELLED,
      },
    ] as const;

    for (const fixture of cases) {
      const stream = attachmentStream();
      const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery: vi.fn() });
      const pending = fixture.start(sender);
      sender.disconnect();

      const reconnectedStream = attachmentStream();
      const sent: Uint8Array[] = [];
      sender.reconnect(reconnectedStream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });
      const status = reconnectedStream.acceptAgentMessage(agentEnvelope(2n, 2n, {
        case: "attachmentTransferStatus",
        value: create(AttachmentTransferStatusSchema, {
          transferId,
          disposition: fixture.disposition,
        }),
      }));
      sender.acceptAgentMessage(status);

      await expect(pending, fixture.name).resolves.toBeUndefined();
      expect(sent, fixture.name).toHaveLength(1);
    }
  });

  test("replays a chunk only when the Agent cursor proves it was not applied", async () => {
    const stream = attachmentStream();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery: vi.fn() });
    const pending = sender.chunk(chunkRequest(), new AbortController().signal);
    sender.disconnect();

    const reconnectedStream = attachmentStream();
    const sent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });
    const status = reconnectedStream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId,
        disposition: AttachmentTransferDisposition.ACTIVE,
        nextOffsetBytes: 0n,
      }),
    }));
    sender.acceptAgentMessage(status);

    expect(sent).toHaveLength(2);
    expect(fromBinary(EnvelopeSchema, sent[1]!).payload.case).toBe("attachmentChunk");
    acceptAgentMessage(reconnectedStream, sender, 3n, 3n, acknowledgementPayload());
    await pending;
  });

  test("fails closed when the Agent cursor is inside the pending chunk", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery });
    const pending = sender.chunk(chunkRequest(), new AbortController().signal);
    const rejection = expect(pending).rejects.toThrow("cannot safely reconcile");
    sender.disconnect();

    const reconnectedStream = attachmentStream();
    const sent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, { send: (encoded) => sent.push(encoded), requestRecovery });
    const status = reconnectedStream.acceptAgentMessage(agentEnvelope(2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId,
        disposition: AttachmentTransferDisposition.ACTIVE,
        nextOffsetBytes: 3n,
      }),
    }));
    sender.acceptAgentMessage(status);

    await rejection;
    expect(sent).toHaveLength(1);
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("poisons the stream when WebSocket send throws", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => { throw new Error("socket send failed"); },
      requestRecovery,
    });

    await expect(sender.begin(beginRequest(), new AbortController().signal)).rejects.toThrow("socket send failed");
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("uses acknowledgement metadata piggybacked on an unrelated Agent message", async () => {
    const stream = attachmentStream();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery: vi.fn(),
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const message = acceptAgentMessage(stream, sender, 2n, 2n, {
      case: "terminalOutput",
      value: create(TerminalOutputSchema, { data: new TextEncoder().encode("ready") }),
    });

    expect(message.type).toBe("terminal-output");
    await begin;
  });

  test("fails closed on an unacknowledged non-attachment error", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const rejection = expect(begin).rejects.toThrow("before the Agent acknowledged");
    acceptAgentMessage(stream, sender, 2n, 1n, errorPayload(ErrorCode.TERMINAL_INPUT_FAILED));

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("sends one acknowledged discard operation with defensive transfer ID copies", async () => {
    const stream = attachmentStream();
    const sent: Uint8Array[] = [];
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery: vi.fn(),
    });
    const first = transferId.slice();
    const second = secondTransferId.slice();

    const discard = sender.discard([first, second]);
    first[0] ^= 255;
    second[0] ^= 255;

    const envelope = onlySentEnvelope(sent);
    expect(envelope.payload.case).toBe("attachmentDiscard");
    if (envelope.payload.case !== "attachmentDiscard") throw new Error("expected attachment discard");
    expect(envelope.payload.value.transferIds).toEqual([transferId, secondTransferId]);
    acceptAgentMessage(stream, sender, 2n, 2n, acknowledgementPayload());
    await expect(discard).resolves.toBeUndefined();
  });

  test("reconciles a lost discard acknowledgement when every ID is absent", async () => {
    const stream = attachmentStream();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery: vi.fn() });
    const discard = sender.discard([transferId, secondTransferId]);
    sender.disconnect();

    const reconnectedStream = attachmentStream();
    const sent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });
    expect(onlySentEnvelope(sent).payload.case).toBe("attachmentTransferStatusRequest");

    acceptAgentMessage(reconnectedStream, sender, 2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId,
        disposition: AttachmentTransferDisposition.CANCELLED,
      }),
    });
    expect(sent).toHaveLength(2);
    const secondStatus = fromBinary(EnvelopeSchema, sent[1]!);
    expect(secondStatus.payload.case).toBe("attachmentTransferStatusRequest");
    if (secondStatus.payload.case !== "attachmentTransferStatusRequest") throw new Error("expected second status request");
    expect(secondStatus.payload.value.transferId).toEqual(secondTransferId);

    acceptAgentMessage(reconnectedStream, sender, 3n, 3n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId: secondTransferId,
        disposition: AttachmentTransferDisposition.UNKNOWN,
      }),
    });
    await expect(discard).resolves.toBeUndefined();
    expect(sent).toHaveLength(2);
  });

  test.each([
    ["active", AttachmentTransferDisposition.ACTIVE],
    ["completed", AttachmentTransferDisposition.COMPLETED],
  ])("replays the whole discard when one reconciled ID is still %s", async (_name, disposition) => {
    const stream = attachmentStream();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery: vi.fn() });
    const discard = sender.discard([transferId, secondTransferId]);
    sender.disconnect();

    const reconnectedStream = attachmentStream();
    const sent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });
    acceptAgentMessage(reconnectedStream, sender, 2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, { transferId, disposition }),
    });

    expect(sent).toHaveLength(2);
    const replay = fromBinary(EnvelopeSchema, sent[1]!);
    expect(replay.payload.case).toBe("attachmentDiscard");
    if (replay.payload.case !== "attachmentDiscard") throw new Error("expected replayed discard");
    expect(replay.payload.value.transferIds).toEqual([transferId, secondTransferId]);
    acceptAgentMessage(reconnectedStream, sender, 3n, 3n, acknowledgementPayload());
    await expect(discard).resolves.toBeUndefined();
  });

  test("fails closed when discard reconciliation returns a different transfer ID", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery });
    const discard = sender.discard([transferId, secondTransferId]);
    const rejection = expect(discard).rejects.toThrow("did not match");
    sender.disconnect();

    const reconnectedStream = attachmentStream();
    sender.reconnect(reconnectedStream, { send: () => {}, requestRecovery });
    acceptAgentMessage(reconnectedStream, sender, 2n, 2n, {
      case: "attachmentTransferStatus",
      value: create(AttachmentTransferStatusSchema, {
        transferId: secondTransferId,
        disposition: AttachmentTransferDisposition.CANCELLED,
      }),
    });

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("queues discard during recovery and sends it after reconnect", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sent: Uint8Array[] = [];
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: (encoded) => sent.push(encoded),
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const beginRejection = expect(begin).rejects.toThrow("Agent rejected");
    acceptAgentMessage(stream, sender, 2n, 1n, errorPayload(ErrorCode.ATTACHMENT_TRANSFER_FAILED));
    await beginRejection;

    const discard = sender.discard([transferId]);
    await Promise.resolve();
    expect(sent).toHaveLength(1);
    expect(requestRecovery).toHaveBeenCalledTimes(1);

    const reconnectedStream = attachmentStream();
    const reconnectedSent: Uint8Array[] = [];
    sender.reconnect(reconnectedStream, {
      send: (encoded) => reconnectedSent.push(encoded),
      requestRecovery,
    });
    expect(onlySentEnvelope(reconnectedSent).payload.case).toBe("attachmentDiscard");
    acceptAgentMessage(reconnectedStream, sender, 2n, 2n, acknowledgementPayload());
    await expect(discard).resolves.toBeUndefined();
  });

  test("rejects a queued discard when the owning session is disposed", async () => {
    const stream = attachmentStream();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery: vi.fn() });
    sender.disconnect();

    const discard = sender.discard([transferId]);
    const rejection = expect(discard).rejects.toThrow("disposed");
    sender.dispose();

    await rejection;
  });

  test("rejects cleanup started after the owning session is disposed", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, { send: () => {}, requestRecovery });
    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const beginRejection = expect(begin).rejects.toThrow("disposed");

    sender.dispose();

    await beginRejection;
    await expect(sender.discard([transferId])).rejects.toThrow("disposed");
    expect(requestRecovery).not.toHaveBeenCalled();
  });

});
