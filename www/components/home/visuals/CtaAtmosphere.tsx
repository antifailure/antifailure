"use client";

import { useId, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_CUBIC } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";

const LOOP_S = 20;
const GRID_DRAW_S = 1.2;
const DESTROY_AT = 4;
const HOLD_AT = 12;
const STRIKE_EVERY_S = 0.7;
const STRIKE_DRAW_S = 0.28;
const FADE_S = 0.5;
const CHAR_S = 0.03;
const PIP_S = 0.4;
const TTL_S = 14;
const LIVE_OPACITY = 0.18;
const DEAD_OPACITY = 0.04;
const GRID_LIVE = 0.04;
const GRID_HOLD = 0.02;
const VW = 1920;
const VH = 944;
const GRID = 72;
const CHECKSUM = "cleanup  sha256:3e91…  14/14  0 orphans";
const REDUCED_T = 19;

const RESOURCES = [
  { key: "workers", label: "workers", journal: "workers", x: "52%", y: "34%" },
  { key: "app", label: "ecs-app", journal: "app", x: "69%", y: "38%" },
  { key: "simulators", label: "sim-stripe", journal: "simulators", x: "54%", y: "51%" },
  { key: "cert", label: "preview-cert", journal: "cert", x: "80%", y: "24%" },
  { key: "dns", label: "dns", journal: "dns", x: "73%", y: "58%" },
  { key: "postgres", label: "rds-twin", journal: "postgres", x: "61%", y: "45%" },
  { key: "vpc", label: "vpc", journal: "vpc", x: "42%", y: "40%" },
] as const;

const V_LINES = Array.from({ length: Math.floor(VW / GRID) + 1 }, (_, i) => i * GRID);
const H_LINES = Array.from({ length: Math.floor(VH / GRID) + 1 }, (_, i) => i * GRID);

const LAST_STRIKE_AT = DESTROY_AT + (RESOURCES.length - 1) * STRIKE_EVERY_S;
const CHECKSUM_AT = LAST_STRIKE_AT + STRIKE_DRAW_S + FADE_S;
const CHECKSUM_DONE_AT = CHECKSUM_AT + CHECKSUM.length * CHAR_S;

function formatTtl(seconds: number) {
  const s = Math.max(0, Math.floor(seconds));
  const hh = String(Math.floor(s / 3600)).padStart(2, "0");
  const mm = String(Math.floor((s % 3600) / 60)).padStart(2, "0");
  const ss = String(s % 60).padStart(2, "0");
  return `${hh}:${mm}:${ss}`;
}

function strikeAt(index: number) {
  return DESTROY_AT + index * STRIKE_EVERY_S;
}

export function CtaAtmosphere() {
  const rootRef = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(rootRef, 0.18);
  const [t, setT] = useState(0);
  const rawId = useId();
  const clipId = `cta-grid-${rawId.replace(/:/g, "")}`;

  usePausedRaf(idle, (_now, elapsed) => {
    const next = Math.round(((elapsed / 1000) % LOOP_S) * 60) / 60;
    setT((prev) => (prev === next ? prev : next));
  });

  const time = reduced ? REDUCED_T : t;
  const hold = time >= HOLD_AT;
  const gridDraw = EASE_OUT_CUBIC(clamp(time / GRID_DRAW_S));
  const gridAlpha = hold
    ? GRID_HOLD + (GRID_LIVE - GRID_HOLD) * (1 - clamp((time - HOLD_AT) / 0.8))
    : GRID_LIVE;
  const ttlSec = Math.max(0, TTL_S - Math.floor(time));
  const ttlLastSecond = ttlSec === 0 && time >= TTL_S && time < TTL_S + 1;
  const checksumChars = reduced
    ? CHECKSUM.length
    : Math.max(0, Math.min(CHECKSUM.length, Math.floor((time - CHECKSUM_AT) / CHAR_S)));
  const checksumDone = checksumChars >= CHECKSUM.length;
  const pip =
    !checksumDone ? "off" : time < CHECKSUM_DONE_AT + PIP_S && !reduced ? "green" : "dim";
  const scanOn = !reduced && hold && time < LOOP_S;
  const scanY = scanOn ? ((time - HOLD_AT) / (LOOP_S - HOLD_AT)) * VH : -8;

  return (
    <div
      ref={rootRef}
      className="pointer-events-none absolute inset-0 z-0 overflow-hidden bg-[#151617] select-none"
      aria-hidden
    >
      <svg
        className="absolute inset-0 hidden h-full w-full max-md:hidden"
        viewBox={`0 0 ${VW} ${VH}`}
        preserveAspectRatio="xMidYMid slice"
      >
        <defs>
          <clipPath id={clipId}>
            <rect x="0" y="0" width={VW * gridDraw} height={VH * gridDraw} />
          </clipPath>
        </defs>
        <g
          clipPath={`url(#${clipId})`}
          fill="none"
          stroke="#fff"
          strokeOpacity={gridAlpha}
          strokeWidth="1"
          vectorEffect="non-scaling-stroke"
        >
          {V_LINES.map((x) => (
            <line key={`v-${x}`} x1={x} y1="0" x2={x} y2={VH} />
          ))}
          {H_LINES.map((y) => (
            <line key={`h-${y}`} x1="0" y1={y} x2={VW} y2={y} />
          ))}
        </g>
        {scanOn ? (
          <rect x="0" y={scanY} width={VW} height="1" fill="#fff" opacity="0.08" />
        ) : null}
      </svg>

      <div
        className="absolute top-[42%] right-[8%] hidden -translate-y-1/2 font-mono text-[8vw] tracking-tighter text-white/[0.06] max-md:hidden"
      >
        run_08f2
      </div>

      <div className="absolute inset-0 hidden max-md:hidden">
        {RESOURCES.map((resource, index) => {
          const at = strikeAt(index);
          const struck = time >= at;
          const line = struck ? clamp((time - at) / STRIKE_DRAW_S) : 0;
          const fade = struck ? clamp((time - at - STRIKE_DRAW_S) / FADE_S) : 0;
          const opacity = struck ? LIVE_OPACITY + (DEAD_OPACITY - LIVE_OPACITY) * fade : LIVE_OPACITY;

          return (
            <span
              key={resource.key}
              className="absolute font-mono text-[11px] tracking-extra-tight whitespace-nowrap"
              style={{ left: resource.x, top: resource.y, opacity, color: "#fff" }}
            >
              {resource.label}
              <span
                className="absolute top-1/2 left-0 h-px bg-white/40"
                style={{ width: `${line * 100}%` }}
              />
            </span>
          );
        })}
      </div>

      <div className="absolute bottom-[26%] left-8 font-mono text-[10px] leading-[14px] tracking-extra-tight text-white/25 tabular-nums max-md:top-[36%] max-md:bottom-auto max-md:left-5">
        <div>destruction journal</div>
        {RESOURCES.map((resource, index) => {
          const visible = reduced || time >= strikeAt(index);
          if (!visible) return null;
          return (
            <div key={resource.key} className="relative w-max">
              {resource.journal}
              <span className="absolute top-1/2 left-0 h-px w-full bg-white/40" />
            </div>
          );
        })}
        <div
          className={cn(
            "mt-2 tabular-nums",
            ttlLastSecond ? "text-green-52" : "text-white/25",
          )}
        >
          ttl  {formatTtl(ttlSec)}
        </div>
        <div className="mt-1.5 flex items-center gap-2 tabular-nums whitespace-pre">
          <span>{CHECKSUM.slice(0, checksumChars)}</span>
          {pip !== "off" ? (
            <span
              className="size-[6px] shrink-0 rounded-full"
              style={{
                background: pip === "green" ? "#34d59a" : "rgba(255,255,255,0.5)",
              }}
            />
          ) : null}
        </div>
      </div>
    </div>
  );
}
