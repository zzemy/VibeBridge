import { describe, expect, test, vi } from "vitest";

import {
  AttachmentBatchCleanupError,
  attachmentMaxFileBytes,
  attachmentMaxSelectionBytes,
  describeAttachment,
  transferAttachments,
  validateAttachmentSelection,
  type AttachmentTransferSender,
} from "./attachments";

function textFile(name = "notes.md", content = "hello") {
  return new File([content], name, { type: "text/markdown", lastModified: 1 });
}

function textFileWithDeclaredSize(name: string, size: number) {
  const file = textFile(name, "x");
  Object.defineProperty(file, "size", { value: size });
  return file;
}

describe("attachment client policy", () => {
  test("accepts allowlisted metadata and rejects unsupported or oversized files", () => {
    expect(describeAttachment(textFile())).toEqual({
      displayName: "notes.md",
      declaredContentType: "text/markdown",
      declaredExtension: "md",
      totalSizeBytes: 5,
    });

    expect(describeAttachment(new File(["x"], "notes.txt", { type: "text/plain; charset=UTF-8" }))).toMatchObject({
      declaredContentType: "text/plain; charset=utf-8",
    });
    expect(() => describeAttachment(new File(["x"], "run.exe", { type: "application/octet-stream" }))).toThrow("unsupported file type");
    expect(() => describeAttachment(new File(["x"], "notes.md", { type: "text/html" }))).toThrow("mismatched content type");
    expect(() => describeAttachment(new File(["x"], "image.png", { type: "image/png; charset=utf-8" }))).toThrow("mismatched content type");
    expect(() => describeAttachment(new File([new Uint8Array(attachmentMaxFileBytes + 1)], "large.txt", { type: "text/plain" }))).toThrow("25 MB");
  });

  test("enforces the per-action file count before hashing", () => {
    const files = Array.from({ length: 11 }, (_, index) => textFile(`${index}.md`));
    expect(() => validateAttachmentSelection(files)).toThrow("no more than 10 files");
  });

  test("enforces the aggregate selection limit before hashing", () => {
    const fileSize = Math.floor(attachmentMaxSelectionBytes / 5) + 1;
    const files = Array.from({ length: 5 }, (_, index) => textFileWithDeclaredSize(`${index}.md`, fileSize));

    expect(() => validateAttachmentSelection(files)).toThrow("100 MB session limit");
  });
});

describe("attachment transfer", () => {
  test("computes the total checksum incrementally without buffering the whole file", async () => {
    const hashReadBytes = 1024 * 1024;
    const content = new Uint8Array(hashReadBytes + 17).fill(97);
    const file = new File([content], "notes.txt", { type: "text/plain" });
    const wholeFileRead = vi.spyOn(file, "arrayBuffer").mockRejectedValue(new Error("whole-file read is forbidden"));
    const slices = vi.spyOn(file, "slice");
    let totalSha256: Uint8Array | undefined;
    const sender: AttachmentTransferSender = {
      async begin(request) { totalSha256 = request.totalSha256; },
      async chunk() {},
      async complete() {},
      async cancel() {},
      async discard() {},
    };

    await transferAttachments([file], sender, new AbortController().signal, () => {});

    expect(wholeFileRead).not.toHaveBeenCalled();
    expect(slices.mock.calls.slice(0, 2).map(([start, end]) => [start, end])).toEqual([
      [0, hashReadBytes],
      [hashReadBytes, hashReadBytes + 17],
    ]);
    expect(totalSha256 ? Array.from(totalSha256, (byte) => byte.toString(16).padStart(2, "0")).join("") : "").toBe(
      "c26032d5154f96bd29c799447d715ab681d8d0aa308ecc6f321a35d98f0672da",
    );
  });

  test("can cancel during incremental hashing before a transfer begins", async () => {
    const file = new File(["abcdefghijk"], "notes.txt", { type: "text/plain" });
    const controller = new AbortController();
    const originalSlice = file.slice.bind(file);
    const slice = vi.spyOn(file, "slice").mockImplementation((start, end, contentType) => {
      controller.abort();
      return originalSlice(start, end, contentType);
    });
    const sender: AttachmentTransferSender = {
      begin: vi.fn(),
      chunk: vi.fn(),
      complete: vi.fn(),
      cancel: vi.fn(),
      discard: vi.fn(),
    };

    await expect(transferAttachments([file], sender, controller.signal, () => {}, 4)).rejects.toMatchObject({ name: "AbortError" });

    expect(slice).toHaveBeenCalledTimes(1);
    expect(sender.begin).not.toHaveBeenCalled();
    expect(sender.cancel).not.toHaveBeenCalled();
    expect(sender.discard).not.toHaveBeenCalled();
  });

  test("waits for acknowledged operations before progress and completion", async () => {
    const calls: string[] = [];
    let acknowledgeBegin: () => void = () => { throw new Error("begin acknowledgement was not initialized"); };
    let acknowledgeChunk: () => void = () => { throw new Error("chunk acknowledgement was not initialized"); };
    const beginAcknowledged = new Promise<void>((resolve) => { acknowledgeBegin = resolve; });
    const chunkAcknowledged = new Promise<void>((resolve) => { acknowledgeChunk = resolve; });
    let completedTransferId: Uint8Array | undefined;
    const discard = vi.fn();
    const sender: AttachmentTransferSender = {
      async begin(request) {
        calls.push(`begin:${request.displayName}:${request.totalSizeBytes}`);
        expect(request.transferId).toHaveLength(16);
        expect(request.totalSha256).toHaveLength(32);
        await beginAcknowledged;
      },
      async chunk(request) {
        calls.push(`chunk:${request.offsetBytes}:${request.data.byteLength}`);
        expect(request.chunkSha256).toHaveLength(32);
        await chunkAcknowledged;
      },
      async complete(transferId) {
        completedTransferId = transferId;
        calls.push(`complete:${transferId.byteLength}`);
      },
      async cancel() {
        calls.push("cancel");
      },
      discard,
    };
    const progress = vi.fn();

    const transfer = transferAttachments([textFile()], sender, new AbortController().signal, progress);

    await vi.waitFor(() => expect(calls).toEqual(["begin:notes.md:5"]));
    acknowledgeBegin();
    await vi.waitFor(() => expect(calls).toEqual(["begin:notes.md:5", "chunk:0:5"]));
    expect(progress).not.toHaveBeenCalled();
    acknowledgeChunk();
    const completedIds = await transfer;

    expect(completedIds).toHaveLength(1);
    expect(completedIds[0]).toEqual(completedTransferId);
    const firstCompletedByte = completedTransferId?.[0];
    if (completedIds[0]) completedIds[0][0] ^= 255;
    expect(completedTransferId?.[0]).toBe(firstCompletedByte);
    expect(calls).toEqual(["begin:notes.md:5", "chunk:0:5", "complete:16"]);
    expect(discard).not.toHaveBeenCalled();
    expect(progress).toHaveBeenLastCalledWith(expect.objectContaining({
      fileName: "notes.md",
      fileBytesSent: 5,
      totalBytesSent: 5,
    }));
  });

  test("uses bounded chunks and discards the started batch after cancellation", async () => {
    const file = new File([new Uint8Array(49 * 1024).fill(97)], "large.txt", { type: "text/plain" });
    const controller = new AbortController();
    const calls: string[] = [];
    const startedIds: Uint8Array[] = [];
    const sender: AttachmentTransferSender = {
      async begin(request) { startedIds.push(request.transferId.slice()); calls.push("begin"); },
      async chunk(request) { calls.push(`chunk:${request.offsetBytes}:${request.data.byteLength}`); },
      async complete() { calls.push("complete"); },
      async cancel() { calls.push("cancel"); },
      async discard(transferIds) {
        expect(transferIds).toEqual(startedIds);
        calls.push(`discard:${transferIds.length}`);
      },
    };

    await expect(transferAttachments([file], sender, controller.signal, () => controller.abort())).rejects.toMatchObject({ name: "AbortError" });
    expect(calls).toEqual(["begin", "chunk:0:49152", "discard:1"]);
  });

  test("uses a downward-negotiated chunk limit", async () => {
    const file = new File([new Uint8Array(20 * 1024).fill(97)], "large.txt", { type: "text/plain" });
    const chunkSizes: number[] = [];
    const sender: AttachmentTransferSender = {
      async begin() {},
      async chunk(request) { chunkSizes.push(request.data.byteLength); },
      async complete() {},
      async cancel() {},
      async discard() {},
    };

    await transferAttachments([file], sender, new AbortController().signal, () => {}, 8 * 1024);

    expect(chunkSizes).toEqual([8 * 1024, 8 * 1024, 4 * 1024]);
  });

  test("discards completed and active IDs together when a later file fails", async () => {
    const calls: string[] = [];
    const startedIds: Uint8Array[] = [];
    let currentFile = 0;
    const sender: AttachmentTransferSender = {
      async begin(request) {
        currentFile += 1;
        startedIds.push(request.transferId.slice());
        calls.push(`begin:${currentFile}`);
      },
      async chunk() {
        calls.push(`chunk:${currentFile}`);
        if (currentFile === 2) throw new Error("second file failed");
      },
      async complete() { calls.push(`complete:${currentFile}`); },
      async cancel() { calls.push("cancel"); },
      async discard(transferIds) {
        expect(transferIds).toEqual(startedIds);
        calls.push(`discard:${transferIds.length}`);
      },
    };

    await expect(transferAttachments(
      [textFile("first.md"), textFile("second.md")],
      sender,
      new AbortController().signal,
      () => {},
    )).rejects.toThrow("second file failed");

    expect(calls).toEqual(["begin:1", "chunk:1", "complete:1", "begin:2", "chunk:2", "discard:2"]);
  });

  test("discards the completed and current IDs when the user aborts the second file", async () => {
    const controller = new AbortController();
    const startedIds: Uint8Array[] = [];
    let currentFile = 0;
    const discard = vi.fn(async (transferIds: readonly Uint8Array[]) => {
      expect(transferIds).toEqual(startedIds);
    });
    const sender: AttachmentTransferSender = {
      async begin(request) { currentFile += 1; startedIds.push(request.transferId.slice()); },
      async chunk() { if (currentFile === 2) controller.abort(); },
      async complete() {},
      async cancel() {},
      discard,
    };

    await expect(transferAttachments(
      [textFile("first.md"), textFile("second.md")],
      sender,
      controller.signal,
      () => {},
    )).rejects.toMatchObject({ name: "AbortError" });

    expect(discard).toHaveBeenCalledTimes(1);
    expect(startedIds).toHaveLength(2);
  });

  test("preserves the transfer failure when batch cleanup cannot be confirmed", async () => {
    const transferFailure = new Error("send failed");
    const cleanupFailure = new Error("discard failed");
    const sender: AttachmentTransferSender = {
      async begin() {},
      async chunk() { throw transferFailure; },
      async complete() {},
      async cancel() {},
      async discard() { throw cleanupFailure; },
    };

    let caught: unknown;
    try {
      await transferAttachments([textFile()], sender, new AbortController().signal, () => {});
    } catch (cause) {
      caught = cause;
    }

    expect(caught).toBeInstanceOf(AttachmentBatchCleanupError);
    expect(caught).toMatchObject({
      message: "send failed",
      transferCause: transferFailure,
      cleanupCause: cleanupFailure,
      cause: transferFailure,
    });
  });
});
