// The Security & Governance lane, proved rather than described.
//
// WHAT THIS SUITE IS FOR. Every route in admin/security.ts answers a question
// somebody will act on: whether a credential is live, whether an erasure is
// stuck, what is held about a named person, and whether the operator log can be
// shown to somebody who does not trust us. A route that returns a plausible
// shape and the wrong answer is worse on this lane than on any other, because
// the reader has no independent way to check it.
//
// So every test here seeds a fact, asks the route, and asserts the answer
// matches the fact. Nothing asserts a shape alone, and nothing trusts a return
// value about an audit entry: the entries are read back out of the table.
//
// THE THREE THINGS IT PINS THAT NOTHING ELSE DOES:
//
//   1. admin.audit.export produces a FILE. The permission has existed since the
//      catalog did with no route behind it, and a permission that guards
//      nothing is a capability that looks built. The test parses the document.
//   2. The export is VERIFIED over its own range. verifyAdminAuditChain had
//      zero production call sites before this lane; a range walk that seeds
//      itself wrongly reports every intact export as broken, so the test
//      exports a slice from the middle of the chain and asserts it verifies.
//   3. Looking up a person is RECORDED with the person's name on it. The
//      automatic read auditing records the route; only an explicit entry
//      records the subject, and that is the entry a data protection request is
//      answered from.

import { after, before, describe, test } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword } from '../src/admin/session.ts'
import { type AdminRole } from '../src/admin/permissions.ts'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { available, startApi, seedOrg, adminUrl, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

describe('security and governance', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  let adminPool: AdminPool
  const password = 'provisioned-at-deploy-not-in-source'

  /** Signs an operator in with a role and returns a caller carrying that
   *  session, which is the path a real request takes.
   *
   *  REAL TIME, not h.clock, and it is the same trap admin-routes.test.ts
   *  documents: resolveAdminSession compares expiry against the injected clock
   *  and the RLS policy behind current_admin_user() compares it against the
   *  DATABASE's now(). A fake past clock leaves every operator write failing
   *  with an RLS violation that reads like a permissions bug. */
  async function callerFor(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())`
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    const { resolveAdminSession } = await import('../src/admin/session.ts')
    const resolved = await resolveAdminSession(h.pool, signedIn.token, new Date())
    assert.ok(resolved, 'the operator session did not resolve')
    return {
      email,
      caller: appRouter.createCaller({
        pool: h.pool,
        adminPool,
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
          sessionHash: resolved.sessionHash,
          impersonating: resolved.impersonating,
          impersonatedUserId: resolved.impersonatedUserId,
        },
      } as never),
    }
  }

  /** One customer account, created rather than borrowed. `SELECT id FROM users
   *  LIMIT 1` passes on a dirty database and fails on a fresh one, which is a
   *  suite that depends on what ran before it. */
  async function seedUser(label: string): Promise<{ id: string; login: string; email: string }> {
    const login = `${label}-${randomUUID().slice(0, 8)}`
    const email = `${login}@example.test`
    const [row] = await h.admin<{ id: string }[]>`
      INSERT INTO users (github_id, github_login, email, name)
      VALUES (${Math.floor(Math.random() * 2_000_000_000)}, ${login}, ${email}, ${label})
      RETURNING id`
    return { id: row!.id, login, email }
  }

  async function auditCount(action: string): Promise<number> {
    const [row] = await h.admin<{ n: string }[]>`
      SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = ${action}`
    return Number(row!.n)
  }

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'sec')
    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'operator-test-password'
    adminPool = createAdminPool({ url: u.toString() })
    // Loudly here rather than as an empty page later, which is exactly what a
    // pool pointed at a non-bypassing role produces on every screen in this
    // lane and is indistinguishable from an installation with no customers.
    await adminPool.ensureBypass()
  })

  after(async () => {
    await adminPool?.close()
    await h?.close()
  })

  // -------------------------------------------------------------------------
  // Security Center
  // -------------------------------------------------------------------------

  describe('the credential inventory', () => {
    test('a live token, an expired one and a revoked one are told apart', async () => {
      // Three rows differing only in the two columns that decide the state, so
      // a route that reported all three the same would have to get all three
      // wrong in the same direction.
      const now = h.clock.now()
      const past = new Date(now.getTime() - 60 * 60 * 1000).toISOString()
      const future = new Date(now.getTime() + 60 * 60 * 1000).toISOString()
      const tag = randomUUID().slice(0, 8)
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix, expires_at, revoked_at)
        VALUES
          (${org.orgId}, ${`live-${tag}`}, ${Buffer.from(randomUUID())}, 'af_live', ${future}, NULL),
          (${org.orgId}, ${`gone-${tag}`}, ${Buffer.from(randomUUID())}, 'af_gone', ${past}, NULL),
          (${org.orgId}, ${`dead-${tag}`}, ${Buffer.from(randomUUID())}, 'af_dead', NULL, ${past})`

      const { caller } = await callerFor('security')
      const live = await caller.admin.security.credentials({ kind: 'engine_token', state: 'live', limit: 200 })
      const expired = await caller.admin.security.credentials({ kind: 'engine_token', state: 'expired', limit: 200 })
      const revoked = await caller.admin.security.credentials({ kind: 'engine_token', state: 'revoked', limit: 200 })

      const names = (page: { rows: { label: string }[] }) => page.rows.map((r) => r.label)
      assert.ok(names(live).includes(`live-${tag}`), 'the live token is not in the live list')
      assert.ok(!names(live).includes(`gone-${tag}`), 'an expired token was reported as live')
      assert.ok(!names(live).includes(`dead-${tag}`), 'a revoked token was reported as live')
      assert.ok(names(expired).includes(`gone-${tag}`), 'the expired token is not in the expired list')
      assert.ok(names(revoked).includes(`dead-${tag}`), 'the revoked token is not in the revoked list')
    })

    test('an unused token is flagged only after the grace period', async () => {
      // The grace period is the whole difference between a finding and noise.
      // A credential minted this morning and not used yet is somebody setting
      // up, and flagging it teaches the reader to ignore the flag.
      const now = h.clock.now()
      const old = new Date(now.getTime() - 400 * 24 * 60 * 60 * 1000).toISOString()
      const tag = randomUUID().slice(0, 8)
      await h.admin`
        INSERT INTO engine_tokens (org_id, name, token_hash, prefix, created_at, last_used_at)
        VALUES
          (${org.orgId}, ${`stale-${tag}`}, ${Buffer.from(randomUUID())}, 'af_st', ${old}, NULL),
          (${org.orgId}, ${`fresh-${tag}`}, ${Buffer.from(randomUUID())}, 'af_fr', now(), NULL)`

      const { caller } = await callerFor('infrastructure')
      const flagged = await caller.admin.security.credentials({ state: 'flagged', limit: 200 })
      const labels = flagged.rows.map((r) => r.label)
      assert.ok(labels.includes(`stale-${tag}`), 'a token unused for a year was not flagged')
      assert.ok(
        !labels.includes(`fresh-${tag}`),
        'a token minted moments ago and not yet used was flagged, which makes the flag noise',
      )
      const stale = flagged.rows.find((r) => r.label === `stale-${tag}`)
      assert.deepEqual(stale?.flags, ['never_used'])
    })

    test('the summary and the list agree, because they are the same predicate', async () => {
      // The failure this guards: a summary that counts one way and a list that
      // filters another, so the number above the table and the length of the
      // table disagree and nobody can tell which is wrong.
      const { caller } = await callerFor('security')
      const posture = await caller.admin.security.posture()
      const listed = await caller.admin.security.credentials({
        kind: 'engine_token',
        state: 'live',
        limit: 200,
      })
      assert.equal(
        posture.engineTokens.live,
        listed.rows.length,
        'the posture count and the live list disagree about how many engine tokens are live',
      )
      assert.equal(listed.nextCursor, null, 'the fixture set grew past one page, so this comparison is void')
    })

    test('no credential response carries a secret', async () => {
      // The same assertion admin-routes.test.ts makes about the operators
      // route, made again here because this lane reads three tables that hold
      // a hash or a ciphertext in a neighbouring column.
      const { caller } = await callerFor('owner')
      const page = await caller.admin.security.credentials({ limit: 50 })
      const text = JSON.stringify(page)
      for (const key of ['token_hash', 'tokenHash', 'ciphertext', 'nonce', 'password_hash', 'code_hash']) {
        assert.ok(!text.includes(key), `a credential response carried ${key}`)
      }
    })
  })

  describe('single sign-on, which nothing in the product writes yet', () => {
    // THE ROUTE IS TESTED EVEN THOUGH NO CUSTOMER CAN REACH THE STATE. Migration
    // 0014 built the whole schema and nothing reads or writes it, which
    // writers.test.ts records as an exemption with the reason. The operator page
    // says that in words rather than rendering a row of zeroes, and it renders
    // the real panel the moment a connection exists. This is what makes that
    // second half a claim somebody checked: the fixture writes the rows the
    // product cannot yet write, and the route is asked what it says about them.
    test('reports enabled, enforced and bypassable as three different states', async () => {
      const handle = () => randomUUID().replace(/-/g, '') + randomUUID().replace(/-/g, '').slice(0, 8)
      // The entity id is globally unique by a partial index, deliberately,
      // because IdP-initiated SAML arrives with an Issuer and no handle. A
      // fixture that reuses one passes on a fresh database and fails on the
      // second run, which is a suite that depends on what ran before it.
      const entity = () => `https://idp-${randomUUID().slice(0, 8)}.example/entity`
      const enforced = await seedOrg(h.admin, 'sso-on')
      const bypassable = await seedOrg(h.admin, 'sso-soft')
      const off = await seedOrg(h.admin, 'sso-off')
      await h.admin`
        INSERT INTO sso_connections
          (org_id, handle, kind, display_name, enabled, enforced,
           idp_entity_id, idp_sso_url, idp_certificates)
        VALUES
          (${enforced.orgId}, ${handle()}, 'saml', 'Okta', true, true,
           ${entity()}, 'https://idp.example/s', ARRAY['cert']),
          (${bypassable.orgId}, ${handle()}, 'saml', 'Entra ID', true, false,
           ${entity()}, 'https://idp2.example/s', ARRAY['cert'])`
      // Its own statement: an unfinished connection names none of the SAML
      // columns, and a check constraint refuses one that claims to be enabled
      // without them.
      await h.admin`
        INSERT INTO sso_connections (org_id, handle, kind, display_name, enabled, enforced)
        VALUES (${off.orgId}, ${handle()}, 'oidc', 'Google Workspace', false, false)`

      const { caller } = await callerFor('security')
      const page = await caller.admin.security.sso({ limit: 200 })
      const bySlug = (slug: string) => page.rows.find((r) => r.organization === slug)

      assert.equal(bySlug(enforced.slug)?.enforced, true)
      assert.equal(bySlug(bypassable.slug)?.enabled, true)
      assert.equal(
        bySlug(bypassable.slug)?.enforced,
        false,
        'a connection every member can still sign in around was reported as enforced, which is ' +
          'the one state on this page worth surfacing',
      )
      assert.equal(bySlug(off.slug)?.enabled, false)

      // And the summary agrees with the list, which is the property the page
      // rests on: bypassable is enabled AND NOT enforced, and getting that
      // predicate backwards would report the safe organizations as the risky
      // ones.
      const posture = await caller.admin.security.posture()
      assert.ok(posture.sso.connections >= 3)
      assert.equal(
        posture.sso.enabled,
        page.rows.filter((r) => r.enabled).length,
        'the summary and the list disagree about how many connections are enabled',
      )
      assert.equal(
        posture.sso.bypassable,
        page.rows.filter((r) => r.enabled && !r.enforced).length,
      )
      assert.equal(page.nextCursor, null, 'the fixture set grew past one page, so this is void')
    })

    test('never returns an identity provider certificate, only how many there are', async () => {
      // A page that can print an identity provider's signing material is a page
      // that can leak one, and the count is what answers "is this configured".
      //
      // The material is marked, rather than asserting on the word certificate.
      // The response has a `certificates` FIELD, so a substring test for that
      // word passes on a response that leaks and fails on one that does not,
      // which is a check that answers a nearby question instead of this one.
      const marked = `PEM-MUST-NOT-LEAVE-${randomUUID()}`
      const org = await seedOrg(h.admin, 'sso-cert')
      await h.admin`
        INSERT INTO sso_connections
          (org_id, handle, kind, display_name, enabled, enforced,
           idp_entity_id, idp_sso_url, idp_certificates)
        VALUES (${org.orgId}, ${randomUUID().replace(/-/g, '') + randomUUID().replace(/-/g, '').slice(0, 8)},
                'saml', 'Marked', true, true,
                ${`https://idp-${randomUUID().slice(0, 8)}.example/entity`},
                'https://idp3.example/s', ARRAY[${marked}])`

      const { caller } = await callerFor('owner')
      const page = await caller.admin.security.sso({ limit: 200 })
      assert.ok(
        !JSON.stringify(page).includes(marked),
        'an SSO response carried an identity provider certificate',
      )
      const row = page.rows.find((r) => r.organization === org.slug)
      assert.equal(row?.certificates, 1, 'the count of certificates is missing or wrong')
    })
  })

  describe('what the product can and cannot erase', () => {
    test('the statement is served rather than written into the page', async () => {
      // The page and the exported document have to say the same thing, and two
      // copies of a compliance statement is one copy that is wrong. This is the
      // route both read, and it names nobody, so rendering the caveats does not
      // put a person in the operator log.
      const { caller } = await callerFor('support')
      const answer = await caller.admin.security.erasure()
      assert.match(answer.erasure.perSubject, /Not implemented/)
      assert.match(answer.erasure.perOrganization, /Implemented/)
      assert.ok(answer.retained.some((r) => r.table === 'audit_entries'))
      assert.equal(typeof answer.countCeiling, 'number')

      const before = await auditCount('governance.subject_inspected')
      await caller.admin.security.erasure()
      assert.equal(
        await auditCount('governance.subject_inspected'),
        before,
        'reading the erasure statement recorded a lookup against a person',
      )
    })
  })

  describe('who may read what', () => {
    test('support can read governance and cannot read the credential inventory', async () => {
      // The split the two permissions exist for. Support answers "where is my
      // deletion" every week and never needs to know what can authenticate.
      const { caller } = await callerFor('support')
      await assert.doesNotReject(() => caller.admin.security.deletions({ limit: 5 }))
      await assert.rejects(
        () => caller.admin.security.posture(),
        (err: Error) => /admin\.security\.read/.test(err.message),
        'support could read the credential inventory',
      )
    })

    test('infrastructure can read credentials and cannot read a person', async () => {
      const { caller } = await callerFor('infrastructure')
      await assert.doesNotReject(() => caller.admin.security.posture())
      await assert.rejects(
        () => caller.admin.security.subject({ query: 'nobody@example.test' }),
        (err: Error) => /admin\.governance\.read/.test(err.message),
        'infrastructure could read a subject data map',
      )
    })

    test('reading a subject is not the same as exporting one', async () => {
      // super_admin holds governance.read and NOT governance.export, which is
      // the same split that keeps audit.export off every role but two. A
      // document about a named individual leaving the system is a different
      // act from answering a question about them.
      const subject = await seedUser('split')
      const { caller } = await callerFor('super_admin')
      await assert.doesNotReject(() => caller.admin.security.subject({ userId: subject.id }))
      await assert.rejects(
        () =>
          caller.admin.security.subjectExport({
            userId: subject.id,
            reason: 'a data protection request',
          }),
        (err: Error) => /admin\.governance\.export/.test(err.message),
        'super_admin could export a person data map',
      )
    })
  })

  // -------------------------------------------------------------------------
  // Data Governance
  // -------------------------------------------------------------------------

  describe('the subject data map', () => {
    test('a person with data has it located, and the on-delete behaviour is read from the database', async () => {
      const subject = await seedUser('mapped')
      await h.admin`
        INSERT INTO members (org_id, user_id, role) VALUES (${org.orgId}, ${subject.id}, 'member')`

      const { caller } = await callerFor('security')
      const answer = await caller.admin.security.subject({ query: subject.email })
      assert.ok(answer.subject, 'an exact email did not resolve to one person')
      assert.equal(answer.subject.githubLogin, subject.login)
      assert.deepEqual(
        answer.subject.organizations.map((o) => o.slug),
        [org.slug],
      )

      const members = answer.map?.find((m) => m.table === 'members')
      assert.ok(members, 'members is not in the data map, so the map is not read from the catalog')
      assert.equal(members.rows, 1, 'the map counted the wrong number of membership rows')
      // Not a description of the behaviour, the behaviour: members.user_id
      // carries ON DELETE CASCADE in 0001, and this is that column read back
      // out of pg_constraint.
      assert.equal(members.onDelete, 'cascade')

      // And a table the person has nothing in is still LISTED, with zero. A map
      // that hid the empty rows would be a map that answers "we hold nothing
      // there" and "we never looked" with the same blank.
      const sessions = answer.map?.find((m) => m.table === 'sessions')
      assert.ok(sessions, 'sessions is missing from the map')
      assert.equal(sessions.rows, 0)
    })

    test('the map is discovered from the catalog, not from a list somebody typed', async () => {
      // The property that keeps the answer true a year from now. Every one of
      // these is a real foreign key to users(id) declared in a different
      // migration, and none of them is named in security.ts.
      const subject = await seedUser('catalog')
      const { caller } = await callerFor('security')
      const answer = await caller.admin.security.subject({ userId: subject.id })
      const tables = new Set((answer.map ?? []).map((m) => m.table))
      for (const expected of ['members', 'sessions', 'engine_tokens', 'provider_keys']) {
        assert.ok(tables.has(expected), `${expected} references users(id) and is not in the map`)
      }
    })

    test('looking somebody up is recorded with their name on it', async () => {
      // The automatic per-request auditing records `read.admin.security.subject`
      // and nothing about who was read. Only this entry can answer "who looked
      // at me", which is the question the subject themselves asks.
      const subject = await seedUser('recorded')
      const before = await auditCount('governance.subject_inspected')

      const { email, caller } = await callerFor('security')
      await caller.admin.security.subject({ userId: subject.id })

      assert.equal(
        await auditCount('governance.subject_inspected'),
        before + 1,
        'a subject lookup left no entry naming the subject',
      )
      const [entry] = await h.admin<
        { actor_label: string; target_id: string; severity: string; detail: { subject: string } }[]
      >`
        SELECT actor_label, target_id, severity, detail FROM admin_audit_entries
        WHERE action = 'governance.subject_inspected' ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.target_id, subject.id)
      assert.equal(entry!.actor_label, email)
      assert.equal(entry!.severity, 'notice')
      assert.equal(entry!.detail.subject, subject.login)
    })

    test('a search that matches nobody in particular offers candidates and records nothing', async () => {
      // An entry naming a person the operator did not actually open would put
      // somebody in the log for a typo.
      const tag = randomUUID().slice(0, 8)
      await seedUser(`amb-${tag}`)
      await seedUser(`amb-${tag}`)
      const before = await auditCount('governance.subject_inspected')

      const { caller } = await callerFor('security')
      const answer = await caller.admin.security.subject({ query: `amb-${tag}` })
      assert.equal(answer.subject, null)
      assert.ok(answer.candidates.length >= 2, 'a partial match offered no candidates')
      assert.equal(
        await auditCount('governance.subject_inspected'),
        before,
        'an unresolved search recorded a lookup against a person',
      )
    })

    test('exporting a person produces a document and records that it left', async () => {
      const subject = await seedUser('exported')
      const before = await auditCount('governance.subject_exported')

      const { caller } = await callerFor('security')
      const result = await caller.admin.security.subjectExport({
        userId: subject.id,
        reason: 'a subject access request from counsel',
      })

      // A file, parsed. "It returned an object" is what a route that builds
      // nothing also does.
      const parsed = JSON.parse(result.document) as {
        subject: { githubLogin: string }
        locations: { table: string }[]
        erasure: { perSubject: string; perOrganization: string }
        retainedByDesign: { table: string }[]
      }
      assert.equal(parsed.subject.githubLogin, subject.login)
      assert.ok(parsed.locations.length > 0, 'the exported map located nothing at all')
      assert.match(parsed.erasure.perSubject, /Not implemented/)
      assert.ok(
        parsed.retainedByDesign.some((r) => r.table === 'admin_audit_entries'),
        'the document does not say the operator chain keeps the label',
      )
      assert.equal(result.contentType, 'application/json')

      assert.equal(
        await auditCount('governance.subject_exported'),
        before + 1,
        'a document about a named person left with no record that it did',
      )
      const [entry] = await h.admin<{ severity: string; detail: { reason: string } }[]>`
        SELECT severity, detail FROM admin_audit_entries
        WHERE action = 'governance.subject_exported' ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.severity, 'high')
      assert.equal(entry!.detail.reason, 'a subject access request from counsel')
    })

    test('a login that would be a path is not a file name', async () => {
      // github_login carries no CHECK constraint in this schema, so it is a
      // column another system populates and a file name is not the place to
      // discover that. The browser sanitises a download attribute too; this is
      // the layer that does not depend on which browser.
      const login = `../../etc/pwn-${randomUUID().slice(0, 8)}`
      const [row] = await h.admin<{ id: string }[]>`
        INSERT INTO users (github_id, github_login, email, name)
        VALUES (${Math.floor(Math.random() * 2_000_000_000)}, ${login},
                ${`${randomUUID().slice(0, 8)}@example.test`}, 'Awkward')
        RETURNING id`

      const { caller } = await callerFor('security')
      const result = await caller.admin.security.subjectExport({
        userId: row!.id,
        reason: 'a subject access request',
      })
      assert.ok(!result.filename.includes('/'), `the file name holds a path: ${result.filename}`)
      assert.ok(!result.filename.startsWith('.'), 'the file name would be a hidden file')
      assert.match(result.filename, /^subject-[A-Za-z0-9._-]+\.json$/)
    })

    test('exporting somebody who does not exist records nothing', async () => {
      // The negative half. A refused action that still wrote an entry would
      // put a fabricated uuid in the log as a person somebody exported.
      const before = await auditCount('governance.subject_exported')
      const { caller } = await callerFor('security')
      await assert.rejects(() =>
        caller.admin.security.subjectExport({
          userId: randomUUID(),
          reason: 'this should not be recorded',
        }),
      )
      assert.equal(
        await auditCount('governance.subject_exported'),
        before,
        'an export that refused still recorded one',
      )
    })
  })

  describe('erasure requests', () => {
    test('a deletion in flight reports the step it is waiting on, derived the same way the tenant sees it', async () => {
      const target = await seedOrg(h.admin, 'erase')
      await h.admin`
        INSERT INTO organization_deletions
          (org_id, org_slug, org_name, requested_by_label, reason)
        VALUES (${target.orgId}, ${target.slug}, ${target.slug}, 'owner@example.test', 'closing the account')`

      const { caller } = await callerFor('security')
      const open = await caller.admin.security.deletions({ state: 'open', limit: 200 })
      const row = open.rows.find((r) => r.slug === target.slug)
      assert.ok(row, 'a deletion that has not finished is missing from the open list')
      // stepOf is imported from enterprise/deletion.ts rather than reimplemented,
      // so the operator page and the customer's own page cannot disagree about
      // which step a deletion is on. Nothing has happened yet, so it is the first.
      assert.equal(row.step, 'stop_work')
      assert.equal(row.export, null, 'a deletion with no export row reported one')
    })

    test('a deletion that failed a step is findable as stuck, with the step and the message', async () => {
      // The queue somebody is actually waiting on. A deletion that errored six
      // weeks ago and is sitting in the open list beside forty healthy ones is
      // a deletion nobody will find.
      const target = await seedOrg(h.admin, 'stuck')
      await h.admin`
        INSERT INTO organization_deletions
          (org_id, org_slug, org_name, requested_by_label, reason,
           last_error_at, last_error_step, last_error_message, attempts)
        VALUES (${target.orgId}, ${target.slug}, ${target.slug}, 'owner@example.test',
                'closing', now(), 'cancel_subscription', 'the payment provider refused', 3)`

      const { caller } = await callerFor('security')
      const stuck = await caller.admin.security.deletions({ state: 'stuck', limit: 200 })
      const row = stuck.rows.find((r) => r.slug === target.slug)
      assert.ok(row, 'a deletion with a recorded error is not in the stuck list')
      assert.equal(row.lastError?.step, 'cancel_subscription')
      assert.equal(row.lastError?.message, 'the payment provider refused')
      assert.equal(row.attempts, 3)

      // And it is NOT in the finished list, which is the other half of the
      // filter and the half a single positive test would not catch.
      const finished = await caller.admin.security.deletions({ state: 'finished', limit: 200 })
      assert.ok(
        !finished.rows.some((r) => r.slug === target.slug),
        'a deletion that is still running was listed as finished',
      )
    })

    test('an account whose last organization was purged is listed as orphaned', async () => {
      // The residue nobody wrote down: purge is DELETE FROM organizations, the
      // org-scoped tables cascade, and the users row is global and stays.
      const subject = await seedUser('orphan')
      const { caller } = await callerFor('security')
      const orphans = await caller.admin.security.orphanedAccounts({ limit: 200 })
      assert.ok(
        orphans.rows.some((r) => r.githubLogin === subject.login),
        'an account belonging to no organization is not listed as orphaned',
      )

      // Joining an organization takes it off the list, which is what makes the
      // list mean "belongs to nobody" rather than "was created recently".
      await h.admin`
        INSERT INTO members (org_id, user_id, role) VALUES (${org.orgId}, ${subject.id}, 'member')`
      const after = await caller.admin.security.orphanedAccounts({ limit: 200 })
      assert.ok(
        !after.rows.some((r) => r.githubLogin === subject.login),
        'an account with a membership was still listed as orphaned',
      )
    })
  })

  // -------------------------------------------------------------------------
  // The audit chain's export
  // -------------------------------------------------------------------------

  describe('exporting the operator chain', () => {
    test('the export is a real file, in the format asked for', async () => {
      const { caller } = await callerFor('security')
      const json = await caller.admin.audit.export({ format: 'json', limit: 50 })
      const parsed = JSON.parse(json.document) as {
        chain: string
        entryCount: number
        entries: { seq: number; entryHash: string }[]
      }
      assert.equal(parsed.chain, 'admin_audit_entries')
      assert.equal(parsed.entries.length, json.entryCount)
      assert.ok(json.entryCount > 0, 'the chain was empty, so this test proves nothing')
      // The hashes are in the file. Without them nobody outside this company
      // can check it, and tamper evidence only the vendor can run is not
      // evidence.
      assert.ok(parsed.entries[0]!.entryHash.length > 0)

      const csv = await caller.admin.audit.export({ format: 'csv', limit: 50 })
      const lines = csv.document.split('\n')
      assert.match(lines[0]!, /^seq,occurredAt,actor,action,/)
      assert.equal(lines.length, csv.entryCount + 1, 'the CSV has the wrong number of rows')
      assert.equal(csv.contentType, 'text/csv')
    })

    test('a slice from the middle of the chain verifies, which a naive range walk cannot do', async () => {
      // THE BUG THIS TEST EXISTS FOR. The first entry of a range points at a
      // predecessor OUTSIDE the range. A range walk that starts with no
      // expected predecessor reports its own first row as a broken link every
      // single time, so every export would ship saying the chain was tampered
      // with, and the one that really was would be indistinguishable.
      const { caller } = await callerFor('security')
      const [head] = await h.admin<{ seq: string }[]>`
        SELECT seq FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`
      const to = Number(head!.seq)
      const from = Math.max(2, to - 5)

      const result = await caller.admin.audit.export({ format: 'json', fromSeq: from, toSeq: to })
      assert.ok(from > 1, 'the chain is too short for this test to be about a middle slice')
      assert.equal(
        result.verification.ok,
        true,
        `an intact slice of the chain reported as broken: ${JSON.stringify(result.verification.problems)}`,
      )
      assert.ok(result.verification.entriesWalked > 0)
    })

    test('the verifier says NO when an entry is edited', async () => {
      // An instrument that cannot return no is worse than none. The chain is
      // append-only to the application, so this edits it as the superuser the
      // test harness is, which is the only way to produce the state the
      // verifier exists to detect.
      const { caller } = await callerFor('security')
      const [row] = await h.admin<{ seq: string; action: string }[]>`
        SELECT seq, action FROM admin_audit_entries ORDER BY seq DESC LIMIT 1`
      const seq = Number(row!.seq)

      const clean = await caller.admin.audit.verify({ fromSeq: seq, toSeq: seq })
      assert.equal(clean.ok, true, 'the chain was already broken before this test touched it')

      await h.admin`
        UPDATE admin_audit_entries SET action = 'quietly.rewritten' WHERE seq = ${seq}`
      try {
        const dirty = await caller.admin.audit.verify({ fromSeq: seq, toSeq: seq })
        assert.equal(dirty.ok, false, 'an edited entry verified clean')
        assert.equal(dirty.problems[0]?.kind, 'altered')
        assert.equal(dirty.problems[0]?.seq, seq)
      } finally {
        // Put it back, or every later assertion in this file about the chain
        // is running against a chain this test broke.
        await h.admin`
          UPDATE admin_audit_entries SET action = ${row!.action} WHERE seq = ${seq}`
      }
    })

    test('the export records itself, saying what left', async () => {
      const before = await auditCount('audit.exported')
      const { email, caller } = await callerFor('security')
      const result = await caller.admin.audit.export({ format: 'csv', limit: 5 })

      assert.equal(
        await auditCount('audit.exported'),
        before + 1,
        'a file of every operator action left with no record that it did',
      )
      const [entry] = await h.admin<
        {
          actor_label: string
          severity: string
          detail: { format: string; entries: number; truncated: boolean }
        }[]
      >`
        SELECT actor_label, severity, detail FROM admin_audit_entries
        WHERE action = 'audit.exported' ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.actor_label, email)
      assert.equal(entry!.severity, 'high')
      assert.equal(entry!.detail.format, 'csv')
      assert.equal(entry!.detail.entries, result.entryCount)
      assert.equal(
        entry!.detail.truncated,
        result.truncated,
        'the record disagrees with the file about whether it was complete',
      )
    })

    test('an export that hit its ceiling says so', async () => {
      // A truncated export read as the whole chain is a person telling a
      // regulator that this is everything, when it is the first thousand rows.
      const { caller } = await callerFor('security')
      const result = await caller.admin.audit.export({ format: 'json', limit: 1 })
      assert.equal(result.entryCount, 1)
      assert.equal(result.truncated, true)
      const parsed = JSON.parse(result.document) as { truncated: boolean }
      assert.equal(parsed.truncated, true, 'the file itself does not say it was truncated')
    })

    test('a role that may read the log may not export it', async () => {
      // read_only holds admin.audit.read on purpose: oversight is not a
      // privilege to be rationed. Exporting is the act of answering for it.
      const { caller } = await callerFor('read_only')
      await assert.doesNotReject(() => caller.admin.audit.list({ limit: 5 }))
      await assert.rejects(
        () => caller.admin.audit.export({ format: 'json', limit: 5 }),
        (err: Error) => /admin\.audit\.export/.test(err.message),
        'read_only could export the operator chain',
      )
      await assert.rejects(
        () => caller.admin.audit.verify({}),
        (err: Error) => /admin\.audit\.export/.test(err.message),
      )
    })
  })
})
