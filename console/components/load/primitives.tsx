"use client";

import type { ReactNode } from "react";
import { Badge, type Tone } from "@/components/ui";
import {
  SOURCE_FACTS,
  STATE_FACTS,
  VERDICT_FACTS,
  ms,
  type Latency,
  type RunState,
  type SourceKind,
  type Verdict,
  percentiles,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * Provenance
 * ---------------------------------------------------------------------- */

/**
 * A mark for where a load source's traffic came from.
 *
 * Two glyphs, two words, and deliberately no colour. Provenance is not a
 * verdict: measured traffic is not better or worse than authored traffic, it
 * is a different thing to trust. Giving each a hue would put more colours on a
 * page whose green and red already mean pass and fail, and a reader would
 * spend the first minute working out whether the blue one was good news.
 *
 * The glyphs are not interchangeable decoration. A stepped profile for a mix
 * that was measured, a pinned document for one that was written down.
 */
const MARKS: Record<SourceKind, string> = {
  observed: "M2 11.5h2.6V7.4h2.6V4h2.6v7.5H13",
  deterministic: "M4 2.8h8v10.4l-4-2.3-4 2.3zM6.4 6h3.2",
};

export function Provenance({ kind, className = "" }: { kind: SourceKind; className?: string }) {
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
        {SOURCE_FACTS[kind].noun}
      </span>
    </span>
  );
}

/* -------------------------------------------------------------------------
 * Verdict and state
 * ---------------------------------------------------------------------- */

/**
 * The colour a verdict is drawn in.
 *
 * Written out rather than reaching for `toneFor`, which reads "ready", "ok"
 * and "active" as a pass by name and would drop `blocked` and `unverified`
 * through to neutral. A blocked run rendered as unremarkable is the whole
 * failure this product has already had once.
 */
export function verdictTone(v: Verdict): Tone {
  if (v === "pass") return "pass";
  if (v === "fail") return "fail";
  return "warn";
}

/**
 * What a run decided, or that it has not decided anything.
 *
 * A run with no verdict does not get a blank cell. "No verdict" is a real
 * answer and it is the correct one for a run that is still going or was
 * stopped, where a blank reads as a value that failed to load.
 */
export function VerdictBadge({ verdict }: { verdict: Verdict | null }) {
  if (verdict === null) return <Badge tone="neutral">No verdict</Badge>;
  return <Badge tone={verdictTone(verdict)}>{VERDICT_FACTS[verdict].label}</Badge>;
}

/**
 * Where a run is.
 *
 * Static, with nothing that moves. A load run that is genuinely in flight is
 * the most tempting place in this console for a throbbing dot, and it is still
 * wrong: the word "Running" says the same thing, holds still, and does not
 * compete with the numbers underneath it for a reader's eye.
 *
 * Amber for everything unsettled and neutral for cancelled, which is the one
 * outcome that is over and is not a judgement. That split is deliberate: with
 * both neutral, a run in flight and a run somebody stopped rendered
 * identically, which is the defect `toneFor`'s own comment records being fixed
 * once already, when an environment still building looked like one torn down.
 */
export function StateBadge({ state }: { state: RunState }) {
  const tone: Tone = state === "cancelled" ? "neutral" : state === "finished" ? "neutral" : "warn";
  return <Badge tone={tone}>{STATE_FACTS[state].label}</Badge>;
}

/** The sentence under a verdict, for the two that are not judgements. A pass
 *  or a fail needs no explanation, and putting one under every verdict turns
 *  the note into furniture by the time it matters. */
export function VerdictNote({ verdict }: { verdict: Verdict | null }) {
  if (verdict === null) return null;
  const fact = VERDICT_FACTS[verdict];
  if (fact.conclusive) return null;
  return <p className="mt-2 max-w-[64ch] text-[12.5px] leading-6 text-muted">{fact.meaning}</p>;
}

/* -------------------------------------------------------------------------
 * Numbers
 * ---------------------------------------------------------------------- */

/**
 * One measurement.
 *
 * Takes an already-formatted string, and "--" is a real answer meaning the run
 * did not record it. That is why it is a string and not a number: there is no
 * value this component could substitute for a missing measurement that would
 * not be an invention.
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

/* -------------------------------------------------------------------------
 * The latency ladder
 * ---------------------------------------------------------------------- */

/**
 * A latency distribution.
 *
 * One bar per percentile, one colour, scaled to the slowest shown. One series,
 * so there is no legend to draw and no hue to assign: these are the same
 * measurement at different points in one distribution, and colouring them by
 * value would encode the bar's own length a second time.
 *
 * Every rung is labelled with its number, which a chart with more marks could
 * not afford. There are at most five and the number is what a person came for;
 * the bar is there so the shape of the tail is visible at a glance, which a
 * column of figures does not give you. The tail is the point: an average hides
 * it and the tail is what a user notices.
 *
 * A percentile the run did not record is absent, not a rung of length zero.
 */
export function LatencyLadder({ latency }: { latency: Latency }) {
  const rungs = percentiles(latency);
  if (rungs.length === 0) return null;
  const max = Math.max(...rungs.map((r) => r.ms));

  return (
    <div className="px-4 py-4">
      <table className="w-full border-collapse text-left">
        <caption className="sr-only">
          Response latency by percentile, in milliseconds. Each bar is that
          percentile as a share of the slowest one shown.
        </caption>
        <tbody>
          {rungs.map((r) => {
            // A floor of 2%, so the fastest percentile is still a visible mark.
            // Below that a bar stops being a length and starts reading as an
            // absence, which means something else entirely on this page.
            const share = max > 0 ? Math.max(0.02, r.ms / max) : 0;
            return (
              <tr key={r.label}>
                <th
                  scope="row"
                  className="w-[6ch] py-1.5 pr-3 text-left text-[12px] font-medium text-muted"
                >
                  {r.label}
                </th>
                <td className="w-full py-1.5">
                  {/* aria-hidden: the row heading and the value already say
                      everything the bar says, and announcing a decorative
                      length would add noise to every row. */}
                  <span className="block h-2 w-full" aria-hidden>
                    <span
                      className="block h-2 rounded-sm bg-[rgba(16,16,16,0.68)]"
                      style={{ width: `${(share * 100).toFixed(2)}%` }}
                    />
                  </span>
                </td>
                <td className="tnum whitespace-nowrap py-1.5 pl-3 text-right text-[12.5px] text-ink">
                  {ms(r.ms)}
                </td>
              </tr>
            );
          })}
        </tbody>
      </table>
    </div>
  );
}

/* -------------------------------------------------------------------------
 * Routes in a table
 * ---------------------------------------------------------------------- */

/**
 * A route path in a table cell, bounded so a long one cannot cross the column.
 *
 * The bound has to be on a block INSIDE the cell, not on the cell. Under the
 * automatic table layout these tables use, `max-width` on a `<td>` is advisory
 * and a long unbroken path ignores it: a 78 character route ran straight
 * through the neighbouring column and printed on top of the request count.
 * `break-all` gives the browser somewhere to break a path with no spaces.
 */
export function RouteCell({ route }: { route: string }) {
  // The engine renders a route as "METHOD /path". Splitting on the first space
  // lets the method sit in a quieter colour without the caller having to know
  // the format.
  const space = route.indexOf(" ");
  const method = space > 0 ? route.slice(0, space) : null;
  const path = space > 0 ? route.slice(space + 1) : route;
  return (
    <span className="block break-all sm:max-w-[44ch]">
      {method ? <span className="text-dim">{method} </span> : null}
      {path}
    </span>
  );
}
