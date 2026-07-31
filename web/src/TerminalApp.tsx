import { Activity, Clock3, Power, Radio, RefreshCw, SendHorizontal, ShieldCheck, WifiOff } from "lucide-react";
import { Suspense, lazy, useCallback, useEffect, useMemo, useRef, useState } from "react";
import { AttachmentComposer } from "./components/AttachmentComposer";
import { AttachmentPromptDialog } from "./components/AttachmentPromptDialog";
import { ConnectionStatus } from "./components/ConnectionStatus";
import { PromptComposer } from "./components/PromptComposer";
import { ShortcutBar } from "./components/ShortcutBar";
import { TerminalToolbar } from "./components/TerminalToolbar";
import type { TerminalViewHandle } from "./components/TerminalView";
import { AttachmentPromptDisposition, ErrorCode, ProcessExitOutcome, ResumeDisposition } from "./gen/vibebridge/v1/envelope_pb";
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
import { t, subscribeLang } from "./lib/i18n";
import {
  acceptAgentHello,
  attachmentPromptActionCapability,
  attachmentTransferCapability,
  controlErrorCapability,
  controlHealthCapability,
  createClientHello,
  newProtocolV1ConnectionId,
  ProtocolV1ClientStream,
  protocolV1WebSocketSubprotocol,
  sessionProcessExitCapability,
  sessionResumeCapability,
  type SessionResumeCursor,
  terminalBinaryOutputCapability,
  terminalResizeEndCapability,
  terminalSequencedIoCapability,
} from "./lib/protocol-v1";
import {
  transferAttachments,
  type AttachmentTransferProgress,
} from "./lib/attachments";
import { AcknowledgedAttachmentSender, type AcknowledgedAttachmentSenderOptions } from "./lib/attachment-protocol";
import { AttachmentPromptActionClient } from "./lib/attachment-prompt-action";
import { terminalKeys } from "./lib/terminalKeys";
import { connectViaRelay, hexToBase64Url, provisionRelayRoute } from "./lib/relay-client";

type ConnectionState = "missing-token" | "connecting" | "reconnecting" | "connected" | "closed" | "error";
type TerminalChunk = string | Uint8Array;
type PreparedAttachmentPrompt = { preview: string; appendEnter: boolean };

const TerminalView = lazy(() => import("./components/TerminalView").then((module) => ({ default: module.TerminalView })));
const reconnectBaseDelaySeconds = 1;
const reconnectMaxDelaySeconds = 30;
const reconnectMaxAttempts = 10;
const minTerminalFontSize = 11;
// Keep the prepared client flow dark until the full prompt-action and adapter path is ready.
const attachmentClientFlowEnabled = true;
const maxTerminalFontSize = 18;

function stableErrorMessage(code: ErrorCode) {
  switch (code) {
    case ErrorCode.SESSION_START_FAILED:
      return "could not start terminal session";
    case ErrorCode.SESSION_ALREADY_ACTIVE:
      return "session already active";
    case ErrorCode.TERMINAL_INPUT_FAILED:
      return "could not write terminal input";
    case ErrorCode.TERMINAL_RESIZE_FAILED:
      return "could not resize terminal";
    case ErrorCode.UNSUPPORTED_MESSAGE:
      return "unsupported message type";
    case ErrorCode.ATTACHMENT_TRANSFER_FAILED:
      return "attachment transfer failed";
    case ErrorCode.ATTACHMENT_PROMPT_ACTION_FAILED:
      return "attachment prompt action failed";
    default:
      return "unknown";
  }
}

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
    return t("term.notStarted");
  }
  const elapsedSeconds = Math.max(0, Math.floor((now - new Date(startedAt).getTime()) / 1000));
  const hours = Math.floor(elapsedSeconds / 3600);
  const minutes = Math.floor((elapsedSeconds % 3600) / 60);
  if (hours > 0) {
    return t("time.elapsedHm", { h: hours, m: minutes });
  }
  return minutes > 0 ? t("time.elapsedM", { m: minutes }) : t("time.elapsedLess");
}

function equalBytes(left: Uint8Array, right: Uint8Array) {
  return left.byteLength === right.byteLength && left.every((value, index) => value === right[index]);
}

function formatAgo(timestamp: string, now: number) {
  const seconds = Math.max(0, Math.floor((now - new Date(timestamp).getTime()) / 1000));
  if (seconds < 60) return t("time.now");
  if (seconds < 3600) return t("time.mAgo", { m: Math.floor(seconds / 60) });
  return t("time.hAgo", { h: Math.floor(seconds / 3600) });
}

export function TerminalApp() {
  const [connectionState, setConnectionState] = useState<ConnectionState>("connecting");
  const [terminalChunks, setTerminalChunks] = useState<TerminalChunk[]>([]);
  const [endDialogOpen, setEndDialogOpen] = useState(false);
  const [retryIn, setRetryIn] = useState(0);
  const [retryTrigger, setRetryTrigger] = useState(0);
  const [sessionStatus, setSessionStatus] = useState<SessionStatus | null>(null);
  const [terminalFontSize, setTerminalFontSize] = useState(readTerminalFontSize);
  const [notice, setNotice] = useState("");
  const [attachmentTransferAvailable, setAttachmentTransferAvailable] = useState(false);
  const [stagedTransferIds, setStagedTransferIds] = useState<Uint8Array[]>([]);
  const [preparedAttachmentPrompt, setPreparedAttachmentPrompt] = useState<PreparedAttachmentPrompt | null>(null);
  const [attachmentComposerKey, setAttachmentComposerKey] = useState(0);
  const [now, setNow] = useState(Date.now());
  const socketRef = useRef<WebSocket | null>(null);
  const protocolStreamRef = useRef<ProtocolV1ClientStream | null>(null);
  const attachmentSenderRef = useRef<AcknowledgedAttachmentSender | null>(null);
  const [attachmentPromptClient] = useState(() => new AttachmentPromptActionClient());
  const attachmentPromptClientLifecycleRef = useRef(0);
  const resumeCursorRef = useRef<SessionResumeCursor | undefined>(undefined);
  const terminalRef = useRef<TerminalViewHandle | null>(null);
  const stopReconnectRef = useRef(false);
  const hasConnectedRef = useRef(false);
  const disconnectReportedRef = useRef(false);
  const noticeTimerRef = useRef<number | undefined>(undefined);
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const token = useMemo(() => new URLSearchParams(window.location.search).get("token") ?? "", []);
  const forcedTransport = useMemo<"direct" | "relay" | null>(() => {
    const params = new URLSearchParams(window.location.search);
    if (params.has("relay")) return "relay";
    if (params.has("direct")) return "direct";
    return null;
  }, []);
  const transportRef = useRef<"direct" | "relay">(forcedTransport ?? "direct");
  const relayAttemptedRef = useRef(false);

  const buildWsUrl = useCallback(async (): Promise<string> => {
    const protocol = window.location.protocol === "https:" ? "wss:" : "ws:";
    const base = `${protocol}//${window.location.host}/ws`;
    try {
      const resp = await fetch(`/pairing/web-session?token=${encodeURIComponent(token)}`);
      if (!resp.ok) throw new Error(`status ${resp.status}`);
      const data = await resp.json();
      const params = new URLSearchParams({
        "vb-device": data.device_id,
        "vb-nonce": data.nonce,
        "vb-sig": data.signature,
      });
      return `${base}?${params}`;
    } catch {
      return `${base}?token=${encodeURIComponent(token)}`;
    }
  }, [token]);

  const statusUrl = useMemo(() => token ? `/status?token=${encodeURIComponent(token)}` : "", [token]);

  const connectRelaySocket = useCallback(async (): Promise<WebSocket> => {
    const sessionResp = await fetch(`/pairing/web-session?token=${encodeURIComponent(token)}`);
    if (!sessionResp.ok) throw new Error(`web-session failed: ${sessionResp.status}`);
    const session = await sessionResp.json();
    const deviceB64Url = hexToBase64Url(session.device_id);
    const provision = await provisionRelayRoute(token, deviceB64Url);
    return connectViaRelay(provision.relay_url, provision.client_ticket, protocolV1WebSocketSubprotocol);
  }, [token]);

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
    const lifecycle = attachmentPromptClientLifecycleRef.current + 1;
    attachmentPromptClientLifecycleRef.current = lifecycle;
    return () => {
      // StrictMode replays Effects; defer closure so its immediate setup can advance the lifecycle.
      queueMicrotask(() => {
        if (attachmentPromptClientLifecycleRef.current === lifecycle) {
          attachmentPromptClient.close();
        }
      });
    };
  }, [attachmentPromptClient]);

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

  const resetAttachmentSessionState = useCallback(() => {
    setPreparedAttachmentPrompt(null);
    setStagedTransferIds([]);
    setAttachmentComposerKey((key) => key + 1);
  }, []);

  const handleProcessExit = useCallback((message: string) => {
    stopReconnectRef.current = true;
    setTerminalChunks((chunks) => [...chunks, `process: ${message}\r\n`]);
    setConnectionState("closed");
    resetAttachmentSessionState();
  }, [resetAttachmentSessionState]);

  const handleApplicationError = useCallback((message: string, sessionAlreadyActive: boolean) => {
    if (sessionAlreadyActive) {
      stopReconnectRef.current = true;
      setConnectionState("error");
      setTerminalChunks((chunks) => [...chunks, "error: another browser is already controlling this session\r\n"]);
      return;
    }
    setTerminalChunks((chunks) => [...chunks, `error: ${message}\r\n`]);
  }, []);

  const handleServerMessage = useCallback((message: ServerMessage) => {
    switch (message.type) {
      case "error":
        handleApplicationError(message.data ?? "unknown", message.data === "session already active");
        break;
      case "exit":
        handleProcessExit(message.data ?? "exited");
        break;
      case "pong":
        break;
    }
  }, [handleApplicationError, handleProcessExit]);

  useEffect(() => {
    if (!token) {
      setConnectionState("missing-token");
      setTerminalChunks(["missing session token\r\n"]);
      return;
    }

    let disposed = false;
    let reconnectTimer: number | undefined;
    let countdownTimer: number | undefined;
    let reconnectAttempts = 0;

    const scheduleReconnect = () => {
      if (reconnectAttempts >= reconnectMaxAttempts) {
        stopReconnectRef.current = true;
        setConnectionState("error");
        setTerminalChunks((chunks) => [...chunks, "max reconnect attempts reached; giving up\r\n"]);
        return;
      }
      // Exponential backoff with full jitter: delay is uniform random
      // in [base * 2^attempts, base * 2^(attempts+1)), capped at max.
      // Jitter prevents thundering herd when multiple clients lose
      // connectivity simultaneously (e.g. AP failover).
      const exp = reconnectBaseDelaySeconds * Math.pow(2, reconnectAttempts);
      const minDelay = Math.min(exp, reconnectMaxDelaySeconds);
      const maxDelay = Math.min(exp * 2, reconnectMaxDelaySeconds);
      const delaySeconds = Math.floor(minDelay + Math.random() * (maxDelay - minDelay));
      reconnectAttempts++;
      let remaining = delaySeconds;
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

    const connect = async () => {
      if (disposed || stopReconnectRef.current) {
        return;
      }

      setConnectionState(hasConnectedRef.current ? "reconnecting" : "connecting");
      const useRelay = transportRef.current === "relay";
      const url = useRelay ? null : await buildWsUrl();
      if (disposed || stopReconnectRef.current) {
        return;
      }
      const connectionId = newProtocolV1ConnectionId();
      let socket: WebSocket;
      try {
        socket = useRelay ? await connectRelaySocket() : new WebSocket(url!, [protocolV1WebSocketSubprotocol]);
      } catch (err) {
        if (!disposed && !stopReconnectRef.current) {
          if (useRelay) {
            relayAttemptedRef.current = true;
            transportRef.current = "direct";
          }
          scheduleReconnect();
        }
        return;
      }
      let protocolNegotiated = false;
      let fatalProtocolError = false;
      let attachmentTransfer = false;
      let attachmentPromptAction = false;
      let attachmentSender: AcknowledgedAttachmentSender | null = null;
      let attachmentSenderOptions: AcknowledgedAttachmentSenderOptions | null = null;
      let attachmentSenderNeedsReconnect = false;
      let protocolStream: ProtocolV1ClientStream | null = null;
      let promptTransportConnected = false;
      socket.binaryType = "arraybuffer";
      socketRef.current = socket;
      protocolStreamRef.current = null;
      setAttachmentTransferAvailable(false);

      const markConnected = () => {
        if (attachmentPromptAction && protocolStream && !promptTransportConnected) {
          attachmentPromptClient.connect(protocolStream, {
            send(encoded) {
              if (socket.readyState !== WebSocket.OPEN) {
                throw new Error("Connection lost during attachment prompt action");
              }
              socket.send(encoded.slice().buffer);
            },
            requestRecovery() {
              setAttachmentTransferAvailable(false);
              if (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING) {
                socket.close(1012, "Attachment prompt action recovery");
              }
            },
          });
          promptTransportConnected = true;
        }
        if (hasConnectedRef.current) {
          setTerminalChunks((chunks) => [...chunks, "connection restored\r\n"]);
          showNotice(t("term.sessionRestored"));
        }
        hasConnectedRef.current = true;
        disconnectReportedRef.current = false;
        reconnectAttempts = 0;
        setRetryIn(0);
        setConnectionState("connected");
        setAttachmentTransferAvailable(attachmentTransfer && attachmentPromptAction && attachmentSender !== null && promptTransportConnected);
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
          socket.send(createClientHello(connectionId, new Date(), {
            attachmentTransfer: attachmentClientFlowEnabled,
            attachmentPromptAction: attachmentClientFlowEnabled,
          }).slice().buffer);
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
            if (negotiated.capabilities.has(terminalSequencedIoCapability)) {
              const sessionResume = negotiated.capabilities.has(sessionResumeCapability);
              const sessionProcessExit = negotiated.capabilities.has(sessionProcessExitCapability);
              const terminalResizeEnd = negotiated.capabilities.has(terminalResizeEndCapability);
              const controlError = negotiated.capabilities.has(controlErrorCapability);
              const controlHealth = negotiated.capabilities.has(controlHealthCapability);
              const completeAttachmentFlow = negotiated.capabilities.has(attachmentTransferCapability)
                && negotiated.capabilities.has(attachmentPromptActionCapability);
              attachmentTransfer = attachmentClientFlowEnabled && completeAttachmentFlow;
              attachmentPromptAction = attachmentClientFlowEnabled && completeAttachmentFlow;
              const stream = new ProtocolV1ClientStream(connectionId, negotiated.maxEnvelopeBytes, {
                sessionProcessExit,
                sessionResume,
                terminalResizeEnd,
                controlError,
                controlHealth,
                attachmentTransfer,
                attachmentPromptAction,
              });
              protocolStream = stream;
              protocolStreamRef.current = stream;
              if (attachmentTransfer) {
                attachmentSenderOptions = {
                  send(encoded) {
                    if (socket.readyState !== WebSocket.OPEN) {
                      throw new Error("Connection lost during attachment transfer");
                    }
                    socket.send(encoded.slice().buffer);
                  },
                  requestRecovery() {
                    setAttachmentTransferAvailable(false);
                    if (socket.readyState === WebSocket.OPEN || socket.readyState === WebSocket.CONNECTING) {
                      socket.close(1012, "Attachment transfer recovery");
                    }
                  },
                };
                const existingSender = attachmentSenderRef.current;
                if (existingSender && sessionResume) {
                  attachmentSender = existingSender;
                  attachmentSenderNeedsReconnect = true;
                } else {
                  existingSender?.dispose();
                  attachmentSender = new AcknowledgedAttachmentSender(stream, attachmentSenderOptions);
                  attachmentSenderRef.current = attachmentSender;
                }
              } else if (attachmentSenderRef.current) {
                attachmentSenderRef.current.dispose();
                attachmentSenderRef.current = null;
              }
              protocolNegotiated = true;
              if (sessionResume) {
                socket.send(stream.createAttachSession(resumeCursorRef.current).slice().buffer);
              } else {
                markConnected();
              }
            } else {
              setAttachmentTransferAvailable(false);
              protocolNegotiated = true;
              markConnected();
            }
          } catch (error) {
            failProtocol(error instanceof Error ? error.message : "invalid Agent Hello");
          }
          return;
        }

        if (typeof payload !== "string") {
          const protocolStream = protocolStreamRef.current;
          if (!protocolStream) {
            setTerminalChunks((chunks) => [...chunks, new Uint8Array(payload)]);
            return;
          }
          try {
            const message = protocolStream.acceptAgentMessage(new Uint8Array(payload));
            const previousSession = resumeCursorRef.current;
            const sessionChanged = message.type === "session-status"
              && previousSession !== undefined
              && (message.sessionGeneration !== previousSession.sessionGeneration
                || !equalBytes(message.sessionId, previousSession.sessionId));
            if (message.type === "session-status" && attachmentSenderNeedsReconnect) {
              if (!attachmentSender || !attachmentSenderOptions) {
                throw new Error("Attachment sender reconnect state is incomplete");
              }
              if (sessionChanged) {
                attachmentSender.dispose();
                attachmentSender = new AcknowledgedAttachmentSender(protocolStream, attachmentSenderOptions);
                attachmentSenderRef.current = attachmentSender;
              } else {
                attachmentSender.reconnect(protocolStream, attachmentSenderOptions);
              }
              attachmentSenderNeedsReconnect = false;
            }
            attachmentSender?.acceptAgentMessage(message);
            attachmentPromptClient.acceptAgentMessage(message);
            const resumeCursor = protocolStream.getResumeCursor();
            if (sessionChanged) {
              attachmentPromptClient.resetForSessionChange();
              resetAttachmentSessionState();
            }
            if (resumeCursor) {
              resumeCursorRef.current = resumeCursor;
            }
            if (message.type === "session-status") {
              if (message.disposition === ResumeDisposition.RESYNC_REQUIRED) {
                terminalRef.current?.reset();
                setTerminalChunks(["terminal history was truncated; synchronized with the current session\r\n"]);
                showNotice(t("term.historyTruncated"));
              }
              markConnected();
            } else if (message.type === "terminal-output") {
              setTerminalChunks((chunks) => [...chunks, message.data]);
              socket.send(protocolStream.createAcknowledgement().slice().buffer);
            } else if (message.type === "process-exit") {
              handleProcessExit(message.outcome === ProcessExitOutcome.SUCCESS ? "exited" : "failed");
            } else if (message.type === "error") {
              handleApplicationError(
                stableErrorMessage(message.code),
                message.code === ErrorCode.SESSION_ALREADY_ACTIVE,
              );
            }
          } catch (error) {
            failProtocol(error instanceof Error ? error.message : "invalid Protocol V1 stream message");
          }
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
        if (parsed.type === "exit" && protocolStreamRef.current?.usesSessionProcessExit()) {
          failProtocol("Negotiated process exit must use a Protocol V1 envelope");
          return;
        }
        if (parsed.type === "error" && protocolStreamRef.current?.usesControlError()) {
          failProtocol("Negotiated errors must use a Protocol V1 envelope");
          return;
        }
        if (parsed.type === "pong" && protocolStreamRef.current?.usesControlHealth()) {
          failProtocol("Negotiated health checks must use Protocol V1 envelopes");
          return;
        }
        handleServerMessage(parsed);
      });

      socket.addEventListener("close", (event) => {
        attachmentSender?.disconnect();
        if (protocolStream) {
          attachmentPromptClient.disconnect(protocolStream);
        }
        promptTransportConnected = false;
        if (socketRef.current === socket) {
          socketRef.current = null;
          protocolStreamRef.current = null;
          setAttachmentTransferAvailable(false);
        }
        if (fatalProtocolError || (!protocolNegotiated && socket.protocol === protocolV1WebSocketSubprotocol && event.code === 1002)) {
          stopReconnectRef.current = true;
          attachmentSender?.dispose();
          if (attachmentSenderRef.current === attachmentSender) attachmentSenderRef.current = null;
          setConnectionState("error");
          if (!fatalProtocolError) {
            setTerminalChunks((chunks) => [...chunks, "protocol negotiation rejected by Agent\r\n"]);
          }
          return;
        }
        if (disposed) {
          setConnectionState("closed");
          return;
        }
        if (stopReconnectRef.current) {
          attachmentSender?.dispose();
          if (attachmentSenderRef.current === attachmentSender) attachmentSenderRef.current = null;
          setConnectionState((current) => current === "error" ? current : "closed");
          return;
        }
        if (!protocolNegotiated && !hasConnectedRef.current && forcedTransport === null) {
          if (transportRef.current === "direct" && !relayAttemptedRef.current) {
            transportRef.current = "relay";
            relayAttemptedRef.current = true;
            showNotice(t("term.tryingRelay"));
            reconnectTimer = window.setTimeout(connect, 0);
            return;
          }
          if (transportRef.current === "relay") {
            transportRef.current = "direct";
          }
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
      attachmentSenderRef.current?.dispose();
      attachmentSenderRef.current = null;
      const currentProtocolStream = protocolStreamRef.current;
      if (currentProtocolStream) {
        attachmentPromptClient.disconnect(currentProtocolStream);
      }
      socketRef.current?.close();
      socketRef.current = null;
      protocolStreamRef.current = null;
      setAttachmentTransferAvailable(false);
    };
  }, [attachmentPromptClient, buildWsUrl, handleApplicationError, handleProcessExit, handleServerMessage, resetAttachmentSessionState, retryTrigger, showNotice, token]);

  const sendAttachments = useCallback(async (
    files: readonly File[],
    signal: AbortSignal,
    onProgress: (progress: AttachmentTransferProgress) => void,
  ) => {
    const socket = socketRef.current;
    const protocolStream = protocolStreamRef.current;
    const sender = attachmentSenderRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN || !protocolStream?.usesAttachmentTransfer() || !sender) {
      throw new Error("Attachment transfer is not available");
    }

    const completedTransferIds = await transferAttachments(files, sender, signal, onProgress, protocolStream.maxAttachmentChunkBytes());
    setStagedTransferIds(completedTransferIds.map((transferId) => transferId.slice()));
    return completedTransferIds;
  }, []);

  const sendInput = useCallback((data: string) => {
    const socket = socketRef.current;
    if (!socket || socket.readyState !== WebSocket.OPEN) {
      showNotice(t("term.terminalNotConnected"));
      return;
    }
    try {
      const protocolStream = protocolStreamRef.current;
      const payload = protocolStream ? protocolStream.createTerminalInput(data).slice().buffer : JSON.stringify({ type: "input", data });
      socket.send(payload);
    } catch (error) {
      showNotice(error instanceof Error ? error.message : t("term.invalidInput"));
    }
  }, [showNotice]);

  const submitPrompt = useCallback(async (value: string, appendEnter: boolean) => {
    if (stagedTransferIds.length === 0) {
      sendInput(`${value}${appendEnter ? terminalKeys.enter : ""}`);
      showNotice(appendEnter ? t("term.promptSent") : t("term.promptInserted"));
      return;
    }

    const result = await attachmentPromptClient.prepare({
      transferIds: stagedTransferIds.map((transferId) => transferId.slice()),
      prompt: value,
      appendEnter,
    });
    if (result.disposition === AttachmentPromptDisposition.COMMITTED) {
      setStagedTransferIds([]);
      setAttachmentComposerKey((key) => key + 1);
      showNotice(t("term.promptCommitted"));
      return;
    }
    setPreparedAttachmentPrompt({ preview: result.preview, appendEnter: result.appendEnter });
  }, [attachmentPromptClient, sendInput, showNotice, stagedTransferIds]);

  const sendResize = useCallback((cols: number, rows: number) => {
    const socket = socketRef.current;
    if (socket?.readyState !== WebSocket.OPEN) {
      return;
    }
    try {
      const protocolStream = protocolStreamRef.current;
      const payload = protocolStream?.usesTerminalResizeEnd()
        ? protocolStream.createTerminalResize(cols, rows).slice().buffer
        : JSON.stringify({ type: "resize", cols, rows });
      socket.send(payload);
    } catch (error) {
      showNotice(error instanceof Error ? error.message : t("term.invalidDimensions"));
    }
  }, [showNotice]);

  const retryConnection = useCallback(() => {
    stopReconnectRef.current = false;
    setRetryTrigger((value) => value + 1);
  }, []);

  const endSession = useCallback(() => {
    const socket = socketRef.current;
    stopReconnectRef.current = true;
    if (socket?.readyState === WebSocket.OPEN) {
      try {
        const protocolStream = protocolStreamRef.current;
        const payload = protocolStream?.usesTerminalResizeEnd()
          ? protocolStream.createEndSession().slice().buffer
          : JSON.stringify({ type: "exit" });
        socket.send(payload);
      } catch (error) {
        stopReconnectRef.current = false;
        showNotice(error instanceof Error ? error.message : t("term.couldNotEndSession"));
      }
      return;
    }
    socket?.close();
    setConnectionState("closed");
  }, [showNotice]);

  const copySelection = useCallback(async () => {
    const copied = await terminalRef.current?.copySelection();
    showNotice(copied ? t("term.selectionCopied") : t("term.selectTextFirst"));
  }, [showNotice]);

  const searchTerminal = useCallback((query: string) => {
    const found = terminalRef.current?.findNext(query) ?? false;
    showNotice(found ? t("term.found", { q: query }) : t("term.noMatch", { q: query }));
  }, [showNotice]);

  const canSend = connectionState === "connected";
  const canRetry = connectionState === "closed" || connectionState === "error";
  const elapsed = formatElapsed(sessionStatus?.started_at, now);
  const statusText = notice || (canSend ? t("term.keyboardReady") : connectionState === "reconnecting" ? t("term.reconnectingIn", { s: retryIn }) : t("term.waitingConnection"));

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
                {sessionStatus?.state ?? t("term.localRelay")} · {elapsed}
              </p>
            </div>
          </div>

          <div className="flex shrink-0 items-center gap-2">
            <ConnectionStatus state={connectionState} />
            <Badge variant="outline" className="hidden border-zinc-700 bg-zinc-900 text-zinc-300 sm:inline-flex">
              <ShieldCheck className="mr-1 size-3" aria-hidden="true" />
              {t("term.privateLAN")}
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
              showNotice(t("term.terminalCleared"));
            }}
            onCopy={() => void copySelection()}
            onFocus={() => terminalRef.current?.focus()}
            onSearch={searchTerminal}
            onZoomIn={() => setTerminalFontSize((size) => Math.min(maxTerminalFontSize, size + 1))}
            onZoomOut={() => setTerminalFontSize((size) => Math.max(minTerminalFontSize, size - 1))}
          />
          <div className="min-h-0 flex-1">
            <Suspense fallback={<div className="grid h-full min-h-0 place-items-center text-sm text-zinc-500">{t("term.loadingTerminal")}</div>}>
              <TerminalView ref={terminalRef} chunks={terminalChunks} fontSize={terminalFontSize} onInput={sendInput} onResize={sendResize} />
            </Suspense>
          </div>
        </section>

        <section className="workspace-controls shrink-0 space-y-2 pt-2 sm:pt-3">
          {connectionState === "reconnecting" ? (
            <div className="flex items-center justify-between rounded-md border border-amber-400/20 bg-amber-400/5 px-3 py-2 text-xs text-amber-200">
              <span>{t("term.connectionInterrupted")}</span>
              <span className="tabular-nums">{t("term.retryLabel", { s: retryIn })}</span>
            </div>
          ) : null}

          <ShortcutBar disabled={!canSend} onInput={sendInput} />
          {attachmentTransferAvailable ? (
            <AttachmentComposer
              key={attachmentComposerKey}
              disabled={!canSend}
              transferEnabled={canSend}
              onTransfer={sendAttachments}
            />
          ) : null}
          <PromptComposer
            disabled={!canSend}
            historyStorageKey={token ? `vibebridge:history:${token}` : "vibebridge:history"}
            storageKey={token ? `vibebridge:draft:${token}` : "vibebridge:draft"}
            onSubmit={submitPrompt}
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
                  {t("term.retryBtn")}
                </Button>
              ) : null}
              <Button type="button" variant="ghost" size="sm" className="h-8 text-zinc-400 hover:text-red-300" onClick={() => setEndDialogOpen(true)}>
                <Power className="size-3" aria-hidden="true" />
                {t("term.endBtn")}
              </Button>
            </div>
          </div>
        </section>
        </div>
      </div>

      <AttachmentPromptDialog
        open={preparedAttachmentPrompt !== null}
        preview={preparedAttachmentPrompt?.preview ?? ""}
        appendEnter={preparedAttachmentPrompt?.appendEnter ?? false}
        onConfirm={() => attachmentPromptClient.commit()}
        onCancel={() => attachmentPromptClient.cancel()}
        onComplete={(result) => {
          const appendEnter = preparedAttachmentPrompt?.appendEnter ?? false;
          setPreparedAttachmentPrompt(null);
          if (result === "committed") {
            setStagedTransferIds([]);
            setAttachmentComposerKey((key) => key + 1);
            showNotice(appendEnter ? t("term.promptSent") : t("term.promptInserted"));
          } else if (result === "cancelled") {
            showNotice(t("term.promptCancelled"));
          } else {
            showNotice(t("term.promptActionFailed"));
          }
        }}
      />

      <AlertDialog open={endDialogOpen} onOpenChange={setEndDialogOpen}>
        <AlertDialogContent>
          <AlertDialogTitle className="text-base font-semibold text-zinc-50">{t("term.endSessionTitle")}</AlertDialogTitle>
          <AlertDialogDescription className="mt-2 text-sm leading-6 text-zinc-400">
            {t("term.endSessionDesc")}
          </AlertDialogDescription>
          <AlertDialogFooter>
            <AlertDialogCancel>{t("term.keepSession")}</AlertDialogCancel>
            <AlertDialogAction onClick={endSession}>{t("term.endSession")}</AlertDialogAction>
          </AlertDialogFooter>
        </AlertDialogContent>
      </AlertDialog>
    </main>
  );
}
