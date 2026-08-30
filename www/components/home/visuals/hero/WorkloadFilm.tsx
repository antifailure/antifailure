"use client";

import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { Bar, Hairline, Label, Meta, Pill, easeInOut, moveStyle, smooth } from "./linear";

const LOOP = 8;

/** The route mix, weighted the way the access log served it. */
const JOURNEYS = [
  { label: "GET /settings/billing", value: 0.34, display: "34%", slow: false },
  { label: "GET /", value: 0.27, display: "27%", slow: false },
  { label: "GET /api/subscriptions", value: 0.18, display: "18%", slow: true },
] as const;

/** Nothing named these safe, so nothing sent them. */
const STEPS = ["POST /billing/upgrade", "POST /api/payments", "DELETE /api/seats"] as const;

export function WorkloadFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 0,
    reducedT: 0,
  });

  const fill = smooth(span(t, 0.45, 1.85));
  const page = easeInOut(span(t, 3.3, 4.45));

  return (
    <div ref={ref} className="absolute inset-0 overflow-hidden font-sans select-none" aria-hidden>
      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: 1 - page, y: page * -10, scale: 1 + page * 0.16 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>mix</Label>
          <Meta>not live diversion</Meta>
        </div>
        <div className="min-h-0 flex-1 overflow-hidden rounded-[10px] border border-black/[0.08] bg-white">
          {JOURNEYS.map((row, i) => (
            <div key={row.label}>
              {i > 0 ? <Hairline /> : null}
              <div className="px-2.5 py-2">
                <div className="flex items-center gap-2">
                  <span className="min-w-0 flex-1 truncate font-mono text-[10px] tracking-extra-tight text-[#1A1A1A]">
                    {row.label}
                  </span>
                  {row.slow ? (
                    <span style={moveStyle({ opacity: smooth(span(t, 1.4, 1.95)) })}>
                      <Pill className="bg-[#D94841]/12 text-[#A8332C] ring-[#D94841]/25">129% slower</Pill>
                    </span>
                  ) : null}
                  <Meta className="tabular-nums">{row.display}</Meta>
                </div>
                <Bar className="mt-1.5" value={fill * row.value} tone={row.slow ? "block" : "neutral"} />
              </div>
            </div>
          ))}
        </div>
      </div>

      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: page, y: (1 - page) * 14, scale: 0.96 + page * 0.04 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>not sent</Label>
          <Meta>no rule named them safe</Meta>
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center overflow-hidden rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          {STEPS.map((step, i) => {
            const show = smooth(span(t, 4.35 + i * 0.28, 5.0 + i * 0.28));
            return (
              <div key={step}>
                {i > 0 ? <Hairline className="my-1.5" /> : null}
                <div className="flex items-center gap-2" style={moveStyle({ opacity: show, x: (1 - show) * 8 })}>
                  <Meta className="w-3 tabular-nums">{i + 1}</Meta>
                  <span className="min-w-0 truncate font-mono text-[10px] tracking-extra-tight text-[#1A1A1A]">
                    {step}
                  </span>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
