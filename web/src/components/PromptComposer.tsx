import { ClipboardPaste, SendHorizontal } from "lucide-react";
import { useRef, useState } from "react";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";

type Props = {
  disabled: boolean;
  onSend: (value: string) => void;
};

export function PromptComposer({ disabled, onSend }: Props) {
  const [value, setValue] = useState("");
  const isComposingRef = useRef(false);

  function submit() {
    const trimmed = value.trim();
    if (!trimmed || disabled || isComposingRef.current) {
      return;
    }

    onSend(trimmed);
    setValue("");
  }

  async function pasteFromClipboard() {
    if (!navigator.clipboard?.readText) {
      return;
    }

    const text = await navigator.clipboard.readText();
    if (!text) {
      return;
    }

    if (text.length > 8000 && !window.confirm("Paste more than 8,000 characters into the prompt composer?")) {
      return;
    }

    setValue((current) => `${current}${current ? "\n" : ""}${text}`);
  }

  return (
    <div className="flex items-end gap-2 rounded-md border border-zinc-800 bg-zinc-900/90 p-2">
      <Textarea
        value={value}
        disabled={disabled}
        rows={2}
        placeholder="Tell the local AI CLI what to do..."
        className="max-h-32 min-h-12 resize-none border-zinc-800 bg-zinc-950/80 text-sm text-zinc-100 placeholder:text-zinc-600"
        onChange={(event) => setValue(event.target.value)}
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
        <Button type="button" variant="secondary" size="icon" className="size-10" onClick={pasteFromClipboard}>
          <ClipboardPaste className="size-4" aria-hidden="true" />
          <span className="sr-only">Paste from clipboard</span>
        </Button>
        <Button type="button" disabled={disabled || value.trim() === ""} size="icon" className="size-10" onClick={submit}>
          <SendHorizontal className="size-4" aria-hidden="true" />
          <span className="sr-only">Send prompt</span>
        </Button>
      </div>
    </div>
  );
}
