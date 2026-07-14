import { describe, expect, test, vi } from "vitest";

import {
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

    expect(() => describeAttachment(new File(["x"], "run.exe", { type: "application/octet-stream" }))).toThrow("unsupported file type");
    expect(() => describeAttachment(new File(["x"], "notes.md", { type: "text/html" }))).toThrow("mismatched content type");
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
  test("hashes, chunks, reports progress, and completes in order", async () => {
    const calls: string[] = [];
    const sender: AttachmentTransferSender = {
      begin(request) {
        calls.push(`begin:${request.displayName}:${request.totalSizeBytes}`);
        expect(request.transferId).toHaveLength(16);
        expect(request.totalSha256).toHaveLength(32);
      },
      chunk(request) {
        calls.push(`chunk:${request.offsetBytes}:${request.data.byteLength}`);
        expect(request.chunkSha256).toHaveLength(32);
      },
      complete(transferId) {
        calls.push(`complete:${transferId.byteLength}`);
      },
      cancel() {
        calls.push("cancel");
      },
    };
    const progress = vi.fn();

    await transferAttachments([textFile()], sender, new AbortController().signal, progress);

    expect(calls).toEqual(["begin:notes.md:5", "chunk:0:5", "complete:16"]);
    expect(progress).toHaveBeenLastCalledWith(expect.objectContaining({
      fileName: "notes.md",
      fileBytesSent: 5,
      totalBytesSent: 5,
    }));
  });

  test("uses bounded chunks and stops after cancellation", async () => {
    const file = new File([new Uint8Array(49 * 1024).fill(97)], "large.txt", { type: "text/plain" });
    const controller = new AbortController();
    const calls: string[] = [];
    const sender: AttachmentTransferSender = {
      begin() { calls.push("begin"); },
      chunk(request) { calls.push(`chunk:${request.offsetBytes}:${request.data.byteLength}`); },
      complete() { calls.push("complete"); },
      cancel() { calls.push("cancel"); },
    };

    await expect(transferAttachments([file], sender, controller.signal, () => controller.abort())).rejects.toMatchObject({ name: "AbortError" });
    expect(calls).toEqual(["begin", "chunk:0:49152", "cancel"]);
  });

  test("cancels a begun transfer when a chunk fails", async () => {
    const calls: string[] = [];
    const sender: AttachmentTransferSender = {
      begin() { calls.push("begin"); },
      chunk() {
        calls.push("chunk");
        throw new Error("send failed");
      },
      complete() { calls.push("complete"); },
      cancel() { calls.push("cancel"); },
    };

    await expect(transferAttachments([textFile()], sender, new AbortController().signal, () => {})).rejects.toThrow("send failed");
    expect(calls).toEqual(["begin", "chunk", "cancel"]);
  });
});