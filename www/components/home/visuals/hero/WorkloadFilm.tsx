"use client";

import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { Avatar, Bar, Hairline, Label, Meta, Pill, easeInOut, moveStyle, smooth } from "./linear";

const LOOP = 8;

const JOURNEYS = [
  { label: "observed", value: 0.4, display: "40%", avatars: null },
  { label: "deterministic", value: 0.4, display: "40%", avatars: null },
  {
    label: "exploratory",
    value: 0.2,
    display: "20%",
    avatars: [
      { initial: "I", bg: "#c17a5a" },
      { initial: "M", bg: "#6b8cae" },
      { initial: "S", bg: "#5a8f6e" },
    ],
  },
] as const;

const STEPS = ["checkout", "retry", "unknown sku"] as const;

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
                  {row.label === "exploratory" ? (
                    <Pill className="bg-[#4CB782]/12 text-[#2F7A56] ring-[#4CB782]/25">{row.label}</Pill>
                  ) : (
                    <span className="min-w-0 flex-1 truncate text-[11px] tracking-extra-tight text-[#1A1A1A]">
                      {row.label}
                    </span>
                  )}
                  {row.avatars ? (
                    <span className="ml-auto flex -space-x-1.5">
                      {row.avatars.map((a, ai) => {
                        const show = smooth(span(t, 1.4 + ai * 0.18, 1.95 + ai * 0.18));
                        return (
                          <span key={a.initial} style={moveStyle({ opacity: show, scale: 0.7 + show * 0.3 })}>
                            <Avatar initial={a.initial} bg={a.bg} />
                          </span>
                        );
                      })}
                    </span>
                  ) : null}
                  <Meta className="tabular-nums">{row.display}</Meta>
                </div>
                <Bar className="mt-1.5" value={fill * row.value} tone={row.label === "exploratory" ? "ok" : "neutral"} />
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
          <Label>exploratory</Label>
          <span className="flex -space-x-1.5">
            {JOURNEYS[2].avatars.map((a) => (
              <Avatar key={a.initial} initial={a.initial} bg={a.bg} />
            ))}
          </span>
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center overflow-hidden rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          {STEPS.map((step, i) => {
            const show = smooth(span(t, 4.35 + i * 0.28, 5.0 + i * 0.28));
            return (
              <div key={step}>
                {i > 0 ? <Hairline className="my-1.5" /> : null}
                <div className="flex items-center gap-2" style={moveStyle({ opacity: show, x: (1 - show) * 8 })}>
                  <Meta className="w-3 tabular-nums">{i + 1}</Meta>
                  <span className="min-w-0 truncate text-[11px] tracking-extra-tight text-[#1A1A1A]">{step}</span>
                </div>
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
