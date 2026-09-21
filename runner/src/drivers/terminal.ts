// The terminal surface driver: drives a command line program the way the web
// driver drives a browser.
//
// A terminal's rendered text is its accessibility tree, so the same workflow
// model transfers: send input, read what the program rendered, and judge it
// against the words a person would look for. The program's output IS the live
// cast, which is why a terminal agent needs no frame pump: its steps are the
// stream, rendered directly by `af watch` and the console.
//
// TWO KINDS OF PROGRAM, AND WHY BOTH PATHS ARE HERE. A program that reads a
// line and prints lines needs a pipe: it is driven by writing to standard
// input and judged on everything it wrote. A program that takes over the
// screen needs a pseudo terminal: it will not start at all without one, it
// reads raw keystrokes rather than lines, and what it "wrote" is a stream of
// cursor moves whose only meaning is the grid of cells they leave behind.
//
// The manifest picks by saying whether the program has a screen and how big it
// is. That is not a flag, it is the fact that decides everything else, and it
// is the one thing an author already knows about their own program.
//
// THE REASON THEY ARE NOT ONE PATH. A pseudo terminal has a line discipline,
// and a line discipline ECHOES what is typed into it. Run a line oriented
// program under a pty and every character the driver sends appears on the
// screen before the program has done anything, so `expect: ["Welcome aboard"]`
// would be satisfied by a workflow that TYPED "Welcome aboard" and by a
// program that printed nothing. That is a check that cannot say no, wearing
// the costume of one that can. Keeping the pipe path for programs that do not
// draw keeps their evidence the program's own output and nothing else.

import { spawn } from 'node:child_process';
import { failureSentence, judgeAll, notFound, observed } from '../workflow.ts';
import { classify, type Attempt, type Cause } from '../verdict.ts';
import { nullSink, type LiveSink } from '../live.ts';
import { openScreen, type Screen } from './screen.ts';
import { encodeKeys, describeEntry } from './keys.ts';
import type { WorkflowResult } from '../execute.ts';

/** How big the program's screen is. Its presence is what says the program
 *  draws one, so it is what selects the pseudo terminal. */
export interface TerminalScreen {
  readonly rows: number;
  readonly cols: number;
}

/** A terminal workflow: a program to run, what to type at it, and the words its
 *  output must show for the workflow to have passed. */
export interface TerminalWorkflow {
  readonly name: string;
  readonly command: string;
  readonly args?: readonly string[];
  /** input is what a person types, in order. Without a screen each entry is a
   *  line written to standard input. With one, each entry is keystrokes: text
   *  is typed as written and `<enter>`, `<down>`, `<ctrl-c>` and the rest
   *  become the bytes a real keyboard sends. See keys.ts. */
  readonly input?: readonly string[];
  /** expect are the strings that must all appear in what the program showed. */
  readonly expect: readonly string[];
  /** screen present means the program draws a full screen and is driven
   *  through a pseudo terminal of this size. Absent means it is line oriented
   *  and is driven through a pipe. */
  readonly screen?: TerminalScreen;
  /** cwd overrides the job's working directory for this workflow. */
  readonly cwd?: string;
  /** maxMs bounds the run; the program is killed past it and the workflow is
   *  blocked rather than judged, the same rule the web driver follows. */
  readonly maxMs?: number;
}

/** What one terminal run needs. */
export interface TerminalJob {
  readonly workflows: readonly TerminalWorkflow[];
  readonly cwd?: string;
  readonly live?: LiveSink;
  /** env is added to the environment every program is started with. The engine
   *  puts the running environment's address here, so a command line tool under
   *  test talks to the rehearsal environment rather than to nothing. */
  readonly env?: Readonly<Record<string, string>>;
}

const DEFAULT_MAX_MS = 30_000;

/** How long the driver waits for the screen to stop changing before it decides
 *  a redraw is finished. Quiet rather than a fixed sleep: a program that
 *  redraws in one millisecond is not waited on for two hundred, and one that
 *  takes two hundred is not read half drawn. */
const QUIET_MS = 120;

/** The ceiling on one settle, for a program that never goes quiet: a clock, a
 *  progress spinner, an animation. Past it the screen is read as it stands,
 *  which is what a person watching would do. */
const SETTLE_CEILING_MS = 3_000;

/** How long a key is given to produce ANY output before the driver accepts
 *  that the program ignored it.
 *
 *  THE BUG THIS NUMBER EXISTS FOR, found by driving a real menu. Waiting only
 *  for quiet is waiting for a silence that is already there: nothing has
 *  arrived since the key was sent because the key was sent a microsecond ago,
 *  so the quiet test passes immediately and the screen is read BEFORE the
 *  program has redrawn. Every keystroke then appeared to do nothing, the
 *  report recorded the same opening screen four times over, and the driver
 *  looked like it was working. So a settle waits for the program to answer
 *  first and only then waits for it to stop.
 *
 *  A SECOND AND SMALLER, measured rather than chosen: at four hundred
 *  milliseconds this window was itself the flake. Sixteen node processes on a
 *  loaded machine put a keystroke's redraw past it, the driver captured the
 *  screen from BEFORE that key, and the run recorded three screens where four
 *  were drawn. A program is given a full second to answer a key, which costs a
 *  second only for a key the program truly ignores and costs nothing at all
 *  for one it handles, because the wait ends the moment the first byte of the
 *  redraw arrives. */
const KEY_RESPONSE_MS = 1_000;

/** runTerminal drives every terminal workflow and returns a result for each,
 *  in the same shape the web driver produces so the counting and the report do
 *  not care which surface a run used. */
export async function runTerminal(job: TerminalJob): Promise<WorkflowResult[]> {
  const sink = job.live ?? nullSink();
  for (const workflow of job.workflows) {
    sink.agent({ id: workflow.name, workflow: workflow.name, surface: 'terminal' }, 'pending');
  }
  const results: WorkflowResult[] = [];
  for (const workflow of job.workflows) {
    results.push(await runOneTerminal(workflow, job, sink));
  }
  return results;
}

/** What driving a program produced, before it is classified. `output` is
 *  everything the program showed, which is the raw stream for a line oriented
 *  program and the rendered screens for one that draws. */
interface Outcome {
  readonly cause: Cause;
  readonly detail: string;
  readonly output: string;
}

async function runOneTerminal(
  workflow: TerminalWorkflow, job: TerminalJob, sink: LiveSink,
): Promise<WorkflowResult> {
  const started = Date.now();
  const desc = { id: workflow.name, workflow: workflow.name, surface: 'terminal' as const };
  const steps: string[] = [];
  const record = (text: string, action?: string) => {
    steps.push(text);
    sink.step(desc.id, { text, ...(action ? { action } : {}) });
  };

  sink.agent(desc, 'connecting');
  const invocation = [workflow.command, ...(workflow.args ?? [])].join(' ');
  record(`Run ${invocation}`, 'spawn');

  const cwd = workflow.cwd ?? job.cwd;
  const outcome = workflow.screen
    ? await driveOnAScreen(workflow, workflow.screen, cwd, job.env, sink, desc, record)
    : await driveThroughAPipe(workflow, cwd, job.env, sink, desc, record);

  const attempt: Attempt = {
    cause: outcome.cause,
    detail: outcome.detail,
    durationMs: Date.now() - started,
  };
  const classified = classify([attempt]);
  sink.agent(desc, 'ended', classified.verdict);
  return {
    workflow: workflow.name,
    // The reproduction is filled in here for the same reason the web driver
    // fills in its own: classify() cannot write one, because what a person
    // would have to DO to see this again is a fact about the surface. A
    // terminal workflow that reached here with the empty list classify
    // returns produced a failing verdict with nothing under it, and the
    // report's "how to see this yourself" block is skipped entirely when the
    // list is empty, so the reader of a failed check was shown the verdict
    // and no way to reach it.
    outcome: { ...classified, reproduction: reproduction(workflow, invocation, classified) },
    steps,
    evidence: { console: [], failed: [] },
    durationMs: Date.now() - started,
    startedAt: new Date(started).toISOString(),
    finishedAt: new Date().toISOString(),
  };
}

/** reproduction turns the workflow into steps a person can follow at their own
 *  terminal, in the same shape the web driver produces: the invocation, what
 *  was typed, what was expected and what happened instead.
 *
 *  The RENDERED SCREENS are deliberately not in here. A grid of cells is a
 *  picture rather than an instruction, and the markdown report writes one
 *  reproduction entry per line, where a run of spaces is collapsed and the
 *  columns that make a screen readable stop lining up. The screens are in the
 *  result's steps, which is where the live cast reads them and where the JSON
 *  report keeps them whole. */
function reproduction(
  workflow: TerminalWorkflow, invocation: string, outcome: { verdict: string; detail: string },
): string[] {
  if (outcome.verdict === 'pass') return [];
  const how = workflow.screen
    ? `Run ${invocation} on a ${workflow.screen.rows} by ${workflow.screen.cols} terminal`
    : `Run ${invocation}`;
  const typed = (workflow.input ?? []).map((entry, i) => `${i + 2}. ${describeEntry(entry)}`);
  return [
    'Bring the environment up with af up, then follow these:',
    `1. ${how}`,
    ...typed,
    `Expected: ${workflow.expect.join(' ')}`,
    `Got: ${outcome.detail}`,
  ];
}

/** The pipe path, for a program that reads lines and prints lines. Unchanged
 *  in what it judges: the program's own output and nothing the driver typed. */
function driveThroughAPipe(
  workflow: TerminalWorkflow,
  cwd: string | undefined,
  env: Readonly<Record<string, string>> | undefined,
  sink: LiveSink,
  desc: { id: string; workflow: string; surface: 'terminal' },
  record: (text: string, action?: string) => void,
): Promise<Outcome> {
  return new Promise<Outcome>((resolve) => {
    let child;
    try {
      child = spawn(workflow.command, [...(workflow.args ?? [])], {
        ...(cwd ? { cwd } : {}),
        env: { ...process.env, ...env },
        stdio: ['pipe', 'pipe', 'pipe'],
      });
    } catch (err) {
      // The program could not be started. That is the runner's own failure,
      // not evidence about the application, so it is blocked.
      resolve({
        cause: 'runner-failure',
        detail: err instanceof Error ? err.message : String(err),
        output: '',
      });
      return;
    }
    sink.agent(desc, 'live');

    let output = '';
    let carry = '';
    // Each finished output line is a step, which is what makes the terminal
    // cast live: a watcher sees the program's output as it prints.
    const onChunk = (chunk: Buffer) => {
      const text = chunk.toString('utf8');
      output += text;
      carry += text;
      const lines = carry.split('\n');
      carry = lines.pop() ?? '';
      for (const line of lines) {
        if (line.trim()) record(line, 'output');
      }
    };
    child.stdout?.on('data', onChunk);
    child.stderr?.on('data', onChunk);

    const timer = setTimeout(() => {
      child.kill('SIGKILL');
      resolve({
        cause: 'budget-exhausted',
        detail: `The program was still running after its budget of ${workflow.maxMs ?? DEFAULT_MAX_MS} ms and was stopped.`,
        output,
      });
    }, workflow.maxMs ?? DEFAULT_MAX_MS);
    timer.unref?.();

    child.on('error', (err) => {
      clearTimeout(timer);
      resolve({ cause: 'runner-failure', detail: err.message, output });
    });
    child.on('close', (code) => {
      clearTimeout(timer);
      if (carry.trim()) record(carry, 'output');
      // The same three way check the web driver uses, with the exit code
      // playing the part HTTP status plays there: a nonzero exit is a program
      // that failed, which outranks an unread expectation, exactly as a page
      // answering 500 does in finalJudgement. Met always wins: a program that
      // showed what was expected passed however it exited.
      const verdict = judgeAll(workflow.expect, output);
      if (verdict === 'met') {
        resolve({ cause: 'succeeded', detail: 'Every expectation appeared in the output.', output });
      } else if (code !== 0) {
        resolve({
          cause: 'application-error',
          detail: `The program exited ${code} and the output did not show what was expected.`,
          output,
        });
      } else if (verdict === 'unmet') {
        resolve({ cause: 'expectation-not-met', detail: unmetDetail(workflow.expect, output, 'output', output), output });
      } else {
        resolve({
          cause: 'page-unreadable',
          detail: 'Nothing in the output confirmed or contradicted what was expected.',
          output,
        });
      }
    });

    // Type the input, then close stdin so a program waiting on end of input
    // proceeds. Written after the handlers are attached so no early output is
    // missed.
    for (const line of workflow.input ?? []) {
      child.stdin?.write(line + '\n');
      record(`Type ${JSON.stringify(line)}`, 'input');
    }
    child.stdin?.end();
  });
}

/** The pseudo terminal path, for a program that draws a screen.
 *
 * THE SHAPE OF ONE STEP, and why it is not a sleep. A key is sent, the program
 * redraws, and the redraw is not instant: a curses program answers a keypress
 * with a burst of cursor moves and erases that arrives over several
 * milliseconds. Reading the grid too early reads the screen the program was
 * showing BEFORE the key, which produces an expectation that fails about a
 * screen the program really did draw. So after every key this waits for the
 * program to go quiet, with a ceiling for a program that never does, and only
 * then reads the grid.
 *
 * WHAT IT JUDGES. Every screen the program showed, in order, plus whatever is
 * left in the scrollback at the end. A TUI overwrites itself, so the menu that
 * was on screen before Enter was pressed exists nowhere afterwards; keeping
 * each screen as it was is what lets a workflow say "the list showed Drafts,
 * and after Enter the editor showed the draft" and have both halves be
 * checkable.
 *
 * WHEN THE PROGRAM DOES NOT EXIT. A full screen program is not supposed to.
 * The workflow is over when its keys have been sent and the screen has
 * settled, so the driver judges what it sees and then stops the program. That
 * is a completed workflow, not an exhausted budget: the budget is only spent
 * when the clock runs out with keys still to send. Reporting a TUI as blocked
 * for failing to exit would make every honest pass unreachable. */
async function driveOnAScreen(
  workflow: TerminalWorkflow,
  size: TerminalScreen,
  cwd: string | undefined,
  env: Readonly<Record<string, string>> | undefined,
  sink: LiveSink,
  desc: { id: string; workflow: string; surface: 'terminal' },
  record: (text: string, action?: string) => void,
): Promise<Outcome> {
  const budgetMs = workflow.maxMs ?? DEFAULT_MAX_MS;
  const deadline = Date.now() + budgetMs;

  let screen: Screen;
  try {
    screen = await openScreen(size.rows, size.cols);
  } catch (err) {
    return notOurs('the terminal emulator could not be loaded', err);
  }

  let child;
  try {
    const pty = await import('@lydell/node-pty');
    child = pty.spawn(workflow.command, [...(workflow.args ?? [])], {
      name: 'xterm-256color',
      cols: size.cols,
      rows: size.rows,
      ...(cwd ? { cwd } : {}),
      // TERM is what tells the program a terminal of this kind is present at
      // all. Without it a curses program either refuses to start or falls back
      // to a dumb mode and draws nothing, and the screen would be empty for a
      // reason that has nothing to do with the application.
      env: { ...process.env, ...env, TERM: 'xterm-256color' } as Record<string, string>,
    });
  } catch (err) {
    screen.dispose();
    // A pseudo terminal is the one part of this the runner cannot provide
    // itself. A platform with no prebuilt binding says so here, by name, and
    // the workflow is blocked rather than failed: nothing was learned about
    // the program.
    return notOurs('a pseudo terminal could not be opened', err);
  }

  // The emulator parses asynchronously, so the writes are chained rather than
  // fired: two chunks parsed out of order would render a screen the program
  // never drew. Awaiting the chain is what makes a snapshot mean "everything
  // received so far has been drawn".
  let parsed: Promise<void> = Promise.resolve();
  let lastDataAt = Date.now();
  let exited: { code: number; signal: number | undefined } | undefined;
  child.onData((data: string) => {
    lastDataAt = Date.now();
    parsed = parsed.then(() => screen.write(data));
  });
  child.onExit((e: { exitCode: number; signal?: number }) => {
    exited = { code: e.exitCode, signal: e.signal };
  });
  sink.agent(desc, 'live');

  const shown: string[] = [];
  let lastRecorded = '';
  const capture = () => {
    const now = screen.screen();
    shown.push(now);
    // Only a screen that CHANGED and has something on it is worth a step. A
    // program that ignores a key would otherwise fill the report with the same
    // grid ten times over, and a program that has restored the terminal on its
    // way out leaves a blank one that is true and is not evidence.
    if (now !== lastRecorded && now.trim() !== '') {
      record(now, 'screen');
      lastRecorded = now;
    }
  };
  // `responseMs` is how long the program is given to say anything at all
  // before the driver accepts that it had nothing to say. Starting up is given
  // the whole ceiling, because a program that has not printed its first screen
  // yet has not started; a keystroke is given far less, because a key a
  // program ignores must not cost the ceiling.
  // `drawn` waits until everything RECEIVED so far has been parsed, which is
  // not what awaiting the chain once does. The chain grows while it is
  // awaited, because the program keeps writing during the await and every
  // chunk appends another link, so a single `await parsed` proves only that
  // the chunks queued at the instant of the call are on the grid. Awaiting
  // until the chain stops changing is the fixed point that makes a snapshot
  // mean what the comment above claims it means.
  const drawn = async () => {
    for (;;) {
      const chain = parsed;
      await chain;
      if (parsed === chain) return;
    }
  };
  const settle = async (since: number, responseMs: number) => {
    const ceiling = Date.now() + SETTLE_CEILING_MS;
    for (;;) {
      await drawn();
      if (exited) return;
      if (Date.now() >= ceiling || Date.now() >= deadline) return;
      const answered = lastDataAt > since;
      if (!answered) {
        if (Date.now() - since >= responseMs) return;
      } else if (Date.now() - lastDataAt >= QUIET_MS) {
        return;
      }
      await sleep(10);
    }
  };

  await settle(Date.now(), SETTLE_CEILING_MS);
  capture();

  let ranOutOfTime = false;
  for (const entry of workflow.input ?? []) {
    if (Date.now() >= deadline) {
      ranOutOfTime = true;
      break;
    }
    if (exited) {
      // The program is gone and there are keys left. Sending them would throw
      // on a closed pseudo terminal, and the workflow's remaining steps never
      // happened, which the detail below says out loud.
      ranOutOfTime = false;
      record(`The program exited before ${describeEntry(entry).toLowerCase()}`, 'note');
      break;
    }
    record(describeEntry(entry), 'input');
    // The cursor key encoding is read again for every key, because the program
    // decides it and decides it while it is starting: reading it once before
    // the first redraw would send the wrong bytes for every arrow afterwards.
    const sentAt = Date.now();
    child.write(encodeKeys(entry, screen.cursorKeys()));
    await settle(sentAt, KEY_RESPONSE_MS);
    capture();
  }

  if (!exited && !ranOutOfTime) {
    // The last key may have been the one that quits. Give the program the
    // moment it needs to actually go, so a workflow that ends by quitting
    // reports the exit code it chose rather than the signal we sent it.
    await waitForExit(() => exited !== undefined, Math.min(600, Math.max(0, deadline - Date.now())));
  }

  // The program's last bytes are what the expectation is usually about, and
  // waiting for the exit above waits for the PROCESS rather than for the
  // emulator. Draining here is what stops the transcript being judged with
  // the final redraw still queued.
  await drawn();
  const everything = screen.everything();
  const transcript = [...shown, everything].join('\n');
  capture();

  if (!exited) child.kill();
  screen.dispose();

  const verdict = judgeAll(workflow.expect, transcript);
  const drew = size.rows + ' by ' + size.cols;
  if (verdict === 'met') {
    return {
      cause: 'succeeded',
      detail: `Every expectation appeared on the ${drew} screen the program drew.`,
      output: transcript,
    };
  }
  if (ranOutOfTime) {
    return {
      cause: 'budget-exhausted',
      detail: `The budget of ${budgetMs} ms ran out with keys still to send, so the workflow never finished.`,
      output: transcript,
    };
  }
  if (exited && exited.code !== 0) {
    return {
      cause: 'application-error',
      detail: `The program exited ${exited.code} and the screen did not show what was expected.`,
      output: transcript,
    };
  }
  if (verdict === 'unmet') {
    return {
      cause: 'expectation-not-met',
      // The last screen that had anything on it, not the transcript, which
      // repeats every frame drawn on the way there, and not the final grid,
      // which a program that restored the terminal on its way out left blank.
      detail: unmetDetail(workflow.expect, transcript, 'screen',
        [...shown, everything].reverse().find((s) => s.trim() !== '') ?? ''),
      output: transcript,
    };
  }
  return {
    cause: 'page-unreadable',
    detail: 'Nothing on the screen confirmed or contradicted what was expected.',
    output: transcript,
  };
}

/** unmetDetail explains an unmet expectation without inventing an error.
 *
 * This said the output or the screen showed a failure every time, including
 * for a program drawing its whole healthy menu with one quoted string absent,
 * which sent the reader looking for a failure nobody could find. A failure is
 * named only when `failureSentence` found one in what was judged, and quoted;
 * otherwise the detail names what was missing and ends with the last thing the
 * program showed, because a terminal's final screen is where its answer is. */
function unmetDetail(
  expect: readonly string[], judged: string, where: 'output' | 'screen', last: string,
): string {
  const said = failureSentence(judged);
  const missing = notFound(expect, judged);
  return said
    ? `The ${where} showed a failure rather than what was expected. It says: "${said}" ${missing}`
    : `${missing} ${observed(last, 'end')}`;
}

/** notOurs is the driver's own failure, which is blocked rather than failed:
 *  nothing was learned about the program, and an environment we could not
 *  provide must never read as a fault in the thing under test. */
function notOurs(what: string, err: unknown): Outcome {
  return {
    cause: 'runner-failure',
    detail: `${what}: ${err instanceof Error ? err.message : String(err)}`,
    output: '',
  };
}

/** sleep does NOT unreference its timer, and that is deliberate. A pseudo
 *  terminal's own handle is not enough to hold the event loop open between two
 *  settle polls, so an unreferenced timer lets node decide the program has
 *  nothing left to do and exit in the middle of a workflow. The symptom is an
 *  unsettled promise and a run that produces no result at all. */
function sleep(ms: number): Promise<void> {
  return new Promise((resolve) => {
    setTimeout(resolve, ms);
  });
}

/** waitForExit polls rather than sleeping the whole grace period, so a program
 *  that quits at once is not waited on for the ceiling. */
async function waitForExit(done: () => boolean, withinMs: number): Promise<void> {
  const until = Date.now() + withinMs;
  while (!done() && Date.now() < until) await sleep(10);
}
