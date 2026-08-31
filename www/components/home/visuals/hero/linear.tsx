import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_CUBIC } from "@/lib/easing";
import type { CSSProperties, ReactNode } from "react";

export const FILM_EASE = "cubic-bezier(0.16, 1, 0.3, 1)";

export function ease(t: number) {
  return EASE_OUT_CUBIC(clamp(t));
}

export function smooth(t: number) {
  const x = clamp(t);
  return x * x * (3 - 2 * x);
}

export function easeInOut(t: number) {
  const x = clamp(t);
  return x < 0.5 ? 4 * x * x * x : 1 - Math.pow(-2 * x + 2, 3) / 2;
}

export function fadeStyle(opacity: number, y = 0): CSSProperties {
  return {
    opacity,
    transform: `translateY(${y}px)`,
  };
}

export function moveStyle({
  opacity = 1,
  x = 0,
  y = 0,
  scale = 1,
}: {
  opacity?: number;
  x?: number;
  y?: number;
  scale?: number;
}): CSSProperties {
  return {
    opacity,
    transform: `translate3d(${x}px, ${y}px, 0) scale(${scale})`,
  };
}

export function ticks(from: number, to: number, t: number, digits = 1) {
  return (from + (to - from) * clamp(t)).toFixed(digits);
}

export function Bar({
  value,
  className,
  tone = "neutral",
}: {
  value: number;
  className?: string;
  tone?: "neutral" | "block" | "ok";
}) {
  return (
    <span className={cn("block h-[3px] overflow-hidden rounded-full bg-black/[0.06]", className)}>
      <span
        className={cn(
          "block h-full rounded-full",
          tone === "block" && "bg-[#EB5757]",
          tone === "ok" && "bg-[#4CB782]",
          tone === "neutral" && "bg-black/25",
        )}
        style={{ width: `${clamp(value) * 100}%` }}
      />
    </span>
  );
}

export const AMBER = "#F2C94C";
export const RED = "#EB5757";

export function Hairline({ className }: { className?: string }) {
  return <span className={cn("block h-px w-full bg-black/[0.08]", className)} aria-hidden />;
}

export function Pill({
  children,
  className,
  tone = "neutral",
}: {
  children: ReactNode;
  className?: string;
  tone?: "neutral" | "purple" | "block" | "ok";
}) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-[8px] px-1.5 py-0.5 text-[10px] tracking-extra-tight ring-1",
        tone === "neutral" && "bg-[#F4F4F6] text-[#3C3F44] ring-black/[0.06]",
        tone === "purple" && "bg-[#5E6AD2]/12 text-[#5E6AD2] ring-[#5E6AD2]/20",
        tone === "block" && "bg-[#EB5757]/10 text-[#C43D3D] ring-[#EB5757]/25",
        tone === "ok" && "bg-[#F4F4F6] text-[#6B6F76] ring-black/[0.06]",
        className,
      )}
    >
      {children}
    </span>
  );
}

export function StatusMoon({
  tone,
  className,
}: {
  tone: "todo" | "progress" | "block" | "ok";
  className?: string;
}) {
  if (tone === "todo") {
    return (
      <svg viewBox="0 0 12 12" className={cn("size-3", className)} fill="none" aria-hidden>
        <circle cx="6" cy="6" r="4.15" stroke="#C0C3C8" strokeWidth="1.2" />
      </svg>
    );
  }
  if (tone === "progress") {
    return (
      <svg viewBox="0 0 12 12" className={cn("size-3", className)} aria-hidden>
        <circle cx="6" cy="6" r="4.15" fill="none" stroke={AMBER} strokeWidth="1.2" />
        <path d="M6 1.85 A4.15 4.15 0 0 1 10.15 6 H6 Z" fill={AMBER} />
      </svg>
    );
  }
  if (tone === "block") {
    return (
      <svg viewBox="0 0 12 12" className={cn("size-3", className)} aria-hidden>
        <circle cx="6" cy="6" r="4.2" fill={RED} />
        <path d="M4.2 4.2 7.8 7.8M7.8 4.2 4.2 7.8" stroke="#fff" strokeWidth="1.15" strokeLinecap="round" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 12 12" className={cn("size-3", className)} aria-hidden>
      <circle cx="6" cy="6" r="4.2" fill="#4CB782" />
      <path d="M4 6.1 5.45 7.5 8.1 4.7" stroke="#fff" strokeWidth="1.15" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

export function Meta({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("text-[10px] tracking-extra-tight text-[#9B9EA5]", className)}>{children}</span>
  );
}

export function Label({ children, className }: { children: ReactNode; className?: string }) {
  return (
    <span className={cn("text-[10px] tracking-extra-tight text-[#6B6F76]", className)}>{children}</span>
  );
}
