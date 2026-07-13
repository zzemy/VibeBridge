import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  AcknowledgementSchema,
  AttachSessionSchema,
  EndSessionSchema,
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
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
export const protocolV1MaxTerminalInputBytes = 32 * 1024;
export const protocolV1MaxTerminalDimension = 65_535;

const connectionIdBytes = 16;
const maxCapabilities = 64;
const maxCapabilityLength = 128;

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
        capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, terminalResizeEndCapability, sessionProcessExitCapability, sessionResumeCapability],
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
  for (const dependent of [terminalResizeEndCapability, sessionProcessExitCapability]) {
    if (capabilities.has(dependent) && !capabilities.has(terminalSequencedIoCapability)) {
      throw new Error(`${dependent} requires ${terminalSequencedIoCapability}`);
    }
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
  | { type: "acknowledgement" };

type ProtocolV1ClientStreamOptions = {
  sessionResume?: boolean;
  sessionProcessExit?: boolean;
  terminalResizeEnd?: boolean;
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
      } else {
        if (envelope.payload.case !== "sessionStatus") {
          throw new Error("First Agent stream message must contain SessionStatus");
        }
        assertSessionIdentity(envelope.sessionId, envelope.sessionGeneration);
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
