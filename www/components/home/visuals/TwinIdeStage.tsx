"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";

const STILL = 11.2;
const SPEED = 0.5;
const FILM = "cubic-bezier(0.16, 1, 0.3, 1)";

const NAV = ["Build", "Restore", "Contain", "Destroy"] as const;

const INK = "#111111";
const MUTED = "#797d86";
const CUT = "#C43D3D";
const OK = "#33bf00";
const PAPER = "#ffffff";
const FONT = "var(--font-sans), ui-sans-serif, system-ui, sans-serif";
const MONO = "var(--font-mono), ui-monospace, monospace";

function beatIndex(t: number) {
  if (t >= 9) return 3;
  if (t >= 6) return 2;
  if (t >= 3) return 1;
  return 0;
}

function remainingOf(beat: number) {
  return beat >= 3 ? 0 : 4;
}

function fiducial(x: number, y: number, dx: number, dy: number) {
  return `M${x + dx * 10} ${y} H${x} V${y + dy * 10}`;
}

function Zone({
  x,
  y,
  w,
  h,
  label,
  dashed,
}: {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
  dashed?: boolean;
}) {
  const lw = label.length * 6.4 + 18;
  return (
    <g>
      <rect
        x={x}
        y={y}
        width={w}
        height={h}
        rx="12"
        fill="rgba(0,0,0,0.018)"
        stroke={dashed ? "rgba(0,0,0,0.28)" : INK}
        strokeWidth="1"
        strokeDasharray={dashed ? "4 3" : undefined}
      />
      <rect x={x + 16} y={y - 7} width={lw} height="14" rx="4" fill={PAPER} />
      <text x={x + 25} y={y + 4} fill={MUTED} fontSize="9" fontFamily={MONO} letterSpacing="0.14em">
        {label}
      </text>
    </g>
  );
}

function Port({ x, y, dir }: { x: number; y: number; dir: "n" | "s" | "e" | "w" }) {
  const d =
    dir === "n"
      ? `M${x} ${y} v-4`
      : dir === "s"
        ? `M${x} ${y} v4`
        : dir === "e"
          ? `M${x} ${y} h4`
          : `M${x} ${y} h-4`;
  return <path d={d} stroke={INK} strokeWidth="1.15" />;
}

function Glyph({ kind, x, y, dim }: { kind: "dns" | "app" | "wrk" | "key"; x: number; y: number; dim?: boolean }) {
  const s = dim ? "rgba(0,0,0,0.35)" : INK;
  if (kind === "dns") {
    return (
      <g transform={`translate(${x} ${y})`} stroke={s} strokeWidth="1">
        <circle r="6.5" fill={PAPER} />
        <ellipse rx="3" ry="6.5" />
        <path d="M-6.5 0h13M0 -6.5v13" />
      </g>
    );
  }
  if (kind === "app") {
    return (
      <g transform={`translate(${x} ${y})`} stroke={s} strokeWidth="1" fill={PAPER}>
        <rect x="-7" y="-5" width="11" height="8" rx="1.5" />
        <rect x="-3" y="-2" width="11" height="8" rx="1.5" />
      </g>
    );
  }
  if (kind === "wrk") {
    return (
      <g transform={`translate(${x} ${y})`} stroke={s} strokeWidth="1.15">
        <path d="M-6 -5v10M0 -5v10M6 -5v10" />
      </g>
    );
  }
  return (
    <g transform={`translate(${x} ${y})`} stroke={s} strokeWidth="1.15" fill="none">
      <circle cx="-3" cy="0" r="3.2" />
      <path d="M0 0h8M5 0v3M8 0v3" strokeLinecap="round" />
    </g>
  );
}

function Box({
  x,
  y,
  w,
  h,
  label,
  kicker,
  glyph,
  dim,
  live,
}: {
  x: number;
  y: number;
  w: number;
  h: number;
  label: string;
  kicker: string;
  glyph: "dns" | "app" | "wrk" | "key";
  dim?: boolean;
  live?: boolean;
}) {
  const stroke = dim ? "rgba(0,0,0,0.3)" : INK;
  return (
    <g>
      <rect x={x} y={y} width={w} height={h} rx="8" fill={PAPER} stroke={stroke} strokeWidth="1.15" />
      <text x={x + 10} y={y + 14} fill={MUTED} fontSize="7.5" fontFamily={MONO} letterSpacing="0.12em">
        {kicker}
      </text>
      <text x={x + 10} y={y + 30} fill={dim ? MUTED : INK} fontSize="12" fontFamily={FONT}>
        {label}
      </text>
      <Glyph kind={glyph} x={x + w - 18} y={y + 22} dim={dim} />
      {live ? <circle cx={x + w - 10} cy={y + 10} r="2.6" fill={OK} /> : null}
    </g>
  );
}

function Cylinder({
  cx,
  cy,
  rx,
  ry,
  h,
  label,
  kicker,
  dim,
  cut,
}: {
  cx: number;
  cy: number;
  rx: number;
  ry: number;
  h: number;
  label: string;
  kicker: string;
  dim?: boolean;
  cut?: boolean;
}) {
  const stroke = cut ? CUT : dim ? "rgba(0,0,0,0.3)" : INK;
  const top = cy - h / 2;
  const bot = cy + h / 2;
  return (
    <g>
      <path
        d={`M${cx - rx} ${top + ry} V${bot - ry} A${rx} ${ry} 0 0 0 ${cx + rx} ${bot - ry} V${top + ry}`}
        fill={PAPER}
        stroke={stroke}
        strokeWidth="1.15"
      />
      <ellipse cx={cx} cy={bot - ry} rx={rx} ry={ry} fill={PAPER} stroke={stroke} strokeWidth="1.15" />
      <ellipse cx={cx} cy={top + ry} rx={rx} ry={ry} fill={PAPER} stroke={stroke} strokeWidth="1.15" />
      <Caption x={cx - rx - 16} y={cy + 3} text={kicker} fill={MUTED} anchor="end" mono />
      <Caption x={cx} y={bot + 26} text={label} fill={cut ? CUT : dim ? MUTED : INK} anchor="middle" />
    </g>
  );
}

function Cut({ x, y, s = 6.5 }: { x: number; y: number; s?: number }) {
  return (
    <g>
      <circle cx={x} cy={y} r={s + 3.5} fill={PAPER} />
      <line x1={x - s} y1={y - s} x2={x + s} y2={y + s} stroke={CUT} strokeWidth="1.25" />
      <line x1={x + s} y1={y - s} x2={x - s} y2={y + s} stroke={CUT} strokeWidth="1.25" />
    </g>
  );
}

function Caption({
  x,
  y,
  text,
  fill = MUTED,
  anchor = "start",
  mono,
}: {
  x: number;
  y: number;
  text: string;
  fill?: string;
  anchor?: "start" | "middle" | "end";
  mono?: boolean;
}) {
  const w = text.length * (mono ? 5.8 : 5.1) + 10;
  const left = anchor === "middle" ? x - w / 2 : anchor === "end" ? x - w : x;
  return (
    <g>
      <rect x={left} y={y - 11} width={w} height="16" rx="4" fill={PAPER} />
      <text
        x={x}
        y={y}
        textAnchor={anchor}
        fill={fill}
        fontSize={mono ? 8 : 9}
        fontFamily={mono ? MONO : FONT}
        letterSpacing={mono ? "0.08em" : undefined}
      >
        {text}
      </text>
    </g>
  );
}

function TwinSchematic({ beat, gone }: { beat: number; gone: boolean }) {
  const sealed = (from: number) => beat >= from && !gone;
  const dash = "4 3";
  const shift = 492;

  return (
    <svg viewBox="0 0 940 528" className="h-auto w-full overflow-visible" fill="none" aria-hidden>
      <path d={fiducial(10, 10, 1, 1)} stroke={INK} strokeWidth="0.9" />
      <path d={fiducial(930, 10, -1, 1)} stroke={INK} strokeWidth="0.9" />
      <path d={fiducial(10, 518, 1, -1)} stroke={INK} strokeWidth="0.9" />
      <path d={fiducial(930, 518, -1, -1)} stroke={INK} strokeWidth="0.9" />

      <text x="24" y="28" fill={MUTED} fontSize="9" fontFamily={MONO} letterSpacing="0.16em">
        FIG. 01
      </text>
      <text x="88" y="28" fill={INK} fontSize="9" fontFamily={MONO} letterSpacing="0.12em">
        ISOLATED TWIN
      </text>
      <text x="210" y="28" fill={MUTED} fontSize="9" fontFamily={FONT}>
        production not in path
      </text>
      <text x="916" y="28" textAnchor="end" fill={MUTED} fontSize="9" fontFamily={MONO} letterSpacing="0.12em">
        SHEET 01 / 01
      </text>
      <path d="M24 36 H916" stroke="rgba(0,0,0,0.12)" strokeWidth="1" />

      <g>
        <Zone x={20} y={54} w={404} h={392} label="ISOLATED TWIN" />
        <rect
          x={44}
          y={84}
          width={356}
          height={308}
          rx="10"
          fill="none"
          stroke="rgba(0,0,0,0.16)"
          strokeWidth="0.9"
          strokeDasharray={dash}
        />
        <rect x={56} y={77} width={92} height="12" rx="3" fill={PAPER} />
        <text x={62} y={87} fill={MUTED} fontSize="8" fontFamily={MONO} letterSpacing="0.12em">
          CLONE-LOCAL
        </text>

        <path d="M220 132 V162 H112 Q104 162 104 170 V186" stroke={INK} strokeWidth="1.15" />
        <path d="M220 162 H328 Q336 162 336 170 V186" stroke={INK} strokeWidth="1.15" />
        <path d="M112 226 V268 H220 V296" stroke={INK} strokeWidth="1.15" />
        <path d="M328 226 V268 H278" stroke={INK} strokeWidth="1.15" />
        <path d="M328 268 H340 V300" stroke={INK} strokeWidth="1.15" />

        <Port x={220} y={132} dir="s" />
        <Port x={112} y={186} dir="n" />
        <Port x={328} y={186} dir="n" />
        <Port x={112} y={226} dir="s" />
        <Port x={328} y={226} dir="s" />
        <Port x={220} y={296} dir="n" />
        <Port x={340} y={300} dir="n" />

        <Caption x={248} y={148} text="resolve" mono />
        <Caption x={220} y={208} text="MASK · KEEP" anchor="middle" mono />

        <Box x={140} y={92} w={160} h={40} kicker="DNS" label="clone-local" glyph="dns" live={sealed(0)} />
        <Box x={52} y={186} w={120} h={40} kicker="APP" label="candidate" glyph="app" live={!gone} />
        <Box x={268} y={186} w={120} h={40} kicker="WORKERS" label="isolated" glyph="wrk" live={!gone} />
        <Cylinder cx={220} cy={324} rx={44} ry={8} h={56} kicker="STATE" label="postgres" />
        <Box x={286} y={300} w={108} h={40} kicker="CREDS" label="replaced" glyph="key" live={sealed(1)} />

        <g>
          <rect x={36} y={408} width={44} height="14" rx="4" fill={PAPER} stroke="rgba(0,0,0,0.12)" />
          <text x={42} y={418} fill={MUTED} fontSize="8" fontFamily={MONO} letterSpacing="0.1em">
            SEALS
          </text>
          {[
            { x: 88, label: "clone-local DNS", on: sealed(0) },
            { x: 214, label: "secrets replaced", on: sealed(1) },
            { x: 336, label: "no egress", on: sealed(2) },
          ].map((s) => (
            <g key={s.label}>
              <rect
                x={s.x}
                y={406}
                width={s.label.length * 6.1 + 22}
                height="18"
                rx="6"
                fill={s.on ? "rgba(51,191,0,0.1)" : PAPER}
                stroke={s.on ? OK : "rgba(0,0,0,0.14)"}
              />
              <circle cx={s.x + 9} cy={415} r="2.2" fill={s.on ? OK : "rgba(0,0,0,0.18)"} />
              <text x={s.x + 16} y={419} fill={s.on ? INK : MUTED} fontSize="9" fontFamily={FONT}>
                {s.label}
              </text>
            </g>
          ))}
        </g>
      </g>

      <path d="M424 210 H448" stroke={INK} strokeWidth="1.15" />
      <Cut x={468} y={210} />
      <path d="M488 210 H512" stroke="rgba(0,0,0,0.3)" strokeWidth="1" strokeDasharray={dash} />
      <Caption x={468} y={186} text="DENY" fill={CUT} anchor="middle" mono />
      <Caption x={468} y={240} text="no route" fill={CUT} anchor="middle" />

      <g opacity="0.5">
        <Zone x={20 + shift} y={54} w={404} h={392} label="PRODUCTION" dashed />
        <rect
          x={44 + shift}
          y={84}
          width={356}
          height={308}
          rx="10"
          fill="none"
          stroke="rgba(0,0,0,0.18)"
          strokeWidth="0.9"
          strokeDasharray={dash}
        />
        <rect x={56 + shift} y={77} width={48} height="12" rx="3" fill={PAPER} />
        <text x={62 + shift} y={87} fill={MUTED} fontSize="8" fontFamily={MONO} letterSpacing="0.12em">
          LIVE
        </text>

        <path
          d={`M${220 + shift} 132 V162 H${112 + shift} Q${104 + shift} 162 ${104 + shift} 170 V186`}
          stroke="rgba(0,0,0,0.45)"
          strokeWidth="1"
          strokeDasharray={dash}
        />
        <path
          d={`M${220 + shift} 162 H${328 + shift} Q${336 + shift} 162 ${336 + shift} 170 V186`}
          stroke="rgba(0,0,0,0.45)"
          strokeWidth="1"
          strokeDasharray={dash}
        />
        <path
          d={`M${112 + shift} 226 V268 H${220 + shift} V296`}
          stroke="rgba(0,0,0,0.45)"
          strokeWidth="1"
          strokeDasharray={dash}
        />
        <path
          d={`M${328 + shift} 226 V268 H${278 + shift}`}
          stroke="rgba(0,0,0,0.45)"
          strokeWidth="1"
          strokeDasharray={dash}
        />
        <path
          d={`M${328 + shift} 268 H${340 + shift} V300`}
          stroke="rgba(0,0,0,0.45)"
          strokeWidth="1"
          strokeDasharray={dash}
        />

        <Box x={140 + shift} y={92} w={160} h={40} kicker="DNS" label="public resolver" glyph="dns" dim />
        <Box x={52 + shift} y={186} w={120} h={40} kicker="APP" label="live" glyph="app" dim />
        <Box x={268 + shift} y={186} w={120} h={40} kicker="WORKERS" label="live" glyph="wrk" dim />
        <Cylinder cx={220 + shift} cy={324} rx={44} ry={8} h={56} kicker="STATE" label="prod-db" dim cut />
        <Box x={286 + shift} y={300} w={108} h={40} kicker="CREDS" label="live keys" glyph="key" dim />
        <Cut x={220 + shift} y={280} s={5} />
        <Cut x={340 + shift} y={282} s={5} />

        <rect x={36 + shift} y={406} width={84} height="18" rx="6" fill={PAPER} stroke="rgba(196,61,61,0.35)" />
        <text x={44 + shift} y={419} fill={CUT} fontSize="8" fontFamily={MONO} letterSpacing="0.1em">
          NOT IN PATH
        </text>
        <Caption x={128 + shift} y={419} text="prod-db cut · live keys unreachable" />
      </g>

      <path d="M24 458 H700" stroke="rgba(0,0,0,0.1)" />
      <text x="24" y="486" fill={MUTED} fontSize="8" fontFamily={MONO} letterSpacing="0.1em">
        LEGEND
      </text>
      <path d="M80 482 h16" stroke={INK} strokeWidth="1.15" />
      <text x="100" y="486" fill={MUTED} fontSize="9" fontFamily={FONT}>
        twin path
      </text>
      <path d="M164 482 h16" stroke="rgba(0,0,0,0.4)" strokeWidth="1" strokeDasharray={dash} />
      <text x="184" y="486" fill={MUTED} fontSize="9" fontFamily={FONT}>
        not in path
      </text>
      <Cut x={264} y={480} s={4.5} />
      <text x="274" y="486" fill={MUTED} fontSize="9" fontFamily={FONT}>
        deny
      </text>
      <circle cx="324" cy="480" r="2.4" fill={OK} />
      <text x="332" y="486" fill={MUTED} fontSize="9" fontFamily={FONT}>
        sealed
      </text>

      <rect x={728} y={466} width={188} height="34" rx="8" fill={PAPER} stroke="rgba(0,0,0,0.12)" />
      <text x={740} y={480} fill={MUTED} fontSize="8" fontFamily={MONO} letterSpacing="0.12em">
        RUN  pr-4182
      </text>
      <text x={740} y={494} fill={gone ? OK : INK} fontSize="8" fontFamily={MONO} letterSpacing="0.12em">
        {gone ? "0 REMAINING  ·  DESTROYED" : "4 LIVE  ·  CONTAINED"}
      </text>
    </svg>
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

      <div className="border-t border-black/[0.06] px-3 pb-3 pt-2 sm:px-5 sm:pb-5 sm:pt-3">
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
