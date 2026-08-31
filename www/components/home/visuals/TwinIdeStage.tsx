"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { clamp, EASE_OUT_QUART } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";

const STILL = 11.2;
const SPEED = 0.5;
const FILM = "cubic-bezier(0.16, 1, 0.3, 1)";

const BEATS = [
  { id: "build", label: "Build", at: 0 },
  { id: "restore", label: "Restore", at: 3 },
  { id: "contain", label: "Contain", at: 6 },
  { id: "destroy", label: "Destroy", at: 9 },
] as const;

const SLOTS = [
  { id: "net", label: "Network" },
  { id: "app", label: "App" },
  { id: "pg", label: "Postgres" },
  { id: "wrk", label: "Workers" },
] as const;

const PROD = [
  { id: "ingress", label: "ingress" },
  { id: "app", label: "app" },
  { id: "db", label: "prod-db", cut: true },
  { id: "workers", label: "workers" },
] as const;

const SEALS = [
  { id: "dns", label: "Clone-local DNS", detail: "No public resolver", from: 0.6 },
  { id: "keys", label: "Secrets replaced", detail: "Live keys unreachable", from: 3.2 },
  { id: "egress", label: "No default egress", detail: "The network has no route out", from: 6.2 },
] as const;

function u01(t: number, a: number, b: number) {
  if (b <= a) return t >= a ? 1 : 0;
  return clamp((t - a) / (b - a));
}

function eased(t: number, a: number, b: number) {
  return EASE_OUT_QUART(u01(t, a, b));
}

function slotFill(t: number, i: number) {
  const appear = 0.35 + i * 0.28;
  const built = eased(t, appear, appear + 0.55);
  const gone = 9.15 + i * 0.18;
  return built * (1 - eased(t, gone, gone + 0.42));
}

function beatIndex(t: number) {
  let i = 0;
  for (let n = 0; n < BEATS.length; n += 1) {
    if (t >= BEATS[n].at) i = n;
  }
  return i;
}

function TwinIsolationMap() {
  const ref = useRef<HTMLDivElement>(null);
  const wasInView = useRef(false);
  const { idle, reduced, inView } = useInViewPlay(ref, 0.22);
  const [tRaw, setTRaw] = useState(0);

  useEffect(() => {
    if (inView && !wasInView.current) setTRaw(0);
    wasInView.current = inView;
  }, [inView]);

  const playing = idle && tRaw < STILL;

  usePausedRaf(playing, (_now, elapsed) => {
    const next = Math.min(STILL, Math.round((elapsed / 1000) * SPEED * 60) / 60);
    setTRaw((prev) => (prev === next ? prev : next));
  });

  const t = reduced ? STILL : tRaw;
  const beat = beatIndex(t);
  const destroying = t >= 9.05;
  const gone = t >= 10.4;

  return (
    <div ref={ref} className="relative select-none" aria-hidden>
      <div>
        <p className="text-[11px] font-medium uppercase tracking-[0.16em] text-black/40">Isolated twin</p>
        <p className="mt-1 text-[22px] tracking-[-0.03em] text-black max-sm:text-[18px]">
          {gone ? "Nothing remains" : destroying ? "Tearing down" : "Production not in path"}
        </p>
      </div>

      <ol className="mt-6 flex gap-1">
        {BEATS.map((item, i) => {
          const on = i === beat;
          const done = i < beat || gone;
          return (
            <li key={item.id} className="min-w-0 flex-1">
              <div
                className={cn(
                  "h-1 rounded-full",
                  on && !gone ? "bg-[#33bf00]" : done ? "bg-black/55" : "bg-black/12",
                )}
                style={{ transition: `background-color 700ms ${FILM}` }}
              />
              <div
                className={cn(
                  "mt-2 text-[12px] tracking-tight",
                  on && !gone ? "text-black" : "text-black/40",
                )}
              >
                {item.label}
              </div>
            </li>
          );
        })}
      </ol>

      <div className="mt-6 grid grid-cols-1 gap-3 lg:grid-cols-[minmax(0,0.95fr)_minmax(0,1.15fr)_minmax(0,1.35fr)]">
        <article className="rounded-[12px] border border-black/10 bg-white p-4">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-[13px] tracking-tight text-black/55">Production</h3>
            <span className="text-[11px] tracking-tight text-[#C43D3D]">Not in path</span>
          </div>
          <ul className="mt-4 opacity-60">
            {PROD.map((row) => (
              <li
                key={row.id}
                className="flex items-center justify-between border-b border-black/[0.06] py-2.5 last:border-0"
              >
                <span className={cn("text-[13px] tracking-tight", "cut" in row && row.cut ? "text-[#C43D3D]" : "text-black/50")}>
                  {row.label}
                </span>
                {"cut" in row && row.cut ? (
                  <span className="relative size-2.5 shrink-0" aria-hidden>
                    <span className="absolute inset-x-0 top-1/2 h-px -rotate-45 bg-[#EB5757]" />
                    <span className="absolute inset-x-0 top-1/2 h-px rotate-45 bg-[#EB5757]" />
                  </span>
                ) : (
                  <span className="size-1.5 rounded-full bg-black/15" />
                )}
              </li>
            ))}
          </ul>
        </article>

        <article className="relative overflow-hidden rounded-[12px] border border-black/10 bg-white p-4 shadow-[0_18px_40px_rgba(0,0,0,0.06)]">
          <h3 className="text-[13px] tracking-tight text-black">Containment</h3>
          <p className="mt-1 text-[12px] leading-snug tracking-tight text-black/45">
            The twin cannot reach live keys or the public internet.
          </p>
          <ul className="mt-4">
            {SEALS.map((seal) => {
              const on = t >= seal.from && !gone;
              const held = gone;
              return (
                <li key={seal.id} className="border-b border-black/[0.06] py-2.5 last:border-0">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[13px] tracking-tight text-black">{seal.label}</span>
                    <span
                      className={cn(
                        "size-2 shrink-0 rounded-full",
                        on || held ? "bg-[#33bf00]" : "bg-black/15",
                      )}
                    />
                  </div>
                  <p className="mt-0.5 text-[12px] tracking-tight text-black/45">{seal.detail}</p>
                </li>
              );
            })}
          </ul>
        </article>

        <article className="rounded-[12px] border border-black/10 bg-white p-4 shadow-[0_18px_40px_rgba(0,0,0,0.06)]">
          <div className="flex items-center justify-between gap-2">
            <h3 className="text-[13px] tracking-tight text-black">Disposable twin</h3>
            <span className="text-[11px] tracking-tight text-black/40">{gone ? "empty" : "live"}</span>
          </div>
          <ul className="mt-4">
            {SLOTS.map((slot, i) => {
              const fill = slotFill(t, i);
              return (
                <li key={slot.id} className="border-b border-black/[0.06] py-2.5 last:border-0">
                  <div className="flex items-center justify-between gap-2">
                    <span className="text-[13px] tracking-tight text-black">{slot.label}</span>
                    <span className="text-[12px] tabular-nums tracking-tight text-black/40">
                      {fill < 0.08 ? (gone ? "gone" : "—") : `${Math.round(fill * 100)}%`}
                    </span>
                  </div>
                  <span className="mt-2 block h-[3px] overflow-hidden bg-black/[0.06]">
                    <span
                      className="block h-full bg-black/35"
                      style={{
                        width: `${fill * 100}%`,
                        transition: `width 700ms ${FILM}`,
                      }}
                    />
                  </span>
                </li>
              );
            })}
          </ul>
        </article>
      </div>

      <div className="mt-6">
        <div className="text-[13px] tracking-tight text-black">Cleanup proof</div>
        <p className="mt-0.5 text-[12px] tracking-tight text-black/45">
          {gone
            ? "Every resource journaled, destroyed, and counted."
            : "Resources are journaled as they come up. Nothing outlives the run."}
        </p>
      </div>
    </div>
  );
}

export function TwinIdeStage() {
  const glow = useRef<HTMLDivElement>(null);
  const { story } = useInViewPlay(glow, 0.15);

  return (
    <div className="relative min-w-0 overflow-hidden rounded-[3px] border border-black/12 bg-[#f7f7f5]">
      <div className="pointer-events-none absolute inset-0" aria-hidden>
        <div
          className="absolute inset-0"
          style={{
            background:
              "radial-gradient(ellipse 52% 48% at 0% 0%, rgba(51,191,0,0.48), transparent 72%), radial-gradient(ellipse 52% 48% at 100% 100%, rgba(0,229,153,0.44), transparent 72%)",
            opacity: story ? 1 : 0.78,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          ref={glow}
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#33bf00 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 0% 0%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="absolute inset-0"
          style={{
            backgroundImage: "radial-gradient(#00e599 1.15px, transparent 1.3px)",
            backgroundSize: "6.5px 6.5px",
            WebkitMaskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            maskImage: "radial-gradient(ellipse 52% 48% at 100% 100%, black 0%, transparent 70%)",
            opacity: story ? 0.85 : 0.55,
            transition: "opacity 0.8s ease",
          }}
        />
        <div
          className="auth-honeycomb absolute inset-0"
          style={{
            WebkitMaskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            maskImage:
              "radial-gradient(ellipse 50% 46% at 0% 0%, black 0%, transparent 68%), radial-gradient(ellipse 50% 46% at 100% 100%, black 0%, transparent 68%)",
            opacity: 0.18,
          }}
        />
      </div>

      <div className="relative px-5 pb-6 pt-6 sm:px-8 sm:pb-8 sm:pt-8 lg:px-12 lg:pb-10">
        <TwinIsolationMap />
      </div>
    </div>
  );
}
