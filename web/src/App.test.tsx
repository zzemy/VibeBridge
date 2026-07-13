import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { act, cleanup, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

import {
  EnvelopeSchema,
  HelloSchema,
  PeerRole,
  ProtocolVersionRangeSchema,
  ProtocolVersionSchema,
  ResumeDisposition,
  SessionStatusSchema,
} from "./gen/vibebridge/v1/envelope_pb";
import {
  protocolV1MaxEnvelopeBytes,
  protocolV1WebSocketSubprotocol,
  sessionResumeCapability,
  terminalBinaryOutputCapability,
  terminalSequencedIoCapability,
} from "./lib/protocol-v1";

type TerminalState = {
  chunks: Array<string | Uint8Array>;
  resets: number;
};

const terminalState = vi.hoisted<TerminalState>(() => ({
  chunks: [],
  resets: 0,
}));

vi.mock("./components/TerminalView", async () => {
  const React = await import("react");
  type MockProps = { chunks: Array<string | Uint8Array> };
  const TerminalView = React.forwardRef(function MockTerminalView({ chunks }: MockProps, ref) {
    terminalState.chunks = chunks;
    React.useImperativeHandle(ref, () => ({
      clear() {},
      reset() { terminalState.resets += 1; },
      async copySelection() { return false; },
      findNext() { return false; },
      focus() {},
    }));
    return React.createElement("div", { "data-testid": "terminal-view" });
  });
  return { TerminalView };
});

class FakeWebSocket {
  static readonly CONNECTING = 0;
  static readonly OPEN = 1;
  static readonly CLOSING = 2;
  static readonly CLOSED = 3;
  static instances: FakeWebSocket[] = [];

  readonly sent: unknown[] = [];
  binaryType = "blob";
  protocol: string;
  readyState = FakeWebSocket.CONNECTING;
  private readonly listeners = new Map<string, Set<EventListenerOrEventListenerObject>>();

  constructor(_url: string | URL, protocols?: string | string[]) {
    const offered = typeof protocols === "string" ? [protocols] : protocols ?? [];
    this.protocol = offered.includes(protocolV1WebSocketSubprotocol) ? protocolV1WebSocketSubprotocol : "";
    FakeWebSocket.instances.push(this);
  }

  addEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    const listeners = this.listeners.get(type) ?? new Set<EventListenerOrEventListenerObject>();
    listeners.add(listener);
    this.listeners.set(type, listeners);
  }

  removeEventListener(type: string, listener: EventListenerOrEventListenerObject) {
    this.listeners.get(type)?.delete(listener);
  }

  send(data: unknown) {
    this.sent.push(data);
  }

  close(code = 1000, reason = "") {
    if (this.readyState === FakeWebSocket.CLOSED) return;
    this.readyState = FakeWebSocket.CLOSED;
    this.dispatch(new CloseEvent("close", { code, reason }));
  }

  open() {
    this.readyState = FakeWebSocket.OPEN;
    this.dispatch(new Event("open"));
  }

  message(data: string | ArrayBuffer) {
    this.dispatch(new MessageEvent("message", { data }));
  }

  private dispatch(event: Event) {
    for (const listener of this.listeners.get(event.type) ?? []) {
      if (typeof listener === "function") listener(event);
      else listener.handleEvent(event);
    }
  }
}

import { App } from "./App";

beforeEach(() => {
  FakeWebSocket.instances = [];
  terminalState.chunks = [];
  terminalState.resets = 0;
  vi.stubGlobal("WebSocket", FakeWebSocket);
  vi.stubGlobal("fetch", vi.fn().mockResolvedValue({
    ok: true,
    json: async () => ({ state: "idle", reconnect_timeout_seconds: 90, idle_timeout_seconds: 0 }),
  }));
});

afterEach(() => {
  cleanup();
  window.history.replaceState({}, "", "/");
  vi.unstubAllGlobals();
});

test("requires confirmation before ending a session", async () => {
  const user = userEvent.setup();
  render(<App />);

  await user.click(screen.getByRole("button", { name: "End" }));
  expect(screen.getByRole("heading", { name: "End this terminal session?" })).toBeTruthy();

  await user.click(screen.getByRole("button", { name: "Keep session" }));
  expect(screen.queryByRole("heading", { name: "End this terminal session?" })).toBeNull();
});

test("waits for SessionStatus and clears stale terminal history on RESYNC_REQUIRED", async () => {
  window.history.replaceState({}, "", "/?token=test-token");
  render(<App />);
  await screen.findByTestId("terminal-view");
  await waitFor(() => expect(FakeWebSocket.instances).toHaveLength(1));
  const socket = FakeWebSocket.instances[0];
  if (!socket) throw new Error("expected WebSocket instance");

  act(() => socket.open());
  const clientHelloBytes = socket.sent[0];
  if (!(clientHelloBytes instanceof ArrayBuffer)) throw new Error("expected binary client Hello");
  const clientHello = fromBinary(EnvelopeSchema, new Uint8Array(clientHelloBytes));
  expect(screen.getByText("Connecting")).toBeTruthy();

  const version = () => create(ProtocolVersionSchema, { major: 1, minor: 0 });
  const agentHello = toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: clientHello.connectionId,
    sequence: 1n,
    payload: {
      case: "hello",
      value: create(HelloSchema, {
        peerRole: PeerRole.AGENT,
        supportedVersions: create(ProtocolVersionRangeSchema, { minimum: version(), maximum: version() }),
        capabilities: [terminalBinaryOutputCapability, terminalSequencedIoCapability, sessionResumeCapability],
        maxEnvelopeBytes: protocolV1MaxEnvelopeBytes,
      }),
    },
  }));
  act(() => socket.message(agentHello.slice().buffer));

  expect(screen.getByText("Connecting")).toBeTruthy();
  const attachBytes = socket.sent[1];
  if (!(attachBytes instanceof ArrayBuffer)) throw new Error("expected binary AttachSession");
  const attach = fromBinary(EnvelopeSchema, new Uint8Array(attachBytes));
  expect(attach.payload.case).toBe("attachSession");

  const sessionId = Uint8Array.from({ length: 16 }, (_, index) => 255 - index);
  const status = toBinary(EnvelopeSchema, create(EnvelopeSchema, {
    protocolMajor: 1,
    connectionId: clientHello.connectionId,
    sessionId,
    sessionGeneration: 4n,
    sequence: 2n,
    acknowledge: 2n,
    payload: {
      case: "sessionStatus",
      value: create(SessionStatusSchema, { resumeDisposition: ResumeDisposition.RESYNC_REQUIRED }),
    },
  }));
  act(() => socket.message(status.slice().buffer));

  await waitFor(() => expect(screen.getByText("Connected")).toBeTruthy());
  expect(terminalState.resets).toBe(1);
  expect(terminalState.chunks).toEqual(["terminal history was truncated; synchronized with the current session\r\n"]);
});
