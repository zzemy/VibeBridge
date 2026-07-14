import { clone, create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { ed25519, x25519 } from "@noble/curves/ed25519.js";
import { sha256 } from "@noble/hashes/sha2.js";
import NoiseHandshake, { type NoiseCurve, type NoiseKeyPair } from "noise-handshake";
import NoiseCipher from "noise-handshake/cipher";

import { ProtocolVersionSchema } from "../gen/vibebridge/v1/envelope_pb";
import {
  HandshakeContextSchema,
  HandshakeIntent,
  PairingFinishPayloadSchema,
  PairingHandshakeFinishSchema,
  PairingHandshakeStartSchema,
  PairingInitiatorPayloadSchema,
  PairingResponderPayloadSchema,
  type HandshakeContext,
  type PairingHandshakeFinish,
  type PairingHandshakeResponse,
  type PairingHandshakeStart,
} from "../gen/vibebridge/v1/handshake_pb";
import {
  DeviceClass,
  DeviceDescriptorSchema,
  SignedDeviceDescriptorSchema,
  type SignedDeviceDescriptor,
} from "../gen/vibebridge/v1/identity_pb";

const pairingContextSchemaVersion = 1;
const deviceIdBytes = 16;
const keyBytes = 32;
const signatureBytes = 64;
const invitationIdBytes = 16;
const maxNoiseMessageBytes = 65_535;
const maxTransportPlaintextBytes = maxNoiseMessageBytes - 16;
const maxBrowserNoiseNonce = 0xffff_ffff;
const descriptorSignatureDomain = encodeUtf8("VibeBridge device descriptor v1\0");
const pairingPrologueDomain = encodeUtf8("VibeBridge pairing prologue v1\0");
const pairingSasDomain = encodeUtf8("VibeBridge pairing SAS v1\0");

export type PairingInitiatorConfig = {
  context: HandshakeContext;
  client: SignedDeviceDescriptor;
  agent: SignedDeviceDescriptor;
  staticPrivateKey: Uint8Array;
  bootstrapSecret: Uint8Array;
  /** Fixed only by cross-language vector tests; production callers must omit it. */
  testOnlyEphemeralPrivateKey?: Uint8Array;
};

export type PairingResult = {
  peer: SignedDeviceDescriptor;
  sas: string;
  handshakeHash: Uint8Array;
  transport: PairingTransport;
};

export function newPairingContext(
  initiatorDeviceId: Uint8Array,
  responderDeviceId: Uint8Array,
  invitationId: Uint8Array,
  relayTicket: Uint8Array,
): HandshakeContext {
  const context = create(HandshakeContextSchema, {
    schemaVersion: pairingContextSchemaVersion,
    protocolVersion: create(ProtocolVersionSchema, { major: 1, minor: 0 }),
    initiatorDeviceId: Uint8Array.from(initiatorDeviceId),
    responderDeviceId: Uint8Array.from(responderDeviceId),
    relayTicketHash: sha256(Uint8Array.from(relayTicket)),
    intent: HandshakeIntent.PAIR_DEVICE,
    invitationId: Uint8Array.from(invitationId),
  });
  validatePairingContext(context);
  return context;
}

export class PairingInitiator {
  readonly #context: HandshakeContext;
  readonly #client: SignedDeviceDescriptor;
  readonly #agent: SignedDeviceDescriptor;
  #staticPrivateKey: Uint8Array;
  #bootstrapSecret: Uint8Array;
  #noise: NoiseHandshake | null;
  #state: "ready" | "awaiting-response" | "complete" | "failed" = "ready";

  constructor(config: PairingInitiatorConfig) {
    validatePairingContext(config.context);
    verifySignedDescriptor(config.client);
    verifySignedDescriptor(config.agent);
    const client = requiredDescriptor(config.client);
    const agent = requiredDescriptor(config.agent);
    if (client.deviceClass !== DeviceClass.CLIENT || agent.deviceClass !== DeviceClass.AGENT) {
      throw new Error("Pairing descriptors have invalid device classes");
    }
    if (!equalBytes(config.context.initiatorDeviceId, client.deviceId)
      || !equalBytes(config.context.responderDeviceId, agent.deviceId)) {
      throw new Error("Pairing context device IDs do not match descriptors");
    }
    if (!descriptorSupportsVersion(config.client, config.context)
      || !descriptorSupportsVersion(config.agent, config.context)) {
      throw new Error("Pairing descriptor does not support the handshake protocol version");
    }
    if (config.staticPrivateKey.byteLength !== keyBytes
      || !equalBytes(x25519.getPublicKey(Uint8Array.from(config.staticPrivateKey)), client.keyAgreementPublicKey)) {
      throw new Error("Static private key does not match the signed descriptor");
    }
    if (config.bootstrapSecret.byteLength !== keyBytes) {
      throw new Error("Bootstrap secret must be 32 bytes");
    }
    if (config.testOnlyEphemeralPrivateKey !== undefined && config.testOnlyEphemeralPrivateKey.byteLength !== keyBytes) {
      throw new Error("Test ephemeral private key must be 32 bytes");
    }

    this.#context = clone(HandshakeContextSchema, config.context);
    this.#client = clone(SignedDeviceDescriptorSchema, config.client);
    this.#agent = clone(SignedDeviceDescriptorSchema, config.agent);
    this.#staticPrivateKey = Uint8Array.from(config.staticPrivateKey);
    this.#bootstrapSecret = Uint8Array.from(config.bootstrapSecret);

    const curve = noiseCurve(config.testOnlyEphemeralPrivateKey);
    const staticKeypair = keyPair(this.#staticPrivateKey);
    this.#noise = new NoiseHandshake("XXpsk0", true, staticKeypair, {
      curve,
      psk: this.#bootstrapSecret,
    });
    this.#noise.initialise(pairingPrologue(this.#context));
  }

  start(): PairingHandshakeStart {
    if (this.#state !== "ready" || this.#noise === null) {
      throw new Error("Pairing handshake is in an invalid state");
    }
    try {
      const payload = toBinary(PairingInitiatorPayloadSchema, create(PairingInitiatorPayloadSchema, {
        client: this.#client,
      }));
      const message = this.#noise.send(payload);
      assertNoiseMessage(message);
      this.#state = "awaiting-response";
      return create(PairingHandshakeStartSchema, {
        context: clone(HandshakeContextSchema, this.#context),
        noiseMessage: Uint8Array.from(message),
      });
    } catch {
      this.#fail();
      throw new Error("Pairing handshake is invalid");
    }
  }

  finish(response: PairingHandshakeResponse): { result: PairingResult; finish: PairingHandshakeFinish } {
    if (this.#state !== "awaiting-response" || this.#noise === null) {
      throw new Error("Pairing handshake is in an invalid state");
    }
    try {
      assertNoiseMessage(response.noiseMessage);
      // noise-handshake clears a view into the received frame on completion.
      // Always pass an owned copy so caller/replay buffers are never mutated.
      const payloadBytes = this.#noise.recv(Uint8Array.from(response.noiseMessage));
      if (this.#noise.complete || this.#noise.rs === null
        || !equalBytes(this.#noise.rs, requiredDescriptor(this.#agent).keyAgreementPublicKey)) {
        throw new Error("Agent Noise static key does not match the QR descriptor");
      }
      const payload = fromBinary(PairingResponderPayloadSchema, payloadBytes);
      if (payload.agent === undefined
        || !equalBytes(toBinary(SignedDeviceDescriptorSchema, payload.agent), toBinary(SignedDeviceDescriptorSchema, this.#agent))) {
        throw new Error("Agent descriptor does not match the QR descriptor");
      }
      verifyPeerDescriptor(payload.agent, DeviceClass.AGENT, this.#context.responderDeviceId, this.#context);

      const finishPayload = toBinary(PairingFinishPayloadSchema, create(PairingFinishPayloadSchema, {
        initiatorDeviceId: this.#context.initiatorDeviceId,
      }));
      const message = this.#noise.send(finishPayload);
      assertNoiseMessage(message);
      if (!this.#noise.complete || this.#noise.hash === null || this.#noise.hash.byteLength !== 64
        || this.#noise.tx === null || this.#noise.rx === null) {
        throw new Error("Noise handshake did not produce transport keys");
      }
      const handshakeHash = Uint8Array.from(this.#noise.hash);
      const result: PairingResult = {
        peer: clone(SignedDeviceDescriptorSchema, this.#agent),
        sas: pairingSas(handshakeHash),
        handshakeHash,
        // Source inspection and Go interop vectors establish tx as local send
        // and rx as local receive despite contradictory upstream README prose.
        transport: new PairingTransport(this.#noise.tx, this.#noise.rx),
      };
      this.#state = "complete";
      this.#clearSecrets();
      return {
        result,
        finish: create(PairingHandshakeFinishSchema, { noiseMessage: Uint8Array.from(message) }),
      };
    } catch {
      this.#fail();
      throw new Error("Pairing handshake is invalid");
    }
  }

  #fail() {
    this.#state = "failed";
    this.#clearSecrets();
  }

  #clearSecrets() {
    this.#staticPrivateKey.fill(0);
    this.#bootstrapSecret.fill(0);
    this.#noise = null;
  }
}

export class PairingTransport {
  #send: NoiseCipher | null;
  #receive: NoiseCipher | null;
  #sendCounter = 0;
  #receiveCounter = 0;

  constructor(sendKey: Uint8Array, receiveKey: Uint8Array) {
    this.#send = new NoiseCipher(Uint8Array.from(sendKey));
    this.#receive = new NoiseCipher(Uint8Array.from(receiveKey));
  }

  encrypt(plaintext: Uint8Array, associatedData: Uint8Array = new Uint8Array()): Uint8Array {
    if (this.#send === null || plaintext.byteLength > maxTransportPlaintextBytes
      || this.#sendCounter > maxBrowserNoiseNonce) {
      throw new Error("Encrypted transport cannot send this message");
    }
    const ciphertext = this.#send.encrypt(plaintext, associatedData);
    this.#sendCounter += 1;
    return Uint8Array.from(ciphertext);
  }

  decrypt(ciphertext: Uint8Array, associatedData: Uint8Array = new Uint8Array()): Uint8Array {
    if (this.#receive === null) {
      throw new Error("Encrypted transport cannot receive this message");
    }
    if (ciphertext.byteLength < 16 || ciphertext.byteLength > maxNoiseMessageBytes
      || this.#receiveCounter > maxBrowserNoiseNonce) {
      this.close();
      throw new Error("Encrypted transport cannot receive this message");
    }
    try {
      const plaintext = this.#receive.decrypt(ciphertext, associatedData);
      this.#receiveCounter += 1;
      return Uint8Array.from(plaintext);
    } catch {
      this.close();
      throw new Error("Encrypted transport authentication failed");
    }
  }

  get sendCounter() {
    return this.#sendCounter;
  }

  get receiveCounter() {
    return this.#receiveCounter;
  }

  close() {
    this.#send = null;
    this.#receive = null;
  }
}

function verifySignedDescriptor(signed: SignedDeviceDescriptor) {
  const descriptor = requiredDescriptor(signed);
  validateDescriptor(descriptor);
  if (signed.signature.byteLength !== signatureBytes) {
    throw new Error("Device descriptor signature must be 64 bytes");
  }
  const message = concatBytes(descriptorSignatureDomain, toBinary(DeviceDescriptorSchema, descriptor));
  // Protobuf fields decrypted by the CommonJS Noise library may retain a
  // cross-realm Uint8Array prototype. Normalize at the noble boundary.
  if (!ed25519.verify(
    Uint8Array.from(signed.signature),
    message,
    Uint8Array.from(descriptor.signingPublicKey),
    { zip215: false },
  )) {
    throw new Error("Device descriptor signature is invalid");
  }
}

function validateDescriptor(descriptor: ReturnType<typeof requiredDescriptor>) {
  if (descriptor.deviceId.byteLength !== deviceIdBytes
    || descriptor.signingPublicKey.byteLength !== keyBytes
    || descriptor.keyAgreementPublicKey.byteLength !== keyBytes) {
    throw new Error("Device descriptor key or ID length is invalid");
  }
  // Reject low-order/non-canonical X25519 values through a public-key operation later;
  // byte length and signed binding are the checks possible without a private scalar.
  if (descriptor.displayName.length === 0 || descriptor.displayName.trim() !== descriptor.displayName
    || new TextEncoder().encode(descriptor.displayName).byteLength > 128
    || descriptor.platform.length === 0 || descriptor.platform.trim() !== descriptor.platform
    || new TextEncoder().encode(descriptor.platform).byteLength > 32) {
    throw new Error("Device descriptor metadata is invalid");
  }
  if (descriptor.deviceClass !== DeviceClass.AGENT && descriptor.deviceClass !== DeviceClass.CLIENT) {
    throw new Error("Device descriptor class is invalid");
  }
  const createdAt = descriptor.createdAt;
  if (createdAt === undefined || createdAt.seconds < -62_135_596_800n || createdAt.seconds > 253_402_300_799n
    || createdAt.nanos < 0 || createdAt.nanos > 999_999_999 || (createdAt.seconds === 0n && createdAt.nanos === 0)) {
    throw new Error("Device creation time is invalid");
  }
  if (descriptor.keyVersion === 0 || descriptor.supportedVersions === undefined
    || descriptor.supportedVersions.minimum === undefined || descriptor.supportedVersions.maximum === undefined
    || descriptor.supportedVersions.minimum.major === 0
    || descriptor.supportedVersions.minimum.major !== descriptor.supportedVersions.maximum.major
    || descriptor.supportedVersions.minimum.minor > descriptor.supportedVersions.maximum.minor) {
    throw new Error("Device descriptor version is invalid");
  }
}

function verifyPeerDescriptor(
  signed: SignedDeviceDescriptor,
  deviceClass: DeviceClass,
  deviceId: Uint8Array,
  context: HandshakeContext,
) {
  verifySignedDescriptor(signed);
  const descriptor = requiredDescriptor(signed);
  if (descriptor.deviceClass !== deviceClass || !equalBytes(descriptor.deviceId, deviceId)
    || !descriptorSupportsVersion(signed, context)) {
    throw new Error("Peer descriptor does not match the handshake context");
  }
}

function descriptorSupportsVersion(signed: SignedDeviceDescriptor, context: HandshakeContext) {
  const descriptor = requiredDescriptor(signed);
  const versions = descriptor.supportedVersions;
  const version = context.protocolVersion;
  return versions !== undefined && versions.minimum !== undefined && versions.maximum !== undefined
    && version !== undefined && versions.minimum.major === version.major && versions.maximum.major === version.major
    && versions.minimum.minor <= version.minor && version.minor <= versions.maximum.minor;
}

function validatePairingContext(context: HandshakeContext) {
  if (context.schemaVersion !== pairingContextSchemaVersion || context.protocolVersion === undefined
    || context.protocolVersion.major !== 1 || context.protocolVersion.minor !== 0
    || context.initiatorDeviceId.byteLength !== deviceIdBytes || context.responderDeviceId.byteLength !== deviceIdBytes
    || equalBytes(context.initiatorDeviceId, context.responderDeviceId)
    || context.relayTicketHash.byteLength !== keyBytes || context.intent !== HandshakeIntent.PAIR_DEVICE
    || context.invitationId.byteLength !== invitationIdBytes) {
    throw new Error("Pairing handshake context is invalid");
  }
}

function pairingPrologue(context: HandshakeContext): Uint8Array {
  validatePairingContext(context);
  return concatBytes(pairingPrologueDomain, toBinary(HandshakeContextSchema, context));
}

function pairingSas(handshakeHash: Uint8Array): string {
  const digest = sha256(concatBytes(pairingSasDomain, handshakeHash));
  let value = 0n;
  for (const byte of digest.subarray(0, 8)) value = (value << 8n) | BigInt(byte);
  const code = Number(value % 1_000_000n).toString().padStart(6, "0");
  return `${code.slice(0, 3)}-${code.slice(3)}`;
}

function noiseCurve(fixedEphemeralPrivateKey?: Uint8Array): NoiseCurve {
  let fixed = fixedEphemeralPrivateKey === undefined ? undefined : Uint8Array.from(fixedEphemeralPrivateKey);
  return {
    DHLEN: keyBytes,
    PKLEN: keyBytes,
    SKLEN: keyBytes,
    ALG: "25519",
    generateKeyPair() {
      const secretKey = fixed === undefined ? x25519.utils.randomSecretKey() : Uint8Array.from(fixed);
      fixed?.fill(0);
      fixed = undefined;
      return keyPair(secretKey);
    },
    dh(publicKey, localKeyPair) {
      // CommonJS `noise-handshake` can hand us buffers from another JS realm.
      // Noble deliberately rejects cross-realm views, so normalize both inputs.
      return x25519.getSharedSecret(
        Uint8Array.from(localKeyPair.secretKey),
        Uint8Array.from(publicKey),
      );
    },
  };
}

function keyPair(secretKey: Uint8Array): NoiseKeyPair {
  return {
    secretKey,
    publicKey: x25519.getPublicKey(secretKey),
  };
}

function requiredDescriptor(signed: SignedDeviceDescriptor) {
  if (signed.deviceDescriptor === undefined) throw new Error("Signed device descriptor is required");
  return signed.deviceDescriptor;
}

function assertNoiseMessage(message: Uint8Array) {
  if (message.byteLength === 0 || message.byteLength > maxNoiseMessageBytes) {
    throw new Error("Noise handshake message has invalid size");
  }
}

function concatBytes(...values: Uint8Array[]): Uint8Array {
  const result = new Uint8Array(values.reduce((length, value) => length + value.byteLength, 0));
  let offset = 0;
  for (const value of values) {
    result.set(value, offset);
    offset += value.byteLength;
  }
  return result;
}

function equalBytes(left: Uint8Array, right: Uint8Array) {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}

function encodeUtf8(value: string) {
  return new TextEncoder().encode(value);
}
