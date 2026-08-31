"use client";

/**
 * 19s pull request safety-report film. The truth is FAIL.
 *
 * Everything typed into the evidence column is a line af ci really writes: the
 * headline comes from report.go's Headline, the invariant sentence and its row
 * table from invariantSection and evidenceTable, and the outbound line from
 * the egress summary. The film used to type an oracle's deserialize mismatch,
 * a fidelity score and three policy thresholds, none of which exist.
 */
import { useEffect, useRef, useState, type ReactNode } from "react";
import { cn } from "@/lib/cn";
import { EASE_OUT_CUBIC, clamp } from "@/lib/easing";
import { useInViewPlay } from "@/lib/useInViewPlay";
import { usePausedRaf } from "@/lib/usePausedRaf";
import {
  CheckRow,
  FILM_EASE,
  Hairline,
  MonoLabel,
  Panel,
  QueueChip,
  Receipt,
  Sparkline,
  StatusPill,
  Timestamp,
  formatClock,
  seededNoise,
} from "@/components/home/visuals/primitives";

const LOOP = 19.3;
const HOLD_END = 19;
const DISSOLVE = 0.3;
const REDUCED_T = 8.95;
const DRAWER_MS = 0.22;

const TITLE = "1 workflow failed, and 1 invariant did not hold.";
const CLAUSES = [
  "Workflow `upgrade_to_pro` FAILED: the confirmation never appeared.",
  "Invariant `one_active_subscription` does not hold. 3 rows returned.",
  "Outbound: 18 allowed, 1 refused, 2 captured, 1 mocked.",
] as const;
const ACTION = "open /settings/billing · click Upgrade · submit · trace.zip · video.webm";
const LOG_LINE = "runner: expectation not met after 2 attempts";
const GREY_CHECKS = ["Lint / typecheck", "Unit tests", "Docker build"] as const;

const SUBCHECKS: { at: number; label: string }[] = [
  { at: 2.15, label: "golden verified" },
  { at: 2.75, label: "branch restored" },
  { at: 3.35, label: "containment in force" },
  { at: 3.95, label: "workflows running" },
  { at: 4.55, label: "invariants asked" },
];

const DRAWER_AT = [3.5, 4.32, 5.14, 5.96, 6.78] as const;
const DRAWER_H = [52, 78, 44, 44, 40] as const;
const TITLE_START = 0.02;
const CLAUSE_START = 0.96;
const ACTION_START = 2.0;

const LATENCY_SPARK = (() => {
  const pts: string[] = [];
  for (let i = 0; i <= 16; i++) {
    const x = i * 10;
    const n = seededNoise(i, 11);
    const slow = i >= 4 && i <= 12;
    const y = slow ? 7 + n * 2.2 : 28 - n * 3.5;
    pts.push(`${i === 0 ? "M" : "L"}${x.toFixed(1)} ${y.toFixed(1)}`);
  }
  return pts.join(" ");
})();

function ramp(t: number, start: number, dur: number) {
  return EASE_OUT_CUBIC(clamp((t - start) / dur, 0, 1));
}

function typed(text: string, t: number, start: number, cps: number) {
  if (t < start) return "";
  return text.slice(0, Math.min(text.length, Math.floor((t - start) * cps)));
}

function typing(text: string, t: number, start: number, cps: number) {
  if (t < start) return false;
  return Math.floor((t - start) * cps) < text.length;
}

function InkCaret({ on }: { on: boolean }) {
  if (!on) return null;
  return (
    <span
      // Static, for the reason in components/motion/Caret.tsx.
      className="ml-px inline-block h-[0.95em] w-[6px] translate-y-px bg-black align-middle"
      aria-hidden
    />
  );
}

function StepSquare({ t, className }: { t: number; className?: string }) {
  const step = Math.floor(t * 8) % 8;
  return (
    <svg
      width="10"
      height="10"
      viewBox="0 0 10 10"
      className={cn("shrink-0 text-black/55", className)}
      style={{ transform: `rotate(${step * 45}deg)` }}
      aria-hidden
    >
      <rect x="0.7" y="0.7" width="8.6" height="8.6" fill="none" stroke="currentColor" strokeWidth="1" />
      <rect x="0.7" y="0.7" width="8.6" height="2.1" fill="currentColor" />
    </svg>
  );
}

function BlockMark() {
  return (
    <svg width="10" height="10" viewBox="0 0 10 10" className="shrink-0 text-red-700" aria-hidden>
      <rect x="0.6" y="0.6" width="8.8" height="8.8" fill="none" stroke="currentColor" strokeWidth="1" />
      <path d="M2.4 2.4 L7.6 7.6 M7.6 2.4 L2.4 7.6" stroke="currentColor" strokeWidth="1" />
    </svg>
  );
}

function GreyDot() {
  return <span className="size-1.5 shrink-0 rounded-full bg-black/20" />;
}

function JourneyFrame({ i, error }: { i: number; error?: boolean }) {
  const labels = ["billing", "upgrade", "retry", "timeout"] as const;
  return (
    <svg viewBox="0 0 64 44" className="h-11 w-16" aria-hidden>
      <rect x="0.5" y="0.5" width="63" height="43" fill="#f4f7f5" stroke={error ? "#b91c1c" : "rgba(0,0,0,0.18)"} strokeWidth="1" />
      <rect x="6" y="6" width="52" height="3" fill="rgba(0,0,0,0.18)" />
      <rect x="6" y="12" width={i === 0 ? 40 : 28} height="2.5" fill="rgba(0,0,0,0.12)" />
      <rect x="6" y="17" width={i === 0 ? 34 : 22} height="2.5" fill="rgba(0,0,0,0.1)" />
      {i === 0 ? (
        <rect x="20" y="28" width="24" height="8" fill="none" stroke="rgba(0,0,0,0.35)" strokeWidth="1" />
      ) : i === 1 ? (
        <rect x="18" y="26" width="28" height="10" fill="none" stroke="#111" strokeWidth="1.2" />
      ) : i === 2 ? (
        <>
          <rect x="14" y="25" width="22" height="9" fill="none" stroke="#111" strokeWidth="1" />
          <rect x="28" y="28" width="22" height="9" fill="none" stroke="rgba(0,0,0,0.4)" strokeWidth="1" />
        </>
      ) : (
        <>
          <path d="M22 26 L42 38 M42 26 L22 38" stroke="#b91c1c" strokeWidth="1" />
          <rect x="0.5" y="0.5" width="63" height="43" fill="none" stroke="#b91c1c" strokeWidth="1" />
        </>
      )}
      <text x="32" y="41" textAnchor="middle" fontSize="6" fill={error ? "#b91c1c" : "rgba(0,0,0,0.45)"} fontFamily="ui-monospace, monospace">
        {labels[i]}
      </text>
    </svg>
  );
}

/** The evidence table af ci prints under a violated invariant. */
function InvariantRows() {
  const rows = [
    ["acct_00418", "2", "sub_9c41"],
    ["acct_02277", "2", "sub_a180"],
    ["acct_09903", "3", "sub_b774"],
  ];
  return (
    <table className="w-full text-left font-mono text-[9px] tabular-nums tracking-extra-tight">
      <thead>
        <tr className="text-black/35">
          <th className="pb-0.5 pr-4 font-normal">account_id</th>
          <th className="pb-0.5 pr-4 font-normal">active</th>
          <th className="pb-0.5 font-normal">latest</th>
        </tr>
      </thead>
      <tbody className="text-black/65">
        {rows.map((row) => (
          <tr key={row[0]} className="border-t border-black/[0.06]">
            <td className="py-0.5 pr-4">{row[0]}</td>
            <td className="py-0.5 pr-4">{row[1]}</td>
            <td className="py-0.5">{row[2]}</td>
          </tr>
        ))}
      </tbody>
    </table>
  );
}

function Drawer({
  t,
  start,
  height,
  title,
  children,
}: {
  t: number;
  start: number;
  height: number;
  title: string;
  children: ReactNode;
}) {
  const u = ramp(t, start, DRAWER_MS);
  return (
    <div className="overflow-hidden" style={{ height: u * height, opacity: 0.2 + u * 0.8 }}>
      <div className="border-t border-black/10 px-2.5 py-1.5" style={{ height }}>
        <MonoLabel className="text-[9px] uppercase">{title}</MonoLabel>
        <div className="mt-1">{children}</div>
      </div>
    </div>
  );
}

function ActionLine({ shown }: { shown: string }) {
  const segs: ReactNode[] = [];
  let rest = shown;
  let k = 0;
  while (rest.length) {
    const i = rest.indexOf("·");
    if (i === -1) {
      segs.push(
        <span className="text-black" key={k}>
          {rest}
        </span>,
      );
      break;
    }
    segs.push(
      <span className="text-black" key={k}>
        {rest.slice(0, i)}
      </span>,
    );
    segs.push(
      <span className="text-[#00E599]" key={`${k}-d`}>
        ·
      </span>,
    );
    rest = rest.slice(i + 1);
    k += 1;
  }
  return <span className="font-mono text-[11px] tracking-extra-tight">{segs}</span>;
}

function ChecksColumn({ t, blocked, running }: { t: number; blocked: boolean; running: boolean }) {
  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex items-center justify-between px-3 py-2">
        <MonoLabel className="uppercase">checks</MonoLabel>
        {t >= 1.2 && t < 5.5 ? (
          <Timestamp className="text-[10px]" value={`${t.toFixed(1)}s`} />
        ) : blocked ? (
          <Timestamp className="text-[10px]" value={formatClock(Math.min(t, HOLD_END))} />
        ) : null}
      </div>
      <Hairline />
      <ul className="flex flex-col">
        {GREY_CHECKS.map((name) => (
          <li key={name} className="flex items-center gap-2 px-3 py-1.5 font-mono text-[11px] tracking-extra-tight text-black/35">
            <GreyDot />
            {name}
          </li>
        ))}
      </ul>
      <div
        className="relative mx-2 mt-1 mb-2 px-2 py-2"
        style={{
          boxShadow: blocked ? "inset 1px 0 0 #b91c1c" : "inset 0 0 0 1px rgba(0,0,0,0.08)",
        }}
      >
        <div className="flex items-start gap-2">
          {blocked ? <BlockMark /> : running ? <StepSquare t={t} /> : <GreyDot />}
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-x-2 gap-y-0.5 font-mono text-[11px] tracking-extra-tight">
              {blocked ? (
                <span className="text-red-700">FAILED</span>
              ) : (
                <span className="text-black/70">{t < 2 ? "queued" : "running"}</span>
              )}
              <span className="text-black">Antifailure / deployment safety</span>
            </div>
            <div className="mt-1 flex flex-wrap items-center gap-1.5">
              {t < 2 ? (
                <>
                  <MonoLabel>env queued</MonoLabel>
                  <QueueChip>127.0.0.1:46000</QueueChip>
                </>
              ) : t < 5.5 ? (
                <MonoLabel>5 workflows, 2 invariants</MonoLabel>
              ) : (
                <StatusPill tone="FAIL">FAIL</StatusPill>
              )}
            </div>
          </div>
        </div>

        {t >= 2 && t < 5.5 ? (
          <div className="mt-2 space-y-1">
            {SUBCHECKS.map((row, i) => {
              const on = t >= row.at;
              if (!on) return null;
              const last = i === SUBCHECKS.length - 1;
              const ok = last ? ("run" as const) : true;
              return (
                <CheckRow key={row.label} ok={ok} className="text-[10px] text-black/60">
                  {row.label}
                </CheckRow>
              );
            })}
            <div className="pt-1" style={{ opacity: t >= 2.4 ? 0.85 : 0 }}>
              <Sparkline d={LATENCY_SPARK} width={160} height={36} color="rgba(17,17,17,0.38)" className="h-9 w-40" />
              <MonoLabel className="mt-0.5 block">p95 · sampled per route</MonoLabel>
            </div>
          </div>
        ) : null}

        {t >= 3 && t < 5.5 ? (
          <p className="mt-2 font-mono text-[10px] leading-4 tracking-extra-tight text-black/50">{LOG_LINE}</p>
        ) : null}
      </div>

      <div className="mt-auto border-t border-black/10 px-3 py-2.5">
        <button
          type="button"
          disabled
          tabIndex={-1}
          className="flex w-full cursor-not-allowed items-center justify-between bg-black/[0.04] px-2.5 py-1.5 font-mono text-[11px] tracking-extra-tight text-black/35 ring-1 ring-black/10"
        >
          <span>Merge pull request</span>
          <span className="text-black/25">▾</span>
        </button>
        <p className="mt-1.5 font-mono text-[9px] tracking-extra-tight text-black/30">
          {blocked ? "inert · required check failed" : "inert · waiting on deployment safety"}
        </p>
      </div>
    </div>
  );
}

function EvidenceColumn({ t }: { t: number }) {
  const evidenceOn = t > 0;
  const stamp = ramp(t, 0, 0.28);
  const shownTitle = evidenceOn ? typed(TITLE, t, TITLE_START, 38) : "";
  const titleBusy = evidenceOn && typing(TITLE, t, TITLE_START, 38);
  const clauseShown = CLAUSES.map((c, i) => typed(c, t, CLAUSE_START + i * 0.4, 92));
  const clauseBusy = CLAUSES.map((c, i) => typing(c, t, CLAUSE_START + i * 0.4, 92));
  const activeClause = clauseBusy.findIndex(Boolean);
  const shownAction = evidenceOn ? typed(ACTION, t, ACTION_START, 52) : "";
  const actionBusy = evidenceOn && typing(ACTION, t, ACTION_START, 52);
  const caretOn = titleBusy ? "title" : activeClause >= 0 ? `c${activeClause}` : actionBusy ? "action" : null;

  return (
    <div className="flex min-h-0 flex-col">
      <div className="flex items-center justify-between px-3 py-2">
        <MonoLabel className="uppercase">evidence</MonoLabel>
        {evidenceOn ? (
          <span
            className="origin-right"
            style={{
              opacity: stamp,
              transform: `scale(${0.96 + 0.04 * stamp})`,
            }}
          >
            <StatusPill tone="FAIL">FAILED</StatusPill>
          </span>
        ) : (
          <MonoLabel>empty</MonoLabel>
        )}
      </div>
      <Hairline />

      {!evidenceOn ? (
        <div className="flex flex-1 items-center justify-center px-4">
          <span className="font-mono text-[10px] tracking-extra-tight text-black/20">awaiting the run</span>
        </div>
      ) : (
        <div className="min-h-0 flex-1 overflow-hidden">
          <div className="px-3 py-2.5">
            <p className="font-mono text-[13px] tracking-extra-tight text-black">
              {shownTitle}
              <InkCaret on={caretOn === "title"} />
            </p>
            <div className="mt-2 space-y-1.5 font-mono text-[11px] leading-4 tracking-extra-tight text-black/70">
              {CLAUSES.map((c, i) => (
                <p key={c} className="tabular-nums">
                  {clauseShown[i]}
                  <InkCaret on={caretOn === `c${i}`} />
                </p>
              ))}
            </div>
            {t >= ACTION_START ? (
              <div className="mt-2.5">
                <MonoLabel className="mb-1 block uppercase">how to see it yourself</MonoLabel>
                <p>
                  <ActionLine shown={shownAction} />
                  <InkCaret on={caretOn === "action"} />
                </p>
              </div>
            ) : null}
          </div>
          <Drawer t={t} start={DRAWER_AT[0]} height={DRAWER_H[0]} title="1 · Workflow · upgrade to pro">
            <div className="flex items-center gap-1">
              {[0, 1, 2, 3].map((i) => (
                <JourneyFrame key={i} i={i} error={i === 3} />
              ))}
            </div>
          </Drawer>
          <Drawer t={t} start={DRAWER_AT[1]} height={DRAWER_H[1]} title="2 · Invariant · rows returned">
            <InvariantRows />
          </Drawer>
          <Drawer t={t} start={DRAWER_AT[2]} height={DRAWER_H[2]} title="3 · Attempted effects">
            <div className="flex gap-1.5">
              <Receipt>Stripe · mock x2</Receipt>
              <Receipt>email · capture x1</Receipt>
              <Receipt className="text-black/45">escaped · 0</Receipt>
            </div>
          </Drawer>
          <Drawer t={t} start={DRAWER_AT[3]} height={DRAWER_H[3]} title="4 · Load">
            <p className="font-mono text-[10px] tabular-nums tracking-extra-tight text-black/65">
              2,140 requests at 18/s · p95 412ms · GET /api/subscriptions 129% over baseline
            </p>
          </Drawer>
          <Drawer t={t} start={DRAWER_AT[4]} height={DRAWER_H[4]} title="5 · Teardown">
            <p className="font-mono text-[10px] tabular-nums tracking-extra-tight text-black/65">
              14 resources removed · 0 left behind
            </p>
          </Drawer>

          {t >= 8.5 ? (
            <div className="mt-1 border-t border-black/10 px-2.5 py-2">
              <MonoLabel className="mb-1.5 block uppercase">verdict</MonoLabel>
              <ul className="space-y-1">
                {(
                  [
                    { label: "a workflow failed", tag: "fail", fired: true },
                    { label: "an invariant did not hold", tag: "fail", fired: true },
                    { label: "load p95_increase 0.25", tag: "listed", fired: false },
                    { label: "outbound refusals", tag: "listed", fired: false },
                  ] as const
                ).map((row) => (
                  <li
                    key={row.label}
                    className={cn(
                      "flex items-center justify-between gap-2 py-0.5 pl-2 font-mono text-[10px] tracking-extra-tight text-black/55",
                      row.fired && "text-red-700",
                    )}
                    style={{ boxShadow: row.fired ? "inset 1px 0 0 #b91c1c" : undefined }}
                  >
                    <span>{row.label}</span>
                    <span
                      className={cn(
                        "tabular-nums uppercase",
                        row.tag === "fail" ? "text-red-700" : "text-black/35",
                      )}
                    >
                      {row.tag}
                    </span>
                  </li>
                ))}
              </ul>
              <p className="mt-1.5 pl-2 font-mono text-[9px] leading-3 tracking-extra-tight text-black/35">
                The verdict comes from the workflows and the invariants. The other two are reported.
              </p>
            </div>
          ) : null}
        </div>
      )}
    </div>
  );
}

export function ReportScene() {
  const ref = useRef<HTMLDivElement>(null);
  const { idle, reduced, story } = useInViewPlay(ref, 0.16);
  const [t, setT] = useState(0);
  const [vis, setVis] = useState(1);

  useEffect(() => {
    if (!idle) return;
    setT(0);
    setVis(1);
  }, [idle]);

  usePausedRaf(idle, (_now, elapsed) => {
    const cycle = (elapsed / 1000) % LOOP;
    if (cycle >= HOLD_END) {
      const d = (cycle - HOLD_END) / DISSOLVE;
      if (d < 0.45) {
        setT(18.92);
        setVis(1 - d / 0.45);
      } else {
        setT(0);
        setVis((d - 0.45) / 0.55);
      }
      return;
    }
    setT(cycle);
    setVis(1);
  });

  const playhead = reduced ? REDUCED_T : story ? t : 0;
  const blocked = playhead >= 5.5;
  const running = playhead < 5.5;

  return (
    <div ref={ref} className="pointer-events-none mt-16 select-none max-xl:mt-12 max-lg:mt-10" aria-hidden>
      <div className="max-xl:overflow-visible" style={{ opacity: reduced ? 1 : vis }}>
        <Panel className="min-h-[680px] min-w-[720px] max-xl:min-h-0 max-xl:min-w-0">
          <div className="flex items-center justify-between gap-4 px-3 py-2.5">
            <div className="flex min-w-0 items-center gap-2.5">
              <QueueChip className="text-black/70">pr/184</QueueChip>
              <span className="truncate font-mono text-[13px] tracking-extra-tight text-black">add access_tier</span>
            </div>
            <div className="flex items-center gap-2">
              <MonoLabel className="tabular-nums">{blocked ? "gate · fail" : running && playhead >= 2 ? "gate · running" : "gate · queued"}</MonoLabel>
              <button
                type="button"
                disabled
                tabIndex={-1}
                className="cursor-not-allowed bg-black/[0.04] px-2 py-1 font-mono text-[10px] tracking-extra-tight text-black/30 ring-1 ring-black/10"
              >
                Merge
              </button>
            </div>
          </div>
          <Hairline />
          <div className="grid min-h-[632px] grid-cols-[minmax(300px,0.92fr)_1.2fr] max-xl:min-h-0 max-xl:grid-cols-1">
            <ChecksColumn t={playhead} blocked={blocked} running={running} />
            <div className="relative flex flex-col border-l border-black/10 max-xl:border-t max-xl:border-l-0">
              <EvidenceColumn t={playhead} />
            </div>
          </div>
        </Panel>
      </div>
    </div>
  );
}
