import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

import { AttachmentBatchCleanupError, type AttachmentTransferProgress } from "../lib/attachments";
import { AttachmentComposer } from "./AttachmentComposer";

afterEach(cleanup);

function attachment(name = "notes.md", content = "hello") {
  return new File([content], name, { type: "text/markdown", lastModified: 1 });
}

function expectButtonDisabled(name: string, disabled: boolean) {
  const button = screen.getByRole("button", { name });
  if (!(button instanceof HTMLButtonElement)) {
    throw new Error(`${name} is not a button`);
  }
  expect(button.disabled).toBe(disabled);
}

test("reviews selected files but blocks confirmation without negotiated support", async () => {
  const user = userEvent.setup();
  const onTransfer = vi.fn();
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled={false} onTransfer={onTransfer} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());

  expect(screen.getByText("notes.md")).toBeTruthy();
  expect(screen.getByText("5 B · text/markdown")).toBeTruthy();
  expect(screen.getByText("Attachment transfer is not available on this Agent yet.")).toBeTruthy();
  expectButtonDisabled("Send files", true);
  expect(onTransfer).not.toHaveBeenCalled();
});

test("confirms a supported selection and reports that files were verified and staged", async () => {
  const user = userEvent.setup();
  const onTransfer = vi.fn(async (
    _files: readonly File[],
    _signal: AbortSignal,
    onProgress: (progress: AttachmentTransferProgress) => void,
  ) => {
    onProgress({
      fileIndex: 0,
      fileCount: 1,
      fileName: "notes.md",
      fileBytesSent: 5,
      fileSizeBytes: 5,
      totalBytesSent: 5,
      totalSizeBytes: 5,
    });
  });
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={onTransfer} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());
  await user.click(screen.getByRole("button", { name: "Send files" }));

  expect(onTransfer).toHaveBeenCalledTimes(1);
  expect(await screen.findByText("Files verified and staged.")).toBeTruthy();
});

test("shows local policy failures and clears a stale selection", async () => {
  const user = userEvent.setup();
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={vi.fn()} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());
  expect(screen.getByText("notes.md")).toBeTruthy();

  await user.upload(input, new File(["x"], "notes.md", { type: "text/html" }));

  expect(screen.getByRole("alert").textContent).toContain("mismatched content type");
  expect(screen.queryByText("5 B · text/markdown")).toBeNull();
  expect(screen.queryByRole("button", { name: "Send files" })).toBeNull();
});

test("disables picker controls when the session cannot send", () => {
  render(<AttachmentComposer disabled transferEnabled onTransfer={vi.fn()} />);

  expectButtonDisabled("Camera", true);
  expectButtonDisabled("Choose", true);
  expect(screen.queryByRole("button", { name: "Send files" })).toBeNull();
});

test("shows progress and cancels an in-flight transfer", async () => {
  const user = userEvent.setup();
  let observedSignal: AbortSignal | undefined;
  const onTransfer = vi.fn((
    _files: readonly File[],
    signal: AbortSignal,
    onProgress: (progress: AttachmentTransferProgress) => void,
  ) => {
    observedSignal = signal;
    onProgress({
      fileIndex: 0,
      fileCount: 1,
      fileName: "notes.md",
      fileBytesSent: 5,
      fileSizeBytes: 5,
      totalBytesSent: 5,
      totalSizeBytes: 5,
    });
    return new Promise<void>((_resolve, reject) => {
      signal.addEventListener("abort", () => reject(new DOMException("cancelled", "AbortError")), { once: true });
    });
  });
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={onTransfer} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());
  await user.click(screen.getByRole("button", { name: "Send files" }));

  const progressbar = await screen.findByRole("progressbar", { name: "Attachment transfer progress" });
  expect(progressbar.getAttribute("aria-valuenow")).toBe("100");
  expectButtonDisabled("Choose", true);
  await user.click(screen.getByRole("button", { name: "Cancel" }));

  expect(observedSignal?.aborted).toBe(true);
  expect((await screen.findByRole("alert")).textContent).toContain("This file batch was discarded");
  expectButtonDisabled("Send files", false);
});


test("explains session-end fallback when batch cleanup cannot be confirmed", async () => {
  const user = userEvent.setup();
  const transferFailure = new Error("connection queue failed");
  const onTransfer = vi.fn(async () => {
    throw new AttachmentBatchCleanupError(transferFailure, new Error("discard acknowledgement lost"));
  });
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={onTransfer} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());
  await user.click(screen.getByRole("button", { name: "Send files" }));

  const alert = await screen.findByRole("alert");
  expect(alert.textContent).toContain("connection queue failed");
  expect(alert.textContent).toContain("remaining files will be removed when the session ends");
});

test("shows transfer failures and permits an explicit retry", async () => {
  const user = userEvent.setup();
  let attempts = 0;
  const onTransfer = vi.fn(async () => {
    attempts += 1;
    if (attempts === 1) {
      throw new Error("connection queue failed");
    }
  });
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={onTransfer} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, attachment());
  await user.click(screen.getByRole("button", { name: "Send files" }));
  expect((await screen.findByRole("alert")).textContent).toContain("connection queue failed");

  await user.click(screen.getByRole("button", { name: "Send files" }));
  expect(await screen.findByText("Files verified and staged.")).toBeTruthy();
  expect(onTransfer).toHaveBeenCalledTimes(2);
});
