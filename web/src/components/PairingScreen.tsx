import { CircleCheck, CircleX, KeyRound, Laptop, LoaderCircle, RefreshCw, ShieldCheck, Smartphone } from "lucide-react";
import { useEffect, useRef, useState } from "react";

import { BrowserDeviceStore } from "../lib/device-identity-store";
import { pairPhone, type PairingClientStatus } from "../lib/pairing-client";
import type { PairingEntry } from "../lib/pairing-invitation";
import { Button } from "./ui/button";

type PairingPhase =
  | { state: "invalid"; message: string }
  | { state: "connecting" }
  | { state: "handshaking" }
  | { state: "pending"; sas: string }
  | { state: "approved"; sas: string }
  | { state: "rejected"; sas: string }
  | { state: "error"; message: string; sas?: string };

type PairingExecution = {
  attempt: number;
  controller: AbortController;
};

export function PairingScreen({ entry }: { entry: PairingEntry }) {
  const [attempt, setAttempt] = useState(0);
  const [store] = useState(() => new BrowserDeviceStore());
  const [phase, setPhase] = useState<PairingPhase>(() => entry.kind === "invalid"
    ? { state: "invalid", message: entry.message }
    : { state: "connecting" });
  const executionRef = useRef<PairingExecution | null>(null);
  const lifecycleRef = useRef(0);
  const lastSasRef = useRef<string | undefined>(undefined);

  useEffect(() => {
    if (entry.kind === "invalid") return;
    const lifecycle = lifecycleRef.current + 1;
    lifecycleRef.current = lifecycle;
    if (executionRef.current?.attempt !== attempt) {
      const controller = new AbortController();
      executionRef.current = { attempt, controller };
      lastSasRef.current = undefined;
      setPhase({ state: "connecting" });
      void (async () => {
        try {
          const identity = await store.getOrCreateIdentity();
          const outcome = await pairPhone({
            invitation: entry.route.invitation,
            websocketUrl: entry.route.websocketUrl,
            identity,
            trustStore: store,
            signal: controller.signal,
            onStatus: (status) => updateStatus(status, setPhase, lastSasRef),
          });
          entry.route.invitation.bootstrapSecret.fill(0);
          if (outcome.state === "approved") setPhase({ state: "approved", sas: outcome.sas });
          else setPhase({ state: "rejected", sas: outcome.sas });
        } catch (error) {
          if (controller.signal.aborted) return;
          setPhase({
            state: "error",
            message: error instanceof Error ? error.message : "Pairing failed",
            sas: lastSasRef.current,
          });
        }
      })();
    }

    return () => {
      // React StrictMode replays Effects in development. Defer destructive
      // cleanup so the immediate replay can retain the one active flow.
      queueMicrotask(() => {
        if (lifecycleRef.current !== lifecycle) return;
        executionRef.current?.controller.abort();
        executionRef.current = null;
        entry.route.invitation.bootstrapSecret.fill(0);
        void store.close();
      });
    };
  }, [attempt, entry, store]);

  if (entry.kind === "invalid") {
    return (
      <PairingShell>
        <StatusIcon tone="error" />
        <p className="text-xs font-semibold uppercase tracking-[0.2em] text-red-300">Invalid pairing link</p>
        <h1 className="mt-3 text-2xl font-semibold tracking-tight text-zinc-50">This code cannot be used</h1>
        <p className="mt-3 text-sm leading-6 text-zinc-400">{phase.state === "invalid" ? phase.message : entry.message}</p>
        <p className="mt-5 text-xs leading-5 text-zinc-500">Open VibeBridge on the computer and create a new single-use QR code.</p>
      </PairingShell>
    );
  }

  const agentName = entry.route.invitation.agent?.deviceDescriptor?.displayName ?? "Home computer";
  const waiting = phase.state === "connecting" || phase.state === "handshaking";
  const sas = "sas" in phase ? phase.sas : undefined;

  return (
    <PairingShell>
      <div className="flex items-center justify-between gap-4">
        <div className="flex items-center gap-2 text-xs font-semibold uppercase tracking-[0.18em] text-emerald-300">
          <ShieldCheck className="size-4" aria-hidden="true" />
          Secure device pairing
        </div>
        <span className="rounded-full border border-zinc-800 bg-zinc-900 px-2.5 py-1 text-[11px] text-zinc-400">E2EE</span>
      </div>

      <div className="mt-8 flex items-center justify-center gap-3" aria-hidden="true">
        <div className="grid size-12 place-items-center rounded-2xl border border-zinc-800 bg-zinc-900"><Smartphone className="size-5 text-zinc-200" /></div>
        <div className="h-px w-16 bg-gradient-to-r from-zinc-700 via-emerald-400 to-zinc-700" />
        <div className="grid size-12 place-items-center rounded-2xl border border-zinc-800 bg-zinc-900"><Laptop className="size-5 text-zinc-200" /></div>
      </div>

      <div className="mt-6 text-center">
        <StatusIcon tone={phase.state === "approved" ? "success" : phase.state === "rejected" || phase.state === "error" ? "error" : "working"} />
        <h1 className="mt-4 text-2xl font-semibold tracking-tight text-zinc-50">{phaseTitle(phase)}</h1>
        <p className="mx-auto mt-2 max-w-sm text-sm leading-6 text-zinc-400">{phaseDescription(phase, agentName)}</p>
      </div>

      <div className="mt-7 space-y-3 rounded-xl border border-zinc-800 bg-zinc-950/70 p-4">
        <VerificationRow label="QR verification code" value={entry.route.invitation.verificationCode} />
        <VerificationRow label="Encrypted handshake code" value={sas ?? (waiting ? "Computing…" : "—")} emphasis={phase.state === "pending"} />
      </div>

      {phase.state === "pending" ? (
        <div className="mt-4 rounded-xl border border-amber-400/20 bg-amber-400/5 p-4 text-sm leading-6 text-amber-100">
          Confirm that <strong>{phase.sas}</strong> also appears in the computer's tray menu or local management page, then choose <strong>Allow phone</strong> there.
        </div>
      ) : null}

      {phase.state === "error" ? (
        <div className="mt-5 flex flex-col items-center gap-3">
          <p role="alert" className="text-center text-sm text-red-300">{phase.message}</p>
          <Button type="button" variant="secondary" onClick={() => {
            executionRef.current = null;
            setAttempt((value) => value + 1);
          }}>
            <RefreshCw className="size-4" aria-hidden="true" />
            Try again
          </Button>
        </div>
      ) : null}

      <div className="mt-7 flex items-start gap-2 border-t border-zinc-800 pt-5 text-xs leading-5 text-zinc-500">
        <KeyRound className="mt-0.5 size-3.5 shrink-0" aria-hidden="true" />
        The QR secret was removed from the address bar and is never saved. Trust is stored only after encrypted approval on the computer.
      </div>
    </PairingShell>
  );
}

function updateStatus(
  status: PairingClientStatus,
  setPhase: (phase: PairingPhase) => void,
  lastSas: { current: string | undefined },
) {
  if (status.state === "pending") {
    lastSas.current = status.sas;
    setPhase({ state: "pending", sas: status.sas });
  } else {
    setPhase({ state: status.state });
  }
}

function PairingShell({ children }: { children: React.ReactNode }) {
  return (
    <main className="min-h-[100dvh] bg-[radial-gradient(circle_at_top,#15352b_0%,#09090b_38%)] px-4 py-8 text-zinc-100 sm:grid sm:place-items-center sm:py-12">
      <section className="mx-auto w-full max-w-md rounded-2xl border border-zinc-800/90 bg-zinc-950/90 p-5 shadow-2xl shadow-black/40 backdrop-blur sm:p-7">
        {children}
      </section>
    </main>
  );
}

function StatusIcon({ tone }: { tone: "working" | "success" | "error" }) {
  const className = "mx-auto size-7";
  if (tone === "success") return <CircleCheck className={`${className} text-emerald-300`} aria-hidden="true" />;
  if (tone === "error") return <CircleX className={`${className} text-red-300`} aria-hidden="true" />;
  return <LoaderCircle className={`${className} animate-spin text-emerald-300`} aria-hidden="true" />;
}

function VerificationRow({ label, value, emphasis = false }: { label: string; value: string; emphasis?: boolean }) {
  return (
    <div className="flex items-center justify-between gap-4">
      <span className="text-xs text-zinc-500">{label}</span>
      <strong className={`font-mono text-base tracking-[0.12em] ${emphasis ? "text-amber-200" : "text-zinc-200"}`}>{value}</strong>
    </div>
  );
}

function phaseTitle(phase: PairingPhase): string {
  switch (phase.state) {
    case "connecting": return "Connecting to your computer";
    case "handshaking": return "Securing the connection";
    case "pending": return "Approve this phone on the computer";
    case "approved": return "Phone paired";
    case "rejected": return "Pairing rejected";
    case "error": return "Pairing could not finish";
    case "invalid": return "Invalid pairing link";
  }
}

function phaseDescription(phase: PairingPhase, agentName: string): string {
  switch (phase.state) {
    case "connecting": return `Opening a private connection to ${agentName}.`;
    case "handshaking": return "Authenticating both devices and deriving one-time encryption keys.";
    case "pending": return "The encrypted handshake is complete. Final approval must happen locally.";
    case "approved": return `${agentName} now recognizes this browser. You can close this page.`;
    case "rejected": return `${agentName} did not authorize this browser. Create a new QR code to try again.`;
    case "error": return "No trust was created on this browser. You can retry while the QR invitation is still valid.";
    case "invalid": return phase.message;
  }
}
