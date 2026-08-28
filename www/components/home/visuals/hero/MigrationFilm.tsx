"use client";

import { Caret } from "@/components/motion/Caret";
import { cn } from "@/lib/cn";
import { span, typed, useHeroFilmClock, type FilmProps } from "./clock";

const LOOP = 8;
const SQL = "ALTER TABLE subscriptions ADD COLUMN access_tier text";

function p99of(t: number) {
  if (t < 2.4) return 820;
  if (t < 3.2) return 1240;
  if (t < 4.2) return 3100;
  return 6900;
}

function fmtMs(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${ms}ms`;
}

export function MigrationFilm({ active, hovered }: FilmProps) {
  const { ref, t, playing } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 6.6,
    reducedT: 6.6,
  });

  const sql = typed(SQL, t, 0.15, 18);
  const sqlDone = sql.length >= SQL.length;
  const wait = Math.pow(span(t, 1.1, 5.4), 1.8);
  const blocked = t >= 5.4;
  const p99 = p99of(t);

  return (
    <div ref={ref} className="absolute inset-0 flex flex-col p-3 font-sans select-none" aria-hidden>
      <div className="mb-2 flex items-center justify-between gap-2">
        <span className="font-sans text-[10px] tracking-extra-tight text-black/45">migration</span>
        <span
          className={cn(
            "inline-flex items-center rounded-[2px] px-1.5 py-0.5 font-sans text-[10px] tracking-extra-tight uppercase ring-1",
            blocked ? "text-red-700 ring-red-600/50" : "text-black/35 ring-black/15",
          )}
        >
          {blocked ? "BLOCK" : "watch"}
        </span>
      </div>
      <div className="min-w-0 font-sans text-[11px] leading-4 tracking-extra-tight text-[#285D49]">
        {sql}
        {playing && !sqlDone ? <Caret className="bg-black" /> : null}
      </div>
      <div className="mt-3">
        <div className="mb-1 flex items-center justify-between">
          <span className="font-sans text-[10px] tracking-extra-tight text-black/45">lock wait</span>
          <span className="font-sans text-[10px] tabular-nums tracking-extra-tight text-[#285D49]">
            {(wait * 27.4).toFixed(1)}s
          </span>
        </div>
        <div className="h-1 overflow-hidden rounded-[2px] bg-black/10">
          <div
            className={cn("h-full", blocked ? "bg-red-600" : "bg-[#285D49]")}
            style={{ width: `${Math.max(4, wait * 100)}%` }}
          />
        </div>
      </div>
      <div className="mt-auto pt-3 font-sans text-[10px] tabular-nums tracking-extra-tight text-[#285D49]">
        p99 820ms → {fmtMs(p99)}
      </div>
    </div>
  );
}
