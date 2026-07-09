import { Badge } from "./ui/badge";

type Props = {
  state: "missing-token" | "connecting" | "connected" | "closed" | "error";
};

const labels: Record<Props["state"], string> = {
  "missing-token": "No token",
  connecting: "Connecting",
  connected: "Connected",
  closed: "Closed",
  error: "Error",
};

export function ConnectionStatus({ state }: Props) {
  const tone = state === "connected" ? "success" : state === "connecting" ? "muted" : "danger";

  return <Badge variant={tone}>{labels[state]}</Badge>;
}
