// The site beacon, against the orderings it actually meets.
//
// WHY THIS FILE EXISTS AT ALL. Every rule in lib/beacon.ts was written in a file
// that imported next/navigation, which does not resolve outside the bundler, so
// none of it could be loaded by a test runner and none of it was tested. The
// session was the tab, there was no retry, and a request that failed lost its
// event silently. All three were invisible for the same reason: nothing could
// run the code.
//
// WHAT IS STUBBED, AND WHY IT IS NOT A MOCK OF THE THING UNDER TEST. The browser
// globals are stubbed: storage, navigator, location, fetch and the clock. The
// beacon itself is imported and run for real, including its queue, its timers
// and its backoff. So a test here fails when the beacon's rules change, which is
// the only property that makes a test worth having.
//
// EVERY TEST GETS A FRESH MODULE. lib/beacon.ts holds a queue, a decision about
// whether this page is measured, and a stopped flag, all at module scope,
// because that is what a page needs. A suite that shared one instance would have
// tests that pass in isolation and fail in order, so each test imports the
// module under its own query string and gets its own state.

import { describe, it, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import type { StoredSession } from '../lib/beacon'

// ---------------------------------------------------------------------------
// The browser, as much of it as the beacon touches
// ---------------------------------------------------------------------------

interface Sent {
  url: string
  body: { events: WireEvent[] }
  via: 'fetch' | 'beacon'
  /** What the server answered. A request that was made and refused is still a
   *  request, and the difference between "asked" and "delivered" is what most
   *  of the retry cases are about. */
  status: number
}

interface WireEvent {
  id: string
  name: string
  at: string
  session: string
  payload: Record<string, string | boolean>
}

interface Harness {
  sent: Sent[]
  /** What the next fetch answers with. A function so a test can change its mind
   *  between attempts, which is the whole point of the retry cases. */
  respond: (attempt: number) => number | 'throw'
  attempts: number
  now: number
  /** Timers the beacon scheduled, smallest delay first. */
  timers: { at: number; run: () => void }[]
  storage: Map<string, string>
  local: Map<string, string>
  /** Set to make every storage accessor throw, which is what a browser in
   *  private mode does rather than returning null. */
  storageThrows: boolean
  listeners: Map<string, (() => void)[]>
  visibility: 'visible' | 'hidden'
}

let h: Harness

function throwingStore(get: () => Map<string, string>) {
  return {
    getItem(key: string): string | null {
      if (h.storageThrows) throw new DOMException('denied')
      return get().get(key) ?? null
    },
    setItem(key: string, value: string): void {
      if (h.storageThrows) throw new DOMException('denied')
      get().set(key, value)
    },
    removeItem(key: string): void {
      if (h.storageThrows) throw new DOMException('denied')
      get().delete(key)
    },
  }
}

function install(options: {
  userAgent?: string
  webdriver?: boolean
  gpc?: boolean
  dnt?: string
  href?: string
  referrer?: string
} = {}): void {
  h = {
    sent: [],
    respond: () => 202,
    attempts: 0,
    now: Date.UTC(2026, 8, 2, 12, 0, 0),
    timers: [],
    storage: new Map(),
    local: new Map(),
    storageThrows: false,
    listeners: new Map(),
    visibility: 'visible',
  }

  const url = new URL(options.href ?? 'https://antifailure.dev/pricing')
  // defineProperty rather than assignment, because Node defines `navigator` as
  // a getter-only global and a plain assignment throws. Every stub goes through
  // the same path so that adding one later cannot hit the same wall.
  const g = new Proxy({} as Record<string, unknown>, {
    set(_target, key, value) {
      Object.defineProperty(globalThis, key, {
        value,
        configurable: true,
        writable: true,
        enumerable: true,
      })
      return true
    },
  })

  g.location = { search: url.search, pathname: url.pathname, href: url.href, hostname: url.hostname }
  g.document = {
    referrer: options.referrer ?? '',
    get visibilityState() {
      return h.visibility
    },
    addEventListener(name: string, fn: () => void) {
      h.listeners.set(name, [...(h.listeners.get(name) ?? []), fn])
    },
  }
  g.window = {
    addEventListener(name: string, fn: () => void) {
      h.listeners.set(name, [...(h.listeners.get(name) ?? []), fn])
    },
  }
  g.navigator = {
    userAgent: options.userAgent ?? 'Mozilla/5.0 (Macintosh) AppleWebKit/605 Safari/605',
    webdriver: options.webdriver ?? false,
    globalPrivacyControl: options.gpc,
    doNotTrack: options.dnt,
    sendBeacon(target: string, blob: { text?: () => Promise<string>; body?: string }) {
      h.sent.push({
        url: target,
        body: JSON.parse((blob as unknown as { body: string }).body),
        via: 'beacon',
        status: 202,
      })
      return true
    },
  }
  g.sessionStorage = throwingStore(() => h.storage)
  g.localStorage = throwingStore(() => h.local)

  // A Blob that remembers what it was given, because the beacon builds one and
  // sendBeacon is where the body has to be readable again.
  g.Blob = class {
    body: string
    type: string
    constructor(parts: string[], init?: { type?: string }) {
      this.body = parts.join('')
      this.type = init?.type ?? ''
    }
  }

  g.fetch = async (target: string, init: { body: string }) => {
    const attempt = h.attempts
    h.attempts += 1
    const answer = h.respond(attempt)
    if (answer === 'throw') throw new TypeError('network')
    h.sent.push({ url: target, body: JSON.parse(init.body), via: 'fetch', status: answer })
    return { status: answer } as Response
  }

  g.setTimeout = ((fn: () => void, ms: number) => {
    const timer = { at: h.now + ms, run: fn }
    h.timers.push(timer)
    return timer as unknown as NodeJS.Timeout
  }) as typeof setTimeout

  g.clearTimeout = ((timer: unknown) => {
    h.timers = h.timers.filter((t) => t !== timer)
  }) as typeof clearTimeout

  g.Date = class extends Date {
    constructor(...args: unknown[]) {
      if (args.length === 0) super(h.now)
      else super(args[0] as number)
    }
    static now() {
      return h.now
    }
  } as unknown as DateConstructor
}

/** Moves the clock forward and runs every timer that is now due, the way a
 *  browser would. Loops, because a retry schedules the next retry. */
function advance(ms: number): void {
  const target = h.now + ms
  for (let guard = 0; guard < 50; guard += 1) {
    const due = h.timers.filter((t) => t.at <= target).sort((a, b) => a.at - b.at)
    if (due.length === 0) break
    const next = due[0]!
    h.timers = h.timers.filter((t) => t !== next)
    h.now = Math.max(h.now, next.at)
    next.run()
  }
  h.now = target
}

/** Waits for the promises the flush chain is made of. The beacon's flush is
 *  async and nothing awaits it, so a test that asserted immediately would race
 *  it. Four turns covers a chunked flush and its retry decision. */
async function settle(): Promise<void> {
  for (let i = 0; i < 8; i += 1) await Promise.resolve()
}

let moduleCount = 0

/** A beacon with its own module state. */
async function loadBeacon(): Promise<typeof import('../lib/beacon.ts')> {
  moduleCount += 1
  return import(`../lib/beacon.ts?instance=${moduleCount}`)
}

/** Requests the server actually accepted. A five hundred is a request that was
 *  made and is not an event that arrived, and conflating the two is how a retry
 *  test passes without a retry. */
function delivered(): Sent[] {
  return h.sent.filter((s) => s.status < 400)
}

function events(): WireEvent[] {
  return delivered().flatMap((s) => s.body.events)
}

// ---------------------------------------------------------------------------

describe('the beacon decides whether to measure at all', () => {
  beforeEach(() => install())

  it('says nothing when Global Privacy Control is set', async () => {
    install({ gpc: true })
    const beacon = await loadBeacon()
    beacon.pageViewed('pricing')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0)
  })

  it('says nothing when Do Not Track is set', async () => {
    install({ dnt: '1' })
    const beacon = await loadBeacon()
    beacon.pageViewed('pricing')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0)
  })

  it('says nothing for a crawler that runs JavaScript', async () => {
    install({
      userAgent:
        'Mozilla/5.0 (compatible; GPTBot/1.2; +https://openai.com/gptbot)',
    })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0, 'a declared crawler was measured')
  })

  it('says nothing for a driven browser, which is this repository own test runs', async () => {
    install({ webdriver: true })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0)
  })

  it('measures an ordinary reader, which is the negative control on all four above', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('pricing')
    advance(4000)
    await settle()
    assert.equal(events().length, 1)
    assert.equal(events()[0]!.name, 'site.page_viewed')
  })

  it('remembers an opt out across the tab, which sessionStorage could not', async () => {
    const first = await loadBeacon()
    first.setMeasurement(false)
    // A fresh module is a fresh page load. The preference has to survive it,
    // which is the whole reason it is not in sessionStorage with the session.
    const second = await loadBeacon()
    second.pageViewed('home')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0)
    assert.equal(h.local.get('af.analytics.optout.v1'), 'off')
  })

  it('stops measuring the moment the reader opts out, not on the next page load', async () => {
    // THE ORDERING THIS IS ABOUT: opt out AFTER capture has already started.
    // setMeasurement is exported, in its own words, "so a control on the
    // privacy page can call it". This site navigates on the client, so pressing
    // that control does not reload the module: the reader stays on the same
    // beacon instance that has already decided it is measuring. If the decision
    // is cached and never invalidated, the control appears to work, writes the
    // preference, and the beacon keeps sending until the tab is closed.
    //
    // The existing opt out case cannot see this. It opts out on a fresh module
    // and then loads a SECOND one, which is a page reload, and a reload is the
    // one path where the cache is rebuilt anyway.
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(10_000)
    await settle()
    assert.equal(events().length, 1, 'the reader was being measured before opting out')

    beacon.setMeasurement(false)
    beacon.pageViewed('pricing')
    beacon.ctaEngaged('waitlist_open')
    advance(10_000)
    await settle()
    assert.equal(events().length, 1, 'the beacon kept sending after the reader opted out')
  })

  it('does not send what was already queued when the reader opts out', async () => {
    // The queue holds events for up to three seconds, so an opt out lands on a
    // beacon with unsent events in hand more often than not. Sending them
    // because they were captured a moment before the reader objected is the
    // same disclosure the opt out was pressed to prevent.
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    // Deliberately no advance: the flush timer has not fired, so the event is
    // still in the queue rather than on the wire.
    assert.equal(h.sent.length, 0, 'the event should still be queued')

    beacon.setMeasurement(false)
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0, 'the queue was flushed after the reader opted out')
  })

  it('takes the opt out from a link, so excluding a colleague needs no install', async () => {
    install({ href: 'https://antifailure.dev/?af-analytics=off' })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(10_000)
    await settle()
    assert.equal(h.sent.length, 0)
    assert.equal(h.local.get('af.analytics.optout.v1'), 'off')
  })

  it('clears the opt out from the opposite link rather than recording a consent', async () => {
    install({ href: 'https://antifailure.dev/?af-analytics=on' })
    h.local.set('af.analytics.optout.v1', 'off')
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(events().length, 1)
    // Cleared rather than set to "on". The absence of an objection is not a
    // consent and this file must not be able to record one.
    assert.equal(h.local.has('af.analytics.optout.v1'), false)
  })
})

describe('the session is computed rather than left to the tab', () => {
  beforeEach(() => install())

  it('holds one identifier across pages inside the timeout', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    h.now += 20 * 60 * 1000
    beacon.pageViewed('pricing')
    advance(4000)
    await settle()
    const seen = events()
    assert.equal(seen.length, 2)
    assert.equal(seen[0]!.session, seen[1]!.session)
    assert.equal(seen[0]!.payload.entry, true)
    assert.equal(seen[1]!.payload.entry, false, 'a second page is not an arrival')
  })

  it('starts a new session after thirty minutes idle, which the tab never did', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    h.now += 31 * 60 * 1000
    beacon.pageViewed('pricing')
    advance(4000)
    await settle()
    const seen = events()
    assert.equal(seen.length, 2)
    assert.notEqual(seen[0]!.session, seen[1]!.session)
    assert.equal(seen[1]!.payload.entry, true, 'the new session did not count as an arrival')
  })

  it('starts a new session after a day, however busy the reader was', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    // Kept alive by activity every twenty minutes, so the idle rule never
    // fires. Only the maximum length can end this one.
    for (let i = 0; i < 24 * 3; i += 1) {
      h.now += 20 * 60 * 1000
      beacon.pageViewed('product')
    }
    advance(4000)
    await settle()
    const seen = events()
    const ids = new Set(seen.map((e) => e.session))
    assert.ok(ids.size >= 2, 'a tab open for a day held one identifier for the whole day')
  })

  it('ends the session when the clock jumps backwards rather than extending it', async () => {
    const beacon = await loadBeacon()
    const stored: StoredSession = {
      id: 'a'.repeat(32),
      startedAt: h.now,
      lastSeenAt: h.now + 60 * 60 * 1000,
      attribution: { source: 'search', landing: 'home', campaign: null },
    }
    // A negative idle is not evidence of activity, so the rule reads the
    // distance rather than the difference.
    assert.equal(beacon.sessionEnded(stored, h.now), 'idle')
  })

  it('recomputes attribution for the session that replaces an expired one', async () => {
    install({ referrer: 'https://news.ycombinator.com/item?id=1' })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(events()[0]!.payload.source, 'news')

    // The reader came back to the tab an hour later. They arrived from wherever
    // they are now, and carrying the morning's channel onto the afternoon would
    // attribute a second visit to a first one.
    ;(globalThis as unknown as { document: { referrer: string } }).document.referrer = ''
    h.now += 90 * 60 * 1000
    beacon.pageViewed('pricing')
    advance(4000)
    await settle()
    assert.equal(events()[1]!.payload.source, 'direct')
  })

  it('holds the arrival channel through the visit, which is what attribution means', async () => {
    install({ referrer: 'https://chatgpt.com/', href: 'https://antifailure.dev/' })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    h.now += 60_000
    beacon.waitlistSubmitted('joined')
    advance(4000)
    await settle()
    const submitted = events().find((e) => e.name === 'site.waitlist_submitted')!
    assert.equal(submitted.payload.source, 'ai', 'the submission lost the channel it arrived on')
    assert.equal(submitted.payload.landing, 'home')
  })

  it('records a reader whose browser refuses storage, rather than nothing at all', async () => {
    const beacon = await loadBeacon()
    h.storageThrows = true
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    // The old version returned early on the accessor throwing, so every reader
    // in private mode was invisible and the counts were short by an unknown
    // amount with nothing saying so.
    assert.equal(events().length, 1)
  })

  it('replaces a session record of the wrong shape rather than spreading it into an event', async () => {
    const beacon = await loadBeacon()
    h.storage.set('af.session.v1', JSON.stringify({ id: 7, attribution: 'nonsense' }))
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    const seen = events()
    assert.equal(seen.length, 1)
    assert.equal(typeof seen[0]!.session, 'string')
    assert.equal(seen[0]!.payload.entry, true)
  })
})

describe('the queue batches, and a retry cannot double count', () => {
  beforeEach(() => install())

  it('sends two events produced together in one request', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('signup')
    beacon.ctaEngaged('waitlist_open')
    advance(4000)
    await settle()
    assert.equal(h.sent.length, 1, 'two events took two requests')
    assert.equal(h.sent[0]!.body.events.length, 2)
  })

  it('splits a queue larger than the endpoint accepts rather than truncating it', async () => {
    const beacon = await loadBeacon()
    for (let i = 0; i < 25; i += 1) beacon.ctaEngaged('waitlist_open')
    advance(4000)
    await settle()
    assert.equal(h.sent.length, 2)
    assert.equal(h.sent[0]!.body.events.length, 20)
    assert.equal(h.sent[1]!.body.events.length, 5)
    assert.equal(events().length, 25, 'the batch over the limit was truncated')
  })

  it('retries a server error, and the retry is the same event rather than a new one', async () => {
    const beacon = await loadBeacon()
    h.respond = (attempt) => (attempt === 0 ? 500 : 202)
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(delivered().length, 0, 'a five hundred was treated as delivered')

    // The backoff is jittered, so this waits past its widest value rather than
    // asserting a delay the test would have to keep in step with.
    advance(10_000)
    await settle()
    assert.equal(delivered().length, 1)
    const arrived = delivered()[0]!.body.events[0]!
    assert.equal(arrived.name, 'site.page_viewed')
    // The whole reason a retry is safe: the id and the time are stamped once,
    // so the second copy collides with the first on the primary key and is
    // counted as a duplicate rather than added.
    assert.match(arrived.id, /^[0-9a-f]{32}$/)
  })

  it('retries a network failure, which is what an offline reader produces', async () => {
    const beacon = await loadBeacon()
    h.respond = (attempt) => (attempt === 0 ? 'throw' : 202)
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(delivered().length, 0)
    advance(10_000)
    await settle()
    assert.equal(delivered().length, 1)
  })

  it('does not retry a refusal, because a four hundred is not true on the third try', async () => {
    const beacon = await loadBeacon()
    h.respond = () => 400
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    const before = h.attempts
    advance(120_000)
    await settle()
    assert.equal(h.attempts, before, 'a refused batch was sent again')
  })

  it('treats a partial rejection as delivered, because resending cannot fix it', async () => {
    const beacon = await loadBeacon()
    h.respond = () => 207
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    const before = h.attempts
    advance(120_000)
    await settle()
    assert.equal(h.attempts, before)
  })

  it('stops asking when the control plane says it records nothing', async () => {
    const beacon = await loadBeacon()
    h.respond = () => 503
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(h.attempts, 1)
    beacon.pageViewed('pricing')
    advance(120_000)
    await settle()
    assert.equal(h.attempts, 1, 'the page kept asking a server that said it was not recording')
  })

  it('gives up rather than retrying forever', async () => {
    const beacon = await loadBeacon()
    h.respond = () => 500
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    for (let i = 0; i < 10; i += 1) {
      advance(120_000)
      await settle()
    }
    assert.ok(h.attempts <= 5, `gave up after ${h.attempts} attempts, which is not bounded`)
  })

  it('drops an event the server would refuse for being a day old', async () => {
    const beacon = await loadBeacon()
    h.respond = () => 'throw'
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    // A tab asleep for two days. The server refuses anything dated more than a
    // day away, so sending it would be a request made to be rejected.
    h.now += 2 * 24 * 60 * 60 * 1000
    h.respond = () => 202
    advance(120_000)
    await settle()
    assert.equal(events().length, 0)
  })

  it('flushes through sendBeacon when the reader leaves, not through a fetch', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    h.visibility = 'hidden'
    for (const fn of h.listeners.get('visibilitychange') ?? []) fn()
    assert.equal(h.sent.length, 1)
    assert.equal(h.sent[0]!.via, 'beacon', 'a fetch during unload is cancelled by some browsers')
    assert.equal(h.sent[0]!.body.events.length, 1)
  })

  it('flushes on pagehide as well, because neither event fires on every platform', async () => {
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    for (const fn of h.listeners.get('pagehide') ?? []) fn()
    assert.equal(h.sent.length, 1)
    assert.equal(h.sent[0]!.via, 'beacon')
  })

  it('sends the unload body as text, because sendBeacon cannot answer a preflight', async () => {
    const beacon = await loadBeacon()
    let type = ''
    ;(globalThis as unknown as { navigator: { sendBeacon: (u: string, b: { type: string; body: string }) => boolean } }).navigator.sendBeacon =
      (_url, blob) => {
        type = blob.type
        return true
      }
    beacon.pageViewed('home')
    for (const fn of h.listeners.get('pagehide') ?? []) fn()
    // application/json is not a CORS safelisted content type, so it would force
    // a preflight that sendBeacon cannot make, and the request would simply
    // never happen. The control plane parses the body as JSON either way.
    assert.match(type, /^text\/plain/)
  })

  it('flushes immediately when the reader comes back online', async () => {
    const beacon = await loadBeacon()
    h.respond = (attempt) => (attempt === 0 ? 'throw' : 202)
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(delivered().length, 0)
    // No advance past the backoff. Coming back online has to cancel the timer
    // that is armed and try now, and the previous version of this listener
    // scheduled alongside the pending one, which does nothing.
    for (const fn of h.listeners.get('online') ?? []) fn()
    advance(1)
    await settle()
    assert.equal(delivered().length, 1)
  })

  it('backs off further each time, and never past a minute', async () => {
    const beacon = await loadBeacon()
    // Jitter is plus or minus half, so the bounds are what can be asserted.
    for (let attempt = 0; attempt < 8; attempt += 1) {
      const delay = beacon.retryDelay(attempt)
      assert.ok(delay > 0, 'a retry delay of zero is a busy loop')
      assert.ok(delay <= 90_000, `attempt ${attempt} waited ${delay}ms, which outlives a visit`)
    }
    const early = beacon.retryDelay(0)
    const later = beacon.retryDelay(4)
    assert.ok(later > early, 'the backoff does not grow')
  })
})

describe('what the beacon puts on the wire', () => {
  beforeEach(() => install())

  it('carries no cookie and no credential', async () => {
    const beacon = await loadBeacon()
    let init: RequestInit | undefined
    const original = globalThis.fetch
    ;(globalThis as unknown as { fetch: unknown }).fetch = async (url: string, options: RequestInit) => {
      init = options
      return original(url, options)
    }
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(init?.credentials, 'omit')
  })

  it('sends a campaign identifier only when it matches the pattern', async () => {
    install({ href: 'https://antifailure.dev/?c=launch-2026' })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    assert.equal(events()[0]!.payload.campaign, 'launch-2026')
    assert.equal(events()[0]!.payload.source, 'campaign')
  })

  it('drops a campaign identifier that is not one, rather than truncating it', async () => {
    install({ href: 'https://antifailure.dev/?c=' + 'x'.repeat(80) })
    const beacon = await loadBeacon()
    beacon.pageViewed('home')
    advance(4000)
    await settle()
    // A truncated identifier is still an identifier.
    assert.equal(events()[0]!.payload.campaign, undefined)
  })

  it('never puts a referrer, a path or a query string on the wire', async () => {
    install({
      href: 'https://antifailure.dev/blog/why-a-green-ci-proves-nothing?utm_source=x&q=secret',
      referrer: 'https://internal.example.com/team/roadmap?ticket=4821',
    })
    const beacon = await loadBeacon()
    beacon.pageViewed('blog_post')
    advance(4000)
    await settle()
    const wire = JSON.stringify(h.sent)
    for (const forbidden of ['internal.example.com', 'roadmap', 'ticket', 'secret', 'utm_source', 'why-a-green-ci']) {
      assert.ok(!wire.includes(forbidden), `the wire carried ${forbidden}`)
    }
    assert.equal(events()[0]!.payload.route, 'blog_post')
    assert.equal(events()[0]!.payload.source, 'referral')
  })
})
