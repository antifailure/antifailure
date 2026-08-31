// The environments projection, against the orderings the network produces.
//
// This suite exists because the projection was an UPDATE that matched nothing
// for the whole life of the product, and the reason nobody noticed is written
// into the other suite: every test of ingestion inserted the environments row
// by hand in the harness first. A test that seeds the row it is testing the
// creation of cannot fail when the creation is missing.
//
// So nothing here seeds an environment. Every row asserted on has to have been
// brought into existence by an event arriving at /v1/events, which is the only
// path a real engine has.
//
// One test per arrival ordering, named for the ordering. The set is the one
// the rule asks for: A then B, B then A, both in one batch, A twice, A with no
// B, and B with no A, plus the two entry points that reach this state without
// an engine at all.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import {
  available, startApi, seedOrg, dropOrg, signInAs, callProcedure,
  type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

/** One hour, which is the lifetime every environment in this suite declares
 *  unless it is the one testing what happens when none is declared. */
const TTL_SECONDS = 3600

describe('the environments projection', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'projection')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_test')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function send(
    events: unknown[],
    bearer = token,
  ): Promise<{ status: number; body: any }> {
    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events }),
    })
    return { status: res.status, body: await res.json() }
  }

  /** An event as the engine now sends it: the identity travels on every
   *  lifecycle event, not only on the first. */
  function event(
    type: string,
    envId: string,
    sequence: number,
    over: Record<string, unknown> = {},
  ): Record<string, unknown> {
    const { payload, ...rest } = over
    return {
      id: randomUUID(),
      type,
      envId,
      sequence,
      occurredAt: h.clock.now().toISOString(),
      payload: {
        repository: org.repository,
        branch: 'feature/a-branch',
        ttl_seconds: TTL_SECONDS,
        ...(payload as Record<string, unknown> | undefined),
      },
      ...rest,
    }
  }

  interface Row {
    id: string
    env_id: string
    branch: string
    pull_request: number | null
    state: string
    preview_url: string | null
    runtime: string | null
    last_sequence: string
    created_at: Date
    expires_at: Date | null
    torn_down_at: Date | null
    repository: string
  }

  async function rows(envId: string): Promise<Row[]> {
    return h.admin<Row[]>`
      SELECT e.id, e.env_id, e.branch, e.pull_request, e.state::text AS state, e.preview_url,
             e.runtime, e.last_sequence, e.created_at, e.expires_at, e.torn_down_at,
             r.full_name AS repository
      FROM environments e JOIN repositories r ON r.id = e.repository_id
      WHERE e.org_id = ${org.orgId} AND e.env_id = ${envId}`
  }

  async function one(envId: string): Promise<Row> {
    const found = await rows(envId)
    assert.equal(found.length, 1, `expected exactly one row for ${envId}, found ${found.length}`)
    return found[0]!
  }

  function envId(label: string): string {
    return `env-${label}-${randomUUID().slice(0, 8)}`
  }

  // -------------------------------------------------------------------------
  // The orderings
  // -------------------------------------------------------------------------

  it('ordering: creating then ready then torn down, the one everybody tests', async () => {
    const id = envId('happy')
    await send([event('environment.creating', id, 1)])

    const created = await one(id)
    assert.equal(created.state, 'creating', 'the first event has to create the row')
    assert.equal(created.repository, org.repository)
    assert.equal(created.branch, 'feature/a-branch')

    await send([event('environment.ready', id, 2, {
      payload: { preview_url: 'https://a.example.test', runtime: 'local' },
    })])
    const tornDownAt = new Date(h.clock.now().getTime() - 90 * 60 * 1000)
    await send([event('environment.torn_down', id, 3, {
      occurredAt: tornDownAt.toISOString(),
    })])

    const finished = await one(id)
    assert.equal(finished.state, 'torn_down')
    assert.equal(finished.preview_url, 'https://a.example.test')
    assert.equal(finished.runtime, 'local')
    assert.equal(Number(finished.last_sequence), 3)
    assert.notEqual(finished.torn_down_at, null, 'a torn down environment needs the timestamp the bill is computed from')
    // The engine's timestamp, not this service's. Both ends of the interval a
    // bill is computed from come from the same clock, so an event that spent a
    // day in a spool file does not add a day to the bill.
    assert.equal(finished.torn_down_at!.toISOString(), tornDownAt.toISOString())
    assert.equal(finished.id, created.id, 'the later events updated the row rather than making another')
  })

  it('ordering: ready with no creating at all, because the spool dropped the oldest event', async () => {
    // The engine's sink drops the oldest events when a failed batch overflows,
    // and the oldest event of a run is creating. A projection that waits for
    // it loses the whole environment in exactly this case.
    const id = envId('no-creating')
    const res = await send([event('environment.ready', id, 7, {
      payload: { preview_url: 'https://b.example.test' },
    })])
    assert.equal(res.body.unprojected, 0, 'this event carries everything the row needs')

    const row = await one(id)
    assert.equal(row.state, 'running')
    assert.equal(row.branch, 'feature/a-branch')
    assert.equal(Number(row.last_sequence), 7)
  })

  it('ordering: ready then creating, so the row is created by the later event and not dragged back', async () => {
    const id = envId('reversed')
    const readyAt = h.clock.now()
    const createdAt = new Date(readyAt.getTime() - 5 * 60 * 1000)

    await send([event('environment.ready', id, 2, { occurredAt: readyAt.toISOString() })])
    await send([event('environment.creating', id, 1, { occurredAt: createdAt.toISOString() })])

    const row = await one(id)
    assert.equal(row.state, 'running', 'a late creating event must not put the environment back into creating')
    assert.equal(Number(row.last_sequence), 2)
    // created_at is the earliest occurred_at seen, whichever event carried it,
    // because the bill is the time the environment was actually held and the
    // ready event is minutes after the environment started costing money.
    assert.equal(
      row.created_at.toISOString(), createdAt.toISOString(),
      'the late creating event should have corrected created_at backwards',
    )
    assert.equal(
      row.expires_at!.toISOString(),
      new Date(createdAt.getTime() + TTL_SECONDS * 1000).toISOString(),
      'the expiry follows created_at, so correcting one corrects the other',
    )
  })

  it('ordering: the same creating event delivered twice makes one row, not two', async () => {
    const id = envId('retry')
    const twice = event('environment.creating', id, 1)

    const first = await send([twice])
    assert.equal(first.body.accepted, 1)
    const second = await send([twice])
    assert.equal(second.body.duplicates, 1, 'the second copy is a duplicate by idempotency key')

    // And again with a different id, which is the case the unique constraint on
    // the events table cannot catch: a sender that regenerates its identifier.
    await send([event('environment.creating', id, 1)])

    const found = await rows(id)
    assert.equal(found.length, 1, 'a repeated creating event must upsert, not insert')
  })

  it('ordering: creating and ready in one batch, delivered in reverse inside the batch', async () => {
    const id = envId('one-batch')
    const res = await send([
      event('environment.ready', id, 2, { payload: { runtime: 'kubernetes' } }),
      event('environment.creating', id, 1),
    ])
    assert.equal(res.body.accepted, 2)

    const row = await one(id)
    assert.equal(row.state, 'running')
    assert.equal(row.runtime, 'kubernetes')
  })

  it('ordering: torn down with nothing before it, because the run was torn down before the row existed', async () => {
    const id = envId('torn-down-first')
    await send([event('environment.torn_down', id, 4)])

    const row = await one(id)
    assert.equal(row.state, 'torn_down')
    assert.notEqual(row.torn_down_at, null)
    // The whole point of recording it rather than ignoring it: an environment
    // that was held and then torn down is on the bill, and an environment the
    // control plane never recorded is not.
    assert.notEqual(row.created_at, null)
  })

  it('ordering: a teardown that spent a day in the spool is not a day on the bill', async () => {
    // The engine buffers to disk when the control plane is unreachable and
    // sends on the next command, which can be the next day. The interval a
    // bill is computed from is created_at to torn_down_at, so taking the
    // teardown time from this service's clock would charge the customer for
    // the outage.
    const id = envId('late-teardown')
    const created = new Date(h.clock.now().getTime() - 26 * 60 * 60 * 1000)
    const removed = new Date(created.getTime() + 60 * 60 * 1000)

    await send([event('environment.creating', id, 1, { occurredAt: created.toISOString() })])
    await send([event('environment.torn_down', id, 2, { occurredAt: removed.toISOString() })])

    const row = await one(id)
    const held = (row.torn_down_at!.getTime() - row.created_at.getTime()) / 3_600_000
    assert.equal(held, 1, `the environment was held for an hour and the row says ${held}`)
  })

  it('ordering: a teardown claiming to predate the creation cannot make a negative duration', async () => {
    // occurred_at comes from a sender this service does not trust, and a
    // negative interval poisons every sum computed from these two columns.
    const id = envId('impossible-teardown')
    const created = h.clock.now()

    await send([event('environment.creating', id, 1, { occurredAt: created.toISOString() })])
    await send([event('environment.torn_down', id, 2, {
      occurredAt: new Date(created.getTime() - 6 * 60 * 60 * 1000).toISOString(),
    })])

    const row = await one(id)
    assert.ok(
      row.torn_down_at!.getTime() >= row.created_at.getTime(),
      'an environment was torn down before it was created',
    )
  })

  it('ordering: failed with nothing before it, because the run died before it reported anything else', async () => {
    const id = envId('failed-first')
    await send([event('environment.failed', id, 1, { payload: { code: 'AF-DB-001' } })])

    const row = await one(id)
    assert.equal(row.state, 'failed')
  })

  it('ordering: every permutation of three events converges on the same row', async () => {
    const orderings = [[1, 2, 3], [3, 2, 1], [2, 1, 3], [2, 3, 1], [3, 1, 2], [1, 3, 2]]
    const types = ['environment.creating', 'environment.ready', 'environment.torn_down']
    const base = h.clock.now().getTime()

    for (const [index, ordering] of orderings.entries()) {
      const id = envId(`permutation-${index}`)
      for (const seq of ordering) {
        await send([event(types[seq - 1]!, id, seq, {
          // Each event keeps its own occurred_at whatever order it arrives in,
          // so the convergence being asserted is a real one and not an
          // artefact of every event carrying the same timestamp.
          occurredAt: new Date(base + seq * 60 * 1000).toISOString(),
        })])
      }

      const row = await one(id)
      assert.equal(row.state, 'torn_down', `arriving as ${ordering.join(',')} left it ${row.state}`)
      assert.equal(Number(row.last_sequence), 3)
      assert.equal(
        row.created_at.toISOString(), new Date(base + 60 * 1000).toISOString(),
        `arriving as ${ordering.join(',')} put created_at somewhere else`,
      )
      assert.equal(
        row.expires_at!.toISOString(),
        new Date(base + 60 * 1000 + TTL_SECONDS * 1000).toISOString(),
        `arriving as ${ordering.join(',')} computed a different expiry`,
      )
    }
  })

  it('ordering: two environments in one batch each get their own row', async () => {
    const first = envId('pair-a')
    const second = envId('pair-b')
    await send([
      event('environment.creating', first, 1),
      event('environment.creating', second, 1),
      event('environment.ready', first, 2),
    ])

    assert.equal((await one(first)).state, 'running')
    assert.equal((await one(second)).state, 'creating')
  })

  // -------------------------------------------------------------------------
  // What the row is worth once it exists
  // -------------------------------------------------------------------------

  it('reports a real expiry for an environment that declared a lifetime, and null for one that did not', async () => {
    const declared = envId('with-ttl')
    const at = h.clock.now()
    await send([event('environment.creating', declared, 1, { occurredAt: at.toISOString() })])

    const silent = envId('no-ttl')
    await send([event('environment.creating', silent, 1, { payload: { ttl_seconds: undefined } })])

    const withTtl = await one(declared)
    assert.equal(
      withTtl.expires_at!.toISOString(),
      new Date(at.getTime() + TTL_SECONDS * 1000).toISOString(),
    )
    assert.equal(
      (await one(silent)).expires_at, null,
      'null must mean nobody declared a lifetime, not that nobody wrote the column',
    )

    // And through the API the console actually calls, because a column that is
    // right in the database and not returned is the same empty field.
    const viewer = await signInAs(h, org, 'admin')
    const got = await callProcedure(h, viewer, 'environments.get', 'query', { envId: declared })
    assert.equal(got.status, 200)
    const body = got.body as { result: { data: { expires_at: string; repository: string } } }
    assert.equal(
      new Date(body.result.data.expires_at).toISOString(),
      withTtl.expires_at!.toISOString(),
    )
    assert.equal(body.result.data.repository, org.repository)
  })

  it('records the pull request number, which the console has always had a column for and never a value in', async () => {
    const id = envId('pull-request')
    await send([event('environment.creating', id, 1, { payload: { pull_request: 42 } })])
    assert.equal((await one(id)).pull_request, 42)
  })

  it('creates the repository row when the engine reports one the GitHub App has never mentioned', async () => {
    const id = envId('unknown-repo')
    const unknown = `${org.slug}/a-repo-nobody-connected`
    await send([event('environment.creating', id, 1, { payload: { repository: unknown } })])

    const row = await one(id)
    assert.equal(row.repository, unknown)

    const repos = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM repositories
      WHERE org_id = ${org.orgId} AND full_name = ${unknown}`
    assert.equal(Number(repos[0]!.n), 1, 'reporting it twice must not create two repositories')

    await send([event('environment.ready', id, 2, { payload: { repository: unknown } })])
    const again = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM repositories
      WHERE org_id = ${org.orgId} AND full_name = ${unknown}`
    assert.equal(Number(again[0]!.n), 1)
  })

  // -------------------------------------------------------------------------
  // What happens when the sender cannot say who it is
  // -------------------------------------------------------------------------

  it('says out loud that it could not project an event from an engine too old to name its repository', async () => {
    const id = envId('no-identity')
    const res = await send([{
      id: randomUUID(),
      type: 'environment.ready',
      envId: id,
      sequence: 1,
      occurredAt: h.clock.now().toISOString(),
      payload: { preview_url: 'https://c.example.test' },
    }])

    assert.equal(res.body.accepted, 1, 'the event is still stored; it is not lost')
    assert.equal(res.body.unprojected, 1, 'and the response counts it separately from a rejection')
    assert.equal(res.body.rejected, 0, 'the ingestion loss objective is not affected by this')
    assert.match(
      res.body.outcomes[0].note, /repository/,
      'the note has to say what is missing, or nobody can act on it',
    )
    assert.equal((await rows(id)).length, 0, 'and no row is invented from a name nobody gave')
  })

  it('lets an engine too old to name its repository still advance a row a newer one created', async () => {
    const id = envId('mixed-versions')
    await send([event('environment.creating', id, 1)])

    const res = await send([{
      id: randomUUID(),
      type: 'environment.ready',
      envId: id,
      sequence: 2,
      occurredAt: h.clock.now().toISOString(),
      payload: {},
    }])
    assert.equal(res.body.unprojected, 0, 'the row exists, so there is nothing to report')
    assert.equal((await one(id)).state, 'running')
  })

  it('does not let one organization report an environment into another one', async () => {
    const other = await seedOrg(h.admin, 'projection-other')
    try {
      const id = envId('cross-tenant')
      // The repository name belongs to the other organization. The event is
      // authenticated as this one, so the row it creates has to be this
      // organization's own repository of that name, never the other's.
      await send([event('environment.creating', id, 1, {
        payload: { repository: other.repository },
      })])

      const mine = await h.admin<{ org_id: string }[]>`
        SELECT r.org_id FROM environments e JOIN repositories r ON r.id = e.repository_id
        WHERE e.env_id = ${id}`
      assert.equal(mine.length, 1)
      assert.equal(mine[0]!.org_id, org.orgId, 'an environment was attached to another tenant’s repository')

      const theirs = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM environments WHERE org_id = ${other.orgId} AND env_id = ${id}`
      assert.equal(Number(theirs[0]!.n), 0)
    } finally {
      await dropOrg(h.admin, other.orgId)
    }
  })
})
