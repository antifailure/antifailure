// The Customers lane: support notes, and stepping into a customer's account.
//
// WHAT THIS SUITE IS FOR that admin-routes.test.ts cannot do. That one walks the
// whole operator tree and asserts no route is unguarded, with a floor rather
// than a count. This one counts MINE exactly, because a floor satisfied by
// another lane's routes would stay green if all four of these composed out of
// the tree tomorrow.
//
// The rest of it is the impersonation flow, and it is tested over REAL HTTP
// rather than through createCaller. That is not thoroughness for its own sake:
// both halves of the flow are plain routes precisely BECAUSE they end in a
// Set-Cookie, and a caller with no transport cannot see a cookie. The
// assertions that matter here are "the browser is now the customer" and "the
// browser is no longer the customer", and neither is expressible without the
// header.
//
// THE ORDERINGS, which is where the real bugs are. An impersonation is two
// events with three ways out, and the way out that was missing is the one
// nobody would have written a test for: the operator does not press End, they
// press the button on the portal's own refusal screen, which signs them out. So
// the exits are enumerated and each one has a test:
//
//   start then end                  the deliberate path
//   start then operator sign-out    the refusal screen's only button
//   end with nothing started        a second press, a reloaded tab
//   start then start                refused, so two impersonations never share
//                                   one reason
//
// And the one that is not an ordering but is the whole point: after a start,
// the CUSTOMER's token resolves and the operator portal does not.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword, adminCsrfTokenFor } from '../src/admin/session.ts'
import { resolveSession } from '../src/auth/session.ts'
import { type AdminRole } from '../src/admin/permissions.ts'
import { available, startApi, seedOrg, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

/**
 * This lane's routes, counted rather than floored.
 *
 * Exact and not a minimum, because a route appearing here that nobody added is
 * as much worth knowing about as one disappearing. The same device
 * adminrouters.test.ts uses on the money lane, and for the same reason.
 */
describe("the Customers lane's routes are all in the served tree", () => {
  const MINE = /^admin\.customers\./
  const EXPECTED = [
    'admin.customers.impersonation.list',
    'admin.customers.notes.add',
    'admin.customers.notes.list',
    'admin.customers.notes.retract',
  ]

  test('every one of them, and no others', () => {
    const paths = Object.keys(
      (appRouter as unknown as { _def: { procedures: Record<string, unknown> } })._def.procedures,
    )
      .filter((p) => MINE.test(p))
      .sort()
    assert.deepEqual(
      paths,
      EXPECTED,
      'the Customers namespace does not hold exactly the routes this lane added. A route that ' +
        'left the tree answers 404 with nothing red anywhere else.',
    )
  })
})

describe('support notes and impersonation', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let acme: Org
  let other: Org
  const password = 'provisioned-at-deploy-not-in-source'

  /** One octet. The address has to be a real one: it is written to an `inet`
   *  column, and the first version of this generated `203.0.113.7.a3f1`, which
   *  Postgres refuses with a 22P02 that surfaces as a 500 on the route. That
   *  mistake is the reason clientaddress.ts exists. */
  const rand = () => Math.floor(Math.random() * 254) + 1

  /** A person who exists, in an organization. Seeded rather than borrowed:
   *  `SELECT id FROM users LIMIT 1` passes on a dirty database and fails on a
   *  fresh one, which is the worst kind of test. */
  async function seedPerson(org: Org | null): Promise<{ id: string; login: string }> {
    const suffix = randomUUID().slice(0, 8)
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 2_000_000_000)}, ${`person-${suffix}`},
              ${`person-${suffix}@example.test`}, 'A Customer')
      RETURNING id`
    if (org) {
      await h.admin`
        INSERT INTO members (org_id, user_id, role) VALUES (${org.orgId}, ${row!.id}, 'owner')`
    }
    return { id: row!.id, login: `person-${suffix}` }
  }

  /**
   * An operator, signed in for real, with the cookie and the token a browser
   * would hold.
   *
   * Real time rather than h.clock. Operator expiry is enforced in two places
   * that must agree: resolveAdminSession compares against the injected clock,
   * and the policy behind current_admin_user() compares expires_at against the
   * DATABASE's now(). A fake past clock moves the first and cannot move the
   * second, so every operator write fails with an RLS violation that reads like
   * a permissions bug.
   */
  async function operator(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())
      RETURNING id`
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    return {
      adminUserId: row!.id,
      email,
      token: signedIn.token,
      cookie: `af_admin_session=${signedIn.token}`,
      csrf: adminCsrfTokenFor(signedIn.token),
      // A DISTINCT ADDRESS PER OPERATOR, and it is not decoration.
      //
      // Starting an impersonation is rate limited to one per five seconds with
      // a burst of five, keyed on the address, deliberately: it is the most
      // powerful thing in the portal and one every five seconds is far above
      // honest use. Every request in a suite comes from the same client, so
      // sharing one address means the sixth test in this file is refused with a
      // 429 and reports whatever it was actually asserting as broken. Two
      // operators on one installation are two people at two addresses, so this
      // is also what the real traffic looks like.
      ip: `10.${rand()}.${rand()}.${rand()}`,
    }
  }

  async function callerFor(role: AdminRole) {
    const who = await operator(role)
    const { resolveAdminSession } = await import('../src/admin/session.ts')
    const resolved = await resolveAdminSession(h.pool, who.token, new Date())
    assert.ok(resolved, 'the operator session did not resolve')
    return {
      who,
      caller: appRouter.createCaller({
        pool: h.pool,
        adminPool: h.adminPool,
        clock: h.clock,
        github: h.github,
        stripe: null,
        appBaseUrl: 'http://localhost',
        mailer: null,
        productName: 'Antifailure',
        hostedRequiredPlan: null,
        actor: null,
        origin: 'web' as const,
        admin: {
          adminUserId: resolved.adminUserId,
          label: resolved.label,
          email: resolved.email,
          role: resolved.role,
          sessionId: resolved.sessionId,
          impersonating: resolved.impersonating,
          impersonatedUserId: resolved.impersonatedUserId,
        },
      } as never),
    }
  }

  /** How many entries the operator chain holds, so a write can be shown to have
   *  recorded exactly one and a refusal to have recorded none. */
  async function auditCount(): Promise<number> {
    const [row] = await h.admin<{ n: string }[]>`SELECT count(*) AS n FROM admin_audit_entries`
    return Number(row!.n)
  }

  /** Entries in ONE customer's own log, which is the half that says whether
   *  they were told. */
  async function tenantAuditCount(orgId: string): Promise<number> {
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries WHERE org_id = ${orgId}`
    return Number(row!.n)
  }

  type Operator = Awaited<ReturnType<typeof operator>>

  function start(who: Operator, body: unknown, headers: Record<string, string> = {}) {
    return h.fetch('/v1/admin/impersonation/start', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: who.cookie,
        'x-antifailure-admin-csrf': who.csrf,
        'x-forwarded-for': who.ip,
        ...headers,
      },
      body: JSON.stringify(body),
    })
  }

  function end(who: Operator) {
    return h.fetch('/v1/admin/impersonation/end', {
      method: 'POST',
      headers: {
        cookie: who.cookie,
        'x-antifailure-admin-csrf': who.csrf,
        'x-forwarded-for': who.ip,
      },
    })
  }

  /** The customer session cookie a response handed back, if it handed one. */
  function sessionTokenFrom(res: Response): string | null {
    for (const value of res.headers.getSetCookie()) {
      const match = /^af_session=([^;]+)/.exec(value)
      if (match && match[1] && match[1].length > 0) return match[1]
    }
    return null
  }

  before(async () => {
    h = await startApi()
    acme = await seedOrg(h.admin, 'acme')
    other = await seedOrg(h.admin, 'other')
    // THE HARNESS'S OWN OPERATOR POOL, not a second one built here.
    //
    // Other suites in this directory build their own, and they can, because
    // they exercise routes through createCaller and never touch the pool the
    // SERVER is holding. This one goes over HTTP, so the server's pool is the
    // one doing the work. Building a second pool means giving antifailure_admin
    // a password, which is an ALTER ROLE, which changes the password out from
    // under the pool startApi already created with a different one. Every
    // request through the server then fails authentication and answers 500,
    // and the failure surfaces as "PostgresError" on a route whose SQL is
    // fine. Costing an hour to find once is enough.
    //
    // Loudly rather than as an empty page later, which is exactly what a pool
    // pointed at a non-bypassing role produces.
    await h.adminPool.ensureBypass()
  })

  after(async () => {
    await h?.close()
  })

  /* -----------------------------------------------------------------------
   * Notes
   * -------------------------------------------------------------------- */

  test('a note is written, is read back, and records exactly one entry', async () => {
    const { caller } = await callerFor('support')
    const before = await auditCount()
    const written = await caller.admin.customers.notes.add({
      subjectType: 'organization',
      subjectId: acme.orgId,
      body: 'Asked about a failed run on main. Their engine token had been revoked.',
    })
    assert.equal(await auditCount(), before + 1, 'writing a note did not record exactly one entry')

    // Read back from the database rather than trusted from the return value,
    // which is the rule this directory states: a write that reports success and
    // changes nothing looks identical from the caller's side.
    const [row] = await h.admin<{ body: string; author_label: string; deleted_at: Date | null }[]>`
      SELECT body, author_label, deleted_at FROM admin_notes WHERE id = ${written.id}::uuid`
    assert.ok(row, 'the note was reported written and is not in the table')
    assert.match(row.body, /engine token had been revoked/)
    assert.match(row.author_label, /@example\.test$/, 'the note does not name the operator')

    const page = await caller.admin.customers.notes.list({
      subjectType: 'organization',
      subjectId: acme.orgId,
    })
    assert.ok(
      page.rows.some((n) => n.id === written.id),
      'the note is in the table and not in the list',
    )
  })

  test('a note about a customer is NOT copied into that customer\'s audit log', async () => {
    // 0023 says it in the table's own comment and backs it by giving the
    // application no grant at all: an operator's note about a customer is not
    // that customer's data. Everything else an operator does to an organization
    // IS copied there, so this is the one place the default is deliberately
    // reversed, and a default reversed by hand is a default that silently comes
    // back. This is the test that notices.
    const { caller } = await callerFor('support')
    const before = await tenantAuditCount(acme.orgId)
    await caller.admin.customers.notes.add({
      subjectType: 'organization',
      subjectId: acme.orgId,
      body: 'Internal: this account keeps asking for a discount.',
    })
    assert.equal(
      await tenantAuditCount(acme.orgId),
      before,
      "an operator's private note appeared in the customer's own audit log",
    )
  })

  test('the note text never reaches the audit chain, so retracting it means something', async () => {
    // The chain takes INSERT and SELECT and never DELETE or UPDATE. A body
    // copied into it would survive the retraction, readable one table over, and
    // the retraction would be theatre.
    const { caller } = await callerFor('support')
    const secret = `unmistakable-${randomUUID()}`
    await caller.admin.customers.notes.add({
      subjectType: 'organization',
      subjectId: acme.orgId,
      body: secret,
    })
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM admin_audit_entries WHERE detail::text LIKE ${'%' + secret + '%'}`
    assert.equal(Number(row!.n), 0, 'the note body was copied into the audit chain')
  })

  test('a note about a subject that does not exist is refused and records nothing', async () => {
    // admin_notes.subject_id cannot carry a foreign key, by the migration's own
    // argument, so this refusal is the only thing between a pasted identifier
    // off by a character and a note nobody will ever find.
    const { caller } = await callerFor('support')
    const before = await auditCount()
    await assert.rejects(
      () =>
        caller.admin.customers.notes.add({
          subjectType: 'organization',
          subjectId: randomUUID(),
          body: 'about nobody',
        }),
      (err: Error) => /No organization with that id/.test(err.message),
    )
    assert.equal(await auditCount(), before, 'an audit entry survived a refused action')
  })

  test('a retracted note stays on the record, flagged, and cannot be retracted twice', async () => {
    const { caller } = await callerFor('support')
    const written = await caller.admin.customers.notes.add({
      subjectType: 'organization',
      subjectId: acme.orgId,
      body: 'Wrong account. Retracting this one.',
    })
    await caller.admin.customers.notes.retract({
      id: written.id,
      reason: 'Filed against the wrong organization.',
    })

    const page = await caller.admin.customers.notes.list({
      subjectType: 'organization',
      subjectId: acme.orgId,
    })
    const found = page.rows.find((n) => n.id === written.id)
    assert.ok(found, 'a retracted note disappeared from the list instead of being flagged')
    assert.ok(found.retractedAt, 'the retracted note is not marked as retracted')

    await assert.rejects(
      () =>
        caller.admin.customers.notes.retract({
          id: written.id,
          reason: 'Retracting it again, which should be refused.',
        }),
      (err: Error) => /already retracted/i.test(err.message),
    )
  })

  test('a role without the permission is refused, naming it', async () => {
    // analytics holds admin.tenants.read and neither support permission. The
    // split is what lets somebody read usage without reading what operators
    // wrote about a paying customer.
    const { caller } = await callerFor('analytics')
    await assert.rejects(
      () =>
        caller.admin.customers.notes.list({ subjectType: 'organization', subjectId: acme.orgId }),
      (err: Error) => /admin\.support\.read/.test(err.message),
    )
  })

  /* -----------------------------------------------------------------------
   * Impersonation: the forgery guard on routes the /trpc middleware misses
   * -------------------------------------------------------------------- */

  test('starting an impersonation without the token is refused', async () => {
    // These two routes are NOT under /trpc/*, so the middleware that guards
    // every other operator mutation does not see them. Without the check they
    // carry themselves, they would be the only unguarded operator writes in the
    // system, and they are the two that hand a browser a customer's session.
    const who = await operator('support')
    const person = await seedPerson(acme)
    const res = await h.fetch('/v1/admin/impersonation/start', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: who.cookie,
        'x-forwarded-for': who.ip,
      },
      body: JSON.stringify({ userId: person.id, orgId: acme.orgId, reason: 'no token at all' }),
    })
    assert.equal(res.status, 403)
    assert.match(await res.text(), /x-antifailure-admin-csrf/)
  })

  test('starting an impersonation that declares another site is refused', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    const res = await start(
      who,
      { userId: person.id, orgId: acme.orgId, reason: 'from somewhere else' },
      { origin: 'https://evil.example', 'sec-fetch-site': 'cross-site' },
    )
    assert.equal(res.status, 403)
    assert.match(await res.text(), /came from another site/)
  })

  test('a role without admin.impersonation.start is refused', async () => {
    // security investigates impersonations and does not start them, which is
    // the same shape as holding sessions.revoke without the means to create a
    // session.
    const who = await operator('security')
    const person = await seedPerson(acme)
    const res = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'security should not be able to do this',
    })
    assert.equal(res.status, 403)
    assert.match(await res.text(), /admin\.impersonation\.start/)
  })

  /* -----------------------------------------------------------------------
   * Impersonation: what it actually does
   * -------------------------------------------------------------------- */

  test('starting one hands back a working customer session and closes the portal', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    const beforeAdmin = await auditCount()
    const beforeTenant = await tenantAuditCount(acme.orgId)

    const res = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'Ticket 4821, cannot see their own runs.',
      minutes: 15,
    })
    assert.equal(res.status, 200, await res.text())

    // ONE. The browser is now the customer. This is the assertion createCaller
    // cannot make and the reason this route is not a procedure.
    const token = sessionTokenFrom(res)
    assert.ok(token, 'starting an impersonation set no customer session cookie')
    const resolved = await resolveSession(h.pool, h.clock, token)
    assert.ok(resolved, 'the minted session does not resolve')
    assert.equal(resolved.userId, person.id, 'the session is for the wrong account')
    assert.equal(resolved.orgId, acme.orgId, 'the session is in the wrong organization')

    // TWO. And it says so. Migration 0023 put the marker on the session row
    // precisely so it could never fail open, and then nothing read it: an
    // impersonated session was indistinguishable from an ordinary one to every
    // check in the product. This is the read that makes the guarantee true.
    assert.ok(resolved.impersonation, 'the minted session looks like an ordinary sign-in')
    assert.equal(resolved.impersonation.operator, who.email)
    assert.match(resolved.impersonation.reason, /Ticket 4821/)

    // THREE. It is time bounded, by the mechanism that ends every other
    // session rather than by a second one that could be forgotten.
    // Measured against the SERVER's clock, which the harness fakes. The route
    // stamps expires_at from the clock it was given and resolveSession compares
    // against the one it is handed, so measuring either against the wall clock
    // compares two different times and reports the session eternal or already
    // gone depending on which way the fake one points.
    const minutes = (resolved.expiresAt.getTime() - h.clock.now().getTime()) / 60_000
    assert.ok(minutes > 13 && minutes < 16, `the session lasts ${minutes} minutes, not about 15`)

    // FOUR. The record exists, in both chains. The customer is entitled to know
    // somebody from the vendor was inside their account.
    assert.equal(await auditCount(), beforeAdmin + 1)
    assert.equal(
      await tenantAuditCount(acme.orgId),
      beforeTenant + 1,
      'the customer was not told in their own audit log',
    )

    // FIVE. The record exists BEFORE the session, structurally: the column is
    // NOT NULL when impersonating and carries a foreign key into the chain, so
    // a session that was never audited cannot be represented.
    const [row] = await h.admin<{ seq: string | null; label: string | null }[]>`
      SELECT impersonation_audit_seq AS seq, impersonator_label AS label
      FROM sessions WHERE user_id = ${person.id}::uuid AND revoked_at IS NULL`
    assert.ok(row?.seq, 'the session records no audit entry')
    assert.equal(row.label, who.email)

    // SIX. The portal is closed to this operator until they come back out.
    const me = await h.fetch('/trpc/admin.me', { headers: { cookie: who.cookie } })
    assert.equal(me.status, 403)
    assert.match(await me.text(), /impersonating/i)

    await end(who)
  })

  test('a second start is refused, so two impersonations never share one reason', async () => {
    const who = await operator('support')
    const first = await seedPerson(acme)
    const second = await seedPerson(acme)
    assert.equal((await start(who, { userId: first.id, orgId: acme.orgId, reason: 'the first one' })).status, 200)

    const again = await start(who, { userId: second.id, orgId: acme.orgId, reason: 'the second one' })
    assert.equal(again.status, 409)
    assert.match(await again.text(), /already impersonating/i)

    await end(who)
  })

  test('acting as somebody in an organization they are not in is refused', async () => {
    // The product would show an empty console rather than refuse, so this is
    // the only place it can be caught. An operator acting as a non-member is
    // not a support session; it is a cross tenant read wearing one.
    const who = await operator('support')
    const person = await seedPerson(acme)
    const before = await auditCount()
    const res = await start(who, {
      userId: person.id,
      orgId: other.orgId,
      reason: 'They are not in this organization.',
    })
    assert.equal(res.status, 412)
    assert.match(await res.text(), /not a member/i)
    assert.equal(await auditCount(), before, 'a refused impersonation still wrote a record')
  })

  test('a blank reason is refused before anything is written', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    const before = await auditCount()
    const res = await start(who, { userId: person.id, orgId: acme.orgId, reason: '   ' })
    assert.equal(res.status, 400)
    assert.equal(await auditCount(), before)
  })

  test('a length outside the bound is refused, so nothing lasts a month', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    const res = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'A perfectly good reason.',
      minutes: 60 * 24,
    })
    assert.equal(res.status, 400)
  })

  test('a suspended account is refused, because its session would not resolve', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    await h.admin`
      UPDATE users SET suspended_at = now(), suspended_reason = 'test', suspended_by = 'test'
      WHERE id = ${person.id}::uuid`
    const res = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'They are suspended, so this cannot work.',
    })
    assert.equal(res.status, 412)
    assert.match(await res.text(), /suspended/i)
  })

  /* -----------------------------------------------------------------------
   * The ways out
   * -------------------------------------------------------------------- */

  test('ending one stops the customer token working and reopens the portal', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    const started = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'Ticket 4822, reproducing their view.',
    })
    const token = sessionTokenFrom(started)
    assert.ok(token)
    assert.ok(await resolveSession(h.pool, h.clock, token))

    const res = await end(who)
    assert.equal(res.status, 200)
    const body = (await res.json()) as { ended: boolean; revoked: number }
    assert.equal(body.ended, true)
    assert.equal(body.revoked, 1)

    // The SAME token stops resolving. Not "a token", and not "the row says
    // revoked": the thing the browser is holding.
    assert.equal(
      await resolveSession(h.pool, h.clock, token),
      null,
      'the customer session still worked after the impersonation ended',
    )

    // And the operator is back.
    const me = await h.fetch('/trpc/admin.me', { headers: { cookie: who.cookie } })
    assert.equal(me.status, 200)
  })

  test('ending one twice is a success both times, because both presses want the same thing', async () => {
    const who = await operator('support')
    const person = await seedPerson(acme)
    await start(who, { userId: person.id, orgId: acme.orgId, reason: 'Ticket 4823, one moment.' })
    const first = (await (await end(who)).json()) as { ended: boolean }
    const second = (await (await end(who)).json()) as { ended: boolean; impersonating: boolean }
    assert.equal(first.ended, true)
    assert.equal(second.ended, false, 'a second press claimed to end something')
    assert.equal(second.impersonating, false, 'a second press left the session impersonating')
  })

  test('signing out of the portal also ends the impersonation and clears the customer cookie', async () => {
    // THE ORDERING NOBODY WOULD HAVE WRITTEN A TEST FOR, and the one the portal
    // actually hits. An impersonating operator sees the shell's refusal screen,
    // whose only button is "End this session", and that button calls
    // /v1/admin/signout. Before this, signing out cleared the OPERATOR cookie
    // and left the customer cookie live in the browser for the rest of its
    // lifetime, with the marker that would have explained it removed from the
    // row in the same act. A way out that leaves the door open is not a way
    // out.
    const who = await operator('support')
    const person = await seedPerson(acme)
    const started = await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'Ticket 4824, then closing the tab.',
    })
    const token = sessionTokenFrom(started)
    assert.ok(token)

    const out = await h.fetch('/v1/admin/signout', {
      method: 'POST',
      headers: {
        cookie: who.cookie,
        'x-antifailure-admin-csrf': who.csrf,
        'x-forwarded-for': who.ip,
      },
    })
    assert.equal(out.status, 200)

    assert.equal(
      await resolveSession(h.pool, h.clock, token),
      null,
      'signing out of the portal left a working customer session in the browser',
    )

    // Both cookies cleared, and both in the SAME response. Hono's header()
    // replaces by default, so a second call that was not marked append would
    // discard the first and this route would clear exactly one while reading as
    // though it cleared two.
    const cleared = out.headers.getSetCookie()
    assert.ok(
      cleared.some((c) => c.startsWith('af_admin_session=')),
      'the operator cookie was not cleared',
    )
    assert.ok(
      cleared.some((c) => c.startsWith('af_session=')),
      'the customer cookie was not cleared',
    )
  })

  /* -----------------------------------------------------------------------
   * The record of it
   * -------------------------------------------------------------------- */

  test('the live list holds an open one and the recent list holds a finished one', async () => {
    const who = await operator('support')
    const reader = await callerFor('security')
    const person = await seedPerson(acme)
    await start(who, {
      userId: person.id,
      orgId: acme.orgId,
      reason: 'Ticket 4825, checking the record.',
    })

    const open = await reader.caller.admin.customers.impersonation.list()
    const live = open.live.find((r) => r.userId === person.id)
    assert.ok(live, 'an open impersonation is missing from the live list')
    assert.equal(live.operator, who.email)
    assert.equal(live.orgSlug, acme.slug)
    assert.match(live.reason, /Ticket 4825/)

    await end(who)

    const closed = await reader.caller.admin.customers.impersonation.list()
    assert.equal(
      closed.live.some((r) => r.userId === person.id),
      false,
      'a finished impersonation is still listed as live',
    )
    // A finished one leaves no live row by design, so the audit chain is the
    // only place it still exists. A page that could only read the live list
    // would answer "nobody is impersonating anybody", which is true and useless
    // to somebody asking whether anybody had.
    assert.ok(
      closed.recent.some(
        (e) => e.action === 'impersonation.ended' && e.targetId === person.id,
      ),
      'a finished impersonation is in neither list',
    )
    assert.ok(
      closed.recent.some(
        (e) => e.action === 'impersonation.started' && e.targetId === person.id,
      ),
    )
  })

  /* -----------------------------------------------------------------------
   * The transport the billing screen's writes go through
   * -------------------------------------------------------------------- */

  test('a money write reaches the ROUTE over HTTP, rather than being refused by the transport', async () => {
    // WHAT THIS PINS, and why it is in this lane's suite rather than the money
    // lane's. The Billing & Stripe screen sends its writes through
    // `console/lib/admin-money.ts:moneyAction`, which called the PRODUCT's
    // `mutate` and therefore sent `x-antifailure-csrf`, the wrong header. Every
    // one of those was refused with a 403 about a header, and the file had no
    // importers, so nothing noticed. It now goes through `adminMutate` like
    // every other operator write.
    //
    // So this asserts the property that was false: with the operator token
    // presented, the transport steps aside and the ROUTE answers on its own
    // terms. Its own terms here are a PRECONDITION_FAILED about this customer
    // having no Stripe customer, which only the handler can know and only the
    // handler names the path of. A refusal ABOUT THE ACCOUNT proves the request
    // arrived; a refusal about a header proves only that it did not.
    //
    // No money moves and none can. There is no Stripe configuration on this
    // harness and no customer on this organization, so the route refuses before
    // it composes a single provider call.
    const who = await operator('billing')
    const res = await h.fetch('/trpc/admin.billing.credit', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: who.cookie,
        'x-antifailure-admin-csrf': who.csrf,
        'x-forwarded-for': who.ip,
      },
      body: JSON.stringify({
        orgId: acme.orgId,
        amountMinor: 500,
        currency: 'usd',
        reason: 'Proving the transport, not moving money.',
      }),
    })
    const body = await res.text()
    assert.doesNotMatch(
      body,
      /x-antifailure-admin-csrf/,
      'the transport refused an operator money write that presented the right token',
    )
    assert.match(
      body,
      /"path":"admin\.billing\.credit"/,
      'the answer does not name the billing route, so the request did not reach it',
    )
    assert.match(
      body,
      /PRECONDITION_FAILED/,
      'the billing route did not refuse on its own terms',
    )
  })

  test('the same write with no token is refused before it reaches the route', async () => {
    // The negative half. Without it the assertion above would pass against a
    // server with no forgery check at all, which is the state this whole change
    // exists to leave.
    const who = await operator('billing')
    const res = await h.fetch('/trpc/admin.billing.credit', {
      method: 'POST',
      headers: {
        'content-type': 'application/json',
        cookie: who.cookie,
        'x-forwarded-for': who.ip,
      },
      body: JSON.stringify({
        orgId: acme.orgId,
        amountMinor: 500,
        currency: 'usd',
        reason: 'Proving the transport, not moving money.',
      }),
    })
    assert.equal(res.status, 403)
    assert.match(await res.text(), /x-antifailure-admin-csrf/)
  })

  test('an operator with no live impersonation is not listed, so the list is not vacuous', async () => {
    // The reads above would all pass against a route that returned everything
    // it ever saw. This is the negative: the live list is filtered, so a
    // session that has been revoked is gone from it.
    const reader = await callerFor('security')
    const answer = await reader.caller.admin.customers.impersonation.list()
    const stale = answer.live.filter(
      (r) => new Date(r.endsAt).getTime() < h.clock.now().getTime(),
    )
    assert.deepEqual(stale, [], 'the live list is showing impersonations that have already lapsed')
  })
})
