// Time, as a dependency, for the same reason the control plane has one.
//
// An exploration measures how long the application took to answer, and a slow
// answer is one of the things it reports. A measurement taken from the real
// clock cannot be asserted on: the test would have to make the application
// genuinely slow, which means a real sleep, which means a test suite that
// takes minutes to prove one number. So the exploration takes every duration
// from here, and a test hands it a clock it moves by hand.
//
// Narrower than web/apps/api/src/clock.ts on purpose. That one carries now()
// and sleep() because session expiry, token lifetimes and the reaper are about
// wall time. Nothing in the runner is: it measures durations and nothing else,
// so this carries one method. Shipping the other two because the shapes ought
// to match would be shipping three unused methods and calling it symmetry.
//
// The declared workflow path in execute.ts still calls Date.now directly.
// Changing that is its own change and would touch the one part of the runner
// that is proven against real browsers.

export interface Clock {
  /** Milliseconds since an arbitrary origin. Monotonic rather than wall time,
   *  because wall time can go backwards under an NTP correction and a duration
   *  cannot. */
  monotonicMs(): number;
}

export const systemClock: Clock = {
  monotonicMs: () => Number(process.hrtime.bigint() / 1_000_000n),
};

/** A clock the test moves by hand. */
export class FakeClock implements Clock {
  #mono = 0;

  monotonicMs(): number {
    return this.#mono;
  }

  advance(ms: number): void {
    this.#mono += ms;
  }
}
