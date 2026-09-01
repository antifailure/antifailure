import type { ReactNode } from "react";
import { cn } from "@/lib/cn";

const DOTS_LIGHT =
  "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16'><circle cx='1' cy='1' r='0.7' fill='rgba(0,0,0,0.14)'/></svg>\")";
const DOTS_DARK =
  "url(\"data:image/svg+xml;utf8,<svg xmlns='http://www.w3.org/2000/svg' width='16' height='16'><circle cx='1' cy='1' r='0.7' fill='rgba(255,255,255,0.12)'/></svg>\")";

export function FigureFrame({
  id,
  children,
  dark = false,
  className,
}: {
  id: string;
  children: ReactNode;
  dark?: boolean;
  className?: string;
}) {
  return (
    <div
      className={cn(
        "relative flex w-full max-w-[560px] min-h-[320px] flex-col overflow-hidden border max-md:min-h-[240px]",
        dark ? "border-white/12 bg-[#0a0a0a] text-white" : "border-black/12 bg-[#f7f7f5] text-black",
        className,
      )}
    >
      <div
        className="pointer-events-none absolute inset-0 opacity-80"
        style={{ backgroundImage: dark ? DOTS_DARK : DOTS_LIGHT }}
        aria-hidden
      />
      <div
        className={cn(
          "relative z-[1] px-4 py-2.5 font-mono text-[10px] uppercase tracking-[0.16em]",
          dark ? "text-white/35" : "text-black/35",
        )}
      >
        FIG. {id}
      </div>
      <div className="relative z-[1] flex min-h-0 flex-1 flex-col justify-center px-5 pb-5">{children}</div>
    </div>
  );
}

export function FigLabel({
  children,
  dark = false,
}: {
  children: ReactNode;
  dark?: boolean;
}) {
  return (
    <span
      className={cn(
        "inline-block border px-1.5 py-0.5 font-mono text-[9px] uppercase tracking-[0.14em]",
        dark ? "border-white/20 text-white/70" : "border-black/15 text-black/55",
      )}
    >
      {children}
    </span>
  );
}

export function FigCmd({ children }: { children: ReactNode }) {
  return (
    <span className="font-mono text-[11px] tracking-extra-tight text-[#33bf00]">{children}</span>
  );
}
