import { useEffect, useState, useCallback } from "react";
import { invoke } from "@tauri-apps/api/core";
import { t, subscribeLang, toggleLang } from "./lib/i18n";

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
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

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
            {status.running ? t("header.running") : t("header.stopped")}
          </span>
        )}
        <button
          className="btn btn-secondary"
          onClick={() => toggleLang()}
          style={{ minWidth: 36, fontSize: 12, padding: "4px 8px" }}
          title="中文 / English"
        >
          {t("lang.toggle")}
        </button>
      </div>

      {/* Tabs */}
      <div style={{ display: "flex", gap: 4 }}>
        {(["status", "pairing", "settings"] as Tab[]).map((tb) => (
          <button
            key={tb}
            className={`btn ${tab === tb ? "btn-primary" : "btn-secondary"}`}
            onClick={() => setTab(tb)}
            style={{ flex: 1, fontSize: 12 }}
          >
            {t(`tab.${tb}`)}
          </button>
        ))}
      </div>

      {/* Content */}
      <div className="scroll-area">
        {tab === "status" && (
          <>
            <div className="card">
              <div className="card-title">{t("status.agent")}</div>
              {status ? (
                <div style={{ display: "flex", flexDirection: "column", gap: 8 }}>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("status.state")}</span>
                    <span style={{ fontSize: 13 }}>{status.running ? t("status.online") : t("status.offline")}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("status.port")}</span>
                    <span style={{ fontSize: 13 }}>{status.port}</span>
                  </div>
                  <div style={{ display: "flex", justifyContent: "space-between" }}>
                    <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("status.protocol")}</span>
                    <span style={{ fontSize: 13 }}>{t("status.protocolValue")}</span>
                  </div>
                  <button
                    className="btn btn-secondary"
                    onClick={handleRestart}
                    disabled={loading}
                    style={{ marginTop: 8 }}
                  >
                    {loading ? t("status.restarting") : t("status.restartAgent")}
                  </button>
                </div>
              ) : (
                <div className="empty-state">{t("status.loading")}</div>
              )}
            </div>

            <div className="card">
              <div className="card-title">{t("status.connectedDevices")}</div>
              <div className="device-list">
                <div className="device-item">
                  <div className="device-icon">📱</div>
                  <div>
                    <div className="device-name">{t("status.noDevices")}</div>
                    <div className="device-status">{t("status.pairToStart")}</div>
                  </div>
                </div>
              </div>
            </div>
          </>
        )}

        {tab === "pairing" && (
          <div className="card">
            <div className="card-title">{t("pairing.title")}</div>
            {pairing?.code ? (
              <div style={{ display: "flex", flexDirection: "column", gap: 16, alignItems: "center" }}>
                <div className="pairing-code">{pairing.code}</div>
                <div style={{ fontSize: 12, color: "var(--text-secondary)", textAlign: "center" }}>
                  {t("pairing.enterCode")}
                </div>
                <div className="qr-placeholder">
                  {pairing.qr_url ? (
                    <img src={pairing.qr_url} alt={t("pairing.qrCode")} style={{ width: 160, height: 160, borderRadius: 12 }} />
                  ) : (
                    t("pairing.qrCode")
                  )}
                </div>
                <button className="btn btn-secondary" onClick={fetchPairing}>
                  {t("pairing.refreshCode")}
                </button>
              </div>
            ) : (
              <div className="empty-state">
                {status?.running ? t("pairing.generating") : t("pairing.agentNotRunning")}
              </div>
            )}
          </div>
        )}

        {tab === "settings" && (
          <>
            <div className="card">
              <div className="card-title">{t("settings.general")}</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 12 }}>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between", cursor: "pointer" }}>
                  <span style={{ fontSize: 14 }}>{t("settings.launchAtStartup")}</span>
                  <input
                    type="checkbox"
                    checked={autoStart}
                    onChange={(e) => setAutoStart(e.target.checked)}
                    style={{ width: 18, height: 18 }}
                  />
                </label>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 14 }}>{t("settings.listenPort")}</span>
                  <span style={{ fontSize: 13, color: "var(--text-secondary)" }}>{t("settings.portDefault")}</span>
                </label>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 14 }}>{t("settings.protocol")}</span>
                  <span style={{ fontSize: 13, color: "var(--text-secondary)" }}>{t("settings.v1Only")}</span>
                </label>
                <label style={{ display: "flex", alignItems: "center", justifyContent: "space-between" }}>
                  <span style={{ fontSize: 14 }}>{t("settings.language")}</span>
                  <button
                    className="btn btn-secondary"
                    onClick={() => toggleLang()}
                    style={{ fontSize: 12, padding: "4px 12px" }}
                  >
                    {t("lang.toggle")}
                  </button>
                </label>
              </div>
            </div>

            <div className="card">
              <div className="card-title">{t("settings.about")}</div>
              <div style={{ display: "flex", flexDirection: "column", gap: 6 }}>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("settings.version")}</span>
                  <span style={{ fontSize: 13 }}>1.0.0</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("settings.agent")}</span>
                  <span style={{ fontSize: 13 }}>{t("settings.agentValue")}</span>
                </div>
                <div style={{ display: "flex", justifyContent: "space-between" }}>
                  <span style={{ color: "var(--text-secondary)", fontSize: 13 }}>{t("settings.shell")}</span>
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
