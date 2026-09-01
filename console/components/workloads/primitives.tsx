"use client";

import type { ReactNode } from "react";
import { Badge, type Tone } from "@/components/ui";
import {
  KIND_FACTS,
  STATUS_FACTS,
  count,
  ms,
  percent,
  type Kind,
  type Percentile,
  type RunStatus,
  type Throughput,
} from "@/lib/workloads";

/* -------------------------------------------------------------------------
 * Provenance
 * ---------------------------------------------------------------------- */

/**
 * A mark for where a workload's traffic came from.
 *
 * Three glyphs and three words, and deliberately no colour. Kind is not a
 * verdict: an observed workload is not better or worse than an authored one,
 * it is a different thing to trust. Giving each kind a hue would put three
 * more colours on a page whose greens and reds already mean pass and fail, and
 * a reader would spend the first minute working out whether the blue one was
 * good news. The glyph carries the identity and the word says it outright.
 *
 * The glyphs are not interchangeable decoration either. Steps for a measured
 * shape, a pin for something authored and committed, a fork for a path an
 * agent found by choosing.
 */
const MARKS: Record<Kind, string> = {
  // A stepped profile: the weighted mix, as measured.
  observed: "M2 11.5h2.6V7.4h2.6V4h2.6v7.5H13",
  // A pinned document: written down, committed, at a version.
  deterministic: "M4 2.8h8v10.4l-4-2.3-4 2.3zM6.4 6h3.2",
  // A path that branched: the agent chose, and the choice is the finding.
  exploratory: "M3 13V6.4a2 2 0 0 1 2-2h6M8.6 2 11.4 4.4 8.6 6.8M11 13V9.6",
};

export function Provenance({ kind, className = "" }: { kind: Kind; className?: string }) {
  const fact = KIND_FACTS[kind];
  return (
    <span className={`inline-flex items-center gap-1.5 whitespace-nowrap ${className}`}>
      <svg viewBox="0 0 16 16" className="h-3.5 w-3.5 shrink-0 text-dim" fill="none" aria-hidden>
        <path
          d={MARKS[kind]}
          stroke="currentColor"
          strokeWidth="1.3"
          strokeLinecap="round"
          strokeLinejoin="round"
        />
      </svg>
      <span className="text-[11px] font-medium uppercase tracking-[0.06em] text-muted">
        {fact.noun}
      </span>
    </span>
  );
}

/* -------------------------------------------------------------------------
 * Status
 * ---------------------------------------------------------------------- */

/**
 * The tone a run's status is drawn in.
 *
 * `passed` is the only status that is allowed to be green, and that is the
 * whole point of writing this by hand instead of reaching for `toneFor`. That
 * helper reads "ready", "ok" and "active" as a pass by name, and three of the
 * statuses here would fall through it to neutral, which is how a blocked run
 * ends up looking like an unremarkable one. Blocked, unverified and errored
 * are warnings: something is unresolved and a person has to look.
 */
export function statusTone(status: RunStatus): Tone {
  if (status === "passed") return "pass";
  if (status === "failed") return "fail";
  if (status === "blocked" || status === "unverified" || status === "errored") return "warn";
  return "neutral";
}

/**
 * A run's status, as a word.
 *
 * Static, with no indicator that moves. A workload that is genuinely running
 * right now is the most tempting place in this console to put a throbbing dot,
 * and it is still wrong: the word "Running" says the same thing, holds still,
 * and does not compete with the numbers underneath it for a reader's eye.
 */
export function RunStatusBadge({ status }: { status: RunStatus }) {
  return <Badge tone={statusTone(status)}>{STATUS_FACTS[status].label}</Badge>;
}

/**
 * The sentence under a status, for the outcomes that are not a verdict.
 *
 * Rendered only when the status is inconclusive. A passed or failed run needs
 * no explanation, and adding one to every state would turn the note into
 * furniture nobody reads by the time it matters.
 */
export function StatusNote({ status }: { status: RunStatus }) {
  const fact = STATUS_FACTS[status];
  if (fact.conclusive) return null;
  return (
    <p className="mt-2 max-w-[62ch] text-[12.5px] leading-6 text-muted">{fact.meaning}</p>
  );
}

/* -------------------------------------------------------------------------
 * Numbers
 * ---------------------------------------------------------------------- */

/**
 * One measurement.
 *
 * `value` is a string the caller has already formatted, and "--" is a real
 * answer meaning the run did not record it. That is why this takes a string
 * rather than a number: there is no value this component could substitute for
 * a missing measurement that would not be an invention.
 */
export function Stat({
  label,
  value,
  note,
  tone,
}: {
  label: string;
  value: string;
  note?: ReactNode;
  tone?: "fail" | "warn";
}) {
  const colour = tone === "fail" ? "text-fail" : tone === "warn" ? "text-warn" : "text-ink";
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</dt>
      <dd className={`tnum mt-1 text-[19px] font-semibold tracking-tighter ${colour}`}>{value}</dd>
      {note ? <p className="mt-0.5 text-[11.5px] leading-5 text-dim">{note}</p> : null}
    </div>
  );
}

/** The run's headline numbers. Anything the run did not record reads "--", and
 *  the error rate is only coloured when there were errors to colour. */
export function ThroughputStats({ t }: { t: Throughput }) {
  const errored = t.errorRate !== null && t.errorRate > 0;
  return (
    <dl className="grid grid-cols-2 gap-x-6 gap-y-5 px-4 py-4 sm:grid-cols-4">
      <Stat label="Requests" value={count(t.requests)} />
      <Stat
        label="Throughput"
        value={t.rps === null ? "--" : `${t.rps < 10 ? t.rps.toFixed(1) : Math.round(t.rps)}`}
        note="requests per second"
      />
      <Stat label="Errors" value={count(t.errors)} tone={errored ? "fail" : undefined} />
      <Stat
        label="Error rate"
        value={percent(t.errorRate)}
        tone={errored ? "fail" : undefined}
      />
    </dl>
  );
}

/* -------------------------------------------------------------------------
 * The latency ladder
 * ---------------------------------------------------------------------- */

/**
 * Latency percentiles.
 *
 * A bar per percentile, one colour, scaled to the slowest of them. One series,
 * so there is no legend to draw and no hue to assign: the rungs are the same
 * measurement at different points in one distribution, and colouring them by
 * value would encode the bar's own length a second time.
 *
 * Every rung is labelled with its number, which a chart with more marks could
 * not afford. There are at most six here and the number is what a person came
 * for; the bar is there so the shape of the tail is visible at a glance,
 * which a column of figures does not give you.
 *
 * Only the percentiles the run reported are passed in. An unrecorded p99 is
 * absent from the ladder rather than drawn as a rung of length zero, because a
 * zero-length bar beside "p99" reads as a p99 of nothing.
 */
export function LatencyLadder({ percentiles }: { percentiles: Percentile[] }) {
  const measured = percentiles.filter((p) => p.ms !== null);
  if (measured.length === 0) return null;
  const max = Math.max(...measured.map((p) => p.ms as number));

  return (
    <div className="px-4 py-4">
      <table className="w-full border-collapse text-left">
        <caption className="sr-only">
          Response latency by percentile, in milliseconds. The bar is each
          percentile as a share of the slowest one shown.
        </caption>
        <tbody>
          {measured.map((p) => {
            const value = p.ms as number;
            // A floor of 2%, so the fastest percentile is still a visible mark
            // rather than nothing. Below that the bar stops being a length and
            // starts being an absence, which is a different thing on this page.
            const share = max > 0 ? Math.max(0.02, value / max) : 0;
            return (
              <tr key={p.label}>
                <th
                  scope="row"
                  className="w-[6ch] py-1.5 pr-3 text-left text-[12px] font-medium text-muted"
                >
                  {p.label}
                </th>
                <td className="w-full py-1.5">
                  {/* aria-hidden: the row's heading and its value already say
                      everything the bar says, and announcing a decorative
                      length adds noise to every row. */}
                  <span className="block h-2 w-full" aria-hidden>
                    <span
                      className="block h-2 rounded-sm bg-[rgba(16,16,16,0.68)]"
                      style={{ width: `${share * 100}%` }}
                    />
                  </span>
                </td>
                <td className="tnum whitespace-nowrap py-1.5 pl-3 text-right text-[12.5px] text-ink">
                  {ms(value)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}
