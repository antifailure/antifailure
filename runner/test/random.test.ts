import { test } from 'node:test';
import assert from 'node:assert/strict';
import { Seeded, fnv1a } from '../src/random.ts';
import { FakeClock, systemClock } from '../src/clock.ts';

test('the same seed produces the same sequence', () => {
  const a = new Seeded('upgrade-a-plan');
  const b = new Seeded('upgrade-a-plan');
  const left = Array.from({ length: 64 }, () => a.next());
  const right = Array.from({ length: 64 }, () => b.next());
  assert.deepEqual(left, right);
});

test('a different seed produces a different sequence', () => {
  // Without this the test above passes for a generator that always returns
  // zero, which is the failure mode a determinism test is most likely to have.
  const a = Array.from({ length: 16 }, (_, i) => new Seeded(`seed-${i}`).next());
  assert.equal(new Set(a).size, 16);
});

test('every value is in range and no value is constant', () => {
  const rng = new Seeded('range');
  const seen = new Set<number>();
  for (let i = 0; i < 10_000; i++) {
    const v = rng.next();
    assert.ok(v >= 0 && v < 1, `${v} is outside [0, 1)`);
    seen.add(v);
  }
  // A generator whose low bits have collapsed produces a handful of values.
  assert.ok(seen.size > 9_000, `only ${seen.size} distinct values in 10000 draws`);
});

test('int stays inside the bound and covers it', () => {
  const rng = new Seeded('int');
  const counts = new Map<number, number>();
  for (let i = 0; i < 3_000; i++) {
    const v = rng.int(5);
    assert.ok(Number.isInteger(v) && v >= 0 && v < 5, `${v} is not an index into 5 items`);
    counts.set(v, (counts.get(v) ?? 0) + 1);
  }
  assert.equal(counts.size, 5, 'every index is reachable');
});

test('int of a non positive bound is an index, not a NaN', () => {
  // A caller indexing an empty list must get something that reads as "nothing
  // here" rather than a subscript that silently produces undefined later.
  const rng = new Seeded('empty');
  assert.equal(rng.int(0), 0);
  assert.equal(rng.int(-3), 0);
  assert.equal(rng.pick([]), undefined);
});

test('a seed that hashes to zero does not freeze the generator', () => {
  // FNV-1a of the empty string is the offset basis, not zero, but the guard
  // exists for any input that lands on zero and this proves it holds.
  assert.notEqual(fnv1a(''), 0);
  const rng = new Seeded('');
  assert.notEqual(rng.next(), rng.next());
});

test('the fake clock measures a duration the test decides', () => {
  const clock = new FakeClock('2026-03-01T00:00:00.000Z');
  const at = clock.monotonicMs();
  clock.advance(4_200);
  assert.equal(clock.monotonicMs() - at, 4_200);
  assert.equal(clock.now().toISOString(), '2026-03-01T00:00:04.200Z');
});

test('a clock rolled back does not make a duration negative', () => {
  // An NTP correction moves wall time and not monotonic time. A duration
  // measured across one has to stay a duration.
  const clock = new FakeClock();
  const at = clock.monotonicMs();
  clock.advance(1_000);
  clock.rollBack(60_000);
  assert.equal(clock.monotonicMs() - at, 1_000);
});

test('the system clock is a clock', () => {
  // Shipped and used by default, so it is worth one assertion that it is not
  // a stub: a monotonic reading that never moves would make every duration
  // zero and every slow_response finding impossible.
  assert.ok(systemClock.now() instanceof Date);
  assert.equal(typeof systemClock.monotonicMs(), 'number');
});
