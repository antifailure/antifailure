// The operator's way back in, and the two things it must not become.
//
// A break-glass is a privilege escalation with a good reason attached, so the
// cases that matter most here are the refusals: it must not work from the
// credential the web tier holds, it must not hand out a session, it must not
// leave an organization with no owner, and it must not be reachable by anybody
// who could already sign in. Half of this file is those.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { breakGlass, parseRole, BreakGlassRefused } from '../src/breakglass.ts'
import { syncMembership } from '../src/auth/signin.ts'
import {
  available, startApi, seedOrg, dropOrg, signInAs, callProcedure, errorCode, adminUrl, appUrl,
  type ApiHarness, type Org,
} from './harness.ts'

const hasDatabase = await available()

describe('reading a role off the command line', () => {
  it('accepts the four roles and nothing else', () => {
    for (const role of ['owner', 'admin', 'member', 'viewer']) {
      assert.equal(parseRole(role), role)
    }
    for (const bad of ['Owner', 'root', 'superuser', '']) {
      assert.throws(() => parseRole(bad), BreakGlassRefused, `${bad} was accepted as a role`)
    }
  })
})

describe('break-glass', { skip: hasDatabase ? false : 'no Postgres' }, () => {
  let h: ApiHarness
  let org: Org

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'breakglass')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function memberRow(orgId: string, login: string) {
    const rows = await h.admin<{ role: string; source: string }[]>`
      SELECT m.role::text AS role, m.source FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${orgId} AND lower(u.github_login) = lower(${login})`
    return rows[0] ?? null
  }

  async function entries(orgId: string) {
    return h.admin<{ actor_label: string; action: string; origin: string; detail: Record<string, unknown> }[]>`
      SELECT actor_label, action, origin, detail FROM audit_entries
      WHERE org_id = ${orgId} AND action = 'member.break_glass' ORDER BY seq ASC`
  }

  /** Somebody who has signed in once, which is the only person this can act on. */
  async function account(label: string): Promise<{ id: string; login: string }> {
    const login = `${label}-${randomUUID().slice(0, 6)}`
    const [user] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, ${label})
      RETURNING id`
    return { id: user!.id, login }
  }

  it('promotes somebody who is not a member at all, and says so in the audit log', async () => {
    // The whole path, as an operator walks it: an organization nobody can act
    // in, an account that exists because they signed in once before the App
    // broke, and one command that leaves an owner behind.
    const target = await seedOrg(h.admin, 'locked-out')
    const person = await account('stranded')
    try {
      const before = await entries(target.orgId)
      assert.equal(before.length, 0)

      const result = await breakGlass({
        adminUrl,
        org: target.slug,
        githubLogin: person.login,
        role: 'owner',
        reason: 'the GitHub App was deleted and nobody holds members.manage',
        operator: 'oncall',
      })

      assert.equal(result.from, null, 'they were reported as already holding a role')
      assert.equal(result.to, 'owner')
      assert.equal(result.applied, true)
      assert.ok(result.auditSeq !== null)

      const row = await memberRow(target.orgId, person.login)
      assert.equal(row?.role, 'owner')
      // manual, or the next membership sync quietly undoes the repair.
      assert.equal(row?.source, 'manual')

      const written = await entries(target.orgId)
      assert.equal(written.length, 1, 'the break-glass was not audited')
      assert.equal(written[0]!.actor_label, 'break-glass')
      assert.equal(written[0]!.origin, 'operator')
      assert.equal(written[0]!.detail.added, true)
      assert.equal(written[0]!.detail.previousRole, null)
      assert.equal(written[0]!.detail.operator, 'oncall')
      assert.match(String(written[0]!.detail.reason), /GitHub App was deleted/)
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('is not a login: it writes no session and revokes none', async () => {
    // The single sign-on recovery path spends a code FROM a session. This one
    // has no session to start from and must not invent one, or a database
    // credential becomes a way to be somebody.
    const target = await seedOrg(h.admin, 'no-session')
    const person = await account('quiet')
    try {
      const result = await breakGlass({
        adminUrl,
        org: target.slug,
        githubLogin: person.login,
        role: 'owner',
        reason: 'checking that nothing else happens',
      })
      // The id it reports, not the one the test knows, so this also proves the
      // login was resolved to the account it claims.
      assert.equal(result.userId, person.id)
      const sessions = await h.admin<{ n: string }[]>`
        SELECT count(*) AS n FROM sessions WHERE user_id = ${result.userId}`
      assert.equal(Number(sessions[0]!.n), 0, 'a break-glass issued a session')
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('a dry run reports the change and writes nothing', async () => {
    const target = await seedOrg(h.admin, 'dry')
    const person = await account('curious')
    try {
      const result = await breakGlass({
        adminUrl,
        org: target.slug,
        githubLogin: person.login,
        role: 'owner',
        reason: 'seeing what it would do',
        dryRun: true,
      })
      assert.equal(result.applied, false)
      assert.equal(result.auditSeq, null)
      assert.equal(result.to, 'owner')
      assert.equal(await memberRow(target.orgId, person.login), null, 'a dry run wrote a member row')
      assert.equal((await entries(target.orgId)).length, 0, 'a dry run wrote an audit entry')
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('finds an organization by id as well as by slug', async () => {
    const person = await account('byid')
    const result = await breakGlass({
      adminUrl,
      org: org.orgId,
      githubLogin: person.login,
      role: 'viewer',
      reason: 'an operator has the id out of a log line, not the slug',
      dryRun: true,
    })
    assert.equal(result.orgSlug, org.slug)
  })

  it('refuses a reason that is not one', async () => {
    const person = await account('lazy')
    for (const reason of ['', '   ']) {
      await assert.rejects(
        breakGlass({ adminUrl, org: org.slug, githubLogin: person.login, role: 'owner', reason }),
        (err: Error) => err instanceof BreakGlassRefused && /needs a reason/.test(err.message),
      )
    }
  })

  it('refuses an account that has never signed in, rather than creating one', async () => {
    await assert.rejects(
      breakGlass({
        adminUrl,
        org: org.slug,
        githubLogin: `ghost-${randomUUID().slice(0, 6)}`,
        role: 'owner',
        reason: 'they have never been here',
      }),
      (err: Error) => err instanceof BreakGlassRefused && /cannot create one/.test(err.message),
    )
  })

  it('refuses an organization that does not exist', async () => {
    const person = await account('lost')
    await assert.rejects(
      breakGlass({
        adminUrl,
        org: 'no-such-organization',
        githubLogin: person.login,
        role: 'owner',
        reason: 'a typo in the slug',
      }),
      (err: Error) => err instanceof BreakGlassRefused && /no organization/.test(err.message),
    )
  })

  it('refuses a login that matches two accounts rather than picking one', async () => {
    // A GitHub login can be renamed and the old one claimed by somebody else,
    // so two rows carrying the same login are two different people. Choosing
    // between them here would be choosing who gets the role.
    const target = await seedOrg(h.admin, 'ambiguous')
    const login = `renamed-${randomUUID().slice(0, 6)}`
    try {
      for (const n of [1, 2]) {
        await h.admin`
          INSERT INTO users (github_id, github_login, email, name)
          VALUES (${Math.floor(Math.random() * 1e12)}, ${login},
                  ${`${login}-${n}@example.test`}, ${login})`
      }
      await assert.rejects(
        breakGlass({
          adminUrl, org: target.slug, githubLogin: login, role: 'owner',
          reason: 'two rows carry this login',
        }),
        (err: Error) => err instanceof BreakGlassRefused && /more than one account/.test(err.message),
      )
    } finally {
      await h.admin`DELETE FROM users WHERE github_login = ${login}`
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('will not remove the last owner, which is the state it exists to get out of', async () => {
    const target = await seedOrg(h.admin, 'sole-owner')
    const person = await account('sole')
    try {
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: person.login, role: 'owner',
        reason: 'establishing the owner',
      })
      await assert.rejects(
        breakGlass({
          adminUrl, org: target.slug, githubLogin: person.login, role: 'member',
          reason: 'demoting the only owner',
        }),
        (err: Error) => err instanceof BreakGlassRefused && /only owner/.test(err.message),
      )
      assert.equal((await memberRow(target.orgId, person.login))?.role, 'owner')

      // With a second owner in place the demotion is allowed, so the refusal
      // above is about the last owner and not about demotions in general.
      const other = await account('second')
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: other.login, role: 'owner',
        reason: 'a second owner',
      })
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: person.login, role: 'member',
        reason: 'now it is safe',
      })
      assert.equal((await memberRow(target.orgId, person.login))?.role, 'member')
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('a repair survives the next membership sync', async () => {
    // The ordering that would otherwise waste the whole exercise: break-glass
    // at three in the morning, GitHub comes back at nine, somebody presses Sync
    // from GitHub, and the owner is demoted to whatever GitHub thinks.
    const target = await seedOrg(h.admin, 'survives')
    const installationId = Math.floor(Math.random() * 1e12)
    try {
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${target.orgId}, ${installationId}, ${target.slug}, 'Organization')`
      const login = `repaired-${randomUUID().slice(0, 6)}`
      const githubId = Math.floor(Math.random() * 1e9)
      await h.admin`
        INSERT INTO users (github_id, github_login, email, name)
        VALUES (${githubId}, ${login}, ${`${login}@example.test`}, ${login})`

      await breakGlass({
        adminUrl, org: target.slug, githubLogin: login, role: 'owner',
        reason: 'the App was gone',
      })

      // GitHub, back on its feet, reports them as a plain member.
      h.github.setMembers(target.slug, [
        {
          user: { id: githubId, login, email: `${login}@example.test`, name: login, avatarUrl: null },
          role: 'member',
        },
      ])
      await syncMembership(h.pool, h.clock, h.github, {
        orgId: target.orgId,
        installationId,
        orgLogin: target.slug,
        actorLabel: 'test',
      })
      assert.equal(
        (await memberRow(target.orgId, login))?.role,
        'owner',
        'a membership sync undid the break-glass',
      )
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('refuses the credential the web tier holds', async () => {
    // THE PROPERTY THIS FILE EXISTS FOR. The application connects as
    // antifailure_app, and every tenant table is FORCE ROW LEVEL SECURITY, so
    // that role reaches nothing outside a tenant. Without row_security = off it
    // would not raise: the UPDATE would match zero rows and the command would
    // print success, which is the failure migration 0007 was written about.
    const person = await account('webtier')
    await assert.rejects(
      breakGlass({
        adminUrl: appUrl(),
        org: org.slug,
        githubLogin: person.login,
        role: 'owner',
        reason: 'trying it with the application credential',
      }),
      (err: Error) =>
        err instanceof BreakGlassRefused && /cannot read the members table/.test(err.message),
    )
    assert.equal(await memberRow(org.orgId, person.login), null, 'the application role wrote a member row')
  })

  it('does not loosen the ordinary path: a member still cannot promote themselves', async () => {
    // Break-glass exists beside the permission model, not inside it. Nothing
    // here should make members.setRole reachable by somebody who could not
    // reach it before.
    const target = await seedOrg(h.admin, 'ordinary')
    try {
      const person = await signInAs(h, target, 'member', 'ordinary')
      const { body } = await callProcedure(h, person, 'members.setRole', 'mutation', {
        githubLogin: 'anyone',
        role: 'owner',
      })
      assert.equal(errorCode(body), 'FORBIDDEN', 'a member was allowed to set a role')
      assert.equal(await memberRow(target.orgId, 'anyone'), null)
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('leaves the audit chain intact, so the entry cannot be quietly removed', async () => {
    const target = await seedOrg(h.admin, 'chained')
    const first = await account('chain-a')
    const second = await account('chain-b')
    try {
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: first.login, role: 'owner',
        reason: 'first',
      })
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: second.login, role: 'admin',
        reason: 'second',
      })
      const rows = await h.admin<{ prev_hash: string | null; entry_hash: string }[]>`
        SELECT prev_hash, entry_hash FROM audit_entries
        WHERE org_id = ${target.orgId} ORDER BY seq ASC`
      assert.equal(rows.length, 2)
      assert.equal(rows[0]!.prev_hash, null)
      assert.equal(rows[1]!.prev_hash, rows[0]!.entry_hash, 'the second entry did not chain onto the first')
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('records the run even when the role did not move', async () => {
    // Somebody reaching for this command against an organization is the fact a
    // review is looking for. Whether the row happened to change is a detail
    // inside the entry, not a reason to write nothing.
    const target = await seedOrg(h.admin, 'idempotent')
    const person = await account('twice')
    try {
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: person.login, role: 'owner', reason: 'once',
      })
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: person.login, role: 'owner', reason: 'again',
      })
      const written = await entries(target.orgId)
      assert.equal(written.length, 2)
      assert.equal(written[1]!.detail.changed, false)
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })

  it('used while GitHub is healthy, it touches membership and nothing else', async () => {
    // The ordering the design has to answer for: a break-glass run when nothing
    // is broken. It works, because a command that only works during an outage
    // is one nobody has ever run. What it must not do is reach anything except
    // the one membership row and the audit entry beside it.
    const target = await seedOrg(h.admin, 'healthy')
    const person = await account('healthy')
    try {
      await breakGlass({
        adminUrl, org: target.slug, githubLogin: person.login, role: 'owner',
        reason: 'used on a good day',
      })
      const counts = await h.admin<{ installations: string; members: string; sessions: string }[]>`
        SELECT
          (SELECT count(*) FROM github_installations WHERE org_id = ${target.orgId}) AS installations,
          (SELECT count(*) FROM members WHERE org_id = ${target.orgId}) AS members,
          (SELECT count(*) FROM sessions WHERE org_id = ${target.orgId}) AS sessions`
      assert.equal(Number(counts[0]!.installations), 0, 'a break-glass created an installation')
      assert.equal(Number(counts[0]!.members), 1)
      assert.equal(Number(counts[0]!.sessions), 0, 'a break-glass created a session')
    } finally {
      await dropOrg(h.admin, target.orgId)
    }
  })
})
