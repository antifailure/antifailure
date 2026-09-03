// The one endpoint a browser on the marketing site may call.
//
// WHAT MAKES THIS SAFE TO EXPOSE WITH NO AUTHENTICATION.
//
// It cannot read anything. The application role holds INSERT on
// analytics_events and no SELECT, so the widest thing an attacker can do here
// is write rows that the catalog already bounds to a closed vocabulary. There
// is no identifier they can supply that names a real organization, because the
// events this endpoint accepts declare `organization: never` and the recorder
// refuses one that carries an organization at all.
//
// WHAT THEY CAN DO, SAID PLAINLY RATHER THAN WAVED AT.
//
// Inflate the numbers. Anybody can post page views claiming any source in the
// enum. The rate limit bounds how fast, and that is the whole defence, so these
// numbers are a floor and a shape rather than an audited count. That is stated
// on the dashboard next to them, because a number whose reliability is not
// written down next to it will be quoted as though it were audited.
//
// The alternative was authenticating the site, which means a credential in a
// static page, which is a credential everybody has. A shared secret in
// JavaScript is not authentication, it is a longer rate limit key.
//
// WHAT NEVER REACHES HERE.
//
// The raw referrer, the raw URL and the query string. The browser turns those
// into a source enum, a route id and a campaign id BEFORE the request is made,
// so the raw values never cross the network at all rather than crossing it and
// being discarded politely. See www/lib/analytics.ts.
//
// The session identifier that does arrive is random, generated per browsing
// session, held in sessionStorage so it dies with the tab, and hashed here
// before storage. It is never written down in its raw form anywhere.

import type { Context as HonoContext } from 'hono'
import type { Pool } from '@antifailure/db'
import type { Clock } from '../clock.ts'
import type { Analytics, AnalyticsEvent, RecordOutcome } from './record.ts'

/**
 * The most events one beacon may carry.
 *
 * Small on purpose. A page produces one view event and at most a handful of
 * engagements, and a tab that was asleep flushes what it buffered. Twenty
 * covers that with room to spare, and refusing a larger batch with a number is
 * better than truncating it, because truncation looks like success.
 */
export const SITE_BEACON_MAX_BATCH = 20

/** The wire shape. Deliberately not zod: this is four fields and the catalog
 *  does the real checking, so a schema here would be a second place for the
 *  rules to disagree. */
interface WireEvent {
  id?: unknown
  name?: unknown
  at?: unknown
  session?: unknown
  payload?: unknown
}

export interface BeaconResult {
  status: number
  body: Record<string, unknown>
}

export interface BeaconOptions {
  pool: Pool
  analytics: Analytics
  clock: Clock
  /**
   * How far a browser's clock may be wrong before its events are refused.
   *
   * The same reasoning as ingest.ts: a machine with a wrong clock is common,
   * and an event dated next year sorts to the top of every chart forever. A day
   * in either direction, because a beacon that a tab held while asleep is
   * hours old and legitimately so.
   */
  maxSkewMs?: number
}

const DEFAULT_MAX_SKEW_MS = 24 * 60 * 60 * 1000

/**
 * Handles one beacon.
 *
 * Every event is reported on individually and one bad event never discards the
 * good ones, which is the property the engine ingestion path has and the reason
 * it is worth having twice: a browser that starts sending one malformed event
 * would otherwise lose every real one alongside it, silently, for as long as
 * the bug lived.
 */
export async function siteBeacon(body: unknown, options: BeaconOptions): Promise<BeaconResult> {
  if (!options.analytics.enabled) {
    // 503 rather than 204. A beacon that is quietly discarded is
    // indistinguishable from one that worked, and the browser has no way to
    // learn that the numbers it is producing go nowhere.
    return {
      status: 503,
      body: { error: 'Analytics is not configured on this control plane.' },
    }
  }

  const raw = (body as { events?: unknown })?.events
  if (!Array.isArray(raw)) {
    return { status: 400, body: { error: 'The body needs an events array.' } }
  }
  if (raw.length === 0) {
    return { status: 202, body: { recorded: 0, duplicates: 0, rejected: 0, failed: 0 } }
  }
  if (raw.length > SITE_BEACON_MAX_BATCH) {
    return {
      status: 413,
      body: {
        error: `A beacon may carry ${SITE_BEACON_MAX_BATCH} events and this one carries ${raw.length}.`,
      },
    }
  }

  const skew = options.maxSkewMs ?? DEFAULT_MAX_SKEW_MS
  const now = options.clock.now()
  const events: AnalyticsEvent[] = []
  const preRejected: RecordOutcome[] = []

  for (const item of raw as WireEvent[]) {
    const problem = envelopeProblem(item, now, skew)
    if (problem) {
      preRejected.push({ status: 'rejected', problem: { reason: 'bad_envelope', detail: problem } })
      continue
    }
    events.push({
      eventId: String(item.id),
      // Not narrowed to an EventName here on purpose. The recorder is the one
      // place that decides whether a name is in the catalog, and a second check
      // here would be a second place for the answer to differ.
      name: String(item.name) as AnalyticsEvent['name'],
      occurredAt: new Date(String(item.at)),
      session: String(item.session),
      payload: (item.payload ?? {}) as Record<string, unknown>,
    })
  }

  const outcome = await options.pool.withoutTenant((db) => options.analytics.recordAll(db, events))

  const rejected = outcome.rejected + preRejected.length
  return {
    // 207 when some were refused, so a caller that only reads the status still
    // learns the batch was not wholly accepted. The site does not act on it;
    // a test does, and so does anybody with curl trying to work out why their
    // numbers are short.
    status: rejected > 0 ? 207 : 202,
    body: {
      recorded: outcome.recorded,
      duplicates: outcome.duplicates,
      rejected,
      failed: outcome.failed,
    },
  }
}

/**
 * The envelope, checked before the recorder sees it.
 *
 * Everything here is about the SHAPE of what arrived rather than its meaning,
 * because the recorder cannot report usefully on an occurredAt that is not a
 * date. Never quotes a value: this message reaches a log.
 */
function envelopeProblem(item: WireEvent, now: Date, skew: number): string | null {
  if (!item || typeof item !== 'object') return 'an event was not an object'
  if (typeof item.id !== 'string' || item.id.length === 0 || item.id.length > 100) {
    return 'an event carried no usable id'
  }
  if (typeof item.name !== 'string' || item.name.length === 0 || item.name.length > 64) {
    return 'an event carried no usable name'
  }
  if (typeof item.session !== 'string' || item.session.length < 8 || item.session.length > 64) {
    return 'an event carried no usable session identifier'
  }
  if (typeof item.at !== 'string') return 'an event carried no timestamp'
  const at = new Date(item.at)
  if (Number.isNaN(at.getTime())) return 'an event carried a timestamp that is not a timestamp'
  const drift = at.getTime() - now.getTime()
  if (drift > skew) return "an event is dated in the future; check the sending machine's clock"
  if (-drift > skew) return 'an event is dated more than a day in the past'
  if (item.payload !== undefined && (typeof item.payload !== 'object' || item.payload === null)) {
    return 'an event carried a payload that is not an object'
  }
  if (Array.isArray(item.payload)) return 'an event carried a payload that is an array'
  return null
}

/**
 * The cross-origin answer, for the one origin the site is served from.
 *
 * Reflecting the request's own Origin is the usual shortcut and it is wrong
 * here: it makes every page on the internet able to post to this endpoint from
 * a reader's browser. So the configured origin is compared and echoed, or
 * nothing is echoed and the browser refuses the request itself.
 *
 * No credentials, ever. The beacon carries no cookie, and saying so in the
 * headers is what stops a future change from quietly adding one.
 */
export function beaconCors(c: HonoContext, siteOrigin: string | null): boolean {
  const origin = c.req.header('origin')
  if (!siteOrigin || !origin || origin !== siteOrigin) return false
  c.header('access-control-allow-origin', siteOrigin)
  c.header('access-control-allow-methods', 'POST, OPTIONS')
  c.header('access-control-allow-headers', 'content-type')
  // A day, so a browser is not asking permission before every page view.
  c.header('access-control-max-age', '86400')
  // Vary, or a shared cache serves one origin's allow header to another.
  c.header('vary', 'origin')
  return true
}
