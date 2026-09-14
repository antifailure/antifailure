// members.list answers for the caller's own organization and no other.
//
// It returns user_id since custom role grants needed an id no route supplied,
// and its cursor has always carried one. So the question worth a test is not
// whether an id is visible but whose: a caller in one organization must never
// read another organization's members, including by handing back a cursor
// built from one of theirs. The query names no organization. Two row level
// security policies bound it, through the setting withTenant writes, and either
// one alone holds: tenant_isolation on members keeps the rows to the current
// organization, and self_or_shared_org on users hides everybody who does not
// share it, which the inner join then drops. Opening members alone left both
// cases green for exactly that reason. Opening both turns them red, and that is
// the break these cases exist to catch.
//
// Asked as a viewer, on purpose. A viewer holds environments.view, which is all
// members.list requires, so the least privileged caller that can reach it is
// the one whose answer has to be bounded.

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

interface Row {
  github_login: string
  user_id?: unknown
}

interface MemberPage {
  members: Row[]
  nextCursor: string | null
}

function page(body: unknown): MemberPage {
  return (body as { result: { data: MemberPage } }).result.data
}

describe('members.list answers for one organization', {
  skip: (await available()) ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let mine: Org
  let theirs: Org
  let viewer: SignedIn
  const stamp = randomUUID().slice(0, 8)
  const theirIds: string[] = []
  let forged = ''

  before(async () => {
    h = await startApi()
    mine = await seedOrg(h.admin, 'scope-mine')
    theirs = await seedOrg(h.admin, 'scope-theirs')
    viewer = await signInAs(h, mine, 'viewer')
    await signInAs(h, mine, 'owner')

    // Two members of the other organization. The second is dated a day after
    // the first, so a cursor built from the first names a position that the
    // second sorts after: if the tenant rule did not hold, it is the row that
    // page would carry.
    for (const [i, offset] of [[0, '0 seconds'], [1, '1 day']] as const) {
      const login = `scope-${stamp}-${String(i)}`
      const [user] = await h.admin<{ id: string }[]>`
        INSERT INTO users (github_id, github_login, email, name)
        VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, ${login})
        RETURNING id`
      await h.admin`
        INSERT INTO members (org_id, user_id, role, source, created_at)
        VALUES (${theirs.orgId}, ${user!.id}, 'member', 'manual', now() + ${offset}::interval)`
      theirIds.push(user!.id)
    }
    const [first] = await h.admin<{ created_text: string }[]>`
      SELECT created_at::text AS created_text FROM members
      WHERE org_id = ${theirs.orgId} AND user_id = ${theirIds[0]!}`
    forged = `${first!.created_text}|${theirIds[0]!}`
  })

  after(async () => {
    await dropOrg(h.admin, mine.orgId)
    await dropOrg(h.admin, theirs.orgId)
    await h.admin`DELETE FROM users WHERE github_login LIKE ${`scope-${stamp}-%`}`
    await h.close()
  })

  async function membersOf(orgId: string): Promise<Set<string>> {
    const rows = await h.admin<{ user_id: string }[]>`SELECT user_id FROM members WHERE org_id = ${orgId}`
    return new Set(rows.map((r) => r.user_id))
  }

  function assertOnlyMine(rows: Row[], own: Set<string>, why: string) {
    for (const m of rows) {
      assert.ok(
        !theirIds.includes(String(m.user_id)),
        `${why}: another organization's member ${m.github_login} was returned`,
      )
      assert.ok(own.has(String(m.user_id)), `${why}: ${m.github_login} is not a member of the caller's organization`)
    }
  }

  test('a viewer reads only their own organization\'s members, with their ids', async () => {
    const { status, body } = await callProcedure(h, viewer, 'members.list', 'query', {})
    assert.equal(status, 200, JSON.stringify(body).slice(0, 300))
    const own = await membersOf(mine.orgId)
    const rows = page(body).members
    assertOnlyMine(rows, own, 'no cursor')
    assert.equal(rows.length, own.size, 'every member of the caller\'s organization is listed')
  })

  test('a cursor built from another organization\'s member returns none of theirs', async () => {
    const { status, body } = await callProcedure(h, viewer, 'members.list', 'query', { cursor: forged })
    assert.equal(status, 200, `the forged cursor was refused rather than tested: ${JSON.stringify(body).slice(0, 300)}`)
    assertOnlyMine(page(body).members, await membersOf(mine.orgId), 'forged cursor')
  })
})
