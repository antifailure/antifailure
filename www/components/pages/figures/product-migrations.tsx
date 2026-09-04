"use client";

import { useState, type ReactNode } from "react";
import { StatusPill } from "@/components/home/visuals/primitives";
import { FloatWindow, SageWell } from "@/components/pages/solutions/well";
import { cn } from "@/lib/cn";

function FigureShell({
  id,
  title,
  rail,
  children,
  toolbar,
  compact = false,
}: {
  id: string;
  title: string;
  rail: string;
  children: ReactNode;
  toolbar?: ReactNode;
  compact?: boolean;
}) {
  const titleId = `${id.toLowerCase()}-title`;
  const captionId = `${id.toLowerCase()}-caption`;

  return (
    <figure aria-labelledby={titleId} aria-describedby={captionId} className="w-full min-w-0">
      <SageWell
        className={cn(
          "w-full self-start !min-h-0 !px-3 !py-4 sm:!px-4 sm:!py-5 md:!px-5 md:!py-6",
          compact && "md:!px-4 md:!py-5",
        )}
      >
        <FloatWindow className="w-full overflow-hidden ring-1 ring-black/[0.05]">
          <div className="flex min-w-0 items-end justify-between gap-3 border-b border-black/[0.08] bg-[#f7f7f5] px-3 pt-3 sm:px-4">
            <div className="min-w-0 rounded-t-[9px] bg-[#CAE6D9] px-3 py-2 text-[11px] font-medium text-[#285D49] sm:text-[12px]">
              <span id={titleId} className="block truncate">
                {title}
              </span>
            </div>
            <div className="mb-2 flex shrink-0 items-baseline gap-2">
              <span className="border-b-2 border-[#33bf00] pb-0.5 font-mono text-[9px] font-medium uppercase tracking-[0.14em] text-black sm:text-[10px]">
                {rail}
              </span>
              <span className="hidden font-mono text-[9px] uppercase tracking-[0.14em] text-black/40 sm:inline">
                FIG. {id}
              </span>
            </div>
          </div>
          {toolbar ? <div className="border-b border-black/[0.08] bg-white px-3 py-2.5 sm:px-4">{toolbar}</div> : null}
          <div className={cn("p-3.5 sm:p-4", compact && "sm:p-3.5")}>{children}</div>
        </FloatWindow>
      </SageWell>
      <figcaption id={captionId} className="sr-only">
        {title}
      </figcaption>
    </figure>
  );
}

function Metric({
  label,
  value,
  tone = "neutral",
}: {
  label: string;
  value: string;
  tone?: "neutral" | "fail" | "pass";
}) {
  return (
    <div className="min-w-0 border-l border-black/[0.10] pl-2.5 first:border-l-0 first:pl-0 sm:pl-3">
      <div className="font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-black/50 sm:text-[9px]">
        {label}
      </div>
      <div
        className={cn(
          "mt-1 truncate font-mono text-[10px] font-medium tabular-nums sm:text-[11px]",
          tone === "neutral" && "text-black/75",
          tone === "fail" && "text-red-700",
          tone === "pass" && "text-[#285D49]",
        )}
        title={value}
      >
        {value}
      </div>
    </div>
  );
}

function BooleanMark({ value }: { value: boolean }) {
  return (
    <span
      className={cn(
        "inline-flex items-center rounded-full px-2 py-1 font-mono text-[9px] font-medium uppercase tracking-[0.08em]",
        value ? "bg-red-50 text-red-700 ring-1 ring-red-700/15" : "bg-[#E4F1EB] text-[#285D49] ring-1 ring-[#33bf00]/25",
      )}
    >
      {value ? "true" : "false"}
    </span>
  );
}

function LockComparison({ safe }: { safe: boolean }) {
  const tone = safe ? "pass" : "fail";
  const held = safe ? "0.4s" : "27.4s";
  const rows = [
    { label: "Strongest lock", bad: "ACCESS EXCLUSIVE", good: "ACCESS EXCLUSIVE" },
    { label: "Lock duration", bad: "27.4 seconds", good: "0.4 seconds" },
    { label: "Blocking observed", bad: "true", good: "false" },
    { label: "Table rewrite", bad: "yes", good: "no" },
    { label: "Plan change", bad: "Index Scan -> Seq Scan", good: "unchanged" },
  ] as const;

  return (
    <div>
      <div className="flex flex-wrap items-start justify-between gap-3">
        <div>
          <div className="text-[13px] font-medium tracking-tight text-black">
            ALTER · subscriptions.plan_id
          </div>
          <p className="mt-1 font-mono text-[10px] text-black/50">
            rehearsal branch · observer connection at 250ms
          </p>
        </div>
        <StatusPill tone={safe ? "PASS" : "FAIL"}>{safe ? "PASS" : "FAIL"}</StatusPill>
      </div>

      <div className="mt-4 overflow-hidden rounded-[12px] border border-black/[0.09] bg-white">
        <div className="grid grid-cols-[minmax(0,1fr)_minmax(92px,0.72fr)] border-b border-black/[0.08] bg-[#f4f4f1] px-3 py-2 font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-black/45 sm:grid-cols-[minmax(0,1fr)_minmax(144px,0.7fr)] sm:px-4 sm:text-[9px]">
          <span>Finding</span>
          <span>{safe ? "Remedy" : "Observed"}</span>
        </div>
        <div className="divide-y divide-black/[0.07]">
          {rows.map((row) => (
            <div key={row.label} className="grid grid-cols-[minmax(0,1fr)_minmax(92px,0.72fr)] items-center gap-2 px-3 py-2.5 sm:grid-cols-[minmax(0,1fr)_minmax(144px,0.7fr)] sm:px-4">
              <span className="min-w-0 font-mono text-[9px] text-black/55 sm:text-[10px]">{row.label}</span>
              <span
                className={cn(
                  "min-w-0 break-words text-right font-mono text-[9px] font-medium sm:text-[10px]",
                  safe ? "text-[#285D49]" : "text-red-700",
                )}
              >
                {safe ? row.good : row.bad}
              </span>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-3 grid grid-cols-3 rounded-[10px] border border-black/[0.08] bg-white px-3 py-2.5">
        <Metric label="Held" value={held} tone={tone} />
        <Metric label="Rewrite" value={safe ? "No" : "Yes"} tone={tone} />
        <Metric label="Blocking" value={safe ? "false" : "true"} tone={tone} />
      </div>
    </div>
  );
}

export function PMG01({ captions }: { captions: readonly [string, string] }) {
  const [mode, setMode] = useState<0 | 1>(0);

  return (
    <div>
      <FigureShell
        id="P-MG-01"
        title="migration rehearsal · lock exposure"
        rail="COMPARE"
        toolbar={
          <div className="flex w-full rounded-[10px] bg-[#f4f4f1] p-1" aria-label="Migration strategy comparison">
            {(["Direct type change", "Expand-and-contract"] as const).map((label, index) => {
              const active = mode === index;
              return (
                <button
                  key={label}
                  type="button"
                  aria-pressed={active}
                  onClick={() => setMode(index as 0 | 1)}
                  className={cn(
                    "min-w-0 flex-1 rounded-[7px] px-2 py-2 text-[10px] font-medium sm:text-[11px]",
                    active ? "bg-white text-black shadow-sm ring-1 ring-black/[0.06]" : "text-black/45 hover:text-black/70",
                  )}
                >
                  <span className="block truncate">{label}</span>
                </button>
              );
            })}
          </div>
        }
      >
        <LockComparison safe={mode === 1} />
      </FigureShell>
      <p className="mt-5 max-w-[640px] text-[15px] leading-6 tracking-extra-tight text-black max-md:mt-4 max-md:text-[14px]">
        {captions[mode]}
      </p>
    </div>
  );
}

export function PMG02() {
  const observations = [
    { label: "Migration", detail: "ALTER COLUMN plan_id TYPE bigint" },
    { label: "Lock", detail: "ACCESS EXCLUSIVE held for 27.4 seconds" },
    { label: "Blocking observed", detail: "true" },
  ] as const;

  return (
    <FigureShell id="P-MG-02" title="postgres · concurrent observation" rail="EVIDENCE" compact>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-[13px] font-medium tracking-tight text-black">subscriptions migration</div>
          <p className="mt-1 font-mono text-[10px] text-black/50">three connections · one measured lock window</p>
        </div>
        <StatusPill tone="FAIL">FAIL</StatusPill>
      </div>

      <div className="mt-4 overflow-hidden rounded-[12px] border border-black/[0.09] bg-white">
        <div className="grid grid-cols-[104px_minmax(0,1fr)] border-b border-black/[0.08] bg-[#f4f4f1] px-3 py-2 font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-black/45 sm:grid-cols-[132px_minmax(0,1fr)] sm:px-4 sm:text-[9px]">
          <span>Observed via</span>
          <span>Result</span>
        </div>
        <div className="divide-y divide-black/[0.07]">
          {observations.map((item) => (
            <div key={item.label} className="grid grid-cols-[104px_minmax(0,1fr)] items-center gap-2 px-3 py-3 sm:grid-cols-[132px_minmax(0,1fr)] sm:px-4">
              <span className="font-mono text-[9px] font-medium text-black/65 sm:text-[10px]">{item.label}</span>
              <span className="min-w-0 break-words font-mono text-[9px] text-black/70 sm:text-[10px]">{item.detail}</span>
            </div>
          ))}
        </div>
      </div>

      <div className="mt-3 rounded-[10px] border border-red-700/20 bg-red-50 px-3 py-3">
        <div className="flex items-center justify-between gap-3">
          <div className="min-w-0">
            <div className="font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-red-700/65">Lock finding</div>
            <div className="mt-1 text-[11px] font-medium tracking-tight text-red-800">second session waited on the relation lock</div>
          </div>
          <BooleanMark value />
        </div>
      </div>
    </FigureShell>
  );
}

const EVIDENCE = [
  { source: "pg_locks", measure: "lock mode + hold", result: "ACCESS EXCLUSIVE · 27.4s" },
  { source: "pg_stat_activity", measure: "contention", result: "another session waiting" },
  { source: "Postgres + EXPLAIN", measure: "rewrite + plan", result: "rewrite · Index → Seq Scan" },
] as const;

export function PMG03({ source }: { source: string }) {
  const lines = source.split("\n");

  return (
    <FigureShell id="P-MG-03" title="af insights · evidence provenance" rail="MEASURED" compact>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-[13px] font-medium tracking-tight text-black">one report, three observed sources</div>
          <p className="mt-1 font-mono text-[10px] text-black/50">no timing inferred from SQL text</p>
        </div>
        <StatusPill tone="FAIL">FAIL</StatusPill>
      </div>

      <div className="mt-4 grid gap-2 sm:grid-cols-3" aria-label="Evidence sources">
        {EVIDENCE.map((item, index) => (
          <div key={item.source} className="min-w-0 border border-black/[0.08] bg-[#fbfbfa] px-2.5 py-2.5">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[9px] font-medium text-black/35">
                {index + 1}
              </span>
              <span className="truncate font-mono text-[9px] font-medium text-black/75">{item.source}</span>
            </div>
            <div className="mt-2 font-mono text-[8px] uppercase tracking-[0.1em] text-black/40">{item.measure}</div>
            <div className="mt-1 text-[9px] leading-3.5 text-black/65">{item.result}</div>
          </div>
        ))}
      </div>

      <div className="mt-3 overflow-hidden rounded-[12px] border border-black/[0.09] bg-white">
        <div className="flex items-center justify-between gap-3 border-b border-black/[0.08] bg-[#f4f4f1] px-3 py-2">
          <span className="font-mono text-[9px] font-medium text-black/65">internal/insights</span>
          <span className="font-mono text-[8px] uppercase tracking-[0.12em] text-red-700">lint finding</span>
        </div>
        <div className="flex min-w-0">
          <div className="select-none border-r border-black/[0.07] bg-[#fafaf8] px-2 py-2.5 text-right font-mono text-[9px] leading-[17px] text-black/25" aria-hidden>
            {lines.map((_, index) => (
              <div key={index}>{index + 1}</div>
            ))}
          </div>
          <pre className="min-w-0 flex-1 whitespace-pre-wrap break-words px-2.5 py-2.5 font-mono text-[9px] leading-[17px] tracking-extra-tight text-black/70 sm:text-[10px]">
            {source}
          </pre>
        </div>
      </div>

      <div className="mt-3 grid gap-2 sm:grid-cols-[0.9fr_1.1fr]">
        <div className="rounded-[10px] border border-red-700/20 bg-red-50 px-3 py-2.5">
          <div className="font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-red-700/65">Migration lint</div>
          <div className="mt-1 text-[11px] font-medium tracking-tight text-red-800">direct type change rewrites subscriptions</div>
        </div>
        <div className="rounded-[10px] border border-[#33bf00]/25 bg-[#E4F1EB]/70 px-3 py-2.5">
          <div className="font-mono text-[8px] font-medium uppercase tracking-[0.12em] text-[#285D49]/70">Remedy</div>
          <div className="mt-1 text-[11px] font-medium tracking-tight text-[#285D49]">expand, backfill in batches, dual-read, contract later</div>
        </div>
      </div>
    </FigureShell>
  );
}

const SAFE_STEPS = [
  { number: "01", title: "Expand", detail: "add nullable plan_id_v2", tag: "0.4s lock" },
  { number: "02", title: "Backfill", detail: "copy values in batches", tag: "no rewrite" },
  { number: "03", title: "Dual-read", detail: "read both schema shapes", tag: "compatible" },
  { number: "04", title: "Contract", detail: "drop old column later", tag: "later deploy" },
] as const;

export function PMG04() {
  return (
    <FigureShell id="P-MG-04" title="subscriptions · compatibility window" rail="SAFE PATH" compact>
      <div className="flex items-start justify-between gap-3">
        <div>
          <div className="text-[13px] font-medium tracking-tight text-black">expand-and-contract sequence</div>
          <p className="mt-1 font-mono text-[10px] text-black/50">schema stays readable across deploys</p>
        </div>
        <StatusPill tone="PASS">PASS</StatusPill>
      </div>

      <ol className="mt-4 grid gap-2 sm:grid-cols-4" aria-label="Expand-and-contract migration stages">
        {SAFE_STEPS.map((step, index) => (
          <li key={step.title} className="grid min-w-0 grid-cols-[34px_minmax(0,1fr)] gap-2 border border-black/[0.08] bg-white p-2.5 sm:block sm:min-h-[118px]">
            <div className="font-mono text-[9px] font-medium text-[#285D49]">
              {step.number}
            </div>
            <div className="min-w-0 sm:mt-3">
              <div className="text-[11px] font-medium tracking-tight text-black">{step.title}</div>
              <div className="mt-1 text-[9px] leading-3.5 text-black/55">{step.detail}</div>
              <div className={cn("mt-1.5 font-mono text-[8px] text-[#285D49] sm:mt-2", index === 0 && "font-medium")}>
                {step.tag}
              </div>
            </div>
          </li>
        ))}
      </ol>

      <div className="mt-3 overflow-hidden rounded-[12px] border border-black/[0.09] bg-[#fbfbfa]">
        <div className="grid grid-cols-[84px_1fr_1fr] border-b border-black/[0.07] bg-[#f4f4f1] px-2.5 py-2 font-mono text-[8px] font-medium uppercase tracking-[0.1em] text-black/45 sm:grid-cols-[112px_1fr_1fr] sm:px-3 sm:text-[9px]">
          <span>Window</span>
          <span>Old binary</span>
          <span>New binary</span>
        </div>
        <div className="grid grid-cols-[84px_1fr_1fr] items-center border-b border-black/[0.07] px-2.5 py-2.5 font-mono text-[8px] sm:grid-cols-[112px_1fr_1fr] sm:px-3 sm:text-[9px]">
          <span className="text-black/55">Both columns</span>
          <span className="text-black/70">reads plan_id</span>
          <span className="text-[#285D49]">reads both</span>
        </div>
        <div className="grid grid-cols-[84px_1fr_1fr] items-center px-2.5 py-2.5 font-mono text-[8px] sm:grid-cols-[112px_1fr_1fr] sm:px-3 sm:text-[9px]">
          <span className="text-black/55">After contract</span>
          <span className="text-black/35">retired</span>
          <span className="text-[#285D49]">reads plan_id_v2</span>
        </div>
      </div>

      <div className="mt-3 grid grid-cols-3 rounded-[10px] border border-[#33bf00]/25 bg-[#E4F1EB]/70 px-3 py-2.5">
        <Metric label="Strongest hold" value="0.4s" tone="pass" />
        <Metric label="Blocking" value="false" tone="pass" />
        <Metric label="Rewrite" value="No" tone="pass" />
      </div>
    </FigureShell>
  );
}
