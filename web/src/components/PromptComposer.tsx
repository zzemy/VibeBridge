import { SendHorizontal } from "lucide-react";
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
      <Button type="button" disabled={disabled || value.trim() === ""} className="h-12 px-3" onClick={submit}>
        <SendHorizontal className="size-4" aria-hidden="true" />
        <span className="sr-only">Send prompt</span>
      </Button>
    </div>
  );
}
