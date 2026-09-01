"use client";

import { useRef, useState } from "react";
import {
  FILM_EASE,
  formatClock,
  MonoLabel,
  Node,
  QueueChip,
  Receipt,
  StatusPill,
  Ticker,
} from "@/components/home/visuals/primitives";
import { Caret } from "@/components/motion/Caret";
import { clamp, lerp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";

const LOOP_MS = 14000;
const FONT = "var(--font-geist-mono), ui-monospace, SFMono-Regular, monospace";

const CP = { x: 6, y: 22, w: 168, h: 278 };
const DP = { x: 198, y: 22, w: 196, h: 278 };
const GAP_X = 174;
const GAP_W = 24;
const VB_W = 400;
const VB_H = 336;

const CONTROL_NODES = ["orgs", "GitHub", "policy", "reports", "billing"] as const;
const DATA_NODES = [
  "snapshot",
  "sanitize",
  "provision",
  "secrets",
  "egress",
  "workload",
  "logs",
  "cleanup",
] as const;

const BARRIER_COPY = "raw snapshots · secrets · request bodies do not enter control plane";
const DESTROY_COPY = "destroyed 14/14";

function cubic(t: number, a: number, b: number) {
  const mt = 1 - t;
  return 3 * mt * mt * t * a + 3 * mt * t * t * b + t * t * t;
}

function ease(t: number) {
  const x = clamp(t);
  let lo = 0;
  let hi = 1;
  for (let i = 0; i < 14; i += 1) {
    const mid = (lo + hi) / 2;
    if (cubic(mid, 0.16, 0.3) < x) lo = mid;
    else hi = mid;
  }
  return cubic((lo + hi) / 2, 1, 1);
}

function span(t: number, a: number, b: number) {
  if (b <= a) return t >= a ? 1 : 0;
  return clamp((t - a) / (b - a));
}

function eased(t: number, a: number, b: number) {
  return ease(span(t, a, b));
}

function typeLine(text: string, progress: number) {
  const n = Math.floor(clamp(progress) * text.length);
  return text.slice(0, n);
}

type Visual = {
  controlDraw: number;
  dataDraw: number;
  fillOp: number;
  nodeOp: number;
  gapLabelOp: number;
  cylOp: number;
  cylDx: number;
  barrierOp: number;
  barrierLabel: string;
  tokenOp: number;
  tokenU: number;
  pathOp: number;
  reportPrint: string;
  agentOp: number;
  reach: number;
  hsFrame: number;
  lockOp: number;
  inboundOp: number;
  inboundErase: number;
  orchOp: number;
  reaperOp: number;
  stuckOp: number;
  stuckGhost: number;
  retryFlash: number;
  destroyCount: number;
  destroyPrint: string;
  destroyOp: number;
  holdOp: number;
  tokenFade: number;
  fleetLit: boolean;
  ttlLabel: string;
  crawl: number;
  hover: boolean;
};

function filmVisual(t: number, loop: number, elapsed: number, hover: boolean): Visual {
  const wallsLocked = loop > 0;
  const controlDraw = wallsLocked ? 1 : eased(t, 0, 0.6);
  const dataDraw = wallsLocked ? 1 : eased(t, 0.6, 1.2);
  const fillOp = wallsLocked ? 1 : lerp(0, 1, eased(t, 0.35, 1.5));
  const nodeOp = wallsLocked ? 1 : lerp(0, 1, eased(t, 0.9, 2.0));
  const gapLabelOp = wallsLocked ? 1 : eased(t, 0.85, 2.0);

  const tokenFade = t >= 6.35 ? 1 - span(t, 6.35, 7.2) : 1;
  const holdFade = t >= 12.4 ? 1 - span(t, 12.4, 13.2) : 1;
  const cylOp = t >= 2 && t < 7.2 ? eased(t, 2.0, 2.45) * tokenFade : 0;
  const lunge = eased(t, 2.5, 3.15);
  const cylDx = t >= 2.5 ? -8 * (t >= 3.15 ? 1 : lunge) : 0;
  const barrierOp = wallsLocked || t >= 3.22 ? 1 : t >= 3.12 ? span(t, 3.12, 3.22) : 0;
  const barrierLabel =
    wallsLocked || t >= 4.7 ? BARRIER_COPY : t >= 3.2 ? typeLine(BARRIER_COPY, span(t, 3.2, 4.7)) : "";

  const tokenU = t >= 3.6 && t < 7.2 ? eased(t, 3.6, 5.2) : 0;
  const tokenOp = t >= 3.55 && t < 7.2 ? eased(t, 3.55, 3.85) * tokenFade : 0;
  const pathOp = t >= 3.5 && t < 7.2 ? eased(t, 3.5, 4.1) * tokenFade : 0;
  // No fidelity score in this film. ReportScene dropped the same invented 87%
  // and said so in its own comment; this scene kept it, and renders on both
  // /product/architecture and /product/safe-state, so the number a visitor
  // actually saw was one nothing measures. There is a real inventory now, and
  // it is deliberately not a fixed number: it names its own denominator and
  // refuses to exist when nothing could be measured. Neither of those survives
  // being burned into an animation, so the claim is made in words on
  // /product/report instead.
  const reportPrint =
    t >= 5.15 && t < 10.7 ? typeLine("rpt_08f2  sha256:7c1a  BLOCK", span(t, 5.15, 6.4)) : "";

  const agentOp = wallsLocked || t >= 5.95 ? 0.9 * holdFade : t >= 5.5 ? eased(t, 5.5, 5.95) : 0;
  const reach = wallsLocked || t >= 7.05 ? 1 : t >= 6.0 ? eased(t, 6.0, 7.05) : 0;
  const hsFrame = t >= 6.05 && t < 7.55 ? Math.min(3, Math.floor(span(t, 6.05, 7.25) * 4)) : -1;
  const lockOp = wallsLocked || t >= 7.55 ? 1 : t >= 7.15 ? eased(t, 7.15, 7.55) : 0;

  const inboundOp = t >= 6.65 && t < 7.85 ? (t < 7.05 ? span(t, 6.65, 7.05) : 1) : 0;
  const inboundErase = t >= 7.15 ? eased(t, 7.15, 7.85) : 0;

  const orchOp = t >= 8.5 && t < 12.6 ? eased(t, 8.5, 9.05) * holdFade : 0;
  const reaperOp = wallsLocked || t >= 9.2 ? 0.9 * holdFade : t >= 8.7 ? eased(t, 8.7, 9.2) : 0;
  const stuckOp = t >= 9.15 && t < 10.62 ? eased(t, 9.15, 9.45) : 0;
  const retryFlash = (t >= 9.55 && t < 9.72) || (t >= 10.15 && t < 10.32) ? 1 : 0;
  const stuckGhost = t >= 10.55 && t < 11.4 ? eased(t, 10.55, 11.05) : 0;
  const destroyCount = t >= 10.2 ? Math.round(lerp(0, 14, eased(t, 10.2, 11.15))) : 0;
  const destroyPrint = t >= 10.75 ? typeLine(DESTROY_COPY, span(t, 10.75, 11.45)) : "";
  const destroyOp = t >= 10.7 ? eased(t, 10.7, 11.1) * holdFade : 0;
  const holdOp = t >= 11.5 ? eased(t, 11.5, 12.15) : 0;

  const fleetLit = elapsed % 2800 < 420;
  const ttlSec = 300 - Math.floor((elapsed % 5000) / 1000);
  const ttlLabel = `00:${formatClock(ttlSec)}`;
  const crawl = (elapsed % 2400) / 2400;

  return {
    controlDraw,
    dataDraw,
    fillOp,
    nodeOp,
    gapLabelOp,
    cylOp,
    cylDx,
    barrierOp,
    barrierLabel,
    tokenOp,
    tokenU,
    pathOp,
    reportPrint,
    agentOp,
    reach,
    hsFrame,
    lockOp,
    inboundOp: inboundOp * (1 - inboundErase),
    inboundErase,
    orchOp,
    reaperOp,
    stuckOp,
    stuckGhost,
    retryFlash,
    destroyCount,
    destroyPrint,
    destroyOp,
    holdOp,
    tokenFade,
    fleetLit,
    ttlLabel,
    crawl,
    hover,
  };
}

function stillVisual(_elapsed: number, hover: boolean): Visual {
  return {
    controlDraw: 1,
    dataDraw: 1,
    fillOp: 1,
    nodeOp: 1,
    gapLabelOp: 1,
    cylOp: 0,
    cylDx: 0,
    barrierOp: 1,
    barrierLabel: BARRIER_COPY,
    tokenOp: 0,
    tokenU: 0,
    pathOp: 0,
    reportPrint: "",
    agentOp: 0.9,
    reach: 1,
    hsFrame: -1,
    lockOp: 1,
    inboundOp: 0,
    inboundErase: 1,
    orchOp: 0,
    reaperOp: 0.85,
    stuckOp: 0,
    stuckGhost: 0,
    retryFlash: 0,
    destroyCount: 14,
    destroyPrint: DESTROY_COPY,
    destroyOp: 1,
    holdOp: 1,
    tokenFade: 1,
    fleetLit: true,
    ttlLabel: "00:05:00",
    crawl: 0,
    hover,
  };
}

function planePath(x: number, y: number, w: number, h: number) {
  return `M${x} ${y}h${w}v${h}h${-w}z`;
}

export function TrustBoundaryScene() {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced, story } = useInViewPlay(ref, 0.18);
  const [hover, setHover] = useState(false);
  const [clock, setClock] = useState({ t: 0, elapsed: 0, loop: 0 });

  usePausedRaf(idle, (_now, elapsed) => {
    const loop = Math.floor(elapsed / LOOP_MS);
    const t = (elapsed % LOOP_MS) / 1000;
    setClock((prev) => {
      if (prev.loop === loop && Math.abs(prev.t - t) < 0.012 && Math.abs(prev.elapsed - elapsed) < 32) {
        return prev;
      }
      return { t, elapsed, loop };
    });
  });

  const still = reduced || !idle;
  const v = still
    ? stillVisual(clock.elapsed, hover)
    : filmVisual(clock.t, clock.loop, clock.elapsed, hover);

  const tokenX = lerp(252, 86, v.tokenU);
  const tokenY = lerp(86, 118, v.tokenU * 0.35);
  const reaperBeat = !still && v.reaperOp > 0.4 ? clock.elapsed % 1150 < 180 : v.reaperOp > 0.4;

  const hs = [
    { ax: 248, ay: 214, bx: 52, by: 108 },
    { ax: 214, ay: 176, bx: 96, by: 128 },
    { ax: 186, ay: 148, bx: 168, by: 142 },
    { ax: 178, ay: 136, bx: 178, by: 136 },
  ][Math.max(0, v.hsFrame)] ?? { ax: 248, ay: 214, bx: 52, by: 108 };

  const showStory = story || reduced;

  return (
    <div
      ref={ref}
      className="relative w-full"
      aria-hidden
      onMouseEnter={() => setHover(true)}
      onMouseLeave={() => setHover(false)}
    >
      <div className="relative aspect-[400/336] w-full overflow-hidden max-xl:hidden">
        <svg
          viewBox={`0 0 ${VB_W} ${VB_H}`}
          className="absolute inset-0 h-full w-full"
          preserveAspectRatio="xMidYMid meet"
        >
          <text
            x={CP.x}
            y="14"
            fill="rgba(0,0,0,0.48)"
            fontFamily={FONT}
            fontSize="7.5"
            letterSpacing="0.06em"
            opacity={v.gapLabelOp}
          >
            HOSTED CONTROL PLANE
          </text>
          <text
            x={DP.x}
            y="14"
            fill="rgba(0,0,0,0.48)"
            fontFamily={FONT}
            fontSize="7.5"
            letterSpacing="0.06em"
            opacity={v.gapLabelOp}
          >
            CUSTOMER-HOSTED DATA PLANE
          </text>

          <path
            d={planePath(CP.x, CP.y, CP.w, CP.h)}
            fill="#fff"
            fillOpacity={v.fillOp * v.controlDraw}
            stroke="#111"
            strokeWidth="1"
            strokeOpacity={0.22}
            vectorEffect="non-scaling-stroke"
            pathLength={1}
            strokeDasharray={1}
            strokeDashoffset={1 - v.controlDraw}
          />
          <path
            d={planePath(DP.x, DP.y, DP.w, DP.h)}
            fill="#eaf3ee"
            fillOpacity={v.fillOp * v.dataDraw}
            stroke="#111"
            strokeWidth="1"
            strokeOpacity={0.22}
            vectorEffect="non-scaling-stroke"
            pathLength={1}
            strokeDasharray={1}
            strokeDashoffset={1 - v.dataDraw}
          />

          <line
            x1={GAP_X + GAP_W / 2}
            y1={CP.y}
            x2={GAP_X + GAP_W / 2}
            y2={CP.y + CP.h}
            stroke="#c41e1e"
            strokeWidth="1"
            vectorEffect="non-scaling-stroke"
            opacity={v.barrierOp}
          />
          <g opacity={v.barrierOp}>
            <rect
              x={GAP_X + GAP_W / 2 - 42}
              y={CP.y - 1}
              width="84"
              height="13"
              fill="#f4f7f5"
            />
            <text
              x={GAP_X + GAP_W / 2}
              y={CP.y + 9}
              textAnchor="middle"
              fill="rgba(0,0,0,0.5)"
              fontFamily={FONT}
              fontSize="7"
              letterSpacing="0.08em"
            >
              trust boundary
            </text>
          </g>

          {CONTROL_NODES.map((label, i) => {
            const y = 44 + i * 24;
            return (
              <g key={label} opacity={v.nodeOp}>
                <circle cx={CP.x + 14} cy={y} r="1.4" fill={label === "reports" && v.tokenU > 0.92 ? "#33bf00" : "rgba(0,0,0,0.28)"} />
                <text
                  x={CP.x + 22}
                  y={y + 3}
                  fill="rgba(0,0,0,0.62)"
                  fontFamily={FONT}
                  fontSize="10"
                  letterSpacing="-0.02em"
                >
                  {label}
                </text>
              </g>
            );
          })}

          {DATA_NODES.map((label, i) => {
            const y = 40 + i * 20;
            const lit = v.cylOp > 0.2 && label === "snapshot";
            return (
              <g key={label} opacity={v.nodeOp}>
                <rect
                  x={DP.x + 10}
                  y={y - 6}
                  width="8"
                  height="8"
                  fill="none"
                  stroke={lit ? "#285D49" : "rgba(0,0,0,0.28)"}
                  strokeWidth="1"
                />
                <text
                  x={DP.x + 24}
                  y={y + 2}
                  fill="rgba(0,0,0,0.62)"
                  fontFamily={FONT}
                  fontSize="10"
                  letterSpacing="-0.02em"
                >
                  {label}
                </text>
              </g>
            );
          })}

          <g opacity={v.cylOp} transform={`translate(${v.cylDx} 0)`}>
            <ellipse cx={362} cy={48} rx="14" ry="5" fill="none" stroke="#285D49" strokeWidth="1" />
            <path
              d="M348 48v14c0 2.8 6.3 5 14 5s14-2.2 14-5V48"
              fill="none"
              stroke="#285D49"
              strokeWidth="1"
            />
            <ellipse cx={362} cy={55} rx="14" ry="5" fill="none" stroke="#285D49" strokeWidth="1" strokeOpacity="0.45" />
            <text
              x={362}
              y={76}
              textAnchor="middle"
              fill="#285D49"
              fontFamily={FONT}
              fontSize="7"
              letterSpacing="-0.02em"
            >
              prod-shaped subset
            </text>
          </g>

          <path
            d="M248 90 C 210 90, 196 108, 186 118 S 130 128, 92 122"
            fill="none"
            stroke="#33bf00"
            strokeWidth="1"
            strokeOpacity={0.85}
            vectorEffect="non-scaling-stroke"
            pathLength={1}
            strokeDasharray={1}
            strokeDashoffset={1 - v.tokenU}
            opacity={v.pathOp}
          />

          {v.barrierLabel ? (
            <text
              x={VB_W / 2}
              y={310}
              textAnchor="middle"
              fill="rgba(0,0,0,0.5)"
              fontFamily={FONT}
              fontSize="8"
              letterSpacing="-0.02em"
            >
              {v.barrierLabel}
            </text>
          ) : null}

          {v.reportPrint ? (
            <text
              x={CP.x + 22}
              y={168}
              fill="rgba(0,0,0,0.5)"
              fontFamily={FONT}
              fontSize="8"
              letterSpacing="-0.02em"
            >
              {v.reportPrint}
            </text>
          ) : null}

          <g opacity={v.agentOp}>
            <rect
              x={DP.x + 12}
              y={232}
              width="92"
              height="22"
              fill="#f7f7f5"
              stroke="#111"
              strokeOpacity="0.22"
              strokeWidth="1"
            />
            <text
              x={DP.x + 58}
              y={246}
              textAnchor="middle"
              fill="rgba(0,0,0,0.62)"
              fontFamily={FONT}
              fontSize="9"
              letterSpacing="-0.02em"
            >
              customer agent
            </text>
            <path
              d="M210 243 L186 243"
              fill="none"
              stroke="#111"
              strokeOpacity="0.35"
              strokeWidth="1"
              vectorEffect="non-scaling-stroke"
              pathLength={1}
              strokeDasharray={1}
              strokeDashoffset={1 - v.reach}
            />
          </g>

          {v.hsFrame >= 0 && v.hsFrame < 3 ? (
            <g opacity={v.agentOp}>
              <rect
                x={hs.ax}
                y={hs.ay}
                width="36"
                height="12"
                fill="#f7f7f5"
                stroke="#111"
                strokeOpacity="0.45"
                strokeWidth="1"
              />
              <text
                x={hs.ax + 18}
                y={hs.ay + 9}
                textAnchor="middle"
                fill="rgba(0,0,0,0.55)"
                fontFamily={FONT}
                fontSize="7"
              >
                cert
              </text>
              <rect
                x={hs.bx}
                y={hs.by}
                width="36"
                height="12"
                fill="#f7f7f5"
                stroke="#111"
                strokeOpacity="0.45"
                strokeWidth="1"
              />
              <text
                x={hs.bx + 18}
                y={hs.by + 9}
                textAnchor="middle"
                fill="rgba(0,0,0,0.55)"
                fontFamily={FONT}
                fontSize="7"
              >
                cert
              </text>
            </g>
          ) : null}

          <g opacity={v.lockOp}>
            <circle cx={186} cy={136} r="5" fill="none" stroke="#33bf00" strokeWidth="1" />
            <path d="M184 136h4M186 134v4" stroke="#33bf00" strokeWidth="1" />
          </g>

          <line
            x1={CP.x + CP.w}
            y1={188}
            x2={DP.x}
            y2={188}
            stroke="rgba(0,0,0,0.28)"
            strokeWidth="1"
            strokeDasharray="3 4"
            strokeDashoffset={v.inboundErase * 28}
            opacity={v.inboundOp}
          />
          {v.inboundOp > 0.05 ? (
            <text
              x={GAP_X + GAP_W / 2}
              y={182}
              textAnchor="middle"
              fill="rgba(0,0,0,0.35)"
              fontFamily={FONT}
              fontSize="7"
              letterSpacing="0.08em"
            >
              inbound
            </text>
          ) : null}

          <g opacity={v.orchOp}>
            <rect
              x={DP.x + 108}
              y={232}
              width="78"
              height="28"
              fill="#f7f7f5"
              stroke="#111"
              strokeOpacity="0.22"
              strokeWidth="1"
            />
            <text
              x={DP.x + 147}
              y={244}
              textAnchor="middle"
              fill="rgba(0,0,0,0.6)"
              fontFamily={FONT}
              fontSize="8"
            >
              Orchestrator
            </text>
            <text
              x={DP.x + 147}
              y={255}
              textAnchor="middle"
              fill="rgba(0,0,0,0.38)"
              fontFamily={FONT}
              fontSize="7"
            >
              run_08f2
            </text>
          </g>

          <g opacity={v.reaperOp}>
            <rect
              x={DP.x + 108}
              y={264}
              width="78"
              height="28"
              fill="#f7f7f5"
              stroke="#111"
              strokeOpacity="0.22"
              strokeWidth="1"
            />
            <circle cx={DP.x + 118} cy={278} r="2.2" fill={reaperBeat ? "#33bf00" : "rgba(0,0,0,0.2)"} />
            <text
              x={DP.x + 152}
              y={276}
              textAnchor="middle"
              fill="rgba(0,0,0,0.6)"
              fontFamily={FONT}
              fontSize="8"
            >
              Reaper
            </text>
            <text
              x={DP.x + 152}
              y={286}
              textAnchor="middle"
              fill="rgba(0,0,0,0.38)"
              fontFamily={FONT}
              fontSize="7"
            >
              independent TTL
            </text>
          </g>

          <g opacity={Math.max(v.stuckOp, v.stuckGhost * 0.45)}>
            <rect
              x={DP.x + 24}
              y={268}
              width="72"
              height="16"
              fill={v.stuckGhost > 0.5 ? "none" : v.retryFlash ? "rgba(196,30,30,0.08)" : "rgba(247,247,245,0.9)"}
              stroke={v.retryFlash ? "#c41e1e" : "rgba(0,0,0,0.4)"}
              strokeWidth="1"
              strokeDasharray={v.stuckGhost > 0.5 ? "2 2" : undefined}
            />
            <text
              x={DP.x + 60}
              y={279}
              textAnchor="middle"
              fill="rgba(0,0,0,0.55)"
              fontFamily={FONT}
              fontSize="8"
              style={{ textDecoration: v.stuckGhost > 0.6 ? "line-through" : undefined }}
            >
              ecs-app stuck
            </text>
          </g>

          <g opacity={v.holdOp}>
            <text
              x={VB_W / 2}
              y={324}
              textAnchor="middle"
              fill="rgba(0,0,0,0.5)"
              fontFamily={FONT}
              fontSize="8"
              letterSpacing="0.06em"
            >
              fail closed  ·  customer-hosted  ·  measurable evidence
            </text>
          </g>
        </svg>

        {v.tokenOp > 0.02 ? (
          <div
            className="pointer-events-none absolute z-[1] flex min-w-[92px] -translate-x-1/2 -translate-y-1/2 flex-col gap-0.5 bg-white px-1.5 py-1 ring-1 ring-black/15"
            style={{
              left: `${(tokenX / VB_W) * 100}%`,
              top: `${(tokenY / VB_H) * 100}%`,
              opacity: v.tokenOp,
              transition: `opacity 120ms ${FILM_EASE}`,
            }}
          >
            <MonoLabel className="text-[9px] text-black/70">rpt_08f2</MonoLabel>
            <MonoLabel className="text-[9px]">sha256:7c1a…</MonoLabel>
            <StatusPill tone="FAIL" />
          </div>
        ) : null}

        <div
          className="pointer-events-none absolute"
          style={{
            left: `${((CP.x + 10) / VB_W) * 100}%`,
            top: `${(248 / VB_H) * 100}%`,
            opacity: v.nodeOp,
          }}
        >
          <Node label="fleet status" lit={v.fleetLit} />
        </div>

        {v.destroyOp > 0.05 ? (
          <div
            className="pointer-events-none absolute"
            style={{
              left: `${((CP.x + 10) / VB_W) * 100}%`,
              top: `${(184 / VB_H) * 100}%`,
              opacity: v.destroyOp,
              width: "38%",
            }}
          >
            <Receipt>
              <span className="tabular-nums">
                {v.destroyPrint || (showStory ? DESTROY_COPY : "")}
                {v.destroyPrint.length > 0 && v.destroyPrint.length < DESTROY_COPY.length ? (
                  <Caret className="bg-black" />
                ) : null}
              </span>
              <div className="mt-0.5 flex items-center gap-1.5">
                <Ticker value={`${v.destroyCount}/14`} className="text-[12px]" />
                <MonoLabel>0 orphans</MonoLabel>
              </div>
            </Receipt>
          </div>
        ) : null}

        {v.stuckOp > 0.15 && v.stuckGhost < 0.5 ? (
          <div
            className="pointer-events-none absolute"
            style={{
              left: `${((DP.x + 24) / VB_W) * 100}%`,
              top: `${(252 / VB_H) * 100}%`,
              opacity: v.stuckOp,
            }}
          >
            <QueueChip blocked={v.retryFlash === 1}>retry {v.retryFlash ? "2" : "1"}</QueueChip>
          </div>
        ) : null}
      </div>
      <div className="hidden overflow-hidden rounded-[12px] border border-black/[0.08] bg-white max-xl:block">
        <div className="p-4">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/45">Hosted control plane</div>
          <ul className="mt-3 flex flex-wrap gap-1.5">
            {CONTROL_NODES.map((label) => (
              <li
                key={label}
                className="rounded-[6px] bg-black/[0.04] px-2 py-1 font-mono text-[12px] tracking-extra-tight text-black/70"
              >
                {label}
              </li>
            ))}
          </ul>
        </div>
        <div className="border-y border-red-700/35 px-4 py-2 font-mono text-[11px] leading-4 tracking-extra-tight text-red-700">
          {BARRIER_COPY}
        </div>
        <div className="bg-[#eaf3ee] p-4">
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-black/45">
            Customer-hosted data plane
          </div>
          <ul className="mt-3 flex flex-wrap gap-1.5">
            {DATA_NODES.map((label) => (
              <li
                key={label}
                className="rounded-[6px] bg-white px-2 py-1 font-mono text-[12px] tracking-extra-tight text-black/70 ring-1 ring-black/10"
              >
                {label}
              </li>
            ))}
          </ul>
          <p className="mt-3 font-mono text-[11px] tabular-nums tracking-extra-tight text-black/50">{DESTROY_COPY}</p>
        </div>
      </div>
    </div>
  );
}
