// The verdict model is the most important thing in the runner, and the reason
// is worth stating before any of the code.
//
// A test framework that reports pass or fail forces every ambiguous outcome
// into one of two boxes, and the box it picks is almost always "fail". So a
// browser that crashed, a network that dropped, a fixture that was missing,
// and an application that is genuinely broken all arrive at the reviewer as
// the same red mark, and the reviewer learns to ignore red marks.
//
// These five keep them apart. The distinction that matters most is between
// FAIL and BLOCKED: a failure caused by the runner must never count against
// the application, because the moment it does, people stop believing the
// failures that are real.

/** What happened to a workflow. */
export type Verdict = 'pass' | 'fail' | 'flaky' | 'blocked' | 'unverified';

/** Why a workflow ended the way it did. */
export type Cause =
  /** The application did not do what the workflow said it should. */
  | 'expectation-not-met'
  /** The application returned an error the workflow did not expect. */
  | 'application-error'
  /** The runner could not drive the application: a crash, a timeout in the
   *  browser itself, a page that never loaded. Never the application's fault. */
  | 'runner-failure'
  /** Something the environment owed the workflow was missing: a persona that
   *  could not sign in, a fixture with no route, a message that never arrived. */
  | 'environment-incomplete'
  /** The workflow touched a response a model invented, so the result cannot be
   *  trusted either way. */
  | 'synthesized-response'
  /** It worked. */
  | 'succeeded';

/** How a cause maps to a verdict.
 *
 * Kept as data rather than as branches, so the mapping can be read in one
 * place and argued with. The argument that matters: runner-failure is BLOCKED
 * and not FAIL, always, without exception.
 */
const VERDICT_FOR_CAUSE: Record<Cause, Verdict> = {
  'succeeded': 'pass',
  'expectation-not-met': 'fail',
  'application-error': 'fail',
  'runner-failure': 'blocked',
  'environment-incomplete': 'blocked',
  'synthesized-response': 'unverified',
};

/** One attempt at a workflow. */
export interface Attempt {
  readonly cause: Cause;
  readonly detail: string;
  readonly durationMs: number;
}

/** The outcome of running a workflow, including its retries. */
export interface Outcome {
  readonly verdict: Verdict;
  readonly cause: Cause;
  readonly detail: string;
  readonly attempts: readonly Attempt[];
  /** Steps a person can follow to see it themselves. Empty on a pass. */
  readonly reproduction: readonly string[];
}

/** verdictFor maps a single cause to a verdict. */
export function verdictFor(cause: Cause): Verdict {
  return VERDICT_FOR_CAUSE[cause];
}

/** classify turns a sequence of attempts into one outcome.
 *
 * The flaky rule is the subtle one. A workflow that failed and then passed is
 * not a pass: something is wrong, and reporting a pass hides it until it
 * happens in production. It is also not a fail: the application did the right
 * thing at least once, and blocking a pull request on it wastes somebody's
 * afternoon. FLAKY is its own answer because it is its own problem.
 */
export function classify(attempts: readonly Attempt[]): Outcome {
  if (attempts.length === 0) {
    return {
      verdict: 'blocked',
      cause: 'runner-failure',
      detail: 'The workflow was never attempted.',
      attempts,
      reproduction: [],
    };
  }

  const last = attempts[attempts.length - 1]!;
  const anyPassed = attempts.some((a) => a.cause === 'succeeded');
  const anyFailed = attempts.some(
    (a) => a.cause === 'expectation-not-met' || a.cause === 'application-error',
  );

  // A blocked or unverified attempt is about the environment rather than the
  // application, and it does not become a pass because a retry got luckier.
  const blocking = attempts.find(
    (a) => a.cause === 'runner-failure' || a.cause === 'environment-incomplete',
  );
  const synthesized = attempts.find((a) => a.cause === 'synthesized-response');

  if (anyPassed && anyFailed) {
    return {
      verdict: 'flaky',
      cause: last.cause,
      detail:
        `This workflow passed on ${attempts.filter((a) => a.cause === 'succeeded').length} ` +
        `of ${attempts.length} attempts. Something is wrong, and it is not reliable enough ` +
        `to call either way.`,
      attempts,
      reproduction: [],
    };
  }
  if (anyPassed && synthesized) {
    return {
      verdict: 'unverified',
      cause: 'synthesized-response',
      detail: synthesized.detail,
      attempts,
      reproduction: [],
    };
  }
  if (anyPassed) {
    return { verdict: 'pass', cause: 'succeeded', detail: last.detail, attempts, reproduction: [] };
  }
  // A real failure outranks a later runner problem. Without this, a browser
  // that crashed on the retry would turn a genuine failure into a blocked
  // result, which is the one direction this model must never get wrong.
  const failure = attempts.find(
    (a) => a.cause === 'expectation-not-met' || a.cause === 'application-error',
  );
  if (failure) {
    return {
      verdict: verdictFor(failure.cause),
      cause: failure.cause,
      detail: failure.detail,
      attempts,
      reproduction: [],
    };
  }
  if (blocking) {
    return {
      verdict: 'blocked',
      cause: blocking.cause,
      detail: blocking.detail,
      attempts,
      reproduction: [],
    };
  }
  return {
    verdict: verdictFor(last.cause),
    cause: last.cause,
    detail: last.detail,
    attempts,
    reproduction: [],
  };
}

/** countsAgainstTheApplication says whether a verdict should fail a build.
 *
 * BLOCKED does not, and that is the whole point: a runner crash or a missing
 * fixture is our problem or the configuration's, and charging it to the
 * application teaches people to ignore the result.
 */
export function countsAgainstTheApplication(verdict: Verdict): boolean {
  return verdict === 'fail';
}

/** Exit codes, matching the engine's own registry. */
export const EXIT = {
  ok: 0,
  failure: 1,
  usage: 2,
  configuration: 3,
  testFailure: 8,
} as const;

/** exitCodeFor turns a set of outcomes into a process exit code. */
export function exitCodeFor(outcomes: readonly Outcome[]): number {
  if (outcomes.some((o) => countsAgainstTheApplication(o.verdict))) return EXIT.testFailure;
  // Everything else exits zero, including blocked. A blocked run has already
  // said what was missing; failing the build on it would make an incomplete
  // environment indistinguishable from a broken application.
  return EXIT.ok;
}
