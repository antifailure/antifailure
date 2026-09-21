// The macOS desktop driver.
//
// It drives native applications and Electron applications through their
// accessibility trees, which is the direct analogue of the browser's, and it
// reaches the SAME planner, the same three way expectation check, the same
// verdict and the same live stream a browser run reaches. That is the design
// and not an implementation detail: a second surface should cost a tree reader
// and not a second agent.
//
// The shape of it, top to bottom:
//
//   runner/src/drivers/ax.ts          an accessibility tree -> Snapshot
//   runner/src/drivers/surface.ts     what a driven surface is: look, type,
//                                     choose, press
//   runner/src/drivers/electron.ts    Chromium's tree, over Chrome DevTools
//                                     Protocol, for VS Code, Slack, Discord
//   runner/src/drivers/macax.ts       AXUIElement, over an automation host,
//                                     for anything native
//   this file                         the loop, the verdict, the live cast
//
// Nothing below the planner knows which of the two it is reading, and nothing
// above the driver knows the run was not a browser: runDesktop returns
// WorkflowResult, byte for byte the shape run() and runTerminal() produce, so
// the counting, the exit code and the report do not care which surface ran.
//
// WHAT IS NOT HERE, said plainly rather than left to be discovered. There is
// no live video frame. The browser driver pumps screenshots into the live
// stream and this does not, because the capture path on macOS is
// ScreenCaptureKit and its stop path is a known trap in this repository: a
// stop that loses the moov atom writes a file that will not play, and shipping
// a recorder that sometimes produces an unplayable artifact is worse than
// shipping none. The steps ARE the live cast here, exactly as they are for the
// terminal surface, and a watcher sees every one of them as it happens.

import {
  DeterministicPlanner, freshIdentity,
  type Action, type Planner, type Snapshot, type Workflow,
} from '../workflow.ts';
import { finalJudgement, stepsExhausted, type WorkflowResult } from '../execute.ts';
import { classify, type Attempt, type Cause } from '../verdict.ts';
import { ModelPlanner } from '../model.ts';
import { nullSink, type LiveSink } from '../live.ts';
import { NotImplementedError, type SurfaceDriver } from './driver.ts';
import { openElectron, ElectronError, type ElectronTarget } from './electron.ts';
import { openMac, AxError, type MacTarget } from './macax.ts';
import type { AxSurface } from './surface.ts';

export { NotImplementedError };
export type { AxSurface };

/** Which application to drive, and how to reach it. */
export type DesktopApp =
  | ({ readonly kind: 'electron' } & ElectronTarget)
  | ({ readonly kind: 'macos' } & MacTarget);

/** Everything one desktop run needs.
 *
 * No personas. A desktop application is not signed into over HTTP by a cookie
 * the runner can set, so the sign-in a workflow needs is a workflow: it types
 * into the fields the application shows and presses what it says. Pretending
 * otherwise, by carrying a persona field nothing reads, would be exactly the
 * dead wiring this repository keeps finding in itself.
 *
 * But an ADDRESS, yes. The reasoning above is about signing in and says
 * nothing about where the application's own backend is, and those are two
 * different questions. A desktop client of a service has to be told which
 * service to talk to, and until it was told, an Electron application under a
 * rehearsal could only reach whatever its own configuration pointed at, which
 * is either nothing or production. That is the same hazard the terminal
 * workflows were given AF_BASE_URL for, and the answer here is the same one.
 */
export interface DesktopJob {
  readonly app: DesktopApp;
  readonly workflows: readonly Workflow[];
  /** baseURL is the address of the environment this run is rehearsing. An
   *  Electron application is launched with it as AF_BASE_URL, exactly as a
   *  terminal workflow is. Absent leaves the application's environment as it
   *  was, which is what a run with no environment behind it should do. */
  readonly baseURL?: string;
  /** planner overrides the decision maker, as it does for a browser run. */
  readonly planner?: Planner;
  readonly model?: import('../model.ts').ModelConfig;
  readonly complete?: import('../model.ts').Complete;
  readonly live?: LiveSink;
  /** attempts is how many times a workflow is retried before it is called
   *  flaky or failed, the same two as a browser run. */
  readonly attempts?: number;
  /** open replaces how a surface is opened.
   *
   *  Present so the loop below is the loop a test drives rather than a copy of
   *  it. A test that drove its own reimplementation of these forty lines would
   *  prove the reimplementation correct and say nothing about what ships,
   *  which is the failure runner/src/explore.ts documents on its own Surface
   *  interface for the same reason. */
  readonly open?: (app: DesktopApp) => Promise<AxSurface>;
}

/** The most actions one attempt may take, matching the browser's own default. */
const MAX_STEPS = 40;

export const desktop: SurfaceDriver = {
  surface: 'desktop',
  available: true,
  summary:
    'Drives native macOS applications through AXUIElement and Electron applications through ' +
    "Chromium's accessibility tree, with the same planner, expectations and verdict a browser " +
    'run uses. Native needs the macOS Accessibility permission, which a person grants.',
};

/** drive is the entry a job reaches. It is runDesktop under the name the
 *  registry has always used for it. */
export async function drive(job: DesktopJob): Promise<WorkflowResult[]> {
  return runDesktop(job);
}

/** withEnvironmentAddress gives an Electron application the address of the
 *  environment it is being rehearsed against, as AF_BASE_URL.
 *
 *  Merged over the runner's own environment rather than replacing it, the way
 *  the terminal driver does it, because Playwright replaces the child's whole
 *  environment when it is handed one, and an Electron process with no PATH or
 *  HOME fails in ways that read as the application's fault. The address is
 *  written LAST so it wins over anything the developer's shell happened to
 *  export under the same name: the run's own environment is the only address
 *  a rehearsal may send an application to.
 *
 *  Native macOS applications are returned untouched. They are opened through
 *  Launch Services, which starts them with the user session's environment and
 *  not with one a caller supplies, so a variable set here would be silently
 *  dropped. Saying nothing is better than appearing to pass it. */
export function withEnvironmentAddress(app: DesktopApp, baseURL: string | undefined): DesktopApp {
  if (app.kind !== 'electron' || !baseURL) return app;
  const inherited: Record<string, string> = {};
  for (const [k, v] of Object.entries(process.env)) if (v !== undefined) inherited[k] = v;
  return { ...app, env: { ...inherited, ...(app.env ?? {}), AF_BASE_URL: baseURL } };
}

/** openFor launches the application a job named. */
async function openFor(app: DesktopApp): Promise<AxSurface> {
  if (app.kind === 'electron') return openElectron(app);
  return openMac(app);
}

/** runDesktop drives every workflow and returns a result for each, in the
 *  shape the web and terminal drivers produce. */
export async function runDesktop(job: DesktopJob): Promise<WorkflowResult[]> {
  const sink = job.live ?? nullSink();
  // The whole cast is announced first, each pending, so a watcher sees every
  // agent that is going to run before any of them starts.
  for (const workflow of job.workflows) {
    sink.agent({ id: workflow.name, workflow: workflow.name, surface: 'desktop' }, 'pending');
  }
  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    results.push(await runOneDesktop(job, workflow, sink));
  }
  return results;
}

async function runOneDesktop(
  job: DesktopJob, workflow: Workflow, sink: LiveSink,
): Promise<WorkflowResult> {
  const started = Date.now();
  const desc = { id: workflow.name, workflow: workflow.name, surface: 'desktop' as const };
  const attempts: Attempt[] = [];
  const steps: string[] = [];
  const tries = Math.max(1, job.attempts ?? 1);

  for (let attempt = 1; attempt <= tries; attempt++) {
    const at = Date.now();
    const taken: string[] = [];
    let surface: AxSurface | undefined;
    try {
      sink.agent(desc, 'connecting');
      surface = await (job.open ?? openFor)(withEnvironmentAddress(job.app, job.baseURL));
      sink.agent(desc, 'live');
      const result = await attemptOnce(job, workflow, surface, attempt, taken, (text, url, action) =>
        sink.step(desc.id, { text, ...(url ? { url } : {}), ...(action ? { action } : {}) }));
      attempts.push({ cause: result.cause, detail: result.detail, durationMs: Date.now() - at });
      steps.length = 0;
      steps.push(...taken);
      if (result.cause === 'succeeded') break;
    } catch (err) {
      // Everything that reaches here is the runner's own failure or the
      // environment's, never the application's. The two are told apart because
      // they need different remedies: a missing Accessibility grant is a
      // person ticking a box, and a crashed reader is a bug here.
      attempts.push({ cause: causeOf(err), detail: detailOf(err), durationMs: Date.now() - at });
      steps.length = 0;
      steps.push(...taken);
    } finally {
      await surface?.close().catch(() => undefined);
    }
  }

  const outcome = classify(attempts);
  sink.agent(desc, 'ended', outcome.verdict);
  return {
    workflow: workflow.name,
    outcome,
    steps,
    evidence: { console: [], failed: [] },
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}

/** causeOf charges a thrown failure to the right party.
 *
 * An AxError raised with blocked set, and every ElectronError, is the
 * environment: no Accessibility grant, no window, no such application, a
 * binary that will not start. None of those is evidence about the application
 * under test, and reporting one as a failure is how people learn to ignore
 * these results. An AxError raised with blocked clear is a control the screen
 * does not offer, which is the runner not knowing what to press, and is also
 * not the application's fault. */
function causeOf(err: unknown): Cause {
  if (err instanceof AxError) return err.blocked ? 'environment-incomplete' : 'runner-failure';
  if (err instanceof ElectronError) return 'environment-incomplete';
  return 'runner-failure';
}

function detailOf(err: unknown): string {
  return err instanceof Error ? err.message : String(err);
}

interface AttemptResult {
  readonly cause: Cause;
  readonly detail: string;
}

type EmitStep = (text: string, url?: string, action?: string) => void;

async function attemptOnce(
  job: DesktopJob, workflow: Workflow, surface: AxSurface,
  attempt: number, taken: string[], emit: EmitStep,
): Promise<AttemptResult> {
  let snapshot: Snapshot = await surface.snapshot();
  const record = (text: string, action?: string) => {
    taken.push(text);
    emit(text, snapshot.url, action);
  };
  record(`Open ${snapshot.title || snapshot.url}`, 'open');

  // A fresh identity per attempt, so a retry of a sign up is a sign up rather
  // than a duplicate the application rightly refuses.
  const deterministic = new DeterministicPlanner(
    freshIdentity(`${workflow.name}-${attempt}-${Date.now()}`),
  );
  const planner = job.planner
    ?? (job.model ? new ModelPlanner(job.model, job.complete, deterministic) : deterministic);

  const history: Action[] = [];
  const limit = workflow.maxSteps ?? MAX_STEPS;
  for (let step = 0; step < limit; step++) {
    const action = await planner.next(workflow, snapshot, history);
    history.push(action);

    switch (action.kind) {
      case 'done':
        return { cause: 'succeeded', detail: action.why };
      case 'stuck':
        return finalJudgement(workflow, snapshot, action.why, taken);
      case 'fill':
        await surface.fill(action.field, action.value);
        record(`Fill ${action.field.source.replace(/[\^$]/g, '')}: ${action.why}`, 'fill');
        break;
      case 'check':
        await surface.check(action.field);
        record(`Choose ${action.field.source.replace(/[\^$]/g, '')}: ${action.why}`, 'check');
        break;
      case 'click':
        await surface.click(action.control);
        record(`Press ${action.control.source.replace(/[\^$]/g, '')}: ${action.why}`, 'click');
        break;
      case 'goto':
        // A desktop application has no address bar. Only a model planner can
        // produce this, and answering it with a refusal rather than with a
        // silent no-op is what stops the model spending every remaining step
        // navigating to a url that will never be opened.
        return {
          cause: 'runner-failure',
          detail:
            `The plan asked to open ${action.url}. A desktop application has no address bar, ` +
            `so there is nowhere to open it. Write the workflow in terms of what is on the ` +
            `screen: the controls a screen reader announces.`,
        };
    }
    snapshot = await surface.snapshot();
  }

  return stepsExhausted(workflow, snapshot, limit, taken);
}
