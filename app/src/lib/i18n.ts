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
  } catch {}
  const nav = typeof navigator !== "undefined" ? navigator.language.toLowerCase() : "zh";
  return nav.startsWith("zh") ? "zh" : "en";
}

let currentLang: Lang = detectLang();
const listeners = new Set<() => void>();

export function getLang(): Lang { return currentLang; }
export function setLang(lang: Lang): void {
  currentLang = lang;
  try { localStorage.setItem(STORAGE_KEY, lang); } catch {}
  listeners.forEach((fn) => fn());
}
export function toggleLang(): Lang {
  const next = currentLang === "zh" ? "en" : "zh";
  setLang(next);
  return next;
}
export function subscribeLang(fn: () => void): () => void {
  listeners.add(fn);
  return () => { listeners.delete(fn); };
}

type Dict = Record<string, { zh: string; en: string }>;

const dict: Dict = {
  "header.running": { zh: "运行中", en: "Running" },
  "header.stopped": { zh: "已停止", en: "Stopped" },

  "sidebar.mainMenu": { zh: "主菜单", en: "Main Menu" },
  "sidebar.you": { zh: "郑昊", en: "You" },
  "sidebar.thisDevice": { zh: "MacBook Pro 16”", en: "MacBook Pro 16”" },

  "nav.overview": { zh: "概览", en: "Overview" },
  "nav.pairing": { zh: "配对", en: "Pairing" },
  "nav.devices": { zh: "设备", en: "Devices" },
  "nav.settings": { zh: "设置", en: "Settings" },

  "overview.subtitle": { zh: "监控 Agent 运行状态与连接设备", en: "Monitor Agent status and connected devices" },
  "overview.agentRunning": { zh: "Agent 运行正常", en: "Agent Running" },
  "overview.agentStopped": { zh: "Agent 已停止", en: "Agent Stopped" },
  "overview.uptime": { zh: "运行", en: "Uptime" },
  "overview.resourceUsage": { zh: "资源使用", en: "Resource Usage" },
  "overview.realtime": { zh: "实时", en: "Realtime" },
  "overview.cpu": { zh: "CPU", en: "CPU" },
  "overview.memory": { zh: "内存", en: "Memory" },
  "overview.disk": { zh: "磁盘", en: "Disk" },
  "overview.network": { zh: "网络", en: "Network" },
  "overview.quickActions": { zh: "快捷操作", en: "Quick Actions" },
  "overview.copyAddr": { zh: "复制连接地址", en: "Copy connection address" },
  "overview.openWeb": { zh: "打开 Web 端", en: "Open Web UI" },
  "overview.viewLogs": { zh: "查看日志", en: "View logs" },
  "overview.connectedDevices": { zh: "已连接设备", en: "Connected Devices" },
  "overview.viewAll": { zh: "查看全部", en: "View all" },

  "stats.runtime": { zh: "运行状态", en: "Runtime" },
  "stats.running": { zh: "运行中", en: "Running" },
  "stats.stopped": { zh: "已停止", en: "Stopped" },
  "stats.connectedDevices": { zh: "已连接设备", en: "Connected" },
  "stats.total": { zh: "总计", en: "total" },
  "stats.goroutines": { zh: "协程数", en: "Goroutines" },
  "stats.activeConns": { zh: "活跃连接", en: "active conns" },
  "stats.memory": { zh: "内存占用", en: "Memory" },
  "stats.alloc": { zh: "已分配", en: "allocated" },

  "status.online": { zh: "在线", en: "Online" },
  "status.offline": { zh: "离线", en: "Offline" },
  "status.port": { zh: "端口", en: "Port" },
  "status.protocol": { zh: "协议", en: "Protocol" },
  "status.protocolValue": { zh: "v1（配对会话）", en: "v1 (paired-session)" },
  "status.restartAgent": { zh: "重启 Agent", en: "Restart Agent" },
  "status.restarting": { zh: "正在重启…", en: "Restarting..." },
  "status.loading": { zh: "加载中…", en: "Loading..." },
  "status.pendingPairing": { zh: "待确认配对", en: "Pending Pairing" },
  "status.deviceName": { zh: "设备名称", en: "Device Name" },
  "status.platform": { zh: "平台", en: "Platform" },
  "status.verificationCode": { zh: "验证码", en: "Verification Code" },
  "status.pairingHint": { zh: "请在浏览器中打开配对页面以确认或拒绝", en: "Open the pairing page in your browser to approve or reject" },

  "devices.subtitle": { zh: "管理已配对的设备，可随时撤销访问权限", en: "Manage paired devices, revoke access anytime" },
  "devices.searchPlaceholder": { zh: "搜索设备名称、用户或指纹…", en: "Search device name, user or fingerprint..." },
  "devices.export": { zh: "导出", en: "Export" },
  "devices.batchRevoke": { zh: "批量撤销", en: "Batch Revoke" },
  "devices.colDevice": { zh: "设备", en: "Device" },
  "devices.colPlatform": { zh: "系统", en: "Platform" },
  "devices.colFingerprint": { zh: "公钥指纹", en: "Fingerprint" },
  "devices.colState": { zh: "状态", en: "Status" },
  "devices.connected": { zh: "已授权", en: "Authorized" },
  "devices.revoked": { zh: "已撤销", en: "Revoked" },
  "devices.pending": { zh: "待确认", en: "Pending" },
  "devices.empty": { zh: "暂无配对设备", en: "No paired devices" },
  "devices.pairNew": { zh: "配对新设备", en: "Pair New Device" },
  "devices.pairHint": { zh: "在移动端输入配对码或扫描二维码以连接新设备", en: "Enter the pairing code on your mobile device or scan the QR code" },
  "devices.goPairing": { zh: "前往配对", en: "Go to Pairing" },

  "pairing.title": { zh: "配对管理", en: "Pairing Management" },
  "pairing.subtitle": { zh: "通过扫码或配对码连接新设备", en: "Connect new devices via QR code or pairing code" },
  "pairing.scanPair": { zh: "扫码配对", en: "Scan to Pair" },
  "pairing.orEnterCode": { zh: "或输入配对码", en: "Or enter pairing code" },
  "pairing.expires": { zh: "后过期", en: "expires" },
  "pairing.refresh": { zh: "点击刷新", en: "Refresh" },
  "pairing.generating": { zh: "正在生成配对码…", en: "Generating pairing code..." },
  "pairing.agentNotRunning": { zh: "Agent 未运行", en: "Agent is not running" },
  "pairing.pendingRequests": { zh: "待处理请求", en: "Pending Requests" },
  "pairing.justNow": { zh: "刚刚", en: "just now" },
  "pairing.allow": { zh: "允许", en: "Allow" },
  "pairing.deny": { zh: "拒绝", en: "Deny" },
  "pairing.noPending": { zh: "暂无待处理请求", en: "No pending requests" },
  "pairing.recent": { zh: "最近配对", en: "Recent Pairings" },
  "pairing.noHistory": { zh: "暂无配对记录", en: "No pairing history" },

  "settings.subtitle": { zh: "配置应用行为与安全选项", en: "Configure app behavior and security options" },
  "settings.general": { zh: "通用", en: "General" },
  "settings.network": { zh: "网络", en: "Network" },
  "settings.terminal": { zh: "终端", en: "Terminal" },
  "settings.security": { zh: "安全", en: "Security" },
  "settings.advanced": { zh: "高级", en: "Advanced" },
  "settings.about": { zh: "关于", en: "About" },
  "settings.launchAtStartup": { zh: "开机自启动", en: "Launch at startup" },
  "settings.launchHint": { zh: "系统登录时自动启动", en: "Auto-launch on system login" },
  "settings.silentStart": { zh: "静默启动", en: "Silent start" },
  "settings.silentHint": { zh: "启动时最小化到托盘", en: "Minimize to tray on launch" },
  "settings.language": { zh: "语言", en: "Language" },
  "settings.theme": { zh: "主题", en: "Theme" },
  "settings.dark": { zh: "深色", en: "Dark" },
  "settings.light": { zh: "浅色", en: "Light" },
  "settings.auto": { zh: "跟随系统", en: "Follow system" },
  "settings.listenAddr": { zh: "监听地址", en: "Listen address" },
  "settings.listenPort": { zh: "监听端口", en: "Listen port" },
  "settings.portDefault": { zh: "8787（默认）", en: "8787 (default)" },
  "settings.autoPort": { zh: "自动选择可用端口", en: "Auto-select available port" },
  "settings.autoPortHint": { zh: "端口被占用时自动切换", en: "Switch automatically if port is in use" },
  "settings.maxConns": { zh: "最大连接数", en: "Max connections" },
  "settings.wsCompress": { zh: "WebSocket 压缩", en: "WebSocket compression" },
  "settings.defaultShell": { zh: "默认 Shell", en: "Default shell" },
  "settings.autoDetect": { zh: "自动检测", en: "Auto-detect" },
  "settings.fontFamily": { zh: "终端字体", en: "Terminal font" },
  "settings.fontSize": { zh: "字号", en: "Font size" },
  "settings.cursorStyle": { zh: "光标样式", en: "Cursor style" },
  "settings.cursorBlock": { zh: "块状", en: "Block" },
  "settings.cursorBar": { zh: "竖线", en: "Bar" },
  "settings.cursorUnderline": { zh: "下划线", en: "Underline" },
  "settings.scrollBuffer": { zh: "滚动缓冲行数", en: "Scroll buffer" },
  "settings.requireApproval": { zh: "需要配对确认", en: "Require pairing approval" },
  "settings.requireApprovalHint": { zh: "新设备配对需手动确认", en: "New devices require manual approval" },
  "settings.codeExpiry": { zh: "配对码有效期（分钟）", en: "Pairing code expiry (min)" },
  "settings.maxDevices": { zh: "允许的设备数量", en: "Max allowed devices" },
  "settings.verboseLog": { zh: "详细日志", en: "Verbose logging" },
  "settings.logLevel": { zh: "日志级别", en: "Log level" },
  "settings.perfMonitor": { zh: "性能监控", en: "Performance monitoring" },
  "settings.crashReport": { zh: "崩溃报告", en: "Crash reports" },
  "settings.experimental": { zh: "实验性功能", en: "Experimental features" },
  "settings.appVersion": { zh: "应用版本", en: "App version" },
  "settings.agentVersion": { zh: "Agent 版本", en: "Agent version" },
  "settings.protocolVer": { zh: "协议版本", en: "Protocol version" },
  "settings.shell": { zh: "外壳", en: "Shell" },
  "settings.checkUpdate": { zh: "检查更新", en: "Check for updates" },
  "settings.openSource": { zh: "开源许可", en: "Open source" },
  "settings.viewLicense": { zh: "查看许可", en: "View license" },
};

export function t(key: string, params?: Record<string, string | number>): string {
  const entry = dict[key];
  if (!entry) return key;
  const raw = entry[currentLang];
  if (!params) return raw;
  return raw.replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? `{${k}}`));
}
