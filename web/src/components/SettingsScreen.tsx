import { Globe, Info, Monitor, Type, Wifi, ShieldCheck, FileText, Bell, Vibrate, Terminal as TerminalIcon, RefreshCw, Link2, Lock } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "./ui/button";
import { getLang, setLang, subscribeLang, t, type Lang } from "../lib/i18n";

type Props = {
  fontSize: number;
  onFontSize: (size: number) => void;
  minSize: number;
  maxSize: number;
  transport: "direct" | "relay";
  relayUrl: string | null;
};

export function SettingsScreen({ fontSize, onFontSize, minSize, maxSize, transport, relayUrl }: Props) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const lang = getLang();

  // Read client-side settings from localStorage
  const [scrollback, setScrollback] = useState(() => Number(localStorage.getItem("vibebridge:scrollback") || "1000"));
  const [cursorStyle, setCursorStyle] = useState(() => localStorage.getItem("vibebridge:cursor-style") || "block");
  const [autoReconnect, setAutoReconnect] = useState(() => localStorage.getItem("vibebridge:auto-reconnect") !== "false");

  const handleScrollback = (v: number) => {
    setScrollback(v);
    localStorage.setItem("vibebridge:scrollback", String(v));
  };
  const handleCursorStyle = (v: string) => {
    setCursorStyle(v);
    localStorage.setItem("vibebridge:cursor-style", v);
  };
  const handleAutoReconnect = (v: boolean) => {
    setAutoReconnect(v);
    localStorage.setItem("vibebridge:auto-reconnect", String(v));
  };

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-5">
      <h1 className="mb-4 text-xl font-semibold text-zinc-50">{t("settings.title")}</h1>

      {/* General — Language */}
      <SettingsSection icon={Globe} title={t("settings.general")}>
        <div className="divide-y divide-zinc-800/50">
          <div className="px-4 py-3">
            <p className="mb-2.5 text-xs text-zinc-500">{t("settings.language")}</p>
            <div className="flex gap-2">
              <LangButton active={lang === "zh"} label="中文" onClick={() => setLang("zh" as Lang)} />
              <LangButton active={lang === "en"} label="English" onClick={() => setLang("en" as Lang)} />
            </div>
          </div>
          <AboutRow icon={Monitor} label={t("settings.theme")} value={t("settings.themeDark")} />
        </div>
      </SettingsSection>

      {/* Terminal */}
      <SettingsSection icon={Type} title={t("settings.terminal")}>
        <div className="divide-y divide-zinc-800/50">
          <div className="px-4 py-3">
            <div className="mb-2.5 flex items-center justify-between">
              <p className="text-xs text-zinc-500">{t("settings.fontSize")}</p>
              <span className="text-xs font-medium tabular-nums text-zinc-300">
                {t("settings.fontSizeValue", { n: fontSize })}
              </span>
            </div>
            <input
              type="range"
              className="vb-slider w-full"
              min={minSize}
              max={maxSize}
              step={1}
              value={fontSize}
              onChange={(e) => onFontSize(Number(e.target.value))}
              aria-label={t("settings.fontSize")}
            />
            <div className="mt-1.5 flex justify-between text-[10px] text-zinc-600">
              <span>{minSize}px</span>
              <span>{maxSize}px</span>
            </div>
          </div>
          <div className="px-4 py-3">
            <div className="mb-2.5 flex items-center justify-between">
              <p className="text-xs text-zinc-500">{t("settings.scrollback")}</p>
              <span className="text-xs font-medium tabular-nums text-zinc-300">{scrollback}</span>
            </div>
            <input
              type="range"
              className="vb-slider w-full"
              min={100}
              max={10000}
              step={100}
              value={scrollback}
              onChange={(e) => handleScrollback(Number(e.target.value))}
              aria-label={t("settings.scrollback")}
            />
            <div className="mt-1.5 flex justify-between text-[10px] text-zinc-600">
              <span>100</span>
              <span>10000</span>
            </div>
          </div>
          <div className="px-4 py-3">
            <p className="mb-2.5 text-xs text-zinc-500">{t("settings.cursorStyle")}</p>
            <div className="flex gap-2">
              <LangButton active={cursorStyle === "block"} label={t("settings.cursorBlock")} onClick={() => handleCursorStyle("block")} />
              <LangButton active={cursorStyle === "underline"} label={t("settings.cursorUnderline")} onClick={() => handleCursorStyle("underline")} />
              <LangButton active={cursorStyle === "bar"} label={t("settings.cursorBar")} onClick={() => handleCursorStyle("bar")} />
            </div>
          </div>
        </div>
      </SettingsSection>

      {/* Connection */}
      <SettingsSection icon={Wifi} title={t("settings.connection")}>
        <div className="divide-y divide-zinc-800/50">
          <ToggleRow
            label={t("settings.autoReconnect")}
            checked={autoReconnect}
            onChange={handleAutoReconnect}
          />
          <AboutRow icon={Link2} label={t("settings.transport")} value={transport === "relay" ? "Relay" : "Direct"} />
          {relayUrl && (
            <AboutRow icon={RefreshCw} label={t("settings.relayServer")} value={relayUrl} />
          )}
        </div>
      </SettingsSection>

      {/* Security */}
      <SettingsSection icon={ShieldCheck} title={t("settings.security")}>
        <div className="divide-y divide-zinc-800/50">
          <AboutRow icon={Lock} label={t("settings.e2ee")} value={t("settings.e2eeEnabled")} />
        </div>
      </SettingsSection>

      {/* About */}
      <SettingsSection icon={Info} title={t("settings.about")}>
        <div className="divide-y divide-zinc-800/50">
          <AboutRow icon={Monitor} label={t("settings.server")} value={typeof window !== "undefined" ? window.location.host : "—"} />
          <AboutRow icon={Globe} label={t("settings.version")} value="1.0.0" />
          <LinkRow icon={FileText} label={t("settings.openSource")} href="https://github.com/zzemy/VibeBridge" />
          <LinkRow icon={Info} label={t("settings.feedback")} href="https://github.com/zzemy/VibeBridge/issues" />
        </div>
      </SettingsSection>

      <p className="mt-4 text-center text-[11px] text-zinc-600">
        VibeBridge · {t("settings.tagline")}
      </p>
    </div>
  );
}

function SettingsSection({
  icon: Icon,
  title,
  children,
}: {
  icon: typeof Globe;
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="mb-3 overflow-hidden rounded-xl border border-zinc-800 bg-zinc-900/50">
      <div className="flex items-center gap-2 border-b border-zinc-800/60 px-4 py-2.5">
        <Icon className="size-3.5 text-zinc-500" aria-hidden="true" />
        <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-400">{title}</h2>
      </div>
      {children}
    </section>
  );
}

function LangButton({ active, label, onClick }: { active: boolean; label: string; onClick: () => void }) {
  return (
    <Button
      type="button"
      variant={active ? "default" : "secondary"}
      size="sm"
      className={`h-9 flex-1 ${active ? "" : "border-zinc-700"}`}
      onClick={onClick}
    >
      {label}
    </Button>
  );
}

function AboutRow({
  icon: Icon,
  label,
  value,
}: {
  icon: typeof Globe;
  label: string;
  value: string;
}) {
  return (
    <div className="flex items-center justify-between px-4 py-3">
      <span className="flex items-center gap-2 text-xs text-zinc-500">
        <Icon className="size-3.5" aria-hidden="true" />
        {label}
      </span>
      <span className="text-sm font-medium text-zinc-200">{value}</span>
    </div>
  );
}

function ToggleRow({
  label,
  checked,
  onChange,
}: {
  label: string;
  checked: boolean;
  onChange: (v: boolean) => void;
}) {
  return (
    <div className="flex items-center justify-between px-4 py-3">
      <span className="text-xs text-zinc-500">{label}</span>
      <button
        type="button"
        role="switch"
        aria-checked={checked}
        onClick={() => onChange(!checked)}
        className={`relative h-6 w-11 rounded-full transition-colors ${checked ? "bg-emerald-500" : "bg-zinc-700"}`}
      >
        <span className={`absolute top-0.5 left-0.5 size-5 rounded-full bg-white transition-transform ${checked ? "translate-x-5" : ""}`} />
      </button>
    </div>
  );
}

function LinkRow({
  icon: Icon,
  label,
  href,
}: {
  icon: typeof Globe;
  label: string;
  href: string;
}) {
  return (
    <a
      href={href}
      target="_blank"
      rel="noopener noreferrer"
      className="flex items-center justify-between px-4 py-3 transition-colors hover:bg-zinc-800/30"
    >
      <span className="flex items-center gap-2 text-xs text-zinc-500">
        <Icon className="size-3.5" aria-hidden="true" />
        {label}
      </span>
      <span className="text-sm text-emerald-400">→</span>
    </a>
  );
}
