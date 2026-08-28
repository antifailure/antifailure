"use client";

import { sparkPath, span, useHeroFilmClock, type FilmProps } from "./clock";

const LOOP = 8;
const MIX = [
  { label: "observed", value: 40 },
  { label: "recorded", value: 40 },
  { label: "crowdi", value: 20 },
] as const;

export function WorkloadFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 5.5,
    reducedT: 5.5,
  });

  const draw = span(t, 0.2, 1.6);
  const mixP = span(t, 0.15, 1.4);
  const obs = sparkPath(11, 20, 240, 72, 0.42);
  const cand = sparkPath(19, 20, 240, 72, 0.58);
  const dash = 420;
  const shown = dash * draw;

  return (
    <div ref={ref} className="absolute inset-0 flex flex-col p-3 font-sans select-none" aria-hidden>
      <div className="mb-1.5 flex items-baseline justify-between gap-2">
        <span className="font-sans text-[10px] tracking-extra-tight text-black/45">mix</span>
        <span className="font-sans text-[10px] tracking-extra-tight text-black/35">not live diversion</span>
      </div>
      <div className="mb-2 grid grid-cols-3 gap-2">
        {MIX.map((m) => (
          <div key={m.label} className="min-w-0">
            <div className="font-sans text-[11px] tabular-nums tracking-extra-tight text-[#285D49]">
              {Math.round(m.value * mixP)}
            </div>
            <div className="font-sans text-[10px] tracking-extra-tight text-black/40">{m.label}</div>
          </div>
        ))}
      </div>
      <svg viewBox="0 0 240 72" className="min-h-0 w-full flex-1" aria-hidden>
        <path
          d={obs}
          fill="none"
          stroke="rgba(40,93,73,0.35)"
          strokeWidth="1"
          strokeDasharray={dash}
          strokeDashoffset={dash - shown}
        />
        <path
          d={cand}
          fill="none"
          stroke="#33BF00"
          strokeWidth="1"
          strokeDasharray={dash}
          strokeDashoffset={dash - shown}
        />
      </svg>
    </div>
  );
}
