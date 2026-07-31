import { useEffect, useState } from "react";
import { t, subscribeLang } from "../lib/i18n";
import { usePWAInstall } from "../hooks/usePWAInstall";

export function InstallPrompt() {
  const { canInstall, promptInstall } = usePWAInstall();
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  if (!canInstall) return null;

  return (
    <button
      type="button"
      onClick={() => void promptInstall()}
      className="fixed bottom-4 right-4 z-50 rounded-lg bg-zinc-800 px-3 py-2 text-xs text-zinc-200 shadow-lg ring-1 ring-zinc-700 hover:bg-zinc-700 transition-colors"
    >
      {t("install.installApp")}
    </button>
  );
}
