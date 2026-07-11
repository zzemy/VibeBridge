import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { TerminalToolbar } from "./TerminalToolbar";

afterEach(cleanup);

test("opens terminal search and submits the query", async () => {
  const user = userEvent.setup();
  const onSearch = vi.fn();
  render(
    <TerminalToolbar
      canZoomIn
      canZoomOut
      onClear={vi.fn()}
      onCopy={vi.fn()}
      onFocus={vi.fn()}
      onSearch={onSearch}
      onZoomIn={vi.fn()}
      onZoomOut={vi.fn()}
    />,
  );

  await user.click(screen.getByRole("button", { name: "Search output" }));
  await user.type(screen.getByPlaceholderText("Find terminal output"), "failed");
  await user.click(screen.getByRole("button", { name: "Next" }));

  expect(onSearch).toHaveBeenCalledWith("failed");
});

test("forwards terminal actions", async () => {
  const user = userEvent.setup();
  const onFocus = vi.fn();
  const onCopy = vi.fn();
  const onClear = vi.fn();
  render(
    <TerminalToolbar
      canZoomIn
      canZoomOut
      onClear={onClear}
      onCopy={onCopy}
      onFocus={onFocus}
      onSearch={vi.fn()}
      onZoomIn={vi.fn()}
      onZoomOut={vi.fn()}
    />,
  );

  await user.click(screen.getByRole("button", { name: "Focus terminal keyboard" }));
  await user.click(screen.getByRole("button", { name: "Copy terminal selection" }));
  await user.click(screen.getByRole("button", { name: "Clear terminal view" }));
  expect(onFocus).toHaveBeenCalledOnce();
  expect(onCopy).toHaveBeenCalledOnce();
  expect(onClear).toHaveBeenCalledOnce();
});
