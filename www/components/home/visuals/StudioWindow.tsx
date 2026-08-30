import type { ReactNode } from "react";
import { cn } from "@/lib/cn";
import { FILM_EASE, Hairline, Pill, StatusMoon } from "./hero/linear";

export { FILM_EASE };

export function RunWindow({
  children,
  className,
  fade = 0,
}: {
  children: ReactNode;
  className?: string;
  fade?: number;
}) {
  return (
    <div className={cn("relative pb-[9px] pr-[9px]", className)}>
      <div
        className="absolute right-0 bottom-0 left-[9px] top-[9px] rounded-[16px] border border-black/[0.06] bg-[#E7E7E2]"
        aria-hidden
      />
      <div
        className="relative rounded-[16px] border border-black/[0.06] bg-[#EFEFEA] p-3 max-md:p-2.5"
        style={{
          backgroundImage:
            "radial-gradient(ellipse 90% 70% at 50% 0%, rgba(255,255,255,0.72), transparent 62%)",
        }}
      >
        <div className="relative flex h-[552px] flex-col overflow-hidden rounded-[12px] border border-black/[0.08] bg-white font-sans shadow-[0_1px_0_rgba(0,0,0,0.04),0_24px_48px_rgba(0,0,0,0.08)] max-xl:h-auto max-xl:min-h-[420px] max-xl:overflow-visible">
          {children}
          <div
            className="pointer-events-none absolute inset-0 bg-[#f7f7f5]"
            style={{ opacity: fade, transition: `opacity 280ms ${FILM_EASE}` }}
            aria-hidden
          />
        </div>
      </div>
    </div>
  );
}

export function InnerHeader({
  moon,
  breadcrumb,
  metrics,
}: {
  moon: "todo" | "progress" | "block" | "ok";
  breadcrumb: ReactNode;
  metrics?: { value: string; tone?: "block" | "ok" | "muted" }[];
}) {
  return (
    <header className="flex h-11 shrink-0 items-center justify-between gap-4 px-4 max-md:px-3">
      <div className="flex min-w-0 items-center gap-2">
        <StatusMoon tone={moon} />
        <div className="min-w-0 truncate text-[13px] tracking-extra-tight text-[#1A1A1A]">{breadcrumb}</div>
      </div>
      {metrics && metrics.length > 0 ? (
        <div className="flex shrink-0 items-center gap-3">
          {metrics.map((item) => (
            <span
              key={item.value}
              className={cn(
                "text-[12px] tabular-nums tracking-extra-tight",
                item.tone === "block" && "text-[#C43D3D]",
                item.tone === "ok" && "text-[#4CB782]",
                (!item.tone || item.tone === "muted") && "text-[#6B6F76]",
              )}
            >
              {item.value}
            </span>
          ))}
        </div>
      ) : null}
    </header>
  );
}

/**
 * The tab row inside a run window.
 *
 * `short` carries the same tabs written for a phone. Two twenty-character
 * labels and a verdict chip do not fit in 290 points, and the alternative was
 * a tab whose name ended mid-word.
 */
export function InnerPills({
  items,
  short,
  active,
  onSelect,
  action,
}: {
  items: readonly string[];
  short?: readonly string[];
  active: number;
  onSelect?: (index: number) => void;
  action?: ReactNode;
}) {
  return (
    <div className="flex h-10 shrink-0 items-center justify-between gap-3 border-y border-black/[0.08] px-3 max-md:gap-2 max-md:px-2">
      <div className="fade-scroll-x flex min-w-0 items-center gap-0.5 overflow-x-auto no-scrollbars">
        {items.map((item, index) => {
          const selected = index === active;
          const className = cn(
            "shrink-0 rounded-[8px] px-2.5 py-1 text-[12px] tracking-extra-tight max-md:px-2 max-md:text-[11px]",
            selected ? "bg-[#F4F4F6] text-[#1A1A1A]" : "text-[#6B6F76]",
          );
          const label =
            short && short[index] && short[index] !== item ? (
              <>
                <span className="max-md:hidden">{item}</span>
                <span className="hidden max-md:inline">{short[index]}</span>
              </>
            ) : (
              item
            );
          if (onSelect) {
            return (
              <button
                key={item}
                type="button"
                className={cn(className, "pointer-events-auto hover:text-[#1A1A1A]")}
                onClick={() => onSelect(index)}
              >
                {label}
              </button>
            );
          }
          return (
            <span key={item} className={className}>
              {label}
            </span>
          );
        })}
      </div>
      {action ? (
        <span className="shrink-0 rounded-[8px] px-2.5 py-1 text-[12px] tracking-extra-tight text-[#6B6F76] ring-1 ring-black/[0.08] max-md:px-2 max-md:text-[11px]">
          {action}
        </span>
      ) : null}
    </div>
  );
}

export function InnerSplit({
  left,
  right,
}: {
  left: ReactNode;
  right: ReactNode;
}) {
  return (
    <div className="grid min-h-0 flex-1 grid-cols-[minmax(0,0.42fr)_minmax(0,0.58fr)] overflow-hidden max-xl:flex-none max-xl:grid-cols-1 max-xl:overflow-visible">
      <div className="min-h-0 min-w-0 overflow-hidden border-black/[0.08] px-5 py-4 xl:border-r max-xl:overflow-visible max-xl:border-b max-xl:px-4 max-xl:py-3.5">
        {left}
      </div>
      <div className="flex min-h-0 min-w-0 flex-col overflow-hidden p-3 max-xl:overflow-visible max-xl:p-2.5">{right}</div>
    </div>
  );
}

export function NestedPane({
  title,
  meta,
  children,
}: {
  title: ReactNode;
  meta?: ReactNode;
  children: ReactNode;
}) {
  return (
    <div className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-[10px] border border-black/[0.08] bg-[#FAFAF8]">
      <div className="flex shrink-0 items-center justify-between gap-2 border-b border-black/[0.08] bg-white px-3 py-2">
        <span className="min-w-0 truncate text-[12px] tracking-extra-tight text-[#1A1A1A]">{title}</span>
        {meta ? <span className="shrink-0 text-[11px] tracking-extra-tight text-[#9B9EA5]">{meta}</span> : null}
      </div>
      <div className="flex min-h-0 flex-1 flex-col overflow-hidden">{children}</div>
    </div>
  );
}

export function FindingHead({
  title,
  meta,
  step,
  body,
}: {
  title: string;
  meta: ReactNode;
  step: string;
  body: string;
}) {
  return (
    <div>
      <h3 className="h-[2.5em] overflow-hidden text-[20px] leading-snug tracking-extra-tight text-[#1A1A1A] max-md:text-[17px]">
        {title}
      </h3>
      <div className="mt-2 flex h-5 flex-wrap items-center gap-1.5 overflow-hidden">{meta}</div>
      <div className="mt-3 h-4 text-[11px] tabular-nums tracking-extra-tight text-[#9B9EA5]">{step}</div>
      <p className="mt-2 h-[3.9em] max-w-[34ch] overflow-hidden text-[13px] leading-snug tracking-extra-tight text-[#6B6F76]">
        {body}
      </p>
    </div>
  );
}

export function EvidenceRow({
  moon,
  label,
  value,
  tone = "muted",
}: {
  moon: "todo" | "progress" | "block" | "ok";
  label: string;
  value: string;
  tone?: "block" | "ok" | "muted";
}) {
  return (
    <div className="flex h-9 shrink-0 items-center justify-between gap-3 rounded-[10px] border border-black/[0.06] bg-white px-2.5">
      <div className="flex min-w-0 items-center gap-2">
        <StatusMoon tone={moon} />
        <span className="truncate text-[13px] tracking-extra-tight text-[#3C3F44]">{label}</span>
      </div>
      <span
        className={cn(
          "max-w-[46%] shrink-0 truncate text-right text-[13px] tabular-nums tracking-extra-tight",
          tone === "block" && "text-[#C43D3D]",
          tone === "ok" && "text-[#4CB782]",
          tone === "muted" && "text-[#6B6F76]",
        )}
      >
        {value}
      </span>
    </div>
  );
}

export function RunToast({
  visible,
  tone,
  title,
  detail,
}: {
  visible: boolean;
  tone: "block" | "ok" | "progress";
  title: string;
  detail: string;
}) {
  return (
    <div
      className="pointer-events-none absolute top-3 right-3 z-20 max-w-[min(100%-1.5rem,420px)]"
      style={{
        opacity: visible ? 1 : 0,
        transform: visible ? "translateY(0px)" : "translateY(-6px)",
        transition: `opacity 420ms ${FILM_EASE}, transform 420ms ${FILM_EASE}`,
      }}
    >
      <div className="flex items-start gap-2 rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2 shadow-[0_8px_24px_rgba(0,0,0,0.06)]">
        <StatusMoon
          tone={tone === "block" ? "block" : tone === "ok" ? "ok" : "progress"}
          className="mt-0.5"
        />
        <div className="min-w-0">
          <div className="text-[12px] font-medium tracking-extra-tight text-[#1A1A1A]">{title}</div>
          <p className="mt-0.5 text-[11px] leading-snug tracking-extra-tight text-[#6B6F76]">{detail}</p>
        </div>
      </div>
    </div>
  );
}

export function PrivatePill() {
  return <Pill tone="ok">Private</Pill>;
}

export function LockGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" className={className} fill="none" aria-hidden>
      <rect x="2.4" y="5.4" width="7.2" height="5.2" rx="0.6" stroke="currentColor" strokeWidth="1" />
      <path d="M4.2 5.4V3.8a1.8 1.8 0 0 1 3.6 0v1.6" stroke="currentColor" strokeWidth="1" />
    </svg>
  );
}

export function RunHairline() {
  return <Hairline />;
}
