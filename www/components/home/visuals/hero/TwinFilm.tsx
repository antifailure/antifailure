"use client";

import { Caret } from "@/components/motion/Caret";
import { cn } from "@/lib/cn";
import {
  fmtHMS,
  span,
  typed,
  useHeroFilmClock,
  type FilmProps,
} from "./clock";

export type { FilmProps };

const LOOP = 8;
const HOST = "fix-billing-184.preview.internal";
const TTL0 = 761;

export function TwinFilm({ active }: FilmProps) {
  const { ref, t, playing } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered: false,
    stillT: 5.2,
    reducedT: 5.2,
  });

  const host = typed(HOST, t, 1.0, 22);
  const hostDone = host.length >= HOST.length;
  const inset = span(t, 0.35, 1.0);
  const ttlSec = t < 1.0 ? TTL0 : Math.max(0, TTL0 - (t - 1) * 4);

  return (
    <div ref={ref} className="absolute inset-0 flex flex-col gap-2 p-3 font-sans select-none" aria-hidden>
      <EnvWindow
        label="baseline"
        host="prod.internal"
        ttl={fmtHMS(TTL0)}
        dim
      />
      <EnvWindow
        label="candidate"
        host={host}
        ttl={fmtHMS(ttlSec)}
        live={hostDone}
        caret={playing && t >= 1.0 && !hostDone}
        inset={inset}
      />
    </div>
  );
}

function EnvWindow({
  label,
  host,
  ttl,
  dim,
  live,
  caret,
  inset = 0,
}: {
  label: string;
  host: string;
  ttl: string;
  dim?: boolean;
  live?: boolean;
  caret?: boolean;
  inset?: number;
}) {
  return (
    <div
      className={cn(
        "relative flex min-h-0 flex-1 flex-col justify-between overflow-hidden rounded-[2px] px-2.5 py-2",
        dim ? "bg-white/40 ring-1 ring-black/10" : "bg-white/70 ring-1 ring-black/10",
      )}
      style={
        dim
          ? { opacity: 0.55 }
          : { boxShadow: "inset 0 0 0 1px #33BF00", opacity: 0.7 + inset * 0.3 }
      }
    >
      <div className="flex items-center justify-between gap-2">
        <span className="font-sans text-[10px] tracking-extra-tight text-black/45">{label}</span>
        {live ? (
          <span className="font-sans text-[10px] tracking-extra-tight text-[#33BF00]">live</span>
        ) : (
          <span className="font-sans text-[10px] tracking-extra-tight text-black/35">idle</span>
        )}
      </div>
      <div className="min-w-0">
        <div className="break-all font-sans text-[11px] tracking-extra-tight text-[#285D49]">
          {host}
          {caret ? <Caret className="bg-black" /> : null}
        </div>
        <div className="mt-1 font-sans text-[10px] tabular-nums tracking-extra-tight text-black/45">
          ttl {ttl}
        </div>
      </div>
    </div>
  );
}
