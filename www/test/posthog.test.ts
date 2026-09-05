// PostHog, against the two things the published copy depends on.
//
// WHAT THIS FILE IS FOR. Three documents on this site now say, in writing, that
// a session recording of the marketing site masks every input value and that a
// reader who has asked not to be tracked is not recorded. Both of those are
// properties of one configuration object and one `if`, and neither is visible
// in a build, in a type check, or in a page that renders correctly. So both are
// asserted here, against the real module rather than against a copy of it.
//
// WHAT IS NOT HERE. Whether posthog-js honours the configuration it is handed
// is PostHog's to test, and this file cannot prove it. What it proves is that
// the configuration this site hands over says what the copy says it says, and
// that the library is never fetched at all for a reader who declined. The rest
// is verified in a real browser against the built site, which is the only
// instrument that can watch the network.

import { describe, it, beforeEach } from 'node:test'
import assert from 'node:assert/strict'
import { registerHooks } from 'node:module'
import fs from 'node:fs'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { resetStub, stubRecord } from './posthog-stub'

// ---------------------------------------------------------------------------
// A resolver that counts, and refuses, every attempt to load the vendor.
//
// THE ONE OBSERVABLE THAT MATTERS. "The recorder does not start" is not the
// claim; a recorder that loads and is then stopped has already read the page.
// The claim is that nothing is FETCHED, and the only way to see a dynamic
// import from inside a test is to sit in the resolver. It answers with the stub
// in test/posthog-stub.ts rather than with the real bundle, so what the gate
// hands over is observable too, and a leak cannot pull a browser bundle into a
// node process and fail for a reason that reads like something else entirely.
// ---------------------------------------------------------------------------

let vendorLoads = 0

const STUB = new URL('./posthog-stub.ts', import.meta.url).href

registerHooks({
  resolve(specifier, context, nextResolve) {
    if (specifier === 'posthog-js') {
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
  origin?: string
}

/** As much of a browser as the gate reads, which is the same set lib/beacon.ts
 *  reads, because the gate asks the beacon rather than asking the browser a
 *  second way. */
function install(options: BrowserOptions = {}): void {
  const origin = options.origin ?? 'https://www.antifailure.dev'
  const store = new Map<string, string>()
  const local = new Map<string, string>()
  const define = (key: string, value: unknown) =>
    Object.defineProperty(globalThis, key, {
      value,
      configurable: true,
      writable: true,
      enumerable: true,
    })

  define('location', { search: '', pathname: '/', href: `${origin}/`, hostname: 'www.antifailure.dev', origin })
  define('document', { referrer: '', visibilityState: 'visible', addEventListener() {} })
  define('window', { addEventListener() {}, location: { origin } })
  define('navigator', {
    userAgent: options.userAgent ?? 'Mozilla/5.0 (Macintosh) AppleWebKit/605 Safari/605',
    webdriver: options.webdriver ?? false,
    globalPrivacyControl: options.gpc,
    doNotTrack: options.dnt,
  })
  const asStore = (m: Map<string, string>) => ({
    getItem: (k: string) => m.get(k) ?? null,
    setItem: (k: string, v: string) => void m.set(k, v),
    removeItem: (k: string) => void m.delete(k),
  })
  define('sessionStorage', asStore(store))
  define('localStorage', asStore(local))
}

let moduleCount = 0

/** A module with its own state. lib/posthog.ts remembers that it started, and
 *  lib/beacon.ts caches the decision, so a shared instance would give tests
 *  that pass alone and fail in order. */
async function load(): Promise<typeof import('../lib/posthog.ts')> {
  moduleCount += 1
  return import(`../lib/posthog.ts?instance=${moduleCount}`)
}

// ---------------------------------------------------------------------------

describe('the host the browser is told to send to', () => {
  beforeEach(() => install())

  it('turns a path into an absolute URL on this origin, because the library concatenates', async () => {
    // Not what this site ships, and still reachable: a fork whose host CAN
    // rewrite to an upstream sets a path here. This site is a static export on
    // Azure Static Web Apps with no server at runtime, so it cannot, and its
    // proxy is on the control plane at a different origin instead.
    const { resolveHost } = await load()
    assert.equal(
      resolveHost('/ingest', 'https://www.antifailure.dev'),
      'https://www.antifailure.dev/ingest',
    )
  })

  it('leaves an absolute host alone, which is what this site actually ships', async () => {
    const { resolveHost } = await load()
    assert.equal(
      resolveHost('https://app.antifailure.dev/ph', 'https://www.antifailure.dev'),
      'https://app.antifailure.dev/ph',
    )
  })

  it('never doubles a slash, whichever half carried it', async () => {
    const { resolveHost } = await load()
    assert.equal(resolveHost('/ingest/', 'https://www.antifailure.dev/'), 'https://www.antifailure.dev/ingest')
  })

  it('answers null for the empty string, which is how a build says it is switched off', async () => {
    const { resolveHost } = await load()
    assert.equal(resolveHost('', 'https://www.antifailure.dev'), null)
    assert.equal(resolveHost('   ', 'https://www.antifailure.dev'), null)
  })
})

describe('the configuration the published copy describes', () => {
  beforeEach(() => install())

  it('masks every input value, which is the sentence three documents now carry', async () => {
    // The careers form and the enterprise contact form take a name, a work
    // email, a company and a free paragraph. maskAllInputs is asserted rather
    // than trusted to the library default, because a default is somebody
    // else's decision and this one is load bearing on published copy.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.ok(options)
    assert.equal(options.session_recording?.maskAllInputs, true)
  })

  it('masks every input TYPE by name, so turning the coarse flag off cannot unmask a name', async () => {
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    const byType = options?.session_recording?.maskInputOptions ?? {}
    const values = Object.values(byType)
    assert.ok(values.length >= 16, `only ${values.length} input types are named`)
    for (const [type, masked] of Object.entries(byType)) {
      assert.equal(masked, true, `${type} is not masked`)
    }
  })

  it('sets no cookie and keeps no identifier past the tab, which the privacy page promises', async () => {
    // Two published sentences ride on this one line: "there is no cookie" and
    // "nothing here can join two of your visits". posthog-js defaults to
    // localStorage+cookie, which breaks both.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.equal(options?.persistence, 'sessionStorage')
    assert.equal(options?.opt_out_capturing_persistence_type, 'localStorage')
  })

  it('honours Do Not Track in the library as well as in the gate', async () => {
    const { posthogOptions } = await load()
    assert.equal(posthogOptions('https://www.antifailure.dev')?.respect_dnt, true)
  })

  it('sends to an endpoint we run and links to PostHog, which are not the same host', async () => {
    // ui_host pointing at the proxy would build every "open this in PostHog"
    // link as a path on the proxy that does not exist.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.equal(options?.api_host, 'https://app.antifailure.dev/ph')
    assert.equal(options?.ui_host, 'https://us.posthog.com')
    assert.notEqual(options?.ui_host, options?.api_host)
  })

  it('hands the library no separate host for the script bundles', async () => {
    // posthog-js routes /static and /array at a custom api_host on its own,
    // measured on the wire. So an asset host is redundant, and redundant is
    // not the reason it is absent: it is the single option that would put the
    // session replay recorder, the largest and most blockable request
    // posthog-js makes, back on a vendor address while ingest stayed healthy
    // and every check that reads api_host stayed green.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.equal(options?.asset_host, undefined)
  })

  it('names no PostHog ingestion host anywhere in what it hands the library', async () => {
    // The only posthog.com host allowed in this tree is ui_host, which is a
    // link a person clicks and the browser never fetches.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    const rest = JSON.stringify({ ...options, ui_host: undefined })
    assert.ok(!rest.includes('us-assets'), rest)
    assert.ok(!rest.includes('i.posthog.com'), rest)
    assert.ok(!rest.includes('app.posthog.com'), rest)
  })

  it('keeps autocapture and session replay on, which is the whole point of adding it', async () => {
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.equal(options?.autocapture, true)
    assert.equal(options?.disable_session_recording, false)
    assert.equal(options?.capture_pageview, 'history_change')
  })

  it('captures no heatmap and no page timing, which the remote config had turned on', async () => {
    // NEITHER OF THESE IS IN THE OPTIONS BY DEFAULT AND BOTH WERE HAPPENING.
    // One visit produced two $$heatmap events and three $web_vitals events
    // because the PROJECT's remote config enables them and remote config beats
    // an option nobody set, so the published list of what is captured was two
    // event types short the moment it was written. Found by decoding the wire,
    // not by reading the configuration.
    const { posthogOptions } = await load()
    const options = posthogOptions('https://www.antifailure.dev')
    assert.equal(options?.capture_heatmaps, false)
    assert.equal(options?.capture_performance, false)
  })

  it('never sends the raw user agent, which this repository promises elsewhere', async () => {
    // lib/bots.ts says the user agent is read in the page and never put on the
    // network, and that is the reason its crawler filter runs in the browser at
    // all. posthog-js attaches $raw_user_agent to every event, so without this
    // a claim made in another file would have quietly become false.
    const { posthogOptions } = await load()
    const denied = posthogOptions('https://www.antifailure.dev')?.property_denylist ?? []
    assert.ok(denied.includes('$raw_user_agent'), JSON.stringify(denied))
  })

  it('does not record inside a cross origin frame, because /contact embeds somebody else’s form', async () => {
    const { posthogOptions } = await load()
    assert.equal(posthogOptions('https://www.antifailure.dev')?.session_recording?.recordCrossOriginIframes, false)
  })
})

describe('the gate, which has to refuse before the recorder exists', () => {
  beforeEach(() => {
    vendorLoads = 0
    resetStub()
  })

  // ONE BEACON IS SHARED BY EVERY INSTANCE OF THE MODULE UNDER TEST, and that
  // is a property of the code rather than of the test: lib/posthog.ts imports
  // './beacon' with no query string, so a fresh lib/posthog.ts still reads the
  // beacon module node already has. The beacon caches its decision once per
  // page, deliberately, so without this the first test's answer would be the
  // answer every later test got, whatever browser it installed. Passing true
  // is not "turn it on": it clears the cache, and the next read recomputes
  // against whatever install() has just put on globalThis, which for the cases
  // below is a browser that says no.
  async function clearCachedDecision(): Promise<void> {
    const beacon = await import('../lib/beacon')
    beacon.setMeasurement(true)
  }

  it('fetches the library for a reader who is being measured', async () => {
    // The positive control. Without it every refusal below is satisfied by a
    // gate that refuses everybody, which is a check that cannot say yes.
    install()
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    assert.equal(vendorLoads, 1)
    assert.equal(stubRecord().inits.length, 1)
  })

  it('hands the vendor the project key and the masking, not a different object', async () => {
    // The configuration is asserted above as a value. This is the wiring:
    // that the value built there is the one actually handed over. A test of
    // posthogOptions alone passes while init is called with nothing at all.
    install()
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    const call = stubRecord().inits[0]
    assert.ok(call)
    assert.equal(call.key, posthog.POSTHOG_KEY)
    const recording = call.options.session_recording as { maskAllInputs?: boolean }
    assert.equal(recording.maskAllInputs, true)
    assert.equal(call.options.persistence, 'sessionStorage')
    assert.equal(call.options.api_host, 'https://app.antifailure.dev/ph')
  })

  it('fetches nothing at all for a browser sending Global Privacy Control', async () => {
    install({ gpc: true })
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    assert.equal(vendorLoads, 0)
    assert.equal(stubRecord().inits.length, 0)
  })

  it('fetches nothing at all for a browser sending Do Not Track', async () => {
    install({ dnt: '1' })
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    assert.equal(vendorLoads, 0)
    assert.equal(stubRecord().inits.length, 0)
  })

  it('fetches nothing at all for a reader who switched measurement off', async () => {
    install()
    const beacon = await import('../lib/beacon')
    beacon.setMeasurement(false)
    const posthog = await load()
    await posthog.startProductAnalytics()
    assert.equal(vendorLoads, 0)
    assert.equal(stubRecord().inits.length, 0)
  })

  it('fetches nothing at all for an automated browser', async () => {
    install({ webdriver: true })
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    assert.equal(vendorLoads, 0)
    assert.equal(stubRecord().inits.length, 0)
  })

  it('starts once however many times it is asked', async () => {
    install()
    await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    await posthog.startProductAnalytics()
    await posthog.startProductAnalytics()
    assert.equal(stubRecord().inits.length, 1)
  })
})

describe('the switch on the privacy page, which has to reach the vendor too', () => {
  beforeEach(() => {
    vendorLoads = 0
    resetStub()
  })

  async function clearCachedDecision(): Promise<typeof import('../lib/beacon.ts')> {
    const beacon = await import('../lib/beacon')
    beacon.setMeasurement(true)
    return beacon
  }

  it('ends the recording and stops the capture when a reader switches off mid visit', async () => {
    // THE ORDERING THIS IS FOR. A reader is measured, reads the privacy page,
    // and presses the switch. The beacon discards its own queue; nothing in
    // the beacon knows a recorder exists. Without the subscription this is the
    // case where the switch reads as working and a session recording carries
    // on to the end of the visit.
    install()
    const beacon = await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    const unwatch = posthog.watchMeasurement()
    assert.equal(stubRecord().inits.length, 1)

    beacon.setMeasurement(false)
    assert.equal(stubRecord().stopped, 1, 'the recording was not ended')
    assert.equal(stubRecord().optedOut, 1, 'capture was not stopped')
    unwatch()
  })

  it('throws away the recording it had already buffered, which opting out does not', async () => {
    // FOUND IN A BROWSER. Opting out left a 47KB snapshot of the page the
    // reader had just objected to in the recorder's buffer, and posthog-js's
    // unload handler sent it on the next navigation. The vendor's own discard
    // sits inside a branch that only runs in cookieless mode, so it has to be
    // called here or the switch sends the largest thing it captured.
    install()
    const beacon = await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    const unwatch = posthog.watchMeasurement()
    beacon.setMeasurement(false)
    assert.equal(stubRecord().discarded, 1, 'the buffered recording was kept')
    unwatch()
  })

  it('stops the queued events being flushed by the vendor on unload', async () => {
    // The other half of the same failure. Events captured in the seconds
    // before the press sit in a request queue that opting out does not empty,
    // and the unload handler flushes it only while request batching is on.
    install()
    const beacon = await clearCachedDecision()
    const posthog = await load()
    await posthog.startProductAnalytics()
    const unwatch = posthog.watchMeasurement()
    beacon.setMeasurement(false)
    const off = stubRecord().reconfigured.some((c) => c.request_batching === false)
    assert.ok(off, 'request batching was left on, so the unload handler still flushes the queue')
    unwatch()
  })

  it('starts for a reader who arrived opted out and then switched on', async () => {
    // The other direction, and the reason watchMeasurement is called before
    // startProductAnalytics in the component: the gate refused, so nothing
    // started, and a subscription registered only on success would not exist.
    install()
    const beacon = await import('../lib/beacon')
    beacon.setMeasurement(false)
    const posthog = await load()
    const unwatch = posthog.watchMeasurement()
    await posthog.startProductAnalytics()
    assert.equal(stubRecord().inits.length, 0, 'it started for a reader who had opted out')

    beacon.setMeasurement(true)
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.equal(stubRecord().inits.length, 1, 'the switch did not start it')
    unwatch()
  })

  it('does not start for a reader who presses the switch under Global Privacy Control', async () => {
    // The switch says yes and the browser says no. Starting here would record
    // a reader whose browser asked not to be tracked, and the recorder reads
    // the page on its first frame, so there is no stopping it afterwards.
    install({ gpc: true })
    const beacon = await import('../lib/beacon')
    const posthog = await load()
    const unwatch = posthog.watchMeasurement()
    beacon.setMeasurement(true)
    await new Promise((resolve) => setTimeout(resolve, 0))
    assert.equal(vendorLoads, 0)
    assert.equal(stubRecord().inits.length, 0)
    unwatch()
  })
})

describe('the option that cannot be allowed back, enforced by absence', () => {
  // WHY A SOURCE SCAN AND NOT A VALUE ASSERTION. Every other rule in this file
  // is checked by reading what posthogOptions returns, and that is the wrong
  // instrument for this one: `asset_host: undefined` and no asset_host at all
  // both read the same from the outside, and the leak is a variable somebody
  // sets in a deployment rather than a value in this tree. The failure this
  // guards is one repository variable away from being live, no gate on either
  // side of the lane reads it because every gate reads api_host, and the
  // request it moves is the one nobody inspects because ingest keeps working.
  //
  // WHAT IS SCANNED. The three directories next.config.ts exports into a
  // bundle. test/ is excluded for the same reason tools/routecheck excludes it:
  // an assertion that a string is absent has to be able to name the string, and
  // nothing under test/ reaches a browser.

  const www = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
  const shipped = ['lib', 'components', 'app']

  function sourceFiles(): string[] {
    const out: string[] = []
    const walk = (dir: string) => {
      for (const entry of fs.readdirSync(dir, { withFileTypes: true })) {
        const full = path.join(dir, entry.name)
        if (entry.isDirectory()) {
          if (['node_modules', '.next', 'out'].includes(entry.name)) continue
          walk(full)
        } else if (/\.(ts|tsx|js|jsx|mjs)$/.test(entry.name)) {
          out.push(full)
        }
      }
    }
    for (const dir of shipped) walk(path.join(www, dir))
    return out
  }

  it('scans a real and non empty set of shipped files, or it proves nothing', () => {
    // The positive control on the scanner itself. A walk that found no files
    // would pass both assertions below while checking nothing at all, which is
    // the shape of instrument this repository keeps having to throw away.
    const files = sourceFiles()
    assert.ok(files.length > 40, `only ${files.length} files scanned`)
    assert.ok(
      files.some((f) => f.endsWith(path.join('lib', 'posthog.ts'))),
      'the scan did not reach lib/posthog.ts, so it could not see the option it is about',
    )
  })

  it('no shipped file names the asset host option or the variable that would feed it', () => {
    const offenders: string[] = []
    for (const file of sourceFiles()) {
      const text = fs.readFileSync(file, 'utf8')
      for (const needle of ['asset' + '_host', 'NEXT_PUBLIC_POSTHOG_' + 'ASSET_HOST']) {
        if (text.includes(needle)) offenders.push(`${path.relative(www, file)} names ${needle}`)
      }
    }
    assert.deepEqual(offenders, [])
  })
})
