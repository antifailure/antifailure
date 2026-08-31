"use client";

import { cn } from "@/lib/cn";

const LOCK = "M40 320 C180 300, 260 80, 420 70 C560 62, 620 310, 760 300 C900 288, 980 90, 1120 70 C1240 56, 1300 240, 1340 250";
const SAFE = "M40 300 C200 292, 340 280, 500 275 C700 268, 900 262, 1100 255 C1220 250, 1300 248, 1340 246";

export function LockChart({ state }: { state: 0 | 1 }) {
  const d = state === 0 ? LOCK : SAFE;
  const fill = state === 0 ? "rgba(220,38,38,0.22)" : "rgba(51,191,0,0.18)";
  const stroke = state === 0 ? "#dc2626" : "#33bf00";

  return (
    <div className="relative aspect-[1378/450] w-[1378px] max-w-full overflow-hidden bg-[#d7ebe3] max-md:hidden">
      <svg viewBox="0 0 1378 450" className="absolute inset-0 h-full w-full" aria-hidden>
        {Array.from({ length: 8 }).map((_, i) => (
          <line key={i} x1="40" x2="1340" y1={50 + i * 45} y2={50 + i * 45} stroke="rgba(0,0,0,0.08)" />
        ))}
        {Array.from({ length: 12 }).map((_, i) => (
          <line key={`v${i}`} y1="40" y2="410" x1={40 + i * 110} x2={40 + i * 110} stroke="rgba(0,0,0,0.05)" />
        ))}
        <path key={state} d={`${d} L1340 410 L40 410 Z`} fill={fill} className="film-area" />
        <path key={`l${state}`} d={d} fill="none" stroke={stroke} strokeWidth="3" className="film-stroke" />
        <circle cx={state === 0 ? 420 : 1100} cy={state === 0 ? 70 : 255} r="6" fill={stroke} className="film-node" />
      </svg>
      <div className="absolute top-6 left-8 font-mono text-[12px] tracking-extra-tight text-[#285D49]">
        {state === 0 ? "ALTER TABLE subscriptions … ACCESS EXCLUSIVE" : "expand → backfill → contract"}
      </div>
      <div className="absolute right-8 bottom-8 grid grid-cols-3 gap-8 font-mono text-[12px] text-[#285D49]">
        <div>Lock hold {state === 0 ? "27.4s" : "0.4s"}</div>
        <div>Blocked stmts {state === 0 ? "84" : "0"}</div>
        <div>Rewrite {state === 0 ? "yes" : "no"}</div>
      </div>
    </div>
  );
}

export function LockChartMobile({ state, always }: { state: 0 | 1; always?: boolean }) {
  return (
    <div className={cn("relative w-full", !always && "hidden max-md:block", state === 0 ? "hatch-red" : "hatch-green")}>
      <div className="border-t border-gray-new-10 bg-[#CAE6D9]/90 p-5 font-mono text-[13px] text-[#285D49]">
        {state === 0
          ? "ACCESS EXCLUSIVE on subscriptions, 27.4s, 84 queued"
          : "Expand-and-contract, 0.4s, nothing queued"}
      </div>
    </div>
  );
}
