import { ChevronUp, ClipboardPaste, CornerDownLeft, History, SendHorizontal, Sparkles } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";
import { t, tArray, subscribeLang } from "../lib/i18n";

const maxPromptLength = 8_000;

type InputMode = "send" | "insert";

type Props = {
  disabled: boolean;
  historyStorageKey: string;
  storageKey: string;
  onSubmit: (value: string, appendEnter: boolean) => void | Promise<void>;
};

function readDraft(storageKey: string) {
  try {
    return sessionStorage.getItem(storageKey)?.slice(0, maxPromptLength) ?? "";
  } catch {
    return "";
  }
}

function readHistory(storageKey: string) {
  try {
    const value: unknown = JSON.parse(sessionStorage.getItem(storageKey) ?? "[]");
    return Array.isArray(value) ? value.filter((item): item is string => typeof item === "string").slice(0, 8) : [];
  } catch {
    return [];
  }
}

export function PromptComposer({ disabled, historyStorageKey, storageKey, onSubmit }: Props) {
  const [value, setValue] = useState(() => readDraft(storageKey));
  const [mode, setMode] = useState<InputMode>("send");
  const [notice, setNotice] = useState("");
  const [history, setHistory] = useState(() => readHistory(historyStorageKey));
  const [quickActionsOpen, setQuickActionsOpen] = useState(false);
  const [submitting, setSubmitting] = useState(false);
  const isComposingRef = useRef(false);
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  useEffect(() => {
    setValue(readDraft(storageKey));
    setNotice("");
  }, [storageKey]);

  useEffect(() => {
    setHistory(readHistory(historyStorageKey));
  }, [historyStorageKey]);

  useEffect(() => {
    try {
      if (value) {
        sessionStorage.setItem(storageKey, value);
      } else {
        sessionStorage.removeItem(storageKey);
      }
    } catch {
      setNotice(t("prompt.draftUnavailable"));
    }
  }, [storageKey, value]);

  function updateValue(nextValue: string) {
    if (nextValue.length > maxPromptLength) {
      setNotice(t("prompt.charLimit", { n: maxPromptLength.toLocaleString() }));
      setValue(nextValue.slice(0, maxPromptLength));
      return;
    }

    setNotice("");
    setValue(nextValue);
  }

  async function submit() {
    if (!value.trim() || disabled || submitting || isComposingRef.current) {
      return;
    }

    const submittedValue = value;
    setSubmitting(true);
    setNotice("");
    try {
      await onSubmit(submittedValue, mode === "send");
      const nextHistory = [submittedValue, ...history.filter((item) => item !== submittedValue)].slice(0, 8);
      setHistory(nextHistory);
      let historyStored = true;
      try {
        sessionStorage.setItem(historyStorageKey, JSON.stringify(nextHistory));
      } catch {
        historyStored = false;
        setNotice(t("prompt.historyUnavailable"));
      }
      setValue("");
      if (historyStored) {
        setNotice("");
      }
    } catch (cause) {
      setNotice(cause instanceof Error ? cause.message : t("prompt.prepFailed"));
    } finally {
      setSubmitting(false);
    }
  }

  function appendPrompt(prompt: string) {
    const nextValue = value ? `${value}\n${prompt}` : prompt;
    updateValue(nextValue);
  }

  async function pasteFromClipboard() {
    if (disabled) {
      return;
    }
    if (!navigator.clipboard?.readText) {
      setNotice(t("prompt.clipboardUnavailable"));
      return;
    }

    try {
      const text = await navigator.clipboard.readText();
      if (!text) {
        return;
      }

      const nextValue = `${value}${value ? "\n" : ""}${text}`;
      if (nextValue.length > maxPromptLength) {
        setNotice(t("prompt.clipboardExceeds", { n: maxPromptLength.toLocaleString() }));
        return;
      }
      updateValue(nextValue);
    } catch {
      setNotice(t("prompt.clipboardDenied"));
    }
  }

  const isEmpty = value.trim() === "";
  const interactionDisabled = disabled || submitting;
  const quickPrompts = tArray("prompt.quickPrompts");

  return (
    <div className="rounded-md border border-zinc-800 bg-zinc-900/90 p-2">
      <div className="mb-2 flex items-center justify-between gap-3">
        <div className="inline-flex rounded-md border border-zinc-800 p-0.5" role="group" aria-label={t("prompt.modeLabel")}>
          <Button
            type="button"
            size="sm"
            variant={mode === "send" ? "default" : "ghost"}
            className="h-7 px-2 text-xs"
            aria-pressed={mode === "send"}
            disabled={interactionDisabled}
            onClick={() => setMode("send")}
          >
            {t("prompt.sendEnter")}
          </Button>
          <Button
            type="button"
            size="sm"
            variant={mode === "insert" ? "default" : "ghost"}
            className="h-7 px-2 text-xs"
            aria-pressed={mode === "insert"}
            disabled={interactionDisabled}
            onClick={() => setMode("insert")}
          >
            {t("prompt.insertOnly")}
          </Button>
        </div>
        <span className="shrink-0 text-xs tabular-nums text-zinc-500" aria-live="polite">
          {value.length.toLocaleString()} / {maxPromptLength.toLocaleString()}
        </span>
      </div>

      <div className="mb-2 flex items-center gap-1 overflow-x-auto">
        <Button
          type="button"
          size="sm"
          variant="ghost"
          className="h-7 shrink-0 px-2 text-xs text-zinc-400"
          aria-expanded={quickActionsOpen}
          disabled={interactionDisabled}
          onClick={() => setQuickActionsOpen((open) => !open)}
        >
          {quickActionsOpen ? <ChevronUp className="size-3.5" aria-hidden="true" /> : <Sparkles className="size-3.5" aria-hidden="true" />}
          {t("prompt.quickPromptsBtn")}
        </Button>
        {history.length > 0 ? (
          <span className="flex items-center gap-1 text-xs text-zinc-600">
            <History className="size-3.5" aria-hidden="true" />
            {t("prompt.recent", { n: history.length })}
          </span>
        ) : null}
      </div>

      {quickActionsOpen ? (
        <div className="mb-2 flex gap-1.5 overflow-x-auto pb-1" aria-label={t("prompt.quickPromptsLabel")}>
          {quickPrompts.map((prompt) => (
            <Button key={prompt} type="button" size="sm" variant="secondary" className="h-8 shrink-0 px-2 text-xs" disabled={interactionDisabled} onClick={() => appendPrompt(prompt)}>
              {prompt.split(" ").slice(0, 3).join(" ")}
            </Button>
          ))}
          {history.map((prompt) => (
            <Button key={`history-${prompt}`} type="button" size="sm" variant="ghost" className="h-8 max-w-48 shrink-0 truncate px-2 text-xs text-zinc-400" title={prompt} disabled={interactionDisabled} onClick={() => appendPrompt(prompt)}>
              <History className="size-3.5" aria-hidden="true" />
              <span className="truncate">{prompt}</span>
            </Button>
          ))}
        </div>
      ) : null}

      <div className="flex items-end gap-2">
        <Textarea
          value={value}
          disabled={interactionDisabled}
          rows={2}
          maxLength={maxPromptLength}
          placeholder={t("prompt.placeholder")}
          className="max-h-32 min-h-12 resize-none border-zinc-800 bg-zinc-950/80 text-sm text-zinc-100 placeholder:text-zinc-600"
          onChange={(event) => updateValue(event.target.value)}
          onCompositionStart={() => {
            isComposingRef.current = true;
          }}
          onCompositionEnd={() => {
            isComposingRef.current = false;
          }}
          onKeyDown={(event) => {
            if ((event.ctrlKey || event.metaKey) && event.key === "Enter") {
              event.preventDefault();
              submit();
            }
          }}
        />
        <div className="flex shrink-0 flex-col gap-2">
          <Button type="button" variant="secondary" size="icon" disabled={interactionDisabled} className="size-10" onClick={pasteFromClipboard}>
            <ClipboardPaste className="size-4" aria-hidden="true" />
            <span className="sr-only">{t("prompt.pasteClipboard")}</span>
          </Button>
          <Button type="button" disabled={interactionDisabled || isEmpty} size="icon" className="size-10" onClick={submit}>
            {mode === "send" ? <SendHorizontal className="size-4" aria-hidden="true" /> : <CornerDownLeft className="size-4" aria-hidden="true" />}
            <span className="sr-only">{mode === "send" ? t("prompt.sendPrompt") : t("prompt.insertPrompt")}</span>
          </Button>
        </div>
      </div>
      {notice ? <p className="mt-2 text-xs text-amber-300" role="status">{notice}</p> : null}
    </div>
  );
}
