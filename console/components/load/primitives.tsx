"use client";

import type { ReactNode } from "react";
import { Badge, type Tone } from "@/components/ui";
import {
  KIND_FACTS,
  STATE_FACTS,
  VERDICT_FACTS,
  ms,
  percentiles,
  type Kind,
  type Latency,
  type RunState,
  type Verdict,
} from "@/lib/load";

/* -------------------------------------------------------------------------
 * Kind
 * ---------------------------------------------------------------------- */

/**
 * A mark for what a workload is.
 *
 * Four glyphs, four words, and deliberately no colour. A kind is not a
 * verdict: an exploration is not better or worse than a scenario, it is a
 * different thing to trust. Giving each a hue would put four more colours on a
 * page whose green and red already mean pass and fail, and a reader would
 * spend the first minute working out whether the blue one was good news.
 *
 * The glyphs are not interchangeable decoration. A stepped profile for traffic
 * that was measured, a snaking path for a journey somebody wrote, a browser
 * frame for a workflow driven in one, and a compass for an agent choosing its
 * own way.
 */
const MARKS: Record<Kind, string> = {
  observed_load: "M2.6 11.6h2.4V8.2h2.4V4.6h2.4v7h3.4",
  http_scenario: "M2.8 12.2c2.4 0 2.4-4.2 5.2-4.2s2.8-4 5.2-4",
  browser_workflow: "M2.8 3.8h10.4v8.4H2.8zM2.8 6.4h10.4M4.6 5.1h.01",
  exploration: "M8 2.6a5.4 5.4 0 1 0 0 10.8A5.4 5.4 0 0 0 8 2.6zM10.2 5.8 8.9 8.9 5.8 10.2 7.1 7.1z",
};

export function KindMark({ kind, className = "" }: { kind: Kind; className?: string }) {
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
        {KIND_FACTS[kind].noun}
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
 * and "active" as a pass by name and would drop three of these five through to
 * neutral. A blocked run rendered as unremarkable is the whole failure this
 * product has already had once.
 *
 * Three of the six are amber, and two of those three are amber for opposite
 * reasons. `flaky` and `warn` mean we looked and found something; `blocked` and
 * `unverified` mean we did not look. Colour cannot carry that difference and is
 * not asked to: the sentence under the badge does, through VerdictNote, which
 * renders for every verdict except the two that need no explanation.
 */
export function verdictTone(v: Verdict): Tone {
  if (v === "pass") return "pass";
  if (v === "fail") return "fail";
  return "warn";
}

/**
 * What a run found, or that it has not found anything.
 *
 * A run with no verdict does not get a blank cell. "No verdict" is a real
 * answer and it is the correct one for a run still going or one nothing ever
 * reported on, where a blank reads as a value that failed to load.
 */
export function VerdictBadge({ verdict }: { verdict: Verdict | null }) {
  if (verdict === null) return <Badge tone="neutral">No verdict</Badge>;
  return <Badge tone={verdictTone(verdict)}>{VERDICT_FACTS[verdict].label}</Badge>;
}

/**
 * Where a run is.
 *
 * Static, with nothing that moves. A run that is genuinely in flight is the
 * most tempting place in this console for a throbbing dot, and it is still
 * wrong: the word "Running" says the same thing, holds still, and does not
 * compete with the numbers underneath it for a reader's eye.
 *
 * Three tones for eight values, and the split is the argument this whole
 * screen rests on. Amber for the three that are unsettled. Neutral for
 * `succeeded` and `cancelled`, because neither is a judgement: succeeding is
 * about the work happening and the verdict beside it says what it found. Red
 * for the two an engine reported as a failure of the work.
 *
 * `abandoned` is amber and NOT red, deliberately. Nothing failed; the control
 * plane never heard. Drawing it as a failure would tell a reader the change is
 * broken when what is broken is the reporting.
 */
export function StateBadge({ state }: { state: RunState }) {
  const tone: Tone =
    state === "failed" || state === "timed_out"
      ? "fail"
      : state === "succeeded" || state === "cancelled"
        ? "neutral"
        : "warn";
  return <Badge tone={tone}>{STATE_FACTS[state].label}</Badge>;
}

/** The sentence under a verdict, for the three that need one. A pass or a fail
 *  needs no explanation, and putting one under every verdict turns the note
 *  into furniture by the time it matters. */
export function VerdictNote({ verdict }: { verdict: Verdict | null }) {
  if (verdict === null) return null;
  const fact = VERDICT_FACTS[verdict];
  if (verdict === "pass" || verdict === "fail") return null;
  // The same measure as the state sentence it sits under, or the two wrap at
  // different widths and read as two different columns of text.
  return (
    <p className="mt-2 max-w-[70ch] text-pretty text-[12.5px] leading-6 text-muted">
      {fact.meaning}
    </p>
  );
}

/* -------------------------------------------------------------------------
 * Numbers
 * ---------------------------------------------------------------------- */

/**
 * One measurement.
 *
 * Takes an already formatted string, and "--" is a real answer meaning the run
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
  tone?: "fail" | "warn" | "pass";
}) {
  const colour =
    tone === "fail"
      ? "text-fail"
      : tone === "warn"
        ? "text-warn"
        : tone === "pass"
          ? "text-pass"
          : "text-ink";
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</dt>
      <dd className={`tnum mt-1 text-[19px] font-semibold tracking-tighter ${colour}`}>{value}</dd>
      {note ? <p className="mt-0.5 text-[11.5px] leading-5 text-dim">{note}</p> : null}
    </div>
  );
}

/** A labelled fact in a definition grid. Text rather than a number, so a
 *  sentence and a git ref sit in the same shape a stat does not fit. */
export function Fact({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="min-w-0">
      <dt className="text-[11px] font-medium uppercase tracking-[0.08em] text-dim">{label}</dt>
      <dd className="mt-1 break-words text-[13px] leading-6 text-ink">{children}</dd>
    </div>
  );
}

export function Facts({ children, columns = 2 }: { children: ReactNode; columns?: 2 | 3 }) {
  return (
    <dl
      className={`grid gap-x-8 gap-y-4 px-4 py-4 ${columns === 3 ? "sm:grid-cols-3" : "sm:grid-cols-2"}`}
    >
      {children}
    </dl>
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
