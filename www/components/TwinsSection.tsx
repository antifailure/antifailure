"use client";

import { useEffect, useRef, useState } from "react";
import { motion } from "motion/react";
import { EASE } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";

function ClusterIcon({ play }: { play: boolean }) {
  const dots = [
    [10, 18],
    [22, 8],
    [34, 16],
    [18, 28],
    [30, 30],
    [42, 24],
    [8, 32],
  ];
  return (
    <svg viewBox="0 0 52 40" className="mx-auto mb-5 h-8 w-10">
      {dots.map(([x, y], i) => (
        <motion.circle
          key={i}
          cx={x}
          cy={y}
          r="2.2"
          fill="#0a0a0a"
          initial={{ opacity: 0 }}
          animate={play ? { opacity: 0.35 + (i % 3) * 0.2 } : { opacity: 0 }}
          transition={{ duration: 0.4, delay: i * 0.05, ease: EASE }}
        />
      ))}
    </svg>
  );
}

function at(x: number, y: number) {
  return { left: `${(x / 1100) * 100}%`, top: `${(y / 500) * 100}%` };
}

function Chip({
  x,
  y,
  label,
  tone = "white",
  show,
}: {
  x: number;
  y: number;
  label: string;
  tone?: "white" | "dark";
  show: number;
}) {
  return (
    <div
      className="absolute -translate-x-1/2 -translate-y-1/2 whitespace-nowrap rounded-full px-3.5 py-1.5 text-[12px] font-medium"
      style={{
        ...at(x, y),
        opacity: show,
        transform: `translate(-50%, calc(-50% + ${(1 - show) * 8}px))`,
        background: tone === "white" ? "#fff" : "#0a0a0a",
        color: tone === "white" ? "#0a0a0a" : "#f4f4f5",
        boxShadow: "inset 0 0 0 1px rgba(0,0,0,0.12)",
      }}
    >
      {label}
    </div>
  );
}

function Caption({
  x,
  y,
  label,
  show,
  color = "#33bf00",
}: {
  x: number;
  y: number;
  label: string;
  show: number;
  color?: string;
}) {
  return (
    <div
      className="absolute -translate-x-1/2 whitespace-nowrap text-[11px] leading-none"
      style={{ ...at(x, y), opacity: show, color, transform: "translate(-50%, -100%)" }}
    >
      {label}
    </div>
  );
}

function Stamp({ x, y, label, show }: { x: number; y: number; label: string; show: number }) {
  return (
    <div
      className="absolute -translate-x-1/2 whitespace-nowrap text-[11px] text-black/40"
      style={{ ...at(x, y), opacity: show, transform: "translate(-50%, 0)" }}
    >
      {label}
    </div>
  );
}

export function TwinsSection() {
  const headRef = useRef<HTMLDivElement>(null);
  const ref = useRef<HTMLDivElement>(null);
  const { story: headStory, reduced } = useInViewPlay(headRef, 0.3);
  const { story } = useInViewPlay(ref, 0.28);
  const [progress, setProgress] = useState(reduced ? 7 : 0);

  useEffect(() => {
    if (reduced) {
      setProgress(7);
      return;
    }
    if (!story) {
      setProgress(0);
      return;
    }
    const t0 = performance.now();
    const dur = 5600;
    let raf = 0;
    const tick = (now: number) => {
      const u = Math.min(1, (now - t0) / dur);
      setProgress(u * 7);
      if (u < 1) raf = requestAnimationFrame(tick);
    };
    raf = requestAnimationFrame(tick);
    return () => cancelAnimationFrame(raf);
  }, [story, reduced]);

  const appear = (n: number, span = 0.7) => {
    const t = Math.min(1, Math.max(0, (progress - n) / span));
    return reduced ? 1 : t;
  };

  const prod = Math.min(1, progress / 1.1);
  const branch = appear(1.15, 0.85);
  const preview = appear(2.0);
  const open = appear(2.9);
  const merged = appear(3.7);
  const candidate = appear(4.5);
  const running = appear(5.3);
  const destroyed = appear(6.15, 0.8);
  const headX = 56 + prod * 1000;

  return (
    <section id="twins" className="bg-[#f7f7f5]">
      <div ref={headRef} className="px-8 pt-12 lg:px-16 lg:pl-[260px]">
        <div className="min-w-0 text-center">
          <ClusterIcon play={headStory || reduced} />
          <h2 className="mx-auto max-w-[900px] text-[36px] font-semibold leading-[1.2] tracking-[-0.03em] md:text-[44px]">
            <span className="text-black">Isolated twins. </span>
            <span className="text-black/45">
              A private, production-shaped environment for the change — then destroy it automatically.
            </span>
          </h2>
        </div>
      </div>

      <div ref={ref} className="relative mx-auto mt-12 max-w-[1100px] px-8 pb-20 lg:px-10">
        <div className="relative aspect-[1100/500] min-h-[320px] w-full overflow-visible">
          <svg viewBox="0 0 1100 500" className="absolute inset-0 h-full w-full overflow-visible" aria-hidden>
            <line
              x1="56"
              x2="1056"
              y1="370"
              y2="370"
              stroke="#33bf00"
              strokeWidth="3"
              strokeLinecap="round"
              pathLength={1}
              strokeDasharray={1}
              strokeDashoffset={1 - prod}
            />
            <path
              d="M220 370 L220 175 L980 175"
              fill="none"
              stroke="#c4c4c0"
              strokeWidth="2.5"
              strokeLinecap="round"
              strokeLinejoin="round"
              pathLength={1}
              strokeDasharray={1}
              strokeDashoffset={1 - branch}
            />
            <circle cx="220" cy="370" r="5" fill="#33bf00" opacity={preview} />
            <circle cx="400" cy="175" r="6" fill="#33bf00" opacity={open} />
            <circle cx="560" cy="175" r="6" fill="#33bf00" opacity={merged} />
            <circle cx="760" cy="175" r="6" fill="#33bf00" opacity={running} />
            <circle cx="930" cy="175" r="6" fill="#6b6b6b" opacity={destroyed} />
            <circle cx={headX} cy="370" r="4.5" fill="#33bf00" opacity={prod > 0.08 ? 1 : 0} />
            <circle cx={headX} cy="370" r="11" fill="#33bf00" opacity={prod > 0.08 ? 0.16 : 0} />
          </svg>

          <Chip x={118} y={315} label="production" tone="white" show={appear(0.35)} />
          <Chip x={290} y={240} label="isolated twin" tone="dark" show={preview} />
          <Chip x={660} y={240} label="baseline · candidate" tone="white" show={candidate} />

          <Caption x={400} y={128} label="PR open" show={open} />
          <Caption x={560} y={128} label="twin ready" show={merged} />
          <Caption x={760} y={128} label="report on PR" show={running} />
          <Caption x={930} y={128} label="twin destroyed" show={destroyed} color="#a1a1aa" />

          <div
            className="absolute flex h-7 w-7 -translate-x-1/2 -translate-y-1/2 items-center justify-center rounded-full bg-white text-[9px] font-medium text-black"
            style={{
              ...at(220, 370),
              opacity: candidate,
              boxShadow: "inset 0 0 0 2px #33bf00",
            }}
          >
            GH
          </div>

          <Stamp x={220} y={430} label="18:24:00" show={preview} />
          <Stamp x={560} y={430} label="19:08:12" show={merged} />
          <Stamp x={930} y={430} label="20:32:04" show={destroyed} />
        </div>
      </div>
    </section>
  );
}
