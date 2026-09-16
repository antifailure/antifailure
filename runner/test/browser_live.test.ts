// The web surface, proven live end to end: a real browser against a real
// server, streaming to a real socket. This is the "a frame emitted by the
// runner reaches a watcher" claim for the web driver, with an actual JPEG.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { createServer as createHTTP, type Server as HTTPServer } from 'node:http';
import { createServer as createNet, type Server as NetServer } from 'node:net';
import { mkdtempSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { Session } from '../src/browser.ts';
import { run } from '../src/execute.ts';
import { socketSink, decode, type LiveEvent } from '../src/live.ts';

/** A tiny page with a heading, so a run has something to open and read. */
function application(): { server: HTTPServer; url: Promise<string> } {
  const server = createHTTP((_req, res) => {
    res.writeHead(200, { 'content-type': 'text/html' });
    res.end('<html><body><h1>Welcome home</h1><p>You are signed in.</p></body></html>');
  });
  const url = new Promise<string>((resolve) => {
    server.listen(0, '127.0.0.1', () => {
      const addr = server.address();
      resolve(typeof addr === 'object' && addr ? `http://127.0.0.1:${addr.port}` : '');
    });
  });
  return { server, url };
}

function collector(): Promise<{ path: string; lines: () => LiveEvent[]; close: () => void }> {
  const dir = mkdtempSync(join(tmpdir(), 'af-blive-'));
  const path = join(dir, 'l.sock');
  let buffer = '';
  const server: NetServer = createNet((s) => { s.setEncoding('utf8'); s.on('data', (c) => { buffer += c; }); });
  return new Promise((resolve) => {
    server.listen(path, () => resolve({
      path,
      lines: () => buffer.split('\n').map(decode).filter((e): e is LiveEvent => !!e),
      close: () => server.close(),
    }));
  });
}

test('a web Session captures a real JPEG frame of the viewport', async () => {
  const artifacts = mkdtempSync(join(tmpdir(), 'af-art-'));
  const session = await Session.open({ artifacts, headless: true });
  try {
    const frame = await session.liveFrame();
    assert.ok(frame, 'no frame captured');
    assert.equal(frame!.w > 0 && frame!.h > 0, true);
    // The base64 of a JPEG begins with the encoding of its FF D8 FF magic.
    assert.ok(frame!.b64.startsWith('/9j/'), 'the frame was not a JPEG');
  } finally {
    await session.close('frame-probe');
  }
});

test('a web Session pumps a real JPEG frame through the sink to a watcher', async () => {
  const c = await collector();
  const sink = socketSink(c.path);
  const artifacts = mkdtempSync(join(tmpdir(), 'af-art-'));
  const session = await Session.open({ artifacts, headless: true, live: { sink, agent: 'probe' } });
  try {
    // The pump samples on an interval; poll the watcher until its first frame
    // arrives, which is the Session -> FramePump -> sink -> socket path with a
    // real browser and a real image.
    const frameArrived = () => c.lines().some((e) => e.t === 'frame');
    const deadline = Date.now() + 4000;
    while (!frameArrived() && Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 50));
    }
    const frames = c.lines().filter((e) => e.t === 'frame') as Array<{ b64: string; w: number; h: number }>;
    assert.ok(frames.length >= 1, 'no frame reached the watcher from the pump');
    assert.ok(frames[0]!.b64.startsWith('/9j/'), 'the streamed frame was not a JPEG');
    assert.ok(frames[0]!.w > 0 && frames[0]!.h > 0, 'the streamed frame had no dimensions');
  } finally {
    await session.close('pump-probe');
    await sink.close();
    c.close();
  }
});

test('a web run streams agent lifecycle and steps to a watcher', async () => {
  const app = application();
  const baseURL = await app.url;
  const c = await collector();
  const sink = socketSink(c.path);
  const artifacts = mkdtempSync(join(tmpdir(), 'af-art-'));
  try {
    const results = await run({
      baseURL,
      artifacts,
      headless: true,
      live: sink,
      personas: [],
      workflows: [{ name: 'home', description: 'the home page', expect: ['Welcome home'], maxSteps: 2 }],
    });
    assert.equal(results.length, 1);
    await sink.close();

    const ended = () => c.lines().some((e) => e.t === 'agent' && (e as { state: string }).state === 'ended');
    const deadline = Date.now() + 3000;
    while (!ended() && Date.now() < deadline) {
      await new Promise((r) => setTimeout(r, 20));
    }
    const events = c.lines();
    const states = events.filter((e) => e.t === 'agent').map((e) => (e as { state: string }).state);
    assert.ok(states.includes('live'), `agent never went live: ${states.join(',')}`);
    assert.ok(states.includes('ended'), `agent never ended: ${states.join(',')}`);
    // At least one real step reached the watcher.
    assert.ok(events.some((e) => e.t === 'step'), 'no step reached the watcher');
    // Any frame that arrived during this short run is a JPEG (the deterministic
    // proof that a frame reaches the watcher is the pump test above; a fast run
    // may finish before the sampling interval fires, which is not a fault).
    for (const f of events.filter((e) => e.t === 'frame') as Array<{ b64: string }>) {
      assert.ok(f.b64.startsWith('/9j/'), 'a streamed frame was not a JPEG');
    }
  } finally {
    c.close();
    app.server.close();
  }
});
