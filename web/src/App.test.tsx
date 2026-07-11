import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

vi.mock("./components/TerminalView", () => ({
  TerminalView: () => <div data-testid="terminal-view" />,
}));

import { App } from "./App";

afterEach(() => {
  cleanup();
  window.history.replaceState({}, "", "/");
});

test("requires confirmation before ending a session", async () => {
  const user = userEvent.setup();
  render(<App />);

  await user.click(screen.getByRole("button", { name: "End" }));
  expect(screen.getByRole("heading", { name: "End this terminal session?" })).toBeTruthy();

  await user.click(screen.getByRole("button", { name: "Keep session" }));
  expect(screen.queryByRole("heading", { name: "End this terminal session?" })).toBeNull();
});
