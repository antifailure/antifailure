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
import { failureSentence, firstShown, judgeAll, meetsAll, notFound, observed } from '../workflow.ts';
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
  /** never are the strings the program must not show at any point, matched
   *  character for character. Declaring any changes how long the driver
   *  watches: a met expectation is no longer the end, because a program can
   *  print what was expected and then contradict it, so a program that has not
   *  exited is watched until it does or until its budget is spent. */
  readonly never?: readonly string[];
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
 *  takes two hundred is not read half drawn.
 *
 *  Exported because a silence shorter than this is not a silence the driver can
 *  see, so a test that has to make the driver observe one derives its pause
 *  from this rather than from a number of its own. */
export const QUIET_MS = 120;

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

/** How long a program that has been sent its last key must stay SILENT before
 *  the driver accepts that it has nothing more to say.
 *
 *  THIS IS A WINDOW OF SILENCE AND NOT A BUDGET FOR OUTPUT, and confusing the
 *  two is the defect it used to be. The same number was once the whole wait:
 *  the driver gave the program six hundred milliseconds to exit and then read
 *  the transcript however much of its output was still arriving. A program that
 *  answers a key, falls quiet and only THEN prints was therefore judged on the
 *  fraction that had made it, and the report said the expectation was not met,
 *  which is a different fact from "I did not wait for the rest of it".
 *  Reproduced on a 16GB eight core Mac at load average 90: the transcript was
 *  read at line 3892 of 20000 and the run reported fail, three times in eight
 *  runs, on a tree byte for byte identical to one that had passed.
 *
 *  So the wait now ends on a fact about the program, an exit or a silence this
 *  long, and never on a clock that ran while the program was talking.
 *
 *  WHY IT IS FIVE TIMES QUIET_MS AND NOT QUIET_MS. A redraw's quiet window has
 *  to be short, because every keystroke pays it. This one is paid once per
 *  workflow, and it answers a harder question: whether the program has FINISHED
 *  rather than whether one redraw has. Measured at QUIET_MS, a burst that was
 *  descheduled mid flight on a loaded machine went silent for longer than a
 *  redraw takes, the driver read that as the end, and the transcript was judged
 *  at line 523 of 20000. Raising the bar to a silence no descheduled writer is
 *  likely to reach is what closed that, and the residual was stated where the
 *  test that covers it lives.
 *
 *  LIKELY WAS THE LOAD BEARING WORD, AND IT HAS NOW BEEN MEASURED. A silence of
 *  this length is reached by a descheduled writer on a loaded machine, and 600 ms
 *  is not a bar that stops one: `runner`, a required check, failed once in 333
 *  observations, on pull requests that had not touched the runner. Induced at one
 *  position with only the length of the silence varying, 300 ms and 500 ms never
 *  failed, 700 ms failed 2 runs in 3 and 1000 ms failed 3 in 3, so the transition
 *  sits exactly on this constant. Raising it again would only move that
 *  transition, which is why `lastWord` no longer accepts a silence on its own: it
 *  looks at the SCREEN, and the residual that remains is stated there.
 *
 *  So what this number means has narrowed. It is how long a program must be quiet
 *  before the driver LOOKS, and no longer how long before it CONCLUDES.
 *
 *  runner/test/drivers.test.ts derives the timing of its late burst from this
 *  and from QUIET_MS, so the test covering this boundary cannot quietly lose
 *  its power when either number moves. */
export const EXIT_GRACE_MS = 600;

/** How the wait for a program's last word ended.
 *
 *  `exited` and `quiet` are both a program that has finished talking, and they
 *  are the only two states in which the transcript means what it says.
 *  `still-writing` is the budget running out with output still arriving, which
 *  is not a verdict about the program at all and must never be reported as
 *  one. */
type LastWord = 'exited' | 'quiet' | 'still-writing';

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
    ...(workflow.never?.length ? [`Must never show: ${workflow.never.join(' ')}`] : []),
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
      // A contradiction already printed is a fact however the run ends, and
      // nothing the program could have printed afterwards would take it back.
      const forbidden = firstShown(workflow.never, output);
      if (forbidden !== undefined) {
        resolve({ cause: 'expectation-not-met', detail: shownDetail(forbidden, 'output'), output });
        return;
      }
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
      //
      // Something the program must never show outranks all of it, met
      // included: a program that printed what was expected and then the error
      // it promised never to print did not do what the workflow says.
      const forbidden = firstShown(workflow.never, output);
      if (forbidden !== undefined) {
        resolve({ cause: 'expectation-not-met', detail: shownDetail(forbidden, 'output'), output });
        return;
      }
      const verdict = judgeAll(workflow.expect, output);
      if (verdict === 'met') {
        resolve({
          cause: 'succeeded',
          detail: workflow.never?.length
            ? 'Every expectation appeared in the output, and nothing it must never show did.'
            : 'Every expectation appeared in the output.',
          output,
        });
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
  // Whether the workflow declared what must never show. See WHAT `never`
  // CHANGES below for everything this decides.
  const watching = (workflow.never?.length ?? 0) > 0;

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

  // The emulator parses asynchronously, so its writes are queued rather than
  // fired: two chunks parsed out of order would render a screen the program
  // never drew. Awaiting the queue is what makes a snapshot mean "everything
  // received so far has been drawn". See ParseQueue for why the queue hands the
  // emulator whatever has accumulated rather than one write per chunk.
  //
  // `received` and the queue's `parsedBytes` are what let the driver say HOW FAR
  // BEHIND the emulator was when it had to stop reading. A driver that cannot
  // measure that has no way to report "I did not see all of it" and reports "it
  // was not there" instead.
  const queue = new ParseQueue((data) => screen.write(data));
  let lastDataAt = Date.now();
  let received = 0;
  // Every byte the program wrote, as written, for judging `never`. The screens
  // are snapshots, and a snapshot cannot see what was drawn and erased between
  // two of them; the bytes can, and they are complete the moment they arrive,
  // however far behind the emulator is.
  let raw = '';
  let exited: { code: number; signal: number | undefined } | undefined;
  child.onData((data: string) => {
    lastDataAt = Date.now();
    received += data.length;
    if (watching) raw += data;
    queue.push(data);
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
  // not what awaiting the queue once does. The queue grows while it is
  // awaited, because the program keeps writing during the await, and a write
  // that arrives after the queue went idle starts a new round with a new
  // `settled`, so a single await proves only that what had arrived by the
  // time that round began is on the grid. Awaiting until `settled` stops
  // changing is the fixed point that makes a snapshot mean what the comment
  // above claims it means.
  //
  // AND THE FIXED POINT IS NOT A DRAIN, which is the distinction this lost. The
  // chain holds what has been DELIVERED, so its fixed point says the emulator
  // has caught up with whatever has arrived. A program that is still writing
  // has not arrived yet, so the parser catching up with it proves only that the
  // parser is faster than the producer. Measured under load, with the bytes
  // counted on both sides: the emulator caught a burst still in flight at 41718
  // of 228906 bytes, the driver read the transcript there, and the run reported
  // the expectation unmet at line 3892 of 20000. So this is a DRAIN only once
  // nothing more can arrive, and `lastWord` is what establishes that before the
  // transcript is read.
  //
  // AND IT IS BOUNDED, because an unbounded version CANNOT TERMINATE against a
  // program that writes faster than the emulator parses. Every poll finds the
  // chain longer than it left it, the fixed point is never reached, and the
  // driver waits forever on a program that is behaving normally. That is not a
  // hypothetical: `for (;;) process.stdout.write(...)` under a ten row screen
  // hung a run for over four hundred seconds until it was killed, and a run that
  // never ends reports nothing about anything.
  //
  // The ceiling must bound the AWAIT too. A captured chain is finite, but a
  // fast writer can already have queued more than twenty seconds of parsing
  // inside a 1500 ms budget. Checking the clock only after that chain finishes
  // lets the backlog defeat the budget before the next poll is reached.
  // AN EXITED PROGRAM IS DRAINED TO THE WORKFLOW'S BUDGET, NOT TO THE CALLER'S
  // CEILING, AND NOT WITHOUT ONE. No more bytes can arrive once it has gone, so
  // its queue is finite and finishing it is the drain, which is why a settle's
  // three second ceiling does not cut it short: discarding that tail would
  // judge a partial last redraw again.
  //
  // But finite is not small. This used to take `await parsed` with no bound at
  // all, so a program that EXITED having written more than the budget could pay
  // to parse held the driver for the whole parse, at a measured 46 to 150 KB/s
  // on a loaded 16GB Mac: a 20 MB exit held a 30 second workflow for minutes,
  // past the budget its author declared. The budget now bounds it like every
  // other wait in this file, and reaching the budget with the queue unfinished
  // is reported as exactly that, `blocked` with the two byte counts, by the
  // `behind` branch below. Never as a verdict about the program, because the
  // bytes nobody parsed are precisely the ones the verdict would be about.
  const drawn = async (by: number): Promise<void> => {
    for (;;) {
      const until = exited ? deadline : by;
      const remaining = until - Date.now();
      if (remaining <= 0) return;
      const wasExited = exited !== undefined;
      const chain = queue.settled;
      let timer: ReturnType<typeof setTimeout> | undefined;
      try {
        await Promise.race([
          chain,
          new Promise<void>((resolve) => { timer = setTimeout(resolve, remaining); }),
        ]);
      } finally {
        clearTimeout(timer);
      }
      // Exit can arrive while the captured chain is being parsed. Its final
      // data may have extended the tail, so go round again against the budget
      // rather than the caller's ceiling.
      if (exited && !wasExited) continue;
      if (queue.settled === chain || Date.now() >= until) return;
    }
  };
  const settle = async (since: number, responseMs: number) => {
    const ceiling = Date.now() + SETTLE_CEILING_MS;
    for (;;) {
      await drawn(Math.min(ceiling, deadline));
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

  // THE WAIT FOR THE PROGRAM'S LAST WORD, and why it is not a fixed grace. A
  // program that is STILL WRITING has not finished, however long it has been
  // going, and reading its transcript then reports a fraction of its output as
  // the whole of it. So this ends on a FACT ABOUT THE PROGRAM rather than on a
  // number: it EXITED, which node-pty defers until the pseudo terminal has
  // closed and so until the last data event has fired, which is what makes an
  // exit a complete drain; or it went QUIET for longer than a redraw takes,
  // which is the same evidence every settle above runs on.
  //
  // Only the budget the author declared bounds it, and reaching that bound with
  // output still arriving is reported as exactly that.
  //
  // The silence is measured from the program's LAST BYTE rather than from here,
  // and that is what keeps the ordering this exists for reachable. A program
  // that answered the key and fell silent has already been quiet for QUIET_MS
  // when the key's settle returns, so it is given the REST of the window to
  // start printing, and a program that never printed at all waits no longer
  // than one that answered.
  //
  // AND A SILENCE IS NOT EVIDENCE ON ITS OWN, which is the correction this wait
  // needed and the reason it now looks at the SCREEN before it accepts one. The
  // silence above is measured on DELIVERY, so its clock counts the gap since the
  // DRIVER last read rather than the gap since the PROGRAM last wrote. Those are
  // the same number only while the program is the one deciding. A program merely
  // DESCHEDULED is silent without being finished, and accepting that silence
  // reported `expectation-not-met`, which says "your program did not print this",
  // about output nobody had waited for. That is the same defect one level over
  // from the one this file was rewritten for, and it is worse, because a flake
  // costs us time and this lies to the author about their own program.
  //
  // MEASURED, because "likely" is what the comment beside EXIT_GRACE_MS used to
  // say and 600 ms is not a bar a loaded machine respects. At one position, with
  // only the length of one induced silence varying, against the test that covers
  // this: 300 ms and 500 ms never failed, 700 ms failed 2 runs in 3, 1000 ms
  // failed 3 in 3. The transition sits exactly on the constant. On CI it was one
  // failure in 333 observations of `runner`, a required check, on pull requests
  // that had not touched the runner.
  //
  // SO THE ASYMMETRY DECIDES IT, and it is the rule the other surface already
  // follows: both planners stop the moment the expectation is met and keep going
  // otherwise, at workflow.ts's `meetsAll` and at model.ts's `judgeAll`. More
  // output can only ever turn an UNMET expectation into a MET one, because the
  // transcript accumulates and text is only ever added to it. So a met
  // expectation needs no more waiting, and an unmet one on a program that has not
  // exited means the looking is not finished. Every branch here ends on a fact:
  // the expectation being visible, the process being gone, or the budget the
  // author declared running out.
  //
  // WHAT IT COSTS, since it is not free and the cost falls in one place. A
  // workflow whose expectation never appears, against a program that never
  // exits, now spends its whole budget before reporting the failure rather than
  // ending 600 ms after the last byte. A met expectation pays nothing: the fast
  // path is unchanged. That is correctness bought with latency on the failing
  // case, which is the right way round, because the alternative spends
  // correctness to buy latency.
  //
  // THE RESIDUAL THIS LEAVES, because it does leave one and a reader deserves it
  // rather than a claim of safety. A program whose expectation is ALREADY MET and
  // which then goes quiet is accepted, so a program that would have gone on to
  // contradict itself, printing the expected words and then an error, is still
  // judged on the earlier screen. That is a deliberate limit rather than an
  // oversight: the expectation is what the author said to look for, the
  // transcript only ever grows, and waiting for a program to take something back
  // would mean never accepting any screen from a program that has not exited.
  // Closing it needs the author to say what must NOT appear, and `never` is that,
  // for the author who declares it, as WHAT `never` CHANGES below says. What is
  // no longer possible for anyone is the defect this fix is for: reporting that a
  // program did not print something when the driver had stopped listening while
  // it was descheduled.
  //
  // The screen is judged at most once per silence. `drawn` first, because a grid
  // the emulator has not caught up with would answer for output that has already
  // arrived, and `judgedFor` keeps the cost to one judgement per gap however long
  // the gap is.
  //
  // NEITHER OF THOSE TWO IS COVERED BY A TEST, and saying so is worth more than
  // pretending otherwise, because neither can change a VERDICT: they decide how
  // long it takes to reach one. Removing the `drawn` here was mutation tested and
  // SURVIVED, and the reason is worth knowing. Every settle above returns only
  // once its own `drawn` reached a fixed point, so the grid is already caught up
  // by the time this wait begins, EXCEPT when a settle ended on SETTLE_CEILING_MS
  // instead, which needs a program that writes continuously for three seconds.
  // Then, without this line, the first judgement reads a stale grid, says the
  // words are absent, and `judgedFor` stops it looking again while the program
  // stays quiet, so a workflow that PASSES waits out its whole budget first. A
  // test for that would have to make the emulator lag by an amount that is a
  // property of the host, which is the calibration this file keeps deleting, so
  // the line stays and its justification is this paragraph rather than a check.
  //
  // There is no guard here for a workflow that expects NOTHING, which would never
  // be met and so would wait out its whole budget. schemas/manifest.v1.json gives
  // a terminal workflow's `expect` minItems 1 and says why in its own words: such
  // a workflow "can only ever report that nothing confirmed or contradicted it,
  // which is blocked, so a manifest that declares one has written a workflow that
  // cannot pass". A branch for it here would be one nothing can reach, and the
  // outcome without it is the one the schema describes, reached a budget later.
  //
  // WHAT `never` CHANGES, and it is the residual above, closed for the author who
  // asks. Declaring what must NOT appear is a declaration that the program can
  // contradict itself after saying the right thing, so a met expectation stops
  // being the end of the looking: the driver keeps watching until the program
  // exits or the budget is spent. The asymmetry runs the other way for these and
  // it is just as sound. More output can only ever ADD a forbidden string, never
  // remove one, so the first time one is on screen the answer is final and the
  // wait ends there. The cost is the author's and it is stated in the schema: a
  // program that never exits, with `never` declared, is watched for its whole
  // budget, because a window shorter than that would be the same silence as
  // evidence this function was rewritten to stop believing.
  //
  // WHAT IT IS JUDGED AGAINST is not the transcript the expectations read, and
  // for two reasons a review found. Each screen is judged ON ITS OWN, because
  // the transcript joins screens with a newline and matching folds a newline
  // into a space, so a screen ending in "rollback" followed by one starting
  // with "started" would have failed a program that never showed the phrase.
  // And the BYTES are judged as well as the screens, because a screen is a
  // snapshot: a warning drawn and erased inside one redraw is on no snapshot
  // at all, and `never` promises "at any point".
  const forbiddenIn = (): string | undefined => {
    for (const text of [...shown, screen.everything(), asText(raw)]) {
      const hit = firstShown(workflow.never, text);
      if (hit !== undefined) return hit;
    }
    return undefined;
  };
  let judgedFor = 0;
  const lastWord = async (): Promise<LastWord> => {
    for (;;) {
      if (exited) return 'exited';
      const silent = Date.now() - lastDataAt >= EXIT_GRACE_MS;
      if (Date.now() >= deadline) return silent ? 'quiet' : 'still-writing';
      if (silent && judgedFor !== lastDataAt) {
        judgedFor = lastDataAt;
        await drawn(deadline);
        if (forbiddenIn() !== undefined) return 'quiet';
        if (!watching && meetsAll(workflow.expect, [...shown, screen.everything()].join('\n'))) return 'quiet';
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
    // On Windows the protocol decides instead, because ConPTY hides the mode
    // and encodes a key event for the program itself; see win32KeyPress.
    const sentAt = Date.now();
    child.write(encodeKeys(entry, screen.cursorKeys(), screen.keyProtocol()));
    await settle(sentAt, KEY_RESPONSE_MS);
    capture();
  }

  let stillWriting = false;
  if (!exited && !ranOutOfTime) {
    // The last key may have been the one that quits. Give the program the
    // moment it needs to actually go, so a workflow that ends by quitting
    // reports the exit code it chose rather than the signal we sent it.
    stillWriting = await lastWord() === 'still-writing';
  }

  // The program's last bytes are what the expectation is usually about, and
  // waiting for its last word above waits for the PROCESS rather than for the
  // emulator. Draining here is what stops the transcript being judged with
  // the final redraw still queued.
  //
  // It is SKIPPED when the budget ran out with output or keys still pending,
  // and that is not a shortcut. Such a program can have an unbounded backlog behind
  // it, so parsing the backlog would cost the budget again and still not buy the
  // program's last word. What it bought instead is the measurement below.
  if (!stillWriting && !ranOutOfTime) await drawn(deadline);
  const behind = received - queue.parsedBytes;
  const written = received;
  const everything = screen.everything();
  const transcript = [...shown, everything].join('\n');
  capture();
  const forbidden = forbiddenIn();

  // Everything still queued is dropped from here, so awaiting the queue waits
  // only for the one batch already inside the emulator, which MAX_BATCH keeps
  // small. That is what has to land before the emulator is disposed under it.
  queue.close();
  await queue.settled;
  // What the program did is settled here, before letting go of it. Releasing
  // kills a program still running, and on Windows it waits a moment after, so
  // the exit that kill causes would otherwise arrive in time to be read as the
  // program's own, and a program the driver stopped would be reported as one
  // that failed.
  const ended = exited;
  await release(child, ended !== undefined);
  screen.dispose();

  const drew = size.rows + ' by ' + size.cols;
  // A forbidden string on any screen the program drew, or in any byte it wrote,
  // is a failure whatever else is true, including a budget that ran out: what
  // was seen was seen.
  if (forbidden !== undefined) {
    return { cause: 'expectation-not-met', detail: shownDetail(forbidden, 'screen'), output: transcript };
  }
  const verdict = judgeAll(workflow.expect, transcript);
  if (verdict === 'met' && watching && ranOutOfTime) {
    // Met wins on its own because more output can only add to a transcript.
    // That argument does not cover what must NEVER appear, which the keys
    // nobody sent could still have produced, so a watch that did not finish
    // says so. An emulator that fell behind is NOT this case: `never` reads the
    // bytes as they arrive, so what the screen had not drawn was still read.
    return {
      cause: 'budget-exhausted',
      detail: `Every expectation appeared, but the budget of ${budgetMs} ms ran out with keys still to send, so whether the program went on to show what it must never show was not seen.`,
      output: transcript,
    };
  }
  if (verdict === 'met' && watching && rendered()) {
    // Nothing forbidden was seen, and on this terminal that is not the same
    // as nothing forbidden was shown. See `rendered` for the measurement.
    return {
      cause: 'runner-failure',
      detail: `Every expectation appeared and nothing it must never show was seen, but this terminal reports the screen as drawn rather than every byte the program wrote, so text drawn and then overwritten on the same line never reaches the driver and whether the program showed what it must never show could not be observed.`,
      output: transcript,
    };
  }
  if (verdict === 'met') {
    return {
      cause: 'succeeded',
      detail: watching
        ? `Every expectation appeared on the ${drew} screen the program drew, and nothing it must never show did${ended ? '' : ` in the ${budgetMs} ms it was watched`}.`
        : `Every expectation appeared on the ${drew} screen the program drew.`,
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
  if (stillWriting) {
    // "I could not look" and "it was not there" are different facts, and this
    // branch is the first of them. The program had neither exited nor gone
    // quiet, so the screen is a snapshot of something in progress, and saying
    // the expectation went unmet would be a claim about output nobody waited
    // for. The two byte counts are the measurement that makes it checkable
    // instead of a shrug.
    return {
      cause: 'budget-exhausted',
      detail: `The budget of ${budgetMs} ms ran out with the program still writing. It had written ${written} bytes and the ${drew} screen was ${behind} of them behind, so what it drew is not its last word.`,
      output: transcript,
    };
  }
  if (behind > 0) {
    // A quiet producer may still have a parser backlog at the deadline. Its
    // silence is not proof that the screen was fully drawn, and neither is its
    // exit: an exited program's queue is finite, not short.
    const gone = ended ? `The program exited with code ${ended.code}, but the` : 'The';
    return {
      cause: 'budget-exhausted',
      detail: `${gone} budget of ${budgetMs} ms ran out before the ${drew} screen finished drawing. It had written ${written} bytes and the screen was ${behind} of them behind.`,
      output: transcript,
    };
  }
  if (ended && ended.code !== 0) {
    return {
      cause: 'application-error',
      detail: `The program exited ${ended.code} and the screen did not show what was expected.`,
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

/** The most a single write hands the emulator. It bounds the one parse the
 *  driver still waits for after it has stopped reading, so a program that
 *  wrote megabytes cannot hold the driver past its budget through one batch.
 *  Sixteen thousand characters parse in a few milliseconds, and it is four
 *  times the most a Unix pseudo terminal delivers in one read. */
export const MAX_BATCH = 16_384;

/** ParseQueue feeds a program's output to the emulator in order, handing it
 *  everything that has accumulated rather than one write per chunk.
 *
 *  ONE WRITE PER CHUNK WAS A WINDOWS DEFECT, measured rather than guessed. The
 *  emulator resolves a write on a later timer tick, and a write that finds the
 *  emulator idle is scheduled with setTimeout. Chaining one write per chunk
 *  therefore costs one timer tick per chunk however small the chunk is. On
 *  Linux and macOS a tick is about a millisecond and a pseudo terminal hands
 *  over kilobytes at a time, so nobody noticed. On Windows a tick is the
 *  system timer's 15.6 ms and ConPTY hands over a few dozen bytes at a time,
 *  so the emulator drew about 1.8 KB a second: a ten thousand line burst that
 *  Linux draws in a second was 72723 of 108989 bytes behind after twenty, and
 *  the driver correctly reported that it could not see the program's last
 *  word. The cost of a tick is paid per batch now, and a batch is whatever
 *  arrived while the previous one was being parsed. */
export class ParseQueue {
  /** What the emulator has finished parsing, for the "how far behind" report. */
  parsedBytes = 0;
  /** Resolves once everything pushed before the current round went idle has
   *  been parsed. A push after that starts a new round with a new promise, which
   *  is how a caller tells "drained" from "drained what was there when I
   *  looked". */
  settled: Promise<void> = Promise.resolve();
  /** Chunks not yet handed over, kept as a list so taking a batch off the
   *  front of a large backlog does not copy the whole backlog each time. */
  private pending: string[] = [];
  private running = false;
  private closed = false;
  private readonly write: (data: string) => Promise<void>;

  constructor(write: (data: string) => Promise<void>) {
    this.write = write;
  }

  push(data: string): void {
    if (this.closed || data === '') return;
    this.pending.push(data);
    if (!this.running) {
      this.running = true;
      this.settled = this.pump();
    }
  }

  /** close drops everything not yet handed to the emulator. Nothing reaches
   *  the emulator once the driver has stopped reading it: a disposed emulator
   *  throws, and the backlog behind a budget that has already run out is
   *  precisely what must not be parsed. */
  close(): void {
    this.closed = true;
    this.pending = [];
  }

  private async pump(): Promise<void> {
    try {
      while (this.pending.length > 0) {
        const batch = this.take();
        await this.write(batch);
        this.parsedBytes += batch.length;
      }
    } finally {
      this.running = false;
    }
  }

  /** take removes up to MAX_BATCH characters from the front, splitting a chunk
   *  larger than that. A split can fall inside an escape sequence or between
   *  the two halves of a surrogate pair, and both are safe: the emulator is a
   *  stream parser and carries either across writes. */
  private take(): string {
    const parts: string[] = [];
    let size = 0;
    while (this.pending.length > 0 && size < MAX_BATCH) {
      const head = this.pending[0]!;
      const room = MAX_BATCH - size;
      if (head.length <= room) {
        parts.push(head);
        size += head.length;
        this.pending.shift();
      } else {
        parts.push(head.slice(0, room));
        size += room;
        this.pending[0] = head.slice(room);
      }
    }
    return parts.join('');
  }
}

/** rendered says the bytes the driver reads are a RENDERING of the screen
 *  rather than what the program wrote, which is what a terminal under Windows
 *  hands over.
 *
 *  `never` promises "at any point", and on Unix the bytes keep that promise:
 *  they are the program's own output, so a warning drawn and erased is still
 *  in them. ConPTY parses the program's output into a console buffer and sends
 *  the terminal what that buffer looks like, frame by frame. Measured on a
 *  Windows runner, fifteen trials each: a warning cleared with an erase of the
 *  screen reached the bytes 15 times in 15, and one overwritten on its own
 *  line, by a carriage return or an erase of the line, reached them 0 times in
 *  15, including when the overwrite came 300 ms after "ready". So on Windows a
 *  `never` that saw nothing has not shown that nothing was there.
 *
 *  It is the platform that decides this and not anything on the screen. Every
 *  pseudo terminal on Windows is ConPTY, and none anywhere else is: a Unix
 *  program that asks for win32-input-mode, the request ConPTY makes, still has
 *  its own bytes relayed by its pseudo terminal, so the request is evidence
 *  about which keys to send and not about what the driver can see. */
function rendered(): boolean {
  return process.platform === 'win32';
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

/** asText turns what a program wrote to a terminal into the text it put on
 *  the screen, for judging `never` against.
 *
 *  Colour and character set changes are dropped, because they do not move the
 *  cursor and the text on either side of them is contiguous on screen: "Error:
 *  " in bold followed by "deploy failed" is one phrase. Everything that MOVES
 *  the cursor or erases becomes a separator no phrase can match across: a
 *  screen clear between "rollback" and "started" puts them on two different
 *  screens, which must not read as "rollback started". A carriage return on its
 *  own moves to the start of the line to overwrite it, so it separates too; a
 *  newline is kept as one, exactly as it is on a screen. */
export function asText(raw: string): string {
  const SEP = '\u0000';
  // The order matters. An OSC, a window title, ends at a BEL, so the bare
  // control characters are removed LAST: stripping the BEL first would let the
  // title swallow every character after it.
  return raw
    .replace(/\u001b\][^\u0007\u001b]*(?:\u0007|\u001b\\)?/g, SEP)
    .replace(/\u001b\[[0-?]*[ -\/]*m/g, '')
    .replace(/\u001b[()*+][ -~]/g, '')
    .replace(/\u001b\[[0-?]*[ -\/]*[@-~]/g, SEP)
    .replace(/\u001b[\s\S]?/g, SEP)
    .replace(/\r\n/g, '\n')
    .replace(/[\r\b]/g, SEP)
    .replace(/[\u0001-\u0007\u000b\u000c\u000e-\u001f\u007f]/g, '');
}

/** shownDetail names the thing a program showed and was declared never to,
 *  in the author's own words, which is the whole of the evidence: it is a
 *  string they wrote down and the program printed. */
function shownDetail(forbidden: string, where: 'output' | 'screen'): string {
  const said = /^\s*"[\s\S]+"\s*$/.test(forbidden) ? forbidden.trim() : JSON.stringify(forbidden);
  return `The ${where} showed ${said}, which this workflow says the program must never show.`;
}

/** The part of node-pty's Windows terminal that `release` reaches into. It is
 *  not public API; the dependency is pinned to an exact version, and the
 *  Windows test job is what notices if the shape moves, because a test file
 *  whose pseudo terminals leak never exits. */
interface WindowsAgent {
  readonly _inSocket?: { destroy(): void };
  readonly _outSocket?: { destroy(): void };
}

/** release lets go of a program's pseudo terminal once the workflow is over.
 *
 *  On Unix that is a kill for a program still running, and nothing for one
 *  that has exited, whose pid may already belong to somebody else.
 *
 *  On Windows it is more, measured on a Windows runner because nothing else
 *  here could show it. node-pty keeps both of its pipes to ConPTY open after
 *  the program has gone, killed or exited, and an open pipe keeps Node's event
 *  loop alive: three handles stayed open after a kill, and after a natural
 *  exit the console host behind it stayed running as well. A test file of
 *  terminal workflows therefore never exited and was killed at its timeout,
 *  having passed every test, and a run of many terminal workflows held one
 *  console host per workflow until the runner itself exited. Killing tells
 *  ConPTY to close the console, which is safe after an exit on Windows because
 *  it names the console rather than a pid, and closing the pipes after it lets
 *  go of what node-pty keeps. The pause between them gives the close a moment
 *  to reach ConPTY over the pipe it is about to lose. */
async function release(child: { kill(): void }, exited: boolean): Promise<void> {
  if (process.platform !== 'win32') {
    if (!exited) child.kill();
    return;
  }
  try {
    child.kill();
  } catch {
    // Already closed, which is the state this is trying to reach.
  }
  await sleep(300);
  const agent = (child as unknown as { _agent?: WindowsAgent })._agent;
  agent?._inSocket?.destroy();
  agent?._outSocket?.destroy();
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
