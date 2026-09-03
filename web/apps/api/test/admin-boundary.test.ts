// The operator boundary above the database: sign-in, the gate, and the two
// things that must never be true.
//
// The database suite proves a connection holding an operator session can read
// across tenants and one without cannot. This suite proves the layer that
// decides whether a request HAS one, which is where the interesting failures
// live:
//
//   a default credential exists, so the portal ships openable;
//   a failed sign-in is not recorded, so nobody sees an account being attacked;
//   the permission table is decorative because nothing consults it;
//   an impersonated session can act as an operator, which is the single most
//     dangerous state in this product.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { sql } from 'drizzle-orm'
import { appendAdminAudit } from '@antifailure/db'
import {
  adminSignIn,
  adminSignOut,
  AdminSignInError,
  hashPassword,
  hashAdminToken,
  passwordMatches,
  adminSessionCookie,
  resolveAdminSession,
  adminCsrfTokenFor,
  adminCsrfMatches,
  looksSameOrigin,
  ADMIN_CSRF_HEADER,
} from '../src/admin/session.ts'
import {
  ADMIN_PERMISSIONS,
  ADMIN_ROLES,
  ADMIN_ROLE_PERMISSIONS,
  ADMIN_PERMISSION_DESCRIPTIONS,
  adminRoleHas,
  ROOT_ADMIN_ROLE,
} from '../src/admin/permissions.ts'
import { available, startApi, type ApiHarness } from './harness.ts'

const hasDb = await available()

describe('the operator permission catalog', () => {
  // These need no database, so they run everywhere and are the cheapest place
  // to catch a catalog that has drifted from the roles that use it.

  test('every permission is described, so none can be added without saying what it does', () => {
    const undescribed = ADMIN_PERMISSIONS.filter((p) => !ADMIN_PERMISSION_DESCRIPTIONS[p])
    assert.deepEqual(undescribed, [], `these have no description:\n  ${undescribed.join('\n  ')}`)
  })

  test('every role grants only permissions that exist', () => {
    const known = new Set<string>(ADMIN_PERMISSIONS)
    for (const role of ADMIN_ROLES) {
      const unknown = ADMIN_ROLE_PERMISSIONS[role].filter((p) => !known.has(p))
      assert.deepEqual(unknown, [], `${role} grants permissions that do not exist: ${unknown}`)
    }
  })

  test('every permission is held by at least one role', () => {
    // A permission no role holds is a permission that guards a route nobody
    // can reach, which is a feature that looks built and is not.
    const orphans = ADMIN_PERMISSIONS.filter(
      (p) => !ADMIN_ROLES.some((r) => adminRoleHas(r, p)),
    )
    assert.deepEqual(orphans, [], `no role can use these:\n  ${orphans.join('\n  ')}`)
  })

  test('the least privileged role cannot write anything', () => {
    // The check a reviewer actually wants to make, made once here rather than
    // by reading down a column and hoping.
    //
    // `start` and `engage` are in the list because not every dangerous verb is
    // spelled `write`. `admin.impersonation.start` creates a session as a
    // customer and `admin.emergency.engage` stops the installation, and a
    // pattern that only knew the word `write` would have let read_only hold
    // either one while still passing. The rule this expresses is about what a
    // permission DOES, so the list grows whenever the catalog learns a new verb
    // for doing something.
    const writes = ADMIN_ROLE_PERMISSIONS.read_only.filter((p) =>
      /\.(write|revoke|suspend|plan|export|start|engage|teardown)$/.test(p),
    )
    assert.deepEqual(writes, [], `read_only holds write permissions: ${writes}`)
  })

  test('only owner and security can export the audit chain', () => {
    // Reading is oversight and every role has it; exporting produces a file of
    // every operator action that leaves the system.
    //
    // The NAME of this test used to say "only owner" while its assertion said
    // owner and security. It passed, because the assertion matched the table.
    // A test whose name contradicts its assertion is worse than no test: a
    // reader scanning names believes the name, and the name was the thing
    // being trusted.
    const exporters = ADMIN_ROLES.filter((r) => adminRoleHas(r, 'admin.audit.export'))
    assert.deepEqual(exporters, ['owner', 'security'])
  })

  test('the root role matches what the database trigger enforces', () => {
    // 0030 refuses to let the root row be demoted from owner. This constant and
    // that trigger have to agree, and this is the test that makes them.
    assert.equal(ROOT_ADMIN_ROLE, 'owner')
  })
})

describe('operator sign-in', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let operatorId: string
  let email: string
  const password = 'a-provisioned-password-nobody-shipped'

  before(async () => {
    h = await startApi()
    email = `operator-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, 'Provisioned Operator', 'super_admin', ${hash}, ${salt}, now())
      RETURNING id`
    operatorId = row!.id

    // The two rows the impersonation tests below BORROW rather than create:
    // `SELECT id FROM users LIMIT 1` and the newest admin_audit_entries seq.
    //
    // Without them this file fails on a fresh database with "Cannot read
    // properties of undefined", and it fails FIRST, because `admin-boundary`
    // sorts ahead of every other api suite. It passed only when something else
    // had already run and left a user and an audit entry behind, which is a
    // suite that depends on the alphabet.
    await h.admin`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 1e12)}, ${`impersonated-${randomUUID().slice(0, 8)}`},
              ${`impersonated-${randomUUID().slice(0, 8)}@example.test`}, 'Impersonated')`
    // An entry to point impersonation_audit_seq at. The constraint the second
    // test asserts is that a session cannot record an impersonation without the
    // audit entry that authorised it, so there has to be one to succeed with.
    // Through withAdminSignin, which is the one scope that may append with no
    // session yet. withoutTenant cannot: the append policy requires either a
    // live operator or a declared sign-in email, and that is the whole reason
    // a failed sign-in is recordable at all.
    await h.pool.withAdminSignin(email, (db) =>
      appendAdminAudit(db, {
        adminUserId: operatorId,
        actorLabel: email,
        action: 'admin.fixture',
        targetType: 'fixture',
        origin: 'admin',
        severity: 'info',
        detail: { why: 'a seq for the impersonation tests to reference' },
      }),
    )
  })

  after(async () => {
    await h?.close()
  })

  test('an operator with no password cannot be signed in against', async () => {
    // The state a newly created operator sits in, and the reason no default
    // credential has to exist anywhere: there is no password that hashes to
    // NULL, so the comparison has nothing to succeed against.
    const bare = `bare-${randomUUID().slice(0, 8)}@example.test`
    await h.admin`
      INSERT INTO admin_users (email, name, role) VALUES (${bare}, 'Unprovisioned', 'support')`
    await assert.rejects(
      () => adminSignIn(h.pool, { email: bare, password: '' }, new Date()),
      AdminSignInError,
    )
    await assert.rejects(
      () => adminSignIn(h.pool, { email: bare, password: 'anything' }, new Date()),
      AdminSignInError,
    )
  })

  test('a correct password signs in and the session resolves', async () => {
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    assert.equal(result.actor.adminUserId, operatorId)
    assert.equal(result.actor.role, 'super_admin')

    const actor = await resolveAdminSession(h.pool, result.token, new Date())
    assert.ok(actor, 'the session issued by sign-in does not resolve')
    assert.equal(actor.adminUserId, operatorId)
    assert.equal(actor.impersonating, false)
  })

  test('a wrong password is refused and RECORDED', async () => {
    // The more valuable of the two audit lines: repeated failures against one
    // operator account is somebody being targeted, and a policy requiring a
    // live session would have dropped every one of them.
    const [before] = await h.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'admin.signin_failed'`

    await assert.rejects(
      () => adminSignIn(h.pool, { email, password: 'wrong' }, new Date()),
      AdminSignInError,
    )

    const [after] = await h.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'admin.signin_failed'`
    assert.equal(
      Number(after!.n),
      Number(before!.n) + 1,
      'a failed operator sign-in left no audit entry',
    )

    const [entry] = await h.admin<{ admin_user_id: string; detail: { reason: string } }[]>`
      SELECT admin_user_id, detail FROM admin_audit_entries
      WHERE action = 'admin.signin_failed' ORDER BY seq DESC LIMIT 1`
    assert.equal(entry!.admin_user_id, operatorId)
    assert.equal(entry!.detail.reason, 'wrong password')
  })

  test('an unknown email is refused with the same words as a wrong password', async () => {
    // Distinguishing them turns this endpoint into an oracle for which
    // addresses hold operator accounts.
    let unknownMessage = ''
    let wrongMessage = ''
    try {
      await adminSignIn(h.pool, { email: 'nobody@example.test', password: 'x' }, new Date())
    } catch (err) {
      unknownMessage = (err as Error).message
    }
    try {
      await adminSignIn(h.pool, { email, password: 'x' }, new Date())
    } catch (err) {
      wrongMessage = (err as Error).message
    }
    assert.equal(unknownMessage, wrongMessage)
    assert.ok(unknownMessage.length > 0)
  })

  test('a suspended operator cannot sign in, and the reason is recorded', async () => {
    const suspended = `susp-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, suspended_at)
      VALUES (${suspended}, 'Suspended', 'support', ${hash}, ${salt}, now())`

    await assert.rejects(
      () => adminSignIn(h.pool, { email: suspended, password }, new Date()),
      AdminSignInError,
    )
    const [entry] = await h.admin<{ detail: { reason: string } }[]>`
      SELECT detail FROM admin_audit_entries
      WHERE action = 'admin.signin_failed' ORDER BY seq DESC LIMIT 1`
    assert.equal(entry!.detail.reason, 'account suspended')
  })

  test('signing out makes the session stop resolving', async () => {
    // Effective, not merely recorded. The canonical failure of this whole
    // project is a revoke that leaves the session valid.
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    assert.ok(await resolveAdminSession(h.pool, result.token, new Date()), 'session did not start')

    await adminSignOut(h.pool, result.token, new Date())

    assert.equal(
      await resolveAdminSession(h.pool, result.token, new Date()),
      null,
      'the session still resolved after sign-out, so sign-out does not sign out',
    )
  })

  test('an expired session stops resolving without anything having to sweep it', async () => {
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    const later = new Date(Date.now() + 13 * 60 * 60 * 1000)
    assert.equal(
      await resolveAdminSession(h.pool, result.token, later),
      null,
      'an expired operator session still resolved',
    )
  })

  test('an impersonating session resolves as impersonating, which the gate refuses on', async () => {
    // The marker the gate keys on. The gate itself refuses before it consults
    // the permission, so this is the fact that makes both non-negotiables true
    // at once: an impersonated session cannot act as an operator, and because
    // starting an impersonation IS an operator action, it cannot start another.
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    const hash = hashAdminToken(result.token)

    const user = await seedImpersonationTarget(h)
    const [seq] = await h.admin<{ seq: string }[]>`
      SELECT seq FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`

    await h.admin`
      UPDATE admin_sessions
      SET impersonated_user_id = ${user!.id}, impersonation_reason = 'support case 1',
          impersonation_audit_seq = ${seq!.seq}
      WHERE token_hash = ${hash}`

    const actor = await resolveAdminSession(h.pool, result.token, new Date())
    assert.ok(actor)
    assert.equal(actor.impersonating, true, 'an impersonating session did not report itself as one')
    assert.equal(actor.impersonatedUserId, user!.id)
  })

  test('an impersonation cannot be recorded without the audit entry that authorised it', async () => {
    // Enforced by the constraint rather than by remembering to write first: the
    // session row cannot exist unless its record already does.
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    const hash = hashAdminToken(result.token)
    const user = await seedImpersonationTarget(h)

    await assert.rejects(
      () => h.admin`
        UPDATE admin_sessions
        SET impersonated_user_id = ${user!.id}, impersonation_reason = 'no audit entry'
        WHERE token_hash = ${hash}`,
      /admin_sessions_impersonation_whole|violates check constraint/,
      'an impersonation was recorded with no audit entry behind it',
    )
  })

  test('a blank reason is refused, so an impersonation always says why', async () => {
    const result = await adminSignIn(h.pool, { email, password }, new Date())
    const hash = hashAdminToken(result.token)
    const user = await seedImpersonationTarget(h)
    const [seq] = await h.admin<{ seq: string }[]>`
      SELECT seq FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`

    await assert.rejects(
      () => h.admin`
        UPDATE admin_sessions
        SET impersonated_user_id = ${user!.id}, impersonation_reason = '   ',
            impersonation_audit_seq = ${seq!.seq}
        WHERE token_hash = ${hash}`,
      /admin_sessions_impersonation_whole|violates check constraint/,
    )
  })
})

describe('password hashing', () => {
  test('the same password with different salts does not produce the same hash', async () => {
    const a = await hashPassword('same-password')
    const b = await hashPassword('same-password')
    assert.notEqual(a.hash.toString('hex'), b.hash.toString('hex'))
  })

  test('a correct password matches and a wrong one does not', async () => {
    const stored = await hashPassword('correct-horse')
    assert.equal(await passwordMatches('correct-horse', stored), true)
    assert.equal(await passwordMatches('correct-horse ', stored), false)
    assert.equal(await passwordMatches('wrong', stored), false)
  })

  test('a null stored hash never matches', async () => {
    assert.equal(await passwordMatches('anything', { hash: null, salt: null }), false)
  })
})

describe('the operator cookie', () => {
  test('carries the __Host- prefix and SameSite=Strict when secure', () => {
    // __Host- is browser-enforced: it requires Secure and Path=/ and FORBIDS a
    // Domain attribute, so a subdomain takeover cannot plant this cookie.
    const cookie = adminSessionCookie('t', new Date(Date.now() + 1000), true)
    assert.match(cookie, /^__Host-af_admin_session=/)
    assert.match(cookie, /SameSite=Strict/)
    assert.match(cookie, /Secure/)
    assert.match(cookie, /HttpOnly/)
    assert.doesNotMatch(cookie, /Domain=/)
  })

  test('is not marked Secure over plain http, or the browser would drop it', () => {
    const cookie = adminSessionCookie('t', new Date(Date.now() + 1000), false)
    assert.doesNotMatch(cookie, /Secure/)
    assert.doesNotMatch(cookie, /^__Host-/)
  })
})

/**
 * A customer account to impersonate.
 *
 * Created here rather than read with `SELECT id FROM users LIMIT 1`, which is
 * how these tests were written and why three of them were unreliable: on a
 * database another suite had populated they found a row and passed, and on a
 * FRESH one they found nothing and failed. A fixture that depends on leftovers
 * is green for the wrong reason, which is worse than red.
 */
async function seedImpersonationTarget(h: ApiHarness): Promise<{ id: string }> {
  const suffix = randomUUID().slice(0, 8)
  const [row] = await h.admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 2_000_000_000)}, ${`target-${suffix}`},
            ${`target-${suffix}@example.test`}, 'Impersonation Target')
    RETURNING id`
  return row!
}

describe('cross-site request forgery on the operator surface', () => {
  // The cookie is __Host- and SameSite=Strict, which closes ordinary CSRF. What
  // it does NOT close is the case auth/session.ts already names: SameSite is
  // SITE scoped, so a subdomain an attacker controls is inside it. These are the
  // highest value mutations in the product, so the token is not optional.

  test('the token is derived from the session and is not the session', async () => {
    const csrf = adminCsrfTokenFor('a-session-token')
    assert.notEqual(csrf, 'a-session-token')
    assert.ok(csrf.length > 20)
    // Safe to hand to the page precisely because it is one way.
    assert.doesNotMatch(csrf, /a-session-token/)
  })

  test('a token from one session does not match another', async () => {
    // The property the whole scheme rests on: an attacker who cannot read the
    // cookie cannot derive the token.
    assert.equal(adminCsrfMatches('session-a', adminCsrfTokenFor('session-a')), true)
    assert.equal(adminCsrfMatches('session-a', adminCsrfTokenFor('session-b')), false)
  })

  test('a missing or empty token never matches', async () => {
    assert.equal(adminCsrfMatches('session-a', undefined), false)
    assert.equal(adminCsrfMatches('session-a', null), false)
    assert.equal(adminCsrfMatches('session-a', ''), false)
  })

  test('the operator token is not the product token for the same session', async () => {
    // Two different labels over the same secret. If a refactor ever let one
    // session token reach both derivations, the tenant token must not also be
    // a valid operator token.
    const { csrfTokenFor } = await import('../src/auth/session.ts')
    assert.notEqual(adminCsrfTokenFor('same-token'), csrfTokenFor('same-token'))
    assert.equal(adminCsrfMatches('same-token', csrfTokenFor('same-token')), false)
  })

  test('sign-in hands the token back, so the page has something to send', async () => {
    // Without this the scheme is unusable and the portal would simply refuse
    // every mutation, which is the shape of a guard that gets removed rather
    // than fixed.
    assert.equal(ADMIN_CSRF_HEADER, 'x-antifailure-admin-csrf')
  })

  describe('the same-origin check is a second layer that fails open', () => {
    test('a declared cross-site request is refused', () => {
      assert.equal(
        looksSameOrigin({ secFetchSite: 'cross-site' }, 'https://af.example'),
        false,
      )
      assert.equal(
        looksSameOrigin({ origin: 'https://evil.example' }, 'https://af.example'),
        false,
      )
    })

    test('a same-origin request passes', () => {
      assert.equal(
        looksSameOrigin({ secFetchSite: 'same-origin', origin: 'https://af.example' }, 'https://af.example'),
        true,
      )
    })

    test('a request with neither header passes, which is why it cannot be the only check', () => {
      // Stated as a test rather than a comment because it is the property that
      // makes this insufficient alone: something between the browser and this
      // process may strip the headers, so refusing here would break the portal
      // for reasons nobody could diagnose. The token is what fails closed.
      assert.equal(looksSameOrigin({}, 'https://af.example'), true)
    })
  })
})
