import { useEffect, useState } from "react";
import { TerminalSquare, Monitor, FileText, Settings } from "lucide-react";
import { t, subscribeLang } from "../lib/i18n";

export type TabId = "terminal" | "sessions" | "files" | "settings";

const tabs: Array<{ id: TabId; icon: typeof TerminalSquare; key: string }> = [
  { id: "terminal", icon: TerminalSquare, key: "tab.terminal" },
  { id: "sessions", icon: Monitor, key: "tab.sessions" },
  { id: "files", icon: FileText, key: "tab.files" },
  { id: "settings", icon: Settings, key: "tab.settings" },
];

type Props = {
  active: TabId;
  onChange: (tab: TabId) => void;
};

export function TabBar({ active, onChange }: Props) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  return (
    <nav
      className="vb-tabbar flex shrink-0 items-stretch justify-around border-t border-zinc-800/80 bg-zinc-950/95 backdrop-blur-md"
      aria-label="navigation"
    >
      {tabs.map((tab) => {
        const Icon = tab.icon;
        const isActive = active === tab.id;
        return (
          <button
            key={tab.id}
            type="button"
            onClick={() => onChange(tab.id)}
            aria-current={isActive ? "page" : undefined}
            className={`relative flex min-h-[52px] flex-1 flex-col items-center justify-center gap-1 pt-2 transition-colors duration-200 ${
              isActive ? "text-emerald-400" : "text-zinc-500 hover:text-zinc-300"
            }`}
          >
            <Icon className="size-5" strokeWidth={isActive ? 2.25 : 1.75} aria-hidden="true" />
            <span className="text-[10px] font-medium tracking-wide">{t(tab.key)}</span>
          </button>
        );
      })}
    </nav>
  );
}
