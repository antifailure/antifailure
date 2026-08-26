// A token bucket, with the clock passed in.
//
// Shapes rather than refuses where it can. The ingestion path is the exception:
// an engine that is told to slow down retries, and an engine that is refused
// without a Retry-After retries immediately, so refusing without saying when is
// how a rate limit turns a burst into a storm.
//
// In memory, per process. That is honest about what it is: with several
// replicas the effective limit is the per-replica limit times the replica
// count. A shared limiter needs the database or a cache and buys accuracy that
// nothing here needs, since the purpose is to stop one engine from filling the
// queue rather than to bill anybody.

import type { Clock } from './clock.ts'

export interface Limit {
  /** Tokens added per second. */
  rate: number
  /** The most that can accumulate, which is the size of a burst. */
  burst: number
}

export interface Verdict {
  allowed: boolean
  /** Seconds to wait, rounded up, when refused. Never zero: a Retry-After of
   *  zero is an invitation to retry immediately, which is the thing being
   *  prevented. */
  retryAfterSeconds: number
  remaining: number
}

interface Bucket {
  tokens: number
  updatedAt: number
}

export class RateLimiter {
  private readonly buckets = new Map<string, Bucket>()

  private readonly clock: Clock
  private readonly limit: Limit
  /** Buckets untouched for this long are dropped, so a limiter keyed by token
   *  does not grow without bound as tokens are rotated. */
  private readonly idleMs: number

  constructor(clock: Clock, limit: Limit, idleMs = 10 * 60 * 1000) {
    this.clock = clock
    this.limit = limit
    this.idleMs = idleMs
  }

  take(key: string, cost = 1): Verdict {
    const now = this.clock.now().getTime()
    let bucket = this.buckets.get(key)
    if (!bucket) {
      bucket = { tokens: this.limit.burst, updatedAt: now }
      this.buckets.set(key, bucket)
    }

    const elapsedSeconds = Math.max(0, (now - bucket.updatedAt) / 1000)
    bucket.tokens = Math.min(this.limit.burst, bucket.tokens + elapsedSeconds * this.limit.rate)
    bucket.updatedAt = now

    if (bucket.tokens >= cost) {
      bucket.tokens -= cost
      return { allowed: true, retryAfterSeconds: 0, remaining: Math.floor(bucket.tokens) }
    }

    const shortfall = cost - bucket.tokens
    return {
      allowed: false,
      retryAfterSeconds: Math.max(1, Math.ceil(shortfall / this.limit.rate)),
      remaining: 0,
    }
  }

  /** Drops idle buckets. Called on a timer by the server. */
  sweep(): number {
    const cutoff = this.clock.now().getTime() - this.idleMs
    let removed = 0
    for (const [key, bucket] of this.buckets) {
      if (bucket.updatedAt < cutoff) {
        this.buckets.delete(key)
        removed += 1
      }
    }
    return removed
  }

  get size(): number {
    return this.buckets.size
  }
}
