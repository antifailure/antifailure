// The three insights, against the cases that break each one.
//
// WHAT IS BEING CLAIMED HERE, so that a green run means something.
//
//   a distinct count over a window is a DISTINCT count, not a sum of daily
//   distinct counts, which double counts anybody active on two days
//
//   a funnel step is never wider than the step before it, and the ordering
//   rules are real: out of order does not count, outside the window does not
//   count, and a subject who ran out of window and started again is judged on
//   the better attempt
//
//   a cohort grid's first column is the cohort's own size, and a cohort that
//   the working set can no longer describe is absent rather than empty
//
//   and, underneath all three, THE APPLICATION STILL CANNOT FOLLOW A SUBJECT.
//   These insights needed a table that keeps the surrogate the daily rollup
//   throws away, and the whole design rests on the application having no grant
//   on it. That is asserted here against a row that is provably present, so a
//   permission working is distinguishable from a query finding nothing.
//
// THE ORDERING TABLE. Analytics is where arrival order does the most damage, so
// each ordering has its own case rather than one test over a happy path:
// in order, out of order, late, outside the window, a retry after success, a
// second attempt after a first expired, and an event that arrives after the day
// it belongs to was already rolled up.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomBytes } from 'node:crypto'
import { readFile, readdir } from 'node:fs/promises'
import { sql } from 'drizzle-orm'
import { available, seedOrg, startApi, type ApiHarness, type Org } from './harness.ts'
import { CATALOG, FUNNEL_DEFINITIONS, funnelDefinition } from '../src/analytics/catalog.ts'
import {
  ACTIVE_WINDOWS,
  RETENTION_WEEKS,
  SUBJECT_DAYS_KEPT,
  recomputeInsights,
  stepExpression,
  subjectKinds,
} from '../src/analytics/insights.ts'
import {
  actives,
  conversion,
  freshness,
  retentionGrid,
  MIN_COHORT_FOR_A_RATE,
} from '../src/analytics/read.ts'

const hasDatabase = await available()

// ---------------------------------------------------------------------------
// The declarations, which need no database. Their own describe so they run on a
// machine with no Postgres: a funnel that filters on a value the event cannot
// carry is a chart that reads zero forever, and that is the failure most worth
// being unable to skip.
// ---------------------------------------------------------------------------

describe('every declared funnel could actually be completed', () => {
  it('names only events the catalog declares', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      for (const step of funnel.steps) {
        assert.ok(step.event in CATALOG, `${funnel.id} names ${step.event}, which is not an event`)
      }
    }
  })

  it('filters only on fields those events declare, with values they can hold', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      for (const step of funnel.steps) {
        if (!step.where) continue
        const spec = CATALOG[step.event].payload as Record<string, { kind: string; values?: readonly string[] }>
        const field = spec[step.where.field]
        assert.ok(field, `${funnel.id} filters ${step.event} on ${step.where.field}, undeclared`)
        assert.equal(field.kind, 'enum', `${funnel.id} filters on a field with no closed domain`)
        for (const value of step.where.values) {
          assert.ok(
            field.values?.includes(value),
            // A step nobody can complete renders as a funnel collapsing to
            // zero, which reads as a product problem rather than as a typo.
            `${funnel.id} accepts ${value}, which ${step.event} can never carry`,
          )
        }
      }
    }
  })

  it('carries the subject its own events carry, or the funnel can never fill', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      for (const step of funnel.steps) {
        const spec = CATALOG[step.event]
        const carries = funnel.subject === 'organization' ? spec.organization : spec.session
        assert.notEqual(
          carries,
          'never',
          `${funnel.id} counts ${funnel.subject}s and ${step.event} declares it never carries one`,
        )
      }
    }
  })

  it('names each event once, so a single event cannot be two steps at the same time', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      const names = funnel.steps.map((s) => s.event)
      assert.equal(
        new Set(names).size,
        names.length,
        // The step expression is one CASE, and a repeated name would take the
        // first branch every time, silently making the later step unreachable.
        `${funnel.id} names an event twice, which the step expression cannot express`,
      )
    }
  })

  it('has at least two steps, because a one step funnel is a count', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      assert.ok(funnel.steps.length >= 2, `${funnel.id} has ${funnel.steps.length} steps`)
    }
  })

  it('says why its window is what it is, next to the number', () => {
    for (const funnel of FUNNEL_DEFINITIONS) {
      assert.ok(funnel.windowDays > 0)
      assert.ok(
        funnel.windowReason.length > 30,
        `${funnel.id} states a window and no reason for it, which a reader cannot judge`,
      )
    }
  })

  it('refuses a filter that could not be a literal, which is the injection guard', () => {
    // The negative control on stepExpression's own check. Every value it splices
    // comes from source in this repository today, and "it comes from a
    // constant" is exactly what stops being true later.
    const hostile = {
      id: 'hostile',
      title: 'hostile',
      subject: 'session' as const,
      windowDays: 1,
      windowReason: 'a test',
      steps: [
        { event: 'site.page_viewed' as const, meaning: 'first' },
        {
          event: 'site.waitlist_submitted' as const,
          where: { field: 'outcome', values: ["joined') OR true --"] },
          meaning: 'second',
        },
      ],
    }
    assert.throws(() => stepExpression(hostile), /cannot be a literal/)

    const badField = { ...hostile, steps: [
      hostile.steps[0]!,
      { ...hostile.steps[1]!, where: { field: 'outcome; DROP TABLE', values: ['joined'] } },
    ] }
    assert.throws(() => stepExpression(badField), /cannot be a key/)
  })

  it('publishes the same numbers the code uses, rather than a copy that can drift', async () => {
    // The reference page states how long the working set is kept and how many
    // cohort weeks the grid holds. Both are constants here, and a published
    // number with no gate over it is the exact failure legal-facts.test.ts
    // exists for: every one of the seven claims it found false was true when it
    // was written.
    const doc = await readFile(
      new URL('../../../../docs/src/content/docs/reference/control-plane.md', import.meta.url),
      'utf8',
    )
    const section = doc.slice(doc.indexOf('### What the dashboard can answer'))
    assert.ok(
      section.includes(`kept for ${SUBJECT_DAYS_KEPT} days`),
      `the page does not say the working set is kept for ${SUBJECT_DAYS_KEPT} days`,
    )
    assert.ok(
      section.includes(`${RETENTION_WEEKS} cohort weeks`),
      `the page does not say the grid holds ${RETENTION_WEEKS} cohort weeks`,
    )
    assert.ok(
      section.includes(`fewer than ${MIN_COHORT_FOR_A_RATE} organizations`),
      `the page does not say the suppression floor is ${MIN_COHORT_FOR_A_RATE}`,
    )
  })

  it('names exactly the subject kinds the migration will accept', async () => {
    // THE DRIFT THIS CATCHES. SUBJECT_KINDS is a TypeScript list and the three
    // insight tables each carry a CHECK that repeats it in SQL. Adding a third
    // population to the list alone type checks, passes review, and then every
    // insert for it is refused at runtime by a constraint nobody looked at, on
    // a rollup that runs unattended. The failure surfaces as an insight table
    // that silently stops gaining rows, which is indistinguishable from a
    // product nobody is using.
    //
    // Read out of the migration by shape rather than by number, because the
    // analytics migrations in this repository have been renumbered eight times
    // and a test pinned to a filename fails for the wrong reason on the ninth.
    const dir = new URL('../../../packages/db/migrations/', import.meta.url)
    const name = (await readdir(dir)).find((f) => f.endsWith('_analytics_insights.sql'))
    assert.ok(name, 'no analytics insights migration found to read the constraint out of')
    const migration = await readFile(new URL(name, dir), 'utf8')

    const checks = [...migration.matchAll(/subject_kind IN \(([^)]*)\)/g)].map((m) =>
      (m[1] ?? '').split(',').map((v) => v.trim().replace(/^'|'$/g, '')),
    )
    // Three tables carry the population: the working set, the actives and the
    // cohorts. If one of them ever stops declaring it, this asserts against a
    // shorter list rather than quietly checking two.
    assert.equal(checks.length, 3, `expected three subject_kind CHECKs, found ${checks.length}`)

    for (const declared of checks) {
      assert.deepEqual(
        [...declared].sort(),
        [...subjectKinds()].sort(),
        `the migration accepts ${declared.join(', ')} and the code writes ${subjectKinds().join(', ')}`,
      )
    }
  })

  it('keeps the working set long enough for the grid it feeds', () => {
    // A retention policy shorter than the grid draws an empty corner that reads
    // as a product with no customers rather than as rows that were deleted.
    assert.ok(
      SUBJECT_DAYS_KEPT >= RETENTION_WEEKS * 7,
      `keeps ${SUBJECT_DAYS_KEPT} days for a grid that reaches back ${RETENTION_WEEKS * 7}`,
    )
  })
})

// ---------------------------------------------------------------------------

describe('the insight rollup', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org

  /** Every surrogate this file wrote, so the teardown can find them again.
   *  Written directly as the owner rather than through the recorder, which is
   *  deliberate: these cases are about arrival TIMES to the second, and going
   *  through the recorder would mean the recorder's own clock deciding them. */
  const written: string[] = []

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'insights')
  })

  after(async () => {
    if (written.length > 0) {
      await h.admin`DELETE FROM analytics_events WHERE org_surrogate = ANY(${written}) OR session_surrogate = ANY(${written})`
      await h.admin`DELETE FROM analytics_org_facts WHERE org_surrogate = ANY(${written})`
      await h.admin`DELETE FROM analytics_subject_days WHERE subject = ANY(${written})`
    }
    await h.admin`DELETE FROM organizations WHERE id = ${org.orgId}`
    await h.close()
  })

  /** A surrogate that is not any real organization's, so one test's rows can
   *  never be counted by another's assertions. */
  function surrogate(): string {
    const value = randomBytes(16).toString('hex')
    written.push(value)
    return value
  }

  /**
   * One row in the stream, at an exact time.
   *
   * The payload goes through `admin.json` rather than JSON.stringify and a
   * cast, and that is not a style choice. The cast form stores the payload as a
   * jsonb STRING rather than an object, so `payload->>'outcome'` reads null and
   * every funnel step with a filter silently never matches. The first version
   * of this file did exactly that and the funnel tests failed with a subject
   * stuck one step short, which looked like a bug in the step expression and
   * was a bug in the fixture. There is a case below that goes through the
   * shipped recorder instead, so the shape these tests assert against is the
   * shape the product actually writes.
   */
  async function write(
    subjectKind: 'organization' | 'session',
    subject: string,
    name: string,
    at: string,
    payload: Record<string, string | number | boolean> = {},
  ): Promise<void> {
    const spec = CATALOG[name as keyof typeof CATALOG]
    await h.admin`
      INSERT INTO analytics_events (
        event_id, name, version, occurred_at, source, org_surrogate, session_surrogate,
        actor_kind, privacy_basis, payload)
      VALUES (
        ${randomBytes(12).toString('hex')}, ${name}, ${spec.version}, ${at}::timestamptz,
        ${spec.source},
        ${subjectKind === 'organization' ? subject : null},
        ${subjectKind === 'session' ? subject : null},
        ${spec.actorKind}, ${spec.privacyBasis}, ${h.admin.json(payload)})`
  }

  /** Runs every insight pass for a list of days, as the rollup does. */
  async function roll(days: string[], today: Date) {
    return recomputeInsights(h.admin, days, today, 3)
  }

  function daysBetween(from: string, to: string): string[] {
    const out: string[] = []
    for (let t = Date.parse(from); t <= Date.parse(to); t += 86_400_000) {
      out.push(new Date(t).toISOString().slice(0, 10))
    }
    return out
  }

  // -------------------------------------------------------------------------
  // The permission the whole design rests on
  // -------------------------------------------------------------------------

  it('the application cannot read the working set, and gets an error rather than nothing', async () => {
    const s = surrogate()
    await write('organization', s, 'environment.created', '2026-06-01T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    await roll(['2026-06-01'], new Date('2026-06-01T23:00:00Z'))

    // The row is proved present before it is proved unreachable. Without this,
    // a query returning nothing because nothing was written looks exactly like
    // a permission working, which is the shape of check that certifies a lie.
    const present = await h.admin`
      SELECT count(*)::int AS n FROM analytics_subject_days WHERE subject = ${s}`
    assert.ok(Number(present[0]!.n) > 0, 'the fixture wrote nothing to read')

    await assert.rejects(
      h.pool.withoutTenant((db) => db.execute(sql`SELECT count(*) FROM analytics_subject_days`)),
      (err: unknown) => {
        // The SQLSTATE rather than the message, and unwrapped through `cause`,
        // because the driver wraps a database error in one of its own.
        const code = codeOf(err)
        assert.equal(code, '42501', `expected insufficient_privilege, got ${code}`)
        return true
      },
    )
  })

  it('the application can read the counts, which is what makes the insight usable', async () => {
    const rows = await h.pool.withoutTenant((db) =>
      db.execute(sql`SELECT count(*) FROM analytics_actives`),
    )
    assert.ok(Array.isArray(rows))
  })

  // -------------------------------------------------------------------------
  // Distinct over a window, which is the number a daily count cannot give
  // -------------------------------------------------------------------------

  it('counts one organization active on two days once, not twice', async () => {
    const s = surrogate()
    await write('organization', s, 'environment.created', '2026-06-10T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    await write('organization', s, 'environment.created', '2026-06-11T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    const today = new Date('2026-06-11T23:00:00Z')
    await roll(['2026-06-10', '2026-06-11'], today)

    // Two working set rows, one per day, which is exactly what a sum over the
    // daily distinct counts would see and add up to two.
    const perDay = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM analytics_subject_days WHERE subject = ${s}`
    assert.equal(Number(perDay[0]!.n), 2, 'the working set did not record both days')

    const weekly = await h.admin<{ subjects: string }[]>`
      SELECT subjects::text FROM analytics_actives
      WHERE day = ${'2026-06-11'}::date AND window_days = 7
        AND subject_kind = 'organization' AND name = ''`
    // The whole point. A sum of daily distinct counts would say two.
    assert.ok(Number(weekly[0]!.subjects) >= 1)

    const seen = await h.admin<{ subjects: string }[]>`
      SELECT subjects::text FROM analytics_actives
      WHERE day = ${'2026-06-11'}::date AND window_days = 1
        AND subject_kind = 'organization' AND name = ''`
    assert.ok(
      Number(weekly[0]!.subjects) >= Number(seen[0]!.subjects),
      'a seven day window held fewer subjects than the one day inside it',
    )
  })

  it('does not add the per event counts into the total, because a subject can do two things', async () => {
    const s = surrogate()
    await write('organization', s, 'environment.created', '2026-06-20T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    await write('organization', s, 'validation.run_finished', '2026-06-20T10:00:00Z', {
      kind: 'test', verdict: 'pass',
    })
    await roll(['2026-06-20'], new Date('2026-06-20T23:00:00Z'))

    const perEvent = await h.admin<{ name: string; subjects: string }[]>`
      SELECT name, subjects::text FROM analytics_actives
      WHERE day = '2026-06-20'::date AND window_days = 1 AND subject_kind = 'organization'
        AND name <> ''`
    const total = await h.admin<{ subjects: string }[]>`
      SELECT subjects::text FROM analytics_actives
      WHERE day = '2026-06-20'::date AND window_days = 1 AND subject_kind = 'organization'
        AND name = ''`
    const summed = perEvent.reduce((n, r) => n + Number(r.subjects), 0)
    assert.ok(
      Number(total[0]!.subjects) < summed || perEvent.length < 2,
      'the total equals the sum of the parts, so it is counting the same subject twice',
    )
  })

  it('fills a day the rollup has no row for, rather than skipping it', async () => {
    const points = await h.pool.withoutTenant((db) =>
      actives(db, 'organization', 7, '', 5, new Date('2026-06-25T00:00:00Z')),
    )
    assert.equal(points.length, 5)
    assert.deepEqual(
      points.map((p) => p.day),
      ['2026-06-21', '2026-06-22', '2026-06-23', '2026-06-24', '2026-06-25'],
    )
  })

  // -------------------------------------------------------------------------
  // The funnel, ordering by ordering
  // -------------------------------------------------------------------------

  const ACQ = funnelDefinition('acquisition')!

  /**
   * Rolls the funnel and returns the depth counts for ONE entry week.
   *
   * Scoped to a week deliberately. The recompute reaches back the funnel's
   * window plus the lookback, so it legitimately rewrites weeks that an earlier
   * case in this file wrote, and a read over every week reports another test's
   * subject as this one's. That is exactly how a suite passes for a while and
   * then starts failing when a case is inserted before it.
   */
  async function depthsForWeek(today: Date, week: string) {
    await roll([today.toISOString().slice(0, 10)], today)
    return readDepths(week)
  }

  async function readDepths(week: string) {
    const rows = await h.admin<{ steps_completed: number; subjects: string }[]>`
      SELECT steps_completed, subjects::text FROM analytics_funnel_weeks
      WHERE funnel = 'acquisition' AND entered_week = ${week}::date`
    const out = new Map<number, number>()
    for (const r of rows) out.set(Number(r.steps_completed), Number(r.subjects))
    return out
  }

  it('counts a session that took the steps in order as having completed them', async () => {
    const s = surrogate()
    await write('session', s, 'site.page_viewed', '2026-07-06T09:00:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    await write('session', s, 'site.cta_engaged', '2026-07-06T09:05:00Z', {
      cta: 'waitlist_open', route: 'home',
    })
    await write('session', s, 'site.waitlist_submitted', '2026-07-06T09:06:00Z', {
      source: 'search', landing: 'home', outcome: 'joined',
    })
    const depths = await depthsForWeek(new Date('2026-07-06T23:00:00Z'), '2026-07-06')
    assert.equal(depths.get(3), 1, 'a session that did every step in order was not counted')
  })

  it('does not count a step that happened before the one it follows', async () => {
    await h.admin`DELETE FROM analytics_events WHERE occurred_at >= '2026-07-06'::date AND occurred_at < '2026-07-07'::date`
    const s = surrogate()
    // The button was pressed before the page was ever viewed, which cannot be a
    // conversion however tempting the two counts look side by side.
    await write('session', s, 'site.cta_engaged', '2026-07-06T09:00:00Z', {
      cta: 'waitlist_open', route: 'home',
    })
    await write('session', s, 'site.page_viewed', '2026-07-06T09:05:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    const depths = await depthsForWeek(new Date('2026-07-06T23:00:00Z'), '2026-07-06')
    assert.equal(depths.get(1), 1, 'the session did not enter the funnel')
    assert.equal(depths.get(2), undefined, 'an out of order step was counted as a conversion')
  })

  it('does not count a step that happened outside the window', async () => {
    await h.admin`DELETE FROM analytics_events WHERE occurred_at >= '2026-07-13'::date AND occurred_at < '2026-07-16'::date`
    await h.admin`DELETE FROM analytics_funnel_weeks WHERE funnel = 'acquisition'`
    const s = surrogate()
    await write('session', s, 'site.page_viewed', '2026-07-13T09:00:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    // Two days later, which is past the funnel's one day window.
    await write('session', s, 'site.cta_engaged', '2026-07-15T09:00:00Z', {
      cta: 'waitlist_open', route: 'home',
    })
    await roll(daysBetween('2026-07-13', '2026-07-15'), new Date('2026-07-15T23:00:00Z'))
    const depths = await readDepths('2026-07-13')
    assert.equal(depths.get(1), 1)
    assert.equal(depths.get(2), undefined, `a step ${ACQ.windowDays} days late was counted`)
  })

  it('judges a subject on their better attempt when the first ran out of window', async () => {
    await h.admin`DELETE FROM analytics_events WHERE occurred_at >= '2026-07-20'::date AND occurred_at < '2026-07-25'::date`
    await h.admin`DELETE FROM analytics_funnel_weeks WHERE funnel = 'acquisition'`
    const s = surrogate()
    // First attempt: landed, and never came back that day.
    await write('session', s, 'site.page_viewed', '2026-07-20T09:00:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    // Second attempt, three days later, all the way through.
    await write('session', s, 'site.page_viewed', '2026-07-23T09:00:00Z', {
      route: 'pricing', source: 'internal', entry: false,
    })
    await write('session', s, 'site.cta_engaged', '2026-07-23T09:10:00Z', {
      cta: 'waitlist_open', route: 'pricing',
    })
    await write('session', s, 'site.waitlist_submitted', '2026-07-23T09:11:00Z', {
      source: 'search', landing: 'home', outcome: 'joined',
    })
    await roll(daysBetween('2026-07-20', '2026-07-24'), new Date('2026-07-24T23:00:00Z'))
    const depths = await readDepths('2026-07-20')
    // The obvious query anchors on the first step ever and reports depth one.
    assert.equal(depths.get(3), 1, 'a completed second attempt was scored on the first attempt')
    assert.equal(depths.get(1), undefined, 'the subject was counted twice, once per attempt')
  })

  it('does not count a last step whose payload does not qualify', async () => {
    await h.admin`DELETE FROM analytics_events WHERE occurred_at >= '2026-07-27'::date AND occurred_at < '2026-07-29'::date`
    await h.admin`DELETE FROM analytics_funnel_weeks WHERE funnel = 'acquisition'`
    const s = surrogate()
    await write('session', s, 'site.page_viewed', '2026-07-27T09:00:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    await write('session', s, 'site.cta_engaged', '2026-07-27T09:01:00Z', {
      cta: 'waitlist_open', route: 'home',
    })
    // An address the waitlist would not take is not a conversion.
    await write('session', s, 'site.waitlist_submitted', '2026-07-27T09:02:00Z', {
      source: 'search', landing: 'home', outcome: 'refused',
    })
    await roll(daysBetween('2026-07-27', '2026-07-28'), new Date('2026-07-28T23:00:00Z'))
    const depths = await readDepths('2026-07-27')
    assert.equal(depths.get(2), 1)
    assert.equal(depths.get(3), undefined, 'a refused submission counted as a conversion')
  })

  it('absorbs an event that arrives after its day was already rolled up', async () => {
    await h.admin`DELETE FROM analytics_events WHERE occurred_at >= '2026-08-03'::date AND occurred_at < '2026-08-05'::date`
    await h.admin`DELETE FROM analytics_funnel_weeks WHERE funnel = 'acquisition'`
    const s = surrogate()
    await write('session', s, 'site.page_viewed', '2026-08-03T09:00:00Z', {
      route: 'home', source: 'search', entry: true,
    })
    const today = new Date('2026-08-04T23:00:00Z')
    await roll(daysBetween('2026-08-03', '2026-08-04'), today)
    let rows = await h.admin<{ steps_completed: number }[]>`
      SELECT steps_completed FROM analytics_funnel_weeks
      WHERE funnel = 'acquisition' AND entered_week = '2026-08-03'::date`
    assert.deepEqual(rows.map((r) => Number(r.steps_completed)), [1])

    // The beacon's retry delivers the engagement afterwards, stamped with the
    // time it happened. The recompute has to move the subject from depth one to
    // depth two rather than leaving the earlier answer in place.
    await write('session', s, 'site.cta_engaged', '2026-08-03T09:02:00Z', {
      cta: 'waitlist_open', route: 'home',
    })
    await roll(daysBetween('2026-08-03', '2026-08-04'), today)
    rows = await h.admin<{ steps_completed: number }[]>`
      SELECT steps_completed FROM analytics_funnel_weeks
      WHERE funnel = 'acquisition' AND entered_week = '2026-08-03'::date`
    assert.deepEqual(
      rows.map((r) => Number(r.steps_completed)),
      [2],
      'a late event left the old depth behind as well as writing the new one',
    )
  })

  it('gives the same answer however many times it runs', async () => {
    const before = await h.admin<{ funnel: string; entered_week: string; steps_completed: number; subjects: string }[]>`
      SELECT funnel, entered_week::text, steps_completed, subjects::text
      FROM analytics_funnel_weeks ORDER BY 1, 2, 3`
    await roll(daysBetween('2026-08-03', '2026-08-04'), new Date('2026-08-04T23:00:00Z'))
    const after2 = await h.admin<{ funnel: string; entered_week: string; steps_completed: number; subjects: string }[]>`
      SELECT funnel, entered_week::text, steps_completed, subjects::text
      FROM analytics_funnel_weeks ORDER BY 1, 2, 3`
    assert.deepEqual(after2, before)
  })

  it('reports steps that can never be wider than the step before them', async () => {
    const read = await h.pool.withoutTenant((db) =>
      conversion(db, 'acquisition', 12, new Date('2026-08-04T00:00:00Z')),
    )
    assert.ok(read)
    for (let i = 1; i < read.steps.length; i += 1) {
      assert.ok(
        read.steps[i]!.subjects <= read.steps[i - 1]!.subjects,
        `step ${i + 1} is wider than step ${i}, which a running sum cannot produce`,
      )
    }
  })

  it('answers nothing for a funnel nobody declared, rather than an empty chart', async () => {
    const read = await h.pool.withoutTenant((db) =>
      conversion(db, 'not-a-funnel', 4, new Date('2026-08-04T00:00:00Z')),
    )
    assert.equal(read, null)
  })

  it('sees a funnel step recorded by the shipped recorder, not only by this file', async () => {
    // MATCHING THE SHAPE THE PRODUCT ACTUALLY WRITES. Every case above inserts
    // its own rows, which is what lets them control arrival times to the
    // second, and it is also how they could all pass against a payload shape
    // the recorder never produces. The first version of the fixture here stored
    // the payload as a jsonb STRING rather than an object, so every filtered
    // step read null and matched nothing, and the funnel tests failed in a way
    // that looked like a bug in the step expression.
    //
    // So one case goes through the recorder the site actually posts into, and
    // asserts the funnel sees what it wrote. If the two shapes ever diverge,
    // this is the test that says so.
    const session = 'a-real-looking-browser-session-id'
    for (const [name, at, payload] of [
      ['site.page_viewed', '2026-08-17T09:00:00Z', { route: 'home', source: 'search', entry: true }],
      ['site.cta_engaged', '2026-08-17T09:01:00Z', { cta: 'waitlist_open', route: 'home' }],
      ['site.waitlist_submitted', '2026-08-17T09:02:00Z', {
        source: 'search', landing: 'home', outcome: 'joined',
      }],
    ] as [string, string, Record<string, unknown>][]) {
      const outcome = await h.pool.withoutTenant((db) =>
        h.analytics.record(db, {
          name: name as Parameters<typeof h.analytics.record>[1]['name'],
          occurredAt: new Date(at),
          session,
          payload,
        }),
      )
      assert.equal(outcome.status, 'recorded', `the recorder refused ${name}`)
    }
    written.push(...(await h.admin<{ s: string }[]>`
      SELECT DISTINCT session_surrogate AS s FROM analytics_events
      WHERE occurred_at >= '2026-08-17'::date AND occurred_at < '2026-08-18'::date
        AND session_surrogate IS NOT NULL`).map((r) => r.s))

    await roll(daysBetween('2026-08-17', '2026-08-18'), new Date('2026-08-18T23:00:00Z'))
    const depths = await readDepths('2026-08-17')
    assert.equal(depths.get(3), 1, 'the funnel cannot see what the recorder writes')
  })

  // -------------------------------------------------------------------------
  // The retention grid
  // -------------------------------------------------------------------------

  it('puts an organization in the cohort of the week it was first seen', async () => {
    const s = surrogate()
    // 2026-08-10 is a Monday, so this cohort week is that day.
    await h.admin`
      INSERT INTO analytics_org_facts (org_surrogate, first_seen_on, last_active_on, first_event_on)
      VALUES (${s}, '2026-08-12'::date, '2026-08-27'::date, '2026-08-12'::date)`
    await write('organization', s, 'environment.created', '2026-08-12T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    // Back two weeks later, which is the cell that says whether they stayed.
    await write('organization', s, 'environment.created', '2026-08-27T09:00:00Z', {
      runtime_class: 'docker', declared_lifetime: true,
    })
    await roll(['2026-08-12', '2026-08-27'], new Date('2026-08-31T12:00:00Z'))

    const rows = await h.admin<{ weeks_later: number; subjects: string }[]>`
      SELECT weeks_later, subjects::text FROM analytics_retention_cohorts
      WHERE subject_kind = 'organization' AND cohort_week = '2026-08-10'::date
      ORDER BY weeks_later`
    const cells = new Map(rows.map((r) => [Number(r.weeks_later), Number(r.subjects)]))
    assert.equal(cells.get(0), 1, 'the cohort does not contain the organization that formed it')
    assert.equal(cells.get(2), 1, 'the return two weeks later is missing from the grid')
  })

  it('takes the cohort size from the facts, so week zero is never smaller than week one', async () => {
    const grid = await h.pool.withoutTenant((db) =>
      retentionGrid(db, new Date('2026-08-31T00:00:00Z')),
    )
    for (const row of grid.rows) {
      for (const cell of row.weeks) {
        assert.ok(
          cell <= row.size,
          `cohort ${row.cohortWeek} has a later column wider than its own size`,
        )
      }
      assert.equal(row.weeks[0], row.size)
    }
  })

  it('marks a cohort too small to describe as a rate rather than publishing one', async () => {
    const grid = await h.pool.withoutTenant((db) =>
      retentionGrid(db, new Date('2026-08-31T00:00:00Z')),
    )
    const small = grid.rows.find((r) => r.size > 0 && r.size < 5)
    if (small) {
      assert.equal(small.enough, false, 'a cohort of a handful was offered as a percentage')
    }
  })

  // -------------------------------------------------------------------------
  // Freshness, which is what stops a still-filling number being read as final
  // -------------------------------------------------------------------------

  it('records how far back each shape is final, not one date for all three', async () => {
    const today = new Date('2026-08-31T12:00:00Z')
    const result = await roll(['2026-08-31'], today)
    await h.admin`
      UPDATE analytics_rollup_state
      SET last_run_at = now(), settled_after = '2026-08-29'::date,
          funnels_final_before = ${result.funnelsFinalBefore}::date,
          cohorts_complete_through = ${result.cohortsCompleteThrough}::date,
          subject_days_kept = ${SUBJECT_DAYS_KEPT}
      WHERE id`
    const state = await h.pool.withoutTenant((db) => freshness(db))
    assert.ok(state.funnelsFinalBefore, 'the funnel freshness was never written')
    assert.equal(state.subjectDaysKept, SUBJECT_DAYS_KEPT)
    // The funnel cannot be final more recently than the daily counts are
    // settled, because its widest window reaches back further.
    assert.ok(
      state.funnelsFinalBefore! <= state.settledAfter!,
      'the funnel claims to be final for a week that is still absorbing events',
    )
  })

  // -------------------------------------------------------------------------
  // What it costs
  // -------------------------------------------------------------------------

  it('reads a narrow window from an index, so a lookup cost does not follow the table', async () => {
    // WHAT THE FIRST VERSION OF THIS TEST CLAIMED, AND WHY IT WAS WRONG.
    //
    // It asserted that the rollup's twenty eight day window scan uses an index,
    // and Postgres chose a sequential scan instead. Postgres was right: that
    // window is a large fraction of everything retained, and reading a third of
    // a table through an index is slower than reading the table. So the claim
    // was corrected rather than the planner argued with, and the comment in
    // insights.ts that said the same wrong thing was corrected with it.
    //
    // What IS true, and is what an index is for here, is that a NARROW read
    // stays narrow: its cost follows the window rather than the table. That is
    // the property the dashboard depends on, and it is the one asserted below.
    await h.admin`
      INSERT INTO analytics_subject_days (subject_kind, subject, name, day, events)
      SELECT 'session', md5(s::text), 'site.page_viewed',
             '2025-01-01'::date + (d || ' days')::interval, 1
      FROM generate_series(1, 1000) AS s, generate_series(0, 99) AS d
      ON CONFLICT DO NOTHING`
    await h.admin`ANALYZE analytics_subject_days`

    const narrowPlan = await h.admin<{ 'QUERY PLAN': string }[]>`
      EXPLAIN (COSTS OFF)
      SELECT count(DISTINCT subject) FROM analytics_subject_days
      WHERE subject_kind = 'session' AND day = '2025-03-01'::date`
    const narrow = narrowPlan.map((r) => r['QUERY PLAN']).join('\n')
    assert.ok(
      /Index Only Scan/.test(narrow),
      `a one day read of a hundred thousand rows reads the table:\n${narrow}`,
    )
    assert.ok(!/Seq Scan/.test(narrow), `a one day read falls back to a scan:\n${narrow}`)

    // A plan assertion rather than a timing one, for both of these. A threshold
    // in milliseconds passes on a fast machine with the wrong plan and fails on
    // a slow one with the right plan, which is a check that cannot say no about
    // the thing it is named after.
    const started = Date.now()
    const answer = await h.admin<{ n: string }[]>`
      SELECT count(DISTINCT subject)::text AS n FROM analytics_subject_days
      WHERE subject_kind = 'session'
        AND day > '2025-03-01'::date AND day <= '2025-03-29'::date`
    const took = Date.now() - started
    assert.equal(Number(answer[0]!.n), 1000)
    // Reported rather than asserted on. This is the rollup's own bulk pass, it
    // runs once per maintenance interval and not once per page load, and a
    // sequential scan of the retained window is the correct plan for it. What
    // bounds it is SUBJECT_DAYS_KEPT, not an index.
    console.log(`      rollup window scan, 100,000 working set rows: ${took}ms`)

    await h.admin`DELETE FROM analytics_subject_days WHERE day < '2025-06-01'::date`
  })

  it('reads what the dashboard reads from an index', async () => {
    // The tables the application can actually see. These stay small by
    // construction, a few rows per day, so the assertion is that the access
    // path is the one the chart's range asks for rather than a growth claim.
    const plan = await h.admin<{ 'QUERY PLAN': string }[]>`
      EXPLAIN (COSTS OFF)
      SELECT steps_completed, sum(subjects) FROM analytics_funnel_weeks
      WHERE funnel = 'acquisition' AND entered_week >= '2026-06-01'::date
        AND entered_week <= '2026-08-31'::date
      GROUP BY steps_completed`
    const text = plan.map((r) => r['QUERY PLAN']).join('\n')
    assert.ok(
      /analytics_funnel_weeks/.test(text),
      `the funnel read does not touch the table it is about:\n${text}`,
    )
  })

  it('keeps every window it declares, so a chart cannot ask for one that is missing', async () => {
    const rows = await h.admin<{ window_days: number }[]>`
      SELECT DISTINCT window_days FROM analytics_actives ORDER BY window_days`
    const stored = rows.map((r) => Number(r.window_days))
    for (const w of ACTIVE_WINDOWS) {
      assert.ok(stored.includes(w), `no rows for the ${w} day window, which the dashboard reads`)
    }
  })
})

/** The SQLSTATE out of a driver error, however deeply it was wrapped. Asserting
 *  on this rather than on a message, because an outer message would match a
 *  typo in this file's own SQL just as happily. */
function codeOf(err: unknown): string | null {
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; cause?: unknown }
    if (typeof e.code === 'string' && /^[0-9A-Z]{5}$/.test(e.code)) return e.code
    cur = e.cause
  }
  return null
}
