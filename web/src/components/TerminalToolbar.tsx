import { ClipboardCopy, Eraser, Keyboard, Search, X, ZoomIn, ZoomOut } from "lucide-react";
import { type FormEvent, useState } from "react";
import { Button } from "./ui/button";

type Props = {
  canZoomIn: boolean;
  canZoomOut: boolean;
  onClear: () => void;
  onCopy: () => void;
  onFocus: () => void;
  onSearch: (query: string) => void;
  onZoomIn: () => void;
  onZoomOut: () => void;
};

export function TerminalToolbar({
  canZoomIn,
  canZoomOut,
  onClear,
  onCopy,
  onFocus,
  onSearch,
  onZoomIn,
  onZoomOut,
}: Props) {
  const [searchOpen, setSearchOpen] = useState(false);
  const [query, setQuery] = useState("");

  function submitSearch(event: FormEvent) {
    event.preventDefault();
    if (query.trim()) {
      onSearch(query.trim());
    }
  }

  return (
    <div className="flex min-h-10 items-center gap-1 overflow-x-auto border-b border-zinc-800 bg-zinc-950/95 px-1.5 py-1">
      {searchOpen ? (
        <form className="flex min-w-0 flex-1 items-center gap-1" onSubmit={submitSearch}>
          <Search className="ml-1 size-3.5 shrink-0 text-zinc-500" aria-hidden="true" />
          <input
            autoFocus
            value={query}
            placeholder="Find terminal output"
            className="h-8 min-w-36 flex-1 bg-transparent px-1 text-sm text-zinc-100 outline-none placeholder:text-zinc-600"
            onChange={(event) => setQuery(event.target.value)}
          />
          <Button type="submit" size="sm" variant="secondary" className="h-8 shrink-0 px-2 text-xs">
            Next
          </Button>
          <Button type="button" size="icon" variant="ghost" className="size-8 shrink-0" title="Close search" onClick={() => setSearchOpen(false)}>
            <X className="size-4" aria-hidden="true" />
            <span className="sr-only">Close search</span>
          </Button>
        </form>
      ) : (
        <>
          <Button type="button" size="icon" variant="ghost" className="size-8 shrink-0" title="Focus terminal keyboard" onClick={onFocus}>
            <Keyboard className="size-4" aria-hidden="true" />
            <span className="sr-only">Focus terminal keyboard</span>
          </Button>
          <Button type="button" size="icon" variant="ghost" className="size-8 shrink-0" title="Search output" onClick={() => setSearchOpen(true)}>
            <Search className="size-4" aria-hidden="true" />
            <span className="sr-only">Search output</span>
          </Button>
          <Button type="button" size="icon" variant="ghost" className="size-8 shrink-0" title="Copy terminal selection" onClick={onCopy}>
            <ClipboardCopy className="size-4" aria-hidden="true" />
            <span className="sr-only">Copy terminal selection</span>
          </Button>
          <Button type="button" size="icon" variant="ghost" className="size-8 shrink-0" title="Clear terminal view" onClick={onClear}>
            <Eraser className="size-4" aria-hidden="true" />
            <span className="sr-only">Clear terminal view</span>
          </Button>
          <span className="mx-1 h-5 w-px shrink-0 bg-zinc-800" aria-hidden="true" />
          <Button type="button" size="icon" variant="ghost" disabled={!canZoomOut} className="size-8 shrink-0" title="Decrease terminal font size" onClick={onZoomOut}>
            <ZoomOut className="size-4" aria-hidden="true" />
            <span className="sr-only">Decrease terminal font size</span>
          </Button>
          <Button type="button" size="icon" variant="ghost" disabled={!canZoomIn} className="size-8 shrink-0" title="Increase terminal font size" onClick={onZoomIn}>
            <ZoomIn className="size-4" aria-hidden="true" />
            <span className="sr-only">Increase terminal font size</span>
          </Button>
        </>
      )}
    </div>
  );
}
