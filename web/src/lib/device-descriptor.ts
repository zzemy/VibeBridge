import { toBinary } from "@bufbuild/protobuf";
import { ed25519 } from "@noble/curves/ed25519.js";

import {
  DeviceClass,
  DeviceDescriptorSchema,
  type SignedDeviceDescriptor,
} from "../gen/vibebridge/v1/identity_pb";

const deviceIdBytes = 16;
const keyBytes = 32;
const signatureBytes = 64;
const descriptorSignatureDomain = new TextEncoder().encode("VibeBridge device descriptor v1\0");

export function verifySignedDeviceDescriptor(signed: SignedDeviceDescriptor) {
  const descriptor = signed.deviceDescriptor;
  if (descriptor === undefined) throw new Error("Signed device descriptor is required");
  if (descriptor.deviceId.byteLength !== deviceIdBytes
    || descriptor.signingPublicKey.byteLength !== keyBytes
    || descriptor.keyAgreementPublicKey.byteLength !== keyBytes) {
    throw new Error("Device descriptor key or ID length is invalid");
  }
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
  if (signed.signature.byteLength !== signatureBytes) {
    throw new Error("Device descriptor signature must be 64 bytes");
  }
  const message = concatBytes(descriptorSignatureDomain, toBinary(DeviceDescriptorSchema, descriptor));
  if (!ed25519.verify(
    Uint8Array.from(signed.signature),
    message,
    Uint8Array.from(descriptor.signingPublicKey),
    { zip215: false },
  )) {
    throw new Error("Device descriptor signature is invalid");
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
