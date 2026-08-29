"use client";

import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { Label, Meta, Pill, StatusMoon, easeInOut, moveStyle, smooth } from "./linear";

const LOOP = 8;

const CARDS = [
  { id: "CHG-184", title: "stripe.charges", tone: "ok" as const },
  { id: "MAIL-91", title: "sendgrid.send", tone: "progress" as const },
  { id: "DNS-018", title: "api.prod.internal", tone: "block" as const },
];

const PATHS = [
  "M36 28 C78 28, 96 22, 128 22",
  "M36 52 C74 52, 96 48, 128 52",
  "M36 52 C70 52, 92 78, 128 82",
];

export function FirewallFilm({ active, hovered }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    hovered,
    stillT: 0,
    reducedT: 0,
  });

  const draw = smooth(span(t, 0.35, 1.55));
  const page = easeInOut(span(t, 3.35, 4.5));
  const denied = smooth(span(t, 4.55, 5.35));

  return (
    <div ref={ref} className="absolute inset-0 overflow-hidden font-sans select-none" aria-hidden>
      <div className="absolute inset-0" style={moveStyle({ opacity: 1 - page * 0.92, scale: 1 + page * 0.18, x: -page * 8 })}>
        <svg viewBox="0 0 240 120" className="absolute inset-0 h-full w-full" aria-hidden>
          {PATHS.map((d, i) => (
            <path
              key={d}
              d={d}
              fill="none"
              stroke="rgba(0,0,0,0.22)"
              strokeWidth="1"
              pathLength={1}
              strokeDasharray="1"
              strokeDashoffset={1 - draw}
              markerEnd={i === 2 ? "url(#fw-arrow)" : undefined}
            />
          ))}
          {PATHS.map((d, i) => {
            const p = smooth(span(t, 0.7 + i * 0.22, 1.95 + i * 0.22));
            return (
              <circle
                key={`dot-${d}`}
                r="2.1"
                fill={i === 2 ? "#EB5757" : "rgba(0,0,0,0.35)"}
                style={{
                  offsetPath: `path("${d}")`,
                  offsetDistance: `${p * 100}%`,
                  opacity: draw > 0.18 && p > 0.02 && p < 0.97 ? 0.9 : 0,
                }}
              />
            );
          })}
          <defs>
            <marker id="fw-arrow" markerWidth="6" markerHeight="6" refX="5" refY="3" orient="auto">
              <path d="M0 0.6 L5 3 L0 5.4" fill="none" stroke="rgba(0,0,0,0.28)" strokeWidth="1" />
            </marker>
          </defs>
        </svg>

        <div className="absolute top-[18%] left-3 flex w-[76px] flex-col gap-2">
          {["charge", "email"].map((src) => (
            <div
              key={src}
              className="rounded-[8px] border border-black/[0.08] bg-white px-2 py-1.5 text-[10px] tracking-extra-tight text-[#1A1A1A]"
            >
              {src}
            </div>
          ))}
        </div>

        <div className="absolute top-[8%] right-2 flex w-[118px] flex-col gap-1.5">
          {CARDS.map((card, i) => (
            <div
              key={card.id}
              className="rounded-[10px] border border-black/[0.08] bg-white px-2 py-1.5"
            >
              <div className="flex items-center gap-1.5">
                <StatusMoon tone={card.tone} />
                <Meta className="tabular-nums">{card.id}</Meta>
              </div>
              <div className="mt-0.5 truncate text-[11px] tracking-extra-tight text-[#1A1A1A]">{card.title}</div>
            </div>
          ))}
        </div>
      </div>

      <div
        className="absolute inset-3.5 flex flex-col"
        style={moveStyle({ opacity: page, y: (1 - page) * 16, scale: 0.96 + page * 0.04 })}
      >
        <div className="mb-2 flex items-center justify-between">
          <Label>egress</Label>
          <StatusMoon tone="block" />
        </div>
        <div className="flex min-h-0 flex-1 flex-col justify-center rounded-[10px] border border-black/[0.08] bg-white px-2.5 py-2">
          <div className="flex items-center gap-1.5">
            <StatusMoon tone="block" />
            <Meta className="tabular-nums">DNS-018</Meta>
          </div>
          <div className="mt-1 truncate text-[12px] tracking-extra-tight text-[#1A1A1A]">api.prod.internal</div>
          <div className="mt-2 flex items-center gap-1.5" style={moveStyle({ opacity: denied, y: (1 - denied) * 5 })}>
            <Pill tone="block">denied</Pill>
            <Meta>unknown dest</Meta>
          </div>
        </div>
      </div>
    </div>
  );
}
