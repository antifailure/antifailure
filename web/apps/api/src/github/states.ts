// The seven states one commit's check can be in, and what GitHub is told.
//
// THE RULE THIS FILE EXISTS FOR: blocked and unverified are not passes. They
// are not failures either, and the temptation is to reach for GitHub's
// `neutral`, which reads as "nothing to say". `neutral` PASSES a required
// check. So a pull request whose agents never ran, or whose environment could
// not be built, would merge behind a green tick, which is exactly the defect
// this repository already shipped once: `af test` exits 0 on unverified and
// `blocked` does not count against a run, and an entire nightly corpus was
// green having never once reached an agent.
//
// GITHUB'S VOCABULARY IS SMALLER THAN OURS AND THAT IS WORTH SAYING OUT LOUD.
// A check run's conclusion is one of success, failure, neutral, cancelled,
// timed_out, action_required, skipped and stale, and only success, neutral and
// skipped let a required check pass. Seven states do not map one to one onto
// three non-passing conclusions that fit, so blocked and unverified share
// `action_required`. They are NOT collapsed: the state column keeps them
// apart, the check's title says which one it is in the first line a person
// reads, and the comment says why. What is shared is GitHub's word for "this
// did not pass and a person has to do something", which is true of both.
//
// The one exception is a generation that ran out of time. `timed_out` is a
// conclusion GitHub already has for exactly that, it does not pass a required
// check, and it tells a reader something `action_required` does not: nothing
// came back at all.

/** The state of one attempt against one head commit. Mirrors the
 *  `pr_generation_state` enum in migration 0021. */
export type GenerationState =
  'queued' | 'running' | 'passed' | 'failed' | 'blocked' | 'unverified' | 'cancelled'

export const GENERATION_STATES: readonly GenerationState[] = [
  'queued',
  'running',
  'passed',
  'failed',
  'blocked',
  'unverified',
  'cancelled',
] as const

export type CheckStatus = 'queued' | 'in_progress' | 'completed'

export type CheckConclusion =
  | 'success'
  | 'failure'
  | 'neutral'
  | 'cancelled'
  | 'timed_out'
  | 'action_required'
  | 'skipped'
  | 'stale'

export interface CheckShape {
  status: CheckStatus
  /** Absent while the check is still running, which is what GitHub requires:
   *  a conclusion is what makes a check run completed. */
  conclusion?: CheckConclusion
  /** The first line a person reads, and the only place the seven states stay
   *  seven. */
  title: string
  /** Whether a branch protection rule requiring this check would let the pull
   *  request merge. Written down rather than inferred at each call site,
   *  because "does this pass" is the question the whole file is about and it
   *  is the one nobody should have to look up GitHub's table for. */
  passes: boolean
}

/**
 * What GitHub is told about a generation.
 *
 * `timedOut` distinguishes a generation that said nothing before its deadline
 * from one that reported `unverified` itself. Both are unverified as far as
 * this control plane's own state is concerned, because in both cases nothing
 * was verified; they differ in what a reader should go and look at, so they
 * differ in the conclusion and in the title.
 */
export function checkShapeFor(state: GenerationState, timedOut = false): CheckShape {
  switch (state) {
    case 'queued':
      return { status: 'queued', title: 'Waiting for a runner', passes: false }
    case 'running':
      return {
        status: 'in_progress',
        title: 'Building the environment and running the agents',
        passes: false,
      }
    case 'passed':
      return {
        status: 'completed',
        conclusion: 'success',
        title: 'Every check passed',
        passes: true,
      }
    case 'failed':
      return { status: 'completed', conclusion: 'failure', title: 'A check failed', passes: false }
    case 'blocked':
      return {
        status: 'completed',
        conclusion: 'action_required',
        title: 'Blocked before anything could be checked',
        passes: false,
      }
    case 'unverified':
      return timedOut
        ? {
            status: 'completed',
            conclusion: 'timed_out',
            title: 'Nothing was verified: the run never reported back',
            passes: false,
          }
        : {
            status: 'completed',
            conclusion: 'action_required',
            title: 'Nothing was verified',
            passes: false,
          }
    case 'cancelled':
      return {
        status: 'completed',
        conclusion: 'cancelled',
        title: 'Superseded by a newer commit',
        passes: false,
      }
  }
}

/**
 * The state a finished engine report means.
 *
 * Read tolerantly, because this crosses a boundary: the report is produced by
 * a version of the engine that may be older or newer than this control plane.
 * An outcome word this control plane does not recognise is `unverified` and
 * not `passed`, because the safe direction for an unknown answer about whether
 * software works is "we do not know".
 */
export function stateFromReport(report: ReportCounts): GenerationState {
  if (report.failed > 0) return 'failed'
  if (report.blocked > 0) return 'blocked'
  if (report.unverified > 0) return 'unverified'
  if (report.passed > 0) return 'passed'
  // Zero of everything. A run that executed no workflow at all verified
  // nothing, and this is the exact case that made a whole nightly corpus green:
  // two examples declared no workflows, the report headline was literally
  // "Antifailure: Nothing ran", and the leg was green.
  return 'unverified'
}

/** The counts an engine report carries, after tolerant decoding. */
export interface ReportCounts {
  passed: number
  failed: number
  flaky: number
  blocked: number
  unverified: number
}

