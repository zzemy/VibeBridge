import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test, vi } from "vitest";

import {
  AcknowledgementSchema,
  AttachmentPromptDisposition,
  AttachmentPromptPreviewSchema,
  EnvelopeSchema,
  ErrorCode,
  ErrorSchema,
  ProcessExitOutcome,
  ProcessExitSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";
import { AttachmentPromptActionClient } from "./attachment-prompt-action";
import { ProtocolV1ClientStream, protocolV1MaxEnvelopeBytes } from "./protocol-v1";

const sentAt = new Date("2026-07-14T10:00:00Z");
const firstConnectionId = Uint8Array.from({ length: 16 }, (_, index) => index);
const actionId = Uint8Array.from({ length: 16 }, (_, index) => 64 + index);

function promptStream(connectionId = firstConnectionId) {
  return new ProtocolV1ClientStream(connectionId, protocolV1MaxEnvelopeBytes, {
    controlError: true,
    attachmentTransfer: true,
    attachmentPromptAction: true,
    sessionProcessExit: true,
  });
}

function agentEnvelope(connectionId: Uint8Array, sequence: bigint, acknowledge: bigint, payload: Envelope["payload"]) {
  return toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId,
    sequence,
    acknowledge,
    sentAt: timestampFromDate(sentAt),
    payload,
  }));
}

function accept(
  client: AttachmentPromptActionClient,
  stream: ProtocolV1ClientStream,
  connectionId: Uint8Array,
  sequence: bigint,
  acknowledge: bigint,
  payload: Envelope["payload"],
) {
  const message = stream.acceptAgentMessage(agentEnvelope(connectionId, sequence, acknowledge, payload));
  client.acceptAgentMessage(message);
}

describe("AttachmentPromptActionClient", () => {
  test("prepares one durable action and resolves only its exact Agent preview", async () => {
    const stream = promptStream();
    const sent: Uint8Array[] = [];
    const client = new AttachmentPromptActionClient({
      newActionId: () => actionId,
    });
    client.connect(stream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });

    const prepared = client.prepare({
      transferIds: [new Uint8Array([1, 2, 3])],
      prompt: "Inspect this file",
      appendEnter: true,
    });
    expect(sent).toHaveLength(1);
    const request = fromBinary(EnvelopeSchema, sent[0]);
    expect(request.payload.case).toBe("attachmentPromptPrepare");
    if (request.payload.case !== "attachmentPromptPrepare") throw new Error("expected prompt prepare");
    expect(request.payload.value.actionId).toEqual(actionId);

    const previewText = "Inspect this file\n\nUse the following local files:\n- `./staged/file.txt`";
    accept(client, stream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: previewText,
        appendEnter: true,
      }),
    });

    await expect(prepared).resolves.toEqual({
      disposition: AttachmentPromptDisposition.PREPARED,
      preview: previewText,
      appendEnter: true,
    });
  });

  test("commits and cancels only after an explicit prepared action", async () => {
    const stream = promptStream();
    const sent: Uint8Array[] = [];
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: (encoded) => sent.push(encoded), requestRecovery: vi.fn() });

    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: false });
    accept(client, stream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: false,
      }),
    });
    await prepared;

    const committed = client.commit();
    const commit = fromBinary(EnvelopeSchema, sent[1]);
    expect(commit.payload.case).toBe("attachmentPromptCommit");
    if (commit.payload.case !== "attachmentPromptCommit") throw new Error("expected prompt commit");
    expect(commit.payload.value.actionId).toEqual(actionId);
    accept(client, stream, firstConnectionId, 3n, 3n, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    });
    await committed;
    await expect(client.commit()).rejects.toThrow("No prepared");

    const secondActionId = Uint8Array.from(actionId, (value) => value + 1);
    const cancelStream = promptStream(Uint8Array.from(firstConnectionId, (value) => value + 1));
    const cancelSent: Uint8Array[] = [];
    const cancelClient = new AttachmentPromptActionClient({ newActionId: () => secondActionId });
    const cancelConnectionId = Uint8Array.from(firstConnectionId, (value) => value + 1);
    cancelClient.connect(cancelStream, { send: (encoded) => cancelSent.push(encoded), requestRecovery: vi.fn() });
    const secondPrepared = cancelClient.prepare({ transferIds: [new Uint8Array([2])], prompt: "Inspect", appendEnter: true });
    accept(cancelClient, cancelStream, cancelConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId: secondActionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/second.txt",
        appendEnter: true,
      }),
    });
    await secondPrepared;
    const cancelled = cancelClient.cancel();
    expect(fromBinary(EnvelopeSchema, cancelSent[1]).payload.case).toBe("attachmentPromptCancel");
    accept(cancelClient, cancelStream, cancelConnectionId, 3n, 3n, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    });
    await cancelled;
  });

  test("retries prepare with the same action ID after reconnect", async () => {
    const firstStream = promptStream();
    const firstSent: Uint8Array[] = [];
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(firstStream, { send: (encoded) => firstSent.push(encoded), requestRecovery: vi.fn() });
    const prepared = client.prepare({ transferIds: [new Uint8Array([7])], prompt: "Inspect", appendEnter: true });

    client.disconnect(firstStream);
    const secondConnectionId = Uint8Array.from(firstConnectionId, (value) => value + 16);
    const secondStream = promptStream(secondConnectionId);
    const secondSent: Uint8Array[] = [];
    client.connect(secondStream, { send: (encoded) => secondSent.push(encoded), requestRecovery: vi.fn() });

    const firstPrepare = fromBinary(EnvelopeSchema, firstSent[0]);
    const secondPrepare = fromBinary(EnvelopeSchema, secondSent[0]);
    if (firstPrepare.payload.case !== "attachmentPromptPrepare" || secondPrepare.payload.case !== "attachmentPromptPrepare") {
      throw new Error("expected retried prompt prepares");
    }
    expect(secondPrepare.payload.value.actionId).toEqual(firstPrepare.payload.value.actionId);
    accept(client, secondStream, secondConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: true,
      }),
    });
    await prepared;
  });

  test("retries a commit with the same action ID after its acknowledgement is lost", async () => {
    const firstStream = promptStream();
    const firstSent: Uint8Array[] = [];
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(firstStream, { send: (encoded) => firstSent.push(encoded), requestRecovery: vi.fn() });
    const prepared = client.prepare({ transferIds: [new Uint8Array([7])], prompt: "Inspect", appendEnter: true });
    accept(client, firstStream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: true,
      }),
    });
    await prepared;
    const committed = client.commit();

    client.disconnect(firstStream);
    const secondConnectionId = Uint8Array.from(firstConnectionId, (value) => value + 32);
    const secondStream = promptStream(secondConnectionId);
    const secondSent: Uint8Array[] = [];
    client.connect(secondStream, { send: (encoded) => secondSent.push(encoded), requestRecovery: vi.fn() });
    const firstCommit = fromBinary(EnvelopeSchema, firstSent[1]);
    const retryCommit = fromBinary(EnvelopeSchema, secondSent[0]);
    if (firstCommit.payload.case !== "attachmentPromptCommit" || retryCommit.payload.case !== "attachmentPromptCommit") {
      throw new Error("expected retried prompt commits");
    }
    expect(retryCommit.payload.value.actionId).toEqual(firstCommit.payload.value.actionId);
    accept(client, secondStream, secondConnectionId, 2n, 2n, {
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    });
    await committed;
  });

  test("rejects a matching preview that does not acknowledge prepare", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: vi.fn(), requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    const rejection = expect(prepared).rejects.toThrow("does not acknowledge");

    accept(client, stream, firstConnectionId, 2n, 1n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: true,
      }),
    });

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("recognizes an already committed prepare tombstone", async () => {
    const stream = promptStream();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: () => {}, requestRecovery: vi.fn() });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    accept(client, stream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.COMMITTED,
      }),
    });

    await expect(prepared).resolves.toEqual({
      disposition: AttachmentPromptDisposition.COMMITTED,
      preview: "",
      appendEnter: false,
    });
    await expect(client.commit()).rejects.toThrow("No prepared");
  });

  test("rejects invalid prepare input without requesting transport recovery", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: vi.fn(), requestRecovery });

    await expect(client.prepare({ transferIds: [], prompt: "Inspect", appendEnter: true }))
      .rejects.toThrow("requires between 1 and 10 transfer IDs");
    expect(requestRecovery).not.toHaveBeenCalled();
  });

  test("abandons pending work when the logical PTY session changes", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: vi.fn(), requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });

    client.disconnect(stream);
    client.resetForSessionChange();

    await expect(prepared).rejects.toThrow("previous session");
    expect(requestRecovery).not.toHaveBeenCalled();
    await expect(client.commit()).rejects.toThrow("No prepared");
  });

  test("completes a commit acknowledged by the process-exit envelope", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: vi.fn(), requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    accept(client, stream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId,
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: true,
      }),
    });
    await prepared;
    const committed = client.commit();

    accept(client, stream, firstConnectionId, 3n, 3n, {
      case: "processExit",
      value: create(ProcessExitSchema, { outcome: ProcessExitOutcome.SUCCESS }),
    });

    await expect(committed).resolves.toBeUndefined();
    expect(requestRecovery).not.toHaveBeenCalled();
  });

  test("rejects active work without recovery when the process exits", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: vi.fn(), requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    const rejection = expect(prepared).rejects.toThrow("Session ended before the attachment prompt action completed");

    accept(client, stream, firstConnectionId, 2n, 1n, {
      case: "processExit",
      value: create(ProcessExitSchema, { outcome: ProcessExitOutcome.SUCCESS }),
    });

    await rejection;
    expect(requestRecovery).not.toHaveBeenCalled();
    await expect(client.cancel()).rejects.toThrow("No prepared");
  });

  test("fails closed with a stable error and recovery request", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: () => {}, requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    const rejection = expect(prepared).rejects.toThrow("Attachment prompt action failed");
    accept(client, stream, firstConnectionId, 2n, 1n, {
      case: "error",
      value: create(ErrorSchema, { code: ErrorCode.ATTACHMENT_PROMPT_ACTION_FAILED }),
    });

    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);
  });

  test("rejects a mismatched preview and closes pending work on shutdown", async () => {
    const stream = promptStream();
    const requestRecovery = vi.fn();
    const client = new AttachmentPromptActionClient({ newActionId: () => actionId });
    client.connect(stream, { send: () => {}, requestRecovery });
    const prepared = client.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    const rejection = expect(prepared).rejects.toThrow("does not match");
    accept(client, stream, firstConnectionId, 2n, 2n, {
      case: "attachmentPromptPreview",
      value: create(AttachmentPromptPreviewSchema, {
        actionId: new Uint8Array([99]),
        disposition: AttachmentPromptDisposition.PREPARED,
        preview: "Inspect ./staged/file.txt",
        appendEnter: true,
      }),
    });
    await rejection;
    expect(requestRecovery).toHaveBeenCalledTimes(1);

    const closeStream = promptStream(Uint8Array.from(firstConnectionId, (value) => value + 1));
    const closeClient = new AttachmentPromptActionClient({ newActionId: () => actionId });
    closeClient.connect(closeStream, { send: () => {}, requestRecovery: vi.fn() });
    const pending = closeClient.prepare({ transferIds: [new Uint8Array([1])], prompt: "Inspect", appendEnter: true });
    const closeRejection = expect(pending).rejects.toThrow("closed");
    closeClient.close();
    await closeRejection;
  });

});
