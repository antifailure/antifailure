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
  driverFor, surfaces, assertAvailable, NotImplementedError, type Surface,
} from '../src/drivers/driver.ts';
import * as ios from '../src/drivers/ios.ts';
import { runTerminal } from '../src/drivers/terminal.ts';
import { socketSink, decode, type LiveEvent } from '../src/live.ts';
import type { WorkflowResult } from '../src/execute.ts';

test('the registry knows every surface and which are available', () => {
  assert.deepEqual([...surfaces()].sort(), ['desktop', 'ios', 'terminal', 'web']);
  assert.equal(driverFor('web').available, true);
  assert.equal(driverFor('terminal').available, true);
  assert.equal(driverFor('desktop').available, true);
  assert.equal(driverFor('ios').available, false);
});

test('assertAvailable passes a built surface and refuses a scaffolded one loudly', () => {
  // Built surfaces do not throw.
  assertAvailable('web');
  assertAvailable('terminal');
  assertAvailable('desktop');
  // Scaffolded surfaces throw, so a run that targets them fails rather than
  // returning a green verdict that tested nothing.
  for (const surface of ['ios'] as Surface[]) {
    assert.throws(() => assertAvailable(surface), (err: unknown) => {
      assert.ok(err instanceof NotImplementedError);
      assert.equal((err as NotImplementedError).surface, surface);
      return true;
    });
  }
});

test('the scaffolded driver throws rather than silently passing', () => {
  assert.throws(() => ios.drive(), NotImplementedError);
  assert.equal(ios.ios.available, false);
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
