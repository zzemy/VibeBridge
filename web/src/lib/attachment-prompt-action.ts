import { AttachmentPromptDisposition, ErrorCode } from "../gen/vibebridge/v1/envelope_pb";
import {
  type AgentStreamMessage,
  type AttachmentPromptPrepareRequest,
  ProtocolV1ClientStream,
  type SequencedClientEnvelope,
} from "./protocol-v1";

export const attachmentPromptActionFailedMessage = "Attachment prompt action failed";

export type AttachmentPromptPrepareInput = Omit<AttachmentPromptPrepareRequest, "actionId">;

export type AttachmentPromptPreviewResult = {
  disposition: AttachmentPromptDisposition.PREPARED | AttachmentPromptDisposition.COMMITTED;
  preview: string;
  appendEnter: boolean;
};

type AttachmentPromptPhase = "preparing" | "prepared" | "committing" | "cancelling";

type ActiveAction = {
  actionId: Uint8Array;
  request: AttachmentPromptPrepareInput;
  phase: AttachmentPromptPhase;
  pendingSequence: bigint | null;
};

type Deferred<T> = {
  promise: Promise<T>;
  resolve: (value: T) => void;
  reject: (reason: unknown) => void;
};

type AttachmentPromptTransport = {
  send: (encoded: Uint8Array) => void;
  requestRecovery: () => void;
};

type AttachmentPromptActionClientOptions = {
  newActionId?: () => Uint8Array;
};

/**
 * Owns one durable, session-local attachment prompt action across physical
 * WebSocket reconnects. Files remain opaque transfer IDs until the Agent
 * prepares an exact terminal preview.
 */
export class AttachmentPromptActionClient {
  private readonly newActionId: () => Uint8Array;
  private stream: ProtocolV1ClientStream | null = null;
  private transport: AttachmentPromptTransport | null = null;
  private action: ActiveAction | null = null;
  private prepareDeferred: Deferred<AttachmentPromptPreviewResult> | null = null;
  private completionDeferred: Deferred<void> | null = null;
  private recoveryRequested = false;
  private closed = false;

  constructor(options: AttachmentPromptActionClientOptions = {}) {
    this.newActionId = options.newActionId ?? (() => crypto.getRandomValues(new Uint8Array(16)));
  }

  connect(stream: ProtocolV1ClientStream, transport: AttachmentPromptTransport): void {
    if (this.closed) {
      throw new Error("Attachment prompt action client is closed");
    }
    if (!stream.usesAttachmentPromptAction()) {
      throw new Error("Attachment prompt action was not negotiated");
    }
    if (this.stream) {
      throw new Error("Attachment prompt action transport is already connected");
    }
    this.stream = stream;
    this.transport = transport;
    this.recoveryRequested = false;
    this.sendCurrentOperation();
  }

  disconnect(stream: ProtocolV1ClientStream): void {
    if (this.stream !== stream) {
      return;
    }
    this.stream = null;
    this.transport = null;
    this.recoveryRequested = false;
    if (this.action) {
      this.action.pendingSequence = null;
    }
  }

  prepare(input: AttachmentPromptPrepareInput): Promise<AttachmentPromptPreviewResult> {
    if (this.closed) {
      return Promise.reject(new Error("Attachment prompt action client is closed"));
    }
    if (!this.stream || !this.transport) {
      return Promise.reject(new Error("Attachment prompt action transport is not connected"));
    }
    if (this.action) {
      return Promise.reject(new Error("Another attachment prompt action is active"));
    }

    const actionId = this.newActionId().slice();
    const deferred = createDeferred<AttachmentPromptPreviewResult>();
    this.action = {
      actionId,
      request: {
        transferIds: input.transferIds.map((transferId) => transferId.slice()),
        prompt: input.prompt,
        appendEnter: input.appendEnter,
      },
      phase: "preparing",
      pendingSequence: null,
    };
    this.prepareDeferred = deferred;
    this.sendCurrentOperation();
    return deferred.promise;
  }

  commit(): Promise<void> {
    return this.startCompletion("committing");
  }

  cancel(): Promise<void> {
    return this.startCompletion("cancelling");
  }

  /** Applies one message already accepted and validated by the bound stream. */
  acceptAgentMessage(message: AgentStreamMessage): void {
    const stream = this.stream;
    if (!stream) {
      return;
    }

    if (message.type === "error" && message.code === ErrorCode.ATTACHMENT_PROMPT_ACTION_FAILED) {
      this.failAction(new Error(attachmentPromptActionFailedMessage), true);
      return;
    }

    const action = this.action;
    if (
      action
      && (action.phase === "committing" || action.phase === "cancelling")
      && action.pendingSequence !== null
      && stream.highestAcknowledgedClientSequence() >= action.pendingSequence
    ) {
      const deferred = this.completionDeferred;
      this.completionDeferred = null;
      this.action = null;
      deferred?.resolve();
      return;
    }
    if (action && message.type === "process-exit") {
      this.failAction(new Error("Session ended before the attachment prompt action completed"), false);
      return;
    }
    if (!action) {
      if (message.type === "attachment-prompt-preview") {
        this.failAction(new Error("Unexpected attachment prompt preview"), true);
      }
      return;
    }

    if (message.type === "attachment-prompt-preview") {
      if (action.phase !== "preparing" || !equalBytes(message.actionId, action.actionId)) {
        this.failAction(new Error("Attachment prompt preview does not match the active action"), true);
        return;
      }
      if (action.pendingSequence === null || stream.highestAcknowledgedClientSequence() < action.pendingSequence) {
        this.failAction(new Error("Attachment prompt preview does not acknowledge the active prepare"), true);
        return;
      }
      if (message.disposition !== AttachmentPromptDisposition.PREPARED
        && message.disposition !== AttachmentPromptDisposition.COMMITTED) {
        this.failAction(new Error("Attachment prompt preview disposition is invalid"), true);
        return;
      }

      const deferred = this.prepareDeferred;
      this.prepareDeferred = null;
      action.pendingSequence = null;
      if (message.disposition === AttachmentPromptDisposition.PREPARED) {
        action.phase = "prepared";
        deferred?.resolve({
          disposition: AttachmentPromptDisposition.PREPARED,
          preview: message.preview,
          appendEnter: message.appendEnter,
        });
      } else {
        this.action = null;
        deferred?.resolve({
          disposition: AttachmentPromptDisposition.COMMITTED,
          preview: message.preview,
          appendEnter: message.appendEnter,
        });
      }
      return;
    }
  }

  resetForSessionChange(): void {
    this.failAction(new Error("Attachment prompt action belongs to a previous session"), false);
  }

  close(): void {
    if (this.closed) {
      return;
    }
    this.closed = true;
    this.stream = null;
    this.transport = null;
    this.failAction(new Error("Attachment prompt action client closed"), false);
  }

  private startCompletion(phase: "committing" | "cancelling"): Promise<void> {
    const action = this.action;
    if (this.closed) {
      return Promise.reject(new Error("Attachment prompt action client is closed"));
    }
    if (!action || action.phase !== "prepared") {
      return Promise.reject(new Error("No prepared attachment prompt action is available"));
    }
    const deferred = createDeferred<void>();
    action.phase = phase;
    action.pendingSequence = null;
    this.completionDeferred = deferred;
    this.sendCurrentOperation();
    return deferred.promise;
  }

  private sendCurrentOperation(): void {
    const stream = this.stream;
    const transport = this.transport;
    const action = this.action;
    if (!stream || !transport || !action || action.phase === "prepared" || action.pendingSequence !== null) {
      return;
    }

    let envelope: SequencedClientEnvelope;
    try {
      if (action.phase === "preparing") {
        envelope = stream.createAttachmentPromptPrepare({ actionId: action.actionId, ...action.request });
      } else if (action.phase === "committing") {
        envelope = stream.createAttachmentPromptCommit(action.actionId);
      } else {
        envelope = stream.createAttachmentPromptCancel(action.actionId);
      }
    } catch (cause) {
      this.failAction(asError(cause, "Could not create attachment prompt operation"), false);
      return;
    }

    action.pendingSequence = envelope.sequence;
    try {
      transport.send(envelope.encoded);
    } catch {
      action.pendingSequence = null;
      this.requestRecovery();
    }
  }

  private failAction(reason: Error, recover: boolean): void {
    const prepareDeferred = this.prepareDeferred;
    const completionDeferred = this.completionDeferred;
    this.prepareDeferred = null;
    this.completionDeferred = null;
    this.action = null;
    prepareDeferred?.reject(reason);
    completionDeferred?.reject(reason);
    if (recover) {
      this.requestRecovery();
    }
  }

  private requestRecovery(): void {
    if (this.recoveryRequested) {
      return;
    }
    this.recoveryRequested = true;
    this.transport?.requestRecovery();
  }
}

function createDeferred<T>(): Deferred<T> {
  let resolvePromise: (value: T) => void = () => {};
  let rejectPromise: (reason: unknown) => void = () => {};
  const promise = new Promise<T>((resolve, reject) => {
    resolvePromise = resolve;
    rejectPromise = reject;
  });
  return { promise, resolve: resolvePromise, reject: rejectPromise };
}

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}

function asError(reason: unknown, fallback: string): Error {
  return reason instanceof Error ? reason : new Error(fallback);
}
