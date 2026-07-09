import type { HTMLAttributes } from "react";
import { cn } from "../../lib/utils";

type BadgeVariant = "default" | "outline" | "success" | "danger" | "muted";

type BadgeProps = HTMLAttributes<HTMLSpanElement> & {
  variant?: BadgeVariant;
};

const variants: Record<BadgeVariant, string> = {
  default: "border-transparent bg-zinc-100 text-zinc-950",
  outline: "border-zinc-700 bg-transparent text-zinc-300",
  success: "border-emerald-400/30 bg-emerald-400/10 text-emerald-300",
  danger: "border-red-400/30 bg-red-400/10 text-red-300",
  muted: "border-zinc-700 bg-zinc-800 text-zinc-300",
};

export function Badge({ className, variant = "default", ...props }: BadgeProps) {
  return (
    <span
      className={cn(
        "inline-flex h-7 items-center rounded-md border px-2 text-xs font-medium",
        variants[variant],
        className,
      )}
      {...props}
    />
  );
}
