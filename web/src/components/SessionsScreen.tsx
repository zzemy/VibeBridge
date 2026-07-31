import { Activity, Clock3, Power, Radio, RefreshCw, ShieldCheck, WifiOff } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "./ui/button";
import { ConnectionStatus } from "./ConnectionStatus";
import { t, subscribeLang } from "../lib/i18n";
import type { SessionStatus } from "../lib/protocol";

type ConnectionState = "missing-token" | "connecting" | "reconnecting" | "connected" | "closed" | "error";

type Props = {
  connectionState: ConnectionState;
  sessionStatus: SessionStatus | null;
  elapsed: string;
  lastActivityAgo: string | null;
  retryIn: number;
  transport: "direct" | "relay";
  onRetry: () => void;
  onEnd: () => void;
};

export function SessionsScreen({
  connectionState,
  sessionStatus,
  elapsed,
  lastActivityAgo,
  retryIn,
  transport,
  onRetry,
  onEnd,
}: Props) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const isConnected = connectionState === "connected";
  const canRetry = connectionState === "closed" || connectionState === "error";
  const hasSession = Boolean(sessionStatus);

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-5">
      {/* Page title */}
      <h1 className="mb-4 text-xl font-semibold text-zinc-50">{t("session.title")}</h1>

      {/* Session card */}
      <section className="overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900/50">
        {/* Card header */}
        <div className="flex items-center justify-between border-b border-zinc-800/60 px-4 py-3">
          <div className="flex items-center gap-2.5">
            <div
              className={`grid size-9 place-items-center rounded-lg ${
                isConnected
                  ? "bg-emerald-400/10 text-emerald-400"
                  : "bg-zinc-800 text-zinc-500"
              }`}
            >
              <Radio className="size-4.5" aria-hidden="true" />
            </div>
            <div>
              <p className="text-sm font-medium text-zinc-100">
                {sessionStatus?.state ?? t("term.localRelay")}
              </p>
              <p className="text-xs text-zinc-500">{t("session.title")}</p>
            </div>
          </div>
          <ConnectionStatus state={connectionState} />
        </div>

        {/* Card body — details grid */}
        <dl className="divide-y divide-zinc-800/50">
          <DetailRow icon={Activity} label={t("session.state")} value={sessionStatus?.state ?? "—"} />
          <DetailRow icon={Clock3} label={t("session.elapsed")} value={elapsed} />
          {lastActivityAgo ? (
            <DetailRow icon={Clock3} label={t("session.lastActivity")} value={lastActivityAgo} />
          ) : null}
          <DetailRow
            icon={ShieldCheck}
            label={t("session.transport")}
            value={transport === "relay" ? t("session.transportRelay") : t("session.transportDirect")}
          />
        </dl>
      </section>

      {/* Reconnecting notice */}
      {connectionState === "reconnecting" ? (
        <div className="mt-3 flex items-center justify-between rounded-lg border border-amber-400/20 bg-amber-400/5 px-4 py-2.5 text-xs text-amber-200">
          <span>{t("term.connectionInterrupted")}</span>
          <span className="tabular-nums">{t("term.retryLabel", { s: retryIn })}</span>
        </div>
      ) : null}

      {/* Action buttons */}
      <div className="mt-4 flex gap-2.5">
        {canRetry ? (
          <Button type="button" variant="secondary" className="flex-1" onClick={onRetry}>
            <RefreshCw className="size-4" aria-hidden="true" />
            {t("session.reconnect")}
          </Button>
        ) : null}
        <Button
          type="button"
          variant="ghost"
          className={`${
            canRetry ? "" : "flex-1"
          } border border-zinc-800 text-zinc-300 hover:bg-red-500/10 hover:text-red-300`}
          onClick={onEnd}
        >
          <Power className="size-4" aria-hidden="true" />
          {t("session.endSession")}
        </Button>
      </div>

      {/* Empty state */}
      {!hasSession && connectionState === "missing-token" ? (
        <div className="mt-6 flex flex-col items-center gap-3 rounded-xl border border-dashed border-zinc-800 px-6 py-10 text-center">
          <div className="grid size-12 place-items-center rounded-full bg-zinc-800/50 text-zinc-600">
            <WifiOff className="size-6" aria-hidden="true" />
          </div>
          <div>
            <p className="text-sm font-medium text-zinc-300">{t("session.noSession")}</p>
            <p className="mt-1 text-xs text-zinc-500">{t("session.noSessionDesc")}</p>
          </div>
        </div>
      ) : null}
    </div>
  );
}

function DetailRow({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Activity;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center justify-between px-4 py-3">
      <dt className="flex items-center gap-2 text-xs text-zinc-500">
        <Icon className="size-3.5" aria-hidden="true" />
        {label}
      </dt>
      <dd className="text-sm font-medium text-zinc-200">{value}</dd>
    </div>
  );
}
