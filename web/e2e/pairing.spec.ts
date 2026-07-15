import { execFileSync, spawn, type ChildProcess } from "node:child_process";
import { once } from "node:events";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

import { fromBinary } from "@bufbuild/protobuf";
import { expect, test, type Page } from "@playwright/test";

import { SignedDeviceDescriptorSchema } from "../src/gen/vibebridge/v1/identity_pb";

type RuntimeState = {
  version: number;
  pid: number;
  listen_address: string;
  session_token: string;
};

type PairingStatus = {
  state: "idle" | "handshaking" | "pending";
  flow_id?: string;
  sas?: string;
};

type BrowserRecords = {
  identityDescriptor: number[];
  trustedAgents: Array<{
    deviceId: string;
    authorizationVersion: string;
  }>;
};

const currentDirectory = dirname(fileURLToPath(import.meta.url));
const repositoryRoot = resolve(currentDirectory, "../..");
const webRoot = join(repositoryRoot, "web");
const agentBinary = join(repositoryRoot, "bin", process.platform === "win32" ? "vibebridge-e2e.exe" : "vibebridge-e2e");

let agent: TestAgent;

test.describe.configure({ mode: "serial" });

test.beforeAll(async () => {
  execFileSync("go", ["build", "-o", agentBinary, "./cmd/vibebridge"], {
    cwd: repositoryRoot,
    stdio: "ignore",
  });
  agent = await TestAgent.start();
});

test.afterAll(async () => {
  await agent?.close();
});

test("a rejected encrypted pairing creates trust on neither side", async ({ browser }) => {
  const context = await browser.newContext();
  try {
    const invitation = await agent.newInvitation();
    const page = await context.newPage();
    const networkURLs = observeNetworkURLs(page);

    await page.goto(invitation, { waitUntil: "domcontentloaded" });
    await expect(page).toHaveURL((url) => url.hash === "" && url.search === "");
    await expect(page.getByRole("heading", { name: "Approve this phone on the computer" })).toBeVisible();

    const status = await agent.waitForStatus("pending");
    expect(status.flow_id).toBeTruthy();
    expect(status.sas).toMatch(/^\d{3}-\d{3}$/);
    await expect(page.getByText(status.sas ?? "missing SAS", { exact: true }).first()).toBeVisible();

    await agent.decide(status.flow_id ?? "", false);
    await expect(page.getByRole("heading", { name: "Pairing rejected" })).toBeVisible();
    await agent.waitForStatus("idle");

    const records = await readBrowserRecords(page);
    expect(records.identityDescriptor.length).toBeGreaterThan(0);
    expect(records.trustedAgents).toHaveLength(0);
    expect(await agent.authorizedDeviceIDs()).toEqual([]);
    expect(networkURLs.some((url) => url.endsWith("/pairing/v1"))).toBe(true);
    expect(networkURLs.every((url) => !url.includes("#/pair/v1/"))).toBe(true);
  } finally {
    await context.close();
  }
});

test("local approval stores the same phone identity on the Agent and in IndexedDB", async ({ browser }) => {
  const context = await browser.newContext();
  try {
    const invitation = await agent.newInvitation();
    const page = await context.newPage();
    const networkURLs = observeNetworkURLs(page);

    await page.goto(invitation, { waitUntil: "domcontentloaded" });
    await expect(page).toHaveURL((url) => url.hash === "" && url.search === "");
    await expect(page.getByRole("heading", { name: "Approve this phone on the computer" })).toBeVisible();

    const status = await agent.waitForStatus("pending");
    expect(status.flow_id).toBeTruthy();
    await expect(page.getByText(status.sas ?? "missing SAS", { exact: true }).first()).toBeVisible();

    await agent.decide(status.flow_id ?? "", true);
    await expect(page.getByRole("heading", { name: "Phone paired" })).toBeVisible();
    await agent.waitForStatus("idle");

    const records = await readBrowserRecords(page);
    expect(records.trustedAgents).toHaveLength(1);
    expect(BigInt(records.trustedAgents[0].authorizationVersion)).toBeGreaterThan(0n);
    const identity = fromBinary(SignedDeviceDescriptorSchema, Uint8Array.from(records.identityDescriptor));
    expect(identity.deviceDescriptor).toBeDefined();
    const browserDeviceID = encodeBase64URL(identity.deviceDescriptor?.deviceId ?? new Uint8Array());
    expect(records.trustedAgents[0].deviceId).not.toBe(browserDeviceID);
    expect(await agent.authorizedDeviceIDs()).toEqual([browserDeviceID]);

    expect(networkURLs.some((url) => url.endsWith("/pairing/v1"))).toBe(true);
    expect(networkURLs.every((url) => !url.includes("#/pair/v1/"))).toBe(true);
    expect(await page.evaluate(() => ({ local: localStorage.length, session: sessionStorage.length })))
      .toEqual({ local: 0, session: 0 });
  } finally {
    await context.close();
  }
});

class TestAgent {
  readonly #process: ChildProcess;
  readonly #directory: string;
  readonly #state: RuntimeState;

  private constructor(process: ChildProcess, directory: string, state: RuntimeState) {
    this.#process = process;
    this.#directory = directory;
    this.#state = state;
    process.stderr?.resume();

  }

  static async start(): Promise<TestAgent> {
    const directory = await mkdtemp(join(tmpdir(), "vibebridge-pairing-e2e-"));
    const statePath = join(directory, "runtime.json");
    const identityPath = join(directory, "identity.json");
    const child = spawn(agentBinary, [
      "-addr", "0.0.0.0:0",
      "-web-dir", join(webRoot, "dist"),
      "-service-state", statePath,
      "-identity-store", identityPath,
      "-cmd", process.platform === "win32" ? "powershell.exe" : "/bin/sh",
    ], {
      cwd: repositoryRoot,
      stdio: ["ignore", "pipe", "pipe"],
      windowsHide: true,
    });
    child.stdout?.resume();

    try {
      const state = await waitForRuntimeState(statePath, child);
      return new TestAgent(child, directory, state);
    } catch (error) {
      child.kill();
      await rm(directory, { recursive: true, force: true });
      throw error;
    }
  }

  async newInvitation(): Promise<string> {
    const response = await fetch(this.managementURL("/agent/pair"), { redirect: "manual" });
    if (!response.ok) throw new Error(`Agent pairing page returned HTTP ${response.status}`);
    const html = await response.text();
    const match = /Copy pairing link<\/summary><code>([^<]+)<\/code>/.exec(html);
    if (match === null) throw new Error("Agent pairing page did not provide an invitation");
    return match[1];
  }

  async waitForStatus(expected: PairingStatus["state"]): Promise<PairingStatus> {
    const deadline = Date.now() + 15_000;
    while (Date.now() < deadline) {
      const response = await fetch(this.managementURL("/agent/pairing/status"));
      if (response.ok) {
        const status = await response.json() as PairingStatus;
        if (status.state === expected) return status;
      }
      await delay(50);
    }
    throw new Error(`Agent pairing state did not become ${expected}`);
  }

  async decide(flowID: string, approve: boolean): Promise<void> {
    if (flowID === "") throw new Error("Pairing flow ID is missing");
    const response = await fetch(this.managementURL(approve ? "/agent/pairing/approve" : "/agent/pairing/reject"), {
      method: "POST",
      redirect: "manual",
      headers: { "Content-Type": "application/x-www-form-urlencoded" },
      body: new URLSearchParams({ flow_id: flowID }),
    });
    if (response.status !== 303) throw new Error(`Agent pairing decision returned HTTP ${response.status}`);
  }

  async authorizedDeviceIDs(): Promise<string[]> {
    const response = await fetch(this.managementURL("/agent/pair"), { redirect: "manual" });
    if (!response.ok) throw new Error(`Agent pairing page returned HTTP ${response.status}`);
    const html = await response.text();
    return [...html.matchAll(/name="device_id" value="([A-Za-z0-9_-]+)"/g)].map((match) => match[1]);
  }

  async close(): Promise<void> {
    if (this.#process.exitCode === null) {
      this.#process.kill();
      await Promise.race([once(this.#process, "exit"), delay(5_000)]);
    }
    await rm(this.#directory, { recursive: true, force: true });
  }

  private managementURL(pathname: string): URL {
    const port = this.#state.listen_address.slice(this.#state.listen_address.lastIndexOf(":") + 1);
    const url = new URL(`http://127.0.0.1:${port}${pathname}`);
    url.searchParams.set("token", this.#state.session_token);
    return url;
  }
}

async function waitForRuntimeState(path: string, child: ChildProcess): Promise<RuntimeState> {
  const deadline = Date.now() + 15_000;
  while (Date.now() < deadline) {
    if (child.exitCode !== null) throw new Error("Agent exited before publishing runtime state");
    try {
      const value: unknown = JSON.parse(await readFile(path, "utf8"));
      if (isRuntimeState(value)) return value;
    } catch {
      // The state file is created atomically; retry until it becomes visible.
    }
    await delay(50);
  }
  throw new Error("Agent did not publish runtime state");
}

function isRuntimeState(value: unknown): value is RuntimeState {
  if (typeof value !== "object" || value === null) return false;
  const state = value as Partial<RuntimeState>;
  return state.version === 1 && typeof state.pid === "number"
    && typeof state.listen_address === "string" && typeof state.session_token === "string"
    && state.session_token.length > 0;
}

async function readBrowserRecords(page: Page): Promise<BrowserRecords> {
  return page.evaluate(async () => {
    const database = await new Promise<IDBDatabase>((resolveDatabase, rejectDatabase) => {
      const request = indexedDB.open("vibebridge-device-v1");
      request.onsuccess = () => resolveDatabase(request.result);
      request.onerror = () => rejectDatabase(new Error("could not open browser identity database"));
    });
    try {
      const identity = await readStoreValue(database, "identity", "phone");
      const trustedAgents = await readAllStoreValues(database, "trusted-agents");
      if (!isRecord(identity) || !ArrayBuffer.isView(identity.signedDescriptor)) {
        throw new Error("browser identity record is invalid");
      }
      return {
        identityDescriptor: Array.from(new Uint8Array(
          identity.signedDescriptor.buffer,
          identity.signedDescriptor.byteOffset,
          identity.signedDescriptor.byteLength,
        )),
        trustedAgents: trustedAgents.map((record) => {
          if (!isRecord(record) || typeof record.deviceId !== "string"
            || typeof record.authorizationVersion !== "string") {
            throw new Error("trusted Agent record is invalid");
          }
          return {
            deviceId: record.deviceId,
            authorizationVersion: record.authorizationVersion,
          };
        }),
      };
    } finally {
      database.close();
    }

    function readStoreValue(database: IDBDatabase, storeName: string, key: IDBValidKey): Promise<unknown> {
      return new Promise((resolveValue, rejectValue) => {
        const request = database.transaction(storeName, "readonly").objectStore(storeName).get(key);
        request.onsuccess = () => resolveValue(request.result as unknown);
        request.onerror = () => rejectValue(new Error("could not read browser identity record"));
      });
    }

    function readAllStoreValues(database: IDBDatabase, storeName: string): Promise<unknown[]> {
      return new Promise((resolveValues, rejectValues) => {
        const request = database.transaction(storeName, "readonly").objectStore(storeName).getAll();
        request.onsuccess = () => resolveValues(request.result as unknown[]);
        request.onerror = () => rejectValues(new Error("could not read trusted Agent records"));
      });
    }

    function isRecord(value: unknown): value is Record<string, unknown> {
      return typeof value === "object" && value !== null && !Array.isArray(value);
    }
  });
}

function observeNetworkURLs(page: Page): string[] {
  const urls: string[] = [];
  page.on("request", (request) => urls.push(request.url()));
  page.on("websocket", (socket) => urls.push(socket.url()));
  return urls;
}

function encodeBase64URL(value: Uint8Array): string {
  return Buffer.from(value).toString("base64url");
}

function delay(milliseconds: number): Promise<void> {
  return new Promise((resolveDelay) => setTimeout(resolveDelay, milliseconds));
}
