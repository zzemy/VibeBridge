import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon } from "@xterm/addon-search";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { forwardRef, useEffect, useImperativeHandle, useRef } from "react";

export type TerminalViewHandle = {
  clear: () => void;
  reset: () => void;
  copySelection: () => Promise<boolean>;
  findNext: (query: string) => boolean;
  focus: () => void;
};

type Props = {
  chunks: Array<string | Uint8Array>;
  fontSize: number;
  onInput: (data: string) => void;
  onResize: (cols: number, rows: number) => void;
};

export const TerminalView = forwardRef<TerminalViewHandle, Props>(function TerminalView(
  { chunks, fontSize, onInput, onResize },
  ref,
) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const fitAddonRef = useRef<FitAddon | null>(null);
  const searchAddonRef = useRef<SearchAddon | null>(null);
  const writtenChunksRef = useRef(0);
  const chunksRef = useRef(chunks);
  chunksRef.current = chunks;

  useImperativeHandle(ref, () => ({
    clear() {
      terminalRef.current?.clear();
    },
    reset() {
      terminalRef.current?.reset();
      writtenChunksRef.current = 0;
    },
    async copySelection() {
      const selection = terminalRef.current?.getSelection() ?? "";
      if (!selection) {
        return false;
      }
      if (navigator.clipboard?.writeText) {
        try {
          await navigator.clipboard.writeText(selection);
          return true;
        } catch {
          // Fall through for HTTP LAN origins where Clipboard API may be blocked.
        }
      }
      const textarea = document.createElement("textarea");
      textarea.value = selection;
      textarea.style.position = "fixed";
      textarea.style.opacity = "0";
      document.body.appendChild(textarea);
      textarea.select();
      const copied = document.execCommand("copy");
      textarea.remove();
      return copied;
    },
    findNext(query: string) {
      return query ? (searchAddonRef.current?.findNext(query, { caseSensitive: false, incremental: true }) ?? false) : false;
    },
    focus() {
      terminalRef.current?.focus();
    },
  }), []);

  useEffect(() => {
    if (!containerRef.current) {
      return;
    }

    const terminal = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace',
      fontSize,
      lineHeight: 1.25,
      scrollback: 5_000,
      theme: {
        background: "#000000",
        foreground: "#e4e4e7",
        cursor: "#34d399",
        selectionBackground: "#064e3b",
      },
    });
    const fitAddon = new FitAddon();
    const searchAddon = new SearchAddon();

    terminal.loadAddon(fitAddon);
    terminal.loadAddon(searchAddon);
    terminal.open(containerRef.current);
    const inputDisposable = terminal.onData(onInput);
    terminalRef.current = terminal;
    fitAddonRef.current = fitAddon;
    searchAddonRef.current = searchAddon;

    const initialChunks = chunksRef.current.slice(writtenChunksRef.current);
    for (const chunk of initialChunks) {
      terminal.write(chunk);
    }
    writtenChunksRef.current = chunksRef.current.length;

    const fit = () => {
      try {
        fitAddon.fit();
        onResize(terminal.cols, terminal.rows);
      } catch {
        // xterm can throw while hidden or before layout settles.
      }
    };

    fit();
    const resizeObserver = new ResizeObserver(fit);
    resizeObserver.observe(containerRef.current);

    return () => {
      inputDisposable.dispose();
      resizeObserver.disconnect();
      terminal.dispose();
      terminalRef.current = null;
      fitAddonRef.current = null;
      searchAddonRef.current = null;
    };
  }, [onInput, onResize]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) {
      return;
    }
    terminal.options.fontSize = fontSize;
    try {
      fitAddonRef.current?.fit();
      onResize(terminal.cols, terminal.rows);
    } catch {
      // Layout can be temporarily unavailable while the mobile keyboard animates.
    }
  }, [fontSize, onResize]);

  useEffect(() => {
    const terminal = terminalRef.current;
    if (!terminal) {
      return;
    }

    const nextChunks = chunks.slice(writtenChunksRef.current);
    for (const chunk of nextChunks) {
      terminal.write(chunk);
    }
    writtenChunksRef.current = chunks.length;
  }, [chunks]);

  return <div ref={containerRef} className="h-full min-h-0 w-full p-2" onPointerDown={() => terminalRef.current?.focus()} />;
});
