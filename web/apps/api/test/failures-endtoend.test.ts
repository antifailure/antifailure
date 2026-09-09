// The failure store is reachable from a real failure, on both arms.
//
// WHY THIS FILE EXISTS SEPARATELY FROM failures.test.ts. That file proves the
// store behaves. This one proves it is WIRED: that a request which genuinely
// fails reaches `record`, through the handler that ships, with the fields the
// handler chose rather than the fields a test passed in by hand. A store with a
// correct upsert and no caller is a dead shippable gap that looks exactly like
// a working feature, and this repository has shipped that shape before.
//
// So the failures here are real ones, produced the way the two existing suites
// produce them: a CHECK constraint that cannot pass, and a table renamed out
// from under a route. Nothing throws a fixture.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import {
  dropOrg,
  seedOrg,
  signInAs,
  startApi,
  available,
  type ApiHarness,
  type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('a real failure reaches the grouped store', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'failwire')
    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_test')`
  })

  after(async () => {
    if (org) await dropOrg(h.admin, org.orgId)
    if (h) await h.close()
  })

  async function rows() {
    return h.admin`
      SELECT source, route, method, kind, provider_code, occurrences, last_request_id
      FROM control_plane_failures ORDER BY route`
  }

  it('through the HTTP handler, with the declared route and the driver code', async () => {
    await h.admin`DELETE FROM control_plane_failures`
    await h.admin`ALTER TABLE events ADD CONSTRAINT wire_forced_failure CHECK (false) NOT VALID`
    let requestId: string | null
    try {
      const response = await h.fetch('/v1/events', {
        method: 'POST',
        headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
        body: JSON.stringify({
          events: [
            {
              id: randomUUID(),
              type: 'environment.ready',
              envId: org.envId,
              sequence: 1,
              // The harness clock, not the wall clock. Ingestion refuses an
              // event whose stamp is too far from now, and a refusal is a 207
              // with a per-event reason rather than the escaped failure this
              // test is aimed at.
              occurredAt: h.clock.now().toISOString(),
              payload: {},
            },
          ],
        }),
      })
      assert.equal(response.status, 500, 'the request under test did not actually fail')
      requestId = response.headers.get('x-request-id')
    } finally {
      await h.admin`ALTER TABLE events DROP CONSTRAINT wire_forced_failure`
    }

    // Nothing is written until the flush, which is the whole design: the error
    // path never touches the network. Asserting the table is empty first is
    // what makes the assertion after the flush mean something.
    assert.equal((await rows()).length, 0, 'the error path wrote to the database inline')
    await h.failures.flush()

    const found = await rows()
    assert.equal(found.length, 1)
    const row = found[0]!
    assert.equal(row.source, 'http')
    // The DECLARED route key, not the path. A path here would mean an
    // unbounded label, which is the defect routeLabel exists to prevent and
    // which this table would turn into unbounded rows.
    assert.equal(row.route, 'POST /v1/events')
    assert.equal(row.method, 'POST')
    // 23514 is check_violation. The driver's own code is what separates a
    // constraint failure from a connection reset on the same route, and it is
    // the field a fingerprint most needs.
    assert.equal(row.provider_code, '23514')
    assert.equal(Number(row.occurrences), 1)
    assert.equal(row.last_request_id, requestId)

    // And the boundary. The statement that failed carried the event payload,
    // and none of it is here.
    const everything = JSON.stringify(found)
    assert.ok(!everything.includes('Failed query'), `the driver wrapper was stored: ${everything}`)
    assert.ok(!everything.includes('SELECT'), `a statement was stored: ${everything}`)
    assert.ok(!everything.includes('INSERT'), `a statement was stored: ${everything}`)
    assert.ok(!everything.includes(org.orgId), `an organization was stored: ${everything}`)
  })

  it('through the tRPC handler, grouped by the procedure and the cause', async () => {
    await h.admin`DELETE FROM control_plane_failures`
    const member = await signInAs(h, org, 'owner')
    await h.admin.unsafe('ALTER TABLE environments RENAME TO environments_moved')
    try {
      const response = await h.fetch(
        `/trpc/environments.list?input=${encodeURIComponent(JSON.stringify({ limit: 5 }))}`,
        { headers: { cookie: member.cookie } },
      )
      assert.equal(response.status, 500, 'the request under test did not actually fail')
    } finally {
      await h.admin.unsafe('ALTER TABLE environments_moved RENAME TO environments')
    }

    await h.failures.flush()
    const found = await rows()
    assert.equal(found.length, 1)
    const row = found[0]!
    assert.equal(row.source, 'trpc')
    // The procedure path, which the router bounds. Not "trpc", which every
    // tRPC failure on the installation would share.
    assert.equal(row.route, 'environments.list')
    assert.equal(row.method, 'query')
    // The CAUSE's class, not TRPCError. Every internal tRPC failure is a
    // TRPCError, so fingerprinting on it would put a missing table and a
    // connection reset in the same group.
    assert.notEqual(row.kind, 'TRPCError')
    assert.equal(row.provider_code, '42P01')
  })

  it('two occurrences of the same failure are one group with a count of two', async () => {
    // The property the whole table depends on. If this were two rows, the
    // store's size would be a function of how badly the day is going, which is
    // the disk exhaustion this design exists to avoid.
    await h.admin`DELETE FROM control_plane_failures`
    const member = await signInAs(h, org, 'owner')
    await h.admin.unsafe('ALTER TABLE environments RENAME TO environments_moved')
    try {
      for (let i = 0; i < 2; i++) {
        const response = await h.fetch(
          `/trpc/environments.list?input=${encodeURIComponent(JSON.stringify({ limit: 5 }))}`,
          { headers: { cookie: member.cookie } },
        )
        assert.equal(response.status, 500)
      }
    } finally {
      await h.admin.unsafe('ALTER TABLE environments_moved RENAME TO environments')
    }

    await h.failures.flush()
    const found = await rows()
    assert.equal(found.length, 1, 'a second occurrence created a second row')
    assert.equal(Number(found[0]!.occurrences), 2)
  })
})
