"use client";

import { useRef, useState } from "react";
import { Caret } from "@/components/motion/Caret";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_QUART } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import {
  CheckRow,
  FILM_EASE,
  Hairline,
  MonoLabel,
  Node,
  Panel,
  StatusPill,
} from "./primitives";

const LOOP = 12;
const DESTROYED_STILL = 11.35;
const HOST = "fix-billing-184.preview.internal";

const SLOTS = [
  { id: "vpc", label: "vpc" },
  { id: "app", label: "app" },
  { id: "postgres", label: "postgres" },
  { id: "workers", label: "workers" },
] as const;

const PROD = ["ingress", "app", "prod-db", "workers"] as const;

const CHECKS = [
  "no prod write creds",
  "no prod db route",
  "no default internet",
  "clone DNS",
  "secrets namespace",
  "egress gateway",
] as const;

const BEATS = [
  { id: "build", label: "Build", at: 0, until: 3 },
  { id: "restore", label: "Restore", at: 3, until: 6 },
  { id: "contain", label: "Contain", at: 6, until: 9 },
  { id: "destroy", label: "Destroy", at: 9, until: 12 },
] as const;

const PII_FRAMES = [
  { at: 0, text: "ada@corp.io" },
  { at: 0.35, text: "a#a@c*rp.io" },
  { at: 0.55, text: "u**@****.***" },
  { at: 0.8, text: "user_00418@mask.local" },
] as const;

function u01(t: number, a: number, b: number) {
  if (b <= a) return t >= a ? 1 : 0;
  return clamp((t - a) / (b - a));
}

function eased(t: number, a: number, b: number) {
  return EASE_OUT_QUART(u01(t, a, b));
}

function typeChars(text: string, t: number, start: number, cps: number) {
  if (t < start) return "";
  return text.slice(0, Math.min(text.length, Math.floor((t - start) * cps)));
}

function slotFill(t: number, i: number) {
  const appear = 0.4 + i * 0.28;
  const built = eased(t, appear, appear + 0.5);
  const goneStart = 9.2 + i * 0.16;
  return built * (1 - eased(t, goneStart, goneStart + 0.4));
}

function piiAt(t: number) {
  const u = t - 3.15;
  let text: string = PII_FRAMES[0].text;
  for (const frame of PII_FRAMES) {
    if (u >= frame.at) text = frame.text;
  }
  return text;
}

function currentBeat(t: number) {
  let id: (typeof BEATS)[number]["id"] = "build";
  for (const beat of BEATS) {
    if (t >= beat.at) id = beat.id;
  }
  return id;
}

export function TwinLifecycleScene() {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(ref, 0.22);
  const [tRaw, setTRaw] = useState(0);

  usePausedRaf(idle, (_now, elapsed) => {
    const next = Math.round(((elapsed / 1000) % LOOP) * 30) / 30;
    setTRaw((prev) => (prev === next ? prev : next));
  });

  const t = reduced ? DESTROYED_STILL : tRaw;
  const beat = currentBeat(t);
  const host = typeChars(HOST, t, 0.28, 24);
  const hostDone = host.length >= HOST.length && t < 9.35;
  const hostShown = t < 9.35 ? host : "";
  const restoreOn = t >= 3.1 && t < 6.05;
  const checksOn = t >= 6.05 && t < 9.05;
  const reportOn = t >= 9.05;
  const destroyedHold = t >= 10.35;
  const postgresFill = slotFill(t, 2);
  const subset = restoreOn && postgresFill > 0.4;

  return (
    <div
      ref={ref}
      className="pointer-events-none relative w-full select-none"
      style={{ transitionTimingFunction: FILM_EASE }}
      aria-hidden
    >
      <Panel className="relative aspect-[1184/420] w-full overflow-hidden rounded-[12px] max-md:aspect-auto max-md:min-h-[480px]">
        <div className="sage-grid pointer-events-none absolute inset-0 opacity-40" />
        <div className="relative flex h-full flex-col overflow-hidden">
          <div className="grid min-h-0 flex-1 grid-cols-[168px_24px_minmax(0,1fr)] max-md:grid-cols-1">
            <ProductionCol />
            <CutCol />
            <TwinCol
              t={t}
              host={hostShown}
              hostDone={hostDone}
              restoreOn={restoreOn}
              checksOn={checksOn}
              reportOn={reportOn}
              destroyedHold={destroyedHold}
              subset={subset}
            />
          </div>

          <Hairline />
          <BeatRail t={t} current={beat} />
        </div>
      </Panel>
    </div>
  );
}

function ProductionCol() {
  return (
    <div className="relative flex h-full flex-col overflow-hidden px-4 pt-4 pb-3 max-md:border-b max-md:border-black/10 max-md:pb-3">
      <MonoLabel className="uppercase tracking-[0.14em] text-black/35">production</MonoLabel>
      <div className="mt-1 font-mono text-[10px] tracking-extra-tight text-red-700/80 uppercase">
        never in path
      </div>
      <div className="mt-3 flex flex-1 flex-col gap-1.5 max-md:flex-row">
        {PROD.map((name) => {
          const cut = name === "prod-db";
          return (
            <div
              key={name}
              className="flex h-[42px] flex-1 items-center justify-between border border-black/10 bg-white/40 px-2 max-md:h-9 max-md:px-1.5"
            >
              <span
                className={cn(
                  "font-mono text-[10px] tracking-extra-tight",
                  cut ? "text-red-700/80" : "text-black/45",
                )}
              >
                {name}
              </span>
              {cut ? (
                <span className="relative size-2.5 shrink-0">
                  <span className="absolute inset-x-0 top-1/2 h-px -rotate-45 bg-red-600" />
                  <span className="absolute inset-x-0 top-1/2 h-px rotate-45 bg-red-600" />
                </span>
              ) : (
                <span className="size-1 rounded-full bg-black/20" />
              )}
            </div>
          );
        })}
      </div>
    </div>
  );
}

function CutCol() {
  return (
    <div className="relative hidden overflow-hidden md:block">
      <span className="absolute top-4 bottom-4 left-1/2 w-px -translate-x-1/2 bg-black/12" aria-hidden />
      <div className="absolute top-[42%] left-1/2 flex size-5 -translate-x-1/2 -translate-y-1/2 items-center justify-center bg-[#f4f7f5]">
        <svg viewBox="0 0 12 12" className="size-3.5" fill="none" aria-hidden>
          <line x1="2" y1="2" x2="10" y2="10" stroke="#dc2626" strokeWidth="1.2" />
          <line x1="10" y1="2" x2="2" y2="10" stroke="#dc2626" strokeWidth="1.2" />
        </svg>
      </div>
    </div>
  );
}

function TwinCol({
  t,
  host,
  hostDone,
  restoreOn,
  checksOn,
  reportOn,
  destroyedHold,
  subset,
}: {
  t: number;
  host: string;
  hostDone: boolean;
  restoreOn: boolean;
  checksOn: boolean;
  reportOn: boolean;
  destroyedHold: boolean;
  subset: boolean;
}) {
  return (
    <div className="flex min-w-0 flex-col overflow-hidden px-4 pt-4 pb-3">
      <div className="flex items-center justify-between gap-3">
        <Node label="twin" lit={t >= 0.4 && t < 9.4} />
        <MonoLabel className="uppercase tracking-[0.12em] text-black/30">run_08f2</MonoLabel>
      </div>

      <div className="mt-3 flex h-8 items-center gap-1.5 overflow-hidden border border-black/10 bg-white/70 px-2">
        <LockGlyph className={cn("size-3 shrink-0", hostDone ? "text-[#285D49]" : "text-black/25")} />
        <span className="min-w-0 flex-1 truncate font-mono text-[10px] tracking-extra-tight text-black tabular-nums">
          {host || <span className="text-black/25">preview hostname</span>}
          {t >= 0.28 && t < 9.35 && !hostDone ? <Caret className="bg-black" /> : null}
        </span>
        {hostDone ? (
          <span className="shrink-0 font-mono text-[9px] tracking-extra-tight text-[#285D49] uppercase">
            private
          </span>
        ) : null}
      </div>

      <div className="mt-2 grid grid-cols-4 gap-1.5 max-md:grid-cols-2">
        {SLOTS.map((slot, i) => {
          const fill = slotFill(t, i);
          const live = fill > 0.08;
          return (
            <div
              key={slot.id}
              className="relative h-[88px] overflow-hidden border border-black/12 bg-white/40 max-md:h-[72px]"
            >
              <div
                className="absolute inset-x-0 bottom-0 bg-[#CAE6D9]"
                style={{
                  height: `${fill * 100}%`,
                  opacity: live ? 1 : 0,
                  transition: `height 0.4s ${FILM_EASE}, opacity 0.3s ${FILM_EASE}`,
                }}
              />
              <span
                className={cn(
                  "absolute inset-x-1 top-2 font-mono text-[10px] tracking-extra-tight",
                  live ? "text-black/70" : "text-black/30",
                )}
              >
                {slot.label}
              </span>
              {slot.id === "postgres" && subset ? (
                <span className="absolute inset-x-1 bottom-1.5 font-mono text-[9px] tracking-extra-tight text-[#285D49]">
                  subset 12%
                </span>
              ) : null}
            </div>
          );
        })}
      </div>

      <div className="relative mt-3 min-h-[92px] overflow-hidden max-md:min-h-[108px]">
        {restoreOn ? (
          <div className="border border-black/10 bg-white/70 px-2.5 py-2">
            <MonoLabel className="uppercase tracking-[0.12em]">safe state</MonoLabel>
            <div className="mt-1 font-mono text-[11px] tracking-extra-tight text-black/70 tabular-nums">
              {piiAt(t)}
            </div>
          </div>
        ) : null}

        {checksOn ? <ContainmentChecks t={t} /> : null}

        {reportOn ? (
          <div
            className="border border-black/10 bg-white px-2.5 py-2"
            style={{
              opacity: eased(t, 9.05, 9.35),
              transform: `translateY(${(1 - eased(t, 9.05, 9.4)) * 8}px)`,
            }}
          >
            <div className="flex items-center gap-2">
              <StatusPill tone="BLOCK" />
              <span className="font-mono text-[11px] tracking-extra-tight text-black">
                unsafe schema migration
              </span>
            </div>
            {destroyedHold ? (
              <div className="mt-1.5 font-mono text-[10px] tracking-extra-tight text-black/55 tabular-nums">
                14/14 destroyed · 0 orphans
              </div>
            ) : (
              <div className="mt-1.5 font-mono text-[10px] tracking-extra-tight text-black/40">
                tearing down
              </div>
            )}
          </div>
        ) : null}
      </div>
    </div>
  );
}

function ContainmentChecks({ t }: { t: number }) {
  return (
    <div className="overflow-hidden border border-black/10 bg-white/70 px-2.5 py-2">
      <MonoLabel className="uppercase tracking-[0.12em] text-black/35">containment</MonoLabel>
      <div className="mt-1.5 grid grid-cols-2 gap-x-4 gap-y-1 max-md:grid-cols-1">
        {CHECKS.map((line, i) => {
          const start = 6.2 + i * 0.22;
          const on = t >= start;
          return (
            <CheckRow key={line} ok={on ? true : undefined} className="text-[10px] text-black/65">
              {line}
            </CheckRow>
          );
        })}
      </div>
    </div>
  );
}

function BeatRail({ t, current }: { t: number; current: (typeof BEATS)[number]["id"] }) {
  const u = clamp(t / LOOP);
  return (
    <div className="shrink-0 overflow-hidden px-4 pt-3 pb-3">
      <div className="relative h-[3px] overflow-hidden bg-black/[0.08]">
        <div className="h-full bg-[#33bf00]" style={{ width: `${u * 100}%` }} />
      </div>
      <div className="mt-2 flex">
        {BEATS.map((beat) => {
          const active = current === beat.id;
          const done = t >= beat.until;
          return (
            <div key={beat.id} className="flex min-w-0 flex-1 flex-col items-center">
              <span
                className={cn(
                  "w-full truncate text-center font-mono text-[10px] tracking-extra-tight uppercase max-md:text-[9px]",
                  active && "text-black",
                  done && !active && "text-black/50",
                  !done && !active && "text-black/28",
                )}
              >
                {beat.label}
              </span>
              <span
                className="mt-1 size-1.5 rounded-full"
                style={{ background: active ? "#33bf00" : "transparent" }}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

function LockGlyph({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 12 12" className={className} fill="none" aria-hidden>
      <rect x="2.4" y="5.4" width="7.2" height="5.2" stroke="currentColor" strokeWidth="1" />
      <path d="M4.2 5.4V3.8a1.8 1.8 0 0 1 3.6 0v1.6" stroke="currentColor" strokeWidth="1" />
    </svg>
  );
}
