import { clone, fromBinary, toBinary } from "@bufbuild/protobuf";
import { sha256 } from "@noble/hashes/sha2.js";

import { DeviceClass, PairingInvitationSchema, type PairingInvitation } from "../gen/vibebridge/v1/identity_pb";
import { verifySignedDeviceDescriptor } from "./device-descriptor";
import { assertNoUnknownFields } from "./strict-protobuf";

const routePrefix = "#/pair/v1/";
const invitationVersion = 1;
const invitationIdBytes = 16;
const bootstrapSecretBytes = 32;
const maxInvitationBytes = 16 * 1024;
const maxEncodedInvitationBytes = Math.ceil(maxInvitationBytes * 4 / 3);
const maxConnectionHints = 8;
const maxConnectionHintBytes = 512;
const protocolMajor = 1;
const protocolMinor = 0;
const timestampMinSeconds = -62_135_596_800n;
const timestampMaxSeconds = 253_402_300_799n;
const invitationVerificationDomain = new TextEncoder().encode("VibeBridge pairing invitation v1\0");

export type PairingRoute = {
  invitation: PairingInvitation;
  websocketUrl: string;
};

export type PairingEntry =
  | { kind: "pairing"; route: PairingRoute }
  | { kind: "invalid"; message: string };

type LocationSource = Pick<Location, "hash" | "origin" | "pathname" | "search">;
type HistorySink = Pick<History, "replaceState" | "state">;

/**
 * Captures a QR fragment and removes it from the address bar before decoding.
 * The returned invitation remains memory-only and must never be persisted.
 */
export function consumePairingEntry(
  location: LocationSource,
  history: HistorySink,
  now: Date = new Date(),
): PairingEntry | null {
  if (!location.hash.startsWith(routePrefix)) return null;

  const fragment = location.hash;
  const pageUrl = `${location.origin}${location.pathname}${location.search}`;
  history.replaceState(history.state, "", `${location.pathname}${location.search}`);

  try {
    return { kind: "pairing", route: parsePairingRoute(fragment, pageUrl, now) };
  } catch (error) {
    return {
      kind: "invalid",
      message: error instanceof Error ? error.message : "Pairing invitation is invalid",
    };
  }
}

export function parsePairingRoute(fragment: string, pageUrl: string, now: Date = new Date()): PairingRoute {
  if (!fragment.startsWith(routePrefix)) throw new Error("Pairing link is invalid");
  const payload = fragment.slice(routePrefix.length);
  if (payload.length === 0 || payload.length > maxEncodedInvitationBytes
    || !/^[A-Za-z0-9_-]+$/.test(payload) || payload.length % 4 === 1) {
    throw new Error("Pairing link payload is invalid");
  }

  const encoded = decodeBase64Url(payload);
  if (encoded.byteLength === 0 || encoded.byteLength > maxInvitationBytes
    || encodeBase64Url(encoded) !== payload) {
    throw new Error("Pairing link payload is invalid");
  }

  let invitation: PairingInvitation;
  try {
    invitation = fromBinary(PairingInvitationSchema, encoded);
  } catch {
    throw new Error("Pairing link payload is invalid");
  }
  try {
    assertNoUnknownFields(invitation);
    validatePairingInvitation(invitation, now);
    const websocketUrl = selectDirectPairingUrl(invitation.connectionHints, pageUrl);
    return { invitation, websocketUrl };
  } catch (error) {
    invitation.bootstrapSecret.fill(0);
    throw error;
  }
}

export function validatePairingInvitation(invitation: PairingInvitation, now: Date = new Date()) {
  if (toBinary(PairingInvitationSchema, invitation).byteLength > maxInvitationBytes) {
    throw new Error("Pairing invitation is too large");
  }
  if (invitation.version !== invitationVersion) {
    throw new Error(`Unsupported pairing invitation version ${invitation.version}`);
  }
  if (invitation.invitationId.byteLength !== invitationIdBytes
    || invitation.bootstrapSecret.byteLength !== bootstrapSecretBytes) {
    throw new Error("Pairing invitation credentials are invalid");
  }
  if (invitation.agent === undefined) throw new Error("Pairing Agent identity is missing");
  verifySignedDeviceDescriptor(invitation.agent);
  const agent = invitation.agent.deviceDescriptor;
  if (agent === undefined || agent.deviceClass !== DeviceClass.AGENT) {
    throw new Error("Pairing invitation identity is not an Agent");
  }
  const versions = agent.supportedVersions;
  if (versions?.minimum === undefined || versions.maximum === undefined
    || versions.minimum.major !== protocolMajor || versions.maximum.major !== protocolMajor
    || versions.minimum.minor > protocolMinor || versions.maximum.minor < protocolMinor) {
    throw new Error("Pairing Agent does not support protocol v1.0");
  }

  const createdAt = timestampNanoseconds(invitation.createdAt, "creation");
  const expiresAt = timestampNanoseconds(invitation.expiresAt, "expiry");
  const nowNanoseconds = BigInt(now.getTime()) * 1_000_000n;
  if (createdAt > nowNanoseconds + 5n * 60n * 1_000_000_000n) {
    throw new Error("Pairing invitation creation time is too far in the future");
  }
  if (expiresAt <= createdAt || expiresAt - createdAt > 15n * 60n * 1_000_000_000n) {
    throw new Error("Pairing invitation lifetime is invalid");
  }
  if (nowNanoseconds >= expiresAt) throw new Error("Pairing invitation has expired");

  validateConnectionHints(invitation.connectionHints);
  const expectedCode = pairingVerificationCode(invitation);
  if (!constantTimeTextEqual(expectedCode, invitation.verificationCode)) {
    throw new Error("Pairing invitation verification code is invalid");
  }
}

export function pairingVerificationCode(invitation: PairingInvitation): string {
  const transcript = clone(PairingInvitationSchema, invitation);
  transcript.verificationCode = "";
  const encoded = toBinary(PairingInvitationSchema, transcript);
  const message = new Uint8Array(invitationVerificationDomain.byteLength + encoded.byteLength);
  message.set(invitationVerificationDomain);
  message.set(encoded, invitationVerificationDomain.byteLength);
  const digest = sha256(message);
  const value = ((digest[0] ?? 0) << 16 | (digest[1] ?? 0) << 8 | (digest[2] ?? 0)) % 1_000_000;
  const code = value.toString().padStart(6, "0");
  return `${code.slice(0, 3)}-${code.slice(3)}`;
}

function selectDirectPairingUrl(hints: readonly string[], pageUrl: string): string {
  let page: URL;
  try {
    page = new URL(pageUrl);
  } catch {
    throw new Error("Pairing page URL is invalid");
  }
  if ((page.protocol !== "http:" && page.protocol !== "https:") || page.username !== ""
    || page.password !== "" || page.search !== "" || page.hash !== "") {
    throw new Error("Pairing page URL is invalid");
  }

  for (const hint of hints) {
    const candidate = parseConnectionHint(hint);
    const secure = candidate.protocol === "https:" || candidate.protocol === "wss:";
    const pageSecure = page.protocol === "https:";
    if (candidate.host !== page.host || secure !== pageSecure || candidate.pathname !== "/pairing/v1"
      || candidate.search !== "" || candidate.hash !== "") continue;
    candidate.protocol = pageSecure ? "wss:" : "ws:";
    return candidate.href;
  }
  throw new Error("No same-origin direct pairing connection is available");
}

function validateConnectionHints(hints: readonly string[]) {
  if (hints.length > maxConnectionHints) {
    throw new Error(`Pairing invitation has more than ${maxConnectionHints} connection hints`);
  }
  const seen = new Set<string>();
  for (const hint of hints) {
    if (hint.length === 0 || hint.trim() !== hint || new TextEncoder().encode(hint).byteLength > maxConnectionHintBytes
      || seen.has(hint)) {
      throw new Error("Pairing invitation contains an invalid connection hint");
    }
    parseConnectionHint(hint);
    seen.add(hint);
  }
}

function parseConnectionHint(hint: string): URL {
  if (hint.includes("\\") || /[\u0000-\u001f\u007f]/.test(hint)) {
    throw new Error("Pairing invitation contains an invalid connection hint");
  }
  let parsed: URL;
  try {
    parsed = new URL(hint);
  } catch {
    throw new Error("Pairing invitation contains an invalid connection hint");
  }
  if (!["http:", "https:", "ws:", "wss:"].includes(parsed.protocol) || parsed.host === ""
    || parsed.username !== "" || parsed.password !== "" || parsed.hash !== "") {
    throw new Error("Pairing invitation contains an invalid connection hint");
  }
  return parsed;
}

function timestampNanoseconds(
  timestamp: PairingInvitation["createdAt"],
  label: string,
): bigint {
  if (timestamp === undefined || timestamp.seconds < timestampMinSeconds || timestamp.seconds > timestampMaxSeconds
    || timestamp.nanos < 0 || timestamp.nanos > 999_999_999) {
    throw new Error(`Pairing invitation ${label} timestamp is invalid`);
  }
  return timestamp.seconds * 1_000_000_000n + BigInt(timestamp.nanos);
}

function decodeBase64Url(value: string): Uint8Array {
  try {
    const base64 = value.replace(/-/g, "+").replace(/_/g, "/") + "=".repeat((4 - value.length % 4) % 4);
    const decoded = atob(base64);
    return Uint8Array.from(decoded, (character) => character.charCodeAt(0));
  } catch {
    throw new Error("Pairing link payload is invalid");
  }
}

function encodeBase64Url(value: Uint8Array): string {
  let binary = "";
  for (const byte of value) binary += String.fromCharCode(byte);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function constantTimeTextEqual(left: string, right: string): boolean {
  const length = Math.max(left.length, right.length);
  let difference = left.length ^ right.length;
  for (let index = 0; index < length; index += 1) {
    difference |= (left.charCodeAt(index) || 0) ^ (right.charCodeAt(index) || 0);
  }
  return difference === 0;
}
