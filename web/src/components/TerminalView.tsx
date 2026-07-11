import { FitAddon } from "@xterm/addon-fit";
import { Terminal } from "@xterm/xterm";
import "@xterm/xterm/css/xterm.css";
import { useEffect, useRef } from "react";

type Props = {
  chunks: Array<string | Uint8Array>;
  onResize: (cols: number, rows: number) => void;
};

export function TerminalView({ chunks, onResize }: Props) {
  const containerRef = useRef<HTMLDivElement | null>(null);
  const terminalRef = useRef<Terminal | null>(null);
  const writtenChunksRef = useRef(0);

  useEffect(() => {
    if (!containerRef.current) {
      return;
    }

    const terminal = new Terminal({
      cursorBlink: true,
      convertEol: true,
      fontFamily: 'ui-monospace, SFMono-Regular, Consolas, "Liberation Mono", monospace',
      fontSize: 13,
      lineHeight: 1.25,
      theme: {
        background: "#000000",
        foreground: "#e4e4e7",
        cursor: "#34d399",
        selectionBackground: "#064e3b",
      },
    });
    const fitAddon = new FitAddon();

    terminal.loadAddon(fitAddon);
    terminal.open(containerRef.current);
    terminalRef.current = terminal;

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
      resizeObserver.disconnect();
      terminal.dispose();
      terminalRef.current = null;
    };
  }, [onResize]);

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

  return <div ref={containerRef} className="h-full min-h-0 w-full p-2" />;
}
