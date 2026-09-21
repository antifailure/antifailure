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
  DeterministicPlanner, failureSentence, freshIdentity, judgeAll, notFound, observed, unmatchable,
  type Action, type Planner, type Snapshot, type Workflow,
} from './workflow.ts';
import { classify, type Attempt, type Cause, type Outcome } from './verdict.ts';
import { ModelPlanner } from './model.ts';
import { agentsFor, traits, type Assignment, type ResolvedDiversity } from './personality.ts';
import { nullSink, type LiveSink } from './live.ts';

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
  /** diversity is the resolved per-agent personality plan from the engine.
   *  Absent means one neutral agent per workflow, today's behavior. */
  readonly diversity?: ResolvedDiversity;
  /** live, when set, streams agent state, steps, and frames to a watcher while
   *  the run is going. Defaults to a no-op sink, so a run nobody is watching is
   *  byte for byte the run it was before this existed. */
  readonly live?: LiveSink;
}

/** What one workflow produced. */
export interface WorkflowResult {
  readonly workflow: string;
  /** personality is the id of the personality that drove this run, absent for
   *  a neutral run. Present so a report shows which behavioral lens produced a
   *  verdict when several agents drive one workflow. */
  readonly personality?: string;
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
  /** When this workflow ran, as RFC 3339 in UTC.
   *
   *  Here so the engine can attribute an outbound request to the workflow
   *  that caused it. The runner drives a browser and never sees the calls the
   *  APPLICATION makes, so it cannot know that a workflow touched a response
   *  a model invented; the proxy knows, and the only thing that connects the
   *  two is a window. durationMs alone could not: it says how long, not when,
   *  and the engine's clock is not this process's. */
  readonly startedAt: string;
  readonly finishedAt: string;
}

const MAX_STEPS = 40;

/** The agent a workflow shows up as in a live view. Its id is unique within a
 *  run so a watcher can follow or switch to it: a diversity plan drives the
 *  same workflow once per personality, and those are different agents, so the
 *  personality's own index distinguishes them and its name labels the pane. */
function agentFor(job: Job, workflow: Workflow, assignment?: Assignment) {
  const persona = workflow.personas?.[0] ?? workflow.persona
    ?? job.personas[0]?.name;
  const id = assignment ? `${workflow.name}#${assignment.agentIndex}` : workflow.name;
  // The personality is its own field rather than parenthesised onto the
  // workflow name. A watcher heads each pane with it, and a name that has to be
  // parsed back out of another field is a name a view cannot lay out: the
  // workflow and the personality want different widths, different emphasis and,
  // when the pane is narrow, different survival.
  return {
    id,
    workflow: workflow.name,
    surface: 'web' as const,
    ...(persona ? { persona } : {}),
    ...(assignment
      ? { personality: assignment.personality.name, traits: traits(assignment.profile) }
      : {}),
  };
}

/** run drives every workflow and returns a result for each.
 *
 * When a diversity plan is present a workflow is driven once per assigned
 * personality, each its own result labelled with the personality, so behavior
 * coverage is visible. With no plan the inner list is a single neutral run,
 * which is exactly the one result per workflow this produced before.
 */
export async function run(job: Job): Promise<WorkflowResult[]> {
  const sink = job.live ?? nullSink();
  // The whole cast is announced up front, each pending, so a watcher sees every
  // agent that is going to run and can switch to one before it starts rather
  // than watching them appear one at a time.
  for (const workflow of job.workflows) {
    for (const assignment of agentsFor(job.diversity, workflow.name)) {
      sink.agent(agentFor(job, workflow, assignment), 'pending');
    }
  }
  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    for (const assignment of agentsFor(job.diversity, workflow.name)) {
      results.push(await runOne(job, workflow, assignment));
    }
  }
  return results;
}

async function runOne(
  job: Job, workflow: Workflow, assignment?: Assignment,
): Promise<WorkflowResult> {
  const started = Date.now();
  const sink = job.live ?? nullSink();
  const desc = agentFor(job, workflow, assignment);
  const attempts: Attempt[] = [];
  const steps: string[] = [];
  let evidence: WorkflowResult['evidence'] = { console: [], failed: [] };

  const total = job.attempts ?? 2;
  // The time budget covers the whole workflow, retries included, because it is
  // what the manifest says this workflow may cost somebody waiting on it. A
  // retry that started after the budget was spent would be spending time the
  // manifest never granted.
  const deadline = workflow.maxMs === undefined ? undefined : started + workflow.maxMs;
  for (let attempt = 1; attempt <= total; attempt++) {
    if (deadline !== undefined && Date.now() >= deadline) break;
    const at = Date.now();
    let session: Session | undefined;
    let opening: Promise<Session> | undefined;
    let interrupted = false;
    const taken: string[] = [];
    try {
      const attemptRun = (async () => {
        sink.agent(desc, 'connecting');
        opening = Session.open({
          artifacts: job.artifacts,
          ...(job.headless === undefined ? {} : { headless: job.headless }),
          live: { sink, agent: desc.id },
        });
        session = await opening;
        sink.agent(desc, 'live');
        return attemptOnce(job, workflow, session, attempt, taken, assignment, (text, url, action) =>
          sink.step(desc.id, { text, ...(url ? { url } : {}), ...(action ? { action } : {}) }));
      })();
      // Raced rather than checked between steps, because a check between
      // steps is not a cap: one page that never answers holds the workflow for
      // the browser's own thirty second timeout, and a sign in waiting on an
      // inbox holds it for longer. The losing attempt is left to fail when the
      // session closes under it below.
      const settled = deadline === undefined
        ? await attemptRun
        : await withinBudget(attemptRun, deadline - Date.now());
      if (settled === BUDGET_SPENT) {
        interrupted = true;
        attemptRun.catch(() => undefined);
        attempts.push({
          cause: 'budget-exhausted',
          detail: budgetDetail(workflow.maxMs!, Date.now() - started, attempt, taken),
          durationMs: Date.now() - at,
        });
        steps.length = 0;
        steps.push(...taken);
        break;
      }
      const { cause, detail } = settled;
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
        evidence = await session.close(`${workflow.name}-${attempt}`, { interrupted })
          .catch(() => evidence);
      } else if (interrupted && opening) {
        // The budget ran out while the browser was still starting. The launch
        // goes on finishing after the race is over, and a browser nobody closes
        // keeps this process alive forever, so a workflow stopped at its budget
        // became a runner that never exited. Wait for the launch and close what
        // it produced.
        await opening
          .then((late) => late.close(`${workflow.name}-${attempt}`, { interrupted: true }))
          .catch(() => undefined);
      }
    }
  }

  const outcome = classify(attempts);
  // The agent has a verdict now, so the watcher's pane can settle from live to
  // ended and show it. This is the last event an agent sends.
  sink.agent(desc, 'ended', outcome.verdict);
  return {
    workflow: workflow.name,
    ...(assignment ? { personality: assignment.personality.id } : {}),
    outcome: { ...outcome, reproduction: reproduction(workflow, steps, outcome) },
    steps,
    evidence,
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}

interface AttemptResult {
  readonly cause: Cause;
  readonly detail: string;
  readonly taken: readonly string[];
}

/** How a step reaches a live watcher: the human sentence, and optionally the
 *  url it happened on and the kind of action it was. A no-op by default, so the
 *  loop below reads the same whether or not anybody is watching. */
type EmitStep = (text: string, url?: string, action?: string) => void;

async function attemptOnce(
  job: Job, workflow: Workflow, session: Session, attempt: number, taken: string[],
  assignment?: Assignment,
  emit: EmitStep = () => {},
): Promise<AttemptResult> {
  const page = session.page();
  // Every step is pushed to the transcript and streamed to a watcher in one
  // place, so the two can never drift: a step a watcher saw is a step the
  // report has, and the reverse.
  const record = (text: string, action?: string) => {
    taken.push(text);
    emit(text, page.url(), action);
  };

  const chosen = sessionsFor(workflow, job.personas);
  if (chosen.missing) {
    return {
      cause: 'environment-incomplete',
      detail: `This workflow runs as ${chosen.missing}, and no persona by that name is declared.`,
      taken,
    };
  }
  // Every session, in the order named, into ONE browser. A second sign-in
  // does not replace the first: each strategy sets its own cookie and the
  // browser keeps both, which is exactly the state a person in two roles is
  // in and the state a single sign-in could never reproduce.
  for (const persona of chosen.personas) {
    const login = await signIn(page, persona, {
      baseURL: job.baseURL,
      // Where this workflow was going anyway is the first place to look for
      // the form. An application that answers every protected route with its
      // sign-in screen, which is what this repository's own control plane
      // does, is signed into without guessing at a path it does not have.
      // A persona with a sign-in path of its own outranks this inside signIn.
      ...(workflow.startPath ? { signInPath: workflow.startPath } : {}),
      ...(job.inbox ? { inbox: job.inbox } : {}),
    });
    record(`Sign in as ${persona.name}: ${login.detail}`, 'signin');
    if (!login.ok) {
      return {
        cause: login.blocked ? 'environment-incomplete' : 'application-error',
        detail: login.detail,
        taken,
      };
    }
  }

  await page.goto(join(job.baseURL, workflow.startPath ?? '/'));
  record(`Open ${join(job.baseURL, workflow.startPath ?? '/')}`, 'goto');

  // A fresh identity per attempt, so a retry of a sign up is a sign up rather
  // than a duplicate address the application rightly refuses.
  const deterministic = new DeterministicPlanner(
    freshIdentity(`${workflow.name}-${attempt}-${Date.now()}`),
  );
  // A model reads the page when a key is available, and the deterministic
  // planner is its fallback rather than its replacement: a model that is
  // unreachable mid run should not end the workflow, and the shapes every
  // application shares do not need one.
  // A personality reaches the model planner as a prompt preamble; the
  // deterministic fallback stays neutral, which is honest, because without a
  // model key there is nothing a behavioral lens could act on. So a
  // personality with no key runs exactly as a neutral run does today.
  const planner = job.planner
    ?? (job.model
      ? new ModelPlanner(job.model, job.complete, deterministic, assignment)
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
        record(`Fill ${action.field.source.replace(/[\^$]/g, '')}: ${action.why}`, 'fill');
        break;
      case 'check':
        await page.check(action.field);
        record(`Choose ${action.field.source.replace(/[\^$]/g, '')}: ${action.why}`, 'check');
        break;
      case 'click':
        await page.click(action.control);
        record(`Press ${action.control.source.replace(/[\^$]/g, '')}: ${action.why}`, 'click');
        break;
      case 'goto':
        await page.goto(action.url);
        record(`Open ${action.url}: ${action.why}`, 'goto');
        break;
    }
  }

  const snapshot = await session.snapshot();
  return stepsExhausted(workflow, snapshot, limit, taken);
}

/** stepsExhausted is the verdict when a workflow has used every step.
 *
 * The page it reached is still judged, because a workflow whose page shows
 * everything it expected did reach it, and that is a pass however many steps
 * it took. A page that answered with an HTTP error is still the application
 * failing. Anything else is the budget running out before a verdict, which is
 * blocked with the budget named: reporting it as an unmet expectation made a
 * workflow that simply ran out of steps read as a failure of the change. */
export function stepsExhausted(
  workflow: Workflow, snapshot: Snapshot, limit: number, taken: readonly string[],
): AttemptResult {
  const why = `Stopped at its budget of ${limit} ${limit === 1 ? 'step' : 'steps'}: ` +
    'the page it reached does not show what was expected.';
  const judged = finalJudgement(workflow, snapshot, why, taken);
  if (judged.cause === 'succeeded' || judged.cause === 'application-error') return judged;
  return { cause: 'budget-exhausted', detail: judged.detail, taken };
}

/** BUDGET_SPENT is what withinBudget settles to when the time ran out first. */
const BUDGET_SPENT = Symbol('budget spent');

/** withinBudget settles to the work's result, or to BUDGET_SPENT when the
 *  remaining time runs out first. The timer is always cleared, so a workflow
 *  that finished in time leaves nothing holding the process open. */
export async function withinBudget<T>(
  work: Promise<T>, remainingMs: number,
): Promise<T | typeof BUDGET_SPENT> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  const spent = new Promise<typeof BUDGET_SPENT>((resolve) => {
    timer = setTimeout(() => resolve(BUDGET_SPENT), Math.max(0, remainingMs));
  });
  try {
    return await Promise.race([work, spent]);
  } finally {
    clearTimeout(timer);
  }
}

export { BUDGET_SPENT };

/** budgetDetail says which budget stopped a workflow and when.
 *
 *  The budget, how far in the workflow was, which attempt, and the last thing
 *  it did, because the next question about a workflow that ran out of time is
 *  always whether it was stuck or merely slow, and the last step answers it. */
export function budgetDetail(
  maxMs: number, elapsedMs: number, attempt: number, taken: readonly string[],
): string {
  const last = taken[taken.length - 1];
  return `Stopped at its time budget of ${duration(maxMs)}, ${duration(elapsedMs)} into the ` +
    `workflow on attempt ${attempt}, ` +
    (last ? `after: ${last}.` : 'before it took a single step.') +
    ' A workflow that runs out of time is blocked rather than judged, because an unfinished run ' +
    'is evidence about neither the change nor the application. Raise budget.duration if the ' +
    'flow is genuinely this long, or read the trace to see where it waited.';
}

/** duration renders milliseconds the way the manifest writes a budget. */
export function duration(ms: number): string {
  if (ms < 1_000) return `${Math.round(ms)}ms`;
  const seconds = ms / 1_000;
  if (seconds < 60) return `${Number.isInteger(seconds) ? seconds : seconds.toFixed(1)}s`;
  const minutes = Math.floor(seconds / 60);
  const rest = Math.round(seconds - minutes * 60);
  return rest === 0 ? `${minutes}m` : `${minutes}m${rest}s`;
}

/** sessionsFor resolves which personas a workflow signs in as, in order.
 *
 * The list form wins when it is present, because a workflow that names
 * several sessions is about holding all of them. The single form keeps its
 * old fallback to the first declared persona, which a manifest with one
 * persona and no `persona:` lines relies on. A name that matches nothing is
 * returned rather than skipped: silently signing in as fewer personas than the
 * workflow named would run the workflow in a state it was not written for and
 * report against the application whatever that state produced.
 */
export function sessionsFor(
  workflow: Pick<Workflow, 'persona' | 'personas'>,
  declared: readonly Persona[],
): { readonly personas: readonly Persona[]; readonly missing?: string } {
  const names = workflow.personas?.length ? workflow.personas : null;
  if (names) {
    const personas: Persona[] = [];
    for (const name of names) {
      const persona = declared.find((p) => p.name === name);
      if (!persona) return { personas: [], missing: name };
      personas.push(persona);
    }
    return { personas };
  }
  const persona = declared.find((p) => p.name === workflow.persona) ?? declared[0];
  if (workflow.persona && !persona) return { personas: [], missing: workflow.persona };
  return { personas: persona ? [persona] : [] };
}

/** finalJudgement decides what a run that did not obviously finish means.
 *
 * This is where the three way expectation check earns itself. A page that says
 * the opposite of what was expected is a failure; a page that says something
 * the checker cannot read is unverified, not a pass and not a fail. Guessing
 * either way would be worse than saying so.
 */
export function finalJudgement(
  workflow: Workflow, snapshot: Snapshot, why: string, taken: readonly string[],
): AttemptResult {
  // A server error outranks the expectation check entirely, and has to be
  // decided before judgeAll ever runs. The control plane's own launch day
  // readiness review found the failure this guards against: a page erroring
  // with "relation customers does not exist" was judged only on whether its
  // words matched three different rewrites of an expectation, none of which
  // could ever match, because the page never rendered anything to match
  // against. Every one of those runs came back UNVERIFIED with a note about
  // wording, and nothing the runner already knew, the response status it had
  // right here, ever reached the report. An application that answered is not
  // an unread expectation; it is a failed one, and the reader should not have
  // to go to af logs to learn that.
  if (snapshot.status !== undefined && snapshot.status >= 400) {
    return {
      cause: 'application-error',
      detail:
        `The page at ${snapshot.url} answered HTTP ${snapshot.status} instead of rendering. ` +
        `A page that errors cannot show what ${workflow.name} expected, so this is a failure of ` +
        `the application, not an unread expectation. Read the response body or af logs for the cause.`,
      taken,
    };
  }
  switch (judgeAll(workflow.expect, snapshot.text)) {
    case 'met':
      return { cause: 'succeeded', detail: 'Every expectation is visible on the page.', taken };
    case 'unmet': {
      // Quoted, not summarised. "The page shows an error rather than what was
      // expected" was the whole of this sentence, and it is the same sentence
      // for a route that does not exist, a hostname the control plane refuses,
      // a database that is down and a card that was declined. The words on the
      // screen are the only thing that tells those apart, and they were being
      // thrown away one line before the report was written.
      //
      // And only said when it is true. `unmet` also means a quoted expectation
      // is simply absent from a healthy page, and calling that an error sent
      // the reader hunting for one that did not exist. Without a failure
      // sentence the detail names what was missing and what was there
      // instead, and leads with them, because the planner's `why` is 183
      // characters on its own and the report's cell keeps 120.
      const said = failureSentence(snapshot.text);
      const missing = notFound(workflow.expect, snapshot.text);
      return {
        cause: 'expectation-not-met',
        detail: said
          ? `${why} The page shows an error rather than what was expected. It says: "${said}" ${missing}`
          : `${missing} ${observed(snapshot.text, 'start')} ${why}`,
        taken,
      };
    }
    default: {
      // An expectation nothing could ever match is named, rather than left for
      // the reader to infer from an unverified row. The advice below is true
      // when the page is the hard part and actively misleading when the
      // expectation is: it sends somebody to look at a page that may be showing
      // exactly what was asked for.
      const blind = unmatchable(workflow.expect);
      const unreadable =
        `Nothing on the page contradicts what was expected, and nothing confirms it either, so ` +
        `this run proved nothing.`;
      return {
        // page-unreadable, not synthesized-response. This branch is about a
        // page nobody could read; the other name belongs to a response a
        // model invented, which the proxy knows about and the runner cannot
        // see. Wearing it here left the real case with a mapping and no
        // producer for as long as synth has existed.
        cause: 'page-unreadable',
        // AN EXPECTATION THAT COULD NEVER MATCH GOES FIRST, AND THE QUOTED
        // NAME GOES FIRST WITHIN IT.
        //
        // Two reasons, and the second is the one that decides it. The report's
        // table cell is capped at 120 characters by oneLine in the engine's
        // report package, and `why` alone is 183, so anything after it reaches
        // no one: it survives only on the `Got:` line inside a collapsed
        // details block, which is where somebody looks once they already
        // suspect something. That is the ergonomic half.
        //
        // The half that decides it is that `why` is WRONG here. It says
        // nothing on this page moves the workflow forward, about a page that
        // may be showing exactly what was asked for, so leading with it points
        // the reader at their application when the fault is in their
        // expectation. A diagnostic that is merely incomplete costs a click; a
        // diagnostic that points the wrong way costs the hour.
        //
        // The name leads the sentence rather than closing it for the same
        // reason: a sentence that leads and then truncates before naming which
        // expectation tells somebody there is a problem and not what it is,
        // which is worse than the folded version it replaces. Quoted names
        // first, mechanism second, what to do third.
        //
        // Only when there is such an expectation. One that was not met is a
        // different fact from one that could never be met, and only the second
        // earns the front of the cell.
        detail: blind.length > 0
          ? `${blind.map((e) => JSON.stringify(e)).join(', ')} could never match any page, so ` +
            `this workflow can neither pass nor fail. ` +
            `${blind.length === 1 ? 'It carries' : 'They carry'} no word this can look for. ` +
            `Quote a string to require it exactly, or write words of three letters or more. ` +
            `${why} ${unreadable}`
          : `${why} ${unreadable} Set a model key so the runner can read the page, or write an ` +
            `expectation whose words appear on it.`,
        taken,
      };
    }
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
