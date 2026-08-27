"use client";

import { useEffect, useId, useRef, useState } from "react";
import { motion } from "motion/react";
import { EASE, EASE_OUT_CUBIC } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";

function DotChartIcon({ play }: { play: boolean }) {
  const cells = [
    [0, 0, 0, 0.35],
    [0, 0, 0.4, 0.7],
    [0, 0.35, 0.65, 1],
    [0.2, 0.55, 0.85, 1],
  ];
  return (
    <div className="mx-auto mb-6 grid grid-cols-4 gap-1.5">
      {cells.flat().map((o, i) => (
        <motion.span
          key={i}
          className="h-1.5 w-1.5 rounded-full bg-black"
          initial={{ opacity: 0.12, scale: 0.6 }}
          animate={play ? { opacity: o || 0.12, scale: 1 } : { opacity: 0.12, scale: 0.6 }}
          transition={{ duration: 0.35, delay: i * 0.03, ease: EASE }}
        />
      ))}
    </div>
  );
}

const STAIRS = [
  { x: 56, h: 28, label: "nullable" },
  { x: 128, h: 56, label: "backfill" },
  { x: 200, h: 88, label: "dual-read" },
  { x: 272, h: 118, label: "cutover" },
];

export function MigrationSection() {
  const [mode, setMode] = useState<"incidents" | "safer">("incidents");
  const [run, setRun] = useState(0);
  const ref = useRef<HTMLDivElement>(null);
  const headRef = useRef<HTMLDivElement>(null);
  const { story, reduced } = useInViewPlay(ref, 0.22);
  const { story: headStory } = useInViewPlay(headRef, 0.3);
  const [lock, setLock] = useState(reduced ? 27.4 : 0);
  const [p99, setP99] = useState(reduced ? 6.9 : 0.82);
  const [stairs, setStairs] = useState(reduced ? 4 : 0);
  const [tipL, setTipL] = useState(reduced);
  const [tipR, setTipR] = useState(reduced);
  const hatchL = useId();
  const hatchR = useId();

  useEffect(() => {
    if (!story) {
      if (reduced) return;
      setLock(0);
      setP99(0.82);
      setStairs(0);
      setTipL(false);
      setTipR(false);
      return;
    }
    if (reduced) {
      setLock(27.4);
      setP99(6.9);
      setStairs(4);
      setTipL(true);
      setTipR(true);
      return;
    }
    setLock(0);
    setP99(0.82);
    setStairs(0);
    setTipL(false);
    setTipR(false);
    const t0 = performance.now();
    let raf = 0;
    const tick = (now: number) => {
      const u = Math.min(1, (now - t0) / 1600);
      const e = EASE_OUT_CUBIC(u);
      setLock(27.4 * e);
      setP99(0.82 + (6.9 - 0.82) * e);
      if (u < 1) raf = requestAnimationFrame(tick);
      else setTipL(true);
    };
    raf = requestAnimationFrame(tick);
    const timers = [1700, 2200, 2700, 3200].map((ms, i) =>
      window.setTimeout(() => {
        setStairs(i + 1);
        if (i === 3) setTipR(true);
      }, ms),
    );
    return () => {
      cancelAnimationFrame(raf);
      timers.forEach(clearTimeout);
    };
  }, [story, reduced, run]);

  const spike = lock / 27.4;
  const peakY = 168 - 132 * spike;

  return (
    <section id="migration" className="bg-[#e8f1ed] text-black">
      <div ref={headRef} className="px-8 pb-4 pt-10 lg:px-16">
        <div className="min-w-0 px-4 text-center lg:pl-[200px]">
          <DotChartIcon play={headStory || reduced} />
          <h2 className="mx-auto max-w-[860px] text-[40px] font-semibold leading-[1.18] tracking-[-0.03em] md:text-[48px]">
            <span className="text-black">Migration safety. </span>
            <span className="text-black/45">
              Catch exclusive locks, query-plan regressions, and unsafe rollbacks before they reach
              production.
            </span>
          </h2>
          <div className="mx-auto mt-8 inline-flex overflow-hidden rounded-sm border border-black/20 text-[13.5px]">
            <button
              type="button"
              onClick={() => {
                setMode("incidents");
                setRun((n) => n + 1);
              }}
              className={`px-4 py-2 ${mode === "incidents" ? "bg-white text-black" : "bg-black/5 text-black/45"}`}
            >
              Avoid incidents
            </button>
            <button
              type="button"
              onClick={() => {
                setMode("safer");
                setRun((n) => n + 1);
              }}
              className={`px-4 py-2 ${mode === "safer" ? "bg-white text-black" : "bg-black/5 text-black/45"}`}
            >
              Safer pattern
            </button>
          </div>
        </div>
      </div>

      <div
        ref={ref}
        className="sage-grid relative mx-8 mb-8 mt-6 grid min-h-[420px] grid-cols-1 border-t border-black/10 md:grid-cols-2 lg:mx-16"
      >
        <div
          className={`flex flex-col border-black/10 px-6 pb-8 pt-6 md:border-r ${mode === "safer" ? "opacity-55" : ""}`}
        >
          <div className="text-[12px] text-black/40">Without expand-and-contract</div>
          <svg viewBox="0 0 420 220" className="mt-4 h-[220px] w-full">
            <defs>
              <pattern id={hatchL} patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(-45)">
                <rect width="8" height="8" fill="rgba(220,38,38,0.12)" />
                <line x1="0" y1="0" x2="0" y2="8" stroke="#dc2626" strokeWidth="1.4" />
              </pattern>
            </defs>
            <line x1="48" x2="400" y1="168" y2="168" stroke="rgba(0,0,0,0.22)" />
            <line x1="48" x2="48" y1="24" y2="168" stroke="rgba(0,0,0,0.22)" />
            <text x="8" y="172" fill="rgba(0,0,0,0.4)" fontSize="11">
              0s
            </text>
            <text x="4" y="22" fill="rgba(0,0,0,0.4)" fontSize="11">
              lock time
            </text>
            <text x="4" y="40" fill="rgba(0,0,0,0.4)" fontSize="11">
              27.4s
            </text>
            <path
              d={`M48 168 L120 ${168 - 8 * spike} L210 ${peakY} L300 ${168 - 10 * spike} L400 168`}
              fill={`url(#${hatchL})`}
            />
            <path
              d={`M48 168 L120 ${168 - 8 * spike} L210 ${peakY} L300 ${168 - 10 * spike} L400 168`}
              fill="#dc2626"
              fillOpacity="0.1"
              stroke="#dc2626"
              strokeWidth="1.7"
            />
            <text x="188" y="198" fill="rgba(0,0,0,0.45)" fontSize="11">
              lock duration
            </text>
          </svg>
          <div
            className="mt-auto rounded-md border border-red-500 bg-white p-3 text-left text-[12px] shadow-sm"
            style={{
              opacity: tipL ? 1 : 0.35,
              transition: "opacity 0.4s cubic-bezier(0.16,1,0.3,1)",
            }}
          >
            <div className="font-medium text-red-600">ACCESS EXCLUSIVE lock</div>
            <div className="mt-1 text-black/70">
              Held on <span className="rounded bg-red-100 px-1">subscriptions {lock.toFixed(1)}s</span>
            </div>
            <div className="mt-1 text-black/55">Checkout p99 {p99.toFixed(1)}s</div>
          </div>
        </div>

        <div className={`flex flex-col px-6 pb-8 pt-6 ${mode === "incidents" ? "opacity-55" : ""}`}>
          <div className="text-[12px] text-black/40">With staged migration</div>
          <svg viewBox="0 0 420 220" className="mt-4 h-[220px] w-full">
            <defs>
              <pattern id={hatchR} patternUnits="userSpaceOnUse" width="8" height="8" patternTransform="rotate(-45)">
                <rect width="8" height="8" fill="rgba(51,191,0,0.1)" />
                <line x1="0" y1="0" x2="0" y2="8" stroke="#00921b" strokeWidth="1.3" />
              </pattern>
            </defs>
            <line x1="48" x2="400" y1="168" y2="168" stroke="rgba(0,0,0,0.22)" />
            <line x1="48" x2="48" y1="24" y2="168" stroke="rgba(0,0,0,0.22)" />
            {STAIRS.map((s, i) => {
              const on = stairs > i;
              return (
                <g key={s.label}>
                  <motion.rect
                    x={s.x}
                    width="64"
                    fill={`url(#${hatchR})`}
                    stroke="#00921b"
                    strokeWidth="1.5"
                    initial={{ y: 168, height: 0 }}
                    animate={{ y: on ? 168 - s.h : 168, height: on ? s.h : 0 }}
                    transition={{ duration: 0.45, ease: EASE }}
                  />
                  <text
                    x={s.x + 32}
                    y="196"
                    textAnchor="middle"
                    fill="rgba(0,0,0,0.5)"
                    fontSize="11"
                    opacity={on ? 1 : 0.35}
                  >
                    {s.label}
                  </text>
                </g>
              );
            })}
          </svg>
          <div
            className="mt-auto rounded-md border border-[#00921b] bg-white p-3 text-left text-[12px] shadow-sm"
            style={{
              opacity: tipR ? 1 : 0.35,
              transition: "opacity 0.4s cubic-bezier(0.16,1,0.3,1)",
            }}
          >
            <div className="font-medium text-[#00921b]">Safer pattern recommended</div>
            <div className="mt-1 text-black/70">
              Nullable column, batched backfill, dual-read, then cutover.
            </div>
            <div className="mt-1 text-black/55">
              {stairs >= 4 ? "Rollback remains feasible" : "Dual-read pending"}
            </div>
          </div>
        </div>
      </div>
    </section>
  );
}
