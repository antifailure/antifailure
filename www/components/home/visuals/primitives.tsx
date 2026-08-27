import { cn } from "@/lib/cn";
import type { ReactNode } from "react";

export const FILM_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";

export function Panel({
  className,
  children,
}: {
  className?: string;
  children: ReactNode;
}) {
  return (
    <div className={cn("relative overflow-hidden bg-[#f4f7f5] ring-1 ring-black/10", className)}>
      {children}
    </div>
  );
}

export function Hairline({ className, vertical }: { className?: string; vertical?: boolean }) {
  return (
    <span
      className={cn(
        "pointer-events-none bg-black/12",
        vertical ? "w-px self-stretch" : "h-px w-full",
        className,
      )}
      aria-hidden
    />
  );
}

export function MonoLabel({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-[10px] tracking-extra-tight text-black/45", className)}>
      {children}
    </span>
  );
}

export function StatusPill({
  tone,
  children,
  className,
}: {
  tone: "PASS" | "WARN" | "BLOCK";
  children?: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight uppercase ring-1",
        tone === "PASS" && "text-[#285D49] ring-[#33bf00]/50",
        tone === "WARN" && "text-amber-800 ring-amber-700/40",
        tone === "BLOCK" && "text-red-700 ring-red-600/50",
        className,
      )}
    >
      {children ?? tone}
    </span>
  );
}

export function Sparkline({
  d,
  className,
  color = "#111",
  width = 160,
  height = 36,
}: {
  d: string;
  className?: string;
  color?: string;
  width?: number;
  height?: number;
}) {
  return (
    <svg viewBox={`0 0 ${width} ${height}`} className={cn("overflow-visible", className)} aria-hidden>
      <path d={d} fill="none" stroke={color} strokeWidth="1" />
    </svg>
  );
}

export function Node({
  label,
  className,
  lit,
}: {
  label: string;
  className?: string;
  lit?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 font-mono text-[10px] tracking-extra-tight",
        lit ? "text-black" : "text-black/40",
        className,
      )}
    >
      <span className={cn("size-1.5 rounded-full", lit ? "bg-[#33bf00]" : "bg-black/20")} />
      {label}
    </span>
  );
}

export function Connector({
  className,
  progress = 1,
}: {
  className?: string;
  progress?: number;
}) {
  return (
    <svg className={cn("overflow-visible", className)} viewBox="0 0 100 2" preserveAspectRatio="none" aria-hidden>
      <line
        x1="0"
        y1="1"
        x2="100"
        y2="1"
        stroke="currentColor"
        strokeWidth="1"
        strokeDasharray="100"
        strokeDashoffset={100 - progress * 100}
      />
    </svg>
  );
}

export function Timestamp({
  value,
  className,
}: {
  value: string;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-[11px] tabular-nums tracking-extra-tight text-black/55", className)}>
      {value}
    </span>
  );
}

export function LockBadge({
  exclusive,
  className,
}: {
  exclusive?: boolean;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight uppercase ring-1",
        exclusive ? "text-red-700 ring-red-600/50" : "text-[#285D49] ring-black/15",
        className,
      )}
    >
      {exclusive ? "ACCESS EXCLUSIVE" : "lock 0.4s"}
    </span>
  );
}

export function CheckRow({
  ok,
  children,
  className,
}: {
  ok?: boolean | "warn" | "run";
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("flex items-center gap-2 font-mono text-[11px] tracking-extra-tight", className)}>
      <span
        className={cn(
          "size-1.5 shrink-0 rounded-full",
          ok === true && "bg-[#33bf00]",
          ok === false && "bg-red-600",
          ok === "warn" && "bg-amber-600",
          ok === "run" && "bg-black/30",
          ok == null && "bg-black/20",
        )}
      />
      {children}
    </div>
  );
}

export function Ticker({
  value,
  className,
}: {
  value: string | number;
  className?: string;
}) {
  return (
    <span className={cn("font-mono text-[15px] tabular-nums tracking-extra-tight", className)}>
      {value}
    </span>
  );
}

export function QueueChip({
  children,
  className,
  blocked,
}: {
  children: ReactNode;
  className?: string;
  blocked?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight ring-1",
        blocked ? "text-red-700 ring-red-600/40" : "text-black/60 ring-black/10",
        className,
      )}
    >
      {children}
    </span>
  );
}

export function Receipt({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "border border-black/10 bg-white px-2 py-1.5 font-mono text-[10px] leading-4 tracking-extra-tight text-black/70",
        className,
      )}
    >
      {children}
    </div>
  );
}

export function formatClock(seconds: number) {
  const s = Math.max(0, Math.floor(seconds));
  const mm = String(Math.floor(s / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return `${mm}:${ss}`;
}

export function seededNoise(i: number, seed = 8) {
  const x = Math.sin(i * 12.9898 + seed * 78.233) * 43758.5453;
  return x - Math.floor(x);
}
