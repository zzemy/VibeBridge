import { Suspense, lazy } from "react";
import { useEffect, useState } from "react";

import type { PairingEntry } from "./lib/pairing-invitation";
import { InstallPrompt } from "./components/InstallPrompt";
import { UpdateBanner } from "./components/UpdateBanner";
import { t, subscribeLang } from "./lib/i18n";

const PairingScreen = lazy(() => import("./components/PairingScreen").then((module) => ({ default: module.PairingScreen })));
const TerminalApp = lazy(() => import("./TerminalApp").then((module) => ({ default: module.TerminalApp })));

export function App({ pairingEntry }: { pairingEntry?: PairingEntry | null } = {}) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  return (
    <Suspense fallback={<AppLoading />}>
      {pairingEntry ? <PairingScreen entry={pairingEntry} /> : <TerminalApp />}
      <InstallPrompt />
      <UpdateBanner />
    </Suspense>
  );
}

function AppLoading() {
  return (
    <main className="grid min-h-[100dvh] place-items-center bg-zinc-950 text-sm text-zinc-500" role="status">
      {t("app.loading")}
    </main>
  );
}
