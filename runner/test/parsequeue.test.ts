// The queue between a pseudo terminal and the emulator. Its contract is
// order, completeness, and a cost per BATCH rather than per chunk: the last of
// those is the Windows defect it exists for, where every write costs a timer
// tick of 15.6 ms and ConPTY delivers a few dozen bytes at a time.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { ParseQueue, MAX_BATCH } from '../src/drivers/terminal.ts';

/** A writer that records what it was handed and takes a timer tick per call,
 *  which is the cost the real emulator charges. */
function recorder() {
  const writes: string[] = [];
  const write = async (data: string) => {
    writes.push(data);
    await new Promise<void>((resolve) => setTimeout(resolve, 5));
  };
  return { writes, write };
}

test('chunks that arrive while a write is in flight are handed over together, in order', async () => {
  const { writes, write } = recorder();
  const queue = new ParseQueue(write);
  const chunks = Array.from({ length: 200 }, (_, i) => `line ${i}\n`);
  for (const chunk of chunks) queue.push(chunk);
  await queue.settled;
  // Two writes is the honest bound: the first push finds the queue idle and
  // is handed over alone, and everything after it accumulates behind it. One
  // write per chunk would be two hundred ticks, the cost that made a ten
  // thousand line burst take minutes to draw on Windows.
  assert.ok(writes.length <= 2, `${writes.length} writes for ${chunks.length} chunks`);
  assert.equal(writes.join(''), chunks.join(''));
  assert.equal(queue.parsedBytes, chunks.join('').length);
});

test('a backlog larger than one batch is handed over in batches no larger than MAX_BATCH', async () => {
  // The batch bound is what keeps the parse the driver waits for after it has
  // stopped reading a short one, so a single huge batch would let a program
  // that wrote megabytes hold the driver past its budget.
  const { writes, write } = recorder();
  const queue = new ParseQueue(write);
  const big = 'x'.repeat(MAX_BATCH * 3 + 7);
  queue.push('first');
  queue.push(big);
  queue.push('last');
  await queue.settled;
  assert.ok(writes.every((w) => w.length <= MAX_BATCH), writes.map((w) => w.length).join(','));
  assert.equal(writes.join(''), 'first' + big + 'last');
});

test('a push after the queue drained starts a new round, so settled can tell the two apart', async () => {
  const { writes, write } = recorder();
  const queue = new ParseQueue(write);
  queue.push('a');
  const first = queue.settled;
  await first;
  queue.push('b');
  assert.notEqual(queue.settled, first, 'a later push reused the drained round, so a caller could not see it');
  await queue.settled;
  assert.equal(writes.join(''), 'ab');
});

test('close drops what is queued and waits only for the batch already handed over', async () => {
  const { writes, write } = recorder();
  const queue = new ParseQueue(write);
  queue.push('in flight');
  queue.push('queued behind it');
  queue.close();
  queue.push('after close');
  await queue.settled;
  assert.deepEqual(writes, ['in flight']);
});
