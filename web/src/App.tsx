import { Activity, Clock3, Power, Radio, RefreshCw, SendHorizontal, ShieldCheck, WifiOff } from "lucide-react";
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { ConnectionStatus } from "./components/ConnectionStatus";
import { PromptComposer } from "./components/PromptComposer";
import { ShortcutBar } from "./components/ShortcutBar";
import { TerminalToolbar } from "./components/TerminalToolbar";
import type { TerminalViewHandle } from "./components/TerminalView";
import {
  AlertDialog,
  AlertDialogAction,
  AlertDialogCancel,
  AlertDialogContent,
  AlertDialogDescription,
  AlertDialogFooter,
  AlertDialogTitle,
} from "./components/ui/alert-dialog";
import { Badge } from "./components/ui/badge";
import { Button } from "./components/ui/button";
import { isServerMessage, isSessionStatus, type ServerMessage, type SessionStatus } from "./lib/protocol";
import {
  acceptAgentHello,
  createClientHello,
  newProtocolV1ConnectionId,
  protocolV1WebSocketSubprotocol,
  terminalBinaryOutputCapability,
} from "./lib/protocol-v1";
import { terminalKeys } from "./lib/terminalKeys";

type ConnectionState = "missing-token" | "connecting" | "reconnecting" | "connected" | "closed" | "error";
type TerminalChunk = string | Uint8Array;

const TerminalView = lazy(() => import("./components/TerminalView").then((module) => ({ default: module.TerminalView })));
const reconnectDelaySeconds = 3;
const minTerminalFontSize = 11;
const maxTerminalFontSize = 18;

function readTerminalFontSize() {
  try {
    const value = Number(localStorage.getItem("vibebridge:terminal-font-size"));
    return Number.isFinite(value) && value >= minTerminalFontSize && value <= maxTerminalFontSize ? value : 13;
  } catch {
    return 13;
  }
}

function formatElapsed(startedAt: string | undefined, now: number) {
  if (!startedAt) {
    return "Not started";
  }
  const elapsedSeconds = Math.max(0, Math.floor((now - new Date(startedAt).getTime()) / 1000));
  const hours = Math.floor(elapsedSeconds / 3600);
  const minutes = Math.floor((elapsedSeconds % 3600) / 60);
  if (hours > 0) {
    return `${hours}h ${minutes}m`;
  }
  return minutes > 0 ? `${minutes}m` : "<1m";
}

function formatAgo(timestamp: string, now: number) {
  const seconds = Math.max(0, Math.floor((now - new Date(timestamp).getTime()) / 1000));
  if (seconds < 60) return "now";
  if (seconds < 3600) return `${Math.floor(seconds / 60)}m ago`;
  return `${Math.floor(seconds / 3600)}h ago`;
}

export function App() {
  const [connectionState, setConnectionState] = useState<ConnectionState>("connecting");
  const [terminalChunks, setTerminalChunks] = useState<TerminalChunk[]>([]);
  const [endDialogOpen, setEndDialogOpen] = useState(false);
  const [retryIn, setRetryIn] = useState(0);
  const [retryTrigger, setRetryTrigger] = useState(0);
  const [sessionStatus, setSessionStatus] = useState<SessionStatus | null>(null);
  const [terminalFontSize, setTerminalFontSize] = useState(readTerminalFontSize);
  const [notice, setNotice] = useState("");
  const [now, setNow] = useState(Date.now());
  const socketRef = useRef<WebSocket | null>(null);
  const terminalRef = useRef<TerminalViewHandle | null>(null);
  const stopReconnectRef = useRef(false);
  const hasConnectedRef = useRef(false);
  const disconnectReportedRef = useRef(false);
  const noticeTimerRef = useRef<number | undefined>(undefined);

  const token = useMemo(() => new URLSearchParams(window.location.search).get("token") ?? "", []);

  const wsUrl = useMemo(() => {
    if (!token) {
      return "";
    }
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    return `${protocol}//${window.location.host}/ws?token=${encodeURIComponent(token)}`;
  }, [token]);

  const statusUrl = useMemo(() => token ? `/status?token=${encodeURIComponent(token)}` : "", [token]);

  const showNotice = useCallback((message: string) => {
    setNotice(message);
    if (noticeTimerRef.current !== undefined) {
      window.clearTimeout(noticeTimerRef.current);
    }
    noticeTimerRef.current = window.setTimeout(() => setNotice(""), 2_500);
  }, []);

  useEffect(() => () => {
    if (noticeTimerRef.current !== undefined) {
      window.clearTimeout(noticeTimerRef.current);
    }
  }, []);

  useEffect(() => {
    try {
      localStorage.setItem("vibebridge:terminal-font-size", String(terminalFontSize));
    } catch {
      // Font preference remains available for the current render.
    }
  }, [terminalFontSize]);

  useEffect(() => {
    const timer = window.setInterval(() => setNow(Date.now()), 30_000);
    return () => window.clearInterval(timer);
  }, []);

  useEffect(() => {
    if (!statusUrl) {
      return;
    }
    let disposed = false;

    async function loadStatus() {
      try {
        const response = await fetch(statusUrl, { cache: "no-store" });
        if (!response.ok) {
          return;
        }
        const value: unknown = await response.json();
        if (!disposed && isSessionStatus(value)) {
          setSessionStatus(value);
        }
      } catch {
        // WebSocket state remains the primary connection signal.
      }
    }

    void loadStatus();
    const timer = window.setInterval(loadStatus, 10_000);
    return () => {
      disposed = true;
      window.clearInterval(timer);
    };
  }, [statusUrl]);

  const handleServerMessage = useCallback((message: ServerMessage) => {
    switch (message.type) {
      case "error":
        if (message.data === "session already active") {
          stopReconnectRef.current = true;
          setConnectionState("error");
          setTerminalChunks((chunks) => [...chunks, "error: another browser is already controlling this session\r\n"]);
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
    let countdownTimer: number | undefined;

    const scheduleReconnect = () => {
      let remaining = reconnectDelaySeconds;
      setConnectionState("reconnecting");
      setRetryIn(remaining);
      countdownTimer = window.setInterval(() => {
        remaining -= 1;
        setRetryIn(Math.max(0, remaining));
        if (remaining <= 0) {
          if (countdownTimer !== undefined) {
            window.clearInterval(countdownTimer);
          }
          reconnectTimer = window.setTimeout(connect, 0);
        }
      }, 1_000);
    };

    const connect = () => {
      if (disposed || stopReconnectRef.current) {
        return;
      }

      setConnectionState(hasConnectedRef.current ? "reconnecting" : "connecting");
      const connectionId = newProtocolV1ConnectionId();
      const socket = new WebSocket(wsUrl, [protocolV1WebSocketSubprotocol]);
      let protocolNegotiated = false;
      let fatalProtocolError = false;
      socket.binaryType = "arraybuffer";
      socketRef.current = socket;

      const markConnected = () => {
        if (hasConnectedRef.current) {
          setTerminalChunks((chunks) => [...chunks, "connection restored\r\n"]);
          showNotice("Session restored");
        }
        hasConnectedRef.current = true;
        disconnectReportedRef.current = false;
        setRetryIn(0);
        setConnectionState("connected");
      };

      const failProtocol = (message: string) => {
        fatalProtocolError = true;
        stopReconnectRef.current = true;
        setConnectionState("error");
        setTerminalChunks((chunks) => [...chunks, `protocol negotiation failed: ${message}\r\n`]);
        socket.close(1002, "Protocol V1 negotiation failed");
      };

      socket.addEventListener("open", () => {
        if (socket.protocol !== protocolV1WebSocketSubprotocol) {
          protocolNegotiated = true;
          markConnected();
          return;
        }
        try {
          socket.send(createClientHello(connectionId).slice().buffer);
        } catch (error) {
          failProtocol(error instanceof Error ? error.message : "could not create client Hello");
        }
      });

      socket.addEventListener("message", (event: MessageEvent<string | ArrayBuffer>) => {
        const payload = event.data;
        if (!protocolNegotiated && socket.protocol === protocolV1WebSocketSubprotocol) {
          if (typeof payload === "string") {
            failProtocol("Agent Hello must be binary");
            return;
          }
          try {
            const negotiated = acceptAgentHello(new Uint8Array(payload), connectionId);
            if (!negotiated.capabilities.has(terminalBinaryOutputCapability)) {
              throw new Error(`Agent does not support ${terminalBinaryOutputCapability}`);
            }
            protocolNegotiated = true;
            markConnected();
          } catch (error) {
            failProtocol(error instanceof Error ? error.message : "invalid Agent Hello");
          }
          return;
        }

        if (typeof payload !== "string") {
          setTerminalChunks((chunks) => [...chunks, new Uint8Array(payload)]);
          return;
        }

        let parsed: unknown;
        try {
          parsed = JSON.parse(payload);
        } catch {
          setTerminalChunks((chunks) => [...chunks, payload]);
          return;
        }

        if (!isServerMessage(parsed)) {
          setTerminalChunks((chunks) => [...chunks, "received malformed server message\r\n"]);
          return;
        }
        handleServerMessage(parsed);
      });

      socket.addEventListener("close", (event) => {
        if (socketRef.current === socket) {
          socketRef.current = null;
        }
        if (fatalProtocolError || (!protocolNegotiated && socket.protocol === protocolV1WebSocketSubprotocol && event.code === 1002)) {
          stopReconnectRef.current = true;
          setConnectionState("error");
          if (!fatalProtocolError) {
            setTerminalChunks((chunks) => [...chunks, "protocol negotiation rejected by Agent\r\n"]);
          }
          return;
        }
        if (disposed || stopReconnectRef.current) {
          setConnectionState("closed");
          return;
        }
        if (!disconnectReportedRef.current) {
          disconnectReportedRef.current = true;
          setTerminalChunks((chunks) => [
            ...chunks,
            hasConnectedRef.current ? "connection lost; waiting to reconnect...\r\n" : "backend unavailable; waiting to connect...\r\n",
          ]);
        }
        scheduleReconnect();
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
      if (reconnectTimer !== undefined) window.clearTimeout(reconnectTimer);
      if (countdownTimer !== undefined) window.clearInterval(countdownTimer);
      socketRef.current?.close();
      socketRef.current = null;
    };
  }, [handleServerMessage, retryTrigger, showNotice, wsUrl]);

  const sendInput = useCallback((data: string) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      showNotice("Terminal is not connected");
      return;
    }
    socket.send(JSON.stringify({ type: "input", data }));
  }, [showNotice]);

  const sendResize = useCallback((cols: number, rows: number) => {
    const socket = socketRef.current;
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "resize", cols, rows }));
    }
  }, []);

  const retryConnection = useCallback(() => {
    stopReconnectRef.current = false;
    setRetryTrigger((value) => value + 1);
  }, []);

  const endSession = useCallback(() => {
    const socket = socketRef.current;
    stopReconnectRef.current = true;
    if (socket?.readyState === WebSocket.OPEN) {
      socket.send(JSON.stringify({ type: "exit" }));
      return;
    }
    socket?.close();
    setConnectionState("closed");
  }, []);

  const copySelection = useCallback(async () => {
    const copied = await terminalRef.current?.copySelection();
    showNotice(copied ? "Selection copied" : "Select terminal text first");
  }, [showNotice]);

  const searchTerminal = useCallback((query: string) => {
    const found = terminalRef.current?.findNext(query) ?? false;
    showNotice(found ? `Found "${query}"` : `No match for "${query}"`);
  }, [showNotice]);

  const canSend = connectionState === "connected";
  const canRetry = connectionState === "closed" || connectionState === "error";
  const elapsed = formatElapsed(sessionStatus?.started_at, now);
  const statusText = notice || (canSend ? "Terminal keyboard ready" : connectionState === "reconnecting" ? `Reconnecting in ${retryIn}s` : "Waiting for terminal connection");

  return (
    <main className="h-dvh overflow-hidden bg-zinc-950 text-zinc-100">
      <div className="mx-auto flex h-dvh w-full max-w-6xl flex-col px-3 py-3 sm:px-5 sm:py-5">
        <header className="flex items-center justify-between gap-3 pb-3">
          <div className="flex min-w-0 items-center gap-2">
            <div className="grid size-8 shrink-0 place-items-center rounded-md border border-emerald-400/30 bg-emerald-400/10 text-emerald-300">
              <Radio className="size-4" aria-hidden="true" />
            </div>
            <div className="min-w-0">
              <h1 className="truncate text-base font-semibold tracking-normal text-zinc-50">VibeBridge</h1>
              <p className="flex items-center gap-1 truncate text-xs text-zinc-400">
                <Activity className="size-3" aria-hidden="true" />
                {sessionStatus?.state ?? "local terminal relay"} · {elapsed}
              </p>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            <ConnectionStatus state={connectionState} />
            <Badge variant="outline" className="hidden border-zinc-700 bg-zinc-900 text-zinc-300 sm:inline-flex">
              <ShieldCheck className="mr-1 size-3" aria-hidden="true" />
              private LAN
            </Badge>
          </div>
        </header>

        <div className="workspace-layout min-h-0 flex-1">
        <section className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-md border border-zinc-800 bg-black shadow-2xl shadow-black/30">
          <TerminalToolbar
            canZoomIn={terminalFontSize < maxTerminalFontSize}
            canZoomOut={terminalFontSize > minTerminalFontSize}
            onClear={() => {
              terminalRef.current?.clear();
              showNotice("Terminal view cleared");
            }}
            onCopy={() => void copySelection()}
            onFocus={() => terminalRef.current?.focus()}
            onSearch={searchTerminal}
            onZoomIn={() => setTerminalFontSize((size) => Math.min(maxTerminalFontSize, size + 1))}
            onZoomOut={() => setTerminalFontSize((size) => Math.max(minTerminalFontSize, size - 1))}
          />
          <div className="min-h-0 flex-1">
            <Suspense fallback={<div className="grid h-full min-h-0 place-items-center text-sm text-zinc-500">Loading terminal...</div>}>
              <TerminalView ref={terminalRef} chunks={terminalChunks} fontSize={terminalFontSize} onInput={sendInput} onResize={sendResize} />
            </Suspense>
          </div>
        </section>

        <section className="workspace-controls shrink-0 space-y-2 pt-2 sm:pt-3">
          {connectionState === "reconnecting" ? (
            <div className="flex items-center justify-between rounded-md border border-amber-400/20 bg-amber-400/5 px-3 py-2 text-xs text-amber-200">
              <span>Connection interrupted. The PTY is being kept alive.</span>
              <span className="tabular-nums">retry {retryIn}s</span>
            </div>
          ) : null}

          <ShortcutBar disabled={!canSend} onInput={sendInput} />
          <PromptComposer
            disabled={!canSend}
            historyStorageKey={token ? `vibebridge:history:${token}` : "vibebridge:history"}
            storageKey={token ? `vibebridge:draft:${token}` : "vibebridge:draft"}
            onSubmit={(value, appendEnter) => {
              sendInput(`${value}${appendEnter ? terminalKeys.enter : ""}`);
              showNotice(appendEnter ? "Prompt sent" : "Prompt inserted");
            }}
          />

          <div className="flex items-center justify-between gap-3 text-xs text-zinc-500">
            <span className="flex min-w-0 items-center gap-1 truncate" role="status">
              {canSend ? <SendHorizontal className="size-3" /> : <WifiOff className="size-3" />}
              {statusText}
            </span>
            <div className="flex shrink-0 items-center gap-1">
              {sessionStatus?.last_activity_at ? (
                <span className="hidden items-center gap-1 px-2 text-zinc-600 sm:flex" title={new Date(sessionStatus.last_activity_at).toLocaleString()}>
                  <Clock3 className="size-3" aria-hidden="true" />
                  {formatAgo(sessionStatus.last_activity_at, now)}
                </span>
              ) : null}
              {canRetry ? (
                <Button type="button" variant="ghost" size="sm" className="h-8 text-zinc-400" onClick={retryConnection}>
                  <RefreshCw className="size-3" aria-hidden="true" />
                  Retry
                </Button>
              ) : null}
              <Button type="button" variant="ghost" size="sm" className="h-8 text-zinc-400 hover:text-red-300" onClick={() => setEndDialogOpen(true)}>
                <Power className="size-3" aria-hidden="true" />
                End
              </Button>
            </div>
          </div>
        </section>
        </div>
      </div>

      <AlertDialog open={endDialogOpen} onOpenChange={setEndDialogOpen}>
        <AlertDialogContent>
          <AlertDialogTitle className="text-base font-semibold text-zinc-50">End this terminal session?</AlertDialogTitle>
          <AlertDialogDescription className="mt-2 text-sm leading-6 text-zinc-400">
            The local AI CLI and its PTY will be stopped. This cannot be undone.
          </AlertDialogDescription>
          <AlertDialogFooter>
            <AlertDialogCancel>Keep session</AlertDialogCancel>
            <AlertDialogAction onClick={endSession}>End session</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}
