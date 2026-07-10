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
  const stopReconnectRef = useRef(false);

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

  const handleServerMessage = useCallback((message: ServerMessage) => {
    switch (message.type) {
      case "output":
        setTerminalChunks((chunks) => [...chunks, message.data ?? ""]);
        break;
      case "error":
        if (message.data === "session already active") {
          stopReconnectRef.current = true;
          setConnectionState("error");
          setTerminalChunks((chunks) => [
            ...chunks,
            "error: another browser is already controlling this session\r\n",
          ]);
          break;
        }
        setTerminalChunks((chunks) => [...chunks, `error: ${message.data ?? "unknown"}\r\n`]);
        break;
      case "exit":
        stopReconnectRef.current = true;
        setTerminalChunks((chunks) => [...chunks, `process: ${message.data ?? "exited"}\r\n`]);
        setConnectionState("closed");
        break;
      case "pong":
        break;
    }
  }, []);

  useEffect(() => {
    if (!wsUrl) {
      setConnectionState("missing-token");
      setTerminalChunks(["missing session token\r\n"]);
      return;
    }

    let disposed = false;
    let reconnectTimer: number | undefined;

    const connect = () => {
      if (disposed || stopReconnectRef.current) {
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
        if (socketRef.current === socket) {
          socketRef.current = null;
        }

        if (disposed || stopReconnectRef.current) {
          setConnectionState("closed");
          return;
        }

        setConnectionState("connecting");
        setTerminalChunks((chunks) => [...chunks, "connection lost; reconnecting...\r\n"]);
        reconnectTimer = window.setTimeout(connect, 1000);
      });

      socket.addEventListener("error", () => {
        if (!stopReconnectRef.current) {
          setConnectionState("error");
        }
      });
    };

    stopReconnectRef.current = false;
    connect();

    return () => {
      disposed = true;
      if (reconnectTimer !== undefined) {
        window.clearTimeout(reconnectTimer);
      }
      socketRef.current?.close();
      socketRef.current = null;
    };
  }, [handleServerMessage, wsUrl]);

  const sendInput = useCallback((data: string) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      setTerminalChunks((chunks) => [...chunks, "not connected\r\n"]);
      return;
    }

    socket.send(JSON.stringify({ type: "input", data }));
  }, []);

  const sendResize = useCallback((cols: number, rows: number) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      return;
    }

    socket.send(JSON.stringify({ type: "resize", cols, rows }));
  }, []);

  const endSession = useCallback(() => {
    const socket = socketRef.current;
    stopReconnectRef.current = true;
    if (!socket) {
      setConnectionState("closed");
      return;
    }

    if (socket.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "exit" }));
      return;
    }

    socket.close();
    setConnectionState("closed");
  }, []);

  const canSend = connectionState === "connected";

  return (
    <main className="h-dvh overflow-hidden bg-zinc-950 text-zinc-100">
      <div className="mx-auto flex h-dvh w-full max-w-5xl flex-col px-3 py-3 sm:px-5 sm:py-5">
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
          <Suspense fallback={<div className="grid h-full min-h-0 place-items-center text-sm text-zinc-500">Loading terminal...</div>}>
            <TerminalView chunks={terminalChunks} onResize={sendResize} />
          </Suspense>
        </section>

        <section className="shrink-0 space-y-2 pt-3">
          <ShortcutBar disabled={!canSend} onInput={sendInput} />
          <PromptComposer disabled={!canSend} onSend={(value) => sendInput(`${value}${terminalKeys.enter}`)} />

          <div className="flex items-center justify-between gap-3 text-xs text-zinc-500">
            <span className="flex min-w-0 items-center gap-1 truncate">
              {canSend ? <SendHorizontal className="size-3" /> : <WifiOff className="size-3" />}
              {canSend ? "Ready to send input to the local PTY" : "Waiting for WebSocket connection"}
            </span>
            <Button
              type="button"
              variant="ghost"
              size="sm"
              className="h-8 shrink-0 text-zinc-400 hover:text-red-300"
              onClick={endSession}
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
