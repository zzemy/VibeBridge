import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  AcknowledgementSchema,
  AttachSessionSchema,
  AttachmentBeginSchema,
  AttachmentCancelSchema,
  AttachmentChunkSchema,
  AttachmentCompleteSchema,
  EndSessionSchema,
  EnvelopeSchema,
  ErrorCode,
  HelloSchema,
  PeerRole,
  PingSchema,
  ProcessExitOutcome,
  ProtocolVersionRangeSchema,
  ResumeDisposition,
  ProtocolVersionSchema,
  TerminalInputSchema,
  TerminalResizeSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";

export const protocolV1WebSocketSubprotocol = "vibebridge.v1";
export const protocolV1Major = 1;
export const protocolV1Minor = 0;
export const protocolV1MaxEnvelopeBytes = 64 * 1024;
export const terminalBinaryOutputCapability = "terminal.binary_output";
export const terminalSequencedIoCapability = "terminal.sequenced_io_v1";
export const terminalResizeEndCapability = "terminal.resize_end_v1";
export const sessionProcessExitCapability = "session.process_exit_v1";
export const sessionResumeCapability = "session.resume_v1";
export const controlErrorCapability = "control.error_v1";
export const controlHealthCapability = "control.health_v1";
export const attachmentTransferCapability = "attachment.transfer_v1";
export const protocolV1MaxTerminalInputBytes = 32 * 1024;
// Keep chunk payloads below the 64 KiB envelope ceiling after protobuf framing.
export const protocolV1MaxAttachmentChunkBytes = 48 * 1024;
export const protocolV1MaxTerminalDimension = 65_535;

// Transfer IDs, hashes, session metadata, timestamps, and protobuf tags remain outside the data payload.
const attachmentEnvelopeFramingReserveBytes = 1024;

const connectionIdBytes = 16;
const maxCapabilities = 64;
const maxCapabilityLength = 128;
const maxUint64 = (1n << 64n) - 1n;
const maxAttachmentTransferIdBytes = 64;
const sha256Bytes = 32;

export type NegotiatedAgentHello = {
  protocolMajor: number;
  protocolMinor: number;
  maxEnvelopeBytes: number;
  capabilities: ReadonlySet<string>;
};

export function newProtocolV1ConnectionId(): Uint8Array {
  return crypto.getRandomValues(new Uint8Array(connectionIdBytes));
}

export function createClientHello(connectionId: Uint8Array, sentAt = new Date()): Uint8Array {
  assertConnectionId(connectionId);
  const version = () => create(ProtocolVersionSchema, { major: protocolV1Major, minor: protocolV1Minor });
  const envelope = create(EnvelopeSchema, {
    protocolMajor: protocolV1Major,
    protocolMinor: protocolV1Minor,
    connectionId,
    sequence: 1n,
    sentAt: timestampFromDate(sentAt),
    payload: {
      case: "hello",
      value: create(HelloSchema, {
        peerRole: PeerRole.CLIENT,
        supportedVersions: create(ProtocolVersionRangeSchema, {
          minimum: version(),
          maximum: version(),
        }),
        capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, terminalResizeEndCapability, sessionProcessExitCapability, sessionResumeCapability, controlErrorCapability, controlHealthCapability],
        maxEnvelopeBytes: protocolV1MaxEnvelopeBytes,
      }),
    },
  });
  return toBinary(EnvelopeSchema, envelope);
}

export function acceptAgentHello(encoded: Uint8Array, expectedConnectionId: Uint8Array): NegotiatedAgentHello {
  assertConnectionId(expectedConnectionId);
  if (encoded.byteLength === 0 || encoded.byteLength > protocolV1MaxEnvelopeBytes) {
    throw new Error(`Hello envelope size must be between 1 and ${protocolV1MaxEnvelopeBytes} bytes`);
  }

  const envelope = fromBinary(EnvelopeSchema, encoded);
  if (envelope.protocolMajor !== protocolV1Major || envelope.protocolMinor !== protocolV1Minor) {
    throw new Error("Agent selected an unsupported protocol version");
  }
  if (!equalBytes(envelope.connectionId, expectedConnectionId)) {
    throw new Error("Agent Hello connection ID does not match the client connection");
  }
  if (envelope.sessionId.byteLength !== 0 || envelope.sessionGeneration !== 0n || envelope.sequence !== 1n || envelope.acknowledge !== 0n) {
    throw new Error("Agent Hello has invalid session or sequence metadata");
  }
  if (envelope.payload.case !== "hello") {
    throw new Error("First Protocol V1 envelope from the Agent must contain Hello");
  }

  const hello = envelope.payload.value;
  if (hello.peerRole !== PeerRole.AGENT) {
    throw new Error("Hello peer role must be Agent");
  }
  if (hello.maxEnvelopeBytes <= 0) {
    throw new Error("Hello max envelope bytes must be positive");
  }
  const minimum = hello.supportedVersions?.minimum;
  const maximum = hello.supportedVersions?.maximum;
  if (
    !minimum ||
    !maximum ||
    minimum.major !== maximum.major ||
    minimum.major !== protocolV1Major ||
    minimum.minor > maximum.minor ||
    protocolV1Minor < minimum.minor ||
    protocolV1Minor > maximum.minor
  ) {
    throw new Error("Hello supported version range is invalid or incompatible");
  }

  if (hello.capabilities.length > maxCapabilities) {
    throw new Error(`Hello has more than ${maxCapabilities} capabilities`);
  }
  const capabilities = new Set<string>();
  for (const capability of hello.capabilities) {
    if (!capability || capability.length > maxCapabilityLength || capability.trim() !== capability) {
      throw new Error("Hello contains an invalid capability name");
    }
    if (capabilities.has(capability)) {
      throw new Error("Hello contains a duplicate capability");
    }
    capabilities.add(capability);
  }
  for (const dependent of [terminalResizeEndCapability, sessionProcessExitCapability, controlErrorCapability, controlHealthCapability, attachmentTransferCapability]) {
    if (capabilities.has(dependent) && !capabilities.has(terminalSequencedIoCapability)) {
      throw new Error(`${dependent} requires ${terminalSequencedIoCapability}`);
    }
  }
  if (capabilities.has(attachmentTransferCapability) && !capabilities.has(controlErrorCapability)) {
    throw new Error(`${attachmentTransferCapability} requires ${controlErrorCapability}`);
  }

  return {
    protocolMajor: protocolV1Major,
    protocolMinor: protocolV1Minor,
    maxEnvelopeBytes: hello.maxEnvelopeBytes,
    capabilities,
  };
}


export type SessionResumeCursor = {
  sessionId: Uint8Array;
  sessionGeneration: bigint;
  lastAcknowledgedSequence: bigint;
};

export type AgentStreamMessage =
  | { type: "session-status"; disposition: ResumeDisposition; sessionId: Uint8Array; sessionGeneration: bigint }
  | { type: "terminal-output"; data: Uint8Array }
  | { type: "process-exit"; outcome: ProcessExitOutcome }
  | { type: "error"; code: ErrorCode }
  | { type: "pong" }
  | { type: "acknowledgement" };

export type AttachmentBeginRequest = {
  transferId: Uint8Array;
  displayName: string;
  declaredContentType: string;
  declaredExtension: string;
  totalSizeBytes: bigint;
  totalSha256: Uint8Array;
};

export type AttachmentChunkRequest = {
  transferId: Uint8Array;
  offsetBytes: bigint;
  data: Uint8Array;
  chunkSha256: Uint8Array;
};

type ProtocolV1ClientStreamOptions = {
  sessionResume?: boolean;
  sessionProcessExit?: boolean;
  terminalResizeEnd?: boolean;
  controlError?: boolean;
  controlHealth?: boolean;
  attachmentTransfer?: boolean;
};

/** Owns connection-local sequence and acknowledgement state after Hello. */
export class ProtocolV1ClientStream {
  private nextOutbound = 2n;
  private nextInbound = 2n;
  private highestInbound = 1n;
  private highestPeerAcknowledgement = 0n;
  private readonly connectionId: Uint8Array;
  private readonly peerMaxEnvelopeBytes: number;
  private readonly sessionResume: boolean;
  private readonly sessionProcessExit: boolean;
  private readonly terminalResizeEnd: boolean;
  private readonly controlError: boolean;
  private readonly controlHealth: boolean;
  private readonly attachmentTransfer: boolean;
  private readonly outstandingPingSequences: bigint[] = [];
  private sessionBound = false;
  private sessionId = new Uint8Array();
  private sessionGeneration = 0n;

  constructor(connectionId: Uint8Array, peerMaxEnvelopeBytes: number, options: ProtocolV1ClientStreamOptions = {}) {
    assertConnectionId(connectionId);
    if (!Number.isInteger(peerMaxEnvelopeBytes) || peerMaxEnvelopeBytes <= 0) {
      throw new Error("Peer max envelope bytes must be a positive integer");
    }
    this.connectionId = connectionId.slice();
    this.peerMaxEnvelopeBytes = peerMaxEnvelopeBytes;
    this.sessionResume = options.sessionResume === true;
    this.sessionProcessExit = options.sessionProcessExit === true;
    this.terminalResizeEnd = options.terminalResizeEnd === true;
    this.controlError = options.controlError === true;
    this.controlHealth = options.controlHealth === true;
    this.attachmentTransfer = options.attachmentTransfer === true;
  }

  /** Reports whether control.health_v1 was negotiated for this physical connection. */
  usesControlHealth(): boolean {
    return this.controlHealth;
  }

  /**
   * Encodes an ordered application Ping at `sentAt` (now by default).
   * Requires control.health_v1 and, for resumable streams, SessionStatus.
   * The returned bytes are ready for one binary WebSocket message; invalid
   * capability, stream state, or negotiated size throws without queuing a Ping.
   */
  createPing(sentAt = new Date()): Uint8Array {
    if (!this.controlHealth) {
      throw new Error("Control health was not negotiated");
    }
    const sequence = this.nextOutbound;
    const encoded = this.encode({
      case: "ping",
      value: create(PingSchema),
    }, sentAt);
    this.outstandingPingSequences.push(sequence);
    return encoded;
  }

  /** Reports whether control.error_v1 was negotiated for this physical connection. */
  usesControlError(): boolean {
    return this.controlError;
  }

  createAttachSession(cursor?: SessionResumeCursor, sentAt = new Date()): Uint8Array {
    if (!this.sessionResume) {
      throw new Error("Session resume was not negotiated");
    }
    if (this.nextOutbound !== 2n || this.sessionBound) {
      throw new Error("AttachSession has already been sent");
    }
    if (cursor) {
      assertSessionIdentity(cursor.sessionId, cursor.sessionGeneration);
      if (cursor.lastAcknowledgedSequence <= 0n) {
        throw new Error("Resume cursor must acknowledge a positive Agent sequence");
      }
    }
    return this.encode({
      case: "attachSession",
      value: create(AttachSessionSchema, { lastAcknowledgedSequence: cursor?.lastAcknowledgedSequence ?? 0n }),
    }, sentAt, cursor?.sessionId, cursor?.sessionGeneration ?? 0n, true);
  }

  getResumeCursor(): SessionResumeCursor | null {
    if (!this.sessionResume || !this.sessionBound) {
      return null;
    }
    return {
      sessionId: this.sessionId.slice(),
      sessionGeneration: this.sessionGeneration,
      lastAcknowledgedSequence: this.highestInbound,
    };
  }

  /** Reports whether attachment.transfer_v1 was negotiated for this physical connection. */
  usesAttachmentTransfer(): boolean {
    return this.attachmentTransfer;
  }

  /** Returns a conservative data payload limit under the peer's downward-negotiated envelope ceiling. */
  maxAttachmentChunkBytes(): number {
    this.assertAttachmentTransfer();
    const envelopeLimit = Math.min(protocolV1MaxEnvelopeBytes, this.peerMaxEnvelopeBytes);
    const availablePayloadBytes = envelopeLimit - attachmentEnvelopeFramingReserveBytes;
    if (availablePayloadBytes <= 0) {
      throw new Error("Negotiated envelope limit is too small for attachment chunks");
    }
    return Math.min(protocolV1MaxAttachmentChunkBytes, availablePayloadBytes);
  }

  createAttachmentBegin(request: AttachmentBeginRequest, sentAt = new Date()): Uint8Array {
    this.assertAttachmentTransfer();
    assertAttachmentTransferId(request.transferId);
    if (
      request.totalSizeBytes <= 0n
      || request.totalSizeBytes > maxUint64
      || request.totalSha256.byteLength !== sha256Bytes
      || !request.displayName
      || !request.declaredContentType
      || !request.declaredExtension
    ) {
      throw new Error("Attachment metadata has an invalid size, checksum, or declaration");
    }
    return this.encode({
      case: "attachmentBegin",
      value: create(AttachmentBeginSchema, {
        transferId: request.transferId,
        displayName: request.displayName,
        declaredContentType: request.declaredContentType,
        declaredExtension: request.declaredExtension,
        totalSizeBytes: request.totalSizeBytes,
        totalSha256: request.totalSha256,
      }),
    }, sentAt);
  }

  createAttachmentChunk(request: AttachmentChunkRequest, sentAt = new Date()): Uint8Array {
    this.assertAttachmentTransfer();
    assertAttachmentTransferId(request.transferId);
    if (
      request.offsetBytes < 0n
      || request.offsetBytes > maxUint64
      || request.data.byteLength === 0
      || request.data.byteLength > protocolV1MaxAttachmentChunkBytes
      || request.chunkSha256.byteLength !== sha256Bytes
    ) {
      throw new Error("Attachment chunk has an invalid offset, size, or checksum");
    }
    return this.encode({
      case: "attachmentChunk",
      value: create(AttachmentChunkSchema, {
        transferId: request.transferId,
        offsetBytes: request.offsetBytes,
        data: request.data,
        chunkSha256: request.chunkSha256,
      }),
    }, sentAt);
  }

  createAttachmentComplete(transferId: Uint8Array, sentAt = new Date()): Uint8Array {
    this.assertAttachmentTransfer();
    assertAttachmentTransferId(transferId);
    return this.encode({
      case: "attachmentComplete",
      value: create(AttachmentCompleteSchema, { transferId }),
    }, sentAt);
  }

  createAttachmentCancel(transferId: Uint8Array, sentAt = new Date()): Uint8Array {
    this.assertAttachmentTransfer();
    assertAttachmentTransferId(transferId);
    return this.encode({
      case: "attachmentCancel",
      value: create(AttachmentCancelSchema, { transferId }),
    }, sentAt);
  }

  createTerminalInput(data: string, sentAt = new Date()): Uint8Array {
    const encodedInput = new TextEncoder().encode(data);
    if (encodedInput.byteLength === 0 || encodedInput.byteLength > protocolV1MaxTerminalInputBytes) {
      throw new Error(`Terminal input size must be between 1 and ${protocolV1MaxTerminalInputBytes} bytes`);
    }
    return this.encode({
      case: "terminalInput",
      value: create(TerminalInputSchema, { data: encodedInput }),
    }, sentAt);
  }

  /** Reports whether session.process_exit_v1 was negotiated for this physical connection. */
  usesSessionProcessExit(): boolean {
    return this.sessionProcessExit;
  }

  /** Reports whether terminal.resize_end_v1 was negotiated for this physical connection. */
  usesTerminalResizeEnd(): boolean {
    return this.terminalResizeEnd;
  }

  createTerminalResize(columns: number, rows: number, sentAt = new Date()): Uint8Array {
    if (!this.terminalResizeEnd) {
      throw new Error("Terminal resize/end was not negotiated");
    }
    if (!isTerminalDimension(columns) || !isTerminalDimension(rows)) {
      throw new Error(`Terminal dimensions must be integers between 1 and ${protocolV1MaxTerminalDimension}`);
    }
    return this.encode({
      case: "terminalResize",
      value: create(TerminalResizeSchema, { columns, rows }),
    }, sentAt);
  }

  createEndSession(sentAt = new Date()): Uint8Array {
    if (!this.terminalResizeEnd) {
      throw new Error("Terminal resize/end was not negotiated");
    }
    return this.encode({
      case: "endSession",
      value: create(EndSessionSchema),
    }, sentAt);
  }

  createAcknowledgement(sentAt = new Date()): Uint8Array {
    return this.encode({
      case: "acknowledgement",
      value: create(AcknowledgementSchema),
    }, sentAt);
  }

  acceptAgentMessage(encoded: Uint8Array): AgentStreamMessage {
    if (encoded.byteLength === 0 || encoded.byteLength > protocolV1MaxEnvelopeBytes) {
      throw new Error(`Envelope size must be between 1 and ${protocolV1MaxEnvelopeBytes} bytes`);
    }
    const envelope = fromBinary(EnvelopeSchema, encoded);
    if (envelope.protocolMajor !== protocolV1Major || envelope.protocolMinor !== protocolV1Minor) {
      throw new Error("Agent envelope uses an unsupported protocol version");
    }
    if (!equalBytes(envelope.connectionId, this.connectionId)) {
      throw new Error("Agent envelope connection ID does not match");
    }
    if (this.sessionResume) {
      if (this.sessionBound) {
        if (!equalBytes(envelope.sessionId, this.sessionId) || envelope.sessionGeneration !== this.sessionGeneration) {
          throw new Error("Agent envelope session metadata does not match the bound session");
        }
      } else if (envelope.payload.case === "sessionStatus") {
        assertSessionIdentity(envelope.sessionId, envelope.sessionGeneration);
      } else if (envelope.payload.case === "error" && this.controlError) {
        if (envelope.sessionId.byteLength !== 0 || envelope.sessionGeneration !== 0n) {
          throw new Error("Pre-bind Error must use empty session metadata");
        }
      } else {
        throw new Error("First Agent stream message must contain SessionStatus or a negotiated Error");
      }
    } else if (envelope.sessionId.byteLength !== 0 || envelope.sessionGeneration !== 0n) {
      throw new Error("Connection-local envelope contains session metadata");
    }
    if (envelope.sequence !== this.nextInbound) {
      throw new Error(`Agent sequence = ${envelope.sequence}, expected ${this.nextInbound}`);
    }
    if (envelope.acknowledge < this.highestPeerAcknowledgement || envelope.acknowledge >= this.nextOutbound) {
      throw new Error("Agent acknowledgement is invalid or refers to an unsent sequence");
    }

    let message: AgentStreamMessage;
    switch (envelope.payload.case) {
      case "sessionStatus":
        if (!this.sessionResume || this.sessionBound || !isKnownResumeDisposition(envelope.payload.value.resumeDisposition)) {
          throw new Error("SessionStatus is not valid for this stream state");
        }
        message = {
          type: "session-status",
          disposition: envelope.payload.value.resumeDisposition,
          sessionId: envelope.sessionId.slice(),
          sessionGeneration: envelope.sessionGeneration,
        };
        break;
      case "terminalOutput":
        if (envelope.payload.value.data.byteLength === 0) {
          throw new Error("Terminal output must not be empty");
        }
        message = { type: "terminal-output", data: envelope.payload.value.data.slice() };
        break;
      case "processExit":
        if (!this.sessionProcessExit || !isKnownProcessExitOutcome(envelope.payload.value.outcome)) {
          throw new Error("ProcessExit is not valid for this stream state");
        }
        message = { type: "process-exit", outcome: envelope.payload.value.outcome };
        break;
      case "error":
        if (!this.controlError || !isKnownErrorCode(envelope.payload.value.code)) {
          throw new Error("Error is not valid for this stream state");
        }
        if (this.sessionResume && !this.sessionBound && !isPreBindErrorCode(envelope.payload.value.code)) {
          throw new Error("Only session start or active-session Error is valid before SessionStatus");
        }
        message = { type: "error", code: envelope.payload.value.code };
        break;
      case "pong": {
        const pingSequence = this.outstandingPingSequences[0];
        if (!this.controlHealth || pingSequence === undefined || envelope.acknowledge < pingSequence) {
          throw new Error("Pong must acknowledge an outstanding Ping");
        }
        this.outstandingPingSequences.shift();
        message = { type: "pong" };
        break;
      }
      case "acknowledgement":
        message = { type: "acknowledgement" };
        break;
      default:
        throw new Error("Agent envelope contains an unsupported payload");
    }

    if (message.type === "session-status") {
      this.sessionId = message.sessionId.slice();
      this.sessionGeneration = message.sessionGeneration;
      this.sessionBound = true;
    }
    this.highestPeerAcknowledgement = envelope.acknowledge;
    this.highestInbound = envelope.sequence;
    this.nextInbound += 1n;
    return message;
  }

  private assertAttachmentTransfer() {
    if (!this.attachmentTransfer) {
      throw new Error("Attachment transfer was not negotiated");
    }
  }

  private encode(
    payload: Envelope["payload"],
    sentAt: Date,
    sessionId: Uint8Array<ArrayBufferLike> | undefined = this.sessionId,
    sessionGeneration = this.sessionGeneration,
    allowUnbound = false,
  ): Uint8Array {
    if (this.sessionResume && !this.sessionBound && !allowUnbound) {
      throw new Error("SessionStatus must be accepted before stream traffic");
    }
    const envelope = create(EnvelopeSchema, {
      protocolMajor: protocolV1Major,
      protocolMinor: protocolV1Minor,
      connectionId: this.connectionId,
      sessionId,
      sessionGeneration,
      sequence: this.nextOutbound,
      acknowledge: this.highestInbound,
      sentAt: timestampFromDate(sentAt),
      payload,
    });
    const encoded = toBinary(EnvelopeSchema, envelope);
    const limit = Math.min(protocolV1MaxEnvelopeBytes, this.peerMaxEnvelopeBytes);
    if (encoded.byteLength > limit) {
      throw new Error(`Client envelope exceeds negotiated ${limit}-byte limit`);
    }
    this.nextOutbound += 1n;
    return encoded;
  }
}

function assertAttachmentTransferId(transferId: Uint8Array) {
  if (transferId.byteLength === 0 || transferId.byteLength > maxAttachmentTransferIdBytes) {
    throw new Error(`Attachment transfer ID must be between 1 and ${maxAttachmentTransferIdBytes} bytes`);
  }
}

function isTerminalDimension(value: number) {
  return Number.isInteger(value) && value > 0 && value <= protocolV1MaxTerminalDimension;
}

function assertConnectionId(connectionId: Uint8Array) {
  if (connectionId.byteLength !== connectionIdBytes) {
    throw new Error(`Connection ID must be ${connectionIdBytes} bytes`);
  }
}

function assertSessionIdentity(sessionId: Uint8Array, generation: bigint) {
  if (sessionId.byteLength !== connectionIdBytes || generation <= 0n) {
    throw new Error(`Session ID must be ${connectionIdBytes} bytes and generation must be positive`);
  }
}

function isPreBindErrorCode(code: ErrorCode) {
  return code === ErrorCode.SESSION_START_FAILED || code === ErrorCode.SESSION_ALREADY_ACTIVE;
}

function isKnownErrorCode(code: ErrorCode) {
  return code === ErrorCode.SESSION_START_FAILED
    || code === ErrorCode.SESSION_ALREADY_ACTIVE
    || code === ErrorCode.TERMINAL_INPUT_FAILED
    || code === ErrorCode.TERMINAL_RESIZE_FAILED
    || code === ErrorCode.UNSUPPORTED_MESSAGE
    || code === ErrorCode.ATTACHMENT_TRANSFER_FAILED;
}

function isKnownProcessExitOutcome(outcome: ProcessExitOutcome) {
  return outcome === ProcessExitOutcome.SUCCESS || outcome === ProcessExitOutcome.FAILURE;
}

function isKnownResumeDisposition(disposition: ResumeDisposition) {
  return disposition === ResumeDisposition.FRESH
    || disposition === ResumeDisposition.RESUMED
    || disposition === ResumeDisposition.RESYNC_REQUIRED;
}

function equalBytes(left: Uint8Array, right: Uint8Array) {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}
