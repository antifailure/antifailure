"use client";

import { useEffect, useRef, useState } from "react";
import { cn } from "@/lib/cn";
import { clamp, lerp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import { Caret } from "@/components/motion/Caret";
import { LockChartMobile } from "@/components/home/media/LockChart";
import { FILM_EASE, Pill } from "./hero/linear";
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

type SceneProps = {
  tab: 0 | 1;
  playId: number;
  onTab?: (tab: 0 | 1) => void;
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
const LOCK_LIMIT = 2;

const TABS = ["Catch exclusive locks", "Safer expand-and-contract"] as const;
const TABS_SHORT = ["Exclusive lock", "Expand-and-contract"] as const;

/**
 * The risky statement is a type widening, not an ADD COLUMN with a default.
 *
 * It was `ADD COLUMN access_tier text NOT NULL DEFAULT 'free'`, under a film
 * whose beats are lock, queue, rewrite, plan, lint. Postgres stopped rewriting
 * for a constant default in version 11, and `af init` writes Postgres 17, so
 * the engine correctly reports no rewrite and no lint finding for that
 * statement: the film asserted an outcome the product measures as false. A
 * type change is the case the engine's own lint names, "int to bigint most
 * notably", and every beat of the film is true of it.
 */
const SQL_RISKY = "ALTER TABLE subscriptions ALTER COLUMN plan_id TYPE bigint;";
const SQL_EXPAND = "ALTER TABLE subscriptions ADD COLUMN plan_id_new bigint;";
const SQL_BACKFILL =
  "UPDATE subscriptions SET plan_id_new = plan_id WHERE plan_id_new IS NULL LIMIT 2000;";
const SQL_CONTRACT = "ALTER TABLE subscriptions DROP COLUMN plan_id;";

const OPS_A: Op[] = [
  { id: "submit", at: 0, label: "Apply", title: "Apply migration" },
  { id: "lock", at: 2.05, label: "Lock", title: "ACCESS EXCLUSIVE" },
  { id: "queue", at: 3.7, label: "Queue", title: "Checkout waiting" },
  { id: "rewrite", at: 5.55, label: "Rewrite", title: "Table rewritten" },
  { id: "plan", at: 7.45, label: "Plan", title: "Plan regression" },
  { id: "lint", at: 9.35, label: "Lint", title: "Unsafe pattern" },
  { id: "finding", at: 11.15, label: "FINDING", title: "Reported" },
];

const OPS_B: Op[] = [
  { id: "expand", at: 0, label: "Expand", title: "Nullable column" },
  { id: "backfill", at: 2.35, label: "Backfill", title: "Batched backfill" },
  { id: "dual", at: 6.15, label: "Dual-read", title: "Compatibility window" },
  { id: "contract", at: 8.55, label: "Contract", title: "Constraint later" },
  { id: "clean", at: 10.85, label: "CLEAN", title: "Reported" },
];

const LOCK_AT = OPS_A[1].at;
const STAMP_A = OPS_A[6].at;
const STAMP_B = OPS_B[4].at;
const SHORT_LOCK_END = 0.72;

const ROWS = [
  { id: "10041", customer: "acme", status: "active", plan: "31" },
  { id: "10042", customer: "north", status: "trial", plan: "12" },
  { id: "10043", customer: "helix", status: "active", plan: "31" },
  { id: "10044", customer: "lumen", status: "past_due", plan: "7" },
  { id: "10045", customer: "orbit", status: "active", plan: "12" },
  { id: "10046", customer: "pivot", status: "active", plan: "31" },
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

function typeText(text: string, t: number, start: number, cps = 34) {
  if (t < start) return "";
  return text.slice(0, Math.min(text.length, Math.max(0, Math.floor((t - start) * cps))));
}

function padStep(index: number, total: number) {
  return `${String(index + 1).padStart(2, "0")} / ${String(total).padStart(2, "0")}`;
}

function lockHold(t: number) {
  if (t < LOCK_AT) return 0;
  return Math.min(27.4, lerp(0, 27.4, clamp((t - LOCK_AT) / (10.6 - LOCK_AT))));
}

/**
 * Whether another session was seen waiting on the lock.
 *
 * This was a checkout p99 climbing from 820ms to 6.9s, then a count of blocked
 * statements climbing to 84. Neither is something this engine can produce.
 * insights.LockHold carries one boolean, Blocking, "whether another session
 * was ever seen waiting on it": no count of waiters, no list of what they were
 * running. A number here reads as a measurement and there is nothing behind
 * it, so this is the boolean the sampler actually records.
 */
function blockedAnother(t: number, locked: boolean) {
  return locked && t >= LOCK_AT + 1.2;
}

function useFilmClock(playing: boolean, reduced: boolean, stillT: number) {
  const [frame, setFrame] = useState({ t: 0, fade: 0 });

  usePausedRaf(playing && !reduced, (_now, elapsedMs) => {
    const elapsed = elapsedMs / 1000;
    const period = LOOP + FADE;
    const cycle = elapsed % period;
    const next =
      cycle < LOOP
        ? {
            t: Math.round(cycle * 30) / 30,
            fade: cycle < 0.38 ? Math.round((1 - cycle / 0.38) * 30) / 30 : 0,
          }
        : { t: HOLD, fade: Math.round(clamp((cycle - LOOP) / FADE) * 30) / 30 };
    setFrame((prev) => (prev.t === next.t && prev.fade === next.fade ? prev : next));
  });

  if (reduced) return { t: stillT, fade: 0 };
  return frame;
}

function SqlDiff({
  text,
  t,
  start,
  tone,
}: {
  text: string;
  t: number;
  start: number;
  tone: "block" | "ok";
}) {
  const shown = typeText(text, t, start);
  const started = t >= start;
  const done = shown.length >= text.length && started;
  return (
    <div
      className={cn(
        "h-[40px] overflow-hidden px-3 py-2 text-[12px] leading-snug tracking-extra-tight",
        tone === "block" ? "bg-[#EB5757]/[0.06]" : "bg-[#4CB782]/[0.08]",
      )}
    >
      <span className={cn("mr-2 tabular-nums", tone === "block" ? "text-[#C43D3D]" : "text-[#4CB782]")}>
        +
      </span>
      <span className={tone === "block" ? "text-[#C43D3D]" : "text-[#1A1A1A]"}>
        {shown || <span className="text-[#9B9EA5]">waiting to apply…</span>}
      </span>
      {started && !done ? <Caret className="bg-[#1A1A1A]" /> : null}
    </div>
  );
}

function TablePane({
  locked,
  fillThrough,
  windowRow,
  shortLock,
  // The risky plan changes plan_id in place; the safer one fills a second
  // column beside it. Naming both plan_id_new would say the risky migration
  // adds a column, which is the plan it is being contrasted with.
  column,
}: {
  locked: boolean;
  fillThrough: number;
  windowRow: number;
  shortLock: boolean;
  column: string;
}) {
  return (
    <div className="border-t border-black/[0.08] bg-white">
      <div className="flex items-center justify-between gap-2 border-b border-black/[0.08] px-3 py-1.5">
        <span className="text-[12px] tracking-extra-tight text-[#1A1A1A]">subscriptions</span>
        {locked ? (
          <Pill tone="block">ACCESS EXCLUSIVE</Pill>
        ) : shortLock ? (
          <Pill>ShareUpdate 0.4s</Pill>
        ) : (
          <span className="text-[11px] tracking-extra-tight text-[#9B9EA5]">ShareUpdate</span>
        )}
      </div>
      <div className="relative">
        <div
          className="pointer-events-none absolute inset-0"
          style={{
            opacity: locked ? 1 : 0,
            background:
              "repeating-linear-gradient(-45deg, transparent, transparent 5px, rgba(235,87,87,0.07) 5px, rgba(235,87,87,0.07) 6px)",
            transition: `opacity 420ms ${FILM_EASE}`,
          }}
        />
        <table className="relative w-full border-collapse text-left">
          <thead>
            <tr className="text-[11px] tracking-extra-tight text-[#9B9EA5]">
              <th className="px-3 py-1.5 font-medium">id</th>
              <th className="px-3 py-1.5 font-medium">customer</th>
              <th className="px-3 py-1.5 font-medium">status</th>
              <th className="px-3 py-1.5 font-medium">{column}</th>
            </tr>
          </thead>
          <tbody>
            {ROWS.map((row, i) => {
              const filled = i < fillThrough;
              const inWindow = windowRow >= 0 && i >= windowRow && i < windowRow + 2;
              return (
                <tr
                  key={row.id}
                  className="text-[12px] tracking-extra-tight text-[#3C3F44]"
                  style={{
                    background: locked
                      ? "rgba(235,87,87,0.05)"
                      : inWindow
                        ? "rgba(76,183,130,0.10)"
                        : filled
                          ? "rgba(76,183,130,0.08)"
                          : "transparent",
                    transition: `background 280ms ${FILM_EASE}`,
                  }}
                >
                  <td className="px-3 py-1.5 tabular-nums">{row.id}</td>
                  <td className="px-3 py-1.5">{row.customer}</td>
                  <td className="px-3 py-1.5">{row.status}</td>
                  <td
                    className={cn(
                      "px-3 py-1.5",
                      locked ? "text-[#C43D3D]" : filled ? "text-[#3C3F44]" : "text-[#C0C3C8]",
                    )}
                  >
                    {locked ? "rewrite" : filled ? row.plan : "null"}
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

function TrafficList({ t, locked, flowing }: { t: number; locked: boolean; flowing: boolean }) {
  return (
    <ul className="grid h-[130px] shrink-0 grid-rows-5 content-start gap-0 border-t border-black/[0.08] bg-white px-3 py-1.5">
      {TRAFFIC.map((req) => {
        const shown = t >= req.enter - 0.02;
        const blocked = shown && locked && req.enter >= LOCK_AT - 0.15;
        const waitS = blocked ? Math.max(0, t - Math.max(req.enter, LOCK_AT)) : 0;
        const ok = shown && flowing && !blocked && t >= req.enter + 0.42;
        return (
          <li
            key={req.pid}
            className="flex items-baseline justify-between gap-3"
            style={{
              opacity: shown ? 1 : 0,
              transition: `opacity 280ms ${FILM_EASE}`,
            }}
          >
            <span className="min-w-0 truncate text-[12px] tracking-extra-tight text-[#1A1A1A]">
              {req.method} {req.path}
            </span>
            <span
              className={cn(
                "shrink-0 text-[12px] tabular-nums tracking-extra-tight",
                blocked ? "text-[#C43D3D]" : ok ? "text-[#4CB782]" : "text-[#C0C3C8]",
              )}
            >
              {blocked ? `${waitS.toFixed(1)}s` : ok ? `${req.lat}ms` : "waiting"}
            </span>
          </li>
        );
      })}
    </ul>
  );
}

function Film({
  tab,
  playing,
  reduced,
  onTab,
}: {
  tab: 0 | 1;
  playing: boolean;
  reduced: boolean;
  onTab?: (tab: 0 | 1) => void;
}) {
  const { t, fade } = useFilmClock(playing, reduced, HOLD);
  const stamp = tab === 0 ? STAMP_A : STAMP_B;
  const viewT = Math.min(t, HOLD);
  const ops = tab === 0 ? OPS_A : OPS_B;
  const idx = opIndex(ops, viewT);

  const locked = tab === 0 && viewT >= LOCK_AT;
  const shortLock = tab === 1 && viewT >= 0.2 && viewT < SHORT_LOCK_END;
  const lockS =
    tab === 0
      ? lockHold(viewT)
      : shortLock
        ? lerp(0, 0.4, clamp((viewT - 0.2) / 0.22))
        : viewT >= SHORT_LOCK_END
          ? 0.4
          : 0;
  const blockedSession = tab === 0 && blockedAnother(viewT, locked);
  const stamped = viewT >= stamp;
  const lintHot = tab === 0 ? viewT >= OPS_A[5].at : viewT >= OPS_B[2].at;
  const lockHot = lockS > LOCK_LIMIT;


  const phase: 0 | 1 | 2 | 3 =
    tab === 1
      ? viewT < OPS_B[1].at
        ? 0
        : viewT < OPS_B[2].at
          ? 1
          : viewT < OPS_B[3].at
            ? 2
            : 3
      : 0;
  const fillThrough =
    tab === 0
      ? 0
      : phase === 0
        ? 0
        : phase === 1
          ? Math.round(clamp((viewT - 2.35) / 3.6) * ROWS.length)
          : ROWS.length;
  const windowRow =
    tab === 1 && phase === 1
      ? Math.min(ROWS.length - 2, Math.floor(((viewT - 2.35) / 3.6) * (ROWS.length - 1)))
      : -1;
  const sql =
    tab === 0 ? SQL_RISKY : phase === 0 ? SQL_EXPAND : phase === 1 ? SQL_BACKFILL : SQL_CONTRACT;
  const sqlStart = tab === 0 ? 0.12 : phase === 0 ? 0.1 : phase === 1 ? 2.35 : 8.55;

  const evidence =
    tab === 0
      ? `ACCESS EXCLUSIVE ${lockS.toFixed(1)}s · another session blocked · table rewritten · plan regressed`
      : `expand → backfill → contract · lock ${lockS.toFixed(1)}s · nothing blocked · no rewrite`;

  const moon = stamped ? (tab === 0 ? "block" : "ok") : locked ? "progress" : "progress";
  const action = stamped ? (tab === 0 ? "FINDING" : "CLEAN") : "Measuring";
  const lockValue = `${tab === 0 && lockS > 0 ? "+" : ""}${lockS.toFixed(1)}s`;

  return (
    <RunWindow fade={fade}>
      <InnerHeader
        moon={moon}
        breadcrumb={
          <span className="flex min-w-0 items-center gap-1.5">
            <span className="text-[#6B6F76]">PR 184</span>
            <span className="text-[#C0C3C8]">/</span>
            <span className="truncate">{tab === 0 ? "widen_plan_id" : "expand-and-contract"}</span>
          </span>
        }
        metrics={[
          { value: lockValue, tone: lockHot ? "block" : "ok" },
          {
            value: blockedSession ? "blocked a session" : "nothing blocked",
            tone: blockedSession ? "block" : "ok",
          },
        ]}
      />
      <InnerPills
        items={TABS}
        short={TABS_SHORT}
        active={tab}
        onSelect={onTab ? (index) => onTab(index === 0 ? 0 : 1) : undefined}
        action={
          stamped ? (
            <span className={tab === 0 ? "text-[#C43D3D]" : "text-[#4CB782]"}>{action}</span>
          ) : (
            action
          )
        }
      />
      <InnerSplit
        left={
          <>
            <FindingHead
              title={tab === 0 ? "Exclusive lock on subscriptions" : "Expand-and-contract stays live"}
              meta={
                <>
                  <PrivatePill />
                  <span className="text-[11px] tracking-extra-tight text-[#9B9EA5]">production not in path</span>
                </>
              }
              step={padStep(idx, ops.length)}
              body={
                tab === 0
                  ? "An exclusive lock on subscriptions holds while everything else queues. The rehearsal reports it before it ships."
                  : "Expand-and-contract holds the lock for 0.4s, and leaves nothing waiting on it."
              }
            />
            <div className="mt-4 flex flex-col gap-1.5">
              <EvidenceRow
                moon={lockHot ? "block" : lockS > 0 ? "progress" : "todo"}
                label={tab === 0 ? "ACCESS EXCLUSIVE" : "ShareUpdate"}
                value={lockS.toFixed(1) + "s"}
                tone={lockHot ? "block" : "ok"}
              />
              <EvidenceRow
                moon={blockedSession ? "block" : "ok"}
                label="Blocked another session"
                value={blockedSession ? "yes" : "no"}
                tone={blockedSession ? "block" : "ok"}
              />
              <EvidenceRow
                moon={tab === 0 ? (lintHot ? "block" : "todo") : "ok"}
                label="Table rewrite"
                value={tab === 0 ? (lintHot ? "yes" : "measuring") : "no"}
                tone={tab === 0 && lintHot ? "block" : "ok"}
              />
              <EvidenceRow
                moon={lintHot ? (tab === 0 ? "block" : "ok") : "progress"}
                label="Query plan"
                value={lintHot ? (tab === 0 ? "Seq Scan" : "unchanged") : "measuring"}
                tone={lintHot ? (tab === 0 ? "block" : "ok") : "muted"}
              />
            </div>
          </>
        }
        right={
          <NestedPane
            title={tab === 0 ? "subscriptions · rewrite" : "subscriptions · expand-and-contract"}
            meta={locked ? "waiting on lock" : "live"}
          >
            <SqlDiff key={sql} text={sql} t={viewT} start={sqlStart} tone={tab === 0 ? "block" : "ok"} />
            <div className="min-h-0 flex-1 overflow-hidden">
              <TablePane
                locked={locked}
                fillThrough={fillThrough}
                windowRow={windowRow}
                shortLock={shortLock}
                column={tab === 0 ? "plan_id" : "plan_id_new"}
              />
            </div>
            <TrafficList t={viewT} locked={locked} flowing />
          </NestedPane>
        }
      />
      <RunToast
        visible={stamped}
        tone={tab === 0 ? "block" : "ok"}
        title={tab === 0 ? "FINDING" : "CLEAN"}
        detail={evidence}
      />
    </RunWindow>
  );
}

export function MigrationScene({ tab, playId, onTab }: SceneProps) {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced } = useInViewPlay(ref, 0.18);
  const [localTab, setLocalTab] = useState<0 | 1>(tab);

  useEffect(() => {
    setLocalTab(tab);
  }, [tab, playId]);

  const viewTab = onTab ? tab : localTab;

  function selectTab(next: 0 | 1) {
    if (onTab) onTab(next);
    else setLocalTab(next);
  }

  return (
    <div ref={ref} className="@container relative w-full select-none">
      <LockChartMobile state={viewTab} />
      <div className="max-md:hidden">
        <Film
          key={`${viewTab}-${onTab ? playId : viewTab}`}
          tab={viewTab}
          playing={idle}
          reduced={reduced}
          onTab={selectTab}
        />
      </div>
    </div>
  );
}
