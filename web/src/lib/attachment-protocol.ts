import { ErrorCode } from "../gen/vibebridge/v1/envelope_pb";
import type { AttachmentTransferSender } from "./attachments";
import {
  type AgentStreamMessage,
  type AttachmentBeginRequest,
  type AttachmentChunkRequest,
  ProtocolV1ClientStream,
  type SequencedClientEnvelope,
} from "./protocol-v1";

type PendingAttachmentOperation = {
  sequence: bigint;
  resolve: () => void;
  reject: (reason: unknown) => void;
  signal?: AbortSignal;
  abortListener?: () => void;
};

type AcknowledgedAttachmentSenderOptions = {
  send: (encoded: Uint8Array) => void;
  requestRecovery: () => void;
};

/**
 * Serializes attachment operations and resolves each one only after the Agent
 * cumulatively acknowledges its client sequence. An explicit Agent rejection
 * poisons the physical stream because that failed sequence was not committed.
 */
export class AcknowledgedAttachmentSender implements AttachmentTransferSender {
  private readonly stream: ProtocolV1ClientStream;
  private readonly options: AcknowledgedAttachmentSenderOptions;
  private pending: PendingAttachmentOperation | null = null;
  private unavailable = false;
  private recoveryRequested = false;

  constructor(stream: ProtocolV1ClientStream, options: AcknowledgedAttachmentSenderOptions) {
    this.stream = stream;
    this.options = options;
  }

  begin(request: AttachmentBeginRequest, signal: AbortSignal): Promise<void> {
    return this.sendOperation(() => this.stream.createAttachmentBegin(request), signal);
  }

  chunk(request: AttachmentChunkRequest, signal: AbortSignal): Promise<void> {
    return this.sendOperation(() => this.stream.createAttachmentChunk(request), signal);
  }

  complete(transferId: Uint8Array, signal: AbortSignal): Promise<void> {
    return this.sendOperation(() => this.stream.createAttachmentComplete(transferId), signal);
  }

  cancel(transferId: Uint8Array): Promise<void> {
    return this.sendOperation(() => this.stream.createAttachmentCancel(transferId));
  }

  /** Applies the acknowledgement metadata from one already-accepted Agent envelope. */
  acceptAgentMessage(message: AgentStreamMessage): void {
    const acknowledged = this.stream.highestAcknowledgedClientSequence();

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
    if (pending && acknowledged >= pending.sequence) {
      this.resolvePending();
      return;
    }

    if (pending && (message.type === "error" || message.type === "process-exit")) {
      this.failPending(new Error("Connection ended before the Agent acknowledged the attachment operation"), true);
    }
  }

  /** Rejects an outstanding operation when its physical WebSocket closes. */
  disconnect(): void {
    this.unavailable = true;
    this.rejectPending(new Error("Connection closed before the Agent acknowledged the attachment operation"));
  }

  private sendOperation(
    createEnvelope: () => SequencedClientEnvelope,
    signal?: AbortSignal,
  ): Promise<void> {
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
      envelope = createEnvelope();
    } catch (cause) {
      return Promise.reject(cause);
    }

    return new Promise<void>((resolve, reject) => {
      const abortListener = signal
        ? () => this.failPending(abortReason(signal), true)
        : undefined;
      this.pending = {
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
        this.failPending(cause instanceof Error ? cause : new Error("Could not queue attachment operation"), true);
      }
    });
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

function abortReason(signal: AbortSignal): Error {
  return signal.reason instanceof Error
    ? signal.reason
    : new DOMException("Attachment transfer cancelled", "AbortError");
}
