import { usePWAInstall } from "../hooks/usePWAInstall";

/**
 * Renders a minimal install banner when the browser indicates
 * the app is installable. Disappears once installed or dismissed.
 */
export function InstallPrompt() {
  const { canInstall, promptInstall } = usePWAInstall();

  if (!canInstall) return null;

  return (
    <button
      type="button"
      onClick={() => void promptInstall()}
      className="fixed bottom-4 right-4 z-50 rounded-lg bg-zinc-800 px-3 py-2 text-xs text-zinc-200 shadow-lg ring-1 ring-zinc-700 hover:bg-zinc-700 transition-colors"
    >
      Install app
    </button>
  );
}
