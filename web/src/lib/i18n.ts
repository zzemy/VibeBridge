/**
 * Lightweight i18n for VibeBridge web frontend.
 * No external deps. Detects navigator.language, persists to localStorage.
 * Fallback: zh (primary user base is Chinese).
 */

export type Lang = "zh" | "en";

const STORAGE_KEY = "vibebridge:lang";

function detectLang(): Lang {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === "zh" || stored === "en") return stored;
  } catch {
    // localStorage unavailable
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

type Dict = Record<string, { zh: string | string[]; en: string | string[] }>;

const dict: Dict = {
  // ── App.tsx ──
  "app.loading": { zh: "正在加载 VibeBridge…", en: "Loading VibeBridge…" },

  // ── TerminalApp.tsx ──
  "term.notStarted": { zh: "未开始", en: "Not started" },
  "term.sessionRestored": { zh: "会话已恢复", en: "Session restored" },
  "term.historyTruncated": { zh: "终端历史已截断", en: "Terminal history was truncated" },
  "term.tryingRelay": { zh: "正在尝试中继连接…", en: "Trying relay connection..." },
  "term.terminalNotConnected": { zh: "终端未连接", en: "Terminal is not connected" },
  "term.invalidInput": { zh: "无效的终端输入", en: "Invalid terminal input" },
  "term.promptSent": { zh: "提示已发送", en: "Prompt sent" },
  "term.promptInserted": { zh: "提示已插入", en: "Prompt inserted" },
  "term.promptCommitted": { zh: "提示已提交", en: "Prompt was already committed" },
  "term.invalidDimensions": { zh: "无效的终端尺寸", en: "Invalid terminal dimensions" },
  "term.couldNotEndSession": { zh: "无法结束会话", en: "Could not end the session" },
  "term.selectionCopied": { zh: "已复制选中文本", en: "Selection copied" },
  "term.selectTextFirst": { zh: "请先选择终端文本", en: "Select terminal text first" },
  "term.terminalCleared": { zh: "终端视图已清空", en: "Terminal view cleared" },
  "term.promptCancelled": { zh: "提示已取消；暂存文件已保留", en: "Prompt cancelled; staged files were kept" },
  "term.promptActionFailed": { zh: "提示操作失败；暂存文件已保留", en: "Prompt action failed; staged files were kept" },
  "term.keyboardReady": { zh: "终端键盘就绪", en: "Terminal keyboard ready" },
  "term.waitingConnection": { zh: "等待终端连接", en: "Waiting for terminal connection" },
  "term.reconnectingIn": { zh: "正在重连，{s} 秒", en: "Reconnecting in {s}s" },
  "term.connectionInterrupted": { zh: "连接已中断。PTY 仍在保持。", en: "Connection interrupted. The PTY is being kept alive." },
  "term.retryLabel": { zh: "重试 {s} 秒", en: "retry {s}s" },
  "term.loadingTerminal": { zh: "正在加载终端…", en: "Loading terminal..." },
  "term.localRelay": { zh: "本地终端中继", en: "local terminal relay" },
  "term.privateLAN": { zh: "局域网", en: "private LAN" },
  "term.retryBtn": { zh: "重试", en: "Retry" },
  "term.endBtn": { zh: "结束", en: "End" },
  "term.endSessionTitle": { zh: "结束此终端会话？", en: "End this terminal session?" },
  "term.endSessionDesc": { zh: "本地 AI CLI 及其 PTY 将被停止。此操作不可撤销。", en: "The local AI CLI and its PTY will be stopped. This cannot be undone." },
  "term.keepSession": { zh: "保留会话", en: "Keep session" },
  "term.endSession": { zh: "结束会话", en: "End session" },
  "term.found": { zh: "已找到 \"{q}\"", en: "Found \"{q}\"" },
  "term.noMatch": { zh: "未找到 \"{q}\"", en: "No match for \"{q}\"" },

  // ── formatAgo ──
  "time.now": { zh: "刚刚", en: "now" },
  "time.mAgo": { zh: "{m} 分钟前", en: "{m}m ago" },
  "time.hAgo": { zh: "{h} 小时前", en: "{h}h ago" },
  "time.elapsedHm": { zh: "{h}小时{m}分", en: "{h}h {m}m" },
  "time.elapsedM": { zh: "{m}分", en: "{m}m" },
  "time.elapsedLess": { zh: "<1分", en: "<1m" },

  // ── ConnectionStatus.tsx ──
  "conn.noToken": { zh: "无令牌", en: "No token" },
  "conn.connecting": { zh: "连接中", en: "Connecting" },
  "conn.reconnecting": { zh: "重连中", en: "Reconnecting" },
  "conn.connected": { zh: "已连接", en: "Connected" },
  "conn.closed": { zh: "已关闭", en: "Closed" },
  "conn.error": { zh: "错误", en: "Error" },

  // ── PairingScreen.tsx ──
  "pair.invalidLink": { zh: "无效的配对链接", en: "Invalid pairing link" },
  "pair.cannotUse": { zh: "此代码无法使用", en: "This code cannot be used" },
  "pair.openNewQR": { zh: "在电脑上打开 VibeBridge 并创建新的一次性二维码。", en: "Open VibeBridge on the computer and create a new single-use QR code." },
  "pair.homeComputer": { zh: "家用电脑", en: "Home computer" },
  "pair.securePairing": { zh: "安全设备配对", en: "Secure device pairing" },
  "pair.qrVerification": { zh: "二维码校验码", en: "QR verification code" },
  "pair.encryptedHandshake": { zh: "加密握手码", en: "Encrypted handshake code" },
  "pair.computing": { zh: "计算中…", en: "Computing…" },
  "pair.confirmSas": { zh: "确认 {sas} 也出现在电脑的托盘菜单或本地管理页面中，然后在那里选择「允许手机」。", en: "Confirm that {sas} also appears in the computer's tray menu or local management page, then choose Allow phone there." },
  "pair.tryAgain": { zh: "重试", en: "Try again" },
  "pair.qrSecretNote": { zh: "二维码密钥已从地址栏移除，且永不保存。只有在电脑上完成加密审批后才会存储信任。", en: "The QR secret was removed from the address bar and is never saved. Trust is stored only after encrypted approval on the computer." },
  "pair.pairingFailed": { zh: "配对失败", en: "Pairing failed" },
  "pair.title.connecting": { zh: "正在连接到你的电脑", en: "Connecting to your computer" },
  "pair.title.handshaking": { zh: "正在保护连接", en: "Securing the connection" },
  "pair.title.pending": { zh: "在电脑上批准此手机", en: "Approve this phone on the computer" },
  "pair.title.approved": { zh: "手机已配对", en: "Phone paired" },
  "pair.title.rejected": { zh: "配对被拒绝", en: "Pairing rejected" },
  "pair.title.error": { zh: "配对未能完成", en: "Pairing could not finish" },
  "pair.title.invalid": { zh: "无效的配对链接", en: "Invalid pairing link" },
  "pair.desc.connecting": { zh: "正在打开到 {name} 的私有连接。", en: "Opening a private connection to {name}." },
  "pair.desc.handshaking": { zh: "正在对两台设备进行认证并派生一次性加密密钥。", en: "Authenticating both devices and deriving one-time encryption keys." },
  "pair.desc.pending": { zh: "加密握手已完成。最终批准必须在本地进行。", en: "The encrypted handshake is complete. Final approval must happen locally." },
  "pair.desc.approved": { zh: "{name} 现在已识别此浏览器，正在进入终端…", en: "{name} now recognizes this browser, entering terminal…" },
  "pair.redirecting": { zh: "正在进入终端…", en: "Entering terminal…" },
  "pair.desc.rejected": { zh: "{name} 未授权此浏览器。创建新的二维码以重试。", en: "{name} did not authorize this browser. Create a new QR code to try again." },
  "pair.desc.error": { zh: "此浏览器上未创建信任。你可以在二维码邀请仍然有效时重试。", en: "No trust was created on this browser. You can retry while the QR invitation is still valid." },

  // ── InstallPrompt.tsx ──
  "install.installApp": { zh: "安装应用", en: "Install app" },

  // ── UpdateBanner.tsx ──
  "update.available": { zh: "有新版本可用。", en: "A new version is available." },
  "update.reload": { zh: "重新加载", en: "Reload" },

  // ── PromptComposer.tsx ──
  "prompt.quickPrompts": {
    zh: [
      "审查当前改动并找出最高风险的问题。",
      "运行相关测试并修复所有失败。",
      "解释当前的阻塞点和下一步具体操作。",
      "总结进展、剩余工作和验证状态。",
    ],
    en: [
      "Review the current changes and identify the highest-risk issue.",
      "Run the relevant tests and fix any failures.",
      "Explain the current blocker and the next concrete step.",
      "Summarize progress, remaining work, and verification status.",
    ],
  },
  "prompt.sendEnter": { zh: "发送 + 回车", en: "Send + Enter" },
  "prompt.insertOnly": { zh: "仅插入", en: "Insert only" },
  "prompt.quickPromptsBtn": { zh: "快捷提示", en: "Quick prompts" },
  "prompt.recent": { zh: "{n} 条历史", en: "{n} recent" },
  "prompt.placeholder": { zh: "告诉本地 AI CLI 要做什么…", en: "Tell the local AI CLI what to do..." },
  "prompt.pasteClipboard": { zh: "从剪贴板粘贴", en: "Paste from clipboard" },
  "prompt.sendPrompt": { zh: "发送提示", en: "Send prompt" },
  "prompt.insertPrompt": { zh: "插入提示", en: "Insert prompt" },
  "prompt.charLimit": { zh: "提示限制为 {n} 个字符。", en: "Prompts are limited to {n} characters." },
  "prompt.clipboardExceeds": { zh: "剪贴板文本超过 {n} 字符限制。", en: "Clipboard text exceeds the {n} character limit." },
  "prompt.draftUnavailable": { zh: "此浏览器中草稿存储不可用。", en: "Draft storage is unavailable in this browser." },
  "prompt.historyUnavailable": { zh: "此浏览器中提示历史不可用。", en: "Prompt history is unavailable in this browser." },
  "prompt.prepFailed": { zh: "提示准备失败", en: "Prompt preparation failed" },
  "prompt.clipboardUnavailable": { zh: "此局域网页面上无法访问剪贴板。请直接粘贴到编辑器中。", en: "Clipboard access is unavailable on this LAN page. Paste directly into the editor." },
  "prompt.clipboardDenied": { zh: "剪贴板访问被拒绝。请直接粘贴到编辑器中。", en: "Clipboard access was denied. Paste directly into the editor instead." },
  "prompt.quickPromptsLabel": { zh: "快捷提示和历史记录", en: "Quick prompts and recent history" },
  "prompt.modeLabel": { zh: "提示提交模式", en: "Prompt submission mode" },

  // ── AttachmentComposer.tsx ──
  "attach.attachFiles": { zh: "附加文件", en: "Attach files" },
  "attach.reviewUpTo": { zh: "在发送到此工作区之前，最多审查 {n} 个文件。", en: "Review up to {n} files before sending them to this workspace." },
  "attach.camera": { zh: "相机", en: "Camera" },
  "attach.choose": { zh: "选择", en: "Choose" },
  "attach.selectedAttachments": { zh: "已选附件", en: "Selected attachments" },
  "attach.remove": { zh: "移除 {name}", en: "Remove {name}" },
  "attach.fileCount": { zh: "{n} 个文件 · {size}", en: "{n} file(s) · {size}" },
  "attach.clear": { zh: "清除", en: "Clear" },
  "attach.cancel": { zh: "取消", en: "Cancel" },
  "attach.sendFiles": { zh: "发送文件", en: "Send files" },
  "attach.sending": { zh: "正在发送 {name}", en: "Sending {name}" },
  "attach.transferProgress": { zh: "附件传输进度", en: "Attachment transfer progress" },
  "attach.filesVerified": { zh: "文件已验证并暂存。", en: "Files verified and staged." },
  "attach.notAvailable": { zh: "此 Agent 上尚不可用附件传输。", en: "Attachment transfer is not available on this Agent yet." },
  "attach.filesNotSupported": { zh: "所选文件不受支持", en: "Selected files are not supported" },
  "attach.cancelledCleanup": { zh: "传输已取消，但无法确认清理。会话结束时将删除剩余文件。", en: "Transfer cancelled, but cleanup could not be confirmed. Remaining files will be removed when the session ends." },
  "attach.cancelledDiscarded": { zh: "传输已取消。此批文件已被丢弃。", en: "Transfer cancelled. This file batch was discarded." },
  "attach.transferFailed": { zh: "附件传输失败", en: "Attachment transfer failed" },
  "attach.cleanupNote": { zh: "无法确认清理；会话结束时将删除剩余文件。", en: "Cleanup could not be confirmed; remaining files will be removed when the session ends." },

  // ── AttachmentPromptDialog.tsx ──
  "attachDialog.title": { zh: "确认受信任的附件提示", en: "Confirm trusted attachment prompt" },
  "attachDialog.desc": { zh: "Agent 已在本地解析暂存文件。在继续之前，请审查确切的终端文本。", en: "The Agent resolved staged files locally. Review the exact terminal text before continuing." },
  "attachDialog.confirmSend": { zh: "确认将写入此文本并按下回车。", en: "Confirming writes this exact text and presses Enter." },
  "attachDialog.confirmInsert": { zh: "确认将插入此文本，不按回车。", en: "Confirming inserts this exact text without pressing Enter." },
  "attachDialog.close": { zh: "关闭", en: "Close" },
  "attachDialog.cancelAction": { zh: "取消操作", en: "Cancel action" },
  "attachDialog.sending": { zh: "发送中…", en: "Sending…" },
  "attachDialog.confirmAndSend": { zh: "确认并发送", en: "Confirm and send" },
  "attachDialog.confirmInsertion": { zh: "确认插入", en: "Confirm insertion" },
  "attachDialog.actionFailed": { zh: "附件提示操作失败", en: "Attachment prompt action failed" },

  // ── TerminalToolbar.tsx ──
  "toolbar.findOutput": { zh: "搜索终端输出", en: "Find terminal output" },
  "toolbar.next": { zh: "下一个", en: "Next" },
  "toolbar.closeSearch": { zh: "关闭搜索", en: "Close search" },
  "toolbar.focusKeyboard": { zh: "聚焦终端键盘", en: "Focus terminal keyboard" },
  "toolbar.searchOutput": { zh: "搜索输出", en: "Search output" },
  "toolbar.copySelection": { zh: "复制终端选中文本", en: "Copy terminal selection" },
  "toolbar.clearTerminal": { zh: "清空终端视图", en: "Clear terminal view" },
  "toolbar.decreaseFont": { zh: "减小终端字体", en: "Decrease terminal font size" },
  "toolbar.increaseFont": { zh: "增大终端字体", en: "Increase terminal font size" },

  // ── ShortcutBar.tsx ──
  "shortcut.terminalShortcuts": { zh: "终端快捷键", en: "Terminal shortcuts" },

  // ── Language toggle ──
  "lang.toggle": { zh: "EN", en: "中" },

  // ── Tab bar ──
  "tab.terminal": { zh: "终端", en: "Terminal" },
  "tab.sessions": { zh: "会话", en: "Sessions" },
  "tab.files": { zh: "文件", en: "Files" },
  "tab.settings": { zh: "设置", en: "Settings" },

  // ── Sessions screen ──
  "session.title": { zh: "当前会话", en: "Current Session" },
  "session.state": { zh: "状态", en: "State" },
  "session.elapsed": { zh: "运行时长", en: "Uptime" },
  "session.lastActivity": { zh: "最后活动", en: "Last activity" },
  "session.transport": { zh: "传输方式", en: "Transport" },
  "session.transportDirect": { zh: "直连", en: "Direct" },
  "session.transportRelay": { zh: "中继", en: "Relay" },
  "session.noSession": { zh: "没有活跃会话", en: "No active session" },
  "session.noSessionDesc": { zh: "连接到一台电脑以开始远程终端。", en: "Connect to a computer to start a remote terminal." },
  "session.reconnect": { zh: "重新连接", en: "Reconnect" },
  "session.endSession": { zh: "结束会话", en: "End session" },

  // ── Files screen ──
  "files.title": { zh: "文件传输", en: "File Transfers" },
  "files.stagedCount": { zh: "{n} 个文件已就绪", en: "{n} file(s) ready" },
  "files.noTransfers": { zh: "暂无文件传输", en: "No file transfers" },
  "files.notAvailable": { zh: "此 Agent 不支持文件传输。", en: "File transfer is not supported by this Agent." },

  // ── Settings screen ──
  "settings.title": { zh: "设置", en: "Settings" },
  "settings.general": { zh: "通用", en: "General" },
  "settings.language": { zh: "语言", en: "Language" },
  "settings.terminal": { zh: "终端", en: "Terminal" },
  "settings.fontSize": { zh: "字体大小", en: "Font size" },
  "settings.fontSizeValue": { zh: "{n}px", en: "{n}px" },
  "settings.about": { zh: "关于", en: "About" },
  "settings.server": { zh: "服务器", en: "Server" },
  "settings.theme": { zh: "主题", en: "Theme" },
  "settings.themeDark": { zh: "深色", en: "Dark" },
  "settings.connection": { zh: "连接", en: "Connection" },
  "settings.autoReconnect": { zh: "自动重连", en: "Auto-reconnect" },
  "settings.transport": { zh: "传输方式", en: "Transport" },
  "settings.relayServer": { zh: "中继服务器", en: "Relay server" },
  "settings.security": { zh: "安全", en: "Security" },
  "settings.e2ee": { zh: "端到端加密", en: "End-to-end encryption" },
  "settings.e2eeEnabled": { zh: "已启用", en: "Enabled" },
  "settings.version": { zh: "版本", en: "Version" },
  "settings.openSource": { zh: "开源许可", en: "Open source" },
  "settings.feedback": { zh: "反馈与建议", en: "Feedback" },
  "settings.tagline": { zh: "让远程终端触手可及", en: "Remote terminal, within reach" },
  "settings.scrollback": { zh: "回滚行数", en: "Scrollback" },
  "settings.cursorStyle": { zh: "光标样式", en: "Cursor style" },
  "settings.cursorBlock": { zh: "方块", en: "Block" },
  "settings.cursorUnderline": { zh: "下划线", en: "Underline" },
  "settings.cursorBar": { zh: "竖线", en: "Bar" },
};

export function t(key: string, params?: Record<string, string | number>): string {
  const entry = dict[key];
  if (!entry) return key;
  const raw = entry[currentLang];
  if (typeof raw !== "string") return key;
  if (!params) return raw;
  return raw.replace(/\{(\w+)\}/g, (_, k: string) => String(params[k] ?? `{${k}}`));
}

export function tArray(key: string): string[] {
  const entry = dict[key];
  if (!entry) return [];
  const val = entry[currentLang];
  return Array.isArray(val) ? val : [];
}
