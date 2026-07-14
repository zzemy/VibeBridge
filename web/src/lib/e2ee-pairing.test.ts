import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { clone, create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { ed25519, x25519 } from "@noble/curves/ed25519.js";
import { describe, expect, test } from "vitest";

import {
  HandshakeContextSchema,
  PairingHandshakeResponseSchema,
} from "../gen/vibebridge/v1/handshake_pb";
import {
  DeviceDescriptorSchema,
  SignedDeviceDescriptorSchema,
  type SignedDeviceDescriptor,
} from "../gen/vibebridge/v1/identity_pb";
import { newPairingContext, PairingInitiator } from "./e2ee-pairing";

const fixturePath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/pairing_handshake_vector.json");
const descriptorSignatureDomain = new TextEncoder().encode("VibeBridge device descriptor v1\0");

type PairingVector = ReturnType<typeof loadVector>;

function loadVector() {
  const parsed: unknown = JSON.parse(readFileSync(fixturePath, "utf8"));
  if (!isRecord(parsed)) throw new Error("pairing vector must be an object");
  return {
    suite: stringField(parsed, "suite"),
    clientSigningSeed: stringField(parsed, "client_signing_seed"),
    clientStaticPrivateKey: stringField(parsed, "client_static_private_key"),
    clientEphemeralPrivateKey: stringField(parsed, "client_ephemeral_private_key"),
    agentSigningSeed: stringField(parsed, "agent_signing_seed"),
    bootstrapSecret: stringField(parsed, "bootstrap_secret"),
    relayTicket: stringField(parsed, "relay_ticket"),
    context: stringField(parsed, "context"),
    clientSignedDescriptor: stringField(parsed, "client_signed_descriptor"),
    agentSignedDescriptor: stringField(parsed, "agent_signed_descriptor"),
    noiseMessage1: stringField(parsed, "noise_message_1"),
    noiseMessage2: stringField(parsed, "noise_message_2"),
    noiseMessage3: stringField(parsed, "noise_message_3"),
    handshakeHash: stringField(parsed, "handshake_hash"),
    sas: stringField(parsed, "sas"),
    transportAssociatedData: stringField(parsed, "transport_associated_data"),
    clientToAgentPlaintext: stringField(parsed, "client_to_agent_plaintext"),
    clientToAgentCiphertext: stringField(parsed, "client_to_agent_ciphertext"),
    agentToClientPlaintext: stringField(parsed, "agent_to_client_plaintext"),
    agentToClientCiphertext: stringField(parsed, "agent_to_client_ciphertext"),
  };
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") throw new Error(`pairing vector field ${field} must be a string`);
  return value;
}

function hex(value: string): Uint8Array {
  if (value.length % 2 !== 0 || !/^[0-9a-f]*$/.test(value)) throw new Error("invalid lowercase hex fixture");
  return Uint8Array.from(value.match(/.{2}/g) ?? [], (byte) => Number.parseInt(byte, 16));
}

function toHex(value: Uint8Array): string {
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("");
}

function decodeInputs(vector: PairingVector) {
  return {
    context: fromBinary(HandshakeContextSchema, hex(vector.context)),
    client: fromBinary(SignedDeviceDescriptorSchema, hex(vector.clientSignedDescriptor)),
    agent: fromBinary(SignedDeviceDescriptorSchema, hex(vector.agentSignedDescriptor)),
  };
}

function newVectorInitiator(
  vector: PairingVector,
  overrides: Partial<{
    context: ReturnType<typeof decodeInputs>["context"];
    agent: SignedDeviceDescriptor;
    bootstrapSecret: Uint8Array;
  }> = {},
) {
  const inputs = decodeInputs(vector);
  return new PairingInitiator({
    context: overrides.context ?? inputs.context,
    client: inputs.client,
    agent: overrides.agent ?? inputs.agent,
    staticPrivateKey: hex(vector.clientStaticPrivateKey),
    bootstrapSecret: overrides.bootstrapSecret ?? hex(vector.bootstrapSecret),
    testOnlyEphemeralPrivateKey: hex(vector.clientEphemeralPrivateKey),
  });
}

function vectorResponse(vector: PairingVector, message = hex(vector.noiseMessage2)) {
  return create(PairingHandshakeResponseSchema, { noiseMessage: message });
}

function completeVector(vector: PairingVector) {
  const initiator = newVectorInitiator(vector);
  const start = initiator.start();
  const responseBytes = hex(vector.noiseMessage2);
  const responseBefore = Uint8Array.from(responseBytes);
  const completed = initiator.finish(vectorResponse(vector, responseBytes));
  return { initiator, start, responseBytes, responseBefore, ...completed };
}

function alternateSignedAgent(vector: PairingVector): SignedDeviceDescriptor {
  const { agent } = decodeInputs(vector);
  const changed = clone(SignedDeviceDescriptorSchema, agent);
  if (changed.deviceDescriptor === undefined) throw new Error("fixture Agent descriptor is missing");
  changed.deviceDescriptor.keyAgreementPublicKey = x25519.getPublicKey(hex("0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20"));
  return resignDescriptor(changed, hex(vector.agentSigningSeed));
}

function resignDescriptor(signed: SignedDeviceDescriptor, signingSeed: Uint8Array): SignedDeviceDescriptor {
  if (signed.deviceDescriptor === undefined) throw new Error("descriptor is missing");
  const descriptorBytes = toBinary(DeviceDescriptorSchema, signed.deviceDescriptor);
  const signedBytes = new Uint8Array(descriptorSignatureDomain.byteLength + descriptorBytes.byteLength);
  signedBytes.set(descriptorSignatureDomain);
  signedBytes.set(descriptorBytes, descriptorSignatureDomain.byteLength);
  signed.signature = ed25519.sign(signedBytes, signingSeed);
  return signed;
}

describe("browser pairing handshake interop", () => {
  const vector = loadVector();

  test("matches the Go XXpsk0 transcript, SAS, and directional transport", () => {
    expect(vector.suite).toBe("Noise_XXpsk0_25519_ChaChaPoly_BLAKE2b");
    const inputs = decodeInputs(vector);
    if (inputs.client.deviceDescriptor === undefined || inputs.agent.deviceDescriptor === undefined) {
      throw new Error("fixture descriptors are missing");
    }
    const rebuiltContext = newPairingContext(
      inputs.client.deviceDescriptor.deviceId,
      inputs.agent.deviceDescriptor.deviceId,
      inputs.context.invitationId,
      hex(vector.relayTicket),
    );
    expect(toHex(toBinary(HandshakeContextSchema, rebuiltContext))).toBe(vector.context);

    const { start, finish, result, responseBytes, responseBefore } = completeVector(vector);
    expect(toHex(start.noiseMessage)).toBe(vector.noiseMessage1);
    expect(toHex(finish.noiseMessage)).toBe(vector.noiseMessage3);
    expect(toHex(result.handshakeHash)).toBe(vector.handshakeHash);
    expect(result.sas).toBe(vector.sas);
    expect(responseBytes).toEqual(responseBefore);

    const associatedData = hex(vector.transportAssociatedData);
    expect(toHex(result.transport.encrypt(hex(vector.clientToAgentPlaintext), associatedData)))
      .toBe(vector.clientToAgentCiphertext);
    expect(toHex(result.transport.decrypt(hex(vector.agentToClientCiphertext), associatedData)))
      .toBe(vector.agentToClientPlaintext);
    expect(result.transport.sendCounter).toBe(1);
    expect(result.transport.receiveCounter).toBe(1);
  });

  test("fails closed with a wrong bootstrap secret", () => {
    const wrongSecret = hex(vector.bootstrapSecret);
    wrongSecret[0] ^= 0xff;
    const initiator = newVectorInitiator(vector, { bootstrapSecret: wrongSecret });
    initiator.start();
    expect(() => initiator.finish(vectorResponse(vector))).toThrow("Pairing handshake is invalid");
    expect(() => initiator.finish(vectorResponse(vector))).toThrow("invalid state");
  });

  test("binds the exact context and relay ticket hash", () => {
    const inputs = decodeInputs(vector);
    const changedContext = clone(HandshakeContextSchema, inputs.context);
    changedContext.relayTicketHash[0] ^= 0xff;
    const initiator = newVectorInitiator(vector, { context: changedContext });
    expect(toHex(initiator.start().noiseMessage)).not.toBe(vector.noiseMessage1);
    expect(() => initiator.finish(vectorResponse(vector))).toThrow("Pairing handshake is invalid");
  });

  test("rejects a Noise responder static that differs from the signed QR Agent", () => {
    const initiator = newVectorInitiator(vector, { agent: alternateSignedAgent(vector) });
    initiator.start();
    expect(() => initiator.finish(vectorResponse(vector))).toThrow("Pairing handshake is invalid");
  });

  test.each([
    ["empty", new Uint8Array()],
    ["truncated", hex(vector.noiseMessage2).subarray(0, 31)],
    ["oversized", new Uint8Array(65_536)],
  ])("rejects a %s response frame", (_name, message) => {
    const initiator = newVectorInitiator(vector);
    initiator.start();
    expect(() => initiator.finish(vectorResponse(vector, message))).toThrow("Pairing handshake is invalid");
  });

  test("enforces handshake call ordering and single use", () => {
    const beforeStart = newVectorInitiator(vector);
    expect(() => beforeStart.finish(vectorResponse(vector))).toThrow("invalid state");

    const duplicateStart = newVectorInitiator(vector);
    duplicateStart.start();
    expect(() => duplicateStart.start()).toThrow("invalid state");

    const completed = completeVector(vector);
    expect(() => completed.initiator.finish(vectorResponse(vector))).toThrow("invalid state");
    expect(() => completed.initiator.start()).toThrow("invalid state");
  });

  test("rejects signed descriptor mutation before starting Noise", () => {
    const inputs = decodeInputs(vector);
    inputs.client.signature[0] ^= 0x80;
    expect(() => new PairingInitiator({
      context: inputs.context,
      client: inputs.client,
      agent: inputs.agent,
      staticPrivateKey: hex(vector.clientStaticPrivateKey),
      bootstrapSecret: hex(vector.bootstrapSecret),
      testOnlyEphemeralPrivateKey: hex(vector.clientEphemeralPrivateKey),
    })).toThrow("signature");
  });

  test("rejects a signed descriptor that excludes the transcript protocol version", () => {
    const inputs = decodeInputs(vector);
    const client = clone(SignedDeviceDescriptorSchema, inputs.client);
    if (client.deviceDescriptor?.supportedVersions?.minimum === undefined
      || client.deviceDescriptor.supportedVersions.maximum === undefined) {
      throw new Error("fixture client version range is missing");
    }
    client.deviceDescriptor.supportedVersions.minimum.major = 2;
    client.deviceDescriptor.supportedVersions.maximum.major = 2;
    resignDescriptor(client, hex(vector.clientSigningSeed));
    expect(() => new PairingInitiator({
      context: inputs.context,
      client,
      agent: inputs.agent,
      staticPrivateKey: hex(vector.clientStaticPrivateKey),
      bootstrapSecret: hex(vector.bootstrapSecret),
      testOnlyEphemeralPrivateKey: hex(vector.clientEphemeralPrivateKey),
    })).toThrow("does not support");
  });

  test("rejects associated-data mismatch, closes after auth failure, and rejects replay", () => {
    const failed = completeVector(vector).result.transport;
    expect(() => failed.decrypt(hex(vector.agentToClientCiphertext), new TextEncoder().encode("wrong aad")))
      .toThrow("authentication failed");
    expect(() => failed.decrypt(hex(vector.agentToClientCiphertext), hex(vector.transportAssociatedData)))
      .toThrow("cannot receive");
    expect(() => failed.encrypt(hex(vector.clientToAgentPlaintext), hex(vector.transportAssociatedData)))
      .toThrow("cannot send");

    const malformed = completeVector(vector).result.transport;
    expect(() => malformed.decrypt(new Uint8Array(15))).toThrow("cannot receive");
    expect(() => malformed.encrypt(hex(vector.clientToAgentPlaintext))).toThrow("cannot send");

    const replayed = completeVector(vector).result.transport;
    const ciphertext = hex(vector.agentToClientCiphertext);
    const associatedData = hex(vector.transportAssociatedData);
    expect(toHex(replayed.decrypt(ciphertext, associatedData))).toBe(vector.agentToClientPlaintext);
    expect(() => replayed.decrypt(ciphertext, associatedData)).toThrow("authentication failed");
  });
});
