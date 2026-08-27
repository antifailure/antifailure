"use client";

import { cn } from "@/lib/cn";
import { useHeroFilmClock, type FilmProps } from "./clock";

const LOOP = 8;

const LINES = [
  { at: 0.5, host: "stripe.charges", status: "SIMULATED", tone: "ok" as const },
  { at: 2.4, host: "sendgrid.send", status: "CAPTURED", tone: "ok" as const },
  { at: 4.3, host: "api.prod.internal", status: "BLOCKED", tone: "block" as const },
];

export function FirewallFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 6.2,
    reducedT: 6.2,
  });

  const visible = LINES.filter((line) => t >= line.at);
  const activeIdx = visible.length ? visible.length - 1 : -1;

  return (
    <div ref={ref} className="absolute inset-0 flex flex-col p-3 font-sans select-none" aria-hidden>
      <div className="mb-2 flex items-center gap-2">
        <span className="size-1.5 rounded-full bg-[#33BF00]" />
        <span className="font-sans text-[10px] tracking-extra-tight text-black/45">firewall</span>
      </div>
      <div className="flex min-h-0 flex-1 flex-col justify-center gap-1">
        {LINES.map((line, i) => {
          const on = t >= line.at;
          const lit = i === activeIdx;
          return (
            <div
              key={line.host}
              className={cn(
                "relative grid grid-cols-[1fr_auto] items-center gap-3 rounded-[2px] px-2 py-1.5",
                lit && on && "bg-black/[0.05]",
              )}
              style={{ opacity: on ? 1 : 0.28 }}
            >
              {lit && on ? (
                <span className="absolute inset-y-0 left-0 w-px bg-[#33BF00]" />
              ) : null}
              <span className="truncate font-sans text-[11px] tracking-extra-tight text-[#285D49]">
                {line.host}
              </span>
              <span
                className={cn(
                  "font-sans text-[10px] tracking-extra-tight uppercase",
                  !on && "text-black/25",
                  on && line.tone === "ok" && "text-[#285D49]",
                  on && line.tone === "block" && "text-red-700",
                )}
              >
                {line.status}
              </span>
            </div>
          );
        })}
      </div>
    </div>
  );
}
