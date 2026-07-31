import { useEffect, useState, useCallback } from "react";
import { invoke } from "@tauri-apps/api/core";

interface AgentStatus {
  running: boolean;
  port: number;
  info: Record<string, unknown>;
}

interface PairingData {
  code?: string;
  qr_url?: string;
}

type Tab = "status" | "pairing" | "settings";

export default function App() {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [pairing, setPairing] = useState<PairingData | null>(null);
  const [tab, setTab] = useState<Tab>("status");
  const [loading, setLoading] = useState(false);
  const [autoStart, setAutoStart] = useState(false);

  const fetchStatus = useCallback(async () => {
    try {
      const s = await invoke<AgentStatus>("get_agent_status");
      setStatus(s);
    } catch (e) {
      setStatus({ running: false, port: 8787, info: {} });
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 3000);
    return () => clearInterval(interval);
  }, [fetchStatus]);

  const handleRestart = async () => {
    setLoading(true);
    try {
      await invoke("restart_agent");
      setTimeout(() => fetchStatus(), 1000);
    } catch (e) {
      console.error("Restart failed:", e);
    }
    setLoading(false);
  };

  const fetchPairing = async () => {
    try {
      const data = await invoke<PairingData>("fetch_pairing_code");
      setPairing(data);
    } catch (e) {
      console.error("Pairing fetch failed:", e);
    }
  };

  useEffect(() => {
    if (tab === "pairing") fetchPairing();
  }, [tab]);

  return (
    <div className="app-container">
      {/* Header */}
      <div className="app-header">
        <img src="/icon.png" alt="VibeBridge" className="app-logo" />
        <span className="app-title">VibeBridge</span>
        <div style={{ flex: 1 }} />
        {status && (
          <span className={`status-badge ${status.running ? "online" : "offline"}`}>
            <span className={`status-dot ${status.running ? "online" : "offline"}`} />
            {status.running ? "Running" : "Stopped"}
          </span>
        )}
      </div>

      {/* Tabs */}
      <div style={{ display: "flex", gap: 4 }}>
        {(["status", "pairing", "settings"] as Tab[]).map((t) => (
          <button
            key={t}
            className={`btn ${tab === t ? "btn-primary" : "btn-secondary"}`}
            onClick={() => setTab(t)}
            style={{ flex: 1, fontSize: 12 }}
          >
            {t === "status" ? "Status" : t === "pairing" ? "Pairing" : "Settings"}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="scroll-area">
        {tab === "status" && (
          <>
            <div className="card">
              <div className="card-title">Agent</div>
              {status ? (
                <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>State</span>
                    <span style={{ fontSize: 13 }}>{status.running ? "Online" : "Offline"}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>Port</span>
                    <span style={{ fontSize: 13 }}>{status.port}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>Protocol</span>
                    <span style={{ fontSize: 13 }}>v1 (paired-session)</span>
                  </div>
                  <button
                    className="btn btn-secondary"
                    onClick={handleRestart}
                    disabled={loading}
                    style={{ marginTop: 8 }}
                  >
                    {loading ? "Restarting..." : "Restart Agent"}
                  </button>
                </div>
              ) : (
                <div className="empty-state">Loading...</div>
              )}
            </div>

            <div className="card">
              <div className="card-title">Connected Devices</div>
              <div className="device-list">
                <div className="device-item">
                  <div className="device-icon">📱</div>
                  <div>
                    <div className="device-name">No devices connected</div>
                    <div className="device-status">Pair a device to get started</div>
                  </div>
                </div>
              </div>
            </div>
          </>
        )}

        {tab === "pairing" && (
          <div className="card">
            <div className="card-title">Pair New Device</div>
            {pairing?.code ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 16, alignItems: "center" }}>
                <div className="pairing-code">{pairing.code}</div>
                <div style={{ fontSize: 12, color: "var(--text-secondary)", textAlign: "center" }}>
                  Enter this code on your mobile device, or scan the QR code below
                </div>
                <div className="qr-placeholder">
                  {pairing.qr_url ? (
                    <img src={pairing.qr_url} alt="QR Code" style={{ width: 160, height: 160, borderRadius: 12 }} />
                  ) : (
                    "QR Code"
                  )}
                </div>
                <button className="btn btn-secondary" onClick={fetchPairing}>
                  Refresh Code
                </button>
              </div>
            ) : (
              <div className="empty-state">
                {status?.running ? "Generating pairing code..." : "Agent is not running"}
              </div>
            )}
          </div>
        )}

        {tab === "settings" && (
          <>
            <div className="card">
              <div className="card-title">General</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between", cursor: "pointer" }}>
                  <span style={{ fontSize: 14 }}>Launch at startup</span>
                  <input
                    type="checkbox"
                    checked={autoStart}
                    onChange={(e) => setAutoStart(e.target.checked)}
                    style={{ width: 18, height: 18 }}
                  />
                </label>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 14 }}>Listen port</span>
                  <span style={{ fontSize: 13, color: "var(--text-secondary)" }}>8787 (default)</span>
                </label>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 14 }}>Protocol</span>
                  <span style={{ fontSize: 13, color: "var(--text-secondary)" }}>v1 only</span>
                </label>
              </div>
            </div>

            <div className="card">
              <div className="card-title">About</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>Version</span>
                  <span style={{ fontSize: 13 }}>1.0.0</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>Agent</span>
                  <span style={{ fontSize: 13 }}>Go (sidecar)</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>Shell</span>
                  <span style={{ fontSize: 13 }}>Tauri 2.x</span>
                </div>
              </div>
            </div>
          </>
        )}
      </div>
    </div>
  );
}
