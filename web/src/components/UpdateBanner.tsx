import { useEffect, useState } from "react";
import { t, subscribeLang } from "../lib/i18n";
import { useSWUpdate } from "../hooks/useSWUpdate";

export function UpdateBanner() {
  const { updateAvailable, applyUpdate } = useSWUpdate();
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  if (!updateAvailable) return null;

  return (
    <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-lg bg-blue-900 px-4 py-2 text-xs text-blue-100 shadow-lg ring-1 ring-blue-700 flex items-center gap-3">
      <span>{t("update.available")}</span>
      <button
        type="button"
        onClick={applyUpdate}
        className="rounded bg-blue-700 px-2 py-1 text-blue-50 hover:bg-blue-600 transition-colors"
      >
        {t("update.reload")}
      </button>
    </div>
  );
}
