"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";

const STILL = 11.2;
const SPEED = 0.5;
const FILM = "cubic-bezier(0.16, 1, 0.3, 1)";

const NAV = ["Build", "Restore", "Contain", "Destroy"] as const;

function beatIndex(t: number) {
  if (t >= 9) return 3;
  if (t >= 6) return 2;
  if (t >= 3) return 1;
  return 0;
}

function remainingOf(beat: number) {
  return beat >= 3 ? 0 : 4;
}

function Glyph({ kind, dim }: { kind: "dns" | "app" | "wrk" | "key" | "db"; dim?: boolean }) {
  const s = dim ? "rgba(0,0,0,0.32)" : "#111111";
  if (kind === "dns") {
    return (
      <svg viewBox="0 0 16 16" className="size-3.5 shrink-0" fill="none" aria-hidden>
        <circle cx="8" cy="8" r="6.2" stroke={s} strokeWidth="1.15" />
        <ellipse cx="8" cy="8" rx="2.8" ry="6.2" stroke={s} strokeWidth="1.15" />
        <path d="M1.8 8h12.4M8 1.8v12.4" stroke={s} strokeWidth="1.15" />
      </svg>
    );
  }
  if (kind === "app") {
    return (
      <svg viewBox="0 0 16 16" className="size-3.5 shrink-0" fill="none" aria-hidden>
        <rect x="1.6" y="3.2" width="9.2" height="7" rx="1.4" stroke={s} strokeWidth="1.15" fill="#fff" />
        <rect x="5.2" y="5.6" width="9.2" height="7" rx="1.4" stroke={s} strokeWidth="1.15" fill="#fff" />
      </svg>
    );
  }
  if (kind === "wrk") {
    return (
      <svg viewBox="0 0 16 16" className="size-3.5 shrink-0" fill="none" aria-hidden>
        <path d="M3.2 3.2v9.6M8 3.2v9.6M12.8 3.2v9.6" stroke={s} strokeWidth="1.2" />
      </svg>
    );
  }
  if (kind === "db") {
    return (
      <svg viewBox="0 0 16 16" className="size-3.5 shrink-0" fill="none" aria-hidden>
        <ellipse cx="8" cy="4.2" rx="5.1" ry="2.1" stroke={s} strokeWidth="1.15" />
        <path d="M2.9 4.2v7.4c0 1.16 2.28 2.1 5.1 2.1s5.1-.94 5.1-2.1V4.2" stroke={s} strokeWidth="1.15" />
        <path d="M2.9 7.8c0 1.16 2.28 2.1 5.1 2.1s5.1-.94 5.1-2.1" stroke={s} strokeWidth="1.15" />
      </svg>
    );
  }
  return (
    <svg viewBox="0 0 16 16" className="size-3.5 shrink-0" fill="none" aria-hidden>
      <circle cx="5.4" cy="8" r="3" stroke={s} strokeWidth="1.15" />
      <path d="M8.4 8h6.1M12.2 8v2.6M14.5 8v2.6" stroke={s} strokeWidth="1.15" strokeLinecap="round" />
    </svg>
  );
}

function DenyMark({ size = "md" }: { size?: "sm" | "md" }) {
  return (
    <span
      className={cn(
        "relative grid place-items-center rounded-full border-[1.4px] border-[#C43D3D] bg-white",
        size === "md" ? "size-7" : "size-4",
      )}
    >
      <span
        className={cn("absolute rotate-[-38deg] rounded-full bg-[#C43D3D]", size === "md" ? "h-[1.5px] w-3.5" : "h-px w-2")}
      />
    </span>
  );
}

function SealPill({
  label,
  on,
  tone = "ok",
}: {
  label: string;
  on: boolean;
  tone?: "ok" | "cut";
}) {
  const cut = tone === "cut";
  return (
    <span
      className={cn(
        "inline-flex shrink-0 items-center gap-1 whitespace-nowrap rounded-full border px-2 py-[3px] text-[10px] font-medium tracking-tight transition-colors duration-500",
        on && !cut && "border-[#33bf00] bg-[rgba(51,191,0,0.12)] text-[#111111]",
        on && cut && "border-[#C43D3D] bg-[rgba(196,61,61,0.10)] text-[#111111]",
        !on && "border-black/[0.14] bg-white text-[#797d86]",
      )}
      style={{ transitionTimingFunction: FILM }}
    >
      <span
        className={cn(
          "size-1.5 rounded-full",
          on && !cut && "bg-[#33bf00]",
          on && cut && "bg-[#C43D3D]",
          !on && "bg-black/20",
        )}
      />
      {label}
    </span>
  );
}

function Gate({
  label,
  sub,
  tone,
}: {
  label: string;
  sub?: string;
  tone: "ok" | "cut" | "idle";
}) {
  return (
    <div className="flex flex-col items-center gap-0.5 py-1">
      {tone === "cut" ? (
        <DenyMark size="sm" />
      ) : (
        <span
          className={cn(
            "size-2.5 rounded-full border bg-white transition-colors duration-500",
            tone === "ok" ? "border-[#33bf00] bg-[#33bf00]" : "border-black/25",
          )}
          style={{ transitionTimingFunction: FILM }}
        />
      )}
      <span
        className={cn(
          "whitespace-nowrap font-mono text-[8px] tracking-[0.14em]",
          tone === "cut" ? "text-[#C43D3D]" : tone === "ok" ? "text-[#111111]" : "text-[#797d86]",
        )}
      >
        {label}
      </span>
      {sub ? (
        <span className={cn("text-[9px] tracking-tight", tone === "cut" ? "text-[#C43D3D]" : "text-[#797d86]")}>
          {sub}
        </span>
      ) : null}
    </div>
  );
}

function Wire({ lines, dim }: { lines: [string, string, string]; dim?: boolean }) {
  const widths = ["w-[86%]", "w-[68%]", "w-[52%]"];
  return (
    <div className="mt-2 space-y-[3px]">
      {lines.map((line, i) => (
        <div
          key={line}
          className={cn("h-[7px] overflow-hidden rounded-[3px]", dim ? "bg-black/[0.06]" : "bg-black/[0.08]")}
        >
          <div className={cn("h-full rounded-[3px]", widths[i], dim ? "bg-black/[0.10]" : "bg-black/[0.14]")} />
        </div>
      ))}
    </div>
  );
}

function NodeCard({
  kicker,
  title,
  glyph,
  live,
  dim,
  cut,
  gone,
}: {
  kicker: string;
  title: string;
  glyph: "dns" | "app" | "wrk" | "key" | "db";
  live?: boolean;
  dim?: boolean;
  cut?: boolean;
  gone?: boolean;
}) {
  return (
    <div
      className={cn(
        "relative min-w-0 rounded-[10px] border bg-white p-2.5 shadow-[0_1px_2px_rgba(17,17,17,0.06),0_8px_18px_rgba(17,17,17,0.05)] transition-opacity duration-700",
        dim ? "border-dashed border-black/30" : "border-black/[0.14]",
        gone && !dim ? "opacity-45" : dim ? "opacity-70" : "opacity-100",
      )}
      style={{ transitionTimingFunction: FILM }}
    >
      <div className="flex items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="font-mono text-[8px] tracking-[0.14em] text-[#797d86]">{kicker}</div>
          <div className={cn("mt-0.5 truncate text-[12px] tracking-tight", cut ? "text-[#C43D3D]" : dim ? "text-[#797d86]" : "text-[#111111]")}>
            {title}
          </div>
        </div>
        <div className="flex items-center gap-1.5">
          <Glyph kind={glyph} dim={dim || cut} />
          {live ? <span className="size-1.5 rounded-full bg-[#33bf00]" /> : <span className="size-1.5 rounded-full bg-black/15" />}
        </div>
      </div>
      {glyph === "app" ? (
        <div className={cn("mt-2 overflow-hidden rounded-[6px] border", dim ? "border-black/10" : "border-black/[0.08]")}>
          <div className="flex h-2.5 items-center gap-0.5 bg-black/[0.04] px-1.5">
            <span className="size-1 rounded-full bg-black/20" />
            <span className="size-1 rounded-full bg-black/12" />
            <span className="ml-1 h-px flex-1 bg-black/10" />
          </div>
          <div className="space-y-[3px] px-1.5 py-1.5">
            <div className="h-[5px] w-[78%] rounded-sm bg-black/[0.10]" />
            <div className="h-[5px] w-[54%] rounded-sm bg-black/[0.07]" />
          </div>
        </div>
      ) : glyph === "db" ? (
        <div className="mt-2 flex items-center gap-2">
          <svg viewBox="0 0 40 28" className="h-7 w-10 shrink-0" fill="none" aria-hidden>
            <ellipse cx="20" cy="6" rx="14" ry="5" fill="#fff" stroke={cut ? "#C43D3D" : dim ? "rgba(0,0,0,0.28)" : "rgba(0,0,0,0.38)"} />
            <path
              d="M6 6v14c0 2.8 6.3 5 14 5s14-2.2 14-5V6"
              fill="#fff"
              stroke={cut ? "#C43D3D" : dim ? "rgba(0,0,0,0.28)" : "rgba(0,0,0,0.38)"}
            />
            <ellipse cx="20" cy="20" rx="14" ry="5" fill="#fff" stroke={cut ? "#C43D3D" : dim ? "rgba(0,0,0,0.28)" : "rgba(0,0,0,0.38)"} />
          </svg>
          <div className="min-w-0 flex-1 space-y-[3px]">
            <div className="h-[5px] w-full rounded-sm bg-black/[0.10]" />
            <div className="h-[5px] w-[70%] rounded-sm bg-black/[0.07]" />
          </div>
        </div>
      ) : (
        <Wire lines={["a", "b", "c"]} dim={dim} />
      )}
      {cut ? (
        <span className="absolute -bottom-1.5 left-1/2 -translate-x-1/2">
          <DenyMark size="sm" />
        </span>
      ) : null}
    </div>
  );
}

function Cluster({
  title,
  kicker,
  dashed,
  dim,
  aside,
  children,
  footer,
}: {
  title: string;
  kicker: string;
  dashed?: boolean;
  dim?: boolean;
  aside?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "relative min-w-0 rounded-[14px] border bg-white p-3 shadow-[0_1px_2px_rgba(17,17,17,0.05),0_12px_28px_rgba(17,17,17,0.06)] sm:p-3.5",
        dashed ? "border-dashed border-black/35" : "border-black/15",
        dim && "opacity-[0.78]",
      )}
    >
      <div className="mb-3 flex flex-wrap items-start justify-between gap-2">
        <div className="min-w-0">
          <div className="text-[15px] font-semibold tracking-tight text-[#111111]">{title}</div>
          <div className="mt-0.5 font-mono text-[9px] tracking-[0.14em] text-[#797d86]">{kicker}</div>
        </div>
        {aside ? <div className="shrink-0">{aside}</div> : null}
      </div>
      {children}
      {footer ? <div className="mt-3">{footer}</div> : null}
    </div>
  );
}

function Fiducial({
  className,
  dx,
  dy,
}: {
  className?: string;
  dx: 1 | -1;
  dy: 1 | -1;
}) {
  const x = dx === 1 ? 1.2 : 10.8;
  const y = dy === 1 ? 1.2 : 10.8;
  return (
    <svg
      viewBox="0 0 12 12"
      className={cn("pointer-events-none absolute size-3 overflow-visible", className)}
      fill="none"
      aria-hidden
    >
      <path
        d={`M${x + dx * 9} ${y} H${x} V${y + dy * 9}`}
        stroke="#111111"
        strokeWidth="0.9"
        strokeLinejoin="miter"
        strokeLinecap="butt"
      />
    </svg>
  );
}

function TwinSchematic({ beat, gone }: { beat: number; gone: boolean }) {
  const sealed = (from: number) => beat >= from && !gone;
  const twinLive = !gone;

  return (
    <div className="relative select-none px-3.5 pt-3.5 pb-3 font-sans tracking-tight" aria-hidden>
      <Fiducial className="top-0 left-0" dx={1} dy={1} />
      <Fiducial className="top-0 right-0" dx={-1} dy={1} />
      <Fiducial className="bottom-0 left-0" dx={1} dy={-1} />
      <Fiducial className="right-0 bottom-0" dx={-1} dy={-1} />

      <div className="flex items-baseline justify-between gap-3 font-mono text-[9px] tracking-[0.12em] text-[#797d86]">
        <div className="flex min-w-0 flex-wrap items-baseline gap-x-2.5 gap-y-0.5">
          <span>FIG. 01</span>
          <span className="tracking-[0.12em] text-[#111111]">ISOLATED TWIN</span>
          <span className="font-sans tracking-tight">production not in path</span>
        </div>
        <span className="shrink-0">SHEET 01 / 01</span>
      </div>
      <div className="mt-1.5 h-px bg-black/[0.10]" />

      <div className="mt-3 grid grid-cols-[minmax(0,1fr)_6.25rem_minmax(0,1fr)] items-stretch gap-2 max-md:grid-cols-1 max-md:gap-3 sm:gap-3">
        <Cluster
          title="Isolated Twin"
          kicker="CLONE-LOCAL"
          footer={
            <div className="flex flex-wrap items-center gap-1.5">
              <span className="font-mono text-[8px] tracking-[0.12em] text-[#797d86]">SEALS</span>
              <SealPill label="clone-local DNS" on={sealed(0)} />
              <SealPill label="secrets replaced" on={sealed(1)} />
              <SealPill label="no egress" on={sealed(2)} />
            </div>
          }
        >
          <NodeCard kicker="DNS" title="clone-local" glyph="dns" live={sealed(0)} gone={gone} />
          <Gate label="RESOLVE" tone={sealed(0) ? "ok" : "idle"} />
          <div className="grid grid-cols-2 gap-2">
            <NodeCard kicker="APP" title="candidate" glyph="app" live={twinLive} gone={gone} />
            <NodeCard kicker="WORKERS" title="isolated" glyph="wrk" live={twinLive} gone={gone} />
          </div>
          <Gate label="MASK · KEEP" tone={twinLive ? "ok" : "idle"} />
          <div className="grid grid-cols-2 gap-2">
            <NodeCard kicker="STATE" title="postgres" glyph="db" live={twinLive} gone={gone} />
            <NodeCard kicker="CREDS" title="replaced" glyph="key" live={sealed(1)} gone={gone} />
          </div>
        </Cluster>

        <div className="relative flex min-h-[4.5rem] flex-col items-center justify-center gap-3 max-md:flex-row max-md:py-1">
          <span className="absolute inset-y-4 left-1/2 w-px -translate-x-1/2 bg-black/25 max-md:inset-x-10 max-md:top-1/2 max-md:h-px max-md:w-auto max-md:translate-x-0" />
          <span className="absolute top-1/2 bottom-4 left-1/2 w-px -translate-x-1/2 border-l border-dashed border-black/35 max-md:top-1/2 max-md:right-10 max-md:left-1/2 max-md:h-px max-md:w-auto max-md:translate-x-0 max-md:border-l-0 max-md:border-t" />
          <div className="relative z-[1] bg-[#f7f7f5] px-1">
            <Gate label="NO EGRESS" tone={sealed(2) ? "ok" : "idle"} />
          </div>
          <div className="relative z-[1] flex flex-col items-center gap-1 bg-[#f7f7f5] px-1 py-1">
            <span className="font-mono text-[9px] tracking-[0.14em] text-[#C43D3D]">DENY</span>
            <DenyMark />
            <span className="text-[9px] tracking-tight text-[#C43D3D]">no route</span>
          </div>
        </div>

        <Cluster
          title="Production"
          kicker="LIVE"
          dashed
          dim
          aside={<SealPill label="NOT IN PATH" on tone="cut" />}
          footer={
            <div className="text-[10px] tracking-tight text-[#C43D3D]">prod-db cut · live keys unreachable</div>
          }
        >
          <NodeCard kicker="DNS" title="public resolver" glyph="dns" dim />
          <Gate label="RESOLVE" tone="idle" />
          <div className="grid grid-cols-2 gap-2">
            <NodeCard kicker="APP" title="live" glyph="app" dim />
            <NodeCard kicker="WORKERS" title="live" glyph="wrk" dim />
          </div>
          <Gate label="CUT" tone="cut" sub="not in path" />
          <div className="grid grid-cols-2 gap-2 pb-1">
            <NodeCard kicker="STATE" title="prod-db" glyph="db" dim cut />
            <NodeCard kicker="CREDS" title="live keys" glyph="key" dim cut />
          </div>
        </Cluster>
      </div>

      <div className="mt-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2 border-t border-black/[0.08] pt-2.5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[10px] tracking-tight text-[#797d86]">
          <span className="font-mono text-[8px] tracking-[0.12em]">LEGEND</span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-px w-3.5 bg-[#111111]" />
            twin path
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="h-px w-3.5 border-t border-dashed border-black/40" />
            not in path
          </span>
          <span className="inline-flex items-center gap-1.5">
            <DenyMark size="sm" />
            deny
          </span>
          <span className="inline-flex items-center gap-1.5">
            <span className="size-1.5 rounded-full bg-[#33bf00]" />
            sealed
          </span>
        </div>
        <div className="rounded-[8px] border border-black/[0.10] bg-white px-2.5 py-1.5">
          <div className="font-mono text-[8px] tracking-[0.12em] text-[#797d86]">RUN  pr-4182</div>
          <div
            className={cn(
              "font-mono text-[8px] tracking-[0.12em] transition-colors duration-700",
              gone ? "text-[#33bf00]" : "text-[#111111]",
            )}
          >
            {gone ? "0 REMAINING  ·  DESTROYED" : "4 LIVE  ·  CONTAINED"}
          </div>
        </div>
      </div>
    </div>
  );
}

function TwinDocsLanding() {
  const ref = useRef<HTMLDivElement>(null);
  const acc = useRef(0);
  const lastNow = useRef<number | null>(null);
  const { idle, reduced } = useInViewPlay(ref, 0.22);
  const [beat, setBeat] = useState(0);
  const [remaining, setRemaining] = useState(4);
  const [done, setDone] = useState(false);

  const playing = idle && !reduced && !done;

  usePausedRaf(playing, (now) => {
    const prev = lastNow.current;
    lastNow.current = now;
    const dt = prev == null ? 0 : Math.min(48, now - prev);
    acc.current = Math.min(STILL, acc.current + (dt / 1000) * SPEED);
    const nextBeat = beatIndex(acc.current);
    const nextRemaining = remainingOf(nextBeat);
    setBeat((b) => (b === nextBeat ? b : nextBeat));
    setRemaining((n) => (n === nextRemaining ? n : nextRemaining));
    if (acc.current >= STILL) setDone(true);
  });

  useEffect(() => {
    if (!playing) lastNow.current = null;
  }, [playing]);

  useEffect(() => {
    if (!reduced) return;
    setBeat(3);
    setRemaining(0);
    setDone(true);
  }, [reduced]);

  const gone = remaining === 0;

  return (
    <div ref={ref} className="relative select-none font-sans tracking-tight" aria-hidden>
      <header className="grid h-12 grid-cols-[minmax(0,1fr)_auto_minmax(0,1fr)] items-center gap-3 px-4">
        <span className="truncate text-[12px] tracking-tight text-gray-new-50">Twin</span>
        <nav className="relative grid shrink-0 grid-cols-4">
          {NAV.map((item, i) => (
            <span
              key={item}
              className={cn(
                "relative px-2.5 py-1 text-center text-[12px] tracking-tight transition-colors duration-500",
                i === beat ? "text-black" : "text-gray-new-50",
              )}
            >
              {item}
              <span
                className="absolute inset-x-2 -bottom-px h-px bg-black"
                style={{
                  opacity: i === beat ? 1 : 0,
                  transition: `opacity 500ms ${FILM}`,
                }}
              />
            </span>
          ))}
        </nav>
        <span
          className={cn(
            "truncate text-right text-[12px] font-medium tabular-nums tracking-tight transition-colors duration-700",
            gone ? "text-[#33bf00]" : "text-black",
          )}
        >
          {remaining} remaining
        </span>
      </header>

      <div className="border-t border-black/[0.06] bg-[#f7f7f5] px-3 py-3 sm:px-4 sm:py-4">
        <TwinSchematic beat={beat} gone={gone} />
      </div>
    </div>
  );
}

export function TwinIdeStage() {
  const glow = useRef<HTMLDivElement>(null);
  const { story } = useInViewPlay(glow, 0.15);

  return (
    <div className="relative min-w-0 overflow-hidden rounded-none border border-black/12 bg-[#f7f7f5]">
      <div className="pointer-events-none absolute inset-0" aria-hidden>
        <div
          className="absolute inset-0"
          style={{
            background:
              "radial-gradient(ellipse 52% 48% at 0% 0%, rgba(51,191,0,0.48), transparent 72%), radial-gradient(ellipse 52% 48% at 100% 100%, rgba(0,229,153,0.44), transparent 72%)",
            opacity: story ? 1 : 0.78,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          ref={glow}
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#33bf00 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#00e599 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="auth-honeycomb absolute inset-0"
          style={{
            WebkitMaskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            maskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            opacity: 0.18,
          }}
        />
      </div>

      <div className="relative px-4 py-4 sm:px-6 sm:py-6 lg:px-8 lg:py-7">
        <div className="overflow-hidden rounded-[8px] border border-black/[0.08] bg-white">
          <TwinDocsLanding />
        </div>
      </div>
    </div>
  );
}
