import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { ed25519, x25519 } from "@noble/curves/ed25519.js";

import { ProtocolVersionRangeSchema, ProtocolVersionSchema } from "../gen/vibebridge/v1/envelope_pb";
import {
  DeviceClass,
  DeviceDescriptorSchema,
  SignedDeviceDescriptorSchema,
  type SignedDeviceDescriptor,
} from "../gen/vibebridge/v1/identity_pb";
import { verifySignedDeviceDescriptor } from "./device-descriptor";

const defaultDatabaseName = "vibebridge-device-v1";
const databaseVersion = 1;
const identityStoreName = "identity";
const trustedAgentsStoreName = "trusted-agents";
const identityKey = "phone";
const identityRecordVersion = 1;
const trustedAgentRecordVersion = 1;
const keyBytes = 32;
const deviceIdBytes = 16;
const descriptorSignatureDomain = new TextEncoder().encode("VibeBridge device descriptor v1\0");

export type BrowserDeviceIdentity = {
  descriptor: SignedDeviceDescriptor;
  signingPrivateSeed: Uint8Array;
  staticPrivateKey: Uint8Array;
};

export type TrustedAgent = {
  descriptor: SignedDeviceDescriptor;
  authorizationVersion: bigint;
  pairedAt: Date;
};

type StoredIdentity = {
  version: number;
  signingSeed: Uint8Array;
  staticPrivateKey: Uint8Array;
  signedDescriptor: Uint8Array;
};

type StoredTrustedAgent = {
  version: number;
  deviceId: string;
  signedDescriptor: Uint8Array;
  authorizationVersion: string;
  pairedAt: string;
};

export class BrowserDeviceStore {
  readonly #databaseName: string;
  #databasePromise: Promise<IDBDatabase> | null = null;

  constructor(databaseName = defaultDatabaseName) {
    if (databaseName.trim() === "") throw new Error("Device database name is required");
    this.#databaseName = databaseName;
  }

  async getOrCreateIdentity(): Promise<BrowserDeviceIdentity> {
    const database = await this.#database();
    const existing = await readRecord(database, identityStoreName, identityKey);
    if (existing !== undefined) return decodeIdentity(existing);

    const generated = createIdentity();
    const record = encodeIdentity(generated);
    const transaction = database.transaction(identityStoreName, "readwrite");
    const store = transaction.objectStore(identityStoreName);
    const concurrent = await requestResult(store.get(identityKey));
    if (concurrent === undefined) store.put(record, identityKey);
    await transactionDone(transaction);
    return concurrent === undefined ? generated : decodeIdentity(concurrent);
  }

  async trustAgent(
    descriptor: SignedDeviceDescriptor,
    authorizationVersion: bigint,
    pairedAt: Date = new Date(),
  ): Promise<void> {
    verifySignedDeviceDescriptor(descriptor);
    const publicDescriptor = descriptor.deviceDescriptor;
    if (publicDescriptor === undefined || publicDescriptor.deviceClass !== DeviceClass.AGENT) {
      throw new Error("Trusted descriptor is not an Agent");
    }
    if (authorizationVersion <= 0n) throw new Error("Agent authorization version is invalid");
    if (!Number.isFinite(pairedAt.getTime())) throw new Error("Agent pairing time is invalid");

    const deviceId = encodeBase64Url(publicDescriptor.deviceId);
    const record: StoredTrustedAgent = {
      version: trustedAgentRecordVersion,
      deviceId,
      signedDescriptor: toBinary(SignedDeviceDescriptorSchema, descriptor),
      authorizationVersion: authorizationVersion.toString(),
      pairedAt: pairedAt.toISOString(),
    };
    const database = await this.#database();
    const transaction = database.transaction(trustedAgentsStoreName, "readwrite");
    transaction.objectStore(trustedAgentsStoreName).put(record, deviceId);
    await transactionDone(transaction);
  }

  async getTrustedAgent(deviceId: Uint8Array): Promise<TrustedAgent | null> {
    if (deviceId.byteLength !== deviceIdBytes) throw new Error("Agent device ID is invalid");
    const database = await this.#database();
    const record = await readRecord(database, trustedAgentsStoreName, encodeBase64Url(deviceId));
    return record === undefined ? null : decodeTrustedAgent(record);
  }

  async close(): Promise<void> {
    const databasePromise = this.#databasePromise;
    this.#databasePromise = null;
    if (databasePromise !== null) (await databasePromise).close();
  }

  #database(): Promise<IDBDatabase> {
    if (this.#databasePromise === null) this.#databasePromise = openDatabase(this.#databaseName);
    return this.#databasePromise;
  }
}

function createIdentity(now: Date = new Date()): BrowserDeviceIdentity {
  if (!globalThis.crypto?.getRandomValues) throw new Error("Secure browser randomness is unavailable");
  const deviceId = globalThis.crypto.getRandomValues(new Uint8Array(deviceIdBytes));
  const signingSeed = ed25519.utils.randomSecretKey();
  const staticPrivateKey = x25519.utils.randomSecretKey();
  const version = () => create(ProtocolVersionSchema, { major: 1, minor: 0 });
  const descriptor = create(DeviceDescriptorSchema, {
    deviceId,
    displayName: "Mobile browser",
    platform: browserPlatform(),
    deviceClass: DeviceClass.CLIENT,
    signingPublicKey: ed25519.getPublicKey(signingSeed),
    keyAgreementPublicKey: x25519.getPublicKey(staticPrivateKey),
    createdAt: timestampFromDate(now),
    keyVersion: 1,
    supportedVersions: create(ProtocolVersionRangeSchema, {
      minimum: version(),
      maximum: version(),
    }),
  });
  const encodedDescriptor = toBinary(DeviceDescriptorSchema, descriptor);
  const signatureMessage = concatBytes(descriptorSignatureDomain, encodedDescriptor);
  const signed = create(SignedDeviceDescriptorSchema, {
    deviceDescriptor: descriptor,
    signature: ed25519.sign(signatureMessage, signingSeed),
  });
  verifySignedDeviceDescriptor(signed);

  const identity = {
    descriptor: signed,
    signingPrivateSeed: Uint8Array.from(signingSeed),
    staticPrivateKey: Uint8Array.from(staticPrivateKey),
  };
  signingSeed.fill(0);
  staticPrivateKey.fill(0);
  return identity;
}

function encodeIdentity(identity: BrowserDeviceIdentity): StoredIdentity {
  verifySignedDeviceDescriptor(identity.descriptor);
  const descriptor = identity.descriptor.deviceDescriptor;
  if (descriptor === undefined || descriptor.deviceClass !== DeviceClass.CLIENT
    || identity.signingPrivateSeed.byteLength !== keyBytes || identity.staticPrivateKey.byteLength !== keyBytes
    || !equalBytes(ed25519.getPublicKey(identity.signingPrivateSeed), descriptor.signingPublicKey)
    || !equalBytes(x25519.getPublicKey(identity.staticPrivateKey), descriptor.keyAgreementPublicKey)) {
    throw new Error("Browser device identity is invalid");
  }
  return {
    version: identityRecordVersion,
    signingSeed: Uint8Array.from(identity.signingPrivateSeed),
    staticPrivateKey: Uint8Array.from(identity.staticPrivateKey),
    signedDescriptor: toBinary(SignedDeviceDescriptorSchema, identity.descriptor),
  };
}

function decodeIdentity(value: unknown): BrowserDeviceIdentity {
  const record = recordValue(value);
  const version = numberField(record, "version");
  const signingSeed = bytesField(record, "signingSeed");
  const staticPrivateKey = bytesField(record, "staticPrivateKey");
  const signedDescriptor = bytesField(record, "signedDescriptor");
  if (version !== identityRecordVersion || signingSeed.byteLength !== keyBytes
    || staticPrivateKey.byteLength !== keyBytes) {
    throw new Error("Stored browser identity is invalid");
  }

  let descriptor: SignedDeviceDescriptor;
  try {
    descriptor = fromBinary(SignedDeviceDescriptorSchema, signedDescriptor);
  } catch {
    throw new Error("Stored browser identity is invalid");
  }
  verifySignedDeviceDescriptor(descriptor);
  const publicDescriptor = descriptor.deviceDescriptor;
  if (publicDescriptor === undefined || publicDescriptor.deviceClass !== DeviceClass.CLIENT
    || !equalBytes(ed25519.getPublicKey(signingSeed), publicDescriptor.signingPublicKey)
    || !equalBytes(x25519.getPublicKey(staticPrivateKey), publicDescriptor.keyAgreementPublicKey)) {
    throw new Error("Stored browser identity is inconsistent");
  }
  return { descriptor, signingPrivateSeed: signingSeed, staticPrivateKey };
}

function decodeTrustedAgent(value: unknown): TrustedAgent {
  const record = recordValue(value);
  if (numberField(record, "version") !== trustedAgentRecordVersion) {
    throw new Error("Stored trusted Agent is invalid");
  }
  const storedDeviceId = stringField(record, "deviceId");
  const authorizationText = stringField(record, "authorizationVersion");
  const pairedAtText = stringField(record, "pairedAt");
  let descriptor: SignedDeviceDescriptor;
  let authorizationVersion: bigint;
  try {
    descriptor = fromBinary(SignedDeviceDescriptorSchema, bytesField(record, "signedDescriptor"));
    authorizationVersion = BigInt(authorizationText);
  } catch {
    throw new Error("Stored trusted Agent is invalid");
  }
  verifySignedDeviceDescriptor(descriptor);
  const publicDescriptor = descriptor.deviceDescriptor;
  const pairedAt = new Date(pairedAtText);
  if (publicDescriptor === undefined || publicDescriptor.deviceClass !== DeviceClass.AGENT
    || encodeBase64Url(publicDescriptor.deviceId) !== storedDeviceId || authorizationVersion <= 0n
    || !Number.isFinite(pairedAt.getTime()) || pairedAt.toISOString() !== pairedAtText) {
    throw new Error("Stored trusted Agent is inconsistent");
  }
  return { descriptor, authorizationVersion, pairedAt };
}

function browserPlatform(): string {
  const userAgent = globalThis.navigator?.userAgent.toLowerCase() ?? "";
  if (/iphone|ipad|ipod/.test(userAgent)) return "ios";
  if (userAgent.includes("android")) return "android";
  return "web";
}

function openDatabase(name: string): Promise<IDBDatabase> {
  if (globalThis.indexedDB === undefined) return Promise.reject(new Error("Secure browser storage is unavailable"));
  return new Promise((resolve, reject) => {
    const request = globalThis.indexedDB.open(name, databaseVersion);
    request.onupgradeneeded = () => {
      const database = request.result;
      if (!database.objectStoreNames.contains(identityStoreName)) database.createObjectStore(identityStoreName);
      if (!database.objectStoreNames.contains(trustedAgentsStoreName)) database.createObjectStore(trustedAgentsStoreName);
    };
    request.onsuccess = () => resolve(request.result);
    request.onerror = () => reject(new Error("Could not open secure browser storage"));
    request.onblocked = () => reject(new Error("Browser storage upgrade is blocked"));
  });
}

async function readRecord(database: IDBDatabase, storeName: string, key: IDBValidKey): Promise<unknown> {
  const transaction = database.transaction(storeName, "readonly");
  const result = await requestResult(transaction.objectStore(storeName).get(key));
  await transactionDone(transaction);
  return result;
}

function requestResult(request: IDBRequest): Promise<unknown> {
  return new Promise((resolve, reject) => {
    request.onsuccess = () => resolve(request.result as unknown);
    request.onerror = () => reject(new Error("Browser storage request failed"));
  });
}

function transactionDone(transaction: IDBTransaction): Promise<void> {
  return new Promise((resolve, reject) => {
    transaction.oncomplete = () => resolve();
    transaction.onerror = () => reject(new Error("Browser storage transaction failed"));
    transaction.onabort = () => reject(new Error("Browser storage transaction was aborted"));
  });
}

function recordValue(value: unknown): Record<string, unknown> {
  if (typeof value !== "object" || value === null || Array.isArray(value)) {
    throw new Error("Stored browser data is invalid");
  }
  return value as Record<string, unknown>;
}

function numberField(record: Record<string, unknown>, field: string): number {
  const value = record[field];
  if (typeof value !== "number") throw new Error("Stored browser data is invalid");
  return value;
}

function stringField(record: Record<string, unknown>, field: string): string {
  const value = record[field];
  if (typeof value !== "string") throw new Error("Stored browser data is invalid");
  return value;
}

function bytesField(record: Record<string, unknown>, field: string): Uint8Array {
  const value = record[field];
  if (value instanceof ArrayBuffer) return new Uint8Array(value);
  if (ArrayBuffer.isView(value)) return Uint8Array.from(new Uint8Array(value.buffer, value.byteOffset, value.byteLength));
  throw new Error("Stored browser data is invalid");
}

function encodeBase64Url(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
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

function equalBytes(left: Uint8Array, right: Uint8Array): boolean {
  return left.byteLength === right.byteLength && left.every((byte, index) => byte === right[index]);
}
