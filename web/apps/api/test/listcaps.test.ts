// The two lists that had no upper bound.
//
// Every other list procedure on the console takes a limit capped at two
// hundred, or applies one itself, and says why: an organization with ten
// thousand of something must not be able to ask for all of it in one answer.
// members.list and runtimes.list returned every row. A membership synced from
// a large GitHub organization is exactly the ten thousand case, and it arrives
// through a button on the Members page rather than through anything a person
// would think of as bulk.
//
// The bound is proved by crossing it: two hundred and one rows, one request,
// two hundred back. For members the rest is reachable through the cursor and
// the cursor is exact, which matters because a sync writes its whole batch in
// one transaction and every row shares one created_at to the microsecond.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  available,
  callProcedure,
  dropOrg,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'

interface MemberPage {
  members: { github_login: string; created_at: string }[]
  nextCursor: string | null
}

function page(body: unknown): MemberPage {
  return (body as { result: { data: MemberPage } }).result.data
}

describe('members.list and runtimes.list are bounded', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  const extra = 201
  const stamp = randomUUID().slice(0, 8)

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'listcaps')
    owner = await signInAs(h, org, 'owner')

    // Two hundred and one members in ONE transaction, the way members.sync
    // writes them, so every one of them carries the same created_at. A cursor
    // on the timestamp alone cannot page through this batch.
    await h.admin.begin(async (tx) => {
      for (let i = 0; i < extra; i += 1) {
        const login = `cap-${stamp}-${String(i).padStart(3, '0')}`
        const [user] = await tx<{ id: string }[]>`
          INSERT INTO users (github_id, github_login, email, name)
          VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, ${login})
          RETURNING id`
        await tx`
          INSERT INTO members (org_id, user_id, role, source)
          VALUES (${org.orgId}, ${user!.id}, 'member', 'github')`
      }
    })

    for (let i = 0; i < extra; i += 1) {
      await h.admin`
        INSERT INTO runtimes (org_id, name, provider)
        VALUES (${org.orgId}, ${`rt-${stamp}-${String(i).padStart(3, '0')}`}, 'local')`
    }
  })
  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.admin`DELETE FROM users WHERE github_login LIKE ${`cap-${stamp}-%`}`
    await h.close()
  })

  test('members.list answers at most two hundred rows in one request', async () => {
    const { status, body } = await callProcedure(h, owner, 'members.list', 'query', {})
    assert.equal(status, 200, JSON.stringify(body).slice(0, 300))
    const first = page(body)
    assert.equal(first.members.length, 200)
    assert.ok(first.nextCursor, 'two hundred and two members and no cursor for the rest')
  })

  test('and the cursor reaches every remaining member exactly once', async () => {
    const seen = new Set<string>()
    let cursor: string | null = null
    let requests = 0
    do {
      const { status, body } = await callProcedure(h, owner, 'members.list', 'query', {
        limit: 200,
        ...(cursor ? { cursor } : {}),
      })
      assert.equal(status, 200, JSON.stringify(body).slice(0, 300))
      const p = page(body)
      for (const m of p.members) {
        assert.ok(!seen.has(m.github_login), `${m.github_login} was returned twice`)
        seen.add(m.github_login)
      }
      cursor = p.nextCursor
      requests += 1
    } while (cursor && requests < 10)
    // The owner who is asking, plus the batch.
    assert.equal(seen.size, extra + 1)
    assert.equal(requests, 2)
  })

  test('a limit over two hundred is refused rather than honoured', async () => {
    const { status } = await callProcedure(h, owner, 'members.list', 'query', { limit: 201 })
    assert.equal(status, 400)
  })

  test('a cursor nobody was given is refused with a sentence, not a database error', async () => {
    const { status, body } = await callProcedure(h, owner, 'members.list', 'query', {
      cursor: 'not-a-cursor',
    })
    assert.equal(status, 400)
    const message = (body as { error: { message: string } }).error.message
    assert.match(message, /cursor/)
  })

  test('runtimes.list answers at most two hundred rows', async () => {
    const { status, body } = await callProcedure(h, owner, 'runtimes.list', 'query', {
      includeRemoved: false,
    })
    assert.equal(status, 200, JSON.stringify(body).slice(0, 300))
    const rows = (body as { result: { data: unknown[] } }).result.data
    assert.equal(rows.length, 200)
  })
})
