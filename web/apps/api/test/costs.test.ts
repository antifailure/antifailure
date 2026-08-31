// The cap arithmetic. Pure, so every boundary is reachable without a database
// and without waiting for a rolling window to move.

import { describe, expect, it } from 'vitest'

import { PLAN_COST_CAPS, capsFor, checkCostCap, hours } from '../src/costs.ts'

const free = PLAN_COST_CAPS.free!

describe('checkCostCap', () => {
  it('admits an ordinary run on the free plan', () => {
    // The default runtime.ttl is 24h and the free per-run cap is 24, so the
    // ordinary case of one environment for one branch is never refused. If
    // this ever fails, the default lifetime and the cap have drifted apart and
    // the product refuses its own defaults.
    const verdict = checkCostCap('free', 24, 0)
    expect(verdict.allowed).toBe(true)
    expect(verdict.kind).toBeNull()
  })

  it('refuses a run that alone exceeds the per-run cap', () => {
    const verdict = checkCostCap('free', 720, 0)
    expect(verdict.allowed).toBe(false)
    expect(verdict.kind).toBe('per-run')
  })

  // The three things a refusal has to carry, or the customer files a support
  // ticket instead of fixing it themselves.
  it('names the cap, the usage, and who can raise it', () => {
    const verdict = checkCostCap('free', 720, 0)
    expect(verdict.reason).toContain('720 hours')
    expect(verdict.reason).toContain('24 hours')
    expect(verdict.reason).toContain('owner')
    expect(verdict.reason).toContain('runtime.ttl')
    // And it says what did NOT happen, because a refusal that reads like a
    // failure sends somebody looking for the wreckage.
    expect(verdict.reason).toContain('Nothing was created and nothing was removed')
  })

  it('refuses when the day is already spent', () => {
    const verdict = checkCostCap('free', 24, free.perDayHours)
    expect(verdict.allowed).toBe(false)
    expect(verdict.kind).toBe('per-day')
    expect(verdict.reason).toContain('owner')
    expect(verdict.current).toBe(free.perDayHours)
    expect(verdict.limit).toBe(free.perDayHours)
  })

  // The boundary that decides whether a cap holds or is passed by exactly one
  // run every time. Checking the current usage rather than the projected total
  // admits a run that takes the organization over, on every plan, forever.
  it('refuses a run that would cross the cap rather than one that already has', () => {
    const room = free.perDayHours - 1
    expect(checkCostCap('free', 1, room).allowed).toBe(true)
    expect(checkCostCap('free', 2, room).allowed).toBe(false)
    expect(checkCostCap('free', 2, room).kind).toBe('per-day')
  })

  it('admits usage landing exactly on the cap', () => {
    // Exactly at the limit is inside it. The other reading refuses a run the
    // customer was told they could have.
    expect(checkCostCap('free', 24, free.perDayHours - 24).allowed).toBe(true)
  })

  it('reports the per-run cap first when both are broken', () => {
    // Both fail here, and per-run is the one the caller can fix in the same
    // breath by lowering runtime.ttl. The daily one only clears with time.
    const verdict = checkCostCap('free', 5_000, free.perDayHours)
    expect(verdict.kind).toBe('per-run')
  })

  it('falls back to the free caps for a plan it has never heard of', () => {
    // An organization whose plan column holds something unexpected gets the
    // tightest caps, not none. The other direction is an unknown string
    // disabling cost control entirely.
    expect(capsFor('enterprise-trial-2019')).toEqual(free)
    expect(checkCostCap('enterprise-trial-2019', 720, 0).allowed).toBe(false)
  })

  it('gives every plan a per-run cap inside its own daily cap', () => {
    // A per-run cap above the daily cap would mean a single run is refused by
    // the day it has not started yet, and the per-run number would be a lie.
    for (const [plan, cap] of Object.entries(PLAN_COST_CAPS)) {
      expect(cap.perRunHours, plan).toBeLessThanOrEqual(cap.perDayHours)
    }
  })
})

describe('hours', () => {
  it('reads as a sentence rather than as a float', () => {
    expect(hours(1)).toBe('1 hour')
    expect(hours(2)).toBe('2 hours')
    expect(hours(0.5)).toBe('30 minutes')
    expect(hours(1.005)).toBe('1 hour')
  })
})
