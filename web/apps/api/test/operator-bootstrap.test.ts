// The first operator, and whether the account it makes can actually sign in.
//
// WHAT WAS BROKEN. `admin_users` rows were created by exactly one thing,
// `admin.operators.create`, which needs an operator session, which needs an
// `admin_users` row. On an empty table that is a closed loop: the operator
// portal was unreachable by anybody on any deployment, forever. One level down
// it was worse, because that route writes the row with a NULL password and
// tells the caller to "set a password out of band", and nothing in this
// repository ever wrote `admin_users.password_hash`. Migration 0029's own
// header says the root operator's first password "is written by the bootstrap
// command"; there was no bootstrap command.
//
// THE ASSERTION THAT MATTERS is not that a row appears. admin-signin-route
// already makes the argument for this shape: before those routes existed, every
// assertion about `adminSignIn` passed while nobody could sign in, because
// nothing called it. So the first test here posts the bootstrapped credential to
// POST /v1/admin/signin, keeps the cookie, and reaches an admin procedure with
// it. A row with a hash in it that no sign-in accepts is the same dead feature
// in a new place.

import { describe, it, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import {
  bootstrapOperator,
  setOperatorPassword,
  OperatorBootstrapRefused,
  MIN_PASSWORD_LENGTH,
} from '../src/admin/bootstrap.ts'
import { available, startApi, adminUrl, type ApiHarness } from './harness.ts'

const hasDb = await available()

/** Long enough for the floor, and obviously not a real one. */
const PASSWORD = 'a-bootstrapped-passphrase-nobody-shipped'

let caller = 0
function asNewCaller(): Record<string, string> {
  caller += 1
  return { 'content-type': 'application/json', 'x-forwarded-for': `192.0.2.${caller % 250}` }
}

/**
 * The server's clock, moved to real time before anybody signs in.
 *
 * admin-signin-route.test.ts explains this at length and it applies unchanged:
 * an operator session's lifetime is enforced by two clocks, the application's
 * and the database's now(), and the harness clock starts in the past, so a
 * session it issues is born expired from the database's point of view and the
 * audit append is refused by row-level security. The failure names none of that.
 */
function useRealTime(h: ApiHarness): void {
  h.clock.advance(Date.now() - h.clock.now().getTime())
}

describe('bootstrapping the first operator', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const created: string[] = []

  /**
   * Empties the one root operator slot.
   *
   * There is at most one root operator on a cluster, ever, enforced by a
   * partial unique index, and the triggers in migration 0029 refuse to delete
   * one, to demote one, or to promote a second. That is exactly the property
   * being tested, and it is also what makes this suite unable to clean up after
   * itself without help: `DELETE` raises "the root operator cannot be deleted".
   *
   * Disabling the trigger for the delete is the idiom the two suites that came
   * before this one already use, for the same reason, and it is also the
   * cleanest proof that the trigger is what refuses. Every checkout has its own
   * Postgres container, so this touches nobody else's data.
   */
  async function emptyTheRootSlot(): Promise<void> {
    await h.admin`ALTER TABLE admin_users DISABLE TRIGGER admin_root_is_permanent_del`
    await h.admin`DELETE FROM admin_users WHERE is_root`
    await h.admin`ALTER TABLE admin_users ENABLE TRIGGER admin_root_is_permanent_del`
  }

  before(async () => {
    h = await startApi()
    useRealTime(h)
    // A root left by a previous run is cleared rather than skipped around,
    // because a skip here is a pass with extra steps and this is the one
    // property worth proving.
    await emptyTheRootSlot()
  })
  after(async () => {
    await emptyTheRootSlot()
    for (const email of created) {
      await h.admin`DELETE FROM admin_audit_entries WHERE target_id = ${email}`
      await h.admin`DELETE FROM admin_users WHERE email = ${email}`
    }
    await h.close()
  })

  function freshEmail(prefix: string): string {
    const email = `${prefix}-${randomUUID().slice(0, 8)}@example.test`
    created.push(email)
    return email
  }

  it('creates the root operator, and that account can actually sign in', async () => {
    const email = freshEmail('root')
    const result = await bootstrapOperator({
      adminUrl,
      email,
      name: 'The First Operator',
      password: PASSWORD,
      operator: 'a-test',
    })
    assert.equal(result.applied, true)
    assert.equal(result.role, 'owner')
    assert.ok(result.auditSeq, 'the first operator arrived with nothing in the operator chain')

    // Not "a row exists". A row with a hash that no sign-in accepts is the same
    // dead feature in a new place, so this goes through the route a browser
    // uses and then reaches a procedure that needs the session.
    const signedIn = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: PASSWORD }),
    })
    assert.equal(signedIn.status, 200, await signedIn.text())
    const cookie = signedIn.headers.get('set-cookie')!.split(';')[0]!

    const me = await h.fetch('/trpc/admin.me', { headers: { cookie } })
    const body = await me.text()
    assert.equal(me.status, 200, body)
    assert.ok(body.includes(email), `admin.me did not answer for the bootstrapped operator: ${body}`)
  })

  it('refuses a wrong password against the account it just created', async () => {
    // The other half of the assertion above. A sign-in route that accepted
    // anything would have made the last test pass while proving nothing.
    const email = freshEmail('rootcheck')
    await emptyTheRootSlot()
    await bootstrapOperator({ adminUrl, email, name: 'Second Root', password: PASSWORD })

    const refused = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: `${PASSWORD}-wrong` }),
    })
    assert.equal(refused.status, 401)
  })

  it('refuses to take over a root operator that already exists', async () => {
    // The root operator cannot be deleted, demoted or suspended by anybody
    // including itself, which the database enforces with triggers. A bootstrap
    // that quietly rewrote its credential would be the one way to take that
    // account over, and it would run on a connection string rather than on a
    // session.
    const roots = await h.admin<{ email: string }[]>`SELECT email FROM admin_users WHERE is_root`
    assert.equal(roots.length, 1, 'this test needs exactly one root operator to exist')

    await assert.rejects(
      () =>
        bootstrapOperator({
          adminUrl,
          email: freshEmail('usurper'),
          name: 'Somebody Else',
          password: PASSWORD,
        }),
      (err: unknown) =>
        err instanceof OperatorBootstrapRefused &&
        /already has a root operator/.test(err.message) &&
        /set-operator-password/.test(err.message),
    )
  })

  it('refuses a password short enough to guess offline, and a pasted newline', async () => {
    await emptyTheRootSlot()
    const short = 'x'.repeat(MIN_PASSWORD_LENGTH - 1)
    await assert.rejects(
      () => bootstrapOperator({ adminUrl, email: freshEmail('short'), name: 'X', password: short }),
      (err: unknown) => err instanceof OperatorBootstrapRefused && /at least/.test(err.message),
    )
    // Almost always a heredoc or a paste. It would be part of the password
    // forever, invisible in every attempt to type it again.
    await assert.rejects(
      () =>
        bootstrapOperator({
          adminUrl,
          email: freshEmail('space'),
          name: 'X',
          password: `${PASSWORD}\n`,
        }),
      (err: unknown) => err instanceof OperatorBootstrapRefused && /whitespace/.test(err.message),
    )
  })

  it('writes nothing on a dry run', async () => {
    await emptyTheRootSlot()
    const email = freshEmail('dry')
    const result = await bootstrapOperator({
      adminUrl,
      email,
      name: 'Dry Run',
      password: PASSWORD,
      dryRun: true,
    })
    assert.equal(result.applied, false)
    const rows = await h.admin<{ n: number }[]>`
      SELECT count(*)::int AS n FROM admin_users WHERE email = ${email}`
    assert.equal(rows[0]!.n, 0, 'a dry run created an operator')
  })

  it('refuses a connection the operator tables are not reachable on', async () => {
    // Migration 0029 grants admin_users to nobody, so the application's own
    // credential cannot see the table at all. Failing here, where the message
    // can name the credential, is the difference between a five second fix and
    // reading the migration.
    const appUrl = new URL(adminUrl)
    appUrl.username = 'antifailure_app'
    appUrl.password = 'app-test-password'
    await assert.rejects(
      () =>
        bootstrapOperator({
          adminUrl: appUrl.toString(),
          email: 'nope@example.test',
          name: 'Nope',
          password: PASSWORD,
        }),
      (err: unknown) =>
        err instanceof OperatorBootstrapRefused && /cannot read admin_users/.test(err.message),
    )
  })
})

describe('giving an operator the password the portal cannot', {
  skip: hasDb ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  const created: string[] = []

  before(async () => {
    h = await startApi()
    useRealTime(h)
  })
  after(async () => {
    for (const email of created) {
      await h.admin`DELETE FROM admin_audit_entries WHERE target_id = ${email}`
      await h.admin`DELETE FROM admin_users WHERE email = ${email}`
    }
    await h.close()
  })

  /** An operator exactly as `admin.operators.create` leaves one: a row with no
   *  password, which its own message calls an account that cannot sign in. */
  async function unprovisioned(prefix: string): Promise<string> {
    const email = `${prefix}-${randomUUID().slice(0, 8)}@example.test`
    created.push(email)
    await h.admin`
      INSERT INTO admin_users (email, name, role) VALUES (${email}, 'Created In The Portal', 'support')`
    return email
  }

  it('turns an account that cannot sign in into one that can', async () => {
    const email = await unprovisioned('created')

    // Proved unable first, or the test after it proves nothing. A NULL hash
    // cannot be signed in against because no password hashes to NULL, and this
    // is the state every operator the portal creates sits in.
    const before = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: PASSWORD }),
    })
    assert.equal(before.status, 401)

    const result = await setOperatorPassword({ adminUrl, email, password: PASSWORD })
    assert.equal(result.applied, true)
    assert.equal(result.hadPassword, false)

    const after = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: PASSWORD }),
    })
    assert.equal(after.status, 200, await after.text())
  })

  it('revokes the sessions the old password opened', async () => {
    // A reset that leaves the old sessions alive resets nothing. An operator
    // session lasts twelve hours and reads every tenant's data for all of them,
    // so the window matters more here than anywhere else in the product.
    const email = await unprovisioned('rotated')
    await setOperatorPassword({ adminUrl, email, password: PASSWORD })

    const signedIn = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: PASSWORD }),
    })
    assert.equal(signedIn.status, 200)
    const cookie = signedIn.headers.get('set-cookie')!.split(';')[0]!
    assert.equal((await h.fetch('/trpc/admin.me', { headers: { cookie } })).status, 200)

    const result = await setOperatorPassword({
      adminUrl,
      email,
      password: `${PASSWORD}-rotated`,
    })
    assert.equal(result.hadPassword, true, 'the command did not notice it was replacing one')

    const stillIn = await h.fetch('/trpc/admin.me', { headers: { cookie } })
    assert.notEqual(stillIn.status, 200, 'the session the old password opened still works')

    const withNew = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: `${PASSWORD}-rotated` }),
    })
    assert.equal(withNew.status, 200, 'the new password does not work either')
  })

  it('will not create an account, only give one a password', async () => {
    // The rule breakglass and bootstrap-org both keep: a command that could
    // conjure an identity from a connection string turns a leaked database
    // credential into a way in. Creating operators is the portal's job, under a
    // session, audited to a named actor.
    await assert.rejects(
      () =>
        setOperatorPassword({
          adminUrl,
          email: `ghost-${randomUUID().slice(0, 8)}@example.test`,
          password: PASSWORD,
        }),
      (err: unknown) =>
        err instanceof OperatorBootstrapRefused && /No operator has the address/.test(err.message),
    )
  })

  it('records the change in the operator chain, at high severity', async () => {
    const email = await unprovisioned('audited')
    await setOperatorPassword({ adminUrl, email, password: PASSWORD, operator: 'a-test' })

    const rows = await h.admin<{ action: string; severity: string; origin: string }[]>`
      SELECT action, severity, origin FROM admin_audit_entries WHERE target_id = ${email}`
    assert.equal(rows.length, 1)
    assert.equal(rows[0]!.action, 'operator.password_set')
    // Somebody acquiring a credential that reads every tenant is exactly the
    // entry an incident review comes looking for, so it is not filed as info.
    assert.equal(rows[0]!.severity, 'high')
    assert.equal(rows[0]!.origin, 'system')
  })

  it('leaves a suspended operator unable to sign in, and says so rather than refusing', async () => {
    // Setting a password on a suspended account changes nothing about their
    // access, because adminSignIn checks the suspension. Refusing outright
    // would stop somebody preparing an account for restoration; saying nothing
    // would have them set a password, fail to sign in, and conclude the command
    // is broken.
    const email = await unprovisioned('suspended')
    await h.admin`
      UPDATE admin_users SET suspended_at = now(), suspended_reason = 'a test'
      WHERE email = ${email}`

    const result = await setOperatorPassword({ adminUrl, email, password: PASSWORD })
    assert.equal(result.applied, true)

    const refused = await h.fetch('/v1/admin/signin', {
      method: 'POST',
      headers: asNewCaller(),
      body: JSON.stringify({ email, password: PASSWORD }),
    })
    assert.equal(refused.status, 401, 'a suspended operator signed in with a fresh password')
  })

  it('leaves the row alone on a dry run', async () => {
    const email = await unprovisioned('drypassword')
    const result = await setOperatorPassword({ adminUrl, email, password: PASSWORD, dryRun: true })
    assert.equal(result.applied, false)
    const rows = await h.admin<{ provisioned: boolean }[]>`
      SELECT (password_hash IS NOT NULL) AS provisioned FROM admin_users WHERE email = ${email}`
    assert.equal(rows[0]!.provisioned, false, 'a dry run wrote a credential')
  })
})
