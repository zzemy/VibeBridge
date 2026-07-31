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
import { t, subscribeLang } from "../lib/i18n";

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
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

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
      setError(cause instanceof Error ? cause.message : t("attachDialog.actionFailed"));
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
      setError(cause instanceof Error ? cause.message : t("attachDialog.actionFailed"));
    } finally {
      setPending("idle");
    }
  };

  const busy = pending !== "idle";
  return (
    <AlertDialog open={open} onOpenChange={() => {}}>
      <AlertDialogContent className="max-w-xl">
        <AlertDialogTitle className="text-base font-semibold text-zinc-50">{t("attachDialog.title")}</AlertDialogTitle>
        <AlertDialogDescription className="mt-2 text-sm leading-6 text-zinc-400">
          {t("attachDialog.desc")}
        </AlertDialogDescription>
        <pre
          className="mt-4 max-h-72 overflow-auto whitespace-pre-wrap break-words rounded-md border border-zinc-800 bg-black p-3 text-xs leading-5 text-zinc-200"
          data-testid="attachment-prompt-preview"
        >{preview}</pre>
        <p className="mt-3 text-xs leading-5 text-amber-200">
          {appendEnter
            ? t("attachDialog.confirmSend")
            : t("attachDialog.confirmInsert")}
        </p>
        {error ? <p className="mt-3 text-sm text-red-300" role="alert">{error}</p> : null}
        <AlertDialogFooter>
          {error ? (
            <AlertDialogCancel disabled={busy} onClick={() => onComplete("failed")}>{t("attachDialog.close")}</AlertDialogCancel>
          ) : (
            <>
              <AlertDialogCancel disabled={busy} onClick={() => void cancel()}>{t("attachDialog.cancelAction")}</AlertDialogCancel>
              <AlertDialogAction
                disabled={busy}
                className="bg-emerald-400 text-zinc-950 hover:bg-emerald-300 focus-visible:ring-emerald-300"
                onClick={() => void confirm()}
              >
                {pending === "committing" ? t("attachDialog.sending") : appendEnter ? t("attachDialog.confirmAndSend") : t("attachDialog.confirmInsertion")}
              </AlertDialogAction>
            </>
          )}
        </AlertDialogFooter>
      </AlertDialogContent>
    </AlertDialog>
  );
}
