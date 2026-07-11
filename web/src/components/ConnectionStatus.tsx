import { Badge } from "./ui/badge";

type Props = {
  state: "missing-token" | "connecting" | "reconnecting" | "connected" | "closed" | "error";
};

const labels: Record<Props["state"], string> = {
  "missing-token": "No token",
  connecting: "Connecting",
  reconnecting: "Reconnecting",
  connected: "Connected",
  closed: "Closed",
  error: "Error",
};

export function ConnectionStatus({ state }: Props) {
  const tone = state === "connected" ? "success" : state === "connecting" || state === "reconnecting" ? "muted" : "danger";

  return <Badge variant={tone}>{labels[state]}</Badge>;
}
