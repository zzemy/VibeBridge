import { useEffect, useState, useCallback, useRef } from "react";
import type { ReactNode } from "react";
import { invoke } from "@tauri-apps/api/core";
import { t, subscribeLang, setLang, getLang } from "./lib/i18n";

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

interface PairingStatus {
  state: string;
  flow_id?: string;
  display_name?: string;
  platform?: string;
  sas?: string;
  expires_at?: string;
}

interface PairingData {
  code?: string;
  qr_url?: string;
  target?: string;
  expires_at?: string;
}

type Section = "overview" | "pairing" | "devices" | "settings";
type SettingsTab = "general" | "network" | "security" | "about";

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
const CopyIcon = () => (<Svg size={16}><rect x="9" y="9" width="13" height="13" rx="2" /><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1" /></Svg>);
const ExternalIcon = () => (<Svg size={16}><path d="M15 3h6v6" /><path d="M10 14 21 3" /><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6" /></Svg>);
const CheckIcon = () => (<Svg size={14}><path d="M20 6 9 17l-5-5" /></Svg>);
const XIcon = () => (<Svg size={14}><path d="M18 6 6 18" /><path d="m6 6 12 12" /></Svg>);
const ActivityIcon = () => (<Svg size={16}><path d="M22 12h-4l-3 9L9 3l-3 9H2" /></Svg>);
const ShareIcon = () => (<Svg size={16}><circle cx="18" cy="5" r="3" /><circle cx="6" cy="12" r="3" /><circle cx="18" cy="19" r="3" /><path d="m8.59 13.51 6.83 3.98M15.41 6.51l-6.82 3.98" /></Svg>);
const ClockIcon = () => (<Svg size={16}><circle cx="12" cy="12" r="10" /><path d="M12 6v6l4 2" /></Svg>);
const RefreshIcon = () => (<Svg size={14}><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8M21 3v5h-5M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16M3 21v-5h5" /></Svg>);
const ShieldIcon = () => (<Svg size={16}><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></Svg>);
const FolderIcon = () => (<Svg size={16}><path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2z" /></Svg>);

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

function formatPairingCode(code?: string): string {
  if (!code) return "— — —";
  const clean = code.replace(/\s/g, "");
  if (clean.length === 6) return `${clean.slice(0, 3)} ${clean.slice(3)}`;
  return code;
}

/* ── Component ── */

export default function App() {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [pairing, setPairing] = useState<PairingData | null>(null);
  const [pairingStatus, setPairingStatus] = useState<PairingStatus | null>(null);
  const [section, setSection] = useState<Section>("overview");
  const [loading, setLoading] = useState(false);
  const [actionLoading, setActionLoading] = useState(false);
  const [autoStart, setAutoStart] = useState(true);
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

  const fetchPairingStatus = useCallback(async () => {
    try {
      const s = await invoke<PairingStatus>("get_pairing_status");
      setPairingStatus(s);
    } catch {
      setPairingStatus(null);
    }
  }, []);

  useEffect(() => {
    fetchStatus();
    const interval = setInterval(fetchStatus, 3000);
    return () => clearInterval(interval);
  }, [fetchStatus]);

  useEffect(() => {
    fetchPairingStatus();
    const interval = setInterval(fetchPairingStatus, 5000);
    return () => clearInterval(interval);
  }, [fetchPairingStatus]);

  const handleRestart = async () => {
    setLoading(true);
    try { await invoke("restart_agent"); setTimeout(() => fetchStatus(), 1000); } catch {}
    setLoading(false);
  };

  const fetchPairing = async () => {
    try { const data = await invoke<PairingData>("fetch_pairing_code"); setPairing(data); } catch { setPairing(null); }
  };

  useEffect(() => { if (section === "pairing") fetchPairing(); }, [section]);

  const handleApprove = async (flowId: string) => {
    setActionLoading(true);
    try { await invoke("approve_pairing", { flowId }); setPairingStatus(null); fetchStatus(); } catch {}
    setActionLoading(false);
  };

  const handleReject = async (flowId: string) => {
    setActionLoading(true);
    try { await invoke("reject_pairing", { flowId }); setPairingStatus(null); } catch {}
    setActionLoading(false);
  };

  const handleRevoke = async (deviceId: string) => {
    setActionLoading(true);
    try { await invoke("revoke_device", { deviceId }); fetchStatus(); } catch {}
    setActionLoading(false);
  };

  const info = status?.info ?? {};
  const devices: DeviceInfo[] = Array.isArray(info.devices) ? (info.devices as DeviceInfo[]) : [];
  const uptimeSec = Number(info.uptime_seconds ?? 0);
  const memMb = Number(info.memory_alloc_mb ?? 0);
  const goroutines = Number(info.goroutines ?? 0);
  const cpuCores = Number(info.cpu_cores ?? 0);
  const protocol = String(info.protocol ?? "—");
  const running = status?.running ?? false;
  const port = status?.port ?? 8787;
  const activeDevices = devices.filter((d) => d.state === "Authorized");
  const hasPending = pairingStatus && pairingStatus.state !== "idle" && pairingStatus.flow_id;

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
          <div className="sidebar-logo-wrap"><Lightning /></div>
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
            <div className="user-avatar"><Lightning /></div>
            <div className="user-info">
              <div className="user-name">{t("sidebar.thisDevice")}</div>
              <div className="user-meta">{running ? t("sidebar.agentRunning") : t("sidebar.agentStopped")}</div>
            </div>
            <span className="nav-icon user-settings-btn" onClick={() => setSection("settings")}>
              <Svg size={16}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></Svg>
            </span>
          </div>
        </div>
      </aside>

      <main className="main-content">
        {section === "overview" && <OverviewSection running={running} port={port} uptimeSec={uptimeSec} memMb={memMb} goroutines={goroutines} cpuCores={cpuCores} protocol={protocol} activeDevices={activeDevices} loading={loading} onRestart={handleRestart} />}
        {section === "pairing" && <PairingSection running={running} pairing={pairing} pairingStatus={pairingStatus} hasPending={!!hasPending} actionLoading={actionLoading} onRefresh={fetchPairing} onApprove={handleApprove} onReject={handleReject} />}
        {section === "devices" && <DevicesSection devices={devices} actionLoading={actionLoading} onRevoke={handleRevoke} onGoPairing={() => setSection("pairing")} />}
        {section === "settings" && <SettingsSection autoStart={autoStart} setAutoStart={setAutoStart} port={port} protocol={protocol} memMb={memMb} goroutines={goroutines} cpuCores={cpuCores} />}
      </main>
    </div>
  );
}

/* ════════ Overview ════════ */

function OverviewSection({ running, port, uptimeSec, memMb, goroutines, cpuCores, protocol, activeDevices, loading, onRestart }: {
  running: boolean; port: number; uptimeSec: number; memMb: number; goroutines: number; cpuCores: number; protocol: string;
  activeDevices: DeviceInfo[]; loading: boolean; onRestart: () => void;
}) {
  const stats = [
    { label: t("stats.runtime"), value: running ? t("stats.running") : t("stats.stopped"), delta: running ? `已运行 ${formatUptime(uptimeSec)}` : "—", icon: <ActivityIcon />, deltaClass: running ? "up" : "neutral" },
    { label: t("stats.connectedDevices"), value: String(activeDevices.length), delta: activeDevices.length > 0 ? `${activeDevices.length} 台已授权` : "暂无", icon: <PhoneIcon />, deltaClass: activeDevices.length > 0 ? "up" : "neutral" },
    { label: t("stats.goroutines"), value: running ? String(goroutines) : "—", delta: running ? `${cpuCores} 核心` : "—", icon: <ShareIcon />, deltaClass: running ? "up" : "neutral" },
    { label: t("stats.memory"), value: running ? (memMb ? `${memMb} MB` : "—") : "—", delta: running ? t("stats.alloc") : "—", icon: <ActivityIcon />, deltaClass: running ? "up" : "neutral" },
  ];

  const memPercent = memMb ? Math.min(100, (memMb / 4096) * 100) : 0;

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("nav.overview")}</h1>
        <p className="page-subtitle">{t("overview.subtitle")}</p>
      </div>

      <div className="card hero-card-wrap">
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
              {t("status.port")} <span className="mono">{port}</span> · {t("status.protocol")} <span className="mono">{protocol}</span> · {running ? `已连续运行 ${formatUptime(uptimeSec)}` : t("overview.agentStopped")}
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

      <div className="stat-grid">
        {stats.map((s, i) => (
          <div key={i} className="stat-card">
            <div className="stat-top">
              <span className="stat-label">{s.label}</span>
              <span className="stat-icon">{s.icon}</span>
            </div>
            <div className="stat-value">{s.value}</div>
            <div className={`stat-delta ${s.deltaClass}`}>
              {s.deltaClass === "up" && <span className="delta-arrow">↑</span>}
              {s.delta}
            </div>
          </div>
        ))}
      </div>

      <div className="two-col">
        <div className="card">
          <div className="card-header">
            <span className="card-title">{t("overview.resourceUsage")}</span>
            <span className="badge info">{t("overview.realtime")}</span>
          </div>
          {running ? (
            <>
              <div className="resource-row">
                <span className="resource-label"><ActivityIcon /> {t("stats.goroutines")}</span>
                <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${Math.min(100, goroutines)}%` }} /></div>
                <span className="resource-value">{goroutines}</span>
              </div>
              <div className="resource-row">
                <span className="resource-label"><ActivityIcon /> {t("overview.memory")}</span>
                <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${memPercent}%` }} /></div>
                <span className="resource-value">{memMb ? `${memMb} MB` : "—"}</span>
              </div>
              <div className="resource-row">
                <span className="resource-label"><ActivityIcon /> CPU {t("stats.total")}</span>
                <span className="resource-value mono">{cpuCores} cores</span>
              </div>
            </>
          ) : (
            <div className="empty-state">{t("overview.agentStopped")}</div>
          )}
        </div>

        <div className="card">
          <div className="card-header"><span className="card-title">{t("overview.quickActions")}</span></div>
          <div className="action-row">
            <div className="action-icon"><CopyIcon /></div>
            <div className="action-body">
              <div className="action-label">{t("overview.copyAddr")}</div>
              <div className="action-sub mono">ws://127.0.0.1:{port}</div>
            </div>
            <button className="btn btn-ghost btn-sm" onClick={() => {
              try { navigator.clipboard.writeText(`ws://127.0.0.1:${port}`); } catch {}
            }}><CopyIcon /></button>
          </div>
          <div className="action-row">
            <div className="action-icon"><ExternalIcon /></div>
            <div className="action-body">
              <div className="action-label">{t("overview.openWeb")}</div>
              <div className="action-sub mono">http://127.0.0.1:{port}</div>
            </div>
            <button className="btn btn-ghost btn-sm" disabled={!running} onClick={() => {
              try { invoke("open_url", { url: `http://127.0.0.1:${port}` }); } catch {}
            }}><ExternalIcon /></button>
          </div>
        </div>
      </div>

      <div className="card">
        <div className="card-header">
          <span className="card-title">{t("overview.connectedDevices")}</span>
        </div>
        {activeDevices.length > 0 ? activeDevices.map((d) => (
          <div key={d.id} className="device-row">
            <div className="device-avatar"><PhoneIcon /></div>
            <div className="device-body">
              <div className="device-name">{d.name || "—"}</div>
              <div className="device-sub">{d.platform || "—"}</div>
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

function PairingSection({ running, pairing, pairingStatus, hasPending, actionLoading, onRefresh, onApprove, onReject }: {
  running: boolean; pairing: PairingData | null; pairingStatus: PairingStatus | null; hasPending: boolean; actionLoading: boolean;
  onRefresh: () => void; onApprove: (flowId: string) => void; onReject: (flowId: string) => void;
}) {
  const [countdown, setCountdown] = useState(0);

  useEffect(() => {
    if (pairing?.expires_at) {
      const target = new Date(`1970-01-01T${pairing.expires_at}Z`).getTime();
      if (!isNaN(target)) {
        const update = () => {
          const remaining = Math.max(0, Math.floor((target - Date.now()) / 1000));
          setCountdown(remaining);
        };
        update();
        const timer = setInterval(update, 1000);
        return () => clearInterval(timer);
      }
    }
  }, [pairing]);

  const code = pairing?.code;

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("pairing.title")}</h1>
        <p className="page-subtitle">{t("pairing.subtitle")}</p>
      </div>

      <div className="pairing-layout">
        <div className="card pairing-qr-card">
          <div className="card-header"><span className="card-title">{t("pairing.scanPair")}</span></div>
          <div className="qr-wrap">
            <div className="qr-frame">
              {!running ? (
                <div className="empty-state">{t("pairing.agentNotRunning")}</div>
              ) : pairing?.qr_url ? (
                <img src={pairing.qr_url} alt="QR" />
              ) : (
                <div className="empty-state">{t("pairing.generating")}</div>
              )}
            </div>
            <div className="pairing-divider">{t("pairing.orEnterCode")}</div>
            <div className="pairing-code">{formatPairingCode(code)}</div>
            <div className="pairing-meta">
              {countdown > 0 && <><ClockIcon /> <span className="countdown">{countdown}{t("pairing.expires")}</span> · </>}
              <button className="link" onClick={onRefresh} disabled={!running}><RefreshIcon /> {t("pairing.refresh")}</button>
            </div>
          </div>
        </div>

        <div className="pairing-right">
          <div className="card">
            <div className="card-header">
              <span className="card-title">{t("pairing.pendingRequests")}</span>
              {hasPending && <span className="badge pending">1</span>}
            </div>
            {hasPending && pairingStatus?.flow_id ? (
              <div className="device-row">
                <div className="device-avatar"><PhoneIcon /></div>
                <div className="device-body">
                  <div className="device-name">{pairingStatus.display_name || "—"}</div>
                  <div className="device-sub">{pairingStatus.platform || "—"} · {pairingStatus.sas || ""}</div>
                </div>
                <div className="action-pair">
                  <button className="btn btn-primary btn-sm" disabled={actionLoading} onClick={() => onApprove(pairingStatus.flow_id!)}><CheckIcon /> {t("pairing.allow")}</button>
                  <button className="btn btn-danger btn-sm" disabled={actionLoading} onClick={() => onReject(pairingStatus.flow_id!)}><XIcon /> {t("pairing.deny")}</button>
                </div>
              </div>
            ) : (
              <div className="empty-state">{t("pairing.noPending")}</div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}

/* ════════ Devices ════════ */

function DevicesSection({ devices, actionLoading, onRevoke, onGoPairing }: {
  devices: DeviceInfo[]; actionLoading: boolean; onRevoke: (deviceId: string) => void; onGoPairing: () => void;
}) {
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
        <span className="device-count">{devices.length} {t("stats.total")}</span>
      </div>

      {filtered.length > 0 ? (
        <div className="card" style={{ padding: 0, overflow: "hidden" }}>
          <table className="data-table">
            <thead>
              <tr>
                <th>{t("devices.colDevice")}</th>
                <th>{t("devices.colPlatform")}</th>
                <th>{t("devices.colFingerprint")}</th>
                <th>{t("devices.colState")}</th>
                <th> </th>
              </tr>
            </thead>
            <tbody>
              {filtered.map((d) => (
                <tr key={d.id}>
                  <td>
                    <div className="table-device">
                      <div className="table-device-icon"><PhoneIcon /></div>
                      <div><div className="table-device-name">{d.name || "—"}</div></div>
                    </div>
                  </td>
                  <td className="mono">{d.platform || "—"}</td>
                  <td className="mono fp-authorized">{d.id.slice(0, 12)}…</td>
                  <td>
                    <span className={`badge ${d.state === "Authorized" ? "online" : "offline"}`}>
                      <span className={`badge-dot ${d.state === "Authorized" ? "green" : "gray"}`} />
                      {d.state === "Authorized" ? t("devices.connected") : t("devices.revoked")}
                    </span>
                  </td>
                  <td>
                    {d.state === "Authorized" && (
                      <button className="btn btn-danger-ghost btn-sm" disabled={actionLoading} onClick={() => onRevoke(d.id)}>{t("devices.revoked")}</button>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : (
        <div className="card">
          <div className="empty-state">{t("devices.empty")}</div>
          <div style={{ textAlign: "center", marginTop: "12px" }}>
            <button className="btn btn-primary btn-sm" onClick={onGoPairing}>{t("devices.pairNew")}</button>
          </div>
          <div className="action-sub" style={{ textAlign: "center", marginTop: "8px" }}>{t("devices.pairHint")}</div>
        </div>
      )}
    </div>
  );
}

function SearchIcon() {
  return (<Svg size={16}><circle cx="11" cy="11" r="8" /><path d="m21 21-4.35-4.35" /></Svg>);
}

/* ════════ Settings ════════ */

function SettingsSection({ autoStart, setAutoStart, port, protocol, memMb, goroutines, cpuCores }: {
  autoStart: boolean; setAutoStart: (v: boolean) => void; port: number; protocol: string; memMb: number; goroutines: number; cpuCores: number;
}) {
  const [tab, setTab] = useState<SettingsTab>("general");
  const tabs: { key: SettingsTab; label: string }[] = [
    { key: "general", label: t("settings.general") },
    { key: "network", label: t("settings.network") },
    { key: "security", label: t("settings.security") },
    { key: "about", label: t("settings.about") },
  ];

  const handleAutoStart = async (v: boolean) => {
    setAutoStart(v);
    try { await invoke("toggle_autostart", { enabled: v }); } catch {}
  };

  const handleLang = (lang: string) => { setLang(lang as "zh" | "en"); };

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
                <Toggle on={autoStart} onClick={() => handleAutoStart(!autoStart)} />
              </SettingRow>
              <SettingRow label={t("settings.language")} hint={t("settings.language")}>
                <select className="select-control" defaultValue={getLang()} onChange={(e) => handleLang(e.target.value)}>
                  <option value="zh">简体中文</option>
                  <option value="en">English</option>
                </select>
              </SettingRow>
            </>
          )}
          {tab === "network" && (
            <>
              <SettingRow label={t("settings.listenPort")} hint={t("settings.portDefault")}>
                <input className="input-control mono" value={String(port)} readOnly />
              </SettingRow>
              <SettingRow label={t("settings.protocolVer")} hint={t("status.protocol")}>
                <span className="badge info-tag">{protocol}</span>
              </SettingRow>
            </>
          )}
          {tab === "security" && (
            <>
              <SettingRow label={t("overview.resourceUsage")} hint="端到端加密保护所有传输数据">
                <span className="badge online"><span className="badge-dot green" />{t("devices.connected")}</span>
              </SettingRow>
              <SettingRow label={t("settings.requireApproval")} hint={t("settings.requireApprovalHint")}>
                <Toggle on={true} onClick={() => {}} />
              </SettingRow>
              <SettingRow label="身份存储" hint="本地密钥与身份信息存储目录">
                <button className="btn btn-secondary btn-sm" onClick={() => { try { invoke("open_identity_dir"); } catch {} }}><FolderIcon /> 打开</button>
              </SettingRow>
            </>
          )}
          {tab === "about" && (
            <>
              <SettingRow label={t("settings.appVersion")} hint="桌面端">
                <span className="mono">1.0.0</span>
              </SettingRow>
              <SettingRow label={t("settings.protocolVer")} hint="WebSocket">
                <span className="mono">{protocol}</span>
              </SettingRow>
              <SettingRow label={t("stats.goroutines")} hint="Agent 实时">
                <span className="mono">{goroutines}</span>
              </SettingRow>
              <SettingRow label={t("stats.memory")} hint="Agent 实时">
                <span className="mono">{memMb ? `${memMb} MB` : "—"}</span>
              </SettingRow>
              <SettingRow label={t("settings.openSource")} hint={t("settings.viewLicense")}>
                <button className="btn btn-secondary btn-sm" onClick={() => { try { invoke("open_url", { url: "https://github.com/zzemy/VibeBridge" }); } catch {} }}>{t("settings.viewLicense")}</button>
              </SettingRow>
            </>
          )}
        </div>
      </div>
    </div>
  );
}

function Toggle({ on, onClick }: { on: boolean; onClick: () => void }) {
  return <div className={`toggle ${on ? "on" : ""}`} onClick={onClick} />;
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
