// The surface driver abstraction and the drivers that are built. Web is
// exercised by the whole browser suite and desktop by test/desktop.test.ts;
// here we prove the registry, the loud refusal of the surface that is still
// scaffolded, and the terminal driver against a real process.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:net';
import { mkdtempSync, realpathSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execPath } from 'node:process';
import {
  driverFor, surfaces, assertAvailable, NotImplementedError, type Surface, type SurfaceDriver,
} from '../src/drivers/driver.ts';
import * as ios from '../src/drivers/ios.ts';
import * as android from '../src/drivers/android.ts';
import * as desktop from '../src/drivers/desktop.ts';
import { runTerminal, EXIT_GRACE_MS, QUIET_MS } from '../src/drivers/terminal.ts';
import { socketSink, decode, type LiveEvent } from '../src/live.ts';
import type { WorkflowResult } from '../src/execute.ts';

test('the registry knows every surface and which are available', () => {
  assert.deepEqual([...surfaces()].sort(), ['android', 'desktop', 'ios', 'terminal', 'web']);
  assert.equal(driverFor('web').available, true);
  assert.equal(driverFor('terminal').available, true);
  assert.equal(driverFor('desktop').available, true);
  assert.equal(driverFor('ios').available, true);
  // android is implemented and UNPROVEN: no run has driven it, so it is
  // refused however complete its code looks.
  assert.equal(driverFor('android').available, false);
  // Every surface the type allows has a row. A surface in the union and not in
  // the registry is one driverFor answers undefined for, and assertAvailable
  // would refuse it with "unknown surface" rather than with the sentence
  // saying what building it takes.
  for (const surface of surfaces()) {
    assert.ok(driverFor(surface), `no registry row for ${surface}`);
    assert.ok(driverFor(surface).summary.length > 0, `no summary for ${surface}`);
  }
});

test('assertAvailable passes a built surface and refuses a scaffolded one loudly', () => {
  // Built surfaces do not throw.
  assertAvailable('web');
  assertAvailable('terminal');
  assertAvailable('desktop');
  assertAvailable('ios');
  // A surface nobody has driven throws, so a run that targets it fails rather
  // than returning a green verdict that tested nothing.
  for (const surface of ['android'] as Surface[]) {
    assert.throws(() => assertAvailable(surface), (err: unknown) => {
      assert.ok(err instanceof NotImplementedError);
      assert.equal((err as NotImplementedError).surface, surface);
      return true;
    });
  }
});

test('a driver module and the registry make the same claim about being driveable', () => {
  // The registry in driver.ts is what driverFor and assertAvailable read, so a
  // module whose own `available` disagrees with it is a claim nothing
  // enforces: harmless while nothing reads the module, and a surface
  // announcing itself as driven the moment anything does. android shipped in
  // exactly that state, saying true in its module and false in the registry,
  // with no run having ever driven it.
  //
  // Comparing the two is what makes the disagreement impossible rather than
  // unlikely. The modules are named here rather than discovered, because a
  // loop that found no modules would pass while checking nothing.
  const modules: Record<string, SurfaceDriver> = {
    ios: ios.ios,
    android: android.android,
    desktop: desktop.desktop,
  };
  assert.equal(Object.keys(modules).length, 3, 'a module was dropped from this comparison');
  for (const [surface, module] of Object.entries(modules)) {
    assert.equal(
      module.available,
      driverFor(surface as Surface).available,
      `${surface}.ts and the registry in driver.ts disagree about whether it can be driven. ` +
        'The registry is what the engine reads, so this module is the one that lies, and it ' +
        'lies the moment anything reads it.',
    );
    assert.equal(module.surface, surface, `${surface}.ts declares a different surface`);
  }
});

test('the scaffolded driver throws rather than silently passing', () => {
  // ios.drive() is kept so the scaffolded shape stays uniform, and it refuses
  // rather than returning an empty result: the real entry is iosPlatform()
  // with runMobile(), and anything reaching drive() has taken a path that
  // would otherwise report a green run that drove nothing.
  assert.equal(ios.ios.available, true);
  assert.throws(() => ios.drive(), NotImplementedError);
});

test('runTerminal passes when the output shows what was expected', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'greeting',
      command: execPath,
      args: ['-e', 'process.stdout.write("Welcome aboard\\n")'],
      expect: ['Welcome aboard'],
    }],
  });
  assert.equal(results.length, 1);
  assert.equal(results[0]!.outcome.verdict, 'pass');
  // The program's output line is a step, which is the terminal cast.
  assert.ok(results[0]!.steps.some((s) => s.includes('Welcome aboard')),
    `output line not recorded as a step: ${results[0]!.steps.join(' | ')}`);
});

test('runTerminal fails when the expected words are absent', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'missing',
      command: execPath,
      args: ['-e', 'process.stdout.write("something else\\n");process.exit(1)'],
      expect: ['Welcome aboard'],
    }],
  });
  // The program exited nonzero and did not show what was expected: an
  // application error, which classifies as fail, not as blocked.
  assert.equal(results[0]!.outcome.verdict, 'fail');
});

test('runTerminal types input and reads what the program did with it', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'echo',
      command: execPath,
      args: ['-e', 'process.stdin.on("data",(d)=>{process.stdout.write("got:"+d)})'],
      input: ['hello there'],
      expect: ['hello there'],
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass');
  assert.ok(results[0]!.steps.some((s) => s.startsWith('Type ')),
    'the input was not recorded as a step');
});

test('runTerminal blocks, not fails, when the program cannot start', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'nonesuch',
      command: '/nonexistent/definitely/not/a/program',
      expect: ['anything'],
    }],
  });
  // A program that could not be started is the runner's own failure, not the
  // application's: blocked, never fail.
  assert.equal(results[0]!.outcome.verdict, 'blocked');
});

function collector(): Promise<{ path: string; lines: () => LiveEvent[]; close: () => void; server: Server }> {
  const dir = mkdtempSync(join(tmpdir(), 'af-drv-'));
  const path = join(dir, 'l.sock');
  let buffer = '';
  const server = createServer((s) => { s.setEncoding('utf8'); s.on('data', (c) => { buffer += c; }); });
  return new Promise((resolve) => {
    server.listen(path, () => resolve({
      path,
      lines: () => buffer.split('\n').map(decode).filter((e): e is LiveEvent => !!e),
      close: () => server.close(),
      server,
    }));
  });
}

test('runTerminal streams the agent lifecycle and its cast to a watcher', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  await runTerminal({
    live: sink,
    workflows: [{
      name: 'streamed',
      command: execPath,
      args: ['-e', 'process.stdout.write("line one\\nline two\\n")'],
      expect: ['line two'],
    }],
  });
  await sink.close();
  // Poll until the agent has actually ended, not just until some events
  // arrived: the ended event is the last one and a length threshold could stop
  // the poll before it lands.
  const ended = () => c.lines().some((e) => e.t === 'agent' && (e as { state: string }).state === 'ended');
  const deadline = Date.now() + 2000;
  while (!ended() && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 10));
  }
  const events = c.lines();
  const states = events.filter((e) => e.t === 'agent').map((e) => (e as { state: string }).state);
  // The pane sees the agent go from connecting to live to ended, and its
  // surface is terminal.
  assert.ok(states.includes('connecting'));
  assert.ok(states.includes('live'));
  assert.ok(states.includes('ended'));
  const terminalAgent = events.find((e) => e.t === 'agent' && (e as { surface: string }).surface === 'terminal');
  assert.ok(terminalAgent, 'the terminal agent did not declare its surface');
  // Its output reached the watcher as steps: the terminal cast.
  const steps = events.filter((e) => e.t === 'step').map((e) => (e as { text: string }).text);
  assert.ok(steps.some((s) => s.includes('line two')), 'the cast did not stream to the watcher');
  c.close();
});

// The pseudo terminal path, driven against programs that genuinely draw.
//
// THE DEFECT THIS WHOLE SECTION EXISTS FOR. The terminal driver was
// implemented, unit tested and unreachable: it read lines through a pipe, and
// a program that takes over the screen will not start without a terminal, so
// the one kind of command line application nobody can test by hand was the one
// kind this could not drive. The tests below assert on the RENDERED SCREEN,
// because that is the claim: not that bytes arrived, but that the grid of
// cells a person would read says what the workflow expected.

const fixture = (name: string) => join(import.meta.dirname, 'fixtures', name);

/** The rendered screens a result recorded, which are the steps that have rows
 *  in them. The report's evidence for a terminal workflow IS the screen. */
function screens(result: WorkflowResult): string[] {
  return result.steps.filter((s) => s.includes('\n'));
}

test('runTerminal drives a full screen program and judges the rendered screen', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'inbox',
      command: execPath,
      args: [fixture('menu-tui.mjs')],
      screen: { rows: 12, cols: 50 },
      // Arrows rather than j and k, because an arrow is an escape sequence and
      // a character is not: this is the half a pipe could never have sent.
      input: ['<down>', '<down>', '<enter>', 'q'],
      expect: ['"Eleven posts are live."'],
    }],
  });
  assert.equal(results.length, 1);
  const result = results[0]!;
  assert.equal(result.outcome.verdict, 'pass', result.outcome.detail);

  const drawn = screens(result);
  // The selection MOVED, one row per arrow, which is the whole proof that a
  // keystroke reached the program and that the driver read the screen AFTER
  // the redraw rather than before it. Asserted on the screens themselves
  // rather than on how many were recorded: identical consecutive screens are
  // recorded once, so a count is a claim about the deduplication and not about
  // the program.
  assert.match(drawn[0]!, /> Drafts/);
  assert.ok(drawn.some((s) => /> Scheduled/.test(s)),
    'the first arrow never moved the selection');
  assert.match(drawn.at(-1)!, /> Published/);
  assert.ok(!/> Drafts/.test(drawn.at(-1)!),
    'the row that used to be selected is still selected on the last screen');
  // And the body the program drew for the row that was opened.
  assert.match(drawn.at(-1)!, /Eleven posts are live\./);
});

test('runTerminal fails when the screen never shows what was expected', async () => {
  // The same run as above with one word changed, so a pass and a fail differ
  // in the expectation and in nothing else. A driver that could not produce
  // this fail would have proved nothing with the pass.
  const results = await runTerminal({
    workflows: [{
      name: 'inbox',
      command: execPath,
      args: [fixture('menu-tui.mjs')],
      screen: { rows: 12, cols: 50 },
      input: ['<down>', '<down>', '<enter>', 'q'],
      expect: ['"Forty posts are live."'],
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'fail', results[0]!.outcome.detail);
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
  // THE DETAIL, which said "The screen showed a failure rather than what was
  // expected" over this healthy menu. It names what was missing and ends on
  // the screen the program finished on.
  const detail = results[0]!.outcome.detail;
  assert.ok(detail.startsWith('"Forty posts are live." was not found.'), detail);
  assert.ok(!/showed a failure|shows an error/i.test(detail), `a failure was invented: ${detail}`);
  assert.ok(/No error was showing\. Instead it showed: "Inbox Drafts Scheduled > Published Archived Eleven posts are live\."/.test(detail), detail);
});

test('runTerminal still says failure when the screen genuinely shows one', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'broken-screen',
      command: execPath,
      args: ['-e', 'process.stdout.write("Loading posts\\nSomething went wrong. Try again.\\n")'],
      screen: { rows: 8, cols: 50 },
      expect: ['"Eleven posts are live."'],
    }],
  });
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met', results[0]!.outcome.detail);
  assert.ok(results[0]!.outcome.detail.startsWith(
    'The screen showed a failure rather than what was expected. It says: "Something went wrong."'),
  results[0]!.outcome.detail);
});

test('runTerminal through a pipe names what was missing and invents no failure', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'healthy-output',
      command: execPath,
      args: ['-e', 'process.stdout.write("3 posts published\\n")'],
      expect: ['"4 posts published"'],
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'fail', results[0]!.outcome.detail);
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
  const detail = results[0]!.outcome.detail;
  assert.ok(detail.startsWith('"4 posts published" was not found.'), detail);
  assert.ok(!/showed a failure|shows an error/i.test(detail), `a failure was invented: ${detail}`);
  assert.ok(detail.includes('No error was showing. Instead it showed: "3 posts published"'), detail);
});

test('runTerminal through a pipe still says failure when the output shows one', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'broken-output',
      command: execPath,
      args: ['-e', 'process.stdout.write("Unable to connect to the database\\n")'],
      expect: ['"4 posts published"'],
    }],
  });
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met', results[0]!.outcome.detail);
  assert.ok(results[0]!.outcome.detail.startsWith(
    'The output showed a failure rather than what was expected. It says: "Unable to connect to the database"'),
  results[0]!.outcome.detail);
});

test('an arrow reaches a program in the cursor key mode it asked for', async () => {
  // The encoder is told the mode; whether the DRIVER reads the mode the
  // program set is a different claim, and this is the one that can say no. The
  // program sets DECCKM and draws which of the two encodings it received.
  const results = await runTerminal({
    workflows: [{
      name: 'cursor-mode',
      command: execPath,
      args: [fixture('cursor-mode.mjs')],
      screen: { rows: 8, cols: 40 },
      input: ['<up>', 'q'],
      expect: ['"application up"'],
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.ok(!screens(results[0]!).some((s) => s.includes('normal up')),
    'the driver sent the encoding the program had turned off');
});

test('a program that draws forever is judged and stopped, not reported blocked', async () => {
  // A full screen program is not supposed to exit. The workflow is over when
  // its keys have been sent and the screen has settled, and calling that an
  // exhausted budget would make every honest pass unreachable.
  const results = await runTerminal({
    workflows: [{
      name: 'stays-open',
      command: execPath,
      args: [fixture('menu-tui.mjs')],
      screen: { rows: 12, cols: 50 },
      input: ['<down>'],
      expect: ['"Inbox"'],
      maxMs: 20_000,
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.ok(results[0]!.durationMs < 15_000,
    `the driver waited ${results[0]!.durationMs} ms for a program that was never going to exit`);
});

test('the budget stops a program with keys still to send', async () => {
  const results = await runTerminal({
    workflows: [{
      name: 'too-slow',
      command: execPath,
      // Draws nothing and answers nothing, so every key spends its response
      // window and the budget runs out with keys left.
      args: ['-e', 'process.stdin.resume(); setInterval(() => {}, 1000);'],
      screen: { rows: 8, cols: 40 },
      input: ['a', 'b', 'c', 'd', 'e', 'f'],
      expect: ['"anything at all"'],
      maxMs: 1_200,
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'blocked', results[0]!.outcome.detail);
  assert.equal(results[0]!.outcome.cause, 'budget-exhausted');
});

test('without a screen a program is driven through a pipe, so what is typed is not evidence', async () => {
  // THE REASON THERE ARE TWO PATHS. A pseudo terminal echoes what is typed
  // into it. If declaring no screen quietly used one anyway, this expectation
  // would be satisfied by the workflow's own input against a program that
  // printed nothing, which is a check that cannot say no.
  const results = await runTerminal({
    workflows: [{
      name: 'silent',
      command: execPath,
      args: ['-e', 'process.stdin.resume(); process.stdin.on("end", () => process.exit(0));'],
      input: ['Welcome aboard'],
      expect: ['"Welcome aboard"'],
    }],
  });
  // A quoted expectation is present or absent with no third answer, so the
  // absence of the typed string from the program's output is a positive fact
  // rather than a shrug: nothing echoed it.
  assert.equal(results[0]!.outcome.verdict, 'fail', results[0]!.outcome.detail);
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
});

test('a workflow runs where it says, and the job directory is the default', async () => {
  // Two UNRELATED directories, neither a prefix of the other. An earlier
  // version used the job directory and its own parent, and a verbatim
  // expectation is a substring test, so the parent's path was found inside the
  // child's and the assertion could not tell the two apart: deleting the
  // workflow's own cwd left it green.
  const jobDir = realpathSync(mkdtempSync(join(tmpdir(), 'af-terminal-job-')));
  const ownDir = realpathSync(mkdtempSync(join(tmpdir(), 'af-terminal-own-')));
  const printCwd = ['-e', 'process.stdout.write(process.cwd())'];
  const results = await runTerminal({
    cwd: jobDir,
    workflows: [
      { name: 'job-directory', command: execPath, args: printCwd, expect: [JSON.stringify(jobDir)] },
      { name: 'its-own-directory', command: execPath, args: printCwd, cwd: ownDir, expect: [JSON.stringify(ownDir)] },
    ],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.equal(results[1]!.outcome.verdict, 'pass', results[1]!.outcome.detail);
});

test('a program that will not exit and shows the wrong thing fails rather than blocks', async () => {
  // The distinction that decides whether this feature is worth anything. A
  // full screen program does not exit, so "did not exit" cannot mean "the
  // budget ran out": if it did, every TUI showing the WRONG screen would be
  // excused as blocked, and blocked does not count against the application.
  // The pass above cannot catch that, because a met expectation wins over
  // every other answer before this is reached.
  const results = await runTerminal({
    workflows: [{
      name: 'wrong-screen',
      command: execPath,
      args: [fixture('menu-tui.mjs')],
      screen: { rows: 12, cols: 50 },
      input: ['<down>'],
      expect: ['"Outbox"'],
      maxMs: 20_000,
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'fail', results[0]!.outcome.detail);
  assert.equal(results[0]!.outcome.cause, 'expectation-not-met');
});

test('a screen shows the last rows, and the scrollback behind it is still read', async () => {
  // Two claims about a printing program given a screen, and the first is the
  // one an alternate buffer cannot make: on the normal buffer the grid is a
  // WINDOW onto a longer history, so reading from row zero reads the oldest
  // lines and reports a screen the terminal stopped showing long ago.
  const print40 = ['-e', String.raw`for (let i = 1; i <= 40; i++) process.stdout.write("line " + i + "\n")`];
  const results = await runTerminal({
    workflows: [
      { name: 'newest', command: execPath, args: print40, screen: { rows: 10, cols: 40 }, expect: ['"line 40"'] },
      // line 1 scrolled off a ten row screen thirty lines ago, so nothing but
      // the scrollback can answer for it.
      { name: 'oldest', command: execPath, args: print40, screen: { rows: 10, cols: 40 }, expect: ['"line 1"'] },
    ],
  });
  const result = results[0]!;
  assert.equal(result.outcome.verdict, 'pass', result.outcome.detail);
  assert.equal(results[1]!.outcome.verdict, 'pass', results[1]!.outcome.detail);
  const drawn = result.steps.filter((s) => s.includes('\n'));
  const last = drawn.at(-1)!;
  assert.match(last, /line 40/, 'the rendered screen is not showing the newest rows');
  assert.ok(!/line 1\b/.test(last),
    `the rendered screen is showing rows that scrolled away:\n${last}`);
});

test('a burst still being parsed is drawn before the screen is judged', async () => {
  // The forty line version above is the same claim at a size that hides the
  // defect. The driver stops waiting the MOMENT the program exits, and the
  // emulator parses what it was sent in chunks across later ticks, so a
  // program that prints a lot and exits at once is judged with its last
  // redraw still queued. The expectation then fails about output the program
  // certainly wrote, and it fails more often the busier the host is, which is
  // how it reads as a flake rather than as the race it is.
  //
  // Ten thousand lines is past the point where the parse outlives the exit:
  // measured on this driver, the unwaited version missed the last line 18
  // times out of 18 at this size and 8 times out of 10 at two thousand, while
  // forty lines passed every time.
  const lines = 10_000;
  const burst = ['-e', String.raw`for (let i = 1; i <= 10000; i++) process.stdout.write("line " + i + "\n")`];
  const results = await runTerminal({
    workflows: [{
      name: 'burst',
      command: execPath,
      args: burst,
      screen: { rows: 10, cols: 40 },
      expect: [`"line ${lines}"`],
      maxMs: 20_000,
    }],
  });
  // One assertion on purpose. A verdict of pass is reached only through the
  // succeeded cause, so a second assertion about the cause would be a line
  // this defect can never reach: assert.equal stops the test at the first
  // failure, so the extra claim would look alive while measuring nothing.
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
});

test('the first screen recorded as evidence is one the program had finished drawing', async () => {
  // The verdict and the EVIDENCE come from two different reads, and the test
  // above covers only the verdict. A screen is recorded when the driver
  // captures one that CHANGED, so a capture taken while the emulator is still
  // parsing records a grid the program had already moved past, and the report
  // shows its reader a screen that was never the program's last word.
  //
  // This is the assertion that tells the two waits apart. Waiting before the
  // capture is what makes the FIRST recorded screen honest. Waiting before
  // the transcript is read cannot help here, because by then the stale screen
  // has already been recorded and the later honest one is only appended
  // behind it.
  //
  // Say plainly how strong this one is. Dropping the wait before the capture
  // failed it 5 times out of 6, not 6 out of 6, and raising the burst to
  // twenty thousand did not change that ratio. The residual is inherent
  // rather than a matter of sizing: the staleness is observable only when the
  // parse is still outstanding at the first capture, and sometimes it is not.
  // So this gate can say no, and it is not a deterministic one.
  const lines = 10_000;
  const burst = ['-e', String.raw`for (let i = 1; i <= 10000; i++) process.stdout.write("line " + i + "\n")`];
  const results = await runTerminal({
    workflows: [{
      name: 'evidence',
      command: execPath,
      args: burst,
      screen: { rows: 10, cols: 40 },
      expect: [`"line ${lines}"`],
      maxMs: 20_000,
    }],
  });
  const screens = results[0]!.steps.filter((s) => s.includes('\n'));
  assert.ok(screens.length > 0, 'the run recorded no screen at all, so there is no evidence to judge');
  assert.match(screens[0]!, new RegExp(`line ${lines}`),
    `the first screen recorded as evidence was captured mid parse:\n${screens[0]}`);
});

test('a burst that arrives while the driver waits for the exit is still drawn', async () => {
  // The third arrival order, and the one neither test above reaches. A
  // program that answers a key, falls quiet, and only THEN prints and exits
  // leaves the driver waiting on the process rather than on the emulator:
  // the settle is long over, so waiting before the capture cannot help, and
  // the only thing standing between the burst and the verdict is the wait
  // before the transcript is read.
  //
  // WHAT THIS TEST USED TO BE, because the shape is the lesson. It printed
  // twenty thousand lines, and the size was justified in its own comment as a
  // measurement: dropping the wait failed it 6 times out of 6 there and 4 of 6
  // at ten thousand. Both numbers were taken on one machine, and neither is a
  // fact about the driver. The size decides how LONG the burst takes to write,
  // which is a property of the host, so the calibration made the test say two
  // different things on two machines and neither of them was "the property
  // holds". On a loaded machine the burst outlived the driver's fixed grace and
  // the test reported the property FALSE; on an idle one the same twenty
  // thousand lines were written in 297 ms, inside the grace, and the test
  // passed without ever reaching the defect. It was a flake and a vacuous pass
  // wearing one number.
  //
  // WHAT REPLACES IT, AND WHY IT IS PACED RATHER THAN SIZED. The program no
  // longer prints as fast as it can. It prints a little, PAUSES, and repeats,
  // and every number below is derived from a constant the driver exports rather
  // than measured on a host:
  //
  //  * `gapMs` is twice QUIET_MS, so every pause is a silence the driver can
  //    SEE and is nowhere near the silence it is entitled to END on. That is
  //    the whole trick. The driver's wait may not end on it, but its emulator
  //    catches up inside it, so the fixed point that used to be mistaken for a
  //    drain becomes reachable ON PURPOSE instead of only when a loaded machine
  //    happened to deschedule the writer. The condition that made this flaky is
  //    now a scripted step.
  //  * `paces * gapMs` exceeds EXIT_GRACE_MS, so a wait that went back to
  //    ending on a fixed grace expires in the MIDDLE of the burst on any
  //    machine at any load, rather than only on a slow one.
  //  * `pacedLines` is small so the emulator drains inside a gap, and so the
  //    pacing costs bytes nobody has to parse.
  //  * `finalLines` is the one count left, and its job changed. It no longer
  //    has to outlast a grace, which was the host dependent claim. It has to
  //    take longer to PARSE than one turn of the event loop, so that a driver
  //    which read the transcript without draining could not accidentally find
  //    the last line already on the grid. Twenty thousand lines are 228930
  //    bytes and parse in about 1.5 s, against a turn of the loop, which is
  //    four orders of magnitude of room and no calibration at all. It is
  //    bounded ABOVE by the budget, which is where the parse is paid.
  //
  // Both pauses are spent in a busy loop rather than a timer: the ordering is
  // the whole test, and a timer is read only when the child's event loop reaches
  // its timer phase. The expectation is the program's own last line rather than
  // a line number, so the claim is about the END of the burst however it is
  // sized.
  const gapMs = QUIET_MS * 2;
  const paces = 5;
  const pacedLines = 200;
  const finalLines = 20_000;
  // The derivation, asserted rather than trusted. An edit that moves any of
  // these numbers, or either driver constant, out of the relationship the
  // comment above depends on takes this test's power with it, and a test that
  // has quietly lost its power is the thing this file keeps being written to
  // prevent. Both halves are claimed separately: a gap that is invisible and a
  // gap the driver ends on are different ways to lose, and one assert that
  // stopped at the first of them would leave the second unmeasured.
  assert.ok(gapMs > QUIET_MS,
    `a pause of ${gapMs} ms is shorter than the ${QUIET_MS} ms silence the driver can see, so the emulator never catches up inside one`);
  assert.ok(gapMs < EXIT_GRACE_MS,
    `a pause of ${gapMs} ms is a silence the driver is entitled to end on, so this program reads as finished mid burst`);
  assert.ok(paces * gapMs > EXIT_GRACE_MS,
    `a burst paced over ${paces * gapMs} ms does not outlast a reinstated ${EXIT_GRACE_MS} ms grace, so this test cannot catch one`);
  const quiet = String.raw`
const stall = (ms) => { const until = Date.now() + ms; while (Date.now() < until); };
const print = (n, from) => { for (let i = 1; i <= n; i++) process.stdout.write("line " + (from + i) + "\n"); };
process.stdout.write("ready\n");
process.stdin.setRawMode && process.stdin.setRawMode(true);
process.stdin.once("data", () => {
  process.stdout.write("ack\n");
  // Quiet first, so the driver's settle for the key is over before a byte of
  // the burst arrives. That is the arrival order this test exists for.
  stall(${gapMs});
  let printed = 0;
  for (let p = 0; p < ${paces}; p++) {
    print(${pacedLines}, printed);
    printed += ${pacedLines};
    stall(${gapMs});
  }
  print(${finalLines}, printed);
  process.stdout.write("the burst ended\n");
  process.exit(0);
});
`;
  const results = await runTerminal({
    workflows: [{
      name: 'late-burst',
      command: execPath,
      args: ['-e', quiet],
      screen: { rows: 10, cols: 40 },
      input: ['<enter>'],
      expect: ['"the burst ended"'],
      // The ceiling, not an expectation. The driver spends what the pacing and
      // the parse need and no more, which measured out at about 4 s on a loaded
      // 16GB eight core Mac. A budget an order of magnitude past that is what
      // lets the driver report blocked, rather than fail, on a machine that
      // genuinely cannot finish.
      maxMs: 60_000,
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
});

/** NEVER_RETURNED is what this file calls a driver that did not come back.
 *
 *  It exists because "no answer" is not an answer, and a check whose only way
 *  of refusing is to stop finishing has the very defect this lane was opened
 *  for. An emulator drain with no ceiling can NEVER catch a producer faster
 *  than itself: every poll finds the queue longer than it left it. Removing
 *  that ceiling hung a run for four hundred seconds. Bounding the test alone
 *  was not enough, and the measurement is worth keeping: node's own
 *  `{ timeout }` did fire and did name the test, but the abandoned driver still
 *  held the pseudo terminal, so the FILE never finished and the summary read
 *  `pass 0  fail 0  cancelled 2`. A reader scanning counters sees no failure
 *  there at all. Racing the call against a timer is what converts that into a
 *  counted assertion naming the cause. */
const NEVER_RETURNED = 'the driver never returned';

/** withinReach runs a driver call against a timer, so a driver that never comes
 *  back is a failed assertion rather than a run with no verdict in it. The
 *  timer is UNREFERENCED, so a call that returns normally pays nothing for it
 *  and does not hold the event loop open afterwards. */
async function withinReach<T>(work: Promise<T>, ms: number): Promise<T | typeof NEVER_RETURNED> {
  return Promise.race([
    work,
    new Promise<typeof NEVER_RETURNED>((resolve) => {
      setTimeout(() => resolve(NEVER_RETURNED), ms).unref();
    }),
  ]);
}

// Two bounds, and they catch different things. The race below turns a driver
// that never returns into a named assertion. node's own timeout is the backstop
// for a hang anywhere else in the test, and it is longer than the race so the
// assertion is the one that speaks. The run measures about 3 s, so both are an
// order of magnitude of room and neither bounds anything that works.
test('a program still writing when the budget runs out is blocked, not judged', { timeout: 30_000 }, async () => {
  // THE DIFFERENCE BETWEEN THE TWO REDS, and the one the test above used to
  // print the wrong one of. "The expectation was not met" is a claim about the
  // program. "I stopped reading before the program stopped writing" is a claim
  // about the run. The driver reported the first when it meant the second, at
  // line 3892 of 20000, and a reader of that check would have gone looking for
  // a bug in a program that was working.
  //
  // Nothing here is calibrated: the program never exits and never goes silent,
  // so no budget can ever be enough and the outcome is the same on every
  // machine at every load. The budget is small so the test is cheap.
  const raced = await withinReach(runTerminal({
    workflows: [{
      name: 'never-stops',
      command: execPath,
      args: ['-e', 'for (;;) process.stdout.write("still going\\n");'],
      screen: { rows: 10, cols: 40 },
      expect: ['"the burst ended"'],
      maxMs: 1_500,
    }],
  }), 20_000);
  if (raced === NEVER_RETURNED) {
    assert.fail(`${NEVER_RETURNED} for a program that never stops writing, so its emulator drain has no ceiling`);
  }
  const outcome = raced[0]!.outcome;
  assert.equal(outcome.verdict, 'blocked', outcome.detail);
  assert.equal(outcome.cause, 'budget-exhausted', outcome.detail);
  // The detail has to name the real cause and MEASURE it. A blocked verdict
  // whose text says nothing about why sends its reader to the same wrong place
  // the failing verdict did.
  assert.match(outcome.detail, /ran out with the program still writing/, outcome.detail);
  const measured = /It had written (\d+) bytes and the 10 by 40 screen was (\d+) of them behind/
    .exec(outcome.detail);
  assert.ok(measured, `the report never said how far behind it stopped: ${outcome.detail}`);
  const [written, behind] = [Number(measured[1]), Number(measured[2])];
  // Both numbers have to be REAL, because a zero in either would satisfy the
  // pattern above while measuring nothing. A program writing flat out for a
  // second and a half puts the emulator far behind and puts something on the
  // grid, so neither end of that is in doubt.
  assert.ok(behind > 0, `the screen was reported fully caught up with a program still writing: ${outcome.detail}`);
  assert.ok(written > behind,
    `${behind} bytes behind out of ${written} written leaves nothing on the screen: ${outcome.detail}`);
  assert.ok(!/was not found/.test(outcome.detail),
    `the report blamed the expectation for a screen it never waited for: ${outcome.detail}`);
});

test('the job environment reaches the program on both paths', async () => {
  // How a command line tool under test learns where the rehearsal environment
  // is. A variable assembled and sent nowhere is the dead wiring this
  // repository keeps finding in itself.
  const read = ['-e', 'process.stdout.write("base=" + process.env.AF_BASE_URL)'];
  const results = await runTerminal({
    env: { AF_BASE_URL: 'http://127.0.0.1:45999' },
    workflows: [
      {
        name: 'through-a-pipe',
        command: execPath,
        args: read,
        expect: ['"base=http://127.0.0.1:45999"'],
      },
      {
        name: 'on-a-screen',
        command: execPath,
        args: read,
        screen: { rows: 8, cols: 60 },
        expect: ['"base=http://127.0.0.1:45999"'],
      },
    ],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass', results[0]!.outcome.detail);
  assert.equal(results[1]!.outcome.verdict, 'pass', results[1]!.outcome.detail);
});

test('a failed terminal workflow carries steps a person can follow', async () => {
  // classify() returns an empty reproduction, and the markdown report SKIPS
  // the "how to see this yourself" block entirely when the list is empty. So
  // a terminal workflow that failed reported a verdict with nothing under it,
  // and the reader of a red check was shown no way to reach it.
  const results = await runTerminal({
    workflows: [{
      name: 'inbox',
      command: execPath,
      args: [fixture('menu-tui.mjs')],
      screen: { rows: 12, cols: 50 },
      input: ['<down>', '<enter>'],
      expect: ['"Forty posts are live."'],
    }],
  });
  const outcome = results[0]!.outcome;
  assert.equal(outcome.verdict, 'fail', outcome.detail);
  const how = outcome.reproduction.join('\n');
  assert.match(how, /af up/);
  assert.match(how, /menu-tui\.mjs on a 12 by 50 terminal/,
    `the invocation and the terminal size are missing:\n${how}`);
  assert.match(how, /2\. Press down/);
  assert.match(how, /3\. Press enter/);
  assert.match(how, /Expected: "Forty posts are live\."/);
  assert.match(how, /Got: /);
  // A grid of cells is a picture rather than an instruction, and the report
  // writes one entry per line where its columns would stop lining up. The
  // screens stay in the steps.
  assert.ok(!outcome.reproduction.some((line) => line.includes('\n')),
    'a rendered screen reached the reproduction, where its columns collapse');
  assert.ok(results[0]!.steps.some((s) => s.includes('> Drafts')),
    'the screens left the steps as well');
});

test('a terminal workflow that passed carries no reproduction', async () => {
  // The same rule the web driver follows: nobody reproduces a pass, and a
  // list of instructions under a green result is noise in every report.
  const results = await runTerminal({
    workflows: [{
      name: 'greeting',
      command: execPath,
      args: ['-e', 'process.stdout.write("Welcome aboard\\n")'],
      expect: ['"Welcome aboard"'],
    }],
  });
  assert.equal(results[0]!.outcome.verdict, 'pass');
  assert.deepEqual(results[0]!.outcome.reproduction, []);
});
