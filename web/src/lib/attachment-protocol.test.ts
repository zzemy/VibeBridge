import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { describe, expect, test, vi } from "vitest";

import {
  AcknowledgementSchema,
  ErrorCode,
  ErrorSchema,
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

  test("preserves abort semantics and requests stream recovery", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const controller = new AbortController();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), controller.signal);
    const rejection = expect(begin).rejects.toMatchObject({ name: "AbortError" });
    controller.abort();

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("rejects a pending operation when the physical connection closes", async () => {
    const stream = attachmentStream();
    const requestRecovery = vi.fn();
    const sender = new AcknowledgedAttachmentSender(stream, {
      send: () => {},
      requestRecovery,
    });

    const begin = sender.begin(beginRequest(), new AbortController().signal);
    const rejection = expect(begin).rejects.toThrow("Connection closed before the Agent acknowledged");
    sender.disconnect();

    await rejection;
    expect(requestRecovery).not.toHaveBeenCalled();
    await expect(sender.cancel(transferId)).rejects.toThrow("recovering");
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
});
