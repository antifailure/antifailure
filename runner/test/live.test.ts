// The live channel is best effort and must never change a run, so the tests
// below prove exactly that: events arrive in order over a real socket, a
// counter that has to be monotonic is, a pump that must not stack captures does
// not, and every failure mode that could take a run down instead degrades to
// nothing.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer, type Server } from 'node:net';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import {
  encode, decode, nullSink, socketSink, FramePump, PROTOCOL,
  type LiveEvent,
} from '../src/live.ts';

/** A socket server that collects every NDJSON line a sink writes to it, so a
 *  test can assert on what actually crossed the wire. */
function collector(): Promise<{
  path: string; lines: () => LiveEvent[]; raw: () => string; close: () => void; server: Server;
}> {
  const dir = mkdtempSync(join(tmpdir(), 'af-live-'));
  const path = join(dir, 'live.sock');
  let buffer = '';
  const server = createServer((socket) => {
    socket.setEncoding('utf8');
    socket.on('data', (chunk) => { buffer += chunk; });
  });
  return new Promise((resolve) => {
    server.listen(path, () => {
      resolve({
        path,
        raw: () => buffer,
        lines: () => buffer.split('\n').map(decode).filter((e): e is LiveEvent => !!e),
        close: () => server.close(),
        server,
      });
    });
  });
}

/** Waits until `predicate` holds or the budget runs out, polling. A socket is
 *  asynchronous and a test that read once would race the flush. */
async function until(predicate: () => boolean, budgetMs = 2_000): Promise<void> {
  const deadline = Date.now() + budgetMs;
  while (!predicate() && Date.now() < deadline) {
    await new Promise((r) => setTimeout(r, 10));
  }
}

test('encode and decode round-trip every event type', () => {
  const events: LiveEvent[] = [
    { t: 'hello', run: 'r', at: '2026-01-01T00:00:00.000Z', protocol: PROTOCOL },
    { t: 'agent', at: '2026-01-01T00:00:00.000Z', agent: 'a', surface: 'web', state: 'live', persona: 'p', workflow: 'w' },
    { t: 'step', at: '2026-01-01T00:00:00.000Z', agent: 'a', seq: 3, text: 'Press Continue', url: 'http://x/', action: 'click' },
    { t: 'frame', at: '2026-01-01T00:00:00.000Z', agent: 'a', seq: 4, mime: 'image/jpeg', w: 1280, h: 800, b64: 'AAAA' },
    { t: 'done', run: 'r', at: '2026-01-01T00:00:00.000Z', passed: 1, failed: 2, flaky: 0, blocked: 0, unverified: 0 },
  ];
  for (const event of events) {
    const line = encode(event);
    // One event is one line: the newline is the boundary a stream reader splits on.
    assert.equal(line.endsWith('\n'), true);
    assert.equal(line.indexOf('\n'), line.length - 1);
    assert.deepEqual(decode(line), event);
  }
});

test('decode returns undefined for a blank or malformed line', () => {
  assert.equal(decode(''), undefined);
  assert.equal(decode('   '), undefined);
  assert.equal(decode('{not json'), undefined);
  // Valid JSON that is not an event (no `t`) is refused rather than passed on.
  assert.equal(decode('{"x":1}'), undefined);
});

test('nullSink does nothing and never throws', async () => {
  const sink = nullSink();
  // Every method is safe to call and returns nothing worth checking; the point
  // is that none of them throw and close resolves.
  sink.hello('r');
  sink.agent({ id: 'a', surface: 'web' }, 'live');
  sink.step('a', { text: 'x' });
  sink.frame('a', { mime: 'image/jpeg', w: 1, h: 1, b64: 'AA' });
  sink.done({ passed: 0, failed: 0, flaky: 0, blocked: 0, unverified: 0 });
  await sink.close();
  assert.ok(true);
});

test('socketSink writes events in order over a real socket', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  sink.hello('run-1');
  sink.agent({ id: 'a', surface: 'web', persona: 'owner', workflow: 'signup' }, 'live');
  sink.step('a', { text: 'Open /signup', url: 'http://x/signup', action: 'goto' });
  sink.frame('a', { mime: 'image/jpeg', w: 1280, h: 800, b64: 'Zm9v' });
  await until(() => c.lines().length >= 4);
  const lines = c.lines();
  assert.equal(lines.length, 4);
  assert.equal(lines[0]!.t, 'hello');
  assert.equal(lines[1]!.t, 'agent');
  assert.equal(lines[2]!.t, 'step');
  assert.equal(lines[3]!.t, 'frame');
  const frame = lines[3]!;
  assert.equal(frame.t === 'frame' && frame.b64, 'Zm9v');
  await sink.close();
  c.close();
});

test('socketSink numbers steps and frames monotonically per agent', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  sink.step('a', { text: 'one' });
  sink.frame('a', { mime: 'image/jpeg', w: 1, h: 1, b64: 'AA' });
  sink.step('a', { text: 'two' });
  sink.step('b', { text: 'other' });
  await until(() => c.lines().length >= 4);
  const seqs = c.lines()
    .filter((e) => e.t === 'step' || e.t === 'frame')
    .filter((e) => (e as { agent: string }).agent === 'a')
    .map((e) => (e as { seq: number }).seq);
  // Agent a's three events are 1, 2, 3 in the order they were sent, and a
  // different agent has its own counter starting again at 1.
  assert.deepEqual(seqs, [1, 2, 3]);
  const bSeq = c.lines().filter((e) => (e as { agent?: string }).agent === 'b')
    .map((e) => (e as { seq: number }).seq);
  assert.deepEqual(bSeq, [1]);
  await sink.close();
  c.close();
});

test('socketSink buffers events sent before the connection is up and flushes them', async () => {
  const c = await collector();
  // These are sent synchronously, in the same tick socketSink is created, so
  // the socket cannot possibly be connected yet. They must still arrive.
  const sink = socketSink(c.path);
  sink.hello('early');
  sink.step('a', { text: 'also early' });
  await until(() => c.lines().length >= 2);
  assert.equal(c.lines().length, 2);
  assert.equal(c.lines()[0]!.t, 'hello');
  await sink.close();
  c.close();
});

test('socketSink flushes what a run emitted when the run finished before the socket connected', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  sink.hello('fast');
  sink.agent({ id: 'a', surface: 'desktop' }, 'ended', 'pass');
  // Closed in the SAME TICK the sink was created, with no wait of any kind.
  // The socket cannot have connected yet, so every event is still in the
  // buffer, and a close that does not wait for the connection throws all of
  // them away: the watcher of a run that finished in under a millisecond saw
  // not one event rather than a few. The test above cannot catch this,
  // because it waits for the lines before closing, which is the one thing a
  // finished run does not do.
  await sink.close();
  try {
    // Polled after the close rather than before it, because close() is what
    // flushes and because a socket write arriving is not the same event as
    // the server having read it. Two seconds is the budget every other test
    // here uses; a flush that never comes spends all of it and then says so.
    await until(() => c.lines().length >= 2);
    assert.equal(c.lines().length, 2, `the live stream was lost on close: ${c.raw()}`);
    assert.equal(c.lines()[0]!.t, 'hello');
    assert.equal(c.lines()[1]!.t, 'agent');
  } finally {
    // In a finally: a listening server left behind by a red assertion holds
    // the event loop open, and one failed test then reads as a hung suite.
    c.close();
  }
});

test('socketSink degrades to a no-op when the socket cannot connect', async () => {
  // A path nobody is listening on. Every call must return and close must
  // resolve: a watcher that never arrives cannot be allowed to fail the run.
  const dir = mkdtempSync(join(tmpdir(), 'af-live-'));
  const sink = socketSink(join(dir, 'nobody.sock'));
  sink.hello('run');
  sink.agent({ id: 'a', surface: 'web' }, 'live');
  sink.step('a', { text: 'x' });
  sink.frame('a', { mime: 'image/jpeg', w: 1, h: 1, b64: 'AA' });
  sink.done({ passed: 0, failed: 0, flaky: 0, blocked: 0, unverified: 0 });
  await sink.close();
  assert.ok(true);
});

test('FramePump emits a frame per tick with a rising sequence', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  let n = 0;
  const pump = new FramePump(
    async () => ({ w: 100, h: 50, b64: `frame-${++n}` }),
    sink, 'agent-1', 10_000, // long interval: we drive ticks by hand
  );
  await pump.tick();
  await pump.tick();
  await until(() => c.lines().filter((e) => e.t === 'frame').length >= 2);
  const frames = c.lines().filter((e) => e.t === 'frame') as Array<{ seq: number; b64: string }>;
  assert.equal(frames.length, 2);
  assert.deepEqual(frames.map((f) => f.b64), ['frame-1', 'frame-2']);
  // The sequence is monotonic, which is what lets a watcher keep the latest.
  assert.equal(frames[1]!.seq > frames[0]!.seq, true);
  pump.stop();
  await sink.close();
  c.close();
});

test('FramePump never lets two captures overlap', async () => {
  let inFlight = 0;
  let maxConcurrent = 0;
  const pump = new FramePump(
    async () => {
      inFlight++;
      maxConcurrent = Math.max(maxConcurrent, inFlight);
      await new Promise((r) => setTimeout(r, 30));
      inFlight--;
      return { w: 1, h: 1, b64: 'AA' };
    },
    nullSink(), 'a', 5,
  );
  pump.start();
  await new Promise((r) => setTimeout(r, 120));
  pump.stop();
  // A slow capture behind a fast interval must not stack: exactly one capture
  // is ever in flight, which is the whole reason the busy guard exists.
  assert.equal(maxConcurrent, 1);
});

test('FramePump swallows a capture that throws and keeps going', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  let n = 0;
  const pump = new FramePump(
    async () => {
      n++;
      if (n === 1) throw new Error('screenshot failed mid navigation');
      return { w: 1, h: 1, b64: 'ok' };
    },
    sink, 'a', 10_000,
  );
  await pump.tick(); // throws internally, must not reject
  await pump.tick(); // succeeds
  await until(() => c.lines().filter((e) => e.t === 'frame').length >= 1);
  const frames = c.lines().filter((e) => e.t === 'frame');
  // The failed capture produced no frame; the next one did. One frame, not two,
  // and no unhandled rejection took the process down.
  assert.equal(frames.length, 1);
  pump.stop();
  await sink.close();
  c.close();
});

test('FramePump stop halts further frames', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  const pump = new FramePump(
    async () => ({ w: 1, h: 1, b64: 'AA' }),
    sink, 'a', 10_000,
  );
  pump.stop();
  await pump.tick(); // after stop, this must produce nothing
  await new Promise((r) => setTimeout(r, 50));
  assert.equal(c.lines().filter((e) => e.t === 'frame').length, 0);
  await sink.close();
  c.close();
});
