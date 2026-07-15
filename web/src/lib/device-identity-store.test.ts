import "fake-indexeddb/auto";

import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { fromBinary, toBinary } from "@bufbuild/protobuf";
import { afterEach, describe, expect, test } from "vitest";

import { PairingInvitationSchema, SignedDeviceDescriptorSchema } from "../gen/vibebridge/v1/identity_pb";
import { BrowserDeviceStore } from "./device-identity-store";

const databases: string[] = [];

function databaseName() {
  const name = `vibebridge-device-test-${crypto.randomUUID()}`;
  databases.push(name);
  return name;
}

function goldenAgent() {
  const bytes = new Uint8Array(readFileSync(resolve(
    process.cwd(),
    "../proto/vibebridge/v1/testdata/pairing_invitation.bin",
  )));
  const invitation = fromBinary(PairingInvitationSchema, bytes);
  if (invitation.agent === undefined) throw new Error("golden Agent is missing");
  return invitation.agent;
}

function deleteDatabase(name: string) {
  return new Promise<void>((resolve, reject) => {
    const request = indexedDB.deleteDatabase(name);
    request.onsuccess = () => resolve();
    request.onerror = () => reject(request.error);
    request.onblocked = () => reject(new Error("test database deletion blocked"));
  });
}

afterEach(async () => {
  await Promise.all(databases.splice(0).map(deleteDatabase));
});

describe("browser device identity store", () => {
  test("persists one stable signed phone identity in IndexedDB", async () => {
    const name = databaseName();
    const firstStore = new BrowserDeviceStore(name);
    const first = await firstStore.getOrCreateIdentity();
    await firstStore.close();

    const secondStore = new BrowserDeviceStore(name);
    const second = await secondStore.getOrCreateIdentity();
    expect(toBinary(SignedDeviceDescriptorSchema, second.descriptor))
      .toEqual(toBinary(SignedDeviceDescriptorSchema, first.descriptor));
    expect(second.signingPrivateSeed).toEqual(first.signingPrivateSeed);
    expect(second.staticPrivateKey).toEqual(first.staticPrivateKey);
    expect(second.descriptor.deviceDescriptor?.platform).toBe("web");
    await secondStore.close();
  });

  test("round-trips an approved Agent and its durable authorization version", async () => {
    const name = databaseName();
    const store = new BrowserDeviceStore(name);
    const agent = goldenAgent();
    const deviceId = agent.deviceDescriptor?.deviceId;
    if (deviceId === undefined) throw new Error("golden Agent device ID is missing");

    expect(await store.getTrustedAgent(deviceId)).toBeNull();
    const pairedAt = new Date("2026-07-15T09:00:00.000Z");
    await store.trustAgent(agent, 7n, pairedAt);
    const trusted = await store.getTrustedAgent(deviceId);

    expect(trusted?.authorizationVersion).toBe(7n);
    expect(trusted?.pairedAt).toEqual(pairedAt);
    expect(toBinary(SignedDeviceDescriptorSchema, trusted?.descriptor ?? agent))
      .toEqual(toBinary(SignedDeviceDescriptorSchema, agent));
    await store.close();
  });

  test("refuses to persist trust without a positive Agent authorization", async () => {
    const store = new BrowserDeviceStore(databaseName());
    await expect(store.trustAgent(goldenAgent(), 0n)).rejects.toThrow("authorization version");
    await store.close();
  });
});
