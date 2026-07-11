export type ClientMessage =
  | { type: "input"; data: string }
  | { type: "exit" }
  | { type: "ping" }
  | { type: "resize"; cols: number; rows: number };

export type ServerMessage =
  | { type: "error"; data?: string }
  | { type: "exit"; data?: string }
  | { type: "pong" };

export type SessionStatus = {
  state: "idle" | "connected" | "detached" | "ended";
  started_at?: string;
  last_activity_at?: string;
  reconnect_timeout_seconds: number;
  idle_timeout_seconds: number;
};

export function isServerMessage(value: unknown): value is ServerMessage {
  if (!value || typeof value !== "object") {
    return false;
  }

  const candidate = value as Record<string, unknown>;
  if (candidate.type === "pong") {
    return true;
  }

  return (
    (candidate.type === "error" || candidate.type === "exit") &&
    (candidate.data === undefined || typeof candidate.data === "string")
  );
}

export function isSessionStatus(value: unknown): value is SessionStatus {
  if (!value || typeof value !== "object") {
    return false;
  }
  const candidate = value as Record<string, unknown>;
  return (
    (candidate.state === "idle" || candidate.state === "connected" || candidate.state === "detached" || candidate.state === "ended") &&
    typeof candidate.reconnect_timeout_seconds === "number" &&
    typeof candidate.idle_timeout_seconds === "number" &&
    (candidate.started_at === undefined || typeof candidate.started_at === "string") &&
    (candidate.last_activity_at === undefined || typeof candidate.last_activity_at === "string")
  );
}
