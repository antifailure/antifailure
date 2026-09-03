/**
 * What the Product lane's rows MEAN, with no import that reaches the network.
 *
 * Split from admin-product.ts for the reason loadshapes.ts was split from
 * load.ts, and it is the same reason: the hooks next door pull in React and the
 * fetch wrapper and cannot run outside a browser, so anything living beside
 * them is untestable by construction. The console's unit tests are
 * `node --test lib/*.test.ts`, which means a file that imports React is a file
 * whose logic nobody checks. Everything here is a pure function over plain
 * values, and admin-product.test.ts exercises all of it.
 *
 * The one import is loadshapes.ts, which holds none of its own and is where the
 * console's number formatting already lives. A second formatter for a duration
 * or a percentile would be two things that drift while both look authoritative,
 * and that file says so at the bottom of itself about `bytes`.
 */

import { duration, percent, rate } from "./loadshapes.ts";

/** What a reader should do about a run, in one word. Separate from the run's
 *  own state, because a job that finished is not a job that passed. */
export type RunStanding = "running" | "passed" | "failed" | "cancelled" | "unknown";

/** The three families of run this product has. They are different objects with
 *  different columns, so a list filters between them rather than merging them
 *  into a table whose column set is true of none of the three. */
export type RunKind = "agent" | "load" | "check";

export type ChipTone = "pass" | "fail" | "warn" | "neutral";

/**
 * The tone a standing gets, in one place.
 *
 * `unknown` is deliberately neutral rather than warn. A run that reported
 * nothing is not a warning about the run, it is an absence of information, and
 * colouring it amber puts it in the same visual class as a run that is still
 * going. The word carries the meaning; the colour agrees with it and does not
 * add to it.
 */
export function toneForStanding(standing: RunStanding): ChipTone {
  if (standing === "passed") return "pass";
  if (standing === "failed") return "fail";
  if (standing === "running") return "warn";
  return "neutral";
}

/**
 * How long is left before a twin's lifetime runs out, or how long it has been
 * over.
 *
 * A PHRASE RATHER THAN A SIGNED NUMBER. "-3d" beside an environment is the kind
 * of value a reader interprets backwards during an incident, and the direction
 * is the entire point of the column: one means it is about to go and the other
 * means somebody is paying for it right now. This is the same defect `ago` in
 * format.ts was written to stop making, in a different column.
 *
 * WHY NEITHER EXISTING HELPER IS USED HERE, since a third duration formatter
 * needs an argument. `duration` in loadshapes.ts is for a measured run time,
 * where sub-second precision is the interesting part, and its largest unit is
 * the minute: a twin five days overdue would read "7200m over". `ago` in
 * format.ts has exactly the right scale and two things this column cannot use,
 * namely its own direction words baked into the string and a read of
 * `Date.now()` that no test can pin. So the scale below is `ago`'s, on purpose
 * and to the unit, and the words are this column's.
 */
export function expiryPhrase(expiresAt: string | null, now: Date = new Date()): string {
  if (expiresAt === null) return "No expiry set";
  const ms = new Date(expiresAt).getTime() - now.getTime();
  if (!Number.isFinite(ms)) return "No expiry set";
  // The sign is read once, before any rounding can lose it, which is the fix
  // `ago` records in its own header.
  return ms >= 0 ? `${coarse(Math.abs(ms))} left` : `${coarse(Math.abs(ms))} over`;
}

/** A non-negative interval at the scale a lifetime is read at: minutes, then
 *  hours, then days. `ago`'s ladder, without its direction words. */
function coarse(ms: number): string {
  const seconds = Math.round(ms / 1000);
  if (seconds < 60) return `${seconds}s`;
  const minutes = Math.round(seconds / 60);
  if (minutes < 60) return `${minutes}m`;
  const hours = Math.round(minutes / 60);
  if (hours < 48) return `${hours}h`;
  return `${Math.round(hours / 24)}d`;
}

/** One measurement, in the shape the console's Metric component takes. Declared
 *  structurally rather than imported, so this file keeps its promise of holding
 *  no import that reaches React. */
export interface Measurement {
  label: string;
  /** Null means nobody measured it, which Metric renders as "Not measured".
   *  It is not zero, and the distinction is the whole reason it is nullable. */
  value: number | string | null;
  unit?: string;
  note?: string;
}

/**
 * Which numbers a load result actually has, decided by its kind.
 *
 * THE FOUR KINDS MEASURE DIFFERENT THINGS and the table has a CHECK constraint
 * refusing a row that pretends otherwise: an observed load has requests and no
 * workflows, a browser workflow has workflows and no requests. So this reads
 * the kind and returns only what that kind carries, rather than returning every
 * field and letting three quarters of the tiles say "not measured".
 *
 * That is not tidiness. Rendering a latency percentile beside a browser run is
 * drawing a chart over a number that is not a latency, which is exactly the
 * confusion the constraint exists to prevent, and a console can reintroduce it
 * on the far side of a correct database.
 *
 * A kind this build does not know returns nothing at all, rather than guessing
 * at the fields by name.
 */
export function metricsFor(result: Record<string, unknown> | null): Measurement[] {
  if (!result) return [];
  const n = (key: string): number | null => {
    const value = result[key];
    return typeof value === "number" ? value : null;
  };
  const kind = String(result.kind ?? "");

  if (kind === "observed_load" || kind === "http_scenario") {
    const target = n("target_rate");
    return [
      { label: "Requests", value: n("requests") },
      { label: "Failures", value: n("failures") },
      { label: "Error rate", value: nullable(n("error_rate"), percent) },
      {
        label: "Achieved rate",
        value: nullable(n("achieved_rate"), (v) => `${rate(v)}/s`),
        note: target === null ? undefined : `Asked for ${rate(target)} per second`,
      },
      { label: "p50", value: nullable(n("p50_ms"), duration) },
      { label: "p95", value: nullable(n("p95_ms"), duration) },
      { label: "p99", value: nullable(n("p99_ms"), duration) },
      { label: "Slowest", value: nullable(n("max_ms"), duration) },
      ...(kind === "http_scenario"
        ? [
            { label: "Sessions", value: n("sessions") },
            { label: "Iterations", value: n("iterations") },
          ]
        : []),
    ];
  }

  if (kind === "browser_workflow") {
    // Five outcome counts and not two. A real run against a sample project
    // returned nought passed, nought failed and one unverified because the
    // persona could not be created, and passed-plus-failed alone renders that
    // as a run with no failures.
    return [
      { label: "Workflows", value: n("workflows") },
      { label: "Passed", value: n("workflows_passed") },
      { label: "Failed", value: n("workflows_failed") },
      { label: "Flaky", value: n("workflows_flaky") },
      { label: "Blocked", value: n("workflows_blocked") },
      { label: "Unverified", value: n("workflows_unverified") },
      { label: "Steps", value: n("steps") },
    ];
  }

  if (kind === "exploration") {
    // Two counts rather than one boolean, because a version selects up to fifty
    // goals and one boolean cannot answer for fifty.
    return [
      { label: "Findings", value: n("findings") },
      { label: "Goals", value: n("goals") },
      { label: "Goals reached", value: n("goals_reached") },
    ];
  }

  return [];
}

/** Formats only when there is something to format. Null in, null out, so
 *  `Metric` says a number was not measured rather than printing the dash the
 *  formatters return, which it would treat as a value. */
function nullable(value: number | null, format: (v: number) => string): string | null {
  return value === null ? null : format(value);
}
