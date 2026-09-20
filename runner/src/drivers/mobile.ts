// The run loop both mobile surfaces share.
//
// iOS and Android differ in three things and nothing else that matters: the
// capabilities that open a session, how an element is located, and how the
// page source spells a role. Everything after that, look at the screen, decide
// one thing, do it, look again, is the same loop the browser runs, and it is
// the same loop because it is driven by the same planner reading the same
// Snapshot.
//
// That is the point of the whole approach rather than an implementation
// detail. The planner in runner/src/workflow.ts has never heard of a phone: it
// reads a Snapshot and returns fill, check, click, done or stuck. Give it a
// Snapshot built from an accessibility tree and it drives a native app with no
// change at all, and the verdict is decided by finalJudgement, the same
// function that decides a web verdict. So a workflow sentence means the same
// thing on a web page and in an app, and the report does not have to explain
// which surface produced which kind of verdict.

import { judgeAll, DeterministicPlanner, freshIdentity, anchored,
  type Action, type Planner, type Snapshot, type Workflow } from '../workflow.ts';
import { classify, type Attempt, type Cause } from '../verdict.ts';
import { finalJudgement, stepsExhausted, type WorkflowResult } from '../execute.ts';
import { readFile } from 'node:fs/promises';
import { nullSink, type LiveSink } from '../live.ts';
import { snapshotFrom, type AxLocation, type AxNode } from './ax.ts';
import {
  WebDriverSession, WebDriverUnreachable, ping,
  type Locator,
} from './webdriver.ts';

/** What a platform has to provide for the shared loop to drive it. */
export interface MobilePlatform {
  readonly surface: 'ios' | 'android';
  /** The capabilities that open a session against this device and app. */
  capabilities(): Record<string, unknown>;
  /** Turn a page source into a normalized accessibility tree. */
  parse(source: string): AxNode;
  /** How to find the element whose accessible name is exactly this. */
  locator(name: string): Locator;
  /** What a snapshot's url and title say on this surface. */
  context(): AxLocation;
  /** Begin recording the screen, or return undefined when this platform
   *  cannot. Never throws: a recording is evidence, and failing a run because
   *  the camera broke would be reporting our own problem as the app's. */
  record(into: string): Promise<Recorder | undefined>;
  /** Put the application back to its launch state, so a workflow starts from
   *  a known screen rather than from wherever the previous one finished.
   *
   *  This is what a fresh browser context is for the web driver, and leaving
   *  it out is not a smaller version of the same thing: without it the second
   *  workflow in a run starts already signed in, its fields already filled,
   *  and the planner correctly decides there is nothing to do. The run then
   *  reports a verdict about a screen the workflow never described. Measured,
   *  not theorised: it is exactly what the first end to end run of this
   *  driver did. */
  restart(session: WebDriverSession): Promise<void>;
}

/** A screen recording in progress. */
export interface Recorder {
  /** Stop and finalize, returning the finished file, or undefined when the
   *  recording did not survive. Must never throw, for the reason above. */
  stop(): Promise<string | undefined>;
}

export interface MobileJob {
  readonly platform: MobilePlatform;
  readonly workflows: readonly Workflow[];
  /** Where the Appium server is listening. */
  readonly serverURL: string;
  /** Where evidence is written. */
  readonly artifacts: string;
  readonly live?: LiveSink;
  readonly planner?: Planner;
  /** How long session creation may take. The first iOS session on a machine
   *  builds WebDriverAgent with xcodebuild, which is minutes, not seconds. */
  readonly sessionTimeoutMs?: number;
  readonly maxSteps?: number;
}

/** The name cap for a mobile accessibility tree.
 *
 *  ax.ts defaults to 60, which mirrors the browser and is right there. A
 *  VoiceOver or TalkBack label is a sentence by design, because it is read
 *  aloud, so 60 silently drops ordinary controls on a phone: measured against
 *  a real iOS tree, a 65 character button label produced controls 0 and
 *  unnamed 0. 120 is long enough for a spoken sentence and still short enough
 *  that a paragraph which happened to carry a role does not become a control.
 */
const MOBILE_AX = { maxNameLength: 120 } as const;

/** mobileSnapshot is how a mobile tree becomes a Snapshot, and the only way.
 *
 *  A named function rather than a call spelled out at each of the two places
 *  the loop reads the screen, because the cap above is CONFIGURATION and
 *  configuration nothing exercises is configuration that can be wrong forever.
 *  With the call inlined, a test can only reach snapshotFrom directly, passing
 *  its own cap, and then the constant is never the thing under test: changing
 *  it to the browser's 60 broke nothing and no test noticed. This gives the
 *  wiring one call site a test can aim at. */
export function mobileSnapshot(root: AxNode, at: AxLocation): Snapshot {
  return snapshotFrom(root, at, MOBILE_AX);
}

const DEFAULT_MAX_STEPS = 40;
const DEFAULT_SESSION_TIMEOUT_MS = 600_000;

/** runMobile drives every workflow and returns a result for each, in the same
 *  shape the web and terminal drivers produce. */
export async function runMobile(job: MobileJob): Promise<WorkflowResult[]> {
  // A RUN WITH NOTHING TO DRIVE IS REFUSED, LOUDLY, BEFORE ANY DEVICE IS
  // TOUCHED. This is the single most dangerous line in the file and it is one
  // `if`, so it is worth saying exactly what it prevents.
  //
  // While a surface is scaffolded, main.ts sends it through assertAvailable()
  // and that THROWS, which is a correct refusal. The moment `available`
  // becomes true the throw stops, and if nothing takes its place the run falls
  // through with `results` still the empty array it was initialised to: no
  // results, no failures, and EXIT CODE ZERO, because exitCodeFor only fails a
  // run when some verdict counts against the application. A customer asks for
  // a mobile run and is told it passed, by a run that opened a simulator,
  // drove nothing, and judged nothing.
  //
  // That is strictly worse than the scaffold it replaced. A loud "not built"
  // is a true statement; a silent green is a false one, and it is the exact
  // shape of check this repository keeps finding in its own instruments: one
  // that cannot say no, wearing the costume of a test that passed.
  //
  // Thrown rather than returned as a blocked result, because there is no
  // workflow to attach a verdict to. main() reports a throw as the runner's
  // own failure and exits non zero, so it can never be read as a pass.
  if (job.workflows.length === 0) {
    throw new Error(
      `the ${job.platform.surface} surface was given no workflows to drive, so there is ` +
      `nothing to judge. A run that drives nothing is refused rather than reported as passing, ` +
      `because zero failures out of zero workflows is not evidence about the application.`,
    );
  }

  const sink = job.live ?? nullSink();
  const surface = job.platform.surface;
  for (const workflow of job.workflows) {
    sink.agent({ id: workflow.name, workflow: workflow.name, surface }, 'pending');
  }

  // Whether the tool is there at all, asked once, before anything is driven.
  // A missing Appium server is the runner's own problem and every workflow
  // must be BLOCKED by it, never failed: reporting an application broken
  // because our own tooling was not started is the precise mistake that makes
  // a report worthless.
  const reachable = await ping(job.serverURL);

  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    results.push(reachable
      ? await runOneMobile(job, workflow, sink)
      : unreachable(job, workflow, sink));
  }
  return results;
}

function unreachable(job: MobileJob, workflow: Workflow, sink: LiveSink): WorkflowResult {
  const started = Date.now();
  const desc = { id: workflow.name, workflow: workflow.name, surface: job.platform.surface };
  sink.agent(desc, 'connecting');
  const attempt: Attempt = {
    cause: 'runner-failure',
    detail:
      `No Appium server is listening at ${job.serverURL}, so the ${job.platform.surface} ` +
      `surface could not be driven at all. This is the runner's own tooling missing, not ` +
      `evidence about the application, so it is blocked rather than failed. Start one with ` +
      `"appium server --port 4723".`,
    durationMs: Date.now() - started,
  };
  const outcome = classify([attempt]);
  sink.agent(desc, 'error', outcome.verdict);
  return {
    workflow: workflow.name,
    outcome,
    steps: [],
    evidence: { console: [], failed: [] },
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}

async function runOneMobile(
  job: MobileJob, workflow: Workflow, sink: LiveSink,
): Promise<WorkflowResult> {
  const started = Date.now();
  const platform = job.platform;
  const desc = { id: workflow.name, workflow: workflow.name, surface: platform.surface };
  const steps: string[] = [];
  const record = (text: string, action?: string) => {
    steps.push(text);
    sink.step(desc.id, { text, ...(action ? { action } : {}) });
  };

  sink.agent(desc, 'connecting');
  const planner = job.planner ?? new DeterministicPlanner(freshIdentity(workflow.name));
  const limit = workflow.maxSteps ?? job.maxSteps ?? DEFAULT_MAX_STEPS;

  let session: WebDriverSession | undefined;
  let recorder: Recorder | undefined;
  let video: string | undefined;
  let outcome: { cause: Cause; detail: string };
  let lastSnapshot: Snapshot | undefined;

  try {
    session = await WebDriverSession.create(
      { serverURL: job.serverURL },
      platform.capabilities(),
      job.sessionTimeoutMs ?? DEFAULT_SESSION_TIMEOUT_MS,
    );
    sink.agent(desc, 'live');
    // Every workflow starts from the application's launch state. See the
    // restart docstring on MobilePlatform for what happens without this.
    await platform.restart(session);
    record(`Launch the ${platform.surface} application`, 'launch');

    // Started after the session exists, so the recording covers the run rather
    // than the session's own startup, and so a session that never opened does
    // not leave a recorder behind.
    recorder = await platform.record(`${job.artifacts}/${safe(workflow.name)}`);

    const history: Action[] = [];
    let stopped: { cause: Cause; detail: string } | undefined;

    for (let step = 0; step < limit; step += 1) {
      const snapshot = mobileSnapshot(platform.parse(await session.source()), platform.context());
      lastSnapshot = snapshot;

      const action = await planner.next(workflow, snapshot, history);
      history.push(action);

      if (action.kind === 'done') {
        // Judged rather than trusted. The planner says done when the words it
        // was looking for are on the screen, and finalJudgement is what
        // decides whether that is a pass, so both surfaces reach a verdict
        // through exactly one function.
        stopped = finalJudgement(workflow, snapshot, action.why, steps);
        break;
      }
      if (action.kind === 'stuck') {
        // JUDGED, not reported as our own failure, which is what the web
        // driver does at exactly this point (see the `stuck` case in
        // execute.ts) and what this originally got wrong.
        //
        // The two readings are worlds apart on the screen that matters most.
        // "The planner ran out of ideas" and "the planner ran out of ideas in
        // front of a screen saying Something went wrong" are the same action
        // and different facts, and only finalJudgement can tell them apart,
        // because only it reads the words on the screen. Calling stuck a
        // runner-failure BLOCKS the workflow, and a blocked workflow counts
        // against nothing: an application that had just broken in front of the
        // agent would be reported as the agent's own confusion, and the run
        // would exit zero. finalJudgement returns a failure when the screen
        // shows one and unverified when it genuinely proved nothing.
        stopped = finalJudgement(workflow, snapshot, action.why, steps);
        break;
      }

      const target = action.kind === 'goto' ? undefined : nameFor(action, snapshot);
      if (action.kind === 'goto') {
        // The planner only emits goto for a surface with addresses. An app has
        // none, so this is unreachable with the deterministic planner and is
        // handled rather than crashed on, because a model planner could emit
        // one and a thrown TypeError would read as the app failing.
        stopped = {
          cause: 'runner-failure',
          detail:
            `The planner asked to navigate to ${action.url}, and a ${platform.surface} ` +
            `application has no addresses to navigate to. Rewrite the workflow to reach ` +
            `the screen by pressing what a person would press.`,
        };
        break;
      }
      if (!target) {
        // The planner named something the screen does not offer. That is the
        // runner's own confusion rather than an application fault.
        stopped = {
          cause: 'runner-failure',
          detail:
            `The planner asked for ${describeAction(action)}, and nothing on the screen ` +
            `carries that accessible name. The screen offers ${offered(snapshot)}.`,
        };
        break;
      }

      const element = await session.findElement(platform.locator(target));
      if (!element) {
        // The name was in the snapshot a moment ago and the element is not
        // there now. That is a screen that changed under us, not a fault.
        stopped = {
          cause: 'runner-failure',
          detail:
            `"${target}" was on the screen when it was read and could not be found when it ` +
            `was acted on, so the screen changed underneath this step.`,
        };
        break;
      }

      if (action.kind === 'fill') {
        // Cleared first: sendKeys appends, so filling a field twice without
        // clearing produces a value nobody typed and a validation error that
        // looks like the application rejecting a correct answer.
        await session.clear(element);
        await session.sendKeys(element, action.value);
        record(`Type ${JSON.stringify(action.value)} into ${target}`, 'fill');
      } else {
        await session.click(element);
        record(`Press ${target}`, action.kind === 'check' ? 'check' : 'click');
      }
    }

    outcome = stopped ?? stepsExhausted(
      workflow,
      // Read once more rather than reusing the snapshot the last action was
      // decided from: the last thing the agent did is exactly the thing whose
      // effect has not been looked at yet, and judging without looking would
      // miss a workflow that succeeded on its final press.
      lastSnapshot = mobileSnapshot(platform.parse(await session.source()), platform.context()),
      limit,
      steps,
    );
  } catch (err) {
    outcome = {
      cause: err instanceof WebDriverUnreachable ? 'runner-failure' : 'runner-failure',
      detail: err instanceof Error ? err.message : String(err),
    };
  } finally {
    // Stopped before the session is quit: on both platforms the recorder is a
    // child process writing a file, and quitting the session can take the
    // screen away underneath it.
    if (recorder) video = await recorder.stop();
    if (session) await session.quit();
  }

  const attempt: Attempt = {
    cause: outcome.cause,
    detail: outcome.detail,
    durationMs: Date.now() - started,
  };
  const classified = classify([attempt]);
  sink.agent(desc, 'ended', classified.verdict);
  return {
    workflow: workflow.name,
    outcome: classified,
    steps,
    evidence: { console: [], failed: [], ...(video ? { video } : {}) },
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}

/** nameFor resolves the pattern the planner chose back to the exact accessible
 *  name on the screen.
 *
 *  The planner works in patterns because the browser's own locators do. A
 *  native driver locates by exact string, so the pattern is matched against
 *  the names this snapshot actually carries and the winner is what gets
 *  acted on. Doing it this way rather than translating the pattern into a
 *  platform query is what guarantees the element acted on is the element that
 *  was READ: anything else can act on a control that was never in the
 *  snapshot the decision was made from. */
function nameFor(action: Action, snapshot: Snapshot): string | undefined {
  if (action.kind === 'click') {
    return snapshot.controls.find((c) => action.control.test(c));
  }
  if (action.kind === 'fill' || action.kind === 'check') {
    return snapshot.fields.find((f) => action.field.test(f.name))?.name;
  }
  return undefined;
}

function describeAction(action: Action): string {
  if (action.kind === 'click') return `the control ${action.control.source}`;
  if (action.kind === 'fill' || action.kind === 'check') return `the field ${action.field.source}`;
  return action.kind;
}

function offered(snapshot: Snapshot): string {
  const parts: string[] = [];
  if (snapshot.fields.length > 0) {
    parts.push(`fields ${snapshot.fields.map((f) => f.name).join(', ')}`);
  }
  if (snapshot.controls.length > 0) parts.push(`controls ${snapshot.controls.join(', ')}`);
  if (snapshot.unnamed > 0) {
    parts.push(`${snapshot.unnamed} interactive element${snapshot.unnamed === 1 ? '' : 's'} with no accessible name`);
  }
  return parts.length > 0 ? parts.join(' and ') : 'nothing at all';
}

/** safe turns a workflow name into something that can be a filename. */
export function safe(name: string): string {
  return name.replace(/[^a-zA-Z0-9._-]+/g, '-').replace(/^-+|-+$/g, '') || 'workflow';
}

/** xpathLiteral quotes a string for an XPath expression.
 *
 *  XPath 1.0 has no escape character inside a string literal, so a name
 *  carrying both a single and a double quote cannot be written as one literal
 *  at all and has to be assembled with concat(). An accessible name is
 *  application text and genuinely contains apostrophes ("Don't save"), so this
 *  is a correctness requirement rather than a nicety: the naive version builds
 *  a malformed expression and the server answers with a syntax error that
 *  reads like the app is missing the control. */
export function xpathLiteral(value: string): string {
  if (!value.includes("'")) return `'${value}'`;
  if (!value.includes('"')) return `"${value}"`;
  return `concat(${value.split("'").map((part) => `'${part}'`).join(`, "'", `)})`;
}

/** isPlayable answers whether a QuickTime or MP4 file carries its index.
 *
 *  A container this shape is a sequence of boxes, each a four byte big endian
 *  length and a four byte type. The sample data lives in `mdat` and the index
 *  lives in `moov`, and a file with `mdat` and no `moov` is exactly what an
 *  interrupted recording leaves: bytes, but nothing that says where anything
 *  is. Walking the boxes rather than searching the whole file for the four
 *  characters matters, because "moov" appears inside compressed video data by
 *  chance often enough to make a substring search answer yes about a file that
 *  cannot be played. */
export async function isPlayable(path: string): Promise<boolean> {
  let bytes: Buffer;
  try {
    bytes = await readFile(path);
  } catch {
    return false;
  }
  let at = 0;
  while (at + 8 <= bytes.length) {
    let size = bytes.readUInt32BE(at);
    const type = bytes.toString('latin1', at + 4, at + 8);
    if (type === 'moov') return true;
    if (size === 1) {
      // A 64 bit size, for a box larger than four gigabytes.
      if (at + 16 > bytes.length) return false;
      const large = bytes.readBigUInt64BE(at + 8);
      if (large < 16n) return false;
      size = Number(large);
    } else if (size === 0) {
      // Extends to end of file, so there is nothing after it to find.
      return false;
    }
    if (size < 8) return false;
    at += size;
  }
  return false;
}

export { judgeAll, anchored };
