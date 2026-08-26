// Ingestion, against the orderings the network actually produces.
//
// The rule this suite is written to is that testing the states is not testing
// the system. Every property here is about an ordering or a repetition, because
// those are what a lossy, retrying, unsynchronised set of senders produces, and
// a suite that only sends one well-formed event in the right order proves
// nothing that matters.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import { MAX_BATCH } from '../src/ingest.ts'
import {
  available, startApi, seedOrg, dropOrg, type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('event ingestion', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let token: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'ingest')
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
  ): Promise<{ status: number; body: any; retryAfter: string | null }> {
    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body: JSON.stringify({ events }),
    })
    return {
      status: res.status,
      body: await res.json(),
      retryAfter: res.headers.get('retry-after'),
    }
  }

  function event(over: Record<string, unknown> = {}): Record<string, unknown> {
    return {
      id: randomUUID(),
      type: 'environment.ready',
      envId: org.envId,
      sequence: 1,
      occurredAt: h.clock.now().toISOString(),
      payload: {},
      ...over,
    }
  }

  // -------------------------------------------------------------------------

  it('refuses a token that does not exist, and one that was revoked, alike', async () => {
    const made_up = await send([event()], 'aft_not-a-real-token-at-all-here')
    assert.equal(made_up.status, 401)

    const revoked = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix, revoked_at)
      VALUES (${org.orgId}, 'old', ${createHash('sha256').update(revoked).digest()}, 'aft_old', now())`
    const dead = await send([event()], revoked)
    assert.equal(dead.status, 401)
    // Identical bodies. Telling them apart tells somebody probing tokens that
    // one of their guesses was once real.
    assert.deepEqual(dead.body, made_up.body)
  })

  it('accepts a batch and applies it to the environment', async () => {
    const res = await send([event({ sequence: 5, payload: { preview_url: 'http://preview.test' } })])
    assert.equal(res.status, 202)
    assert.equal(res.body.accepted, 1)

    const [row] = await h.admin<{ state: string; preview_url: string; last_sequence: string }[]>`
      SELECT state::text AS state, preview_url, last_sequence FROM environments
      WHERE org_id = ${org.orgId} AND env_id = ${org.envId}`
    assert.equal(row?.state, 'running')
    assert.equal(row?.preview_url, 'http://preview.test')
    assert.equal(Number(row?.last_sequence), 5)
  })

  it('drops a repeat of an event it already has', async () => {
    const e = event({ sequence: 6 })
    const first = await send([e])
    assert.equal(first.body.accepted, 1)

    // The same event again, which is what a retry after a lost response looks
    // like. The sender cannot know whether the first one landed.
    const second = await send([e])
    assert.equal(second.body.accepted, 0)
    assert.equal(second.body.duplicates, 1)
    assert.equal(second.status, 202, 'a duplicate is a success, not an error')

    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events
      WHERE org_id = ${org.orgId} AND idempotency_key = ${e.id as string}`
    assert.equal(Number(row!.n), 1)
  })

  it('collapses duplicates inside one batch the same way', async () => {
    const e = event({ sequence: 7 })
    const res = await send([e, e, e])
    assert.equal(res.body.accepted, 1)
    assert.equal(res.body.duplicates, 2)
  })

  it('does not move an environment backwards when an event arrives late', async () => {
    await send([event({ type: 'environment.ready', sequence: 100 })])
    const [ready] = await h.admin<{ state: string }[]>`
      SELECT state::text AS state FROM environments WHERE env_id = ${org.envId}`
    assert.equal(ready?.state, 'running')

    // The event that was sent first and arrived second. Applying it would show
    // the environment as still being created, and a status that goes backwards
    // is a status nobody believes again.
    const late = await send([event({ type: 'environment.creating', sequence: 99 })])
    assert.equal(late.body.accepted, 1, 'the late event should still be stored')

    const [after] = await h.admin<{ state: string; last_sequence: string }[]>`
      SELECT state::text AS state, last_sequence FROM environments WHERE env_id = ${org.envId}`
    assert.equal(after?.state, 'running', 'a late event dragged the environment backwards')
    assert.equal(Number(after?.last_sequence), 100)
  })

  it('applies events in sequence order however they arrive', async () => {
    // Every ordering of the same three events must land in the same state.
    const orderings = [
      [1, 2, 3],
      [3, 2, 1],
      [2, 1, 3],
      [2, 3, 1],
      [3, 1, 2],
      [1, 3, 2],
    ]
    const types = ['environment.creating', 'environment.ready', 'environment.sleeping']

    for (const [index, ordering] of orderings.entries()) {
      const envId = `${org.envId}-order-${index}`
      const [repo] = await h.admin<{ id: string }[]>`
        SELECT id FROM repositories WHERE org_id = ${org.orgId} LIMIT 1`
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${repo!.id}, ${envId}, 'main', 'queued')`

      for (const seq of ordering) {
        await send([event({ envId, sequence: seq, type: types[seq - 1] })])
      }

      const [row] = await h.admin<{ state: string; last_sequence: string }[]>`
        SELECT state::text AS state, last_sequence FROM environments WHERE env_id = ${envId}`
      assert.equal(
        row?.state,
        'sleeping',
        `arriving as ${ordering.join(',')} left the environment in ${row?.state}`,
      )
      assert.equal(Number(row?.last_sequence), 3)
    }
  })

  it('keeps the good events in a batch that also contains a bad one', async () => {
    const good = event({ sequence: 200 })
    const bad = { id: randomUUID(), type: 'environment.ready' } // no occurredAt
    const alsoGood = event({ sequence: 201 })

    const res = await send([good, bad, alsoGood])
    assert.equal(res.status, 207, 'a partly rejected batch should not look like a clean success')
    assert.equal(res.body.accepted, 2)
    assert.equal(res.body.rejected, 1)

    const rejected = res.body.outcomes.find((o: any) => o.status === 'rejected')
    assert.match(rejected.reason, /occurredAt/, 'the rejection should say what was wrong')
  })

  it('rejects a timestamp from a machine with a wrong clock', async () => {
    const future = new Date(h.clock.now().getTime() + 3 * 24 * 60 * 60 * 1000).toISOString()
    const res = await send([event({ occurredAt: future })])
    assert.equal(res.body.rejected, 1)
    assert.match(res.body.outcomes[0].reason, /clock/, 'the message should name the actual problem')
  })

  it('refuses a batch larger than the limit rather than truncating it', async () => {
    const many = Array.from({ length: MAX_BATCH + 1 }, () => event())
    const res = await send(many)
    assert.equal(res.status, 413)
    // Truncation would look like success and lose events silently.
    assert.match(res.body.error, new RegExp(String(MAX_BATCH)))

    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId}`
    const stored = Number(row!.n)
    const again = await send(many)
    assert.equal(again.status, 413)
    const [after] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM events WHERE org_id = ${org.orgId}`
    assert.equal(Number(after!.n), stored, 'a refused batch stored some events anyway')
  })

  it('answers a burst with 429 and a Retry-After that is not zero', async () => {
    // The limiter is per token. Filling the bucket takes a burst larger than
    // the default, so this suite uses its own server with a small one.
    const small = await startApi()
    const smallOrg = await seedOrg(small.admin, 'burst')
    const burstToken = `aft_${randomUUID().replace(/-/g, '')}`
    await small.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${smallOrg.orgId}, 'ci', ${createHash('sha256').update(burstToken).digest()}, 'aft_b')`

    try {
      let refused: { status: number; retryAfter: string | null; body: any } | null = null
      for (let i = 0; i < 3000 && !refused; i += 1) {
        const res = await small.fetch('/v1/events', {
          method: 'POST',
          headers: { authorization: `Bearer ${burstToken}`, 'content-type': 'application/json' },
          body: JSON.stringify({
            events: [
              {
                id: randomUUID(),
                type: 'environment.ready',
                envId: smallOrg.envId,
                sequence: i,
                occurredAt: small.clock.now().toISOString(),
              },
            ],
          }),
        })
        if (res.status === 429) {
          refused = { status: res.status, retryAfter: res.headers.get('retry-after'), body: await res.json() }
        }
      }

      assert.ok(refused, 'the limiter never refused anything')
      assert.ok(refused.retryAfter, 'a 429 with no Retry-After makes a sender retry immediately')
      assert.ok(
        Number(refused.retryAfter) >= 1,
        `Retry-After was ${refused.retryAfter}; zero is an invitation to retry at once`,
      )

      // And the sender that waits gets through, which is the other half of the
      // contract. A limit that never refills is an outage.
      small.clock.advance(Number(refused.retryAfter) * 1000 + 1000)
      const afterWaiting = await small.fetch('/v1/events', {
        method: 'POST',
        headers: { authorization: `Bearer ${burstToken}`, 'content-type': 'application/json' },
        body: JSON.stringify({
          events: [
            {
              id: randomUUID(),
              type: 'environment.ready',
              envId: smallOrg.envId,
              sequence: 9999,
              occurredAt: small.clock.now().toISOString(),
            },
          ],
        }),
      })
      assert.equal(afterWaiting.status, 202, 'waiting the stated time did not help')
    } finally {
      await dropOrg(small.admin, smallOrg.orgId)
      await small.close()
    }
  })

  it('stores an event type it does not understand rather than refusing it', async () => {
    // An older control plane must be able to ingest a newer engine's events.
    // Refusing an unknown type would mean an upgrade order that nobody can
    // guarantee across machines they do not control.
    const res = await send([event({ type: 'something.invented.later' })])
    assert.equal(res.body.accepted, 1)
  })

  it('one organization’s token cannot write into another organization', async () => {
    const other = await seedOrg(h.admin, 'neighbour')
    try {
      // The token names its own organization, so an event mentioning another
      // organization's environment simply does not match any row there.
      const res = await send([event({ envId: other.envId, sequence: 500 })])
      assert.equal(res.body.accepted, 1)

      const [row] = await h.admin<{ state: string }[]>`
        SELECT state::text AS state FROM environments WHERE env_id = ${other.envId}`
      assert.equal(row?.state, 'running', 'the neighbour’s environment should be untouched')

      const [stored] = await h.admin<{ org_id: string }[]>`
        SELECT org_id FROM events WHERE env_id = ${other.envId} ORDER BY received_at DESC LIMIT 1`
      assert.equal(stored?.org_id, org.orgId, 'the event was filed under the wrong organization')
    } finally {
      await dropOrg(h.admin, other.orgId)
    }
  })
})
