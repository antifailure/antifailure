"use client";

import { cn } from "@/lib/cn";
import { span, useHeroFilmClock, type FilmProps } from "./clock";

const LOOP = 8;
const EMAIL_FRAMES = [
  "ajay@acme.com",
  "a7k2@x9m1.net",
  "m3q9@b4t2.org",
  "n8w1@c7h0.io",
  "p2f6@d1v5.co",
  "q9l3@e8s2.net",
  "r4h0@f6n9.org",
  "s1c8@g2m4.io",
  "m**4@ex***.net",
  "m***@example.net",
];

const ROWS = [
  { id: "u_8f2a", email: "ajay@acme.com", session: "tok_live_8f2" },
  { id: "u_91c0", email: "s***@example.net", session: "deleted" },
  { id: "u_bb12", email: "l***@example.net", session: "deleted" },
] as const;

function scrambleEmail(t: number) {
  const p = span(t, 1.0, 4.2);
  const i = Math.min(EMAIL_FRAMES.length - 1, Math.floor(p * EMAIL_FRAMES.length));
  return EMAIL_FRAMES[i];
}

export function StateFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 6.4,
    reducedT: 6.4,
  });

  const email0 = scrambleEmail(t);
  const session0 = t >= 4.4 ? "deleted" : ROWS[0].session;
  const sessionLive = t >= 4.4;

  return (
    <div ref={ref} className="absolute inset-0 flex flex-col p-3 font-sans select-none" aria-hidden>
      <div className="mb-2 flex items-baseline justify-between gap-2">
        <span className="font-sans text-[10px] tracking-extra-tight text-black/45">public.users</span>
        <span className="font-sans text-[10px] tracking-extra-tight text-[#285D49]">unique</span>
      </div>
      <div className="min-h-0 flex-1 overflow-hidden rounded-[2px] bg-white/50 ring-1 ring-black/10">
        <div className="grid grid-cols-[auto_1fr_auto] gap-x-2 border-b border-black/10 px-2 py-1.5">
          {["id", "email", "session"].map((col) => (
            <span key={col} className="font-sans text-[10px] tracking-extra-tight text-black/35">
              {col}
            </span>
          ))}
        </div>
        {ROWS.map((row, i) => {
          const email = i === 0 ? email0 : row.email;
          const session = i === 0 ? session0 : row.session;
          const gone = session === "deleted";
          return (
            <div
              key={row.id}
              className="grid grid-cols-[auto_1fr_auto] gap-x-2 border-b border-black/8 px-2 py-1.5 last:border-b-0"
            >
              <span className="font-sans text-[10px] tracking-extra-tight text-black/45">{row.id}</span>
              <span className="truncate font-sans text-[10px] tracking-extra-tight text-[#285D49]">{email}</span>
              <span
                className={cn(
                  "font-sans text-[10px] tracking-extra-tight",
                  gone || (i === 0 && sessionLive) ? "text-black/35" : "text-[#285D49]",
                )}
              >
                {session}
              </span>
            </div>
          );
        })}
      </div>
      <div className="mt-2 font-sans text-[10px] tracking-extra-tight text-black/45">
        12% subset · unique
      </div>
    </div>
  );
}
