import { useEffect, useState } from "react";
import { t, subscribeLang } from "../lib/i18n";
import { Badge } from "./ui/badge";

type Props = {
  state: "missing-token" | "connecting" | "reconnecting" | "connected" | "closed" | "error";
};

const keyMap: Record<Props["state"], string> = {
  "missing-token": "conn.noToken",
  connecting: "conn.connecting",
  reconnecting: "conn.reconnecting",
  connected: "conn.connected",
  closed: "conn.closed",
  error: "conn.error",
};

export function ConnectionStatus({ state }: Props) {
  // Re-render on language change
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const tone = state === "connected" ? "success" : state === "connecting" || state === "reconnecting" ? "muted" : "danger";

  return <Badge variant={tone}>{t(keyMap[state])}</Badge>;
}
