import {
  AttachmentTransferDisposition,
  ErrorCode,
} from "../gen/vibebridge/v1/envelope_pb";
import type { AttachmentTransferSender } from "./attachments";
import {
  type AgentStreamMessage,
  type AttachmentBeginRequest,
  type AttachmentChunkRequest,
  ProtocolV1ClientStream,
  type SequencedClientEnvelope,
} from "./protocol-v1";

type AttachmentOperation =
  | { kind: "begin"; request: AttachmentBeginRequest }
  | { kind: "chunk"; request: AttachmentChunkRequest }
  | { kind: "complete"; transferId: Uint8Array }
  | { kind: "cancel"; transferId: Uint8Array };

type PendingAttachmentOperation = {
  operation: AttachmentOperation;
  phase: "operation" | "status";
  sequence: bigint;
  resolve: () => void;
  reject: (reason: unknown) => void;
  signal?: AbortSignal;
  abortListener?: () => void;
};

export type AcknowledgedAttachmentSenderOptions = {
  send: (encoded: Uint8Array) => void;
  requestRecovery: () => void;
};

/**
 * Serializes attachment operations and resolves each one only after the Agent
 * cumulatively acknowledges its client sequence. A pending operation survives
 * a physical disconnect and is reconciled against session-owned Agent state on
 * the next connection before it is resolved or replayed.
 */
export class AcknowledgedAttachmentSender implements AttachmentTransferSender {
  private stream: ProtocolV1ClientStream;
  private options: AcknowledgedAttachmentSenderOptions;
  private pending: PendingAttachmentOperation | null = null;
  private unavailable = false;
  private recoveryRequested = false;

  constructor(stream: ProtocolV1ClientStream, options: AcknowledgedAttachmentSenderOptions) {
    this.stream = stream;
    this.options = options;
  }

  begin(request: AttachmentBeginRequest, signal: AbortSignal): Promise<void> {
    return this.sendOperation({ kind: "begin", request }, signal);
  }

  chunk(request: AttachmentChunkRequest, signal: AbortSignal): Promise<void> {
    return this.sendOperation({ kind: "chunk", request }, signal);
  }

  complete(transferId: Uint8Array, signal: AbortSignal): Promise<void> {
    return this.sendOperation({ kind: "complete", transferId }, signal);
  }

  cancel(transferId: Uint8Array): Promise<void> {
    return this.sendOperation({ kind: "cancel", transferId });
  }

  /** Applies the acknowledgement metadata from one already-accepted Agent envelope. */
  acceptAgentMessage(message: AgentStreamMessage): void {
    const acknowledged = this.stream.highestAcknowledgedClientSequence();

    if (message.type === "attachment-transfer-status") {
      this.acceptTransferStatus(message, acknowledged);
      return;
    }

    if (message.type === "error" && message.code === ErrorCode.ATTACHMENT_TRANSFER_FAILED) {
      const pending = this.pending;
      if (pending && acknowledged + 1n === pending.sequence) {
        this.failPending(new Error("Agent rejected the current attachment operation; reconnect and retry the file"), true);
      } else {
        this.failPending(new Error("Agent attachment failure did not match the in-flight operation"), true);
      }
      return;
    }

    const pending = this.pending;
    if (pending?.phase === "operation" && acknowledged >= pending.sequence) {
      this.resolvePending();
      return;
    }

    if (pending && (message.type === "error" || message.type === "process-exit")) {
      this.failPending(new Error("Connection ended before the Agent acknowledged the attachment operation"), true);
    }
  }

  /** Marks the current physical connection unavailable while retaining ambiguous state. */
  disconnect(): void {
    this.unavailable = true;
  }

  /** Binds a new physical stream and reconciles any operation with a lost acknowledgement. */
  reconnect(stream: ProtocolV1ClientStream, options: AcknowledgedAttachmentSenderOptions): void {
    this.stream = stream;
    this.options = options;
    this.unavailable = false;
    this.recoveryRequested = false;
    const pending = this.pending;
    if (!pending) {
      return;
    }
    try {
      const envelope = this.stream.createAttachmentTransferStatusRequest(transferIdOf(pending.operation));
      pending.phase = "status";
      pending.sequence = envelope.sequence;
      this.options.send(envelope.encoded);
    } catch (cause) {
      this.failPending(asSendError(cause), true);
    }
  }

  /** Permanently rejects pending work when the owning application is disposed. */
  dispose(): void {
    this.unavailable = true;
    this.rejectPending(new Error("Attachment transfer was disposed before completion"));
  }

  private sendOperation(operation: AttachmentOperation, signal?: AbortSignal): Promise<void> {
    if (this.unavailable) {
      return Promise.reject(new Error("Attachment connection is recovering"));
    }
    if (this.pending) {
      return Promise.reject(new Error("Another attachment operation is awaiting Agent acknowledgement"));
    }
    if (signal?.aborted) {
      return Promise.reject(abortReason(signal));
    }

    let envelope: SequencedClientEnvelope;
    try {
      envelope = this.createOperationEnvelope(operation);
    } catch (cause) {
      return Promise.reject(cause);
    }

    return new Promise<void>((resolve, reject) => {
      const abortListener = signal
        ? () => this.failPending(abortReason(signal), true)
        : undefined;
      this.pending = {
        operation,
        phase: "operation",
        sequence: envelope.sequence,
        resolve,
        reject,
        signal,
        abortListener,
      };
      if (signal && abortListener) {
        signal.addEventListener("abort", abortListener, { once: true });
      }

      try {
        this.options.send(envelope.encoded);
      } catch (cause) {
        this.failPending(asSendError(cause), true);
      }
    });
  }

  private acceptTransferStatus(
    message: Extract<AgentStreamMessage, { type: "attachment-transfer-status" }>,
    acknowledged: bigint,
  ): void {
    const pending = this.pending;
    if (!pending || pending.phase !== "status" || acknowledged < pending.sequence
      || !equalBytes(message.transferId, transferIdOf(pending.operation))) {
      this.failPending(new Error("Agent attachment status did not match the operation being reconciled"), true);
      return;
    }

    const operation = pending.operation;
    switch (operation.kind) {
      case "begin":
        if (message.disposition === AttachmentTransferDisposition.ACTIVE && message.nextOffsetBytes === 0n) {
          this.resolvePending();
        } else if (message.disposition === AttachmentTransferDisposition.UNKNOWN) {
          this.replayPending();
        } else {
          this.failReconciliation();
        }
        break;
      case "chunk": {
        const start = operation.request.offsetBytes;
        const end = start + BigInt(operation.request.data.byteLength);
        if (message.disposition !== AttachmentTransferDisposition.ACTIVE) {
          this.failReconciliation();
        } else if (message.nextOffsetBytes === end) {
          this.resolvePending();
        } else if (message.nextOffsetBytes === start) {
          this.replayPending();
        } else {
          this.failReconciliation();
        }
        break;
      }
      case "complete":
        if (message.disposition === AttachmentTransferDisposition.COMPLETED) {
          this.resolvePending();
        } else if (message.disposition === AttachmentTransferDisposition.ACTIVE) {
          this.replayPending();
        } else {
          this.failReconciliation();
        }
        break;
      case "cancel":
        if (
          message.disposition === AttachmentTransferDisposition.CANCELLED
          || message.disposition === AttachmentTransferDisposition.UNKNOWN
          || message.disposition === AttachmentTransferDisposition.COMPLETED
        ) {
          this.resolvePending();
        } else if (message.disposition === AttachmentTransferDisposition.ACTIVE) {
          this.replayPending();
        } else {
          this.failReconciliation();
        }
        break;
    }
  }

  private replayPending(): void {
    const pending = this.pending;
    if (!pending) return;
    try {
      const envelope = this.createOperationEnvelope(pending.operation);
      pending.phase = "operation";
      pending.sequence = envelope.sequence;
      this.options.send(envelope.encoded);
    } catch (cause) {
      this.failPending(asSendError(cause), true);
    }
  }

  private createOperationEnvelope(operation: AttachmentOperation): SequencedClientEnvelope {
    switch (operation.kind) {
      case "begin": return this.stream.createAttachmentBegin(operation.request);
      case "chunk": return this.stream.createAttachmentChunk(operation.request);
      case "complete": return this.stream.createAttachmentComplete(operation.transferId);
      case "cancel": return this.stream.createAttachmentCancel(operation.transferId);
    }
  }

  private failReconciliation(): void {
    this.failPending(new Error("Agent attachment state cannot safely reconcile the pending operation"), true);
  }

  private resolvePending(): void {
    const pending = this.takePending();
    pending?.resolve();
  }

  private rejectPending(reason: unknown): void {
    const pending = this.takePending();
    pending?.reject(reason);
  }

  private failPending(reason: Error, recover: boolean): void {
    this.unavailable = true;
    this.rejectPending(reason);
    if (recover) {
      this.requestRecovery();
    }
  }

  private takePending(): PendingAttachmentOperation | null {
    const pending = this.pending;
    this.pending = null;
    if (pending?.signal && pending.abortListener) {
      pending.signal.removeEventListener("abort", pending.abortListener);
    }
    return pending;
  }

  private requestRecovery(): void {
    if (this.recoveryRequested) {
      return;
    }
    this.recoveryRequested = true;
    this.options.requestRecovery();
  }
}

function transferIdOf(operation: AttachmentOperation): Uint8Array {
  return operation.kind === "begin" || operation.kind === "chunk"
    ? operation.request.transferId
    : operation.transferId;
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}

function asSendError(cause: unknown): Error {
  return cause instanceof Error ? cause : new Error("Could not queue attachment operation");
}

function abortReason(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException("Attachment transfer cancelled", "AbortError");
}
