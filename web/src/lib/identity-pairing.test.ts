import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { timestampFromDate } from "@bufbuild/protobuf/wkt";
import { describe, expect, test } from "vitest";

import {
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
} from "../gen/vibebridge/v1/envelope_pb";
import {
  DeviceClass,
  DeviceDescriptorSchema,
  PairingInvitationSchema,
  SignedDeviceDescriptorSchema,
} from "../gen/vibebridge/v1/identity_pb";

const testdata = resolve(process.cwd(), "../proto/vibebridge/v1/testdata");

function hex(value: string): Uint8Array {
  if (value.length % 2 !== 0) throw new Error("hex fixture has an odd length");
  return Uint8Array.from(value.match(/.{2}/g) ?? [], (byte) => Number.parseInt(byte, 16));
}

function goldenDescriptor() {
  const version = () => create(ProtocolVersionSchema, { major: 1, minor: 0 });
  return create(DeviceDescriptorSchema, {
    deviceId: hex("00112233445566778899aabbccddeeff"),
    displayName: "Home PC",
    platform: "windows",
    deviceClass: DeviceClass.AGENT,
    signingPublicKey: hex("03a107bff3ce10be1d70dd18e74bc09967e4d6309ba50d5f1ddc8664125531b8"),
    keyAgreementPublicKey: hex("79a631eede1bf9c98f12032cdeadd0e7a079398fc786b88cc846ec89af85a51a"),
    createdAt: timestampFromDate(new Date("2026-07-15T08:00:00.123Z")),
    keyVersion: 1,
    supportedVersions: create(ProtocolVersionRangeSchema, {
      minimum: version(),
      maximum: version(),
    }),
  });
}

function goldenSignedDescriptor() {
  return create(SignedDeviceDescriptorSchema, {
    deviceDescriptor: goldenDescriptor(),
    signature: hex(
      "2c60e87b1ce439e575d6cf3bf34bf08569e045a7615f072b922d6795e977bfa8" +
        "7f103d8beeba0135305f5eb16d2d2c21fdff792d39df4f71c3f02b0748a14e05",
    ),
  });
}

function goldenPairingInvitation() {
  return create(PairingInvitationSchema, {
    version: 1,
    invitationId: hex("a0a1a2a3a4a5a6a7a8a9aaabacadaeaf"),
    agent: goldenSignedDescriptor(),
    bootstrapSecret: hex("c0c1c2c3c4c5c6c7c8c9cacbcccdcecfd0d1d2d3d4d5d6d7d8d9dadbdcdddedf"),
    createdAt: timestampFromDate(new Date("2026-07-15T08:05:00.456Z")),
    expiresAt: timestampFromDate(new Date("2026-07-15T08:10:00.456Z")),
    connectionHints: [
      "http://192.168.20.5:8787/pairing/v1",
      "wss://relay.example.test/pair/v1",
    ],
    verificationCode: "792-928",
  });
}

function golden(name: string): Uint8Array {
  return new Uint8Array(readFileSync(resolve(testdata, name)));
}

describe("identity and pairing golden vectors", () => {
  test("encodes and decodes the canonical device descriptor", () => {
    const bytes = golden("device_descriptor.bin");
    const expected = goldenDescriptor();

    expect(fromBinary(DeviceDescriptorSchema, bytes)).toEqual(expected);
    expect(Array.from(toBinary(DeviceDescriptorSchema, expected))).toEqual(Array.from(bytes));
  });

  test("encodes and decodes the canonical signed device descriptor", () => {
    const bytes = golden("signed_device_descriptor.bin");
    const expected = goldenSignedDescriptor();

    expect(fromBinary(SignedDeviceDescriptorSchema, bytes)).toEqual(expected);
    expect(Array.from(toBinary(SignedDeviceDescriptorSchema, expected))).toEqual(Array.from(bytes));
  });

  test("encodes and decodes the canonical pairing invitation", () => {
    const bytes = golden("pairing_invitation.bin");
    const expected = goldenPairingInvitation();

    expect(fromBinary(PairingInvitationSchema, bytes)).toEqual(expected);
    expect(Array.from(toBinary(PairingInvitationSchema, expected))).toEqual(Array.from(bytes));
  });
});
