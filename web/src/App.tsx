import { Power, Radio, SendHorizontal, ShieldCheck, WifiOff } from "lucide-react";
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { ConnectionStatus } from "./components/ConnectionStatus";
import { PromptComposer } from "./components/PromptComposer";
import { ShortcutBar } from "./components/ShortcutBar";
import { type ServerMessage, isServerMessage } from "./lib/protocol";
import { terminalKeys } from "./lib/terminalKeys";

type ConnectionState = "missing-token" | "connecting" | "connected" | "closed" | "error";
const TerminalView = lazy(() => import("./components/TerminalView").then((module) => ({ default: module.TerminalView })));

export function App() {
  const [connectionState, setConnectionState] = useState<ConnectionState>("connecting");
  const [terminalChunks, setTerminalChunks] = useState<string[]>([]);
  const socketRef = useRef<WebSocket | null>(null);

  const token = useMemo(() => {
    return new URLSearchParams(window.location.search).get("token") ?? "";
  }, []);

  const wsUrl = useMemo(() => {
    if (!token) {
      return "";
    }
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${protocol}//${window.location.host}/ws?token=${encodeURIComponent(token)}`;
  }, [token]);

  useEffect(() => {
    if (!wsUrl) {
      setConnectionState("missing-token");
      setTerminalChunks(["missing session token\r\n"]);
      return;
    }

    setConnectionState("connecting");
    const socket = new WebSocket(wsUrl);
    socketRef.current = socket;

    socket.addEventListener("open", () => {
      setConnectionState("connected");
    });

    socket.addEventListener("message", (event: MessageEvent<string>) => {
      let parsed: unknown;
      try {
        parsed = JSON.parse(event.data);
      } catch {
        setTerminalChunks((chunks) => [...chunks, event.data]);
        return;
      }

      if (!isServerMessage(parsed)) {
        setTerminalChunks((chunks) => [...chunks, "received malformed server message\r\n"]);
        return;
      }

      handleServerMessage(parsed);
    });

    socket.addEventListener("close", () => {
      setConnectionState("closed");
      setTerminalChunks((chunks) => [...chunks, "connection closed\r\n"]);
    });

    socket.addEventListener("error", () => {
      setConnectionState("error");
    });

    return () => {
      socket.close();
      if (socketRef.current === socket) {
        socketRef.current = null;
      }
    };
  }, [wsUrl]);

  const handleServerMessage = useCallback((message: ServerMessage) => {
    switch (message.type) {
      case "output":
        setTerminalChunks((chunks) => [...chunks, message.data ?? ""]);
        break;
      case "error":
        setTerminalChunks((chunks) => [...chunks, `error: ${message.data ?? "unknown"}\r\n`]);
        break;
      case "exit":
        setTerminalChunks((chunks) => [...chunks, `process: ${message.data ?? "exited"}\r\n`]);
        setConnectionState("closed");
        break;
      case "pong":
        break;
    }
  }, []);

  const sendInput = useCallback((data: string) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setTerminalChunks((chunks) => [...chunks, "not connected\r\n"]);
      return;
    }

    socket.send(JSON.stringify({ type: "input", data }));
  }, []);

  const canSend = connectionState === "connected";

  return (
    <main className="min-h-dvh bg-zinc-950 text-zinc-100">
      <div className="mx-auto flex min-h-dvh w-full max-w-5xl flex-col px-3 py-3 sm:px-5 sm:py-5">
        <header className="flex items-center justify-between gap-3 pb-3">
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <div className="grid size-8 place-items-center rounded-md border border-emerald-400/30 bg-emerald-400/10 text-emerald-300">
                <Radio className="size-4" aria-hidden="true" />
              </div>
              <div className="min-w-0">
                <h1 className="truncate text-base font-semibold tracking-normal text-zinc-50">
                  VibeBridge
                </h1>
                <p className="truncate text-xs text-zinc-400">Local terminal relay</p>
              </div>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            <ConnectionStatus state={connectionState} />
            <Badge variant="outline" className="hidden border-zinc-700 bg-zinc-900 text-zinc-300 sm:inline-flex">
              <ShieldCheck className="mr-1 size-3" aria-hidden="true" />
              token
            </Badge>
          </div>
        </header>

        <section className="min-h-0 flex-1 overflow-hidden rounded-md border border-zinc-800 bg-black shadow-2xl shadow-black/30">
          <Suspense fallback={<div className="grid h-full min-h-[22rem] place-items-center text-sm text-zinc-500">Loading terminal...</div>}>
            <TerminalView chunks={terminalChunks} />
          </Suspense>
        </section>

        <section className="shrink-0 space-y-2 pt-3">
          <ShortcutBar disabled={!canSend} onInput={sendInput} />
          <PromptComposer disabled={!canSend} onSend={(value) => sendInput(`${value}${terminalKeys.enter}`)} />

          <div className="flex items-center justify-between gap-3 text-xs text-zinc-500">
            <span className="flex min-w-0 items-center gap-1 truncate">
              {canSend ? <SendHorizontal className="size-3" /> : <WifiOff className="size-3" />}
              {canSend ? "Ready to send natural language input" : "Waiting for WebSocket connection"}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 shrink-0 text-zinc-400 hover:text-red-300"
              onClick={() => socketRef.current?.close()}
            >
              <Power className="mr-1 size-3" aria-hidden="true" />
              End
            </Button>
          </div>
        </section>
      </div>
    </main>
  );
}
