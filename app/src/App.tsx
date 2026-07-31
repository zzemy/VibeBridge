import { useEffect, useState, useCallback } from "react";
import { invoke } from "@tauri-apps/api/core";
import { t, subscribeLang, toggleLang, getLang } from "./lib/i18n";

interface AgentStatus {
  running: boolean;
  port: number;
  info: Record<string, unknown>;
}

interface PairingData {
  code?: string;
  qr_url?: string;
}

interface DeviceInfo {
  id: string;
  name: string;
  type: string;
  paired: boolean;
  lastSeen?: string;
}

type Section = "status" | "devices" | "pairing" | "settings";

export default function App() {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [pairing, setPairing] = useState<PairingData | null>(null);
  const [section, setSection] = useState<Section>("status");
  const [loading, setLoading] = useState(false);
  const [autoStart, setAutoStart] = useState(false);
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const fetchStatus = useCallback(async () => {
    try {
      const s = await invoke<AgentStatus>("get_agent_status");
      setStatus(s);
    } catch {
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
    if (section === "pairing") fetchPairing();
  }, [section]);

  // Extract device list from agent status info
  const devices: DeviceInfo[] = (() => {
    if (!status?.info) return [];
    const raw = status.info.devices;
    if (Array.isArray(raw)) return raw as DeviceInfo[];
    return [];
  })();

  // Extract resource info from agent status info
  const resources = (() => {
    if (!status?.info) return null;
    const info = status.info;
    return {
      cpu: (info.cpu as number) ?? 0,
      memory: (info.memory as number) ?? 0,
      uptime: (info.uptime as string) ?? "—",
      sessions: (info.sessions as number) ?? 0,
    };
  })();

  const navItems: { key: Section; icon: string; label: string }[] = [
    { key: "status", icon: "◉", label: t("nav.status") },
    { key: "devices", icon: "▢", label: t("nav.devices") },
    { key: "pairing", icon: "⊕", label: t("nav.pairing") },
    { key: "settings", icon: "⚙", label: t("nav.settings") },
  ];

  return (
    <div className="app-shell">
      {/* Sidebar */}
      <aside className="sidebar">
        <div className="sidebar-brand">
          <img src="/icon.png" alt="VibeBridge" className="sidebar-logo" />
          <span className="sidebar-title">VibeBridge</span>
        </div>

        <nav className="sidebar-nav">
          {navItems.map((item) => (
            <button
              key={item.key}
              className={`nav-item ${section === item.key ? "active" : ""}`}
              onClick={() => setSection(item.key)}
            >
              <span className="nav-icon">{item.icon}</span>
              {item.label}
            </button>
          ))}
        </nav>

        <div className="sidebar-footer">
          <div className="sidebar-status">
            <span className={`status-dot ${status?.running ? "online" : "offline"}`} />
            {status?.running ? t("header.running") : t("header.stopped")}
          </div>
          <button className="lang-btn" onClick={() => toggleLang()}>
            {getLang() === "zh" ? "EN" : "中文"}
          </button>
        </div>
      </aside>

      {/* Main content */}
      <main className="main-content">
        {section === "status" && (
          <div className="fade-in">
            <h1 className="page-title">{t("nav.status")}</h1>

            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("status.agent")}</span>
                {status && (
                  <span className={`status-badge ${status.running ? "online" : "offline"}`}>
                    {status.running ? t("status.online") : t("status.offline")}
                  </span>
                )}
              </div>
              {status ? (
                <>
                  <div className="data-row">
                    <span className="data-label">{t("status.state")}</span>
                    <span className="data-value">{status.running ? t("status.online") : t("status.offline")}</span>
                  </div>
                  <div className="data-row">
                    <span className="data-label">{t("status.port")}</span>
                    <span className="data-value mono">{status.port}</span>
                  </div>
                  <div className="data-row">
                    <span className="data-label">{t("status.protocol")}</span>
                    <span className="data-value">{t("status.protocolValue")}</span>
                  </div>
                  <div className="data-row">
                    <span className="data-label">{t("status.sessions")}</span>
                    <span className="data-value">{resources?.sessions ?? 0}</span>
                  </div>
                  <div style={{ marginTop: 14 }}>
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={handleRestart}
                      disabled={loading}
                    >
                      {loading ? <span className="spinner" /> : null}
                      {loading ? t("status.restarting") : t("status.restartAgent")}
                    </button>
                  </div>
                </>
              ) : (
                <div className="empty-state">{t("status.loading")}</div>
              )}
            </div>

            {resources && (
              <div className="card">
                <div className="card-header">
                  <span className="card-title">{t("status.resources")}</span>
                </div>
                <div className="data-row">
                  <span className="data-label">{t("status.cpu")}</span>
                  <span className="data-value mono">{resources.cpu.toFixed(1)}%</span>
                </div>
                <div className="resource-bar">
                  <div
                    className={`resource-bar-fill ${resources.cpu > 80 ? "danger" : resources.cpu > 50 ? "warning" : "normal"}`}
                    style={{ width: `${Math.min(resources.cpu, 100)}%` }}
                  />
                </div>
                <div className="data-row" style={{ marginTop: 8 }}>
                  <span className="data-label">{t("status.memory")}</span>
                  <span className="data-value mono">{resources.memory.toFixed(1)}%</span>
                </div>
                <div className="resource-bar">
                  <div
                    className={`resource-bar-fill ${resources.memory > 80 ? "danger" : resources.memory > 50 ? "warning" : "normal"}`}
                    style={{ width: `${Math.min(resources.memory, 100)}%` }}
                  />
                </div>
                <div className="data-row" style={{ marginTop: 8 }}>
                  <span className="data-label">{t("status.uptime")}</span>
                  <span className="data-value">{resources.uptime}</span>
                </div>
              </div>
            )}
          </div>
        )}

        {section === "devices" && (
          <div className="fade-in">
            <h1 className="page-title">{t("nav.devices")}</h1>
            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("devices.paired")}</span>
                <span className="text-secondary" style={{ fontSize: 12 }}>{devices.length}</span>
              </div>
              {devices.length > 0 ? (
                <div className="device-list">
                  {devices.map((device) => (
                    <div key={device.id} className="device-item">
                      <div className="device-icon">{device.type === "ios" ? "" : device.type === "android" ? "🤖" : "📱"}</div>
                      <div className="device-info">
                        <div className="device-name">{device.name}</div>
                        <div className="device-meta">
                          {device.paired ? t("devices.connected") : t("devices.disconnected")}
                          {device.lastSeen ? ` · ${device.lastSeen}` : ""}
                        </div>
                      </div>
                      <div className="device-actions">
                        <button className="btn btn-danger btn-sm">{t("devices.revoke")}</button>
                      </div>
                    </div>
                  ))}
                </div>
              ) : (
                <div className="empty-state">
                  <div className="empty-state-icon">📱</div>
                  {t("devices.empty")}
                </div>
              )}
            </div>
            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("devices.pairNew")}</span>
              </div>
              <div className="text-secondary" style={{ fontSize: 13, marginBottom: 12 }}>
                {t("devices.pairHint")}
              </div>
              <button className="btn btn-primary btn-sm" onClick={() => setSection("pairing")}>
                {t("devices.goPairing")}
              </button>
            </div>
          </div>
        )}

        {section === "pairing" && (
          <div className="fade-in">
            <h1 className="page-title">{t("pairing.title")}</h1>
            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("pairing.title")}</span>
                {status?.running && (
                  <span className="status-badge online">{t("status.online")}</span>
                )}
              </div>
              {pairing?.code ? (
                <div style={{ display: "flex", flexDirection: "column", gap: 16, alignItems: "center", padding: "8px 0" }}>
                  <div className="pairing-code">{pairing.code}</div>
                  <div className="text-secondary" style={{ fontSize: 12, textAlign: "center", maxWidth: 280 }}>
                    {t("pairing.enterCode")}
                  </div>
                  <div className="qr-container">
                    <div className="qr-frame">
                      {pairing.qr_url ? (
                        <img src={pairing.qr_url} alt={t("pairing.qrCode")} />
                      ) : (
                        <span className="text-secondary" style={{ fontSize: 12 }}>{t("pairing.qrCode")}</span>
                      )}
                    </div>
                  </div>
                  <button className="btn btn-secondary btn-sm" onClick={fetchPairing}>
                    {t("pairing.refreshCode")}
                  </button>
                </div>
              ) : (
                <div className="empty-state">
                  {status?.running ? t("pairing.generating") : t("pairing.agentNotRunning")}
                </div>
              )}
            </div>
          </div>
        )}

        {section === "settings" && (
          <div className="fade-in">
            <h1 className="page-title">{t("nav.settings")}</h1>

            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("settings.general")}</span>
              </div>
              <div className="setting-row">
                <div>
                  <div className="setting-label">{t("settings.launchAtStartup")}</div>
                </div>
                <div
                  className={`toggle ${autoStart ? "active" : ""}`}
                  onClick={() => setAutoStart(!autoStart)}
                />
              </div>
              <div className="setting-row">
                <div>
                  <div className="setting-label">{t("settings.listenPort")}</div>
                  <div className="setting-hint">{t("settings.portDefault")}</div>
                </div>
                <span className="data-value mono">8787</span>
              </div>
              <div className="setting-row">
                <div>
                  <div className="setting-label">{t("settings.protocol")}</div>
                  <div className="setting-hint">{t("settings.v1Only")}</div>
                </div>
                <span className="text-accent" style={{ fontSize: 12, fontWeight: 600 }}>v1</span>
              </div>
              <div className="setting-row">
                <div>
                  <div className="setting-label">{t("settings.language")}</div>
                </div>
                <button className="btn btn-secondary btn-sm" onClick={() => toggleLang()}>
                  {getLang() === "zh" ? "English" : "中文"}
                </button>
              </div>
            </div>

            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("settings.about")}</span>
              </div>
              <div className="data-row">
                <span className="data-label">{t("settings.version")}</span>
                <span className="data-value mono">1.0.0</span>
              </div>
              <div className="data-row">
                <span className="data-label">{t("settings.agent")}</span>
                <span className="data-value">{t("settings.agentValue")}</span>
              </div>
              <div className="data-row">
                <span className="data-label">{t("settings.shell")}</span>
                <span className="data-value">Tauri 2.x</span>
              </div>
            </div>
          </div>
        )}
      </main>
    </div>
  );
}
