// Randomness, as a dependency, for the reason an exploration exists at all.
//
// An exploratory run has no declared script. When two controls on a page are
// equally good candidates it has to choose one, and that choice is the only
// thing standing between "a run that found something" and "a run somebody can
// reproduce". Math.random makes the second impossible: the finding arrives
// with a trace of a browser session that no longer exists and cannot be
// recreated, which is a bug report nobody can act on.
//
// So every choice an exploration makes comes from here, seeded by a string the
// manifest sets. The same seed against the same application takes the same
// path, step for step, and `af explore --seed` replays it.
//
// The algorithm is a 32 bit linear congruential generator with the constants
// from Numerical Recipes, and the low bits are discarded because an LCG's low
// bits have short periods. It is not cryptographic and must never be used
// where that matters: it exists to be reproducible, not unpredictable.

/** Seeded is a generator whose whole sequence follows from its seed. */
export class Seeded {
  #state: number;

  constructor(seed: string) {
    this.#state = fnv1a(seed);
  }

  /** next returns a number in [0, 1). */
  next(): number {
    // Kept in unsigned 32 bit space by >>> 0, because JavaScript's bitwise
    // operators are signed and a negative state would produce a negative
    // fraction on the very first call.
    this.#state = (Math.imul(this.#state, 1664525) + 1013904223) >>> 0;
    // The top 24 bits, so the poorly distributed low bits never reach a caller.
    return (this.#state >>> 8) / 0x1000000;
  }

  /** int returns a whole number in [0, n). Returns 0 for a non positive n, so
   *  a caller indexing an empty list gets an index rather than a NaN. */
  int(n: number): number {
    if (n <= 0) return 0;
    return Math.floor(this.next() * n) % n;
  }

  /** pick chooses one of the items, or undefined when there are none. */
  pick<T>(items: readonly T[]): T | undefined {
    if (items.length === 0) return undefined;
    return items[this.int(items.length)];
  }
}

/** fnv1a turns a seed string into a starting state.
 *
 * FNV-1a because it is four lines, has no lookup table, and gives two seeds
 * that differ by one character completely different states. A seed that
 * hashed to zero would leave the generator's first output fixed regardless of
 * the seed, so zero is moved off itself.
 */
export function fnv1a(s: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < s.length; i++) {
    h ^= s.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h === 0 ? 0x811c9dc5 : h;
}
