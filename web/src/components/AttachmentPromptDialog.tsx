import { useEffect, useState } from "react";

import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from "./ui/alert-dialog";

type AttachmentPromptDialogProps = {
  open: boolean;
  preview: string;
  appendEnter: boolean;
  onConfirm: () => Promise<void>;
  onCancel: () => Promise<void>;
  onComplete: (result: "committed" | "cancelled" | "failed") => void;
};

type PendingAction = "idle" | "committing" | "cancelling";

/** Requires an explicit choice before the Agent-prepared terminal bytes are committed. */
export function AttachmentPromptDialog({
  open,
  preview,
  appendEnter,
  onConfirm,
  onCancel,
  onComplete,
}: AttachmentPromptDialogProps) {
  const [pending, setPending] = useState<PendingAction>("idle");
  const [error, setError] = useState("");

  useEffect(() => {
    setPending("idle");
    setError("");
  }, [open, preview]);

  const confirm = async () => {
    if (pending !== "idle") return;
    setPending("committing");
    setError("");
    try {
      await onConfirm();
      onComplete("committed");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Attachment prompt action failed");
    } finally {
      setPending("idle");
    }
  };

  const cancel = async () => {
    if (pending !== "idle") return;
    setPending("cancelling");
    setError("");
    try {
      await onCancel();
      onComplete("cancelled");
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : "Attachment prompt action failed");
    } finally {
      setPending("idle");
    }
  };

  const busy = pending !== "idle";
  return (
    <AlertDialog open={open} onOpenChange={() => {}}>
      <AlertDialogContent className="max-w-xl">
        <AlertDialogTitle className="text-base font-semibold text-zinc-50">Confirm trusted attachment prompt</AlertDialogTitle>
        <AlertDialogDescription className="mt-2 text-sm leading-6 text-zinc-400">
          The Agent resolved staged files locally. Review the exact terminal text before continuing.
        </AlertDialogDescription>
        <pre
          className="mt-4 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md border border-zinc-800 bg-black p-3 text-xs leading-5 text-zinc-200"
          data-testid="attachment-prompt-preview"
        >{preview}</pre>
        <p className="mt-3 text-xs leading-5 text-amber-200">
          {appendEnter
            ? "Confirming writes this exact text and presses Enter."
            : "Confirming inserts this exact text without pressing Enter."}
        </p>
        {error ? <p className="mt-3 text-sm text-red-300" role="alert">{error}</p> : null}
        <AlertDialogFooter>
          {error ? (
            <AlertDialogCancel disabled={busy} onClick={() => onComplete("failed")}>Close</AlertDialogCancel>
          ) : (
            <>
              <AlertDialogCancel disabled={busy} onClick={() => void cancel()}>Cancel action</AlertDialogCancel>
              <AlertDialogAction
                disabled={busy}
                className="bg-emerald-400 text-zinc-950 hover:bg-emerald-300 focus-visible:ring-emerald-300"
                onClick={() => void confirm()}
              >
                {pending === "committing" ? "Sending…" : appendEnter ? "Confirm and send" : "Confirm insertion"}
              </AlertDialogAction>
            </>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
