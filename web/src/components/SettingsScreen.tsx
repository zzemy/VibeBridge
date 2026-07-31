import { Globe, Info, Monitor, Type } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "./ui/button";
import { getLang, setLang, subscribeLang, t, type Lang } from "../lib/i18n";

type Props = {
  fontSize: number;
  onFontSize: (size: number) => void;
  minSize: number;
  maxSize: number;
};

export function SettingsScreen({ fontSize, onFontSize, minSize, maxSize }: Props) {
  const [, setTick] = useState(0);
  useEffect(() => subscribeLang(() => setTick((n) => n + 1)), []);

  const lang = getLang();

  return (
    <div className="mx-auto w-full max-w-2xl px-4 py-5">
      <h1 className="mb-4 text-xl font-semibold text-zinc-50">{t("settings.title")}</h1>

      {/* General — Language */}
      <SettingsSection icon={Globe} title={t("settings.general")}>
        <div className="px-4 py-3">
          <p className="mb-2.5 text-xs text-zinc-500">{t("settings.language")}</p>
          <div className="flex gap-2">
            <LangButton
              active={lang === "zh"}
              label="中文"
              onClick={() => setLang("zh" as Lang)}
            />
            <LangButton
              active={lang === "en"}
              label="English"
              onClick={() => setLang("en" as Lang)}
            />
          </div>
        </div>
      </SettingsSection>

      {/* Terminal — Font size */}
      <SettingsSection icon={Type} title={t("settings.terminal")}>
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
      </SettingsSection>

      {/* About */}
      <SettingsSection icon={Info} title={t("settings.about")}>
        <div className="divide-y divide-zinc-800/50">
          <AboutRow
            icon={Monitor}
            label={t("settings.server")}
            value={typeof window !== "undefined" ? window.location.host : "—"}
          />
          <AboutRow
            icon={Globe}
            label={t("settings.theme")}
            value={t("settings.themeDark")}
          />
        </div>
      </SettingsSection>
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
