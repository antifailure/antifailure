// Time, as a dependency, for the same reason the control plane has one.
//
// An exploration measures how long the application took to answer, and a slow
// answer is one of the things it reports. A measurement taken from the real
// clock cannot be asserted on: the test would have to make the application
// genuinely slow, which means a real sleep, which means a test suite that
// takes minutes to prove one number. So the exploration takes every duration
// from here, and a test hands it a clock it moves by hand.
//
// Deliberately identical in shape to web/apps/api/src/clock.ts rather than
// clever, because two clocks that behave differently are worse than two
// clocks. The declared workflow path in execute.ts still calls Date.now
// directly; changing that is its own change and would touch the one part of
// the runner that is proven against real browsers.

export interface Clock {
  now(): Date;
  /** Milliseconds since an arbitrary origin, for measuring durations. Separate
   *  from now() because wall time can go backwards and a duration cannot. */
  monotonicMs(): number;
  sleep(ms: number): Promise<void>;
}

export const systemClock: Clock = {
  now: () => new Date(),
  monotonicMs: () => Number(process.hrtime.bigint() / 1_000_000n),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
};

/** A clock the test moves by hand. */
export class FakeClock implements Clock {
  #current: number;
  #mono = 0;

  constructor(start: Date | string = '2026-01-01T00:00:00.000Z') {
    this.#current = new Date(start).getTime();
  }

  now(): Date {
    return new Date(this.#current);
  }

  monotonicMs(): number {
    return this.#mono;
  }

  advance(ms: number): void {
    this.#current += ms;
    this.#mono += ms;
  }

  /** Moves wall time backwards without moving monotonic time, which is what an
   *  NTP correction looks like and what a duration must survive. */
  rollBack(ms: number): void {
    this.#current -= ms;
  }

  async sleep(ms: number): Promise<void> {
    this.advance(ms);
  }
}
