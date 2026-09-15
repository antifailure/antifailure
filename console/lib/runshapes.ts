/**
 * The tenant runs page's two vocabularies, and the question the page could not
 * answer about either of them.
 *
 * WHY THIS IS A SEPARATE FILE FROM loadshapes.ts, which already holds a
 * `RunState` and a verdict vocabulary. Because they are DIFFERENT Postgres
 * types and the console has already been bitten three times by treating one
 * vocabulary as another. `web/packages/db/migrations/0001_init.sql:230` declares
 *
 *   CREATE TYPE run_state AS ENUM ('queued','running','complete','failed','cancelled')
 *
 * which is what the `runs` table the tenant page reads is typed as.
 * loadshapes' `RunState` is `workload_run_state`: requested, accepted, running,
 * succeeded, failed, cancelled, timed_out, abandoned. The two share exactly
 * two words. Calling loadshapes' `isRunning` on a tenant run would have been
 * the worst possible kind of wrong: `queued` is not in its set, so a run
 * waiting to start would have read as finished and the poll this file exists to
 * enable would never have begun. It would also have been invisible, because the
 * one word they share, `running`, is the one a demo happens to look at.
 *
 * The VERDICT vocabulary is reused rather than restated, because there the two
 * really are one thing: `verdict_value` (0001_init.sql:247) is
 * pass, fail, flaky, blocked, unverified, a strict subset of loadshapes'
 * `Verdict`, and loadshapes' `VERDICT_FACTS` already carries the fact this file
 * needs, which is whether a word is a judgement at all. Copying that list here
 * would create the second copy whose drift loadshapes' own header is about.
 */

import type { Tone } from "./tone.ts";
import { VERDICT_FACTS, verdictOf } from "./loadshapes.ts";

/** `run_state` in 0001_init.sql:230. Five words, not the workload run's eight. */
export type RunState = "queued" | "running" | "complete" | "failed" | "cancelled";

const RUN_STATES = new Set<string>(["queued", "running", "complete", "failed", "cancelled"]);

export function runStateOf(v: unknown): RunState | null {
  return typeof v === "string" && RUN_STATES.has(v) ? (v as RunState) : null;
}

/**
 * Whether the run may still change, which is the only reason to ask again.
 *
 * `queued` counts. A run dispatched to GitHub and not yet picked up is the
 * state a reader watches for longest, and it is the state that made this page
 * look broken: nothing on the screen ever moved and there was no control to
 * make it move.
 *
 * An UNRECOGNISED state answers false. A sixth word appearing in the enum
 * should stop the polling rather than poll a finished run forever, and the
 * gate for that is the test beside this file, which reads the enum out of the
 * migration rather than out of this list.
 */
export function runIsInFlight(state: unknown): boolean {
  const s = runStateOf(state);
  return s === "queued" || s === "running";
}

/** What a run's verdicts add up to, counted rather than asserted. */
export interface VerdictTally {
  /** Every verdict row the control plane returned. */
  total: number;
  /** Rows that are a judgement about the software: pass, fail, flaky, warn. */
  proved: number;
  /** Ran and evaluated nothing. */
  unverified: number;
  /** Never reached the application. */
  blocked: number;
  /** A word this console does not know. Counted, never guessed at. */
  unknown: number;
}

export function tallyVerdicts(values: readonly unknown[]): VerdictTally {
  const t: VerdictTally = { total: 0, proved: 0, unverified: 0, blocked: 0, unknown: 0 };
  for (const raw of values) {
    t.total += 1;
    const v = verdictOf(raw);
    if (v === null) {
      t.unknown += 1;
      continue;
    }
    if (v === "unverified") t.unverified += 1;
    if (v === "blocked") t.blocked += 1;
    if (VERDICT_FACTS[v].conclusive) t.proved += 1;
  }
  return t;
}

/**
 * A run that reported verdicts and proved nothing with any of them.
 *
 * The exit code zero over nothing defect, in the shape the tenant runs page
 * actually holds it. `nothingWasChecked` in loadshapes answers the same
 * question and could not be called here: it takes a `RunResult`, meaning the
 * aggregate counts the load result row carries, and this page has the verdict
 * ROWS with no aggregate anywhere on them. So the judgement is reused (the
 * `conclusive` flag decides, in one place, which words are proof) and only the
 * counting is written twice, because the two inputs are genuinely different.
 *
 * An UNKNOWN word is not proof. If a verdict arrives that this console cannot
 * read, the honest thing on screen is "nothing here verified anything" and not
 * silence: a console that treats a word it does not understand as a pass is the
 * defect with an extra step. It is a false alarm in the worst case and a
 * missed failure in the other direction, and this repository's standing rule is
 * that an instrument says it could not look rather than implying it looked.
 *
 * False when there are no verdicts at all. A run with nothing to report is the
 * empty state's business, and a banner there would shout at every run in its
 * first seconds.
 */
export function nothingWasVerified(values: readonly unknown[]): boolean {
  const t = tallyVerdicts(values);
  return t.total > 0 && t.proved === 0;
}

/**
 * The sentence the "no verdicts" empty state should say, which depends on
 * whether the run is still going.
 *
 * Before the page polled, one sentence had to cover both and it hedged:
 * "usually means it is still going or it failed before the first workflow".
 * That was the compound half of the defect. A run whose personas all failed to
 * provision sits on that screen forever, and the reader is told it is probably
 * fine and probably still working. Now the run's own state is on screen and
 * live, so the two cases are separable and get separate words.
 */
export function noVerdictsReason(state: unknown): string {
  if (runIsInFlight(state)) {
    return (
      "This run is still going and the runner records a verdict per workflow " +
      "as each one finishes. This screen refreshes on its own, so verdicts " +
      "will appear here without reloading."
    );
  }
  if (runStateOf(state) === null) {
    return (
      "The runner records a verdict per workflow when it finishes, and this " +
      "run has produced none. Its state is not one this console recognises, " +
      "so nothing here should be read as saying the run was fine."
    );
  }
  return (
    "This run has finished and produced no verdicts at all, so nothing about " +
    "your application was checked. That is not the same as nothing being " +
    "wrong. A run that ends this way usually failed before the first workflow " +
    "started, most often because the environment or a persona could not be " +
    "created."
  );
}

/**
 * The banner a run that proved nothing has to wear, or null when it proved
 * something.
 *
 * The words are the load view's words, deliberately. `components/load/results.tsx`
 * already says "never reached the application" for blocked and "ran and proved
 * nothing" for unverified, and two screens describing the same outcome in two
 * vocabularies is how a reader learns to distrust both. This is a string rather
 * than markup so the sentence itself is testable: the defect this guards
 * against is silence, and a test that only asserted an element existed could
 * not tell a banner from an empty one.
 */
export function nothingWasVerifiedNotice(values: readonly unknown[]): string | null {
  const t = tallyVerdicts(values);
  if (t.total === 0 || t.proved > 0) return null;

  const lead =
    t.total === 1
      ? "This run recorded one verdict and it did not pass, fail or come back flaky."
      : `This run recorded ${t.total} verdicts and not one of them passed, failed or came back flaky.`;

  const parts: string[] = [];
  if (t.blocked > 0) {
    parts.push(
      t.blocked === 1
        ? "One never reached the application."
        : `${t.blocked} never reached the application.`,
    );
  }
  if (t.unverified > 0) {
    parts.push(
      t.unverified === 1
        ? "One ran and proved nothing."
        : `${t.unverified} ran and proved nothing.`,
    );
  }
  if (t.unknown > 0) {
    parts.push(
      t.unknown === 1
        ? "One carries a verdict this console does not recognise, so it is not being counted as a pass."
        : `${t.unknown} carry a verdict this console does not recognise, so they are not being counted as passes.`,
    );
  }

  return [
    lead,
    "Nothing about your application was checked, which is not the same as nothing being wrong.",
    ...parts,
  ].join(" ");
}

/**
 * The failing verdict's reproduction, as the text to print, or null when the
 * runner recorded none.
 *
 * The column was selected by the control plane, declared on the page's own
 * `Verdict` interface and then dropped on the floor: `runs.verdicts` sends
 * `reproduction`, the interface named it, and no site in the file rendered it.
 * A customer whose check failed was shown the failure and not the one thing
 * that lets them do anything about it, while our own operator console printed
 * it in a labelled column the whole time.
 *
 * NEVER assembled out of the workflow name and the persona. A command a console
 * builds is a command nobody ran, and being the exact one the runner ran is the
 * only reason to print it at all.
 *
 * `unknown` in, because that is how the column arrives: jsonb, so it can be an
 * object, a string, a number or SQL NULL, and the page must not assume which.
 * A JSON `null` is treated as nothing recorded, which is what it means here and
 * is also the only reading that cannot print the word "null" at a customer as
 * if it were a command.
 */
export function reproductionText(reproduction: unknown): string | null {
  if (reproduction === null || reproduction === undefined) return null;
  if (typeof reproduction === "string") return reproduction.trim() === "" ? null : reproduction;
  return JSON.stringify(reproduction, null, 2);
}

/**
 * The counts the runs LIST needs, which is a different question from the run
 * DETAIL above and reaches the page a different way.
 *
 * The detail holds verdict ROWS and tallies them with `tallyVerdicts`. A row in
 * the list has no verdicts attached: `runs.recent` sends aggregate counts per
 * run, because attaching every run's verdicts to a fifty row list is a join the
 * page does not need. So the list gets its numbers from the server and this is
 * their shape. `passing` is the passes alone; `failing` is the fails alone, not
 * fails and blocks together, because a blocked verdict never reached the app and
 * is not a failure of it; `proved` is the conclusive verdicts, the ones that are
 * a judgement at all.
 */
export interface RecentTally {
  /** Every verdict the run recorded, conclusive or not. */
  total: number;
  /** `pass` only. */
  passing: number;
  /** `fail` only. */
  failing: number;
  /** The conclusive verdicts: `pass`, `fail`, `flaky`, `warn`. */
  proved: number;
}

/**
 * The word and tone the list's Verdicts cell shows, or null when there is
 * nothing to say yet (no verdicts, so the cell renders a dash).
 *
 * This is the list's spelling of the same judgement the detail's
 * `nothingWasVerifiedNotice` makes, and the two must not disagree about one
 * run. The list had said "N passing" whenever nothing was failing, so a run
 * whose verdicts were all blocked or unverified drew green on the surface a
 * customer scans first while the detail banner said nothing was verified. The
 * precedence here mirrors the banner: a failure is named first, a run that
 * proved nothing is called that rather than passing, a run that proved
 * something short of all passes is neither green nor a failure, and only a run
 * that is genuinely all passes is green.
 *
 * `failing > 0` implies `proved > 0` because `fail` is conclusive, so the
 * failing branch and the proved-nothing branch never both apply.
 */
export function recentRunSummary(t: RecentTally): { tone: Tone; text: string } | null {
  if (t.total === 0) return null;
  if (t.failing > 0) return { tone: "fail", text: `${t.failing} of ${t.total} failing` };
  if (t.proved === 0) return { tone: "warn", text: "nothing verified" };
  if (t.passing < t.total) return { tone: "warn", text: `${t.passing} of ${t.total} passed` };
  return { tone: "pass", text: `${t.total} passing` };
}
