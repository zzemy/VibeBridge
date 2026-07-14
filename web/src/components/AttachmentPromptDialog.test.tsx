import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

import { AttachmentPromptDialog } from "./AttachmentPromptDialog";

afterEach(cleanup);

test("shows the exact Agent preview and commits only after explicit confirmation", async () => {
  const user = userEvent.setup();
  let resolveCommit: () => void = () => { throw new Error("commit resolver was not initialized"); };
  const onConfirm = vi.fn(() => new Promise<void>((resolve) => { resolveCommit = resolve; }));
  const onCancel = vi.fn(async () => {});
  const onComplete = vi.fn();
  const preview = "Review these files\n\nUse the following local files:\n- `./staged/notes.md`";

  render(<AttachmentPromptDialog
    open
    preview={preview}
    appendEnter
    onConfirm={onConfirm}
    onCancel={onCancel}
    onComplete={onComplete}
  />);

  expect(screen.getByTestId("attachment-prompt-preview").textContent).toBe(preview);
  expect(screen.getByText(/presses Enter/)).toBeTruthy();
  expect(onConfirm).not.toHaveBeenCalled();

  await user.click(screen.getByRole("button", { name: "Confirm and send" }));
  expect(onConfirm).toHaveBeenCalledTimes(1);
  expect((screen.getByRole("button", { name: "Sending…" }) as HTMLButtonElement).disabled).toBe(true);
  expect((screen.getByRole("button", { name: "Cancel action" }) as HTMLButtonElement).disabled).toBe(true);

  resolveCommit();
  await vi.waitFor(() => expect(onComplete).toHaveBeenCalledWith("committed"));
});

test("exposes a close path after a terminal action failure", async () => {
  const user = userEvent.setup();
  const onConfirm = vi.fn(async () => { throw new Error("Attachment prompt action failed"); });
  const onCancel = vi.fn(async () => {});
  const onComplete = vi.fn();

  render(<AttachmentPromptDialog
    open
    preview="Insert this exact text"
    appendEnter={false}
    onConfirm={onConfirm}
    onCancel={onCancel}
    onComplete={onComplete}
  />);

  expect(screen.getByText(/without pressing Enter/)).toBeTruthy();
  await user.click(screen.getByRole("button", { name: "Confirm insertion" }));
  expect((await screen.findByRole("alert")).textContent).toContain("Attachment prompt action failed");
  expect(onComplete).not.toHaveBeenCalled();
  expect(screen.queryByRole("button", { name: "Cancel action" })).toBeNull();

  await user.click(screen.getByRole("button", { name: "Close" }));
  expect(onCancel).not.toHaveBeenCalled();
  expect(onConfirm).toHaveBeenCalledTimes(1);
  expect(onComplete).toHaveBeenCalledWith("failed");
});

test("cancels a prepared action without submitting", async () => {
  const user = userEvent.setup();
  const onConfirm = vi.fn(async () => {});
  const onCancel = vi.fn(async () => {});
  const onComplete = vi.fn();

  render(<AttachmentPromptDialog
    open
    preview="Insert this exact text"
    appendEnter={false}
    onConfirm={onConfirm}
    onCancel={onCancel}
    onComplete={onComplete}
  />);

  await user.click(screen.getByRole("button", { name: "Cancel action" }));
  expect(onCancel).toHaveBeenCalledTimes(1);
  expect(onConfirm).not.toHaveBeenCalled();
  expect(onComplete).toHaveBeenCalledWith("cancelled");
});
