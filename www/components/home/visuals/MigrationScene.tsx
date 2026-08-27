"use client";

import { useEffect, useRef, useState, type ReactNode } from "react";
import { AnimatePresence, motion } from "motion/react";
import { cn } from "@/lib/cn";
import { EASE, clamp, lerp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { Caret } from "@/components/motion/Caret";
import {
  FILM_EASE,
  Hairline,
  LockBadge,
  MonoLabel,
  QueueChip,
  Sparkline,
  StatusPill,
  seededNoise,
} from "@/components/home/visuals/primitives";

export type MigrationBar = {
  verdict: "BLOCK" | "PASS";
  slam: boolean;
  decided: boolean;
  passGlow: boolean;
};

type SceneProps = {
  tab: 0 | 1;
  playId: number;
  onBar?: (bar: MigrationBar) => void;
};

type Op = {
  id: string;
  at: number;
  label: string;
  title: string;
};

const LOOP = 15.2;
const FADE = 0.55;
const HOLD = 14.4;
const SPARK_W = 220;
const SPARK_H = 36;
const POOL_N = 20;
const LOCK_LIMIT = 2;

const SQL_RISKY =
  "ALTER TABLE subscriptions ADD COLUMN access_tier text NOT NULL DEFAULT 'free';";
const SQL_EXPAND = "ALTER TABLE subscriptions ADD COLUMN access_tier text;";
const SQL_BACKFILL =
  "UPDATE subscriptions SET access_tier = 'free' WHERE access_tier IS NULL LIMIT 2000;";
const SQL_CONTRACT =
  "ALTER TABLE subscriptions ALTER COLUMN access_tier SET NOT NULL;";

const OPS_A: Op[] = [
  { id: "submit", at: 0, label: "Submit", title: "Apply migration" },
  { id: "lock", at: 2.05, label: "Lock", title: "ACCESS EXCLUSIVE" },
  { id: "queue", at: 3.7, label: "Queue", title: "Blocked statements" },
  { id: "pool", at: 5.55, label: "Pool", title: "Pool exhaustion" },
  { id: "plan", at: 7.45, label: "Plan", title: "Plan regression" },
  { id: "rollback", at: 9.35, label: "Rollback", title: "Rollback feasibility" },
  { id: "block", at: 11.15, label: "BLOCK", title: "Verdict" },
];

const OPS_B: Op[] = [
  { id: "expand", at: 0, label: "Expand", title: "Nullable column" },
  { id: "backfill", at: 2.35, label: "Backfill", title: "Batched backfill" },
  { id: "dual", at: 6.15, label: "Dual-read", title: "Compatibility window" },
  { id: "contract", at: 8.55, label: "Contract", title: "Constraint later" },
  { id: "pass", at: 10.85, label: "PASS", title: "Verdict" },
];

const LOCK_AT = OPS_A[1].at;
const STAMP_A = OPS_A[6].at;
const STAMP_B = OPS_B[4].at;
const SHORT_LOCK_END = 0.72;

const ROWS = [
  { id: "10041", customer: "acme", status: "active" },
  { id: "10042", customer: "north", status: "trial" },
  { id: "10043", customer: "helix", status: "active" },
  { id: "10044", customer: "lumen", status: "past_due" },
  { id: "10045", customer: "orbit", status: "active" },
  { id: "10046", customer: "pivot", status: "active" },
] as const;

const TRAFFIC = [
  { method: "POST", path: "/v1/checkout", pid: "1842", enter: 0.4, lat: 214 },
  { method: "POST", path: "/v1/billing/upgrade", pid: "1843", enter: 0.95, lat: 268 },
  { method: "GET", path: "/v1/subscriptions", pid: "1844", enter: 1.4, lat: 41 },
  { method: "POST", path: "/v1/checkout", pid: "1845", enter: 3.9, lat: 241 },
  { method: "POST", path: "/v1/billing/upgrade", pid: "1846", enter: 4.55, lat: 276 },
] as const;

function opIndex(ops: Op[], t: number) {
  let i = 0;
  for (let k = 0; k < ops.length; k += 1) {
    if (t >= ops[k].at) i = k;
  }
  return i;
}

function opProgress(ops: Op[], t: number, i: number) {
  const start = ops[i].at;
  const end = ops[i + 1]?.at ?? HOLD;
  return clamp((t - start) / Math.max(0.001, end - start));
}

function typeText(text: string, t: number, start: number, cps = 34) {
  if (t < start) return "";
  return text.slice(0, Math.min(text.length, Math.max(0, Math.floor((t - start) * cps))));
}

function fmtMs(ms: number) {
  return ms >= 1000 ? `${(ms / 1000).toFixed(1)}s` : `${Math.round(ms)}ms`;
}

function fmtClock(t: number) {
  const m = Math.floor(t / 60);
  const s = t % 60;
  const whole = Math.floor(s);
  const tenth = Math.floor((s - whole) * 10);
  return `t+${String(m).padStart(2, "0")}:${String(whole).padStart(2, "0")}.${tenth}`;
}

function lockHold(t: number) {
  if (t < LOCK_AT) return 0;
  return Math.min(27.4, lerp(0, 27.4, clamp((t - LOCK_AT) / (10.6 - LOCK_AT))));
}

function poolUsed(t: number, locked: boolean) {
  if (!locked) return 12;
  return Math.round(lerp(12, 20, clamp((t - OPS_A[3].at) / 1.7)));
}

function poolWait(t: number, locked: boolean) {
  if (t < OPS_A[3].at) return 0;
  return Math.round(lerp(0, 14, clamp((t - OPS_A[3].at) / 2.6)));
}

function blockedStmts(t: number, locked: boolean) {
  if (!locked) return 0;
  return Math.round(lerp(0, 184, clamp((t - OPS_A[2].at) / 6.2)));
}

function p99ms(t: number, locked: boolean) {
  const grain = (seededNoise(Math.floor(t * 12), 3) - 0.5) * 36;
  if (!locked) return Math.round(820 + grain);
  return Math.round(lerp(820, 6900, clamp((t - LOCK_AT) / 7.4)) + grain * 0.28);
}

function timeouts(t: number, locked: boolean) {
  if (t < OPS_A[4].at) return 0;
  return lerp(0, 11.8, clamp((t - OPS_A[4].at) / 3.5));
}

function sparkPath(t: number, valueAt: (u: number) => number) {
  const n = 48;
  const window = 7.2;
  const pts: string[] = [];
  for (let i = 0; i < n; i += 1) {
    const u = i / (n - 1);
    const ti = Math.max(0, t - window * (1 - u));
    const ms = valueAt(ti);
    const y = SPARK_H - 3 - clamp((ms - 400) / 6800) * (SPARK_H - 6);
    const x = u * SPARK_W;
    pts.push(`${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`);
  }
  return pts.join(" ");
}

function useFilmClock(playing: boolean, reduced: boolean, stillT: number) {
  const [frame, setFrame] = useState({ t: 0, fade: 0 });

  usePausedRaf(playing && !reduced, (_now, elapsedMs) => {
    const elapsed = elapsedMs / 1000;
    const period = LOOP + FADE;
    const cycle = elapsed % period;
    if (cycle < LOOP) setFrame({ t: cycle, fade: 0 });
    else setFrame({ t: LOOP, fade: (cycle - LOOP) / FADE });
  });

  if (reduced) return { t: stillT, fade: 0 };
  return frame;
}

function StepRail({ ops, index, progress }: { ops: Op[]; index: number; progress: number }) {
  return (
    <div className="flex min-h-[40px] shrink-0 items-center gap-0 overflow-x-auto border-t border-black/10 px-3 max-md:px-2">
      {ops.map((op, i) => {
        const fill = i < index ? 1 : i === index ? progress : 0;
        const done = i < index;
        const active = i === index;
        return (
          <div key={op.id} className="flex min-w-0 items-center">
            {i > 0 ? (
              <span className="relative mx-0.5 h-px w-4 overflow-hidden bg-black/10 max-md:w-2.5">
                <span
                  className={cn("absolute inset-y-0 left-0", done || active ? "bg-[#285D49]" : "bg-transparent")}
                  style={{ width: `${fill * 100}%` }}
                />
              </span>
            ) : null}
            <span
              className={cn(
                "relative shrink-0 px-1.5 py-1 font-sans text-[10px] tracking-extra-tight uppercase transition-colors duration-300",
                active && "bg-white text-black ring-1 ring-black/12",
                done && !active && "text-[#285D49]",
                !done && !active && "text-black/28",
              )}
            >
              {op.label}
            </span>
          </div>
        );
      })}
    </div>
  );
}

function PoolSlots({ used, wait }: { used: number; wait: number }) {
  return (
    <div className="flex items-end gap-[3px]">
      {Array.from({ length: POOL_N }).map((_, i) => {
        const on = i < used;
        const hot = wait > 0 && i >= POOL_N - 3;
        return (
          <span
            key={i}
            className="h-2.5 w-[7px] ring-1 ring-black/12 max-md:w-[5px] max-md:h-2"
            style={{
              background: on ? (hot ? "#dc2626" : "#285D49") : "transparent",
              transition: `background 240ms ${FILM_EASE}`,
            }}
          />
        );
      })}
    </div>
  );
}

function Meter({
  label,
  value,
  hint,
  hot,
  fill,
}: {
  label: string;
  value: string;
  hint?: string;
  hot?: boolean;
  fill: number;
}) {
  return (
    <div className="min-w-0">
      <div className="flex items-baseline justify-between gap-2">
        <MonoLabel>{label}</MonoLabel>
        <span
          className={cn(
            "font-sans text-[13px] tabular-nums tracking-extra-tight max-md:text-[12px]",
            hot ? "text-red-700" : "text-[#285D49]",
          )}
        >
          {value}
        </span>
      </div>
      {hint ? <p className="mt-0.5 font-sans text-[10px] tracking-extra-tight text-black/40">{hint}</p> : null}
      <div className="mt-1.5 h-[3px] overflow-hidden bg-black/10">
        <div
          className={cn("h-full", hot ? "bg-red-600" : "bg-[#33bf00]")}
          style={{ width: `${Math.max(3, clamp(fill) * 100)}%` }}
        />
      </div>
    </div>
  );
}

function SqlLine({ text, t, start, cps = 36 }: { text: string; t: number; start: number; cps?: number }) {
  const shown = typeText(text, t, start, cps);
  const started = t >= start;
  const done = shown.length >= text.length && started;
  return (
    <p className="min-h-[2.6em] font-sans text-[12px] leading-[1.45] tracking-extra-tight text-black/75 max-md:text-[11px]">
      {shown}
      {started && !done ? <Caret className="bg-[#285D49]" /> : null}
    </p>
  );
}

function TablePane({
  locked,
  fillThrough,
  windowRow,
  shortLock,
}: {
  locked: boolean;
  fillThrough: number;
  windowRow: number;
  shortLock: boolean;
}) {
  return (
    <div className="relative overflow-hidden ring-1 ring-black/10">
      <div className="flex items-center justify-between gap-2 border-b border-black/10 bg-white/70 px-2.5 py-1.5">
        <MonoLabel className="text-black/55">subscriptions</MonoLabel>
        {locked ? (
          <LockBadge exclusive />
        ) : shortLock ? (
          <LockBadge exclusive={false} />
        ) : (
          <MonoLabel>ShareUpdate</MonoLabel>
        )}
      </div>
      <div className="relative bg-[#f7fbf8]">
        <div
          className="pointer-events-none absolute inset-0"
          style={{
            opacity: locked ? 1 : 0,
            background:
              "repeating-linear-gradient(-45deg, transparent, transparent 5px, rgba(220,38,38,0.07) 5px, rgba(220,38,38,0.07) 6px)",
            transition: `opacity 420ms ${FILM_EASE}`,
          }}
        />
        <table className="relative w-full border-collapse text-left">
          <thead>
            <tr className="font-sans text-[9px] tracking-extra-tight text-black/40">
              <th className="px-2.5 py-1.5 font-medium">id</th>
              <th className="px-2.5 py-1.5 font-medium">customer</th>
              <th className="px-2.5 py-1.5 font-medium">status</th>
              <th className="px-2.5 py-1.5 font-medium">access_tier</th>
            </tr>
          </thead>
          <tbody>
            {ROWS.map((row, i) => {
              const filled = i < fillThrough;
              const inWindow = windowRow >= 0 && i >= windowRow && i < windowRow + 2;
              return (
                <tr
                  key={row.id}
                  className="font-sans text-[11px] tracking-extra-tight text-black/70 max-md:text-[10px]"
                  style={{
                    background: locked
                      ? "rgba(220,38,38,0.06)"
                      : inWindow
                        ? "rgba(0,229,153,0.28)"
                        : filled
                          ? "rgba(51,191,0,0.12)"
                          : "transparent",
                    transition: `background 280ms ${FILM_EASE}`,
                  }}
                >
                  <td className="px-2.5 py-1 tabular-nums">{row.id}</td>
                  <td className="px-2.5 py-1">{row.customer}</td>
                  <td className="px-2.5 py-1">{row.status}</td>
                  <td className={cn("px-2.5 py-1", locked ? "text-red-700/80" : filled ? "text-[#285D49]" : "text-black/30")}>
                    {locked ? "rewrite" : filled ? "free" : "null"}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
    </div>
  );
}

function TrafficPane({
  t,
  locked,
  flowing,
}: {
  t: number;
  locked: boolean;
  flowing: boolean;
}) {
  const anyBlocked = TRAFFIC.some(
    (req) => locked && req.enter >= LOCK_AT - 0.2 && t >= req.enter,
  );
  return (
    <div className="flex min-h-0 flex-col">
      <div className="mb-1.5 flex items-baseline justify-between gap-2">
        <MonoLabel>checkout traffic</MonoLabel>
        <MonoLabel className={anyBlocked ? "text-red-700/70" : "text-black/40"}>
          {anyBlocked ? "waiting on subscriptions" : locked ? "lock acquired" : "live"}
        </MonoLabel>
      </div>
      <ul className="space-y-1">
        {TRAFFIC.map((req) => {
          const visible = t >= req.enter - 0.04;
          const blocked = locked && req.enter >= LOCK_AT - 0.2;
          const waitS = blocked ? Math.max(0, t - Math.max(req.enter, LOCK_AT)) : 0;
          const ok = flowing && !blocked && t >= req.enter + 0.48;
          return (
            <li
              key={req.pid}
              className="flex items-center justify-between gap-2"
              style={{
                opacity: visible ? 1 : 0.22,
                transform: visible ? "translateY(0px)" : "translateY(3px)",
                transition: `opacity 340ms ${FILM_EASE}, transform 340ms ${FILM_EASE}`,
              }}
            >
              <QueueChip blocked={blocked && visible}>
                {req.method} {req.path}
              </QueueChip>
              <span
                className={cn(
                  "font-sans text-[10px] tabular-nums tracking-extra-tight",
                  blocked && visible ? "text-red-700" : ok ? "text-[#285D49]" : "text-black/35",
                )}
              >
                {blocked && visible ? `wait ${waitS.toFixed(1)}s` : ok ? `ok ${req.lat}ms` : "—"}
              </span>
            </li>
          );
        })}
      </ul>
    </div>
  );
}

function Chrome({
  kicker,
  op,
  clock,
  badge,
  children,
  fade,
  rail,
}: {
  kicker: string;
  op: Op;
  clock: string;
  badge: ReactNode;
  children: ReactNode;
  fade: number;
  rail: ReactNode;
}) {
  return (
    <div className="relative flex w-full flex-col select-none" aria-hidden>
      <div className="flex h-11 shrink-0 items-center justify-between gap-3 border-b border-black/10 bg-white/50 px-3 max-md:h-10 max-md:px-2">
        <div className="flex min-w-0 items-center gap-2.5">
          <MonoLabel className="shrink-0 text-black/45">{kicker}</MonoLabel>
          <Hairline vertical className="h-3.5 w-px bg-black/12" />
          <AnimatePresence mode="wait">
            <motion.span
              key={op.id}
              initial={{ opacity: 0, y: 5 }}
              animate={{ opacity: 1, y: 0 }}
              exit={{ opacity: 0, y: -5 }}
              transition={{ duration: 0.28, ease: EASE }}
              className="truncate font-sans text-[12px] tracking-extra-tight text-black max-md:text-[11px]"
            >
              {op.title}
            </motion.span>
          </AnimatePresence>
        </div>
        <div className="flex shrink-0 items-center gap-2.5">
          {badge}
          <span className="font-sans text-[11px] tabular-nums tracking-extra-tight text-black/45 max-md:hidden">
            {clock}
          </span>
        </div>
      </div>
      <div className="relative">{children}</div>
      {rail}
      <div className="pointer-events-none absolute inset-0 bg-[#E4F1EB]" style={{ opacity: fade }} />
    </div>
  );
}

function TabA({ t, fade }: { t: number; fade: number }) {
  const viewT = Math.min(t, HOLD);
  const locked = viewT >= LOCK_AT;
  const idx = opIndex(OPS_A, viewT);
  const op = OPS_A[idx];
  const progress = opProgress(OPS_A, viewT, idx);
  const lock = lockHold(viewT);
  const used = poolUsed(viewT, locked);
  const wait = poolWait(viewT, locked);
  const blocked = blockedStmts(viewT, locked);
  const p99 = p99ms(viewT, locked);
  const tout = timeouts(viewT, locked);
  const planHot = viewT >= OPS_A[4].at;
  const rollbackHot = viewT >= OPS_A[5].at;
  const stamped = viewT >= STAMP_A;
  const sparkHot = locked && p99 > 1200;

  return (
    <Chrome
      kicker="pr 184 · add_billing_status"
      op={op}
      clock={fmtClock(viewT)}
      badge={stamped ? <StatusPill tone="BLOCK">BLOCK</StatusPill> : <MonoLabel>measuring</MonoLabel>}
      fade={fade}
      rail={<StepRail ops={OPS_A} index={idx} progress={progress} />}
    >
      <div className="grid grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)] max-md:grid-cols-1">
        <div className="flex flex-col gap-3 border-r border-black/10 bg-[#f4f7f5] p-3 max-md:border-r-0 max-md:border-b max-md:p-2.5">
          <div>
            <MonoLabel>statement</MonoLabel>
            <SqlLine text={SQL_RISKY} t={viewT} start={0.12} />
          </div>
          <TablePane locked={locked} fillThrough={locked ? ROWS.length : 0} windowRow={-1} shortLock={false} />
          <TrafficPane t={viewT} locked={locked} flowing />
        </div>

        <div className="flex flex-col gap-3 bg-[#d7ebe3] p-3 max-md:p-2.5">
          <Meter
            label="lock hold"
            value={`${lock.toFixed(1)}s`}
            hint={locked ? "ACCESS EXCLUSIVE · threshold 2.0s" : "no exclusive lock"}
            hot={lock > LOCK_LIMIT}
            fill={lock / 27.4}
          />
          <Meter
            label="pool"
            value={wait > 0 ? `${used}/${POOL_N} +${wait}` : `${used}/${POOL_N}`}
            hint={wait > 0 ? "checkout connections waiting" : "headroom remaining"}
            hot={wait > 0}
            fill={used / POOL_N}
          />
          <div className="min-w-0">
            <PoolSlots used={used} wait={wait} />
          </div>
          <div className="min-w-0">
            <div className="flex items-baseline justify-between gap-2">
              <MonoLabel>checkout p99</MonoLabel>
              <span
                className={cn(
                  "font-sans text-[13px] tabular-nums tracking-extra-tight",
                  sparkHot ? "text-red-700" : "text-[#285D49]",
                )}
              >
                {fmtMs(p99)}
              </span>
            </div>
            <p className="mt-0.5 font-sans text-[10px] tracking-extra-tight text-black/40">baseline 820ms</p>
            <Sparkline
              d={sparkPath(viewT, (u) => p99ms(u, u >= LOCK_AT))}
              width={SPARK_W}
              height={SPARK_H}
              color={sparkHot ? "#dc2626" : "#285D49"}
              className="mt-1 h-7 w-full"
            />
          </div>
          <Hairline />
          <div className="grid grid-cols-3 gap-2">
            <div>
              <MonoLabel>blocked</MonoLabel>
              <p className={cn("mt-0.5 font-sans text-[12px] tabular-nums", locked ? "text-red-700" : "text-[#285D49]")}>
                {blocked}
              </p>
            </div>
            <div>
              <MonoLabel>plan</MonoLabel>
              <p className={cn("mt-0.5 font-sans text-[12px]", planHot ? "text-red-700" : "text-black/55")}>
                {planHot ? "Seq Scan" : "Index Scan"}
              </p>
            </div>
            <div>
              <MonoLabel>timeouts</MonoLabel>
              <p className={cn("mt-0.5 font-sans text-[12px] tabular-nums", tout > 1 ? "text-red-700" : "text-[#285D49]")}>
                {tout.toFixed(1)}%
              </p>
            </div>
          </div>
          <div
            className="mt-auto ring-1 ring-black/10 bg-white/60 px-2.5 py-2"
            style={{
              opacity: rollbackHot ? 1 : 0.35,
              transition: `opacity 400ms ${FILM_EASE}`,
            }}
          >
            <MonoLabel>rollback</MonoLabel>
            <p className={cn("mt-0.5 font-sans text-[12px] tracking-extra-tight", rollbackHot ? "text-red-700" : "text-black/45")}>
              {rollbackHot ? "unsafe · old app cannot read candidate writes" : "measuring…"}
            </p>
          </div>
        </div>
      </div>
    </Chrome>
  );
}

function TabB({ t, fade }: { t: number; fade: number }) {
  const viewT = Math.min(t, HOLD);
  const idx = opIndex(OPS_B, viewT);
  const op = OPS_B[idx];
  const progress = opProgress(OPS_B, viewT, idx);
  const phase: 0 | 1 | 2 | 3 = viewT < OPS_B[1].at ? 0 : viewT < OPS_B[2].at ? 1 : viewT < OPS_B[3].at ? 2 : 3;
  const fillThrough =
    phase === 0 ? 0 : phase === 1 ? Math.round(clamp((viewT - OPS_B[1].at) / 3.6) * ROWS.length) : ROWS.length;
  const windowRow =
    phase === 1 ? Math.min(ROWS.length - 2, Math.floor(((viewT - OPS_B[1].at) * 1.35) % (ROWS.length - 1))) : -1;
  const shortLock = viewT >= 0.18 && viewT < SHORT_LOCK_END;
  const lockS = shortLock ? lerp(0, 0.4, clamp((viewT - 0.18) / 0.22)) : viewT >= SHORT_LOCK_END ? 0.4 : 0;
  const p99 = 820 + Math.round((seededNoise(Math.floor(viewT * 9), 5) - 0.5) * 42);
  const sql = phase === 0 ? SQL_EXPAND : phase === 1 ? SQL_BACKFILL : phase >= 3 ? SQL_CONTRACT : SQL_BACKFILL;
  const sqlStart = phase === 0 ? 0.1 : phase === 1 ? OPS_B[1].at : phase >= 3 ? OPS_B[3].at : OPS_B[2].at;
  const stamped = viewT >= STAMP_B;
  const rollbackHot = viewT >= OPS_B[2].at;

  return (
    <Chrome
      kicker="pr 184 · expand-and-contract"
      op={op}
      clock={fmtClock(viewT)}
      badge={stamped ? <StatusPill tone="PASS">PASS</StatusPill> : <MonoLabel>measuring</MonoLabel>}
      fade={fade}
      rail={<StepRail ops={OPS_B} index={idx} progress={progress} />}
    >
      <div className="grid grid-cols-[minmax(0,1.15fr)_minmax(0,0.85fr)] max-md:grid-cols-1">
        <div className="flex flex-col gap-3 border-r border-black/10 bg-[#f4f7f5] p-3 max-md:border-r-0 max-md:border-b max-md:p-2.5">
          <div>
            <MonoLabel>statement</MonoLabel>
            <SqlLine key={sql} text={sql} t={viewT} start={sqlStart} cps={40} />
          </div>
          <TablePane locked={false} fillThrough={fillThrough} windowRow={windowRow} shortLock={shortLock} />
          <TrafficPane t={viewT} locked={false} flowing />
        </div>

        <div className="flex flex-col gap-3 bg-[#d7ebe3] p-3 max-md:p-2.5">
          <Meter
            label="lock hold"
            value={`${lockS.toFixed(1)}s`}
            hint={shortLock ? "ShareUpdate · metadata only" : "below 2.0s threshold"}
            hot={false}
            fill={lockS / 2}
          />
          <Meter label="pool" value={`12/${POOL_N}`} hint="checkout connections healthy" hot={false} fill={12 / POOL_N} />
          <PoolSlots used={12} wait={0} />
          <div className="min-w-0">
            <div className="flex items-baseline justify-between gap-2">
              <MonoLabel>checkout p99</MonoLabel>
              <span className="font-sans text-[13px] tabular-nums tracking-extra-tight text-[#285D49]">{fmtMs(p99)}</span>
            </div>
            <p className="mt-0.5 font-sans text-[10px] tracking-extra-tight text-black/40">baseline 820ms</p>
            <Sparkline
              d={sparkPath(viewT, (u) => 820 + (seededNoise(Math.floor(u * 9), 5) - 0.5) * 42)}
              width={SPARK_W}
              height={SPARK_H}
              color="#285D49"
              className="mt-1 h-7 w-full"
            />
          </div>
          <Hairline />
          <div className="grid grid-cols-3 gap-2">
            <div>
              <MonoLabel>blocked</MonoLabel>
              <p className="mt-0.5 font-sans text-[12px] tabular-nums text-[#285D49]">0</p>
            </div>
            <div>
              <MonoLabel>plan</MonoLabel>
              <p className="mt-0.5 font-sans text-[12px] text-black/55">Index Scan</p>
            </div>
            <div>
              <MonoLabel>timeouts</MonoLabel>
              <p className="mt-0.5 font-sans text-[12px] tabular-nums text-[#285D49]">0.0%</p>
            </div>
          </div>
          <div
            className="mt-auto ring-1 ring-black/10 bg-white/60 px-2.5 py-2"
            style={{
              opacity: rollbackHot ? 1 : 0.35,
              transition: `opacity 400ms ${FILM_EASE}`,
            }}
          >
            <MonoLabel>rollback</MonoLabel>
            <p className={cn("mt-0.5 font-sans text-[12px] tracking-extra-tight", rollbackHot ? "text-[#285D49]" : "text-black/45")}>
              {rollbackHot ? "feasible · dual-read, old binary still valid" : "measuring…"}
            </p>
          </div>
        </div>
      </div>
    </Chrome>
  );
}

function PlayingFilm({
  tab,
  playing,
  reduced,
  onBar,
}: {
  tab: 0 | 1;
  playing: boolean;
  reduced: boolean;
  onBar?: (bar: MigrationBar) => void;
}) {
  const { t, fade } = useFilmClock(playing, reduced, HOLD);
  const lastKey = useRef("");
  const onBarRef = useRef(onBar);
  onBarRef.current = onBar;
  const stamp = tab === 0 ? STAMP_A : STAMP_B;

  useEffect(() => {
    const decided = reduced || t >= stamp;
    const slam = !reduced && tab === 0 && t >= stamp && t < stamp + 0.18;
    const next: MigrationBar = {
      verdict: tab === 0 ? "BLOCK" : "PASS",
      slam,
      decided,
      passGlow: tab === 1 && decided,
    };
    const key = `${next.verdict}|${next.slam}|${next.decided}|${next.passGlow}`;
    if (lastKey.current === key) return;
    lastKey.current = key;
    onBarRef.current?.(next);
  }, [t, tab, reduced, stamp]);

  return (
    <div className="relative w-full overflow-hidden border border-b-0 border-black/10 bg-[#d7ebe3] font-sans">
      {tab === 0 ? <TabA t={t} fade={fade} /> : <TabB t={t} fade={fade} />}
    </div>
  );
}

export function MigrationScene({ tab, playId, onBar }: SceneProps) {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(ref, 0.18);

  return (
    <div ref={ref} className="relative w-full min-w-0">
      <PlayingFilm key={`${tab}-${playId}`} tab={tab} playing={idle} reduced={reduced} onBar={onBar} />
    </div>
  );
}
