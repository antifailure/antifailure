"use client";

import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { Hairline, Label, Meta, Pill, StatusMoon, easeInOut, moveStyle, smooth } from "./linear";

const LOOP = 8;

const ROWS = [
  { id: "u_8f2a", email: "ajay@acme.com", masked: "m***@example.net" },
  { id: "u_91c0", email: "s***@example.net" },
  { id: "u_bb12", email: "l***@example.net" },
] as const;

const REFS = [
  { from: "orders.user_id", ok: true },
  { from: "sessions.uid", ok: true },
] as const;

export function StateFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 0,
    reducedT: 0,
  });

  const mask = smooth(span(t, 1.05, 1.95));
  const page = easeInOut(span(t, 4.0, 5.1));

  return (
    <div ref={ref} className="absolute inset-0 overflow-hidden font-sans select-none" aria-hidden>
      <div
        className="absolute inset-3.5"
        style={moveStyle({ opacity: 1 - page, x: page * -16 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>public.users</Label>
          <Meta>unique</Meta>
        </div>
        <div className="overflow-hidden rounded-[10px] border border-black/[0.08] bg-white">
          {ROWS.map((row, i) => {
            const first = i === 0;
            const masked = first && mask > 0.45;
            return (
              <div key={row.id}>
                {i > 0 ? <Hairline /> : null}
                <div className="flex items-center gap-2 px-2.5 py-1.5">
                  <StatusMoon tone={first ? (mask > 0.7 ? "ok" : "progress") : "ok"} />
                  <Meta className="w-12 shrink-0 tabular-nums">{row.id}</Meta>
                  {masked && "masked" in row ? (
                    <Pill>{row.masked}</Pill>
                  ) : (
                    <span className="min-w-0 truncate text-[11px] tracking-extra-tight text-[#1A1A1A]">
                      {row.email}
                    </span>
                  )}
                </div>
              </div>
            );
          })}
        </div>
      </div>

      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: page, x: (1 - page) * 18 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>refs</Label>
          <StatusMoon tone="ok" />
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center overflow-hidden rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          <div className="flex items-center gap-1.5">
            <Meta className="tabular-nums">u_8f2a</Meta>
            <Pill>m***@example.net</Pill>
          </div>
          <Hairline className="my-2" />
          {REFS.map((refRow, i) => {
            const show = smooth(span(t, 4.75 + i * 0.28, 5.35 + i * 0.28));
            return (
              <div
                key={refRow.from}
                className="flex items-center gap-2 py-0.5"
                style={moveStyle({ opacity: show, x: (1 - show) * 8 })}
              >
                <span className="min-w-0 truncate text-[11px] tracking-extra-tight text-[#1A1A1A]">
                  {refRow.from}
                </span>
                <StatusMoon tone="ok" />
              </div>
            );
          })}
        </div>
      </div>
    </div>
  );
}
