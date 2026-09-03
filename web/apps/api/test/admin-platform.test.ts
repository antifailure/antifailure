// The Developer Platform lane, and the one thing a credential list has to do.
//
// A list of keys an operator cannot revoke from is a report, not management.
// So the assertion this suite is built around is not that the route returns
// `revoked: true`: it is that the SAME token which authenticated a moment ago
// stops authenticating afterwards, checked through authenticateEngine, which is
// the function POST /v1/events actually calls. A write that reports success and
// changes nothing is the failure mode admin-routes.test.ts names, and on this
// surface it would be a credential somebody believed they had killed.
//
// The other half is the MCP section, which claims things about a Go package
// this file cannot import. Every one of those claims carries the file and the
// symbol it came from, and the block at the bottom opens each file and looks
// for each symbol. A tool renamed or unregistered in the engine fails here
// rather than leaving a page describing a product that has moved.

import { test, describe, before, after } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { appRouter } from '../src/routers/index.ts'
import { adminSignIn, hashPassword } from '../src/admin/session.ts'
import { type AdminRole } from '../src/admin/permissions.ts'
import {
  MCP_ELSEWHERE,
  MCP_REGISTRATION_FILE,
  MCP_TOOLS,
  MCP_UNKNOWN_FIELD_REFUSAL,
  repositoryPath,
} from '../src/admin/mcp.ts'
import { mintEngineToken } from '../src/tokens.ts'
import { authenticateEngine } from '../src/ingest.ts'
import { createAdminPool, type AdminPool } from '@antifailure/db'
import { available, startApi, seedOrg, signInAs, adminUrl, type ApiHarness, type Org } from './harness.ts'

const hasDb = await available()

/* -------------------------------------------------------------------------
 * The MCP claims, which need no database and no Go toolchain
 * ---------------------------------------------------------------------- */

describe('the MCP section describes the engine that exists', () => {
  test('every tool names a file that declares its constructor', async () => {
    // The file as well as the symbol, for the reason admin/controls.ts gives
    // about enforcedBy: a bare name proves only that SOME file declares one,
    // and a tool moving into a package nothing serves would still satisfy that.
    for (const tool of MCP_TOOLS) {
      const [file, symbol] = tool.servedBy.split(':')
      assert.ok(file && symbol, `${tool.name}.servedBy is not file:symbol`)
      const source = await readFile(repositoryPath(file!), 'utf8')
      assert.match(
        source,
        new RegExp(`func ${symbol}\\b`),
        `${tool.name} says ${tool.servedBy} builds it, and that file declares no such function`,
      )
    }
  })

  test('every tool is REGISTERED, so none of them is a constructor nobody serves', async () => {
    // The second claim, and the one that catches a dead capability. A tool
    // whose constructor exists and which serve.go never registers is a tool no
    // agent can call, and a page listing it would be describing a capability
    // that is not served.
    const serve = await readFile(repositoryPath(MCP_REGISTRATION_FILE), 'utf8')
    for (const tool of MCP_TOOLS) {
      const symbol = tool.servedBy.split(':')[1]!
      assert.match(
        serve,
        new RegExp(`Register\\(${symbol}\\(`),
        `${MCP_REGISTRATION_FILE} does not register ${symbol}, so ${tool.name} is not served`,
      )
    }
  })

  test('the engine serves exactly the tools this page lists, and no others', async () => {
    // Both directions. The two tests above catch a tool that stopped existing;
    // this one catches a tool that STARTED existing, which is the failure that
    // leaves the page quietly incomplete rather than visibly wrong.
    const serve = await readFile(repositoryPath(MCP_REGISTRATION_FILE), 'utf8')
    const registered = [...serve.matchAll(/Register\((new\w+Tool)\(/g)].map((m) => m[1]!)
    const listed = MCP_TOOLS.map((t) => t.servedBy.split(':')[1]!)
    assert.deepEqual(
      [...new Set(registered)].sort(),
      [...listed].sort(),
      'the engine registers a different set of tools than the MCP page lists',
    )
  })

  test('unknown fields really are refused, which is what the whole claim rests on', async () => {
    // "There is no argument that can weaken an experiment" is only true if an
    // argument the server does not know is refused rather than dropped. Without
    // this line the claim degrades to "there is no field we implemented".
    const [file, symbol] = MCP_UNKNOWN_FIELD_REFUSAL.split(':')
    const source = await readFile(repositoryPath(file!), 'utf8')
    assert.match(
      source,
      new RegExp(`\\b${symbol}\\b`),
      `${MCP_UNKNOWN_FIELD_REFUSAL} does not appear, so unknown arguments may be ignored`,
    )
  })

  test('the command the page sends an operator to exists', async () => {
    const [file, symbol] = MCP_ELSEWHERE.commandDeclaredIn.split(':')
    const source = await readFile(repositoryPath(file!), 'utf8')
    assert.match(source, new RegExp(`func ${symbol}\\b`))
    assert.match(source, /Use:\s+"mcp"/, 'the command is not spelled mcp any more')
  })

  test('the documentation page it links to is in the tree', async () => {
    // A link to a page that does not exist is worse on this section than
    // anywhere else in the portal, because the link IS the answer here: the
    // console has nothing else to offer about MCP.
    const slug = MCP_ELSEWHERE.documentation.replace(/^\/|\/$/g, '')
    await assert.doesNotReject(
      () => readFile(repositoryPath(`docs/src/content/docs/${slug}.md`), 'utf8'),
      `${MCP_ELSEWHERE.documentation} does not resolve to a documentation file`,
    )
  })
})

/* -------------------------------------------------------------------------
 * The routes
 * ---------------------------------------------------------------------- */

describe('the developer platform routes', { skip: hasDb ? false : 'no database' }, () => {
  let h: ApiHarness
  let alice: Org
  let bob: Org
  let aliceOwner: { userId: string }
  let adminPool: AdminPool
  const password = 'provisioned-at-deploy-not-in-source'

  async function callerFor(role: AdminRole) {
    const email = `${role}-${randomUUID().slice(0, 8)}@example.test`
    const { hash, salt } = await hashPassword(password)
    await h.admin`
      INSERT INTO admin_users (email, name, role, password_hash, password_salt, password_set_at)
      VALUES (${email}, ${role}, ${role}, ${hash}, ${salt}, now())`
    // Real time rather than h.clock, for the reason admin-routes.test.ts sets
    // out: resolveAdminSession compares against the injected clock and the RLS
    // policy behind current_admin_user() compares against the DATABASE's now(),
    // and a fake past clock makes every operator write fail with an RLS
    // violation that reads like a permissions bug.
    const signedIn = await adminSignIn(h.pool, { email, password }, new Date())
    const { resolveAdminSession } = await import('../src/admin/session.ts')
    const resolved = await resolveAdminSession(h.pool, signedIn.token, new Date())
    assert.ok(resolved, 'the operator session did not resolve')
    return appRouter.createCaller({
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
    } as never)
  }

  /** A real engine token for alice, minted through the route the product uses
   *  rather than by inserting a row: a fixture that writes its own hash proves
   *  nothing about the credential the customer actually holds. */
  async function mintForAlice(name: string) {
    return mintEngineToken(h.pool, h.clock, {
      orgId: alice.orgId,
      name,
      actorUserId: aliceOwner.userId,
      actorLabel: 'alice owner',
      origin: 'web',
    })
  }

  before(async () => {
    h = await startApi()
    alice = await seedOrg(h.admin, 'alice')
    bob = await seedOrg(h.admin, 'bob')
    aliceOwner = await signInAs(h, alice, 'owner')

    await h.admin.unsafe(`ALTER ROLE antifailure_admin LOGIN PASSWORD 'operator-test-password'`)
    const u = new URL(adminUrl)
    u.username = 'antifailure_admin'
    u.password = 'operator-test-password'
    adminPool = createAdminPool({ url: u.toString() })
    await adminPool.ensureBypass()
  })

  after(async () => {
    await adminPool?.close()
    await h?.close()
  })

  test('an operator sees repositories in every organization', async () => {
    const { caller } = { caller: await callerFor('support') }
    const page = await caller.admin.platform.repositories.list({ limit: 200 })
    const names = page.rows.map((r) => r.fullName)
    assert.ok(names.includes(alice.repository), "the operator cannot see alice's repository")
    assert.ok(names.includes(bob.repository), "the operator cannot see bob's repository")
  })

  test('the repository list pages rather than claiming a short list is the whole one', async () => {
    const caller = await callerFor('support')
    const first = await caller.admin.platform.repositories.list({ limit: 1 })
    assert.equal(first.rows.length, 1)
    assert.ok(first.nextCursor, 'two repositories exist and the first page reported no cursor')
    const second = await caller.admin.platform.repositories.list({
      limit: 1,
      cursor: first.nextCursor,
    })
    assert.equal(second.rows.length, 1)
    assert.notEqual(
      second.rows[0]!.id,
      first.rows[0]!.id,
      'the cursor returned the same row again, so paging repeats rather than advances',
    )
  })

  test('a repository detail names its organization and its installations', async () => {
    const caller = await callerFor('support')
    const repo = await caller.admin.platform.repositories.get({ repositoryId: alice.repoId })
    assert.equal(repo.fullName, alice.repository)
    assert.equal(repo.orgSlug, alice.slug)
    // No installation was seeded, and an empty array is the honest answer
    // rather than a missing field. It is also the first thing to check when a
    // customer reports that nothing arrives from GitHub.
    assert.deepEqual(repo.installations, [])
  })

  describe('credentials', () => {
    test('the list never returns anything usable as a credential', async () => {
      const created = await mintForAlice('ci')
      const caller = await callerFor('read_only')
      const page = await caller.admin.platform.keys.list({ limit: 200 })
      const row = page.rows.find((r) => r.id === created.id)
      assert.ok(row, 'the credential just minted is not in the operator list')

      // The same assertion admin-routes.test.ts makes about the operators
      // response, applied to the table where it matters most.
      const serialized = JSON.stringify(page)
      assert.ok(
        !/password|hash|salt|secret|token_hash/i.test(Object.keys(row!).join(' ')),
        `the credential row has a field named like a secret: ${Object.keys(row!).join(', ')}`,
      )
      assert.ok(
        !serialized.includes(created.token),
        'the operator list returned the token value itself',
      )
      assert.equal(row!.prefix, created.prefix)
      assert.equal(row!.standing, 'live')
    })

    test('REVOKING ACTUALLY STOPS THE CREDENTIAL, checked through the ingest path', async () => {
      const created = await mintForAlice('the one that leaks')

      // Prove it works BEFORE, or the assertion afterwards proves nothing: a
      // token that never authenticated would fail the second check for a
      // reason that has nothing to do with the button.
      const before = await authenticateEngine(h.pool, h.clock, created.token)
      assert.ok(before, 'the freshly minted token did not authenticate, so this test proves nothing')
      assert.equal(before!.orgId, alice.orgId)

      const caller = await callerFor('security')
      const result = await caller.admin.platform.keys.revoke({
        tokenId: created.id,
        reason: 'found in a public gist, reported by the customer',
      })
      assert.equal(result.revoked, true)
      assert.equal(result.alreadyRevoked, false)

      // The whole point. authenticateEngine is what POST /v1/events calls, and
      // it reads revoked_at before it reads anything else.
      const after = await authenticateEngine(h.pool, h.clock, created.token)
      assert.equal(after, null, 'the revoked credential still authenticates against the ingest path')
    })

    test('revoking records itself in the operator chain and in the customer\'s own log', async () => {
      const created = await mintForAlice('audited')
      const caller = await callerFor('security')
      const [before] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'credential.revoked'`

      await caller.admin.platform.keys.revoke({
        tokenId: created.id,
        reason: 'rotating after an employee left',
      })

      const [after] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'credential.revoked'`
      assert.equal(
        Number(after!.n),
        Number(before!.n) + 1,
        'a credential was revoked with no entry in the operator chain',
      )

      // The double write. A record only the vendor can read is a vendor's
      // private note; the customer whose pipeline just stopped is the person
      // who most needs to find out why.
      const tenant = await h.admin<{ origin: string; actor_label: string; target_id: string }[]>`
        SELECT origin, actor_label, target_id FROM audit_entries
        WHERE org_id = ${alice.orgId}::uuid AND action = 'credential.revoked'
        ORDER BY seq DESC LIMIT 1`
      assert.equal(tenant.length, 1, "the customer's own audit log has no record of the revocation")
      assert.equal(tenant[0]!.origin, 'admin')
      assert.match(tenant[0]!.actor_label, /@example\.test/)
      // The prefix, so the customer can match the entry against the credential
      // that stopped working. Never the value.
      assert.equal(tenant[0]!.target_id, created.prefix)
    })

    test('revoking twice changes nothing the second time and records nothing either', async () => {
      const created = await mintForAlice('twice')
      const caller = await callerFor('security')
      await caller.admin.platform.keys.revoke({ tokenId: created.id, reason: 'the first attempt' })

      const [before] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'credential.revoked'`
      const [firstRow] = await h.admin<{ revoked_at: Date }[]>`
        SELECT revoked_at FROM engine_tokens WHERE id = ${created.id}::uuid`

      const second = await caller.admin.platform.keys.revoke({
        tokenId: created.id,
        reason: 'the same thing again during the same incident',
      })
      assert.equal(second.alreadyRevoked, true)

      const [after] = await h.admin<{ n: string }[]>`
        SELECT count(*)::text AS n FROM admin_audit_entries WHERE action = 'credential.revoked'`
      assert.equal(
        Number(after!.n),
        Number(before!.n),
        'a second revocation of an already revoked credential wrote an audit entry for doing nothing',
      )
      const [secondRow] = await h.admin<{ revoked_at: Date }[]>`
        SELECT revoked_at FROM engine_tokens WHERE id = ${created.id}::uuid`
      assert.equal(
        new Date(secondRow!.revoked_at).getTime(),
        new Date(firstRow!.revoked_at).getTime(),
        'the second attempt moved the revocation time, rewriting when the credential stopped',
      )
    })

    test('a role that can read credentials cannot necessarily revoke one', async () => {
      // The split this lane's two permissions exist for. support answers "which
      // key is this" every day and must never be the rota that can stop a
      // customer's pipeline while answering.
      const created = await mintForAlice('support cannot touch this')
      const caller = await callerFor('support')
      await assert.doesNotReject(() => caller.admin.platform.keys.list({ limit: 5 }))
      await assert.rejects(
        () => caller.admin.platform.keys.revoke({ tokenId: created.id, reason: 'should not happen' }),
        (err: Error) => /admin\.keys\.revoke/.test(err.message),
        'support revoked a customer credential',
      )
      const still = await authenticateEngine(h.pool, h.clock, created.token)
      assert.ok(still, 'the refused revocation revoked it anyway')
    })

    test('revoking a binding revokes the tokens it minted, not just the binding', async () => {
      // A revocation that stops new tokens being issued while the issued ones
      // keep working is not a revocation. The customer's own command already
      // does both halves; the operator button has to do no less.
      const [binding] = await h.admin<{ id: string }[]>`
        INSERT INTO oidc_repository_bindings (org_id, repository)
        VALUES (${alice.orgId}, ${alice.repository.toLowerCase()})
        RETURNING id`
      const minted = await mintForAlice('exchanged')
      await h.admin`
        UPDATE engine_tokens
        SET kind = 'oidc', binding_id = ${binding!.id}, expires_at = now() + interval '1 hour'
        WHERE id = ${minted.id}`

      const authenticated = await authenticateEngine(h.pool, h.clock, minted.token)
      assert.ok(authenticated, 'the exchanged token did not authenticate before the test began')

      const caller = await callerFor('security')
      const result = await caller.admin.platform.keys.revokeBinding({
        bindingId: binding!.id,
        reason: 'the repository was transferred to another owner',
      })
      assert.equal(result.tokensRevoked, 1, 'the binding was revoked and its token was left alive')

      const after = await authenticateEngine(h.pool, h.clock, minted.token)
      assert.equal(after, null, 'a token minted by the revoked binding still authenticates')
    })
  })

  test('the operator credential can revoke and nothing else, asked of the database', async () => {
    // The migration's intent, checked against the catalog rather than against
    // the comment in the file. 0023 states the rule: reading every tenant is
    // what support work needs and rewriting every tenant's rows is not, so what
    // is granted is exactly the set of actions the portal offers as buttons.
    //
    // The withheld half is the half worth testing. A later blanket
    // GRANT ALL ... TO antifailure_admin would leave every assertion above
    // passing and would quietly give the operator credential the ability to
    // mint a token, which is the one thing this portal must never be able to do.
    const rows = await h.admin<{ table_name: string; privilege_type: string }[]>`
      SELECT table_name, privilege_type FROM information_schema.role_table_grants
      WHERE grantee = 'antifailure_admin' AND table_schema = 'public'
        AND table_name IN ('engine_tokens', 'oidc_repository_bindings', 'scim_tokens',
                           'provider_keys')`
    const holds = (table: string, verb: string) =>
      rows.some((r) => r.table_name === table && r.privilege_type === verb)

    assert.ok(holds('engine_tokens', 'UPDATE'), 'the revoke button cannot write, so it is a report')
    assert.ok(holds('oidc_repository_bindings', 'UPDATE'), 'a binding cannot be revoked')
    assert.ok(holds('engine_tokens', 'SELECT'), 'the list cannot read the table it lists')

    for (const [table, verb] of [
      // Minting is creating a secret, and a route that minted one would have to
      // return it through the operator portal.
      ['engine_tokens', 'INSERT'],
      // A revoked credential is the record of what was allowed to act as this
      // organization and when that stopped. Deleting it destroys the evidence.
      ['engine_tokens', 'DELETE'],
      ['oidc_repository_bindings', 'INSERT'],
      ['oidc_repository_bindings', 'DELETE'],
      // Nothing authenticates a SCIM token today, and a customer's own key to a
      // third party is not ours to rewrite.
      ['scim_tokens', 'UPDATE'],
      ['provider_keys', 'UPDATE'],
    ] as const) {
      assert.ok(
        !holds(table, verb),
        `the operator credential holds ${verb} on ${table}, which no button asks for`,
      )
    }
  })

  test('the delivery ledgers are readable and page', async () => {
    await h.admin`
      INSERT INTO github_deliveries (delivery_id, org_id, account_login, event, action, outcome)
      VALUES (${randomUUID()}, ${alice.orgId}, ${alice.slug}, 'pull_request', 'opened', 'handled'),
             (${randomUUID()}, NULL, 'somebody-else', 'installation', 'created', NULL)`
    const caller = await callerFor('support')
    const page = await caller.admin.platform.integrations.deliveries({
      source: 'github',
      limit: 200,
    })
    assert.ok(page.rows.length >= 2)
    // The row with no organization is KEPT. 0021 makes the column nullable
    // because a delivery about an account this installation has never seen
    // resolves to nobody, and those are exactly the rows worth reading when
    // somebody reports that their events go nowhere.
    assert.ok(
      page.rows.some((r) => r.orgSlug === null),
      'a delivery that resolved to no organization was filtered out of the ledger',
    )
  })

  test('the MCP route says the control plane records nothing, rather than the console assuming it', async () => {
    const caller = await callerFor('read_only')
    const surface = await caller.admin.platform.mcp.surface()
    assert.equal(surface.recordsAnything, false)
    assert.equal(surface.tools.length, MCP_TOOLS.length)
    assert.ok(surface.why.length > 0, 'the page states an absence without saying why')
  })
})
