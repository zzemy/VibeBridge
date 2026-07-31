import { useEffect, useState, useCallback } from "react";
import type { ReactNode } from "react";
import { invoke } from "@tauri-apps/api/core";
import { t, subscribeLang, toggleLang, getLang } from "./lib/i18n";

/* ── Types ── */

interface AgentStatus {
  running: boolean;
  port: number;
  info: Record<string, unknown>;
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

interface PairingData {
  code?: string;
  qr_url?: string;
  target?: string;
  expires_at?: string;
}

type Section = "overview" | "pairing" | "devices" | "settings";
type SettingsTab = "general" | "network" | "terminal" | "security" | "advanced" | "about";

/* ── SVG Icon set ── */

function Svg({ children, size = 18 }: { children: ReactNode; size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth={1.8} strokeLinecap="round" strokeLinejoin="round" aria-hidden="true">{children}</svg>
  );
}

const NavIcons: Record<Section, ReactNode> = {
  overview: (<><rect x="3" y="3" width="7" height="9" rx="1.5" /><rect x="14" y="3" width="7" height="5" rx="1.5" /><rect x="14" y="12" width="7" height="9" rx="1.5" /><rect x="3" y="16" width="7" height="5" rx="1.5" /></>),
  pairing: (<><rect x="3" y="3" width="6" height="6" rx="1" /><rect x="15" y="3" width="6" height="6" rx="1" /><rect x="3" y="15" width="6" height="6" rx="1" /><path d="M15 15h2.5v2.5H15z" /><path d="M21 15v2.5" /><path d="M15 21h2.5" /><path d="M18.5 18.5H21V21h-2.5z" /></>),
  devices: (<><rect x="7" y="2" width="10" height="20" rx="2.5" /><path d="M11 18.5h2" /></>),
  settings: (<><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></>),
};

const Lightning = () => (<Svg size={24}><path d="M13 2L3 14h9l-1 8 10-12h-9l1-8z" fill="currentColor" stroke="none" /></Svg>);
const PhoneIcon = () => (<Svg><rect x="7" y="2" width="10" height="20" rx="2.5" /><path d="M11 18.5h2" /></Svg>);
const SearchIcon = () => (<Svg size={16}><circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" /></Svg>);
const CopyIcon = () => (<Svg size={16}><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></Svg>);
const ExternalIcon = () => (<Svg size={16}><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></Svg>);
const FileIcon = () => (<Svg size={16}><path d="M14 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V8z" /><path d="M14 2v6h6" /></Svg>);
const CheckIcon = () => (<Svg size={14}><path d="M20 6 9 17l-5-5" /></Svg>);
const XIcon = () => (<Svg size={14}><path d="M18 6 6 18" /><path d="m6 6 12 12" /></Svg>);
const CpuIcon = () => (<Svg size={16}><rect x="4" y="4" width="16" height="16" rx="2" /><rect x="9" y="9" width="6" height="6" /><path d="M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 15h3M1 9h3M1 15h3" /></Svg>);
const MemoryIcon = () => (<Svg size={16}><path d="M6 19v-3M10 19v-3M14 19v-3M18 19v-3M8 11V9M16 11V9M12 11V9M2 13h20v4H2zM4 2h16v4H4z" /></Svg>);
const DiskIcon = () => (<Svg size={16}><ellipse cx="12" cy="5" rx="9" ry="3" /><path d="M3 5v14a9 3 0 0 0 18 0V5" /><path d="M3 12a9 3 0 0 0 18 0" /></Svg>);
const WifiIcon = () => (<Svg size={16}><path d="M5 12.55a11 11 0 0 1 14 0M1.42 9a16 16 0 0 1 21.16 0M8.53 16.11a6 6 0 0 1 6.95 0" /><path d="M12 20h.01" /></Svg>);
const ActivityIcon = () => (<Svg size={16}><path d="M22 12h-4l-3 9L9 3l-3 9H2" /></Svg>);
const ShareIcon = () => (<Svg size={16}><circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" /><path d="m8.59 13.51 6.83 3.98M15.41 6.51l-6.82 3.98" /></Svg>);
const ClockIcon = () => (<Svg size={16}><circle cx="12" cy="12" r="10" /><path d="M12 6v6l4 2" /></Svg>);

/* ── Helpers ── */

function formatUptime(seconds: number): string {
  if (!seconds || seconds < 0) return "—";
  const d = Math.floor(seconds / 86400);
  const h = Math.floor((seconds % 86400) / 3600);
  const m = Math.floor((seconds % 3600) / 60);
  const parts: string[] = [];
  if (d > 0) parts.push(`${d}天`);
  parts.push(`${h}小时`);
  parts.push(`${m}分`);
  return parts.join(" ");
}

/* ── Component ── */

export default function App() {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [pairing, setPairing] = useState<PairingData | null>(null);
  const [section, setSection] = useState<Section>("overview");
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
    try { await invoke("restart_agent"); setTimeout(() => fetchStatus(), 1000); } catch {}
    setLoading(false);
  };

  const fetchPairing = async () => {
    try { const data = await invoke<PairingData>("fetch_pairing_code"); setPairing(data); } catch {}
  };

  useEffect(() => { if (section === "pairing") fetchPairing(); }, [section]);

  const info = status?.info ?? {};
  const devices: DeviceInfo[] = Array.isArray(info.devices) ? (info.devices as DeviceInfo[]) : [];
  const pendingPairing: PendingPairing | null = info.pending_pairing ? (info.pending_pairing as PendingPairing) : null;
  const activeDevices = devices.filter((d) => d.state === "Authorized");
  const uptimeSec = Number(info.uptime_seconds ?? 0);
  const memMb = Number(info.memory_alloc_mb ?? 0);
  const goroutines = Number(info.goroutines ?? 0);
  const cpuCores = Number(info.cpu_cores ?? 0);

  const navItems: { key: Section; label: string }[] = [
    { key: "overview", label: t("nav.overview") },
    { key: "pairing", label: t("nav.pairing") },
    { key: "devices", label: t("nav.devices") },
    { key: "settings", label: t("nav.settings") },
  ];

  return (
    <div className="app-shell">
      <aside className="sidebar">
        <div className="sidebar-brand">
          <img src="/icon.png" alt="VibeBridge" className="sidebar-logo" />
          <span className="sidebar-title">VibeBridge</span>
        </div>

        <div className="sidebar-section-label">{t("sidebar.mainMenu")}</div>
        <nav className="sidebar-nav">
          {navItems.map((item) => (
            <button key={item.key} className={`nav-item ${section === item.key ? "active" : ""}`} onClick={() => setSection(item.key)}>
              <span className="nav-icon"><Svg>{NavIcons[item.key]}</Svg></span>
              {item.label}
            </button>
          ))}
        </nav>

        <div className="sidebar-footer">
          <div className="user-card">
            <div className="user-avatar">郑</div>
            <div className="user-info">
              <div className="user-name">{t("sidebar.you")}</div>
              <div className="user-meta">{t("sidebar.thisDevice")}</div>
            </div>
            <span className="nav-icon" style={{ cursor: "pointer" }} onClick={() => setSection("settings")}>
              <Svg size={16}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></Svg>
            </span>
          </div>
        </div>
      </aside>

      <main className="main-content">
        {section === "overview" && <OverviewSection status={status} uptimeSec={uptimeSec} memMb={memMb} goroutines={goroutines} cpuCores={cpuCores} activeDevices={activeDevices} loading={loading} onRestart={handleRestart} />}
        {section === "pairing" && <PairingSection status={status} pairing={pairing} pendingPairing={pendingPairing} onRefresh={fetchPairing} />}
        {section === "devices" && <DevicesSection devices={devices} pendingPairing={pendingPairing} />}
        {section === "settings" && <SettingsSection autoStart={autoStart} setAutoStart={setAutoStart} />}
      </main>
    </div>
  );
}

/* ════════ Overview ════════ */

function OverviewSection({ status, uptimeSec, memMb, goroutines, cpuCores, activeDevices, loading, onRestart }: {
  status: AgentStatus | null;
  uptimeSec: number; memMb: number; goroutines: number; cpuCores: number;
  activeDevices: DeviceInfo[]; loading: boolean; onRestart: () => void;
}) {
  const port = status?.port ?? 8787;
  const running = status?.running ?? false;

  const stats = [
    { label: t("stats.runtime"), value: running ? t("stats.running") : t("stats.stopped"), delta: formatUptime(uptimeSec), icon: <ActivityIcon />, deltaClass: running ? "up" : "neutral" },
    { label: t("stats.connectedDevices"), value: String(activeDevices.length), delta: t("stats.total"), icon: <PhoneIcon />, deltaClass: "neutral" },
    { label: t("stats.goroutines"), value: String(goroutines || "—"), delta: t("stats.activeConns"), icon: <CpuIcon />, deltaClass: "neutral" },
    { label: t("stats.memory"), value: memMb ? `${memMb} MB` : "—", delta: t("stats.alloc"), icon: <MemoryIcon />, deltaClass: "neutral" },
  ];

  const memPercent = Math.min(100, (memMb / 4096) * 100);

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("nav.overview")}</h1>
        <p className="page-subtitle">{t("overview.subtitle")}</p>
      </div>

      {/* Hero */}
      <div className="card">
        <div className="hero-card">
          <div className="hero-icon"><Lightning /></div>
          <div className="hero-body">
            <div className="hero-title-row">
              <span className="hero-title">{running ? t("overview.agentRunning") : t("overview.agentStopped")}</span>
              <span className={`badge ${running ? "online" : "offline"}`}>
                <span className={`badge-dot ${running ? "green" : "red"}`} />
                {running ? t("status.online") : t("status.offline")}
              </span>
            </div>
            <div className="hero-meta">
              {t("status.port")} <span className="mono">{port}</span> · {t("status.protocol")} <span className="mono">{t("status.protocolValue")}</span> · {t("overview.uptime")} {formatUptime(uptimeSec)}
            </div>
          </div>
          <div className="hero-actions">
            <button className="btn btn-secondary btn-sm" onClick={onRestart} disabled={loading}>
              {loading && <span className="spinner" />}
              {loading ? t("status.restarting") : t("status.restartAgent")}
            </button>
          </div>
        </div>
      </div>

      {/* Stat cards */}
      <div className="stat-grid">
        {stats.map((s, i) => (
          <div key={i} className="stat-card">
            <div className="stat-top">
              <span className="stat-label">{s.label}</span>
              <span className="stat-icon">{s.icon}</span>
            </div>
            <div className="stat-value">{s.value}</div>
            <div className={`stat-delta ${s.deltaClass}`}>{s.delta}</div>
          </div>
        ))}
      </div>

      {/* Resource + Quick actions */}
      <div className="two-col">
        <div className="card">
          <div className="card-header">
            <span className="card-title">{t("overview.resourceUsage")}</span>
            <span className="badge info">{t("overview.realtime")}</span>
          </div>
          <div className="resource-row">
            <span className="resource-label">{t("overview.cpu")}</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: cpuCores ? "5%" : "0%" }} /></div>
            <span className="resource-value">{cpuCores ? `${cpuCores}核` : "—"}</span>
          </div>
          <div className="resource-row">
            <span className="resource-label">{t("overview.memory")}</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${memPercent}%` }} /></div>
            <span className="resource-value">{memMb ? `${memMb}MB` : "—"}</span>
          </div>
          <div className="resource-row">
            <span className="resource-label">{t("overview.disk")}</span>
            <div className="resource-bar"><div className="resource-bar-fill yellow" style={{ width: "0%" }} /></div>
            <span className="resource-value">—</span>
          </div>
          <div className="resource-row">
            <span className="resource-label">{t("overview.network")}</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: "0%" }} /></div>
            <span className="resource-value">—</span>
          </div>
        </div>

        <div className="card">
          <div className="card-header"><span className="card-title">{t("overview.quickActions")}</span></div>
          <div className="action-row">
            <div className="action-icon"><CopyIcon /></div>
            <div className="action-body">
              <div className="action-label">{t("overview.copyAddr")}</div>
              <div className="action-sub">ws://127.0.0.1:{port}/ws</div>
            </div>
          </div>
          <div className="action-row">
            <div className="action-icon"><ExternalIcon /></div>
            <div className="action-body">
              <div className="action-label">{t("overview.openWeb")}</div>
              <div className="action-sub">http://127.0.0.1:{port}</div>
            </div>
          </div>
          <div className="action-row">
            <div className="action-icon"><FileIcon /></div>
            <div className="action-body">
              <div className="action-label">{t("overview.viewLogs")}</div>
              <div className="action-sub">/var/log/vibebridge</div>
            </div>
          </div>
        </div>
      </div>

      {/* Connected devices */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">{t("overview.connectedDevices")}</span>
          <button className="link" onClick={() => {}}>{t("overview.viewAll")}</button>
        </div>
        {activeDevices.length > 0 ? activeDevices.map((d) => (
          <div key={d.id} className="device-row">
            <div className="device-avatar"><PhoneIcon /></div>
            <div className="device-body">
              <div className="device-name">{d.name || d.id.slice(0, 8)}</div>
              <div className="device-sub">{d.platform}</div>
            </div>
            <span className="badge online"><span className="badge-dot green" />{t("status.online")}</span>
          </div>
        )) : (
          <div className="empty-state">{t("devices.empty")}</div>
        )}
      </div>
    </div>
  );
}

/* ════════ Pairing ════════ */

function PairingSection({ status, pairing, pendingPairing, onRefresh }: {
  status: AgentStatus | null; pairing: PairingData | null;
  pendingPairing: PendingPairing | null; onRefresh: () => void;
}) {
  const running = status?.running ?? false;
  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("pairing.title")}</h1>
        <p className="page-subtitle">{t("pairing.subtitle")}</p>
      </div>

      <div className="pairing-layout">
        <div className="card">
          <div className="card-header"><span className="card-title">{t("pairing.scanPair")}</span></div>
          {pairing?.code ? (
            <div className="qr-wrap">
              <div className="qr-frame">
                {pairing.qr_url ? <img src={pairing.qr_url} alt="QR" /> : <span className="text-3">QR</span>}
              </div>
              <div className="pairing-divider">{t("pairing.orEnterCode")}</div>
              <div className="pairing-code">{pairing.code}</div>
              <div className="pairing-meta">{pairing.expires_at ?? ""} {t("pairing.expires")} · <button className="link" onClick={onRefresh}>{t("pairing.refresh")}</button></div>
            </div>
          ) : (
            <div className="empty-state">{running ? t("pairing.generating") : t("pairing.agentNotRunning")}</div>
          )}
        </div>

        <div>
          <div className="card">
            <div className="card-header">
              <span className="card-title">{t("pairing.pendingRequests")}</span>
              {pendingPairing && <span className="badge pending">1</span>}
            </div>
            {pendingPairing ? (
              <div className="device-row">
                <div className="device-avatar"><PhoneIcon /></div>
                <div className="device-body">
                  <div className="device-name">{pendingPairing.name}</div>
                  <div className="device-sub">{pendingPairing.platform} · {t("pairing.justNow")}</div>
                </div>
                <div style={{ display: "flex", gap: 6 }}>
                  <button className="btn btn-primary btn-sm"><CheckIcon />{t("pairing.allow")}</button>
                  <button className="btn btn-secondary btn-sm"><XIcon />{t("pairing.deny")}</button>
                </div>
              </div>
            ) : (
              <div className="empty-state">{t("pairing.noPending")}</div>
            )}
          </div>

          <div className="card">
            <div className="card-header"><span className="card-title">{t("pairing.recent")}</span></div>
            <div className="empty-state">{t("pairing.noHistory")}</div>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ════════ Devices ════════ */

function DevicesSection({ devices, pendingPairing }: { devices: DeviceInfo[]; pendingPairing: PendingPairing | null }) {
  const [query, setQuery] = useState("");
  const filtered = devices.filter((d) => {
    if (!query) return true;
    const q = query.toLowerCase();
    return d.name?.toLowerCase().includes(q) || d.platform?.toLowerCase().includes(q) || d.id.toLowerCase().includes(q);
  });

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("nav.devices")}</h1>
        <p className="page-subtitle">{t("devices.subtitle")}</p>
      </div>

      <div className="toolbar">
        <div className="search-input">
          <SearchIcon />
          <input placeholder={t("devices.searchPlaceholder")} value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        <button className="btn btn-secondary btn-sm"><FileIcon />{t("devices.export")}</button>
        <button className="btn btn-danger btn-sm">{t("devices.batchRevoke")}</button>
      </div>

      <div className="card" style={{ padding: "16px 20px" }}>
        <table className="data-table">
          <thead>
            <tr>
              <th>{t("devices.colDevice")}</th>
              <th>{t("devices.colPlatform")}</th>
              <th>{t("devices.colFingerprint")}</th>
              <th>{t("devices.colState")}</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length > 0 ? filtered.map((d) => (
              <tr key={d.id}>
                <td>
                  <div className="table-device">
                    <div className="table-device-icon"><PhoneIcon /></div>
                    <div>
                      <div style={{ fontWeight: 600 }}>{d.name || d.id.slice(0, 8)}</div>
                      <div className="device-sub">{d.id.slice(0, 16)}</div>
                    </div>
                  </div>
                </td>
                <td className="mono">{d.platform || "—"}</td>
                <td className="mono fp-authorized">{d.id.slice(0, 12)}…</td>
                <td>
                  <span className={`badge ${d.state === "Authorized" ? "online" : "revoked"}`}>
                    <span className={`badge-dot ${d.state === "Authorized" ? "green" : "red"}`} />
                    {d.state === "Authorized" ? t("status.online") : t("devices.revoked")}
                  </span>
                </td>
              </tr>
            )) : (
              <tr><td colSpan={4}><div className="empty-state">{t("devices.empty")}</div></td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ════════ Settings ════════ */

function SettingsSection({ autoStart, setAutoStart }: { autoStart: boolean; setAutoStart: (v: boolean) => void }) {
  const [tab, setTab] = useState<SettingsTab>("general");
  const tabs: { key: SettingsTab; label: string }[] = [
    { key: "general", label: t("settings.general") },
    { key: "network", label: t("settings.network") },
    { key: "terminal", label: t("settings.terminal") },
    { key: "security", label: t("settings.security") },
    { key: "advanced", label: t("settings.advanced") },
    { key: "about", label: t("settings.about") },
  ];

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("nav.settings")}</h1>
        <p className="page-subtitle">{t("settings.subtitle")}</p>
      </div>

      <div className="settings-layout">
        <div className="settings-nav">
          {tabs.map((tb) => (
            <button key={tb.key} className={`settings-tab ${tab === tb.key ? "active" : ""}`} onClick={() => setTab(tb.key)}>{tb.label}</button>
          ))}
        </div>
        <div className="settings-panel">
          {tab === "general" && (
            <>
              <SettingRow label={t("settings.launchAtStartup")} hint={t("settings.launchHint")}>
                <div className={`toggle ${autoStart ? "on" : ""}`} onClick={() => setAutoStart(!autoStart)} />
              </SettingRow>
              <SettingRow label={t("settings.silentStart")} hint={t("settings.silentHint")}>
                <div className="toggle" />
              </SettingRow>
              <SettingRow label={t("settings.language")}>
                <select className="select-control" defaultValue="zh">
                  <option value="zh">简体中文</option>
                  <option value="en">English</option>
                </select>
              </SettingRow>
              <SettingRow label={t("settings.theme")}>
                <select className="select-control" defaultValue="dark">
                  <option value="dark">{t("settings.dark")}</option>
                  <option value="light">{t("settings.light")}</option>
                  <option value="auto">{t("settings.auto")}</option>
                </select>
              </SettingRow>
            </>
          )}
          {tab === "network" && (
            <>
              <SettingRow label={t("settings.listenAddr")} hint="127.0.0.1">
                <input className="input-control" defaultValue="127.0.0.1" readOnly />
              </SettingRow>
              <SettingRow label={t("settings.listenPort")} hint={t("settings.portDefault")}>
                <input className="input-control" defaultValue="8787" readOnly />
              </SettingRow>
              <SettingRow label={t("settings.autoPort")} hint={t("settings.autoPortHint")}>
                <div className="toggle on" />
              </SettingRow>
              <SettingRow label={t("settings.maxConns")}>
                <select className="select-control" defaultValue="10"><option>5</option><option>10</option><option>20</option></select>
              </SettingRow>
              <SettingRow label={t("settings.wsCompress")}>
                <div className="toggle on" />
              </SettingRow>
            </>
          )}
          {tab === "terminal" && (
            <>
              <SettingRow label={t("settings.defaultShell")}>
                <select className="select-control" defaultValue="auto"><option value="auto">{t("settings.autoDetect")}</option><option>powershell</option><option>bash</option><option>zsh</option></select>
              </SettingRow>
              <SettingRow label={t("settings.fontFamily")}>
                <select className="select-control" defaultValue="mono"><option value="mono">SF Mono</option><option>JetBrains Mono</option><option>Cascadia Code</option></select>
              </SettingRow>
              <SettingRow label={t("settings.fontSize")}>
                <input className="input-control" type="number" defaultValue={13} />
              </SettingRow>
              <SettingRow label={t("settings.cursorStyle")}>
                <select className="select-control" defaultValue="block"><option value="block">{t("settings.cursorBlock")}</option><option value="bar">{t("settings.cursorBar")}</option><option value="underline">{t("settings.cursorUnderline")}</option></select>
              </SettingRow>
              <SettingRow label={t("settings.scrollBuffer")}>
                <input className="input-control" type="number" defaultValue={10000} />
              </SettingRow>
            </>
          )}
          {tab === "security" && (
            <>
              <SettingRow label={t("settings.requireApproval")} hint={t("settings.requireApprovalHint")}>
                <div className="toggle on" />
              </SettingRow>
              <SettingRow label={t("settings.codeExpiry")}>
                <select className="select-control" defaultValue="10"><option>5</option><option>10</option><option>15</option></select>
              </SettingRow>
              <SettingRow label={t("settings.maxDevices")}>
                <input className="input-control" type="number" defaultValue={5} />
              </SettingRow>
            </>
          )}
          {tab === "advanced" && (
            <>
              <SettingRow label={t("settings.verboseLog")}><div className="toggle" /></SettingRow>
              <SettingRow label={t("settings.logLevel")}>
                <select className="select-control" defaultValue="info"><option value="debug">Debug</option><option value="info">Info</option><option value="warn">Warn</option></select>
              </SettingRow>
              <SettingRow label={t("settings.perfMonitor")}><div className="toggle" /></SettingRow>
              <SettingRow label={t("settings.crashReport")}><div className="toggle on" /></SettingRow>
              <SettingRow label={t("settings.experimental")}><div className="toggle" /></SettingRow>
            </>
          )}
          {tab === "about" && (
            <>
              <SettingRow label={t("settings.appVersion")}><span className="mono">1.0.0</span></SettingRow>
              <SettingRow label={t("settings.agentVersion")}><span className="mono">1.0.0</span></SettingRow>
              <SettingRow label={t("settings.protocolVer")}><span className="mono">v1</span></SettingRow>
              <SettingRow label={t("settings.shell")}><span className="mono">Tauri 2.x</span></SettingRow>
              <SettingRow label={t("settings.checkUpdate")}>
                <button className="btn btn-secondary btn-sm">{t("settings.checkUpdate")}</button>
              </SettingRow>
              <SettingRow label={t("settings.openSource")}>
                <button className="btn btn-secondary btn-sm">{t("settings.viewLicense")}</button>
              </SettingRow>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function SettingRow({ label, hint, children }: { label: string; hint?: string; children: ReactNode }) {
  return (
    <div className="setting-item">
      <div>
        <div className="setting-label">{label}</div>
        {hint && <div className="setting-hint">{hint}</div>}
      </div>
      {children}
    </div>
  );
}
