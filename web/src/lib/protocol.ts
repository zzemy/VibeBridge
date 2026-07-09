export type ClientMessage =
  | { type: "input"; data: string }
  | { type: "ping" }
  | { type: "resize"; cols: number; rows: number };

export type ServerMessage =
  | { type: "output"; data?: string }
  | { type: "error"; data?: string }
  | { type: "exit"; data?: string }
  | { type: "pong" };

export function isServerMessage(value: unknown): value is ServerMessage {
  if (!value || typeof value !== "object") {
    return false;
  }

  const candidate = value as Record<string, unknown>;
  if (candidate.type === "pong") {
    return true;
  }

  return (
    (candidate.type === "output" || candidate.type === "error" || candidate.type === "exit") &&
    (candidate.data === undefined || typeof candidate.data === "string")
  );
}
