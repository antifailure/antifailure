// Time, as a dependency.
//
// Nothing in this application calls Date.now directly. Session expiry, rate
// limit windows, token TTLs, and the reaper that tears down environments past
// their lifetime are all things whose behaviour at the boundary is the whole
// question, and a test that has to wait an hour to ask is a test nobody runs.
//
// The real clock is three lines. The point is that there is somewhere else to
// pass.

export interface Clock {
  now(): Date
  /** Milliseconds since an arbitrary origin, for measuring durations. Separate
   *  from now() because wall time can go backwards and a duration cannot. */
  monotonicMs(): number
  sleep(ms: number): Promise<void>
}

export const systemClock: Clock = {
  now: () => new Date(),
  monotonicMs: () => Number(process.hrtime.bigint() / 1_000_000n),
  sleep: (ms) => new Promise((resolve) => setTimeout(resolve, ms)),
}

/** A clock the test moves by hand. */
export class FakeClock implements Clock {
  private current: number
  private mono = 0

  constructor(start: Date | string = '2026-01-01T00:00:00.000Z') {
    this.current = new Date(start).getTime()
  }

  now(): Date {
    return new Date(this.current)
  }

  monotonicMs(): number {
    return this.mono
  }

  advance(ms: number): void {
    this.current += ms
    this.mono += ms
  }

  /** Moves wall time backwards without moving monotonic time, which is what an
   *  NTP correction looks like and what a clock-rollback check has to survive. */
  rollBack(ms: number): void {
    this.current -= ms
  }

  async sleep(ms: number): Promise<void> {
    this.advance(ms)
  }
}
