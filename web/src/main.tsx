import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import { App } from "./App";
import { consumePairingEntry } from "./lib/pairing-invitation";
import "./styles/globals.css";

const pairingEntry = consumePairingEntry(window.location, window.history);

createRoot(document.getElementById("root")!).render(
  <StrictMode>
    <App pairingEntry={pairingEntry} />
  </StrictMode>,
);
