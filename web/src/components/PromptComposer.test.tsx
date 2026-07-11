import { cleanup, fireEvent, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";
import { PromptComposer } from "./PromptComposer";

afterEach(() => {
  cleanup();
  sessionStorage.clear();
});

test("restores a draft for the active browser session", async () => {
  const user = userEvent.setup();
  const { unmount } = render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={vi.fn()} />);

  await user.type(screen.getByRole("textbox"), "keep this draft");
  expect(sessionStorage.getItem("draft-key")).toBe("keep this draft");

  unmount();
  render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={vi.fn()} />);
  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("keep this draft");
});

test("submits without Enter in insert-only mode", async () => {
  const user = userEvent.setup();
  const onSubmit = vi.fn();
  render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={onSubmit} />);

  await user.click(screen.getByRole("button", { name: "Insert only" }));
  await user.type(screen.getByRole("textbox"), "review changes");
  await user.click(screen.getByRole("button", { name: "Insert prompt" }));

  expect(onSubmit).toHaveBeenCalledWith("review changes", false);
  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("");
  expect(sessionStorage.getItem("draft-key")).toBeNull();
  expect(JSON.parse(sessionStorage.getItem("history-key") ?? "[]")).toEqual(["review changes"]);
});

test("limits direct input to 8,000 characters", () => {
  render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={vi.fn()} />);

  fireEvent.change(screen.getByRole("textbox"), { target: { value: "x".repeat(8_001) } });

  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("x".repeat(8_000));
  expect(screen.getByRole("status").textContent).toContain("Prompts are limited to 8,000 characters.");
});

test("does not submit while an input method composition is active", () => {
  const onSubmit = vi.fn();
  render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={onSubmit} />);
  const editor = screen.getByRole("textbox");

  fireEvent.change(editor, { target: { value: "中文提示" } });
  fireEvent.compositionStart(editor);
  fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
  expect(onSubmit).not.toHaveBeenCalled();

  fireEvent.compositionEnd(editor);
  fireEvent.keyDown(editor, { key: "Enter", ctrlKey: true });
  expect(onSubmit).toHaveBeenCalledWith("中文提示", true);
});

test("adds a quick prompt without replacing the current draft", async () => {
  const user = userEvent.setup();
  render(<PromptComposer disabled={false} historyStorageKey="history-key" storageKey="draft-key" onSubmit={vi.fn()} />);

  await user.type(screen.getByRole("textbox"), "Start here.");
  await user.click(screen.getByRole("button", { name: "Quick prompts" }));
  await user.click(screen.getByRole("button", { name: "Run the relevant" }));

  expect((screen.getByRole("textbox") as HTMLTextAreaElement).value).toBe("Start here.\nRun the relevant tests and fix any failures.");
});
