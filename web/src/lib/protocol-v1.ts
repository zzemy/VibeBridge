import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";

import {
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
} from "../gen/vibebridge/v1/envelope_pb";

export const protocolV1WebSocketSubprotocol = "vibebridge.v1";
export const protocolV1Major = 1;
export const protocolV1Minor = 0;
export const protocolV1MaxEnvelopeBytes = 64 * 1024;
export const terminalBinaryOutputCapability = "terminal.binary_output";

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
        capabilities: [terminalBinaryOutputCapability],
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

function assertConnectionId(connectionId: Uint8Array) {
  if (connectionId.byteLength !== connectionIdBytes) {
    throw new Error(`Connection ID must be ${connectionIdBytes} bytes`);
  }
}

function equalBytes(left: Uint8Array, right: Uint8Array) {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}
