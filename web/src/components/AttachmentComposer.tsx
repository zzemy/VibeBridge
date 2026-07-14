import { Camera, CheckCircle2, CircleAlert, FileText, Image as ImageIcon, Paperclip, UploadCloud, X } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "./ui/button";
import {
  AttachmentBatchCleanupError,
  attachmentMaxFilesPerAction,
  formatAttachmentBytes,
  type AttachmentTransferProgress,
  validateAttachmentSelection,
} from "../lib/attachments";

type AttachmentSelection = {
  id: string;
  file: File;
  previewUrl?: string;
};

type AttachmentComposerProps = {
  disabled: boolean;
  transferEnabled: boolean;
  onTransfer: (
    files: readonly File[],
    signal: AbortSignal,
    onProgress: (progress: AttachmentTransferProgress) => void,
  ) => Promise<unknown>;
};

function isAbortError(cause: unknown): boolean {
  return cause instanceof DOMException && cause.name === "AbortError";
}

export function AttachmentComposer({ disabled, transferEnabled, onTransfer }: AttachmentComposerProps) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  const cameraInputRef = useRef<HTMLInputElement | null>(null);
  const previewUrlsRef = useRef(new Set<string>());
  const abortRef = useRef<AbortController | null>(null);
  const [selection, setSelection] = useState<AttachmentSelection[]>([]);
  const [error, setError] = useState("");
  const [progress, setProgress] = useState<AttachmentTransferProgress | null>(null);
  const [state, setState] = useState<"idle" | "uploading" | "success" | "failed">("idle");

  useEffect(() => {
    return () => {
      abortRef.current?.abort();
      for (const url of previewUrlsRef.current) {
        revokePreview(url);
      }
      previewUrlsRef.current.clear();
    };
  }, []);

  const disposePreview = (url: string | undefined) => {
    revokePreview(url);
    if (url) {
      previewUrlsRef.current.delete(url);
    }
  };

  const selectFiles = (files: File[]) => {
    setError("");
    setState("idle");
    setProgress(null);
    try {
      validateAttachmentSelection(files);
    } catch (cause) {
      setSelection((previous) => {
        for (const item of previous) {
          disposePreview(item.previewUrl);
        }
        return [];
      });
      setError(cause instanceof Error ? cause.message : "Selected files are not supported");
      return;
    }

    const next = files.map((file, index) => {
      const previewUrl = file.type.startsWith("image/") ? createPreview(file) : undefined;
      if (previewUrl) {
        previewUrlsRef.current.add(previewUrl);
      }
      return {
        id: `${file.name}-${file.size}-${file.lastModified}-${index}`,
        file,
        previewUrl,
      };
    });
    setSelection((previous) => {
      for (const item of previous) {
        disposePreview(item.previewUrl);
      }
      return next;
    });
  };

  const removeFile = (id: string) => {
    setSelection((previous) => {
      const removed = previous.find((item) => item.id === id);
      disposePreview(removed?.previewUrl);
      return previous.filter((item) => item.id !== id);
    });
    setProgress(null);
    setState("idle");
    setError("");
  };

  const transfer = async () => {
    if (!transferEnabled || selection.length === 0 || state === "uploading") {
      return;
    }
    const controller = new AbortController();
    abortRef.current = controller;
    setError("");
    setProgress(null);
    setState("uploading");
    try {
      await onTransfer(selection.map((item) => item.file), controller.signal, setProgress);
      setState("success");
    } catch (cause) {
      if (cause instanceof AttachmentBatchCleanupError) {
        if (isAbortError(cause.transferCause)) {
          setError("Transfer cancelled, but cleanup could not be confirmed. Remaining files will be removed when the session ends.");
        } else {
          setError(`${cause.message}. Cleanup could not be confirmed; remaining files will be removed when the session ends.`);
        }
      } else if (isAbortError(cause)) {
        setError("Transfer cancelled. This file batch was discarded.");
      } else {
        setError(cause instanceof Error ? cause.message : "Attachment transfer failed");
      }
      setState("failed");
    } finally {
      abortRef.current = null;
    }
  };

  const cancelTransfer = () => {
    abortRef.current?.abort();
  };

  const clearSelection = () => {
    for (const item of selection) {
      disposePreview(item.previewUrl);
    }
    setSelection([]);
    setProgress(null);
    setError("");
    setState("idle");
    if (inputRef.current) {
      inputRef.current.value = "";
    }
  };

  const totalBytes = selection.reduce((total, item) => total + item.file.size, 0);
  const progressPercent = progress && progress.totalSizeBytes > 0
    ? Math.min(100, Math.round((progress.totalBytesSent / progress.totalSizeBytes) * 100))
    : 0;

  return (
    <div className="rounded-md border border-zinc-800 bg-zinc-900/50 p-3" data-testid="attachment-composer">
      <div className="flex items-start justify-between gap-3">
        <div className="min-w-0">
          <div className="flex items-center gap-2 text-sm font-medium text-zinc-200">
            <Paperclip className="size-4 text-emerald-300" aria-hidden="true" />
            Attach files
          </div>
          <p className="mt-1 text-xs leading-5 text-zinc-500">
            Review up to {attachmentMaxFilesPerAction} files before sending them to this workspace.
          </p>
        </div>
        <input
          ref={inputRef}
          type="file"
          multiple
          className="sr-only"
          accept=".png,.jpg,.jpeg,.webp,.gif,.pdf,.txt,.log,.md,.markdown,.json,.yaml,.yml,.toml,.csv"
          disabled={disabled || state === "uploading"}
          onChange={(event) => {
            selectFiles(Array.from(event.target.files ?? []));
            event.target.value = "";
          }}
        />
        <input
          ref={cameraInputRef}
          type="file"
          capture="environment"
          className="sr-only"
          accept="image/png,image/jpeg,image/webp,image/gif"
          disabled={disabled || state === "uploading"}
          onChange={(event) => {
            selectFiles(Array.from(event.target.files ?? []));
            event.target.value = "";
          }}
        />
        <div className="flex shrink-0 items-center gap-1">
          <Button
            type="button"
            size="sm"
            variant="ghost"
            disabled={disabled || state === "uploading"}
            onClick={() => cameraInputRef.current?.click()}
          >
            <Camera className="size-3.5" aria-hidden="true" />
            Camera
          </Button>
          <Button
            type="button"
            size="sm"
            variant="secondary"
            disabled={disabled || state === "uploading"}
            onClick={() => inputRef.current?.click()}
          >
            <UploadCloud className="size-3.5" aria-hidden="true" />
            Choose
          </Button>
        </div>
      </div>

      {selection.length > 0 ? (
        <div className="mt-3 space-y-2" aria-label="Selected attachments">
          {selection.map((item) => (
            <div key={item.id} className="flex items-center gap-2 rounded border border-zinc-800 bg-zinc-950/70 p-2">
              {item.previewUrl ? (
                <img src={item.previewUrl} alt="" className="size-10 rounded object-cover" />
              ) : item.file.type.startsWith("image/") ? (
                <ImageIcon className="mx-2 size-5 text-zinc-500" aria-hidden="true" />
              ) : (
                <FileText className="mx-2 size-5 text-zinc-500" aria-hidden="true" />
              )}
              <div className="min-w-0 flex-1">
                <p className="truncate text-sm text-zinc-200" title={item.file.name}>{item.file.name}</p>
                <p className="text-xs text-zinc-500">{formatAttachmentBytes(item.file.size)} · {item.file.type}</p>
              </div>
              <Button
                type="button"
                size="icon"
                variant="ghost"
                className="size-8 shrink-0 text-zinc-500 hover:text-red-300"
                disabled={state === "uploading"}
                onClick={() => removeFile(item.id)}
                aria-label={`Remove ${item.file.name}`}
              >
                <X className="size-4" aria-hidden="true" />
              </Button>
            </div>
          ))}
          <div className="flex items-center justify-between gap-2 text-xs text-zinc-500">
            <span>{selection.length} file{selection.length === 1 ? "" : "s"} · {formatAttachmentBytes(totalBytes)}</span>
            <div className="flex items-center gap-1">
              <Button type="button" size="sm" variant="ghost" className="h-7 px-2 text-xs" disabled={state === "uploading"} onClick={clearSelection}>
                Clear
              </Button>
              {state === "uploading" ? (
                <Button type="button" size="sm" variant="secondary" className="h-7 px-2 text-xs" onClick={cancelTransfer}>
                  Cancel
                </Button>
              ) : (
                <Button type="button" size="sm" className="h-7 px-2 text-xs" disabled={disabled || !transferEnabled} onClick={() => void transfer()}>
                  Send files
                </Button>
              )}
            </div>
          </div>
        </div>
      ) : null}

      {state === "uploading" && progress ? (
        <div className="mt-3" role="status" aria-live="polite">
          <div className="mb-1 flex justify-between gap-2 text-xs text-zinc-400">
            <span className="truncate">Sending {progress.fileName}</span>
            <span className="tabular-nums">{progressPercent}%</span>
          </div>
          <div
            className="h-1.5 overflow-hidden rounded-full bg-zinc-800"
            role="progressbar"
            aria-label="Attachment transfer progress"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={progressPercent}
          >
            <div className="h-full rounded-full bg-emerald-400 transition-[width]" style={{ width: `${progressPercent}%` }} />
          </div>
        </div>
      ) : null}
      {state === "success" ? (
        <p className="mt-2 flex items-center gap-1 text-xs text-emerald-300" role="status">
          <CheckCircle2 className="size-3.5" aria-hidden="true" /> Files verified and staged.
        </p>
      ) : null}
      {error ? (
        <p className="mt-2 flex items-start gap-1 text-xs text-red-300" role="alert">
          <CircleAlert className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" /> {error}
        </p>
      ) : null}
      {!transferEnabled ? <p className="mt-2 text-xs text-zinc-600">Attachment transfer is not available on this Agent yet.</p> : null}
    </div>
  );
}

function createPreview(file: File): string | undefined {
  return typeof URL.createObjectURL === "function" ? URL.createObjectURL(file) : undefined;
}

function revokePreview(url: string | undefined) {
  if (url && typeof URL.revokeObjectURL === "function") {
    URL.revokeObjectURL(url);
  }
}