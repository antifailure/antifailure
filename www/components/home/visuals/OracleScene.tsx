"use client";

import { useRef, useState } from "react";
import { clamp, EASE_OUT_QUART } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { cn } from "@/lib/cn";
import { CheckRow, MonoLabel, Node, Panel, StatusPill } from "./primitives";

export const ORACLE_LOOP_S = 12;
const HOLD_T = 10.8;

function ease(u: number) {
  return EASE_OUT_QUART(clamp(u));
}

function gate(t: number, a: number, dur = 0.32) {
  return ease((t - a) / dur);
}

export function OracleScene() {
  const rootRef = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(rootRef, 0.22);
  const [clock, setClock] = useState(0);

  usePausedRaf(idle, (_now, elapsed) => {
    const next = Math.round(((elapsed / 1000) % ORACLE_LOOP_S) * 30) / 30;
    setClock((prev) => (prev === next ? prev : next));
  });

  const t = reduced ? HOLD_T : clock;
  const prodOn = gate(t, 0.08, 0.28);
  const lanesOn = gate(t, 0.55, 0.4);
  const runningOn = gate(t, 1.7, 0.28);
  const submitOn = gate(t, 2.15, 0.28);
  const passedOn = gate(t, 3.5, 0.28);
  const divergeOn = gate(t, 4.35, 0.32);
  const declaredOn = gate(t, 5.9, 0.32);
  const goneOn = gate(t, 7.4, 0.36);

  return (
    <div ref={rootRef} className="w-full" aria-hidden>
      <Panel className="relative aspect-[1184/340] w-full overflow-hidden rounded-[12px] max-md:aspect-auto max-md:min-h-[360px]">
        <div className="sage-grid pointer-events-none absolute inset-0 opacity-40" />

        <div className="relative flex h-full flex-col px-5 py-4 max-md:px-4">
          <div className="flex items-center justify-between gap-3" style={{ opacity: prodOn }}>
            <Node label="production" lit={prodOn > 0.6} />
            <MonoLabel className="uppercase tracking-[0.12em]">same state · same behavior</MonoLabel>
          </div>

          <div
            className="mt-4 grid min-h-0 flex-1 grid-cols-2 gap-px overflow-hidden border border-black/10 bg-black/10 max-md:grid-cols-1"
            style={{ opacity: lanesOn }}
          >
            <Lane
              label="baseline"
              tone="ok"
              verdictOn={passedOn}
              events={[
                { label: "scenario running", on: runningOn, check: false },
                { label: "checks passed", on: passedOn, tone: "ok" },
                { label: "twin deleted", on: goneOn, check: false, mute: true },
              ]}
            />
            <Lane
              label="candidate"
              tone="bad"
              verdictOn={divergeOn}
              events={[
                { label: "submit", on: submitOn, check: false },
                { label: "≠ unexpected", on: divergeOn, tone: "bad" },
                { label: "access_tier", on: declaredOn, tone: "declared" },
                { label: "twin deleted", on: goneOn, check: false, mute: true },
              ]}
            />
          </div>
        </div>
      </Panel>
    </div>
  );
}

type EventTone = "ok" | "bad" | "declared";

function Lane({
  label,
  tone,
  verdictOn,
  events,
}: {
  label: string;
  tone: "ok" | "bad";
  verdictOn: number;
  events: { label: string; on: number; tone?: EventTone; check?: boolean; mute?: boolean }[];
}) {
  const blocked = tone === "bad";
  return (
    <div className="flex flex-col bg-[#f4f7f5] px-5 py-4 max-md:px-4">
      <div className="flex items-center justify-between gap-3">
        <MonoLabel className="uppercase tracking-[0.14em] text-black">{label}</MonoLabel>
        <span style={{ opacity: verdictOn }}>
          {blocked ? <StatusPill tone="BLOCK" /> : <StatusPill tone="PASS" />}
        </span>
      </div>

      <ul className="mt-4 flex flex-1 flex-col gap-2.5">
        {events.map((event) => (
          <li key={event.label} style={{ opacity: event.on }}>
            {event.mute ? (
              <span className="font-mono text-[12px] tracking-extra-tight text-black/35">{event.label}</span>
            ) : event.check === false && !event.tone ? (
              <span className="font-mono text-[12px] tracking-extra-tight text-black/50">{event.label}</span>
            ) : event.tone === "declared" ? (
              <div className="flex items-center justify-between gap-2">
                <CheckRow ok className="text-[12px] text-[#285D49]">
                  {event.label}
                </CheckRow>
                <span className="font-mono text-[10px] tracking-extra-tight text-[#285D49] uppercase">
                  declared
                </span>
              </div>
            ) : (
              <CheckRow
                ok={event.tone === "bad" ? false : event.tone === "ok"}
                className={cn("text-[12px]", event.tone === "bad" ? "text-red-700" : "text-black/70")}
              >
                {event.label}
              </CheckRow>
            )}
          </li>
        ))}
      </ul>
    </div>
  );
}
