/**
 * Lightweight i18n for VibeBridge desktop app.
 * Mirrors the web frontend i18n pattern.
 */

export type Lang = "zh" | "en";

const STORAGE_KEY = "vibebridge:lang";

function detectLang(): Lang {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "zh" || stored === "en") return stored;
  } catch {
    // ignore
  }
  const nav = typeof navigator !== "undefined" ? navigator.language.toLowerCase() : "zh";
  return nav.startsWith("zh") ? "zh" : "en";
}

let currentLang: Lang = detectLang();
const listeners = new Set<() => void>();

export function getLang(): Lang {
  return currentLang;
}

export function setLang(lang: Lang): void {
  currentLang = lang;
  try {
    localStorage.setItem(STORAGE_KEY, lang);
  } catch {
    // ignore
  }
  listeners.forEach((fn) => fn());
}

export function toggleLang(): Lang {
  const next = currentLang === "zh" ? "en" : "zh";
  setLang(next);
  return next;
}

export function subscribeLang(fn: () => void): () => void {
  listeners.add(fn);
  return () => {
    listeners.delete(fn);
  };
}

type Dict = Record<string, { zh: string; en: string }>;

const dict: Dict = {
  // ── Header ──
  "header.running": { zh: "运行中", en: "Running" },
  "header.stopped": { zh: "已停止", en: "Stopped" },

  // ── Tabs ──
  "tab.status": { zh: "状态", en: "Status" },
  "tab.pairing": { zh: "配对", en: "Pairing" },
  "tab.settings": { zh: "设置", en: "Settings" },

  // ── Status tab ──
  "status.agent": { zh: "Agent", en: "Agent" },
  "status.state": { zh: "状态", en: "State" },
  "status.online": { zh: "在线", en: "Online" },
  "status.offline": { zh: "离线", en: "Offline" },
  "status.port": { zh: "端口", en: "Port" },
  "status.protocol": { zh: "协议", en: "Protocol" },
  "status.protocolValue": { zh: "v1（配对会话）", en: "v1 (paired-session)" },
  "status.restartAgent": { zh: "重启 Agent", en: "Restart Agent" },
  "status.restarting": { zh: "正在重启…", en: "Restarting..." },
  "status.loading": { zh: "加载中…", en: "Loading..." },
  "status.connectedDevices": { zh: "已连接设备", en: "Connected Devices" },
  "status.noDevices": { zh: "暂无设备连接", en: "No devices connected" },
  "status.pairToStart": { zh: "配对设备以开始使用", en: "Pair a device to get started" },

  // ── Pairing tab ──
  "pairing.title": { zh: "配对新设备", en: "Pair New Device" },
  "pairing.enterCode": { zh: "在手机上输入此配对码，或扫描下方二维码", en: "Enter this code on your mobile device, or scan the QR code below" },
  "pairing.qrCode": { zh: "二维码", en: "QR Code" },
  "pairing.refreshCode": { zh: "刷新配对码", en: "Refresh Code" },
  "pairing.generating": { zh: "正在生成配对码…", en: "Generating pairing code..." },
  "pairing.agentNotRunning": { zh: "Agent 未运行", en: "Agent is not running" },

  // ── Settings tab ──
  "settings.general": { zh: "通用", en: "General" },
  "settings.launchAtStartup": { zh: "开机自启", en: "Launch at startup" },
  "settings.listenPort": { zh: "监听端口", en: "Listen port" },
  "settings.portDefault": { zh: "8787（默认）", en: "8787 (default)" },
  "settings.protocol": { zh: "协议", en: "Protocol" },
  "settings.v1Only": { zh: "仅 v1", en: "v1 only" },
  "settings.language": { zh: "语言", en: "Language" },
  "settings.about": { zh: "关于", en: "About" },
  "settings.version": { zh: "版本", en: "Version" },
  "settings.agent": { zh: "Agent", en: "Agent" },
  "settings.agentValue": { zh: "Go（sidecar）", en: "Go (sidecar)" },
  "settings.shell": { zh: "外壳", en: "Shell" },

  // ── Language toggle ──
  "lang.toggle": { zh: "EN", en: "中" },
};

export function t(key: string, params?: Record<string, string | number>): string {
  const entry = dict[key];
  if (!entry) return key;
  const raw = entry[currentLang];
  if (!params) return raw;
  return raw.replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? `{${k}}`));
}
