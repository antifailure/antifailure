"use client";

import type { CSSProperties, ReactNode } from "react";
import { cn } from "@/lib/cn";
import { span, useHeroFilmClock, type FilmProps } from "./clock";
import { easeInOut, Label, Meta, Pill, moveStyle, smooth } from "./linear";

const LOOP = 8;

const STACK = ["api", "worker", "postgres"] as const;

export function TwinFilm({ active }: FilmProps) {
  const { ref, t } = useHeroFilmClock({
    loop: LOOP,
    active,
    stillT: 0,
    reducedT: LOOP - 0.001,
  });

  const page = easeInOut(span(t, 3.1, 4.25));
  const isolated = smooth(span(t, 5.2, 6.0));

  return (
    <div ref={ref} className="absolute inset-0 overflow-hidden font-sans select-none" aria-hidden>
      <div
        className="absolute inset-3.5"
        style={moveStyle({ opacity: 1 - page, scale: 1 + page * 0.22, y: page * 8 })}
      >
        <EnvCard
          className="absolute inset-x-1 top-0 h-[58%]"
          label="baseline"
          host="prod.internal"
          ttl="12:41"
          dim
        />
        <div className="absolute inset-x-0 bottom-0 top-7">
          <EnvCard label="candidate" host="fix-billing-184" ttl="12:38" live />
        </div>
      </div>

      <div
        className="absolute inset-3.5"
        style={moveStyle({ opacity: page, y: (1 - page) * 14, scale: 0.94 + page * 0.06 })}
      >
        <EnvCard className="h-full" label="candidate" host="fix-billing-184" ttl="12:38" live>
          <div className="mt-1.5 flex flex-wrap gap-1">
            {STACK.map((name, i) => {
              const show = smooth(span(t, 4.15 + i * 0.22, 4.85 + i * 0.22));
              return (
                <span key={name} style={moveStyle({ opacity: show, y: (1 - show) * 5 })}>
                  <Pill>{name}</Pill>
                </span>
              );
            })}
            <span style={moveStyle({ opacity: isolated, y: (1 - isolated) * 5 })}>
              <Pill tone="ok">isolated</Pill>
            </span>
          </div>
        </EnvCard>
      </div>
    </div>
  );
}

function EnvCard({
  label,
  host,
  ttl,
  dim,
  live,
  className,
  style,
  children,
}: {
  label: string;
  host: string;
  ttl: string;
  dim?: boolean;
  live?: boolean;
  className?: string;
  style?: CSSProperties;
  children?: ReactNode;
}) {
  return (
    <div
      className={cn(
        "flex h-full flex-col justify-between rounded-[10px] border border-black/[0.08] px-2.5 py-2",
        // Chosen rather than layered: bg-white is emitted after this, so as
        // an additive class the dim card rendered the same white as a lit
        // one and the state the prop names never reached the screen.
        dim ? "bg-[#F7F7F8]" : "bg-white",
        className,
      )}
      style={style}
    >
      <div className="flex items-center justify-between gap-2">
        <Label>{label}</Label>
        <Meta strong={live}>{live ? "live" : "idle"}</Meta>
      </div>
      <div className="min-w-0">
        <div className="truncate text-[12px] tracking-extra-tight text-[#1A1A1A]">{host}</div>
        <Meta className="mt-0.5 block tabular-nums">ttl {ttl}</Meta>
        {children}
      </div>
    </div>
  );
}
