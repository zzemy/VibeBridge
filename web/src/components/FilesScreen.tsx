import { FileText, FolderOpen, Paperclip } from "lucide-react";
import { useEffect, useState } from "react";
import { AttachmentComposer } from "./AttachmentComposer";
import { t, subscribeLang } from "../lib/i18n";
import type { AttachmentTransferProgress } from "../lib/attachments";

type Props = {
  available: boolean;
  canSend: boolean;
  onTransfer: (
    files: readonly File[],
    signal: AbortSignal,
    onProgress: (progress: AttachmentTransferProgress) => void,
  ) => Promise<Uint8Array[]>;
  composerKey: number;
  stagedCount: number;
};

export function FilesScreen({ available, canSend, onTransfer, composerKey, stagedCount }: Props) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-5">
      <h1 className="mb-4 text-xl font-semibold text-zinc-50">{t("files.title")}</h1>

      {available ? (
        <div className="space-y-4">
          {/* Staged files summary */}
          {stagedCount > 0 ? (
            <div className="flex items-center gap-2.5 rounded-lg border border-emerald-400/20 bg-emerald-400/5 px-4 py-2.5">
              <Paperclip className="size-4 text-emerald-400" aria-hidden="true" />
              <span className="text-sm text-emerald-200">
                {t("files.stagedCount", { n: stagedCount })}
              </span>
            </div>
          ) : null}

          {/* Attachment composer */}
          <div className="overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900/50">
            <div className="flex items-center gap-2 border-b border-zinc-800/60 px-4 py-3">
              <FolderOpen className="size-4 text-zinc-400" aria-hidden="true" />
              <span className="text-sm font-medium text-zinc-200">{t("attach.attachFiles")}</span>
            </div>
            <div className="p-4">
              <AttachmentComposer
                key={composerKey}
                disabled={!canSend}
                transferEnabled={canSend}
                onTransfer={onTransfer}
              />
            </div>
          </div>
        </div>
      ) : (
        /* Not available empty state */
        <div className="flex flex-col items-center gap-3 rounded-xl border border-dashed border-zinc-800 px-6 py-10 text-center">
          <div className="grid size-12 place-items-center rounded-full bg-zinc-800/50 text-zinc-600">
            <FileText className="size-6" aria-hidden="true" />
          </div>
          <div>
            <p className="text-sm font-medium text-zinc-300">{t("files.noTransfers")}</p>
            <p className="mt-1 text-xs text-zinc-500">{t("files.notAvailable")}</p>
          </div>
        </div>
      )}
    </div>
  );
}
