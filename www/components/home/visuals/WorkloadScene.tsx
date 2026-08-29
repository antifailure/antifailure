"use client";

import { useRef, useState } from "react";
import { clamp, EASE_OUT_QUART, lerp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { FILM_EASE, seededNoise } from "@/components/home/visuals/primitives";

const LOOP = 9.2;
const FADE = 0.4;
const HOLD = 8;
const VW = 960;
const VH = 540;
const CLICK_AT = 2.2;
const CLICK2_AT = 2.29;
const COMPILE_AT = 2.5;
const MERGE_DONE = 4.0;
const DRAW_SEC = 6.2;
const STAMP_AT = 6.5;
const MIX_X = 268;
const CHART = { x: 300, y: 188, w: 580, h: 228 };
const LANES = [
  { key: "obs", label: "Observed", y: 86, color: "#285D49" },
  { key: "det", label: "Deterministic", y: 128, color: "#33bf00" },
  { key: "explore", label: "Exploratory", y: 170, color: "#00e599" },
] as const;
const DOTS_PER = 6;
const TRACE_N = 72;

function latY(ms: number) {
  const u = clamp((ms - 500) / 6800);
  return CHART.y + CHART.h * (1 - u);
}

function buildTraces() {
  const baseline: { x: number; y: number }[] = [];
  const candidate: { x: number; y: number }[] = [];
  for (let i = 0; i < TRACE_N; i += 1) {
    const u = i / (TRACE_N - 1);
    const grain = (seededNoise(i, 8) - 0.5) * 70;
    const bms = 820 + grain;
    let cms = bms + (seededNoise(i, 21) - 0.5) * 16;
    if (u > 0.55) {
      const d = (u - 0.55) / 0.45;
      cms = lerp(bms, 6900, EASE_OUT_QUART(clamp(d)));
    }
    const x = CHART.x + u * CHART.w;
    baseline.push({ x, y: latY(bms) });
    candidate.push({ x, y: latY(cms) });
  }
  return { baseline, candidate };
}

const TRACES = buildTraces();

function toPath(pts: { x: number; y: number }[], until: number) {
  const n = Math.max(2, Math.floor(until * (pts.length - 1)) + 1);
  return pts
    .slice(0, n)
    .map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)} ${p.y.toFixed(1)}`)
    .join(" ");
}

function useFilmClock(playing: boolean, reduced: boolean, stillT: number) {
  const [frame, setFrame] = useState({ t: 0, fade: 0 });

  usePausedRaf(playing && !reduced, (_now, elapsedMs) => {
    const elapsed = elapsedMs / 1000;
    const period = LOOP + FADE;
    const cycle = elapsed % period;
    if (cycle < LOOP) setFrame({ t: cycle, fade: 0 });
    else setFrame({ t: LOOP, fade: (cycle - LOOP) / FADE });
  });

  if (reduced) return { t: stillT, fade: 0 };
  return frame;
}

function LaneDots({ t, merge }: { t: number; merge: number }) {
  const nodes = [];
  for (let lane = 0; lane < LANES.length; lane += 1) {
    const src = LANES[lane];
    for (let i = 0; i < DOTS_PER; i += 1) {
      const period = 4.4;
      const local = (t + i * 0.55 + lane * 0.18) % period;
      if (local < 0.05 || local > period - 0.08) continue;
      const x = 48 + (local / period) * (MIX_X - 48);
      let y: number = src.y;
      if (src.key === "explore" && merge > 0) {
        y = lerp(src.y, LANES[1].y, merge);
      }
      const compiling = src.key === "explore" && merge > 0.15;
      nodes.push(
        <circle
          key={`${src.key}-${i}`}
          cx={x}
          cy={y}
          r={4.2}
          fill={src.color}
          opacity={compiling ? 0.45 + 0.55 * (1 - merge) : 0.92}
        />,
      );
    }
  }
  return <g>{nodes}</g>;
}

function ClickPulse({ t, at }: { t: number; at: number }) {
  const age = t - at;
  if (age < 0 || age > 0.55) return null;
  const r = 4 + age * 28;
  const op = 1 - age / 0.55;
  return (
    <circle
      cx={168}
      cy={LANES[2].y}
      r={r}
      fill="none"
      stroke="#00e599"
      strokeWidth="1.5"
      opacity={op}
    />
  );
}

export function WorkloadScene() {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(ref, 0.18);
  const { t, fade } = useFilmClock(idle, reduced, HOLD);
  const viewT = Math.min(t, HOLD);
  const merge = viewT < COMPILE_AT ? 0 : clamp((viewT - COMPILE_AT) / (MERGE_DONE - COMPILE_AT));
  const draw = clamp(viewT / DRAW_SEC);
  const basePath = toPath(TRACES.baseline, draw);
  const candPath = toPath(TRACES.candidate, draw);
  const bandUntil = draw > 0.55 ? draw : 0;
  const bandN = Math.max(2, Math.floor(bandUntil * (TRACE_N - 1)) + 1);
  const divI = Math.floor(0.55 * (TRACE_N - 1));
  const bandSlice = TRACES.candidate.slice(divI, bandN);
  const bandBack = TRACES.baseline.slice(divI, bandN).slice().reverse();
  const band =
    bandSlice.length > 1
      ? [
          ...bandSlice.map((p, i) => `${i ? "L" : "M"}${p.x.toFixed(1)} ${p.y.toFixed(1)}`),
          ...bandBack.map((p) => `L${p.x.toFixed(1)} ${p.y.toFixed(1)}`),
          "Z",
        ].join(" ")
      : "";
  const stamped = viewT >= STAMP_AT;

  return (
    <div ref={ref} className="relative w-full">
      <div className="hidden max-xl:block">
        <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
          <div className="flex items-center justify-between gap-3 border-b border-black/[0.08] px-4 py-2.5">
            <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-black/45">
              workload studio
            </span>
            <span className="font-mono text-[12px] tracking-extra-tight text-[#C43D3D]">6.9s</span>
          </div>
          <ul>
            {LANES.map((lane) => (
              <li
                key={lane.key}
                className="flex items-center gap-3 border-b border-black/[0.06] px-4 py-3 last:border-0"
              >
                <span className="size-2 shrink-0 rounded-full" style={{ background: lane.color }} />
                <span className="text-[14px] tracking-extra-tight text-black">{lane.label}</span>
              </li>
            ))}
          </ul>
        </div>
      </div>
      <div className="relative aspect-[16/9] w-full overflow-hidden bg-[#f7f7f5] outline outline-1 outline-black/20 max-xl:hidden">
      <div className="absolute inset-0 select-none" aria-hidden>
        <svg viewBox={`0 0 ${VW} ${VH}`} className="absolute inset-0 h-full w-full" aria-hidden>
          <rect width={VW} height={VH} fill="#f7f7f5" />
          {LANES.map((lane) => (
            <g key={lane.key}>
              <text
                x={48}
                y={lane.y - 14}
                fill="#111"
                fontSize="10"
                fontFamily="var(--font-mono), ui-monospace, monospace"
                opacity={lane.key === "explore" ? 0.55 + 0.45 * (1 - merge) : 0.7}
              >
                {lane.label}
              </text>
              <line
                x1={48}
                x2={MIX_X}
                y1={lane.y}
                y2={lane.y}
                stroke="rgba(0,0,0,0.08)"
                strokeWidth="1"
              />
            </g>
          ))}
          <LaneDots t={viewT} merge={merge} />
          <ClickPulse t={viewT} at={CLICK_AT} />
          <ClickPulse t={viewT} at={CLICK2_AT} />
          <rect
            x={CHART.x}
            y={CHART.y}
            width={CHART.w}
            height={CHART.h}
            fill="#fff"
            stroke="rgba(0,0,0,0.1)"
            strokeWidth="1"
          />
          {band ? <path d={band} fill="rgba(220,38,38,0.12)" /> : null}
          <path d={basePath} fill="none" stroke="#111" strokeWidth="1.5" opacity="0.45" />
          <path d={candPath} fill="none" stroke="#dc2626" strokeWidth="1.75" />
        </svg>
        <div
          className="absolute right-7 bottom-8 text-right font-mono text-[56px] leading-none tracking-tighter text-red-700 tabular-nums max-md:right-4 max-md:bottom-5 max-md:text-[40px]"
          style={{
            opacity: stamped ? 1 : 0,
            transform: stamped ? "translateY(0)" : "translateY(6px)",
            transition: `opacity 220ms ${FILM_EASE}, transform 220ms ${FILM_EASE}`,
          }}
        >
          6.9s
        </div>
        <div className="pointer-events-none absolute inset-0 bg-[#f7f7f5]" style={{ opacity: fade }} />
      </div>
      </div>
    </div>
  );
}
