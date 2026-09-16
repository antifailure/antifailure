// The surface driver abstraction and the two drivers that are built. Web is
// exercised by the whole browser suite; here we prove the registry, the loud
// refusal of the scaffolded surfaces, and the terminal driver against a real
// process.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:net';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { execPath } from 'node:process';
import {
  driverFor, surfaces, assertAvailable, NotImplementedError, type Surface,
} from '../src/drivers/driver.ts';
import * as desktop from '../src/drivers/desktop.ts';
import * as ios from '../src/drivers/ios.ts';
import { runTerminal } from '../src/drivers/terminal.ts';
import { socketSink, decode, type LiveEvent } from '../src/live.ts';

test('the registry knows every surface and which are available', () => {
  assert.deepEqual([...surfaces()].sort(), ['desktop', 'ios', 'terminal', 'web']);
  assert.equal(driverFor('web').available, true);
  assert.equal(driverFor('terminal').available, true);
  assert.equal(driverFor('desktop').available, false);
  assert.equal(driverFor('ios').available, false);
});

test('assertAvailable passes a built surface and refuses a scaffolded one loudly', () => {
  // Built surfaces do not throw.
  assertAvailable('web');
  assertAvailable('terminal');
  // Scaffolded surfaces throw, so a run that targets them fails rather than
  // returning a green verdict that tested nothing.
  for (const surface of ['desktop', 'ios'] as Surface[]) {
    assert.throws(() => assertAvailable(surface), (err: unknown) => {
      assert.ok(err instanceof NotImplementedError);
      assert.equal((err as NotImplementedError).surface, surface);
      return true;
    });
  }
});

test('the scaffolded drivers throw rather than silently pass', () => {
  assert.throws(() => desktop.drive(), NotImplementedError);
  assert.throws(() => ios.drive(), NotImplementedError);
  assert.equal(desktop.desktop.available, false);
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
