import { useEffect, useState, useCallback, useRef } from "react";
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
  paired_at?: string;
  last_seen?: string;
  owner?: string;
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
  expires_in?: number;
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
const DownloadIcon = () => (<Svg size={16}><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4M7 10l5 5 5-5M12 15V3" /></Svg>);
const ShieldIcon = () => (<Svg size={16}><path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" /></Svg>);
const FolderIcon = () => (<Svg size={16}><path d="M4 20h16a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.93a2 2 0 0 1-1.66-.9l-.82-1.2A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13c0 1.1.9 2 2 2z" /></Svg>);
const RefreshIcon = () => (<Svg size={14}><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8M21 3v5h-5M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16M3 21v-5h5" /></Svg>);
const StopIcon = () => (<Svg size={14}><rect x="5" y="5" width="14" height="14" rx="2" /></Svg>);

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

/* ── Demo data fallback (when agent not running) ── */

const DEMO_DEVICES: DeviceInfo[] = [
  { id: "a3f27b9c1234abcd", name: "iPhone 15 Pro", platform: "iOS 17.5", state: "Authorized", paired_at: "2024-07-15", last_seen: "刚刚", owner: "郑昊" },
  { id: "7de14a2f5678efgh", name: "iPad Air", platform: "iPadOS 17.5", state: "Authorized", paired_at: "2024-07-20", last_seen: "5分钟前", owner: "郑昊" },
  { id: "2b5a9f3c9012ijkl", name: "Work Laptop", platform: "macOS 14.5", state: "Authorized", paired_at: "2024-06-10", last_seen: "12分钟前", owner: "郑昊" },
  { id: "9c4d2a8f3456mnop", name: "Old Phone", platform: "Android 13", state: "Revoked", paired_at: "2024-03-22", last_seen: "2周前", owner: "郑昊" },
  { id: "1e6b3d9a7890qrst", name: "Test Device", platform: "iOS 17.4", state: "Revoked", paired_at: "2024-05-01", last_seen: "1个月前", owner: "测试账号" },
];

const DEMO_PENDING: PendingPairing[] = [
  { name: "iPhone 15 Pro", platform: "847293", sas: "A3:F2:7B:9C" },
  { name: "iPad Air", platform: "392147", sas: "7D:E1:4A:2F" },
];

/* ── Component ── */

export default function App() {
  const [status, setStatus] = useState<AgentStatus | null>(null);
  const [pairing, setPairing] = useState<PairingData | null>(null);
  const [section, setSection] = useState<Section>("overview");
  const [loading, setLoading] = useState(false);
  const [autoStart, setAutoStart] = useState(true);
  const [autoUpdate, setAutoUpdate] = useState(false);
  const [e2ee] = useState(true);
  const [requireConfirm, setRequireConfirm] = useState(true);
  const [usageStats, setUsageStats] = useState(false);
  const [debugMode, setDebugMode] = useState(false);
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
  const realDevices: DeviceInfo[] = Array.isArray(info.devices) ? (info.devices as DeviceInfo[]) : [];
  const devices = realDevices.length > 0 ? realDevices : DEMO_DEVICES;
  const pendingPairingList: PendingPairing[] = info.pending_pairing
    ? [info.pending_pairing as PendingPairing]
    : DEMO_PENDING;
  const activeDevices = devices.filter((d) => d.state === "Authorized");
  const uptimeSec = Number(info.uptime_seconds ?? 0);
  const memMb = Number(info.memory_alloc_mb ?? 0);
  const goroutines = Number(info.goroutines ?? 0);
  const cpuCores = Number(info.cpu_cores ?? 0);
  const running = status?.running ?? false;
  const port = status?.port ?? 8787;

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
            <div className="user-avatar">郑</div>
            <div className="user-info">
              <div className="user-name">郑昊</div>
              <div className="user-meta">MacBook Pro 16"</div>
            </div>
            <span className="nav-icon user-settings-btn" onClick={() => setSection("settings")}>
              <Svg size={16}><circle cx="12" cy="12" r="3" /><path d="M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06a1.65 1.65 0 0 0 .33-1.82 1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06a1.65 1.65 0 0 0 1.82.33H9a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06a1.65 1.65 0 0 0-.33 1.82V9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z" /></Svg>
            </span>
          </div>
        </div>
      </aside>

      <main className="main-content">
        {section === "overview" && <OverviewSection running={running} port={port} uptimeSec={uptimeSec} memMb={memMb} goroutines={goroutines} cpuCores={cpuCores} activeDevices={activeDevices} loading={loading} onRestart={handleRestart} />}
        {section === "pairing" && <PairingSection running={running} pairing={pairing} pendingList={pendingPairingList} onRefresh={fetchPairing} />}
        {section === "devices" && <DevicesSection devices={devices} />}
        {section === "settings" && <SettingsSection autoStart={autoStart} setAutoStart={setAutoStart} autoUpdate={autoUpdate} setAutoUpdate={setAutoUpdate} e2ee={e2ee} requireConfirm={requireConfirm} setRequireConfirm={setRequireConfirm} usageStats={usageStats} setUsageStats={setUsageStats} debugMode={debugMode} setDebugMode={setDebugMode} />}
      </main>
    </div>
  );
}

/* ════════ Overview ════════ */

function OverviewSection({ running, port, uptimeSec, memMb, goroutines, cpuCores, activeDevices, loading, onRestart }: {
  running: boolean; port: number; uptimeSec: number; memMb: number; goroutines: number; cpuCores: number;
  activeDevices: DeviceInfo[]; loading: boolean; onRestart: () => void;
}) {
  const stats = [
    { label: t("stats.runtime"), value: running ? t("stats.running") : t("stats.stopped"), delta: running ? `已运行 ${formatUptime(uptimeSec)}` : "—", icon: <ActivityIcon />, deltaClass: running ? "up" : "neutral" },
    { label: t("stats.connectedDevices"), value: String(activeDevices.length), delta: "较昨日 +1", icon: <PhoneIcon />, deltaClass: "up" },
    { label: "今日会话", value: "27", delta: "较昨日 +5", icon: <ShareIcon />, deltaClass: "up" },
    { label: "数据传输", value: "1.2 GB", delta: "较昨日 +12%", icon: <WifiIcon />, deltaClass: "up" },
  ];

  const memPercent = Math.min(100, (memMb / 4096) * 100) || 3.6;
  const cpuPercent = running ? 23 : 0;
  const diskPercent = 67;
  const netPercent = running ? 24 : 0;

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">{t("nav.overview")}</h1>
        <p className="page-subtitle">{t("overview.subtitle")}</p>
      </div>

      {/* Hero status card */}
      <div className="card hero-card-wrap">
        <div className="hero-card">
          <div className="hero-icon"><Lightning /></div>
          <div className="hero-body">
            <div className="hero-title-row">
              <span className="hero-title">{running ? "Agent 运行正常" : "Agent 未运行"}</span>
              <span className={`badge ${running ? "online" : "offline"}`}>
                <span className={`badge-dot ${running ? "green" : "red"}`} />
                {running ? "在线" : "离线"}
              </span>
            </div>
            <div className="hero-meta">
              端口 <span className="mono">{port}</span> · 协议 <span className="mono">v2.1</span> · {running ? `已连续运行 ${formatUptime(uptimeSec)}` : "未启动"}
            </div>
          </div>
          <div className="hero-actions">
            <button className="btn btn-secondary btn-sm" onClick={onRestart} disabled={loading}>
              {loading && <span className="spinner" />}
              {loading ? "重启中…" : "重启"}
            </button>
            <button className="btn btn-danger-ghost btn-sm">
              <StopIcon /> 停止
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
            <div className={`stat-delta ${s.deltaClass}`}>
              {s.deltaClass === "up" && <span className="delta-arrow">↑</span>}
              {s.delta}
            </div>
          </div>
        ))}
      </div>

      {/* Resource + Quick actions */}
      <div className="two-col">
        <div className="card">
          <div className="card-header">
            <span className="card-title">资源使用</span>
            <span className="badge info">实时</span>
          </div>
          <div className="resource-row">
            <span className="resource-label"><CpuIcon /> CPU</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${cpuPercent}%` }} /></div>
            <span className="resource-value">{cpuPercent}%</span>
          </div>
          <div className="resource-row">
            <span className="resource-label"><MemoryIcon /> 内存</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${memPercent}%` }} /></div>
            <span className="resource-value">{memMb ? `${memMb}MB` : "3.6G"}</span>
          </div>
          <div className="resource-row">
            <span className="resource-label"><DiskIcon /> 磁盘</span>
            <div className="resource-bar"><div className="resource-bar-fill yellow" style={{ width: `${diskPercent}%` }} /></div>
            <span className="resource-value">{diskPercent}%</span>
          </div>
          <div className="resource-row">
            <span className="resource-label"><WifiIcon /> 网络</span>
            <div className="resource-bar"><div className="resource-bar-fill green" style={{ width: `${netPercent}%` }} /></div>
            <span className="resource-value">{running ? "2.4M" : "—"}</span>
          </div>
        </div>

        <div className="card">
          <div className="card-header"><span className="card-title">快捷操作</span></div>
          <div className="action-row">
            <div className="action-icon"><CopyIcon /></div>
            <div className="action-body">
              <div className="action-label">复制连接地址</div>
              <div className="action-sub mono">ws://localhost:{port}</div>
            </div>
            <button className="btn btn-ghost btn-sm" onClick={() => {
              try { navigator.clipboard.writeText(`ws://localhost:${port}`); } catch {}
            }}><CopyIcon /></button>
          </div>
          <div className="action-row">
            <div className="action-icon"><ExternalIcon /></div>
            <div className="action-body">
              <div className="action-label">打开 Web 端</div>
              <div className="action-sub mono">http://127.0.0.1:{port}</div>
            </div>
            <button className="btn btn-ghost btn-sm" onClick={() => {
              try { invoke("open_url", { url: `http://127.0.0.1:${port}` }); } catch {}
            }}><ExternalIcon /></button>
          </div>
          <div className="action-row">
            <div className="action-icon"><FileIcon /></div>
            <div className="action-body">
              <div className="action-label">查看日志</div>
              <div className="action-sub mono">/var/log/vibebridge</div>
            </div>
            <button className="btn btn-ghost btn-sm"><FileIcon /></button>
          </div>
        </div>
      </div>

      {/* Connected devices */}
      <div className="card">
        <div className="card-header">
          <span className="card-title">已连接设备</span>
          <button className="link">查看全部</button>
        </div>
        {activeDevices.length > 0 ? activeDevices.map((d) => (
          <div key={d.id} className="device-row">
            <div className="device-avatar"><PhoneIcon /></div>
            <div className="device-body">
              <div className="device-name">{d.name}</div>
              <div className="device-sub">{d.platform}</div>
            </div>
            <span className="badge online"><span className="badge-dot green" />在线</span>
            <button className="btn btn-ghost btn-sm"><XIcon /></button>
          </div>
        )) : (
          <div className="empty-state">暂无已连接设备</div>
        )}
      </div>
    </div>
  );
}

/* ════════ Pairing ════════ */

function PairingSection({ running, pairing, pendingList, onRefresh }: {
  running: boolean; pairing: PairingData | null;
  pendingList: PendingPairing[]; onRefresh: () => void;
}) {
  const [countdown, setCountdown] = useState(24);

  useEffect(() => {
    if (pairing?.expires_in) setCountdown(pairing.expires_in);
    const timer = setInterval(() => {
      setCountdown((c) => (c > 0 ? c - 1 : 0));
    }, 1000);
    return () => clearInterval(timer);
  }, [pairing]);

  const code = pairing?.code || "847293";

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">配对管理</h1>
        <p className="page-subtitle">通过扫码或配对码连接新设备</p>
      </div>

      <div className="pairing-layout">
        <div className="card pairing-qr-card">
          <div className="card-header"><span className="card-title">扫码配对</span></div>
          <div className="qr-wrap">
            <div className="qr-frame">
              {pairing?.qr_url ? <img src={pairing.qr_url} alt="QR" /> : (
                <div className="qr-placeholder">
                  <div className="qr-grid">
                    {Array.from({ length: 49 }).map((_, i) => (
                      <div key={i} className={`qr-cell ${Math.random() > 0.45 ? "on" : ""}`} />
                    ))}
                  </div>
                </div>
              )}
            </div>
            <div className="pairing-divider">或手动输入配对码</div>
            <div className="pairing-code">{formatPairingCode(code)}</div>
            <div className="pairing-meta">
              <ClockIcon /> <span className="countdown">{countdown}秒</span> 后过期 ·
              <button className="link" onClick={onRefresh}><RefreshIcon /> 刷新</button>
            </div>
          </div>
        </div>

        <div className="pairing-right">
          <div className="card">
            <div className="card-header">
              <span className="card-title">待处理请求</span>
              {pendingList.length > 0 && <span className="badge pending">{pendingList.length}</span>}
            </div>
            {pendingList.length > 0 ? pendingList.map((p, i) => (
              <div key={i} className="device-row">
                <div className="device-avatar"><PhoneIcon /></div>
                <div className="device-body">
                  <div className="device-name">{p.name} {p.platform}</div>
                  <div className="device-sub">郑昊 · {p.sas} · {i === 0 ? "1分钟前" : "3分钟前"}</div>
                </div>
                <div className="action-pair">
                  <button className="btn btn-primary btn-sm"><CheckIcon /> 允许</button>
                  <button className="btn btn-danger btn-sm"><XIcon /> 拒绝</button>
                </div>
              </div>
            )) : (
              <div className="empty-state">暂无待处理请求</div>
            )}
          </div>

          <div className="card">
            <div className="card-header"><span className="card-title">最近配对</span></div>
            <div className="recent-pairing-row">
              <div className="device-avatar sm"><PhoneIcon /></div>
              <div className="device-body">
                <div className="device-name">Work Laptop</div>
              </div>
              <span className="badge online"><span className="badge-dot green" />已配对</span>
              <span className="time-stamp">今天 10:24</span>
            </div>
            <div className="recent-pairing-row">
              <div className="device-avatar sm"><PhoneIcon /></div>
              <div className="device-body">
                <div className="device-name">Android Tablet</div>
              </div>
              <span className="badge online"><span className="badge-dot green" />已配对</span>
              <span className="time-stamp">昨天 16:48</span>
            </div>
            <div className="recent-pairing-row">
              <div className="device-avatar sm"><PhoneIcon /></div>
              <div className="device-body">
                <div className="device-name">Unknown Device</div>
              </div>
              <span className="badge revoked"><span className="badge-dot red" />已拒绝</span>
              <span className="time-stamp">昨天 09:12</span>
            </div>
          </div>
        </div>
      </div>
    </div>
  );
}

/* ════════ Devices ════════ */

function DevicesSection({ devices }: { devices: DeviceInfo[] }) {
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const filtered = devices.filter((d) => {
    if (!query) return true;
    const q = query.toLowerCase();
    return d.name?.toLowerCase().includes(q) || d.platform?.toLowerCase().includes(q) || d.id.toLowerCase().includes(q);
  });

  const toggleSelect = (id: string) => {
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id); else next.add(id);
      return next;
    });
  };

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">设备管理</h1>
        <p className="page-subtitle">管理所有已授权的连接设备</p>
      </div>

      <div className="toolbar">
        <div className="search-input">
          <SearchIcon />
          <input placeholder="搜索设备名称、指纹..." value={query} onChange={(e) => setQuery(e.target.value)} />
        </div>
        <span className="device-count">共 {devices.length} 台设备</span>
        <button className="btn btn-secondary btn-sm"><DownloadIcon /> 导出</button>
        <button className="btn btn-danger btn-sm">批量撤销</button>
      </div>

      <div className="card" style={{ padding: 0, overflow: "hidden" }}>
        <table className="data-table">
          <thead>
            <tr>
              <th className="col-check"><input type="checkbox" /></th>
              <th>设备名</th>
              <th>所属用户</th>
              <th>系统版本</th>
              <th>公钥指纹</th>
              <th>配对时间</th>
              <th>最后在线</th>
              <th>状态</th>
              <th>操作</th>
            </tr>
          </thead>
          <tbody>
            {filtered.length > 0 ? filtered.map((d) => (
              <tr key={d.id}>
                <td className="col-check"><input type="checkbox" checked={selected.has(d.id)} onChange={() => toggleSelect(d.id)} /></td>
                <td>
                  <div className="table-device">
                    <div className="table-device-icon"><PhoneIcon /></div>
                    <div>
                      <div className="table-device-name">{d.name}</div>
                    </div>
                  </div>
                </td>
                <td>{d.owner || "—"}</td>
                <td className="mono">{d.platform || "—"}</td>
                <td className="mono fp-authorized">{d.id.slice(0, 12)}…</td>
                <td>{d.paired_at || "—"}</td>
                <td>{d.last_seen || "—"}</td>
                <td>
                  <span className={`badge ${d.state === "Authorized" ? "online" : "offline"}`}>
                    <span className={`badge-dot ${d.state === "Authorized" ? "green" : "gray"}`} />
                    {d.state === "Authorized" ? "在线" : "离线"}
                  </span>
                </td>
                <td><button className="btn btn-danger-ghost btn-sm">撤销</button></td>
              </tr>
            )) : (
              <tr><td colSpan={9}><div className="empty-state">暂无设备</div></td></tr>
            )}
          </tbody>
        </table>
      </div>
    </div>
  );
}

/* ════════ Settings ════════ */

function SettingsSection({ autoStart, setAutoStart, autoUpdate, setAutoUpdate, e2ee, requireConfirm, setRequireConfirm, usageStats, setUsageStats, debugMode, setDebugMode }: {
  autoStart: boolean; setAutoStart: (v: boolean) => void;
  autoUpdate: boolean; setAutoUpdate: (v: boolean) => void;
  e2ee: boolean; requireConfirm: boolean; setRequireConfirm: (v: boolean) => void;
  usageStats: boolean; setUsageStats: (v: boolean) => void;
  debugMode: boolean; setDebugMode: (v: boolean) => void;
}) {
  const [tab, setTab] = useState<SettingsTab>("general");
  const tabs: { key: SettingsTab; label: string }[] = [
    { key: "general", label: "通用" },
    { key: "network", label: "网络" },
    { key: "terminal", label: "终端" },
    { key: "security", label: "安全" },
    { key: "advanced", label: "高级" },
    { key: "about", label: "关于" },
  ];

  return (
    <div className="fade-in">
      <div className="page-header">
        <h1 className="page-title">设置</h1>
        <p className="page-subtitle">配置 VibeBridge Agent 与应用偏好</p>
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
              <SettingRow label="开机自启动" hint="登录系统时自动启动 VibeBridge Agent">
                <Toggle on={autoStart} onClick={() => setAutoStart(!autoStart)} />
              </SettingRow>
              <SettingRow label="语言" hint="调整界面显示语言">
                <select className="select-control" defaultValue="zh">
                  <option value="zh">简体中文</option>
                  <option value="en">English</option>
                </select>
              </SettingRow>
              <SettingRow label="主题" hint="调整界面外观主题">
                <select className="select-control" defaultValue="auto">
                  <option value="auto">跟随系统</option>
                  <option value="dark">深色</option>
                  <option value="light">浅色</option>
                </select>
              </SettingRow>
              <SettingRow label="自动检查更新" hint="有新版本时自动推送通知">
                <Toggle on={autoUpdate} onClick={() => setAutoUpdate(!autoUpdate)} />
              </SettingRow>
            </>
          )}
          {tab === "network" && (
            <>
              <SettingRow label="监听端口" hint="Agent WebSocket 服务监听端口">
                <input className="input-control mono" defaultValue="8080" readOnly />
              </SettingRow>
              <SettingRow label="中继服务器" hint="用于穿透连接的中继服务地址">
                <input className="input-control mono" defaultValue="relay.vibebridge.io" readOnly />
              </SettingRow>
              <SettingRow label="中继签发密钥" hint="用于验证中继服务身份的公钥">
                <button className="btn btn-secondary btn-sm">查看</button>
              </SettingRow>
              <SettingRow label="协议版本" hint="当前使用的 WebSocket 通信协议">
                <span className="badge info-tag">v2.1</span>
              </SettingRow>
            </>
          )}
          {tab === "terminal" && (
            <>
              <SettingRow label="默认 Shell" hint="新会话使用的默认 shell 程序">
                <select className="select-control" defaultValue="/bin/zsh">
                  <option value="/bin/zsh">/bin/zsh</option>
                  <option value="/bin/bash">/bin/bash</option>
                  <option value="powershell">PowerShell</option>
                </select>
              </SettingRow>
              <SettingRow label="工作目录" hint="新会话的起始工作目录">
                <input className="input-control mono" defaultValue="~/projects" />
              </SettingRow>
              <SettingRow label="终端字体" hint="终端显示使用的等宽字体">
                <select className="select-control" defaultValue="JetBrains Mono">
                  <option>JetBrains Mono</option>
                  <option>SF Mono</option>
                  <option>Cascadia Code</option>
                  <option>Fira Code</option>
                </select>
              </SettingRow>
              <SettingRow label="字体大小" hint="终端默认字体大小">
                <input className="input-control" type="number" defaultValue={14} />
              </SettingRow>
            </>
          )}
          {tab === "security" && (
            <>
              <SettingRow label="端到端加密" hint="所有传输数据均使用 E2EE 加密保护">
                <span className="badge online"><span className="badge-dot green" />已启用</span>
              </SettingRow>
              <SettingRow label="配对请求需要确认" hint="新设备配对时需要手动批准">
                <Toggle on={requireConfirm} onClick={() => setRequireConfirm(!requireConfirm)} />
              </SettingRow>
              <SettingRow label="身份存储位置" hint="本地密钥与身份信息的存储目录">
                <button className="btn btn-secondary btn-sm"><FolderIcon /> 打开目录</button>
              </SettingRow>
              <SettingRow label="审计日志" hint="记录所有连接与操作日志">
                <button className="btn btn-secondary btn-sm">查看</button>
              </SettingRow>
              <SettingRow label="使用情况统计" hint="匿名使用数据帮助改进产品">
                <Toggle on={usageStats} onClick={() => setUsageStats(!usageStats)} />
              </SettingRow>
            </>
          )}
          {tab === "advanced" && (
            <>
              <SettingRow label="协议版本" hint="供高级用户切换传输协议版本">
                <select className="select-control" defaultValue="v2">
                  <option value="v2">v2 (最新)</option>
                  <option value="v1">v1 (兼容)</option>
                </select>
              </SettingRow>
              <SettingRow label="调试模式" hint="开启后输出详细日志用于问题排查">
                <Toggle on={debugMode} onClick={() => setDebugMode(!debugMode)} />
              </SettingRow>
              <SettingRow label="日志级别" hint="调整 Agent 日志输出的详细程度">
                <select className="select-control" defaultValue="info">
                  <option value="debug">Debug</option>
                  <option value="info">Info</option>
                  <option value="warn">Warn</option>
                  <option value="error">Error</option>
                </select>
              </SettingRow>
              <SettingRow label="重置所有设置" hint="将全部配置恢复为默认值，操作不可撤销" danger>
                <button className="btn btn-danger btn-sm">重置</button>
              </SettingRow>
            </>
          )}
          {tab === "about" && (
            <>
              <SettingRow label="应用版本" hint="当前安装的桌面端版本">
                <span className="mono">v2.1.0</span>
              </SettingRow>
              <SettingRow label="Agent 版本" hint="Go 后端服务版本">
                <span className="mono">v2.1.0</span>
              </SettingRow>
              <SettingRow label="协议版本" hint="WebSocket 通信协议版本">
                <span className="mono">v2.1</span>
              </SettingRow>
              <SettingRow label="检查更新" hint="当前已是最新版本">
                <button className="btn btn-secondary btn-sm">检查</button>
              </SettingRow>
              <SettingRow label="开源许可" hint="可查看应用使用的开源组件">
                <button className="btn btn-secondary btn-sm">查看</button>
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

function SettingRow({ label, hint, children, danger }: { label: string; hint?: string; children: ReactNode; danger?: boolean }) {
  return (
    <div className={`setting-item ${danger ? "danger" : ""}`}>
      <div>
        <div className="setting-label">{label}</div>
        {hint && <div className="setting-hint">{hint}</div>}
      </div>
      {children}
    </div>
  );
}
