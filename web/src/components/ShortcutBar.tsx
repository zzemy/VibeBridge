import { ArrowDown, ArrowLeft, ArrowRight, ArrowUp } from "lucide-react";
import { Button } from "./ui/button";
import { terminalKeys } from "../lib/terminalKeys";

type Props = {
  disabled: boolean;
  onInput: (value: string) => void;
};

const textShortcuts = [
  { label: "Enter", value: terminalKeys.enter },
  { label: "Esc", value: terminalKeys.escape },
  { label: "Ctrl+C", value: terminalKeys.ctrlC },
  { label: "Tab", value: terminalKeys.tab },
  { label: "Y", value: "y" },
  { label: "N", value: "n" },
] as const;

const arrowShortcuts = [
  { label: "Up", icon: ArrowUp, value: terminalKeys.arrowUp },
  { label: "Down", icon: ArrowDown, value: terminalKeys.arrowDown },
  { label: "Left", icon: ArrowLeft, value: terminalKeys.arrowLeft },
  { label: "Right", icon: ArrowRight, value: terminalKeys.arrowRight },
] as const;

export function ShortcutBar({ disabled, onInput }: Props) {
  return (
    <div className="flex gap-2 overflow-x-auto pb-1" aria-label="Terminal shortcuts">
      {textShortcuts.map((shortcut) => (
        <Button
          key={shortcut.label}
          type="button"
          size="sm"
          variant="secondary"
          disabled={disabled}
          className="h-9 shrink-0"
          onClick={() => onInput(shortcut.value)}
        >
          {shortcut.label}
        </Button>
      ))}
      {arrowShortcuts.map((shortcut) => {
        const Icon = shortcut.icon;
        return (
          <Button
            key={shortcut.label}
            type="button"
            size="icon"
            variant="secondary"
            disabled={disabled}
            className="size-9 shrink-0"
            onClick={() => onInput(shortcut.value)}
          >
            <Icon className="size-4" aria-hidden="true" />
            <span className="sr-only">{shortcut.label}</span>
          </Button>
        );
      })}
    </div>
  );
}
