"use client";

import { useRef, useState } from "react";
import { Caret } from "@/components/motion/Caret";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_QUART } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { FILM_EASE, Pill, StatusMoon } from "./hero/linear";
import {
  EvidenceRow,
  FindingHead,
  InnerHeader,
  InnerPills,
  InnerSplit,
  NestedPane,
  PrivatePill,
  RunToast,
  RunWindow,
} from "./StudioWindow";

const LOOP = 12;
const DESTROYED_STILL = 11.35;
const HOST = "fix-billing-184.preview.internal";
const TABS = ["Build", "Restore", "Contain", "Destroy"] as const;

const SLOTS = [
  { id: "vpc", label: "Network" },
  { id: "app", label: "App" },
  { id: "postgres", label: "Postgres" },
  { id: "workers", label: "Workers" },
] as const;

const PROD = [
  { id: "ingress", label: "ingress" },
  { id: "app", label: "app" },
  { id: "prod-db", label: "prod-db", cut: true },
  { id: "workers", label: "workers" },
] as const;

const CONTAINED = [
  { when: "Stripe", then: "Simulated" },
  { when: "Email", then: "Captured" },
  { when: "Webhooks", then: "Blocked" },
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

const COPY = {
  build: {
    title: "Isolated twin, production not in path",
    body: "Clone-local DNS, no default public egress, no route out of the network.",
  },
  restore: {
    title: "Isolated twin, production not in path",
    body: "Production secrets are replaced. The twin cannot reach live keys.",
  },
  contain: {
    title: "Isolated twin, production not in path",
    body: "Stripe, email, and webhooks stay inside the twin. Nothing leaks to production.",
  },
  destroy: {
    title: "Isolated twin, production not in path",
    body: "Every resource is journaled, destroyed, and counted. Nothing outlives the run.",
  },
} as const;

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

function slotMoon(fill: number, destroying: boolean): "todo" | "progress" | "ok" | "block" {
  if (destroying && fill < 0.08) return "ok";
  if (fill <= 0.02) return "todo";
  if (fill < 0.98) return destroying ? "block" : "progress";
  return "ok";
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
  const beatIndex = BEATS.findIndex((item) => item.id === beat);
  const host = typeChars(HOST, t, 0.28, 24);
  const hostDone = host.length >= HOST.length && t < 9.35;
  const hostShown = t < 9.35 ? host : "";
  const restoreOn = t >= 3.1 && t < 6.05;
  const checksOn = t >= 6.05 && t < 9.05;
  const reportOn = t >= 9.05;
  const destroyedHold = t >= 10.35;
  const built = t >= 0.4 && t < 9.4;
  const copy = COPY[beat];
  const alive = SLOTS.reduce((n, _slot, i) => n + (slotFill(t, i) > 0.08 ? 1 : 0), 0);
  const moon = destroyedHold ? "ok" : reportOn ? "block" : built ? "progress" : "todo";
  const action = reportOn ? "BLOCK" : built ? "Measuring" : "Idle";

  return (
    <div ref={ref} className="relative w-full select-none" aria-hidden>
      <div className="hidden max-xl:block">
        <div className="overflow-hidden rounded-[12px] border border-black/[0.08] bg-white">
          <div className="flex items-center justify-between gap-3 border-b border-black/[0.08] px-4 py-2.5">
            <span className="font-mono text-[11px] uppercase tracking-[0.12em] text-black/45">twin lifecycle</span>
            <span className="font-mono text-[12px] tracking-extra-tight text-[#C43D3D]">BLOCK</span>
          </div>
          <ol>
            {BEATS.map((item, i) => (
              <li
                key={item.id}
                className="flex items-start gap-3 border-b border-black/[0.06] px-4 py-3 last:border-0"
              >
                <span className="mt-0.5 size-2 shrink-0 rounded-full bg-black" />
                <div className="min-w-0">
                  <div className="text-[14px] tracking-extra-tight text-black">
                    {String(i + 1).padStart(2, "0")} · {item.label}
                  </div>
                  <p className="mt-1 text-[13px] leading-5 tracking-extra-tight text-[#6B6F76]">
                    {COPY[item.id].body}
                  </p>
                </div>
              </li>
            ))}
          </ol>
        </div>
      </div>
      <div className="max-xl:hidden">
      <RunWindow>
        <InnerHeader
          moon={moon}
          breadcrumb={
            <span className="flex min-w-0 items-center gap-1.5">
              <span className="text-[#6B6F76]">Twin</span>
              <span className="text-[#C0C3C8]">/</span>
              <span className="truncate">
                {hostShown || <span className="text-[#9B9EA5]">preview hostname</span>}
                {t >= 0.28 && t < 9.35 && !hostDone ? <Caret className="bg-[#1A1A1A]" /> : null}
              </span>
            </span>
          }
          metrics={[
            {
              value: destroyedHold ? "14/14" : `${alive}/4`,
              tone: destroyedHold ? "ok" : reportOn ? "block" : "muted",
            },
          ]}
        />
        <InnerPills
          items={TABS}
          active={Math.max(0, beatIndex)}
          action={reportOn ? <span className="text-[#C43D3D]">BLOCK</span> : action}
        />
        <InnerSplit
          left={
            <>
              <FindingHead
                title={copy.title}
                meta={
                  <>
                    {built ? <PrivatePill /> : <Pill>tearing down</Pill>}
                    <span className="text-[11px] tracking-extra-tight text-[#9B9EA5]">
                      production not in path
                    </span>
                  </>
                }
                step={`${String(beatIndex + 1).padStart(2, "0")} / 04`}
                body={copy.body}
              />
              <div className="mt-4 flex flex-col gap-1.5">
                {SLOTS.map((slot, i) => {
                  const fill = slotFill(t, i);
                  return (
                    <EvidenceRow
                      key={slot.id}
                      moon={slotMoon(fill, reportOn)}
                      label={slot.label}
                      value={fill < 0.08 ? (reportOn ? "gone" : "waiting") : `${Math.round(fill * 100)}%`}
                      tone={fill < 0.08 ? (reportOn ? "ok" : "muted") : "ok"}
                    />
                  );
                })}
                <div className="flex h-[120px] flex-col gap-1.5 overflow-hidden">
                  {restoreOn ? (
                    <EvidenceRow moon="ok" label="Credentials" value={piiAt(t)} tone="ok" />
                  ) : null}
                  {checksOn
                    ? CONTAINED.map((row, i) => {
                        const on = t >= 6.2 + i * 0.35;
                        return (
                          <EvidenceRow
                            key={row.when}
                            moon={on ? "ok" : "progress"}
                            label={`${row.when}`}
                            value={on ? row.then : "…"}
                            tone={on ? "ok" : "muted"}
                          />
                        );
                      })
                    : null}
                  {reportOn ? (
                    <EvidenceRow
                      moon="block"
                      label="Unsafe schema migration"
                      value="BLOCK"
                      tone="block"
                    />
                  ) : null}
                </div>
              </div>
            </>
          }
          right={
            <NestedPane title={destroyedHold ? "twin destroyed" : HOST} meta="run_08f2">
              <div className="grid min-h-0 flex-1 grid-cols-2 max-xl:grid-cols-1">
                <div className="border-black/[0.08] p-3 max-xl:border-b xl:border-r">
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <span className="text-[11px] tracking-extra-tight text-[#9B9EA5]">Production</span>
                    <span className="text-[11px] tracking-extra-tight text-[#C0C3C8]">Not in path</span>
                  </div>
                  <ul className="flex flex-col gap-1.5 opacity-55">
                    {PROD.map((row) => (
                      <li
                        key={row.id}
                        className="flex h-9 items-center justify-between rounded-[8px] border border-black/[0.06] bg-white px-2.5"
                      >
                        <span
                          className={cn(
                            "text-[12px] tracking-extra-tight",
                            "cut" in row && row.cut ? "text-[#C43D3D]" : "text-[#9B9EA5]",
                          )}
                        >
                          {row.label}
                        </span>
                        {"cut" in row && row.cut ? (
                          <span className="relative size-2.5 shrink-0" aria-hidden>
                            <span className="absolute inset-x-0 top-1/2 h-px -rotate-45 bg-[#EB5757]" />
                            <span className="absolute inset-x-0 top-1/2 h-px rotate-45 bg-[#EB5757]" />
                          </span>
                        ) : (
                          <StatusMoon tone="todo" />
                        )}
                      </li>
                    ))}
                  </ul>
                </div>
                <div className="p-3">
                  <div className="mb-2 flex items-center justify-between gap-2">
                    <span className="text-[11px] tracking-extra-tight text-[#1A1A1A]">Isolated twin</span>
                    <span className="text-[11px] tracking-extra-tight text-[#9B9EA5]">
                      {destroyedHold ? "empty" : "live"}
                    </span>
                  </div>
                  <div className="grid grid-cols-2 gap-1.5">
                    {SLOTS.map((slot, i) => {
                      const fill = slotFill(t, i);
                      return (
                        <div
                          key={slot.id}
                          className="relative h-[64px] overflow-hidden rounded-[8px] border border-black/[0.08] bg-white max-md:h-[56px]"
                        >
                          <div
                            className="absolute inset-x-0 bottom-0 bg-[#F4F4F6]"
                            style={{
                              height: `${fill * 100}%`,
                              transition: `height 400ms ${FILM_EASE}`,
                            }}
                          />
                          <span className="relative z-[1] flex h-full items-end px-2 pb-1.5 text-[12px] tracking-extra-tight text-[#1A1A1A]">
                            {slot.label}
                          </span>
                        </div>
                      );
                    })}
                  </div>
                </div>
              </div>
            </NestedPane>
          }
        />
        <RunToast
          visible={destroyedHold}
          tone="ok"
          title="14 of 14 destroyed"
          detail="Unsafe schema migration: BLOCK. Nothing remains, and the twin never sat in the production path."
        />
      </RunWindow>
      </div>
    </div>
  );
}
