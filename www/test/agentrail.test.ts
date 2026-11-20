// The gate in front of Agentrail's browser recorder.
//
// The claim is about what is FETCHED, the same as test/posthog.test.ts: a
// reader who sends Global Privacy Control, Do Not Track or switched measurement
// off must cause no request for the vendor's code at all. So the resolver is
// where this watches, and it counts every attempt to load the package.
//
// One case goes the other way on purpose. A browser driven through WebDriver is
// left out of the counter and PostHog as noise, and for Agentrail it is the
// visitor: an AI agent driving a browser. If that case stops loading, the
// recorder has quietly stopped seeing the only thing it was installed for.

import { describe, it, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import { agentrailStub, resetAgentrailStub } from './agentrail-stub'

let vendorLoads = 0
const STUB = new URL('./agentrail-stub.ts', import.meta.url).href

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === 'agentrail-browser') {
      vendorLoads += 1
      return { url: STUB, shortCircuit: true }
    }
    return nextResolve(specifier, context)
  },
})

interface BrowserOptions {
  gpc?: boolean
  dnt?: string
  webdriver?: boolean
  userAgent?: string
  pathname?: string
}

/** As much of a browser as lib/beacon.ts and lib/agentrail.ts read. */
function install(options: BrowserOptions = {}) {
  const origin = 'https://antifailure.dev'
  const define = (key: string, value: unknown) =>
    Object.defineProperty(globalThis, key, { value, configurable: true, writable: true, enumerable: true })
  const asStore = (m: Map<string, string>) => ({
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  })
  const pathname = options.pathname ?? '/'
  const here = { search: '', pathname, href: `${origin}${pathname}`, hostname: 'antifailure.dev', origin }
  const doc = { referrer: '', visibilityState: 'visible', cookie: '', addEventListener() {}, querySelector: () => null }
  define('location', here)
  define('document', doc)
  define('window', { addEventListener() {}, location: here, history: { pushState() {}, replaceState() {} } })
  define('navigator', {
    userAgent: options.userAgent ?? 'Mozilla/5.0 (Macintosh) AppleWebKit/605 Safari/605',
    webdriver: options.webdriver ?? false,
    globalPrivacyControl: options.gpc,
    doNotTrack: options.dnt,
  })
  define('sessionStorage', asStore(new Map()))
  define('localStorage', asStore(new Map()))
  return doc
}

let moduleCount = 0

/** A fresh lib/agentrail.ts, which remembers that it started, over the beacon
 *  module node already holds. lib/agentrail.ts imports './beacon' with no query
 *  string, so it reads that shared instance, and the beacon caches its decision
 *  once per page. setMeasurement(true) clears the cache and any stored opt out,
 *  so each test's browser, installed just before, is what the gate reads. */
async function load() {
  moduleCount += 1
  const beacon: typeof import('../lib/beacon.ts') = await import('../lib/beacon')
  beacon.setMeasurement(true)
  const agentrail: typeof import('../lib/agentrail.ts') = await import(`../lib/agentrail.ts?instance=${moduleCount}`)
  return { beacon, agentrail }
}

describe('what loads Agentrail and what does not', () => {
  beforeEach(() => {
    vendorLoads = 0
    resetAgentrailStub()
  })

  it('starts for an ordinary reader, with behaviour and the fingerprint off', async () => {
    install()
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 1)
    const [options] = agentrailStub().starts
    assert.equal(options.behaviour, false)
    assert.equal(options.fingerprint, false)
    assert.equal(options.sessionCookie, 'agentrail_session')
    assert.equal(options.collector, 'https://api.agentrail.co/ingest/browser')
    assert.ok(String(options.key).startsWith('ajp_'))
    assert.equal(options.firstParty, undefined, 'naming the control plane would add traceparent and fail CORS preflights')
  })

  it('starts for a browser an agent drives, which the counter and PostHog skip', async () => {
    install({ webdriver: true })
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 1)
    assert.equal(agentrailStub().starts.length, 1)
  })

  it('fetches nothing when the browser sends Global Privacy Control', async () => {
    install({ gpc: true })
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 0)
  })

  it('fetches nothing when the browser sends Do Not Track', async () => {
    install({ dnt: '1' })
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 0)
  })

  it('fetches nothing for an agent whose browser also sends Global Privacy Control', async () => {
    install({ webdriver: true, gpc: true })
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 0)
  })

  it('fetches nothing for a reader who switched measurement off', async () => {
    install()
    const { beacon, agentrail } = await load()
    beacon.setMeasurement(false)
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 0)
  })

  it('fetches nothing on a draft preview', async () => {
    install({ pathname: '/cms-preview/' })
    const { agentrail } = await load()
    await agentrail.startAgentrail()
    assert.equal(vendorLoads, 0)
  })

  it('stops and deletes its cookie when the reader switches measurement off mid visit', async () => {
    const doc = install()
    const { beacon, agentrail } = await load()
    const unwatch = agentrail.watchAgentrail()
    try {
      await agentrail.startAgentrail()
      assert.equal(agentrailStub().starts.length, 1)
      beacon.setMeasurement(false)
      assert.equal(agentrailStub().stops, 1)
      assert.match(doc.cookie, /^agentrail_session=; Max-Age=0; Path=\/;/)
    } finally {
      unwatch()
    }
  })
})
