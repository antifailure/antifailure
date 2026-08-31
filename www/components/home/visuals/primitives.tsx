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
        // A span is inline by default, and an inline box takes neither h-px nor
        // w-full, so a horizontal Hairline whose parent is not a flex container
        // painted nothing at all and swallowed its own margin with it. Three
        // call sites in Firewall.tsx had already passed className="block" to
        // work around it one at a time. The last one that had not is
        // Architecture.tsx, where the rule between the isolation minimums that
        // are in force and the ones that are only designed simply was not
        // there, and the two lists ran together.
        vertical ? "w-px self-stretch" : "block h-px w-full",
        className,
      )}
      aria-hidden
    />
  );
}

/**
 * The 10px mono label, in the two jobs it actually does.
 *
 * The default grey draws. It is the type inside a mock terminal, a fake log
 * line, a simulated report frame: there the grey depicts a screen rather than
 * addressing a reader, and darkening it flattens the drawing into something
 * that reads as real interface chrome. So the default stays where it was, at
 * black/45, whatever a contrast script says about it.
 *
 * `tone="reader"` addresses. A kicker over a paragraph, a caption naming what
 * a panel is, a comparison column heading, the annotation a diagram turns on:
 * that text is read, not looked at, so it takes black/60. Measured against
 * every surface a MonoLabel sits on, black/60 is 5.74:1 on white, 5.61:1 on
 * the page ground #f7f7f5, 5.59:1 on a Panel #f4f7f5 and 5.51:1 on the sage
 * band #E4F1EB. black/45 is 3.26:1 to 3.36:1 on the same four, under the
 * 4.5:1 floor.
 *
 * The prop exists because six call sites had already reached for a darker
 * grey by hand, in five different files, at three different values. Nothing
 * was shared, so each author patched their own site and the wall stayed up
 * for the next one.
 */
export function MonoLabel({
  children,
  className,
  tone = "art",
}: {
  children: ReactNode;
  className?: string;
  tone?: "art" | "reader";
}) {
  return (
    <span
      className={cn(
        "font-mono text-[10px] tracking-extra-tight",
        tone === "reader" ? "text-black/60" : "text-black/45",
        className,
      )}
    >
      {children}
    </span>
  );
}

/**
 * The verdict badge, in the engine's vocabulary.
 *
 * The tones used to be PASS, WARN and BLOCK, and two of the three were wrong.
 * The engine has no warning state at all: a run resolves to pass, fail, flaky,
 * blocked or unverified (engine/internal/report/report.go). Worse, its
 * `blocked` means the opposite of what BLOCK said here. On the site BLOCK read
 * as "merge disabled"; in the product it means the run could not be carried
 * through, nothing here counts against the change, and `af ci` exits zero. A
 * reader who learned the word from this site and then read a real check would
 * conclude the check had inverted its own verdict.
 *
 * So FAIL is the badge for a change that must not merge, and UNVERIFIED is the
 * neutral one for a run that could not answer. Neither is amber, because a
 * colour that says "proceed with care" is a third outcome the gate does not
 * have.
 */
export function StatusPill({
  tone,
  children,
  className,
}: {
  tone: "PASS" | "FAIL" | "UNVERIFIED";
  children?: ReactNode;
  className?: string;
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center px-1.5 py-0.5 font-mono text-[10px] tracking-extra-tight uppercase ring-1",
        tone === "PASS" && "text-[#285D49] ring-[#33bf00]/50",
        tone === "UNVERIFIED" && "text-black/50 ring-black/20",
        tone === "FAIL" && "text-red-700 ring-red-600/50",
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
