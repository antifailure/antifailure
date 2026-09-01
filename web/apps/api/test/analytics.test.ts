// The analytics contract, tested where it can actually fail.
//
// Three claims are made about this subsystem and each one is worthless unless a
// test has watched it hold under the case that breaks it:
//
//   the schema is CLOSED          an unknown name or field is refused and
//                                 counted, not stored and cleaned up later
//   the store is WRITE ONLY       the application role cannot read the stream
//                                 back, and gets an error rather than nothing
//   the answer is ORDER FREE      the same events in any order, duplicated, or
//                                 arriving before the organization exists,
//                                 produce the same facts
//
// The third is the one that needed the design to change. The activation
// milestone was an event until the ordering cases below showed two concurrent
// batches could both claim to be the first, so it became a column set with
// LEAST. Every ordering case here is written against that column.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { available, seedOrg, startApi, TEST_ANALYTICS_SECRET, type ApiHarness, type Org } from './harness.ts'
import { CATALOG, EVENT_NAMES, validatePayload } from '../src/analytics/catalog.ts'
import { rollUp } from '../src/analytics/rollup.ts'
import { createAnalytics, surrogateSecretFrom } from '../src/analytics/record.ts'
import { catalogStatus, freshness, organizationFunnel, retention, series } from '../src/analytics/read.ts'
import { FakeClock } from '../src/clock.ts'

const hasDatabase = await available()

// ---------------------------------------------------------------------------
// The catalog, which needs no database at all. Deliberately its own describe so
// it runs on a machine with no Postgres: the closed schema is the property most
// worth being unable to skip.
// ---------------------------------------------------------------------------

describe('the analytics catalog is closed', () => {
  it('refuses a field it does not declare rather than dropping it', () => {
    const out = validatePayload('site.page_viewed', {
      route: 'home',
      source: 'search',
      // The exact shape of the leak this whole subsystem exists to prevent.
      repository: 'antifailure/antifailure',
    })
    assert.equal(out.ok, false)
    assert.equal(out.ok === false && out.problem.reason, 'unknown_field')
  })

  it('refuses a value outside the enum, so the vocabulary cannot grow at a producer', () => {
    const out = validatePayload('site.page_viewed', { route: 'home', source: 'https://news.example/post' })
    assert.equal(out.ok, false)
    assert.equal(out.ok === false && out.problem.reason, 'bad_value')
  })

  it('refuses a campaign id that is not one, because the pattern is the domain', () => {
    const bad = validatePayload('site.page_viewed', {
      route: 'home', source: 'campaign', campaign: 'utm_source=x&utm_medium=y',
    })
    assert.equal(bad.ok, false)
    const good = validatePayload('site.page_viewed', {
      route: 'home', source: 'campaign', campaign: 'launch-2026',
    })
    assert.equal(good.ok, true)
  })

  it('never puts a rejected value in the message, because a message reaches a log', () => {
    const secret = 'ghp_averyrealisticlookingsecret'
    const out = validatePayload('site.page_viewed', { route: secret })
    assert.equal(out.ok, false)
    assert.ok(
      out.ok === false && !out.problem.detail.includes(secret),
      `the rejection quoted the value it refused: ${out.ok === false ? out.problem.detail : ''}`,
    )
  })

  it('declares no free-text field anywhere, which is what makes the rest of this true', () => {
    // The property, not a list that happens to have the property today. A field
    // kind added later that accepts arbitrary strings fails here.
    const kinds = new Set<string>()
    for (const name of EVENT_NAMES) {
      for (const field of Object.values(CATALOG[name].payload)) kinds.add(field.kind)
    }
    assert.deepEqual(
      [...kinds].filter((k) => !['enum', 'id', 'count', 'boolean'].includes(k)),
      [],
      'a payload field kind exists that is not one of the four bounded ones',
    )
  })

  it('gives every event a named producer and a question it answers', () => {
    const missing = EVENT_NAMES.filter(
      (n) => CATALOG[n].producer.trim().length === 0 || CATALOG[n].answers.trim().length === 0,
    )
    assert.deepEqual(missing, [])
  })

  it('rolls up under dimensions that are fields it actually declares', () => {
    // A dimension naming a field the payload does not have would roll up as the
    // empty string forever, which renders as one unlabelled bar on a chart and
    // looks like data.
    const wrong: string[] = []
    for (const name of EVENT_NAMES) {
      for (const dim of CATALOG[name].dimensions) {
        if (!Object.hasOwn(CATALOG[name].payload, dim)) wrong.push(`${name}.${dim}`)
      }
    }
    assert.deepEqual(wrong, [])
  })

  it('refuses a surrogate secret of the wrong length rather than hashing with a short key', () => {
    assert.throws(() => surrogateSecretFrom('abc'), /64 hex characters/)
    assert.equal(surrogateSecretFrom(undefined), null)
    assert.equal(surrogateSecretFrom('aa'.repeat(32))?.length, 32)
  })

  it('records nothing at all when no secret is configured', async () => {
    const off = createAnalytics({
      secret: null,
      clock: new FakeClock(),
      counters: { events: { inc() {} }, rejections: { inc() {} } },
    })
    assert.equal(off.enabled, false)
    assert.equal(off.surrogate('any-org'), null)
    // The database is never touched, so this passes with no Postgres. That is
    // the point: a caller does not have to check enabled before calling.
    const outcome = await off.record(null as never, {
      name: 'identity.signed_in',
      occurredAt: new Date(),
      payload: { method: 'github', first_time: true },
    })
    assert.equal(outcome.status, 'disabled')
  })
})

// ---------------------------------------------------------------------------

describe('the analytics stream', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let surrogate: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'analytics')
    surrogate = h.analytics.surrogate(org.orgId)!
  })

  after(async () => {
    await h.admin`DELETE FROM analytics_events WHERE org_surrogate = ${surrogate}`
    await h.admin`DELETE FROM analytics_org_facts WHERE org_surrogate = ${surrogate}`
    await h.admin`DELETE FROM organizations WHERE id = ${org.orgId}`
    await h.close()
  })

  /** Records one event through the shipped recorder, on the shipped pool. */
  async function record(event: Parameters<typeof h.analytics.record>[1]) {
    return h.pool.withoutTenant((db) => h.analytics.record(db, event))
  }

  /** Reads the stream as the OWNER, because the application cannot. */
  async function streamFor(orgSurrogate: string) {
    return h.admin<{ event_id: string; name: string; payload: Record<string, unknown> }[]>`
      SELECT event_id, name, payload FROM analytics_events
      WHERE org_surrogate = ${orgSurrogate} ORDER BY occurred_at, event_id`
  }

  async function factsFor(orgSurrogate: string) {
    const rows = await h.admin<Record<string, unknown>[]>`
      SELECT * FROM analytics_org_facts WHERE org_surrogate = ${orgSurrogate}`
    return rows[0] ?? null
  }

  // -------------------------------------------------------------------------
  // Write only
  // -------------------------------------------------------------------------

  it('the application role cannot read the stream, and gets an error rather than nothing', async () => {
    await record({
      name: 'environment.created',
      occurredAt: new Date('2026-05-01T00:00:00Z'),
      orgId: org.orgId,
      payload: { runtime_class: 'docker', declared_lifetime: true },
    })

    // Proves the row is there before proving it is unreachable. Without this a
    // query that returns nothing because nothing was written looks exactly like
    // a permission working.
    assert.ok((await streamFor(surrogate)).length > 0, 'the fixture wrote nothing to read')

    await assert.rejects(
      h.pool.withoutTenant((db) => db.execute(sql`SELECT count(*) FROM analytics_events`)),
      (err: unknown) => {
        // 42501 is insufficient_privilege. Asserting on the SQLSTATE rather
        // than on a message, because the outer message would match a typo in
        // this test's own SQL just as happily.
        const code = codeOf(err)
        assert.equal(code, '42501', `expected insufficient_privilege, got ${code}`)
        return true
      },
    )
  })

  it('the surrogate is not the organization id, and does not contain it', async () => {
    assert.notEqual(surrogate, org.orgId)
    assert.equal(surrogate.length, 32)
    assert.ok(!surrogate.includes(org.orgId.slice(0, 8)))

    // And a different key gives a different answer, which is what makes the
    // hash a one-way door rather than an encoding anybody can reverse.
    const other = createAnalytics({
      secret: Buffer.alloc(32, 7),
      clock: h.clock,
      counters: { events: { inc() {} }, rejections: { inc() {} } },
    })
    assert.notEqual(other.surrogate(org.orgId), surrogate)
  })

  it('domain separates, so one value used as both hashes to two surrogates', async () => {
    // The same string, once as an organization and once as a session. Without a
    // domain prefix in the hash these would be the same 32 characters, and a
    // browser tab would then be indistinguishable from an organization in every
    // count the rollup makes.
    const value = randomUUID()
    const orgHash = h.analytics.surrogate(value)!
    await record({
      name: 'site.page_viewed',
      eventId: randomUUID(),
      occurredAt: new Date('2026-08-01T00:00:00Z'),
      session: value,
      payload: { route: 'home', source: 'direct', entry: true },
    })
    const rows = await h.admin<{ session_surrogate: string }[]>`
      SELECT session_surrogate FROM analytics_events
      WHERE occurred_at = '2026-08-01T00:00:00Z'::timestamptz AND name = 'site.page_viewed'`
    assert.ok(rows.length > 0, 'the fixture recorded nothing, so this proves nothing')
    assert.notEqual(rows[0]!.session_surrogate, orgHash)
    assert.notEqual(rows[0]!.session_surrogate, value)
  })

  it('has exactly one unique constraint, which is what makes a bare ON CONFLICT correct', async () => {
    // The recorder cannot name a conflict target, because naming one needs
    // SELECT on the arbiter columns and this role deliberately has none. Bare
    // DO NOTHING therefore covers every unique constraint on the table, so a
    // second one added later would start silently dropping rows. This is the
    // gate on that, asserted as a property rather than as a list.
    const rows = await h.admin<{ conname: string }[]>`
      SELECT conname FROM pg_constraint
      WHERE conrelid = 'analytics_events'::regclass AND contype IN ('p', 'u')`
    assert.equal(
      rows.length,
      1,
      `analytics_events now has ${rows.length} unique constraints: ` +
        `${rows.map((r) => r.conname).join(', ')}. The recorder's bare ON CONFLICT DO NOTHING ` +
        `would silently swallow a conflict on the new one.`,
    )
  })

  // -------------------------------------------------------------------------
  // The closed schema, end to end rather than in the validator alone
  // -------------------------------------------------------------------------

  it('refuses an unknown event name and writes nothing', async () => {
    const before = (await streamFor(surrogate)).length
    const outcome = await record({
      name: 'environment.exploded' as never,
      occurredAt: new Date(),
      orgId: org.orgId,
    })
    assert.equal(outcome.status, 'rejected')
    assert.equal(outcome.status === 'rejected' && outcome.problem.reason, 'unknown_event')
    assert.equal((await streamFor(surrogate)).length, before)
  })

  it('refuses an event that should carry an organization and does not', async () => {
    const outcome = await record({
      name: 'environment.created',
      occurredAt: new Date(),
      payload: { runtime_class: 'docker' },
    })
    assert.equal(outcome.status === 'rejected' && outcome.problem.reason, 'organization_required')
  })

  it('refuses an anonymous event that arrives carrying an organization', async () => {
    const outcome = await record({
      name: 'site.page_viewed',
      occurredAt: new Date(),
      orgId: org.orgId,
      session: 'a-session-identifier',
      payload: { route: 'home', source: 'direct', entry: true },
    })
    assert.equal(outcome.status === 'rejected' && outcome.problem.reason, 'organization_forbidden')
  })

  // -------------------------------------------------------------------------
  // The ordering table. One test per cell.
  // -------------------------------------------------------------------------

  describe('arrival order', () => {
    /** A fresh surrogate per case, so one case cannot pass because of another. */
    function freshOrg(): { orgId: string; surrogate: string } {
      const orgId = randomUUID()
      return { orgId, surrogate: h.analytics.surrogate(orgId)! }
    }

    it('an event about an organization that does not exist yet is recorded, not lost', async () => {
      // The whole point of the surrogate: there is no foreign key, so the
      // analytics store has no opinion about whether the organizations table
      // has caught up. An event that arrived before its organization row is a
      // real case on the sign-in path and it must not be dropped.
      const { orgId, surrogate: s } = freshOrg()
      const exists = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM organizations WHERE id = ${orgId}`
      assert.equal(Number(exists[0]!.n), 0, 'the fixture accidentally created the organization')

      const outcome = await record({
        name: 'identity.organization_created',
        occurredAt: new Date('2026-05-02T00:00:00Z'),
        orgId,
        payload: {},
      })
      assert.equal(outcome.status, 'recorded')
      const facts = await factsFor(s)
      assert.ok(facts !== null, 'no facts row was written for an organization that does not exist')
      assert.equal(day(facts.first_seen_on), '2026-05-02')
    })

    it('the same event delivered twice is recorded once and counts once', async () => {
      const { orgId, surrogate: s } = freshOrg()
      const eventId = randomUUID()
      const at = new Date('2026-05-03T10:00:00Z')
      const event = {
        name: 'environment.created' as const,
        eventId,
        occurredAt: at,
        orgId,
        payload: { runtime_class: 'docker', declared_lifetime: true },
      }

      assert.equal((await record(event)).status, 'recorded')
      assert.equal((await record(event)).status, 'duplicate')

      assert.equal((await streamFor(s)).length, 1)
      // And the counter did not move on the duplicate, which is the half that
      // is easy to get wrong: an environments_created that grows every time an
      // engine retries a batch is the number a bill gets checked against.
      assert.equal(Number((await factsFor(s))!.environments_created), 1)
    })

    it('a retry that restamps the time inserts a second row, so producers must not', async () => {
      // Not a bug being tolerated: a demonstration of the trap, so that the
      // rule in AnalyticsEvent.occurredAt has a test behind it rather than a
      // comment. The table is partitioned on occurred_at, so the unique key
      // carries it and a retry with a new timestamp does not collide.
      const { orgId, surrogate: s } = freshOrg()
      const eventId = randomUUID()
      const base = {
        name: 'environment.created' as const,
        eventId,
        orgId,
        payload: { runtime_class: 'docker' },
      }
      await record({ ...base, occurredAt: new Date('2026-05-04T10:00:00Z') })
      await record({ ...base, occurredAt: new Date('2026-05-04T10:00:01Z') })
      assert.equal(
        (await streamFor(s)).length,
        2,
        'a restamped retry collided, which would mean the partition key changed',
      )
    })

    it('out of order occurred times move a milestone earlier and never later', async () => {
      const { orgId, surrogate: s } = freshOrg()
      const later = {
        name: 'validation.run_finished' as const,
        orgId,
        payload: { kind: 'test', verdict: 'pass' },
      }
      // The later day arrives first, which is the normal case for a backlog.
      await record({ ...later, eventId: randomUUID(), occurredAt: new Date('2026-05-20T00:00:00Z') })
      assert.equal(day((await factsFor(s))!.first_proven_run_on), '2026-05-20')

      // Then the earlier one. It must move the milestone back.
      await record({ ...later, eventId: randomUUID(), occurredAt: new Date('2026-05-11T00:00:00Z') })
      assert.equal(day((await factsFor(s))!.first_proven_run_on), '2026-05-11')

      // And a third, later again, must not move it forward.
      await record({ ...later, eventId: randomUUID(), occurredAt: new Date('2026-05-25T00:00:00Z') })
      assert.equal(day((await factsFor(s))!.first_proven_run_on), '2026-05-11')
      assert.equal(day((await factsFor(s))!.last_active_on), '2026-05-25')
    })

    it('the two orderings of the same two events produce identical facts', async () => {
      // The property the milestone redesign exists for, stated as a comparison
      // rather than as an assertion about one order.
      const forwards = freshOrg()
      const backwards = freshOrg()
      const early = { occurredAt: new Date('2026-06-01T00:00:00Z'), payload: { kind: 'test', verdict: 'pass' } }
      const late = { occurredAt: new Date('2026-06-09T00:00:00Z'), payload: { kind: 'agent', verdict: 'fail' } }

      for (const e of [early, late]) {
        await record({ name: 'validation.run_finished', eventId: randomUUID(), orgId: forwards.orgId, ...e })
      }
      for (const e of [late, early]) {
        await record({ name: 'validation.run_finished', eventId: randomUUID(), orgId: backwards.orgId, ...e })
      }

      const a = await factsFor(forwards.surrogate)
      const b = await factsFor(backwards.surrogate)
      assert.deepEqual(
        [day(a!.first_seen_on), day(a!.last_active_on), day(a!.first_proven_run_on), Number(a!.runs_finished)],
        [day(b!.first_seen_on), day(b!.last_active_on), day(b!.first_proven_run_on), Number(b!.runs_finished)],
      )
    })

    it('an unverified verdict finishes a run and does not activate the organization', async () => {
      // The green-over-nothing this product exists to refuse, in its analytics
      // form: counting `unverified` as activation would report every customer
      // as activated on the day they installed.
      const { orgId, surrogate: s } = freshOrg()
      await record({
        name: 'validation.run_finished',
        eventId: randomUUID(),
        occurredAt: new Date('2026-06-15T00:00:00Z'),
        orgId,
        payload: { kind: 'test', verdict: 'unverified' },
      })
      const facts = (await factsFor(s))!
      assert.equal(Number(facts.runs_finished), 1)
      assert.equal(facts.first_proven_run_on, null)

      await record({
        name: 'validation.run_finished',
        eventId: randomUUID(),
        occurredAt: new Date('2026-06-16T00:00:00Z'),
        orgId,
        payload: { kind: 'test', verdict: 'blocked' },
      })
      assert.equal((await factsFor(s))!.first_proven_run_on, null, 'blocked activated an organization')
    })

    it('one malformed event never discards the good ones around it', async () => {
      const { orgId, surrogate: s } = freshOrg()
      const outcome = await h.pool.withoutTenant((db) =>
        h.analytics.recordAll(db, [
          {
            name: 'environment.created',
            eventId: randomUUID(),
            occurredAt: new Date('2026-06-20T00:00:00Z'),
            orgId,
            payload: { runtime_class: 'docker' },
          },
          {
            name: 'environment.created',
            eventId: randomUUID(),
            occurredAt: new Date('2026-06-20T00:00:00Z'),
            orgId,
            payload: { runtime_class: 'a-runtime-nobody-declared' },
          },
          {
            name: 'environment.created',
            eventId: randomUUID(),
            occurredAt: new Date('2026-06-20T00:00:00Z'),
            orgId,
            payload: { runtime_class: 'kubernetes' },
          },
        ]),
      )
      assert.equal(outcome.recorded, 2)
      assert.equal(outcome.rejected, 1)
      assert.equal((await streamFor(s)).length, 2)
      assert.equal(outcome.outcomes[1]!.status, 'rejected')
    })
  })

  // -------------------------------------------------------------------------
  // The rollup
  // -------------------------------------------------------------------------

  describe('the rollup', () => {
    it('recomputes a day to the same numbers however many times it runs', async () => {
      const orgId = randomUUID()
      const at = new Date('2026-07-04T09:00:00Z')
      for (const verdict of ['pass', 'pass', 'fail']) {
        await record({
          name: 'validation.run_finished',
          eventId: randomUUID(),
          occurredAt: at,
          orgId,
          payload: { kind: 'test', verdict },
        })
      }

      const clock = new FakeClock('2026-07-04T23:00:00Z')
      const first = await rollUp(h.admin, clock, { lookbackDays: 2 })
      const after = await readDaily('2026-07-04', 'validation.run_finished')
      const second = await rollUp(h.admin, clock, { lookbackDays: 2 })
      const again = await readDaily('2026-07-04', 'validation.run_finished')

      assert.deepEqual(again, after, 'a second run of the rollup changed the numbers')
      assert.deepEqual(first.days, second.days)
      const pass = after.find((r) => r.dim_b === 'pass')
      assert.equal(Number(pass?.events), 2)
      assert.equal(Number(pass?.organizations), 1)
    })

    it('removes a row for a combination that no longer has events, rather than leaving it', async () => {
      // The failure an upsert would have. A stale row sits on a chart forever
      // with a count nobody can trace back to an event.
      const at = new Date('2026-07-11T09:00:00Z')
      const orgId = randomUUID()
      await record({
        name: 'adoption.feature_used',
        eventId: randomUUID(),
        occurredAt: at,
        orgId,
        payload: { feature: 'audit_exported' },
      })
      const clock = new FakeClock('2026-07-11T23:00:00Z')
      await rollUp(h.admin, clock, { lookbackDays: 1 })
      assert.equal((await readDaily('2026-07-11', 'adoption.feature_used')).length, 1)

      await h.admin`DELETE FROM analytics_events WHERE occurred_at = ${at.toISOString()}::timestamptz`
      await rollUp(h.admin, clock, { lookbackDays: 1 })
      // Length rather than deepEqual against a literal: postgres.js returns a
      // Result, which is an Array subclass, and deepStrictEqual distinguishes
      // the two even when the contents match.
      assert.equal((await readDaily('2026-07-11', 'adoption.feature_used')).length, 0)
    })

    it('records when it last ran, so a dashboard can tell empty from never', async () => {
      const clock = new FakeClock('2026-07-12T06:00:00Z')
      await rollUp(h.admin, clock, { lookbackDays: 1 })
      const state = await h.pool.withoutTenant((db) => freshness(db))
      assert.ok(state.lastRunAt !== null)
      assert.equal(state.settledAfter, '2026-07-12')
    })

    it('deletes raw events past the retention while the aggregates stay', async () => {
      const orgId = randomUUID()
      const old = new Date('2026-04-01T00:00:00Z')
      await record({
        name: 'environment.created',
        eventId: randomUUID(),
        occurredAt: old,
        orgId,
        payload: { runtime_class: 'local' },
      })
      const clock = new FakeClock('2026-04-02T00:00:00Z')
      await rollUp(h.admin, clock, { lookbackDays: 2 })
      const rolled = await readDaily('2026-04-01', 'environment.created')
      assert.ok(rolled.length > 0, 'the fixture rolled up nothing, so the retention proves nothing')

      // A year later, with a thirty day window.
      const later = new FakeClock('2027-04-02T00:00:00Z')
      const result = await rollUp(h.admin, later, { lookbackDays: 1, retentionDays: 30 })
      assert.ok(result.pruned > 0, 'retention deleted nothing')
      const left = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM analytics_events WHERE occurred_at = ${old.toISOString()}::timestamptz`
      assert.equal(Number(left[0]!.n), 0)
      // The aggregate for that day is still there, which is the shape a
      // retention policy should have: the rows that carry a surrogate go, and
      // the counts computed from them do not.
      assert.deepEqual(await readDaily('2026-04-01', 'environment.created'), rolled)
    })
  })

  // -------------------------------------------------------------------------
  // What the dashboard reads
  // -------------------------------------------------------------------------

  describe('the read side', () => {
    it('returns every day in a window, including the ones with nothing in them', async () => {
      const points = await h.pool.withoutTenant((db) =>
        series(db, 'site.page_viewed', 7, new Date('2026-07-20T00:00:00Z')),
      )
      assert.equal(points.length, 7)
      assert.equal(points[0]!.day, '2026-07-14')
      assert.equal(points[6]!.day, '2026-07-20')
    })

    it('reports an event nothing has ever emitted as zero rather than as a blank', async () => {
      const rows = await h.pool.withoutTenant((db) => catalogStatus(db))
      assert.equal(rows.length, EVENT_NAMES.length)
      for (const row of rows) {
        assert.ok(typeof row.everRecorded === 'number')
        assert.ok(row.producer.length > 0)
      }
    })

    it('builds a funnel that cannot have a step wider than the one before it', async () => {
      const steps = await h.pool.withoutTenant((db) =>
        organizationFunnel(db, 3650, new Date('2027-01-01T00:00:00Z')),
      )
      for (let i = 1; i < steps.length; i += 1) {
        assert.ok(
          steps[i]!.organizations <= steps[i - 1]!.organizations,
          `${steps[i]!.step} is wider than ${steps[i - 1]!.step}, which a funnel cannot be`,
        )
      }
    })

    it('counts a dormant organization as the difference rather than as a separate query', async () => {
      const r = await h.pool.withoutTenant((db) => retention(db, new Date('2027-01-01T00:00:00Z')))
      assert.equal(r.dormant, r.total - r.activeLast28)
      assert.ok(r.activeLast7 <= r.activeLast28)
    })
  })

  async function readDaily(day: string, name: string) {
    return h.admin<{ dim_a: string; dim_b: string; events: string; organizations: string; sessions: string }[]>`
      SELECT dim_a, dim_b, events::text, organizations::text, sessions::text
      FROM analytics_daily WHERE day = ${day}::date AND name = ${name}
      ORDER BY dim_a, dim_b`
  }
})

/** The Postgres error underneath whatever the query builder wrapped it in. */
function codeOf(err: unknown): string | null {
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; cause?: unknown }
    if (typeof e.code === 'string' && /^[0-9A-Z]{5}$/.test(e.code)) return e.code
    cur = e.cause
  }
  return null
}

function day(v: unknown): string | null {
  if (v === null || v === undefined) return null
  return v instanceof Date ? v.toISOString().slice(0, 10) : String(v).slice(0, 10)
}
