import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { consumePairingEntry } from "./lib/pairing-invitation";
import "./styles/globals.css";

const pairingEntry = consumePairingEntry(window.location, window.history);

// Register the service worker for PWA offline support.
// The SW caches static assets only; API calls and WebSocket
// connections are never intercepted.
if ("serviceWorker" in navigator) {
  window.addEventListener("load", () => {
    navigator.serviceWorker.register("/sw.js").catch(() => {
      // SW registration failure is non-fatal; the app works without offline support.
    });
  });
}

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App pairingEntry={pairingEntry} />
  </StrictMode>,
);
