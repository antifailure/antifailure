"use client";

import { useState, type ReactNode } from "react";
import { LogoMark } from "@/components/icons";

type View = "original" | "revision";

function CodeLine({ number, children, marked = false }: { number: number; children: ReactNode; marked?: boolean }) {
  return (
    <div className={`flex min-w-0 border-l-2 py-1 pr-4 sm:min-w-max sm:pr-6 ${marked ? "border-alert-on-dark bg-alert-on-dark/10" : "border-transparent"}`}>
      <span className="w-8 shrink-0 select-none text-right text-sage-2/65 sm:w-12 sm:pl-4">{number}</span>
      <span className="min-w-0 break-words pl-3 text-sage sm:whitespace-nowrap sm:pl-5">{children}</span>
    </div>
  );
}

export function MigrationEvidence() {
  const [view, setView] = useState<View>("original");
  const original = view === "original";

  return (
    <figure className="overflow-hidden rounded-xl border border-forest-line/25 bg-white" aria-label="Interactive migration example showing an original migration, its Antifailure finding, and an agent's proposed revision.">
      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 border-b border-stroke px-5 py-3.5 sm:px-6">
        <div className="flex items-center gap-3">
          <LogoMark className="size-4 shrink-0" />
          <span className="text-sm font-medium text-gray-new-10">Antifailure</span>
          <span aria-hidden className="text-gray-new-80">/</span>
          <span className="text-sm text-gray-new-40">Migration rehearsal</span>
        </div>
        <span className="text-sm text-gray-new-40">Recorded orders demo</span>
      </div>

      <div className="flex overflow-x-auto border-b border-stroke px-2 sm:px-4" role="group" aria-label="Explore the migration example">
        <button
          type="button"
          aria-label="Original migration"
          aria-pressed={original}
          onClick={() => setView("original")}
          className={`min-h-12 shrink-0 border-b-2 px-4 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-[-4px] focus-visible:outline-forest ${original ? "border-forest text-gray-new-10" : "border-transparent text-gray-new-40 hover:text-gray-new-10"}`}
        >
          <span className="sm:hidden">Original</span><span className="max-sm:hidden">Original migration</span>
        </button>
        <button
          type="button"
          aria-label="Agent's revised plan"
          aria-pressed={!original}
          onClick={() => setView("revision")}
          className={`min-h-12 shrink-0 border-b-2 px-4 text-sm font-medium transition-colors focus-visible:outline-2 focus-visible:outline-offset-[-4px] focus-visible:outline-forest ${!original ? "border-forest text-gray-new-10" : "border-transparent text-gray-new-40 hover:text-gray-new-10"}`}
        >
          <span className="sm:hidden">Revised plan</span><span className="max-sm:hidden">Agent&apos;s revised plan</span>
        </button>
      </div>

      <div className="grid lg:grid-cols-[minmax(0,0.96fr)_minmax(0,1.04fr)]">
        <div className="flex min-w-0 flex-col bg-gray-new-10 text-sage">
          <div className="flex items-center justify-between gap-4 border-b border-white/15 px-5 py-3 text-sm sm:px-6">
            <span className="truncate">{original ? "023_widen_total_cents.sql" : "agent-plan.md"}</span>
            <span className="shrink-0 text-sage-2/75">{original ? "SQL" : "Agent revision"}</span>
          </div>
          <div className="min-h-[220px] overflow-x-auto py-6 font-mono text-[15px] leading-7 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-green-52" role="region" tabIndex={0} aria-label={original ? "Original migration code" : "Agent's proposed migration plan"}>
            {original ? (
              <>
                <CodeLine number={1}><span className="text-green-52">ALTER TABLE</span> orders</CodeLine>
                <CodeLine number={2}>  <span className="text-green-52">ALTER COLUMN</span> total_cents</CodeLine>
                <CodeLine number={3} marked>  <span className="text-alert-on-dark">TYPE bigint;</span></CodeLine>
                <CodeLine number={4}> </CodeLine>
              </>
            ) : (
              <>
                <CodeLine number={1}><span className="text-green-52">add:</span> new bigint column</CodeLine>
                <CodeLine number={2}><span className="text-green-52">backfill:</span> small batches</CodeLine>
                <CodeLine number={3}><span className="text-green-52">cutover:</span> read and write new column</CodeLine>
                <CodeLine number={4}><span className="text-green-52">cleanup:</span> remove old column later</CodeLine>
              </>
            )}
          </div>
          <div className="mt-auto border-t border-white/15 px-5 py-5 text-sm sm:px-6">
            <p className="font-medium text-white">{original ? "While this lock is held" : "Revision status"}</p>
            {original ? (
              <div className="mt-3 space-y-2 font-mono text-sage-2">
                <div className="flex justify-between gap-4"><span>read orders</span><span className="text-alert-on-dark">must wait</span></div>
                <div className="flex justify-between gap-4"><span>write orders</span><span className="text-alert-on-dark">must wait</span></div>
              </div>
            ) : (
              <div className="mt-3 space-y-2 text-sage-2">
                <p>The agent has proposed a safer sequence.</p>
                <p className="text-alert-on-dark">No new measurement yet. Rehearse before merge.</p>
              </div>
            )}
          </div>
        </div>

        <div className="flex min-w-0 flex-col justify-between px-5 py-6 sm:px-7 sm:py-7">
          {original ? (
            <div aria-live="polite">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-gray-new-40">Antifailure finding</p>
                <span className="text-sm font-medium text-ochre-ink">Table rewrite</span>
              </div>
              <h3 className="mt-5 max-w-[21ch] text-[clamp(1.65rem,2.6vw,2.5rem)] leading-[1.12] tracking-[-0.04em] text-gray-new-10">
                This change makes reads and writes wait.
              </h3>
              <p className="mt-4 max-w-[43ch] text-base leading-6 text-gray-new-40">
                The type change rewrites the orders table under an ACCESS EXCLUSIVE lock. Reads and writes to orders wait until PostgreSQL releases it.
              </p>
              <dl className="mt-6 divide-y divide-stroke border-y border-stroke text-sm">
                <div className="flex justify-between gap-4 py-3"><dt className="text-gray-new-40">Affected table</dt><dd className="font-medium text-gray-new-10">orders</dd></div>
                <div className="flex justify-between gap-4 py-3"><dt className="text-gray-new-40">Lock type</dt><dd className="font-medium text-gray-new-10">ACCESS EXCLUSIVE</dd></div>
              </dl>
              <details className="group mt-3 border-b border-stroke text-sm">
                <summary className="flex min-h-11 cursor-pointer list-none items-center justify-between gap-4 text-gray-new-20 focus-visible:outline-2 focus-visible:outline-offset-[-2px] focus-visible:outline-forest [&::-webkit-details-marker]:hidden">
                  Inspect measured evidence
                  <svg aria-hidden viewBox="0 0 16 16" className="size-4 shrink-0 text-forest" fill="none">
                    <path d="M2 8h12" stroke="currentColor" strokeWidth="1.5" />
                    <path d="M8 2v12" stroke="currentColor" strokeWidth="1.5" className="group-open:hidden" />
                  </svg>
                </summary>
                <dl className="space-y-2 text-gray-new-40">
                  <div className="flex justify-between gap-3"><dt>orders</dt><dd className="shrink-0 tabular-nums">≥10.5s</dd></div>
                  <div className="flex justify-between gap-3"><dt>orders_pkey</dt><dd className="shrink-0 tabular-nums">≥5.0s</dd></div>
                  <div className="flex justify-between gap-3"><dt className="min-w-0 break-all">orders_customer_id_idx</dt><dd className="shrink-0 tabular-nums">≥2.2s</dd></div>
                </dl>
                <p className="pb-4 pt-2 leading-5 text-gray-new-40">Sampled lower bounds on the demo database.</p>
              </details>
            </div>
          ) : (
            <div aria-live="polite">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <p className="text-sm font-medium text-gray-new-40">Agent&apos;s next step</p>
                <span className="text-sm font-medium text-ochre-ink">Not yet measured</span>
              </div>
              <h3 className="mt-5 max-w-[21ch] text-[clamp(1.65rem,2.6vw,2.5rem)] leading-[1.12] tracking-[-0.04em] text-gray-new-10">
                Split the change into stages, then test it again.
              </h3>
              <p className="mt-4 max-w-[43ch] text-base leading-6 text-gray-new-40">
                Add a new column, backfill in batches, move the application to it, and remove the old column later. The revision still needs its own rehearsal.
              </p>
              <dl className="mt-6 divide-y divide-stroke border-y border-stroke text-sm">
                <div className="flex justify-between gap-4 py-3"><dt className="text-gray-new-40">Current result</dt><dd className="font-medium text-gray-new-10">proposal only</dd></div>
                <div className="flex justify-between gap-4 py-3"><dt className="text-gray-new-40">Next action</dt><dd className="font-medium text-gray-new-10">rehearse revision</dd></div>
              </dl>
            </div>
          )}
          <div className="mt-7 border-t border-stroke pt-4">
            <p className="text-sm font-medium text-forest">{original ? "What the agent receives" : "Why the loop matters"}</p>
            <p className="mt-1 text-sm leading-6 text-gray-new-40">
              {original
                ? "The MCP result carries the finding, measured lock, and evidence. Your agent has something concrete to fix before merge."
                : "A proposed fix is not a pass. Antifailure measures the revised migration on the twin before your agent can call it safe."}
            </p>
          </div>
        </div>
      </div>

      <div className="flex flex-wrap items-center justify-between gap-x-6 gap-y-1 border-t border-forest-line/20 bg-sage px-5 py-4 text-sm sm:px-6">
        <span className="font-medium text-forest">Returned to your coding agent through MCP</span>
        <span className="text-gray-new-40">rehearse_migration_safety → get_rehearsal_run</span>
      </div>
      <figcaption className="px-5 py-3 text-sm leading-6 text-gray-new-40 sm:px-6">
        Original finding from a recorded demo rehearsal. The lock duration is a sampled lower bound; the revised plan is illustrative and has not been measured here.
      </figcaption>
    </figure>
  );
}
