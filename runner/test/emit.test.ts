import { test } from 'node:test';
import assert from 'node:assert/strict';
import { spawn } from 'node:child_process';
import { mkdtempSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

import { replaceRegExp, writeFully } from '../src/emit.ts';

// The runner writes one JSON document to stdout and the process exits the
// instant it is written. process.exit truncates a write that has not drained,
// and a pipe buffer is small, so a document larger than it reached the engine
// cut off mid JSON and a healthy run read as blocked. writeFully waits for the
// write to be acknowledged before it resolves, so emit can be awaited before
// the exit and the whole document is out first.
//
// The property that prevents the truncation is precisely that: writeFully must
// not resolve until the stream has acknowledged the write. Whether a given
// machine truncates an un awaited write is a race between the write draining and
// the exit, so it cannot be asserted directly without being flaky. This asserts
// the property that removes the race instead, deterministically, with a stream
// whose acknowledgement this test controls.

test('writeFully does not resolve until the write is acknowledged', async () => {
  let ack: ((err?: Error | null) => void) | undefined;
  const stream = {
    write(_data: string, cb: (err?: Error | null) => void): boolean {
      ack = cb; // Hold the acknowledgement so the test decides when it fires.
      return false; // Report backpressure, the case that would truncate on exit.
    },
  } as unknown as NodeJS.WriteStream;

  let settled = false;
  const done = writeFully(stream, 'the document').then(() => {
    settled = true;
  });

  // Give any premature resolution a chance to run. A writeFully that returned
  // without waiting for the callback would have settled by now.
  await Promise.resolve();
  await Promise.resolve();
  assert.equal(settled, false, 'writeFully resolved before the write was acknowledged, so a process that exits on its resolution would truncate the document');

  assert.ok(ack, 'writeFully did not pass a callback to the stream, so it cannot know when the write drained');
  ack();
  await done;
  assert.equal(settled, true, 'writeFully did not resolve once the write was acknowledged');
});

test('writeFully rejects when the write reports an error rather than hanging', async () => {
  const stream = {
    write(_data: string, cb: (err?: Error | null) => void): boolean {
      cb(new Error('pipe closed'));
      return false;
    },
  } as unknown as NodeJS.WriteStream;
  await assert.rejects(writeFully(stream, 'x'), /pipe closed/);
});

// A real process across a real pipe, to show the fix delivers a document past
// the pipe buffer whole. It asserts only full delivery, which the fix
// guarantees on every machine; it does not assert that the un awaited write
// truncates, because whether it does is the very race the fix removes.
test('the runner delivers a document larger than the pipe buffer whole', async () => {
  const PAYLOAD = 256 * 1024;
  const here = dirname(fileURLToPath(import.meta.url));
  const emitModule = join(here, '..', 'src', 'emit.ts');
  const dir = mkdtempSync(join(tmpdir(), 'af-emit-'));
  const script = join(dir, 'writer.ts');
  writeFileSync(
    script,
    `import { writeFully } from ${JSON.stringify(emitModule)};\n` +
      `await writeFully(process.stdout, 'x'.repeat(${PAYLOAD}));\n` +
      `process.exit(0);\n`,
  );
  const child = spawn(process.execPath, ['--experimental-strip-types', script], {
    stdio: ['ignore', 'pipe', 'inherit'],
  });
  let bytes = 0;
  child.stdout.on('data', (c: Buffer) => {
    bytes += c.length;
  });
  await new Promise((resolve) => child.on('close', resolve));
  assert.equal(bytes, PAYLOAD, `the drained write lost ${PAYLOAD - bytes} bytes`);
});

test('replaceRegExp renders a pattern as its source rather than an empty object', () => {
  const out = JSON.parse(JSON.stringify({ press: /submit/i, name: 'go' }, replaceRegExp));
  assert.equal(out.press, 'submit', 'a RegExp value must serialise to its source, not {}');
  assert.equal(out.name, 'go', 'a plain value is untouched');
});
