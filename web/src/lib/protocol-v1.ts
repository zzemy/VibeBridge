import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  AcknowledgementSchema,
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
  TerminalInputSchema,
  type Envelope,
} from "../gen/vibebridge/v1/envelope_pb";

export const protocolV1WebSocketSubprotocol = "vibebridge.v1";
export const protocolV1Major = 1;
export const protocolV1Minor = 0;
export const protocolV1MaxEnvelopeBytes = 64 * 1024;
export const terminalBinaryOutputCapability = "terminal.binary_output";
export const terminalSequencedIoCapability = "terminal.sequenced_io_v1";
export const protocolV1MaxTerminalInputBytes = 32 * 1024;

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
        capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability],
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

  return {
    protocolMajor: protocolV1Major,
    protocolMinor: protocolV1Minor,
    maxEnvelopeBytes: hello.maxEnvelopeBytes,
    capabilities,
  };
}


export type AgentStreamMessage =
  | { type: "terminal-output"; data: Uint8Array }
  | { type: "acknowledgement" };

/** Owns connection-local sequence and acknowledgement state after Hello. */
export class ProtocolV1ClientStream {
  private nextOutbound = 2n;
  private nextInbound = 2n;
  private highestInbound = 1n;
  private highestPeerAcknowledgement = 0n;
  private readonly connectionId: Uint8Array;
  private readonly peerMaxEnvelopeBytes: number;

  constructor(connectionId: Uint8Array, peerMaxEnvelopeBytes: number) {
    assertConnectionId(connectionId);
    if (!Number.isInteger(peerMaxEnvelopeBytes) || peerMaxEnvelopeBytes <= 0) {
      throw new Error("Peer max envelope bytes must be a positive integer");
    }
    this.connectionId = connectionId.slice();
    this.peerMaxEnvelopeBytes = peerMaxEnvelopeBytes;
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
    if (envelope.sessionId.byteLength !== 0 || envelope.sessionGeneration !== 0n) {
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
      case "terminalOutput":
        if (envelope.payload.value.data.byteLength === 0) {
          throw new Error("Terminal output must not be empty");
        }
        message = { type: "terminal-output", data: envelope.payload.value.data.slice() };
        break;
      case "acknowledgement":
        message = { type: "acknowledgement" };
        break;
      default:
        throw new Error("Agent envelope contains an unsupported payload");
    }

    this.highestPeerAcknowledgement = envelope.acknowledge;
    this.highestInbound = envelope.sequence;
    this.nextInbound += 1n;
    return message;
  }

  private encode(payload: Envelope["payload"], sentAt: Date): Uint8Array {
    const envelope = create(EnvelopeSchema, {
      protocolMajor: protocolV1Major,
      protocolMinor: protocolV1Minor,
      connectionId: this.connectionId,
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

function assertConnectionId(connectionId: Uint8Array) {
  if (connectionId.byteLength !== connectionIdBytes) {
    throw new Error(`Connection ID must be ${connectionIdBytes} bytes`);
  }
}

function equalBytes(left: Uint8Array, right: Uint8Array) {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}
