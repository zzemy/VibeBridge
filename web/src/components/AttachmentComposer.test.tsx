import { cleanup, render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, expect, test, vi } from "vitest";

import { AttachmentComposer } from "./AttachmentComposer";

afterEach(cleanup);

function attachment(name = "notes.md", content = "hello") {
  return new File([content], name, { type: "text/markdown", lastModified: 1 });
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
  expect((screen.getByRole("button", { name: "Send files" }) as HTMLButtonElement).disabled).toBe(true);
  expect(onTransfer).not.toHaveBeenCalled();
});

test("confirms a supported selection and reports that files were sent for verification", async () => {
  const user = userEvent.setup();
  const onTransfer = vi.fn(async (_files, _signal, onProgress) => {
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
  expect(await screen.findByText("Files sent for verification.")).toBeTruthy();
});

test("shows local policy failures before confirmation", async () => {
  const user = userEvent.setup();
  const { container } = render(<AttachmentComposer disabled={false} transferEnabled onTransfer={vi.fn()} />);
  const input = container.querySelector<HTMLInputElement>('input[type="file"]');
  if (!input) throw new Error("file input missing");

  await user.upload(input, new File(["x"], "notes.md", { type: "text/html" }));

  expect(screen.getByRole("alert").textContent).toContain("mismatched content type");
  expect(screen.queryByRole("button", { name: "Send files" })).toBeNull();
});