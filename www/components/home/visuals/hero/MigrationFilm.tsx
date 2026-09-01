"use client";

import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { Bar, Hairline, Label, Meta, Pill, StatusMoon, easeInOut, moveStyle, smooth, ticks } from "./linear";

const LOOP = 8;

const STEPS = ["LOCK", "QUEUE", "REWRITE", "PLAN"] as const;

export function MigrationFilm({ active }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    stillT: 0,
    reducedT: LOOP - 0.001,
  });

  const lock = smooth(span(t, 0.9, 3.05));
  const blocked = lock > 0.72;
  const page = easeInOut(span(t, 3.45, 4.6));
  const found = smooth(span(t, 4.85, 5.8));

  return (
    <div ref={ref} className="absolute inset-0 overflow-hidden font-sans select-none" aria-hidden>
      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: 1 - page, y: page * -8, scale: 1 + page * 0.12 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>migration</Label>
          <StatusMoon tone={blocked ? "block" : "progress"} />
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          <div className="flex items-center gap-2">
            <Meta className="w-8 shrink-0">When</Meta>
            <Pill tone="block">exclusive lock</Pill>
          </div>
          <Bar className="mt-2" value={lock} tone={blocked ? "block" : "neutral"} />
          <Meta className="mt-1 block tabular-nums">{ticks(0, 27.4, lock)}s</Meta>
          <Hairline className="my-2" />
          <div className="flex items-center gap-2">
            <Meta className="w-8 shrink-0">Then</Meta>
            <Pill tone={blocked ? "block" : "neutral"}>{blocked ? "blocked a session" : "watching"}</Pill>
          </div>
        </div>
      </div>

      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: page, y: (1 - page) * 14, scale: 0.96 + page * 0.04 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>plan</Label>
          <StatusMoon tone="block" />
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          <div className="flex items-center justify-between gap-1">
            {STEPS.map((step, i) => {
              const show = smooth(span(t, 4.45 + i * 0.18, 5.0 + i * 0.18));
              const hot = step === "PLAN";
              return (
                <span
                  key={step}
                  className={`text-[9px] tracking-extra-tight ${hot ? "text-[#C43D3D]" : "text-[#9B9EA5]"}`}
                  style={moveStyle({ opacity: 0.25 + show * 0.75, y: (1 - show) * 4 })}
                >
                  {step}
                </span>
              );
            })}
          </div>
          <Hairline className="my-2" />
          <div className="flex items-center gap-1.5" style={moveStyle({ opacity: found, y: (1 - found) * 5 })}>
            <Pill tone="block">FINDING</Pill>
            <Meta className="tabular-nums">27.4s · blocked a session</Meta>
          </div>
        </div>
      </div>
    </div>
  );
}
