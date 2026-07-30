import { useSWUpdate } from "../hooks/useSWUpdate";

/**
 * Shows a banner when a new service worker version is available.
 * Clicking "Reload" activates the new version.
 */
export function UpdateBanner() {
  const { updateAvailable, applyUpdate } = useSWUpdate();

  if (!updateAvailable) return null;

  return (
    <div className="fixed bottom-4 left-1/2 z-50 -translate-x-1/2 rounded-lg bg-blue-900 px-4 py-2 text-xs text-blue-100 shadow-lg ring-1 ring-blue-700 flex items-center gap-3">
      <span>A new version is available.</span>
      <button
        type="button"
        onClick={applyUpdate}
        className="rounded bg-blue-700 px-2 py-1 text-blue-50 hover:bg-blue-600 transition-colors"
      >
        Reload
      </button>
    </div>
  );
}
