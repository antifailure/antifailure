// Running a workflow is a loop: look at the page, decide one thing, do it,
// look again. It stops when the expectations are met, when the planner has
// nothing left to try, or when it has taken more steps than any real workflow
// takes.
//
// Every way it can stop maps to a cause, and the causes are what keep a runner
// problem from reading as an application failure.

import { Session } from './browser.ts';
import { signIn, type Persona, type Page } from './login.ts';
import type { InboxSource } from './inbox.ts';
import {
  DeterministicPlanner, freshIdentity, judgeAll,
  type Action, type Planner, type Snapshot, type Workflow,
} from './workflow.ts';
import { classify, type Attempt, type Cause, type Outcome } from './verdict.ts';
import { ModelPlanner } from './model.ts';

/** Everything one run needs. */
export interface Job {
  readonly baseURL: string;
  readonly artifacts: string;
  readonly workflows: readonly Workflow[];
  readonly personas: readonly Persona[];
  readonly inbox?: InboxSource;
  readonly planner?: Planner;
  /** model, when set, lets a model read the page and decide. The key is the
   *  user's: nothing here ships one and the engine never stores one. */
  readonly model?: import('./model.ts').ModelConfig;
  /** complete overrides how a model is asked. Set by a cassette, so a run can
   *  replay recorded answers and reach no network at all. */
  readonly complete?: import('./model.ts').Complete;
  /** attempts is how many times a workflow is retried before being called
   *  flaky or failed. Two is the useful number: one retry distinguishes a
   *  genuine failure from a fluke, and more just makes a slow suite slower. */
  readonly attempts?: number;
  readonly headless?: boolean;
  readonly stepTimeoutMs?: number;
}

/** What one workflow produced. */
export interface WorkflowResult {
  readonly workflow: string;
  readonly outcome: Outcome;
  readonly steps: readonly string[];
  readonly evidence: {
    readonly video?: string;
    readonly trace?: string;
    readonly screenshot?: string;
    readonly console: readonly string[];
    readonly failed: readonly string[];
  };
  readonly durationMs: number;
}

const MAX_STEPS = 40;

/** run drives every workflow and returns a result for each. */
export async function run(job: Job): Promise<WorkflowResult[]> {
  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    results.push(await runOne(job, workflow));
  }
  return results;
}

async function runOne(job: Job, workflow: Workflow): Promise<WorkflowResult> {
  const started = Date.now();
  const attempts: Attempt[] = [];
  const steps: string[] = [];
  let evidence: WorkflowResult['evidence'] = { console: [], failed: [] };

  const total = job.attempts ?? 2;
  for (let attempt = 1; attempt <= total; attempt++) {
    const at = Date.now();
    let session: Session | undefined;
    try {
      session = await Session.open({
        artifacts: job.artifacts,
        ...(job.headless === undefined ? {} : { headless: job.headless }),
      });
      const { cause, detail, taken } = await attemptOnce(job, workflow, session, attempt);
      attempts.push({ cause, detail, durationMs: Date.now() - at });
      steps.length = 0;
      steps.push(...taken);
      if (cause === 'succeeded') {
        // Closed by the finally below, not here. Closing twice returned a
        // second, emptier evidence record that overwrote the good one, so a
        // passing run came back with no screenshot and no trace.
        break;
      }
    } catch (err) {
      // Anything thrown out here is the runner's own failure: the browser did
      // not start, the page never loaded, a locator timed out. None of it is
      // evidence about the application.
      attempts.push({
        cause: 'runner-failure',
        detail: err instanceof Error ? err.message : String(err),
        durationMs: Date.now() - at,
      });
    } finally {
      if (session) {
        evidence = await session.close(`${workflow.name}-${attempt}`).catch(() => evidence);
      }
    }
  }

  const outcome = classify(attempts);
  return {
    workflow: workflow.name,
    outcome: { ...outcome, reproduction: reproduction(workflow, steps, outcome) },
    steps,
    evidence,
    durationMs: Date.now() - started,
  };
}

interface AttemptResult {
  readonly cause: Cause;
  readonly detail: string;
  readonly taken: readonly string[];
}

async function attemptOnce(
  job: Job, workflow: Workflow, session: Session, attempt: number,
): Promise<AttemptResult> {
  const taken: string[] = [];
  const page = session.page();

  const persona = job.personas.find((p) => p.name === workflow.persona) ?? job.personas[0];
  if (workflow.persona && !persona) {
    return {
      cause: 'environment-incomplete',
      detail: `This workflow runs as ${workflow.persona}, and no persona by that name is declared.`,
      taken,
    };
  }
  if (persona) {
    const login = await signIn(page, persona, {
      baseURL: job.baseURL,
      // Where this workflow was going anyway is the first place to look for
      // the form. An application that answers every protected route with its
      // sign-in screen, which is what this repository's own control plane
      // does, is signed into without guessing at a path it does not have.
      ...(workflow.startPath ? { signInPath: workflow.startPath } : {}),
      ...(job.inbox ? { inbox: job.inbox } : {}),
    });
    taken.push(`Sign in as ${persona.name}: ${login.detail}`);
    if (!login.ok) {
      return {
        cause: login.blocked ? 'environment-incomplete' : 'application-error',
        detail: login.detail,
        taken,
      };
    }
  }

  await page.goto(join(job.baseURL, workflow.startPath ?? '/'));
  taken.push(`Open ${join(job.baseURL, workflow.startPath ?? '/')}`);

  // A fresh identity per attempt, so a retry of a sign up is a sign up rather
  // than a duplicate address the application rightly refuses.
  const deterministic = new DeterministicPlanner(
    freshIdentity(`${workflow.name}-${attempt}-${Date.now()}`),
  );
  // A model reads the page when a key is available, and the deterministic
  // planner is its fallback rather than its replacement: a model that is
  // unreachable mid run should not end the workflow, and the shapes every
  // application shares do not need one.
  const planner = job.planner
    ?? (job.model
      ? new ModelPlanner(job.model, job.complete, deterministic)
      : deterministic);

  const history: Action[] = [];
  const limit = workflow.maxSteps ?? MAX_STEPS;
  for (let step = 0; step < limit; step++) {
    const snapshot = await session.snapshot();
    const action = await planner.next(workflow, snapshot, history);
    history.push(action);

    switch (action.kind) {
      case 'done':
        return { cause: 'succeeded', detail: action.why, taken };
      case 'stuck':
        return finalJudgement(workflow, snapshot, action.why, taken);
      case 'fill':
        await page.fill(action.field, action.value);
        taken.push(`Fill ${action.field.source.replace(/[\^$]/g, '')}: ${action.why}`);
        break;
      case 'click':
        await page.click(action.control);
        taken.push(`Press ${action.control.source.replace(/[\^$]/g, '')}: ${action.why}`);
        break;
      case 'goto':
        await page.goto(action.url);
        taken.push(`Open ${action.url}: ${action.why}`);
        break;
    }
  }

  const snapshot = await session.snapshot();
  return finalJudgement(
    workflow, snapshot,
    `The workflow took ${limit} steps without reaching what it was asked to reach.`,
    taken,
  );
}

/** finalJudgement decides what a run that did not obviously finish means.
 *
 * This is where the three way expectation check earns itself. A page that says
 * the opposite of what was expected is a failure; a page that says something
 * the checker cannot read is unverified, not a pass and not a fail. Guessing
 * either way would be worse than saying so.
 */
function finalJudgement(
  workflow: Workflow, snapshot: Snapshot, why: string, taken: readonly string[],
): AttemptResult {
  switch (judgeAll(workflow.expect, snapshot.text)) {
    case 'met':
      return { cause: 'succeeded', detail: 'Every expectation is visible on the page.', taken };
    case 'unmet':
      return {
        cause: 'expectation-not-met',
        detail: `${why} The page shows an error rather than what was expected.`,
        taken,
      };
    default:
      return {
        cause: 'synthesized-response',
        detail:
          `${why} Nothing on the page contradicts what was expected, and nothing confirms it ` +
          `either, so this run proved nothing. Set a model key so the runner can read the page, ` +
          `or write an expectation whose words appear on it.`,
        taken,
      };
  }
}

/** reproduction turns what the agent did into steps a person can follow. */
function reproduction(
  workflow: Workflow, steps: readonly string[], outcome: Outcome,
): readonly string[] {
  if (outcome.verdict === 'pass') return [];
  return [
    `Bring the environment up with af up, then follow these:`,
    ...steps.map((s, i) => `${i + 1}. ${s}`),
    `Expected: ${workflow.expect.join(' ')}`,
    `Got: ${outcome.detail}`,
  ];
}

function join(base: string, path: string): string {
  if (/^https?:\/\//.test(path)) return path;
  return base.replace(/\/+$/, '') + '/' + path.replace(/^\/+/, '');
}

export type { Page };
