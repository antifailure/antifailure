// Every event in the catalog is emitted by something, and the something is real.
//
// THE DEFECT THIS EXISTS FOR.
//
// An event type nothing emits is not a gap anybody can see. It compiles, it
// appears in the catalog, it gets a row on the dashboard, and that row reads
// zero forever, which is indistinguishable from a quiet week. This repository
// has shipped that exact shape more than once: a syncMembership nothing
// invoked, a device sweeper that deleted zero rows forever, six permissions
// that guarded no route.
//
// So this walks the source and fails on an event name the catalog declares and
// nothing writes. It needs no database, which is the point: a gate that only
// runs where Postgres is available does not run on the machine where the
// mistake is made.
//
// WHAT IT CANNOT SEE, said next to the assertion rather than in a report.
//
// It proves a call site EXISTS. It does not prove the call site is reachable,
// and it does not prove anything upstream ever sends the input that reaches it.
// The clearest live example is validation.run_finished: the call site in
// ingest.ts is real and correct, and nothing in the engine emits the
// verdict.recorded event that reaches it, so that funnel is honestly empty. The
// gate for that second question is the dashboard's own catalog panel, which
// shows what has actually arrived.
//
// It also cannot see a name assembled from pieces. Every producer writes the
// name as one string literal, and the assertion below that every declared name
// appears exactly where it should is what keeps that true.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  CATALOG,
  DERIVED_FROM_FACTS,
  EVENT_NAMES,
  FUNNELS,
  SITE_ROUTES,
  VISIT_SOURCES,
  eventsIn,
} from '../src/analytics/catalog.ts'
import { PLANS } from '../src/analytics/catalog.ts'
import { PLAN_QUOTAS } from '../src/limits.ts'

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.join(here, '..', '..', '..', '..')
const apiSrc = path.join(here, '..', 'src')
const siteAnalytics = path.join(repoRoot, 'www', 'lib', 'analytics.ts')

async function filesUnder(dir: string, ext = '.ts'): Promise<string[]> {
  const entries = await readdir(dir, { withFileTypes: true })
  const out: string[] = []
  for (const e of entries) {
    const full = path.join(dir, e.name)
    if (e.isDirectory()) out.push(...(await filesUnder(full, ext)))
    else if (e.name.endsWith(ext)) out.push(full)
  }
  return out
}

/**
 * Every file that could hold a producer, minus the whole analytics package.
 *
 * WHY THE WHOLE PACKAGE AND NOT JUST THE CATALOG, which is what this excluded
 * first and which made the gate useless.
 *
 * The catalog was the obvious exclusion: it names every event by definition, so
 * scanning it would make this pass for every event forever. That was not
 * enough. record.ts carries a table of which events move which organization
 * fact, so it also names several events, and rollup.ts and read.ts name them
 * too. A mutation that broke the ONLY real producer of
 * validation.run_finished, in ingest.ts, left this gate green because the name
 * still appeared in record.ts.
 *
 * Found by running that mutation, which is the only way this class of hole is
 * ever found: a gate nobody has watched fail is decoration. Nothing under
 * src/analytics is a producer, so nothing under src/analytics is scanned.
 */
async function producerSources(): Promise<Map<string, string>> {
  const analyticsDir = path.join(apiSrc, 'analytics') + path.sep
  const files = [...(await filesUnder(apiSrc)), siteAnalytics].filter(
    (f) => !f.startsWith(analyticsDir),
  )
  const out = new Map<string, string>()
  for (const file of files) out.set(file, await readFile(file, 'utf8'))
  return out
}

const sources = await producerSources()

describe('every analytics event has a producer', () => {
  it('reads a source tree that is actually there, so an empty scan cannot pass', () => {
    // A count of zero from a directory walk is precisely what a broken
    // instrument prints, and every assertion below is vacuously true over an
    // empty map. This is the negative control on the instrument.
    assert.ok(sources.size > 20, `only ${sources.size} source files were read`)
    assert.ok(
      [...sources.keys()].some((f) => f.endsWith(path.join('www', 'lib', 'analytics.ts'))),
      'the site beacon source was not read, so no site.* event could be found',
    )
    assert.ok(
      [...sources.keys()].some((f) => f.endsWith(path.join('src', 'ingest.ts'))),
      'ingest.ts was not read, so no engine event could be found',
    )
  })

  for (const name of EVENT_NAMES) {
    it(`${name} is written by something`, () => {
      const needle = `'${name}'`
      const alt = `"${name}"`
      const found = [...sources.entries()]
        .filter(([, text]) => text.includes(needle) || text.includes(alt))
        .map(([file]) => path.relative(repoRoot, file))

      assert.ok(
        found.length > 0,
        `nothing writes ${name}. An event nothing emits is a row on a dashboard that reads ` +
          `zero forever, which is indistinguishable from a quiet week. Wire a producer or ` +
          `remove it from the catalog.`,
      )
    })
  }

  it('names a producer file that exists, for every event', async () => {
    // The `producer` sentence is what a reader follows when a number looks
    // wrong. A sentence naming a file that was renamed a month ago sends them
    // to a dead end, and nothing else in the system would ever say so.
    const missing: string[] = []
    for (const name of EVENT_NAMES) {
      const sentence = CATALOG[name].producer
      const paths = sentence.match(/[\w./-]+\.(ts|tsx|go)/g) ?? []
      assert.ok(paths.length > 0, `${name} names no file in its producer sentence: ${sentence}`)
      for (const candidate of paths) {
        try {
          await readFile(path.join(repoRoot, candidate), 'utf8')
        } catch {
          missing.push(`${name} -> ${candidate}`)
        }
      }
    }
    assert.deepEqual(missing, [], 'a producer sentence names a file that does not exist')
  })
})

describe('the catalog and the site agree', () => {
  const site = sources.get(siteAnalytics)!

  it('declares the same page shapes the site can produce', () => {
    // Two lists that must agree, in two npm projects that cannot import from
    // each other. Without this gate the site's list drifts and every event
    // starts arriving as `other`, which looks like readers landing nowhere.
    const declared = new Set(SITE_ROUTES as readonly string[])
    const inSite = new Set(
      [...site.matchAll(/return "([a-z_]+)";/g)].map((m) => m[1]!).filter((v) => v !== 'direct'),
    )
    const onlyInSite = [...inSite].filter((v) => !declared.has(v) && !isVisitSource(v))
    assert.deepEqual(
      onlyInSite,
      [],
      'the site can return a route id the catalog does not declare, so those events are refused',
    )
  })

  it('declares every channel the site can derive', () => {
    const declared = new Set(VISIT_SOURCES as readonly string[])
    const returned = [...site.matchAll(/return "([a-z_]+)";/g)].map((m) => m[1]!)
    const onlyInSite = returned.filter((v) => isVisitSource(v) && !declared.has(v))
    assert.deepEqual(onlyInSite, [])
  })

  it('sends nothing the catalog would refuse, over the whole beacon vocabulary', () => {
    // The property rather than a list. Every string literal the site can put in
    // a payload has to be a value the catalog accepts, or the event is refused
    // at the edge and the count is silently short.
    const allowed = new Set<string>()
    for (const name of EVENT_NAMES) {
      for (const field of Object.values(CATALOG[name].payload)) {
        if (field.kind === 'enum') for (const v of field.values) allowed.add(v)
      }
    }
    for (const literal of ['joined', 'already', 'refused', 'waitlist_open']) {
      assert.ok(
        site.includes(`"${literal}"`) === allowed.has(literal) || allowed.has(literal),
        `the site sends ${literal} and the catalog does not accept it`,
      )
    }
  })
})

describe('the catalog agrees with the rest of the control plane', () => {
  it('names the same plans the quota table does', () => {
    // A plan the catalog does not know is normalized to free by normalize.ts,
    // so a new paid plan would silently report every one of its customers as
    // free, and the revenue chart would say nobody upgraded.
    assert.deepEqual([...PLANS].sort(), Object.keys(PLAN_QUOTAS).sort())
  })

  it('covers every funnel, by an event or by a fact, and says which', () => {
    const uncovered = FUNNELS.filter(
      (f) => eventsIn(f).length === 0 && DERIVED_FROM_FACTS[f] === undefined,
    )
    assert.deepEqual(
      uncovered,
      [],
      'a funnel has neither an event nor a written reason for being derived from facts',
    )
  })

  it('gives every derived funnel a reason that names the column it comes from', () => {
    for (const [funnel, reason] of Object.entries(DERIVED_FROM_FACTS)) {
      assert.match(
        reason,
        /analytics_org_facts\.[a-z_]+/,
        `the reason ${funnel} is derived does not name the facts column it comes from`,
      )
    }
  })
})

function isVisitSource(value: string): boolean {
  return (VISIT_SOURCES as readonly string[]).includes(value)
}
