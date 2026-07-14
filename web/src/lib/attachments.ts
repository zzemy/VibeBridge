import { sha256 } from "@noble/hashes/sha2.js";

import { protocolV1MaxAttachmentChunkBytes, type AttachmentBeginRequest, type AttachmentChunkRequest } from "./protocol-v1";

export const attachmentMaxFileBytes = 25 * 1024 * 1024;
export const attachmentMaxSelectionBytes = 100 * 1024 * 1024;
export const attachmentMaxFilesPerAction = 10;

const attachmentHashReadBytes = 1024 * 1024;
const attachmentTextExtensions = new Set(["txt", "log", "md", "markdown", "json", "yaml", "yml", "toml", "csv"]);

const attachmentContentTypes = new Map<string, ReadonlySet<string>>([
  ["txt", new Set(["text/plain"])],
  ["log", new Set(["text/plain"])],
  ["md", new Set(["text/markdown", "text/plain"])],
  ["markdown", new Set(["text/markdown", "text/plain"])],
  ["json", new Set(["application/json"])],
  ["yaml", new Set(["application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"])],
  ["yml", new Set(["application/yaml", "application/x-yaml", "text/yaml", "text/x-yaml"])],
  ["toml", new Set(["application/toml", "text/toml"])],
  ["csv", new Set(["text/csv"])],
  ["png", new Set(["image/png"])],
  ["jpg", new Set(["image/jpeg"])],
  ["jpeg", new Set(["image/jpeg"])],
  ["webp", new Set(["image/webp"])],
  ["gif", new Set(["image/gif"])],
  ["pdf", new Set(["application/pdf"])],
]);

export type AttachmentMetadata = {
  displayName: string;
  declaredContentType: string;
  declaredExtension: string;
  totalSizeBytes: number;
};

export type AttachmentTransferProgress = {
  fileIndex: number;
  fileCount: number;
  fileName: string;
  fileBytesSent: number;
  fileSizeBytes: number;
  totalBytesSent: number;
  totalSizeBytes: number;
};

export type AttachmentTransferSender = {
  begin(request: AttachmentBeginRequest, signal: AbortSignal): Promise<void>;
  chunk(request: AttachmentChunkRequest, signal: AbortSignal): Promise<void>;
  complete(transferId: Uint8Array, signal: AbortSignal): Promise<void>;
  cancel(transferId: Uint8Array): Promise<void>;
};

export function describeAttachment(file: File): AttachmentMetadata {
  if (file.size <= 0) {
    throw new Error(`${file.name || "Selected file"} is empty`);
  }
  if (file.size > attachmentMaxFileBytes) {
    throw new Error(`${file.name || "Selected file"} exceeds the 25 MB limit`);
  }
  if (!file.name || new TextEncoder().encode(file.name).byteLength > 255 || containsControlCharacter(file.name)) {
    throw new Error("Attachment name is invalid");
  }

  const extension = extensionFromName(file.name);
  const allowedTypes = attachmentContentTypes.get(extension);
  if (!allowedTypes) {
    throw new Error(`${file.name} has an unsupported file type`);
  }

  const contentType = normalizeContentType(file.type, attachmentTextExtensions.has(extension));
  if (!contentType || !allowedTypes.has(contentType.mediaType)) {
    throw new Error(`${file.name} has an unsupported or mismatched content type`);
  }

  return {
    displayName: file.name,
    declaredContentType: contentType.declaration,
    declaredExtension: extension,
    totalSizeBytes: file.size,
  };
}

export function validateAttachmentSelection(files: readonly File[]): AttachmentMetadata[] {
  if (files.length === 0) {
    throw new Error("Select at least one file");
  }
  if (files.length > attachmentMaxFilesPerAction) {
    throw new Error(`Select no more than ${attachmentMaxFilesPerAction} files at once`);
  }
  const metadata = files.map(describeAttachment);
  const totalSizeBytes = metadata.reduce((total, item) => total + item.totalSizeBytes, 0);
  if (totalSizeBytes > attachmentMaxSelectionBytes) {
    throw new Error("Selected files exceed the 100 MB session limit");
  }
  return metadata;
}

export async function transferAttachments(
  files: readonly File[],
  sender: AttachmentTransferSender,
  signal: AbortSignal,
  onProgress: (progress: AttachmentTransferProgress) => void,
  maxChunkBytes = protocolV1MaxAttachmentChunkBytes,
): Promise<void> {
  if (!Number.isInteger(maxChunkBytes) || maxChunkBytes <= 0 || maxChunkBytes > protocolV1MaxAttachmentChunkBytes) {
    throw new Error("Attachment chunk limit is invalid");
  }
  const metadata = validateAttachmentSelection(files);
  const totalSizeBytes = metadata.reduce((total, item) => total + item.totalSizeBytes, 0);
  let completedBytes = 0;

  for (const [fileIndex, file] of files.entries()) {
    throwIfAborted(signal);
    const item = metadata[fileIndex];
    if (!item) {
      throw new Error("Attachment metadata is missing");
    }
    const transferId = crypto.getRandomValues(new Uint8Array(16));
    let began = false;

    try {
      const totalSha256 = await hashAttachment(file, signal);
      await sender.begin({
        transferId,
        displayName: item.displayName,
        declaredContentType: item.declaredContentType,
        declaredExtension: item.declaredExtension,
        totalSizeBytes: BigInt(item.totalSizeBytes),
        totalSha256,
      }, signal);
      began = true;

      for (let offset = 0; offset < file.size; offset += maxChunkBytes) {
        throwIfAborted(signal);
        const end = Math.min(file.size, offset + maxChunkBytes);
        const data = new Uint8Array(await file.slice(offset, end).arrayBuffer());
        throwIfAborted(signal);
        await sender.chunk({
          transferId,
          offsetBytes: BigInt(offset),
          data,
          chunkSha256: sha256(data),
        }, signal);
        onProgress({
          fileIndex,
          fileCount: files.length,
          fileName: file.name,
          fileBytesSent: end,
          fileSizeBytes: file.size,
          totalBytesSent: completedBytes + end,
          totalSizeBytes,
        });
      }

      throwIfAborted(signal);
      await sender.complete(transferId, signal);
      completedBytes += file.size;
    } catch (error) {
      if (began) {
        try {
          await sender.cancel(transferId);
        } catch {
          // Preserve the original transfer failure; session cleanup remains the fallback.
        }
      }
      throw error;
    }
  }
}

export function formatAttachmentBytes(bytes: number): string {
  if (bytes < 1024) return `${bytes} B`;
  const kibibytes = bytes / 1024;
  if (kibibytes < 1024) return `${kibibytes.toFixed(kibibytes >= 10 ? 0 : 1)} KB`;
  const mebibytes = kibibytes / 1024;
  return `${mebibytes.toFixed(mebibytes >= 10 ? 0 : 1)} MB`;
}

function extensionFromName(name: string): string {
  const separator = name.lastIndexOf(".");
  if (separator <= 0 || separator === name.length - 1) {
    return "";
  }
  return name.slice(separator + 1).toLowerCase();
}

function normalizeContentType(
  value: string,
  allowUtf8Charset: boolean,
): { mediaType: string; declaration: string } | undefined {
  const [rawMediaType, ...rawParameters] = value.trim().split(";");
  const mediaType = rawMediaType?.trim().toLowerCase();
  if (!mediaType) {
    return undefined;
  }
  if (rawParameters.length === 0) {
    return { mediaType, declaration: mediaType };
  }
  if (!allowUtf8Charset || rawParameters.length !== 1) {
    return undefined;
  }

  const parameter = rawParameters[0]?.trim() ?? "";
  const separator = parameter.indexOf("=");
  if (separator <= 0 || parameter.indexOf("=", separator + 1) !== -1) {
    return undefined;
  }
  const name = parameter.slice(0, separator).trim().toLowerCase();
  let parameterValue = parameter.slice(separator + 1).trim().toLowerCase();
  if (parameterValue.startsWith('"') && parameterValue.endsWith('"') && parameterValue.length >= 2) {
    parameterValue = parameterValue.slice(1, -1);
  }
  if (name !== "charset" || parameterValue !== "utf-8") {
    return undefined;
  }
  return { mediaType, declaration: `${mediaType}; charset=utf-8` };
}

function containsControlCharacter(value: string): boolean {
  return Array.from(value).some((character) => {
    const codePoint = character.codePointAt(0);
    return codePoint !== undefined && (codePoint <= 0x1f || (codePoint >= 0x7f && codePoint <= 0x9f));
  });
}

async function hashAttachment(file: File, signal: AbortSignal): Promise<Uint8Array> {
  const hasher = sha256.create();
  try {
    for (let offset = 0; offset < file.size; offset += attachmentHashReadBytes) {
      throwIfAborted(signal);
      const end = Math.min(file.size, offset + attachmentHashReadBytes);
      const data = new Uint8Array(await file.slice(offset, end).arrayBuffer());
      throwIfAborted(signal);
      hasher.update(data);
    }
    return hasher.digest();
  } finally {
    hasher.destroy();
  }
}

function throwIfAborted(signal: AbortSignal) {
  if (signal.aborted) {
    throw new DOMException("Attachment transfer was cancelled", "AbortError");
  }
}