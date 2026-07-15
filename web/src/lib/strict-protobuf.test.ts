import { create, fromBinary } from "@bufbuild/protobuf";
import { describe, expect, test } from "vitest";

import { PairingApprovalSchema } from "../gen/vibebridge/v1/handshake_pb";
import {
  DeviceClass,
  DeviceDescriptorSchema,
  PairingInvitationSchema,
  SignedDeviceDescriptorSchema,
} from "../gen/vibebridge/v1/identity_pb";
import { assertNoUnknownFields } from "./strict-protobuf";

describe("assertNoUnknownFields", () => {
  test("accepts security-sensitive messages with only known fields", () => {
    const invitation = create(PairingInvitationSchema, {
      agent: create(SignedDeviceDescriptorSchema, {
        deviceDescriptor: create(DeviceDescriptorSchema, {
          deviceClass: DeviceClass.AGENT,
        }),
      }),
    });

    expect(() => assertNoUnknownFields(invitation)).not.toThrow();
  });

  test("rejects unknown top-level fields decoded from protobuf", () => {
    // field 99, varint 1
    const message = fromBinary(PairingApprovalSchema, Uint8Array.of(0x98, 0x06, 0x01));
    expect(() => assertNoUnknownFields(message)).toThrow("unknown fields");
  });

  test("rejects unknown fields nested in messages and arrays", () => {
    const nested = create(DeviceDescriptorSchema);
    Reflect.set(nested, "$unknown", [{ no: 100, wireType: 2, data: Uint8Array.of(0x01) }]);
    const value = {
      invitations: [{ agentDescriptor: { descriptor: nested } }],
    };

    expect(() => assertNoUnknownFields(value)).toThrow("unknown fields");
  });

  test("handles cycles without traversing byte buffers", () => {
    const value: { bytes: Uint8Array; self?: unknown } = { bytes: Uint8Array.of(1, 2, 3) };
    value.self = value;

    expect(() => assertNoUnknownFields(value)).not.toThrow();
  });
});
