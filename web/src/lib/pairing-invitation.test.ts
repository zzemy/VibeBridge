import { readFileSync } from "node:fs";
import { resolve } from "node:path";

import { clone, fromBinary, toBinary } from "@bufbuild/protobuf";
import { describe, expect, test } from "vitest";

import { PairingInvitationSchema } from "../gen/vibebridge/v1/identity_pb";
import {
  consumePairingEntry,
  pairingVerificationCode,
  parsePairingRoute,
  validatePairingInvitation,
} from "./pairing-invitation";

const goldenPath = resolve(process.cwd(), "../proto/vibebridge/v1/testdata/pairing_invitation.bin");
const validNow = new Date("2026-07-15T08:06:00.000Z");

function goldenInvitation() {
  return fromBinary(PairingInvitationSchema, new Uint8Array(readFileSync(goldenPath)));
}

function fragmentFor(invitation = goldenInvitation()) {
  const bytes = toBinary(PairingInvitationSchema, invitation);
  return `#/pair/v1/${Buffer.from(bytes).toString("base64url")}`;
}

describe("pairing invitation route", () => {
  test("validates the Go golden invitation and selects only its same-origin direct endpoint", () => {
    const invitation = goldenInvitation();
    expect(pairingVerificationCode(invitation)).toBe("792-928");

    const route = parsePairingRoute(fragmentFor(invitation), "http://192.168.20.5:8787/", validNow);
    expect(route.websocketUrl).toBe("ws://192.168.20.5:8787/pairing/v1");
    expect(route.invitation.verificationCode).toBe("792-928");
  });

  test("removes the secret fragment before parsing it", () => {
    const replacements: string[] = [];
    const entry = consumePairingEntry({
      hash: fragmentFor(),
      origin: "http://192.168.20.5:8787",
      pathname: "/",
      search: "",
    }, {
      state: { existing: true },
      replaceState(_data, _unused, url) {
        replacements.push(String(url));
      },
    }, validNow);

    expect(entry?.kind).toBe("pairing");
    expect(replacements).toEqual(["/"]);
    expect(replacements[0]).not.toContain("pair/v1");
  });

  test("rejects tampering, expiration, and non-canonical payloads", () => {
    const tampered = clone(PairingInvitationSchema, goldenInvitation());
    tampered.bootstrapSecret[0] ^= 0xff;
    expect(() => validatePairingInvitation(tampered, validNow)).toThrow("verification code");

    expect(() => validatePairingInvitation(goldenInvitation(), new Date("2026-07-15T08:10:00.456Z")))
      .toThrow("expired");
    expect(() => parsePairingRoute(`${fragmentFor()}=`, "http://192.168.20.5:8787/", validNow))
      .toThrow("payload");
  });

  test("will not connect a direct invitation across origins or through a query", () => {
    expect(() => parsePairingRoute(fragmentFor(), "http://192.168.20.6:8787/", validNow))
      .toThrow("same-origin");
    expect(() => parsePairingRoute(fragmentFor(), "http://192.168.20.5:8787/?token=secret", validNow))
      .toThrow("page URL");
  });

  test("returns an invalid entry while still clearing a malformed pairing fragment", () => {
    const replacements: string[] = [];
    const entry = consumePairingEntry({
      hash: "#/pair/v1/not*base64",
      origin: "http://192.168.20.5:8787",
      pathname: "/",
      search: "",
    }, {
      state: null,
      replaceState(_data, _unused, url) {
        replacements.push(String(url));
      },
    }, validNow);

    expect(entry?.kind).toBe("invalid");
    expect(replacements).toEqual(["/"]);
  });
});
