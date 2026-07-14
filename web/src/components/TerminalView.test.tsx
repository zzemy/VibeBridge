import { cleanup, render } from "@testing-library/react";
import { createRef } from "react";
import { afterEach, beforeEach, expect, test, vi } from "vitest";

const terminalState = vi.hoisted(() => ({
  onData: undefined as ((data: string) => void) | undefined,
  resets: 0,
  writes: [] as Array<string | Uint8Array>,
}));

vi.mock("@xterm/addon-fit", () => ({
  FitAddon: class {
    fit() {}
  },
}));

vi.mock("@xterm/addon-search", () => ({
  SearchAddon: class {
    findNext() {
      return true;
    }
  },
}));

vi.mock("@xterm/xterm", () => ({
  Terminal: class {
    cols = 80;
    rows = 24;
    options: Record<string, unknown>;

    constructor(options: Record<string, unknown>) {
      this.options = options;
    }

    clear() {}
    dispose() {}
    focus() {}
    getSelection() { return ""; }
    loadAddon() {}
    open() {}
    reset() { terminalState.resets += 1; }
    write(data: string | Uint8Array) { terminalState.writes.push(data); }
    onData(callback: (data: string) => void) {
      terminalState.onData = callback;
      return { dispose() {} };
    }
  },
}));

import { TerminalView, type TerminalViewHandle } from "./TerminalView";

beforeEach(() => {
  terminalState.onData = undefined;
  terminalState.resets = 0;
  terminalState.writes = [];
  vi.stubGlobal("ResizeObserver", class {
    disconnect() {}
    observe() {}
  });
});

test("writes output that arrived before the terminal finished mounting", () => {
  render(<TerminalView chunks={["early output\r\n"]} fontSize={13} onInput={vi.fn()} onResize={vi.fn()} />);
  expect(terminalState.writes).toEqual(["early output\r\n"]);
});

afterEach(() => {
  cleanup();
  vi.unstubAllGlobals();
});

test("reset clears xterm state and restarts chunk delivery", () => {
  const ref = createRef<TerminalViewHandle>();
  const props = { fontSize: 13, onInput: vi.fn(), onResize: vi.fn() };
  const { rerender } = render(<TerminalView ref={ref} chunks={["old output\r\n"]} {...props} />);

  ref.current?.reset();
  rerender(<TerminalView ref={ref} chunks={["replacement output\r\n"]} {...props} />);

  expect(terminalState.resets).toBe(1);
  expect(terminalState.writes).toEqual(["old output\r\n", "replacement output\r\n"]);
});

test("forwards direct terminal keyboard input to the PTY callback", () => {
  const onInput = vi.fn();
  render(<TerminalView chunks={[]} fontSize={13} onInput={onInput} onResize={vi.fn()} />);

  terminalState.onData?.("git status\r");
  expect(onInput).toHaveBeenCalledWith("git status\r");
});
