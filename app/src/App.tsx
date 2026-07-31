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
  platform: string;
  state: string;
}

interface PendingPairing {
  name: string;
  platform: string;
  sas: string;
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

  // Extract data from agent info response
  const info = status?.info ?? {};
  const devices: DeviceInfo[] = Array.isArray(info.devices) ? (info.devices as DeviceInfo[]) : [];
  const pendingPairing: PendingPairing | null = info.pending_pairing ? (info.pending_pairing as PendingPairing) : null;
  const activeDevices = devices.filter((d) => d.state === "Authorized");

  const navItems: { key: Section; icon: string; label: string }[] = [
    { key: "status", icon: "◉", label: t("nav.status") },
    { key: "devices", icon: "▢", label: t("nav.devices") },
    { key: "pairing", icon: "⊕", label: t("nav.pairing") },
    { key: "settings", icon: "⚙", label: t("nav.settings") },
  ];

  const platformIcon = (platform: string) => {
    const p = platform.toLowerCase();
    if (p.includes("ios") || p.includes("iphone") || p.includes("ipad")) return "";
    if (p.includes("android")) return "🤖";
    return "📱";
  };

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
                    <span className="data-label">{t("status.devices")}</span>
                    <span className="data-value">{activeDevices.length}</span>
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

            {pendingPairing && (
              <div className="card">
                <div className="card-header">
                  <span className="card-title">{t("status.pendingPairing")}</span>
                </div>
                <div className="data-row">
                  <span className="data-label">{t("status.deviceName")}</span>
                  <span className="data-value">{pendingPairing.name}</span>
                </div>
                <div className="data-row">
                  <span className="data-label">{t("status.platform")}</span>
                  <span className="data-value">{pendingPairing.platform}</span>
                </div>
                <div className="data-row">
                  <span className="data-label">{t("status.verificationCode")}</span>
                  <span className="data-value mono text-accent">{pendingPairing.sas}</span>
                </div>
                <div style={{ marginTop: 12, fontSize: 12, color: "var(--text-secondary)" }}>
                  {t("status.pairingHint")}
                </div>
              </div>
            )}
          </div>
        )}

        {section === "devices" && (
          <div className="fade-in">
            <h1 className="page-title">{t("nav.devices")}</h1>

            {pendingPairing && (
              <div className="card">
                <div className="card-header">
                  <span className="card-title">{t("devices.pendingTitle")}</span>
                </div>
                <div className="device-item">
                  <div className="device-icon">{platformIcon(pendingPairing.platform)}</div>
                  <div className="device-info">
                    <div className="device-name">{pendingPairing.name}</div>
                    <div className="device-meta">{pendingPairing.platform}</div>
                  </div>
                  <span className="status-badge online">{t("devices.pending")}</span>
                </div>
                <div style={{ marginTop: 12, fontSize: 12, color: "var(--text-secondary)" }}>
                  {t("status.pairingHint")}
                </div>
              </div>
            )}

            <div className="card">
              <div className="card-header">
                <span className="card-title">{t("devices.paired")}</span>
                <span className="text-secondary" style={{ fontSize: 12 }}>{devices.length}</span>
              </div>
              {devices.length > 0 ? (
                <div className="device-list">
                  {devices.map((device) => (
                    <div key={device.id} className="device-item">
                      <div className="device-icon">{platformIcon(device.platform)}</div>
                      <div className="device-info">
                        <div className="device-name">{device.name || device.id.slice(0, 8)}</div>
                        <div className="device-meta">
                          {device.platform || "Unknown"}
                          {device.state !== "Authorized" ? ` · ${device.state}` : ""}
                        </div>
                      </div>
                      <div className="device-actions">
                        <span className={`status-badge ${device.state === "Authorized" ? "online" : "offline"}`}>
                          {device.state === "Authorized" ? t("devices.connected") : t("devices.revoked")}
                        </span>
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
