import { useCallback, useEffect, useState } from "react";

/**
 * Detects when a new service worker version has been installed
 * and is waiting to activate. Exposes a function to trigger the
 * update by telling the waiting SW to skipWaiting.
 */
export function useSWUpdate() {
  const [updateAvailable, setUpdateAvailable] = useState(false);

  useEffect(() => {
    if (!("serviceWorker" in navigator)) return;

    const handleControllerChange = () => {
      // Controller changed means a new SW took over — reload to
      // get fresh static assets.
      window.location.reload();
    };

    const handleMessage = (event: MessageEvent) => {
      if (event.data?.type === "SW_UPDATED") {
        setUpdateAvailable(true);
      }
    };

    navigator.serviceWorker.addEventListener("controllerchange", handleControllerChange);
    navigator.serviceWorker.addEventListener("message", handleMessage);

    // Check if there's already a waiting registration.
    navigator.serviceWorker.getRegistration().then((reg) => {
      if (reg?.waiting) {
        setUpdateAvailable(true);
      }
    });

    // Listen for new waiting workers.
    navigator.serviceWorker.addEventListener("updatefound", () => {
      navigator.serviceWorker.getRegistration().then((reg) => {
        if (reg?.installing) {
          reg.installing.addEventListener("statechange", () => {
            if (reg.waiting) {
              setUpdateAvailable(true);
            }
          });
        }
      });
    });

    return () => {
      navigator.serviceWorker.removeEventListener("controllerchange", handleControllerChange);
      navigator.serviceWorker.removeEventListener("message", handleMessage);
    };
  }, []);

  const applyUpdate = useCallback(() => {
    navigator.serviceWorker.getRegistration().then((reg) => {
      if (reg?.waiting) {
        reg.waiting.postMessage({ type: "SKIP_WAITING" });
      }
    });
  }, []);

  return { updateAvailable, applyUpdate };
}
