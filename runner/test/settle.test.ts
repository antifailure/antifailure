// The watch every accessibility tree surface keeps on its screen before it is
// judged. The loops that use it are tested in desktop.test.ts and
// mobile.test.ts, through runDesktop and runMobile, because that is what
// ships. These are the properties of the watch itself that no loop test can
// isolate: what counts as still, what counts as changed, and what a verdict
// says about the watching.

import { test } from 'node:test';
import assert from 'node:assert/strict';
import { settle, withSettling, Patience } from '../src/drivers/settle.ts';
import type { Snapshot } from '../src/workflow.ts';

function screen(text: string, over: Partial<Snapshot> = {}): Snapshot {
  return {
    url: 'desktop://fixture', title: 'Fixture',
    fields: [], controls: [], submits: [], unnamed: 0, text, ...over,
  };
}

test('a screen the platform marks busy is never still, however still it looks', async () => {
  // Every read is identical. Only the platform's own "still working" separates
  // this from a finished screen, and it has to win.
  const busy = screen('Reading the ledger', { busy: true });
  const settled = await settle(async () => busy, busy, { budgetMs: 100, quietMs: 5 });
  assert.equal(settled.still, false);
  assert.equal(settled.changed, false);
});

test('an unchanging screen is still after one matching read', async () => {
  let reads = 0;
  const same = screen('Journal');
  const settled = await settle(async () => { reads++; return same; }, same, { budgetMs: 1_000, quietMs: 5 });
  assert.equal(settled.still, true);
  assert.equal(reads, 1);
});

test('waiting for a change returns the changed screen only once it has held still', async () => {
  // Loading, then two different drawings of the result, then the result held.
  // The half drawn one in the middle must not be what comes back.
  const sequence = [screen('Loading'), screen('Journal 1 entry'), screen('Journal 2 entries'), screen('Journal 2 entries')];
  let i = 0;
  const settled = await settle(async () => sequence[Math.min(i++, sequence.length - 1)]!, screen('Loading'),
    { budgetMs: 1_000, quietMs: 5, untilChanged: true });
  assert.equal(settled.still, true);
  assert.equal(settled.changed, true);
  assert.equal(settled.snapshot.text, 'Journal 2 entries');
});

test('a spent patience reads nothing more and judges what it has', async () => {
  let reads = 0;
  const patience = new Patience({ budgetMs: 60, quietMs: 5 });
  let n = 0;
  const moving = async () => { reads++; n++; return screen(`Tick ${n}`); };
  await patience.first(moving);
  const before = reads;
  const settled = await patience.beforeVerdict(moving, screen('Tick 0'));
  assert.equal(reads, before, 'a spent budget still read the screen again');
  assert.equal(settled.waitedMs < 20, true);
});

test('a verdict says whether the screen it is about had finished changing', () => {
  const failed = { cause: 'expectation-not-met', detail: '"x" was not found.' };
  const snapshot = screen('Tick 9');
  assert.match(withSettling(failed, { snapshot, still: false, changed: true, waitedMs: 10_000 }).detail,
    /still changing when it was judged: it did not hold still within the 10\.0 s/);
  assert.match(withSettling(failed, { snapshot, still: true, changed: false, waitedMs: 10_000 }).detail,
    /watched for 10\.0 s before this verdict and had stopped changing/);
  // A pass carries no excuse, and a verdict nobody waited for adds nothing.
  const passed = { cause: 'succeeded', detail: 'Every expectation is visible on the page.' };
  assert.equal(withSettling(passed, { snapshot, still: false, changed: true, waitedMs: 10_000 }), passed);
  assert.equal(withSettling(failed, { snapshot, still: true, changed: false, waitedMs: 0 }), failed);
});
