// The routes that act rather than report.
//
// Everything here was a permission guarding nothing until now, so the suite is
// written against the behaviour a person gets and not against the shape of the
// code: an egress rule that a proposal alone does not put into force, a
// dispatch that reaches the customer's own repository and writes no
// environment row here, a runtime registry that refuses a name it does not
// know, and a plan change that the quota check immediately obeys.
//
// The permission matrix in permissions.test.ts proves the gates. This proves
// what happens after the gate lets the call through, which the matrix
// deliberately does not claim to.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { migrationsDir } from '@antifailure/db'
import { RealGitHubClient, GitHubError } from '../src/auth/github.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

/** The data out of a successful call, or a failure naming what came back. */
function data<T>(res: { status: number; body: unknown }, what: string): T {
  assert.equal(
    errorCode(res.body),
    null,
    `${what} failed: ${res.status} ${JSON.stringify(res.body).slice(0, 300)}`,
  )
  return (res.body as { result: { data: T } }).result.data
}

/** The message on a refusal, so an assertion can be about what it says. */
function message(body: unknown): string {
  return (body as { error?: { message?: string } })?.error?.message ?? ''
}

/**
 * The real client against the answers GitHub actually gives.
 *
 * Needs no database and no Postgres, deliberately: what is under test is the
 * request this builds and the sentence it turns each refusal into, and both
 * are user-facing.
 *
 * The fake below answers per PATH rather than with one status for everything,
 * because the code it stands in for stopped guessing. Every status in it was
 * observed against api.github.com on 2026-08-31, using two real installations
 * of the Antifailure App on the same public repository, one holding
 * `actions: write` and one holding no actions permission:
 *
 *   POST .../dispatches   403 "Resource not accessible by integration"
 *                              for a missing `actions: write`, AND for a
 *                              repository the App does not hold
 *                         404 "Not Found" for a missing workflow file, AND
 *                              for a repository that does not exist
 *                         422 for a missing ref, a missing workflow_dispatch
 *                              trigger, and undeclared inputs
 *   GET /repos/o/r        200 even for a public repository the App was never
 *                              given, which is why it cannot answer "is the
 *                              App installed"
 *   GET /repos/o/r/installation
 *                         200 with the permissions the installation actually
 *                              holds, 404 when there is no installation
 *
 * Measuring first mattered here more than usual. The code under test used to
 * infer the cause from the status, the documentation said a missing permission
 * answered 404, and it answers 403. A fake written from that belief would have
 * agreed with the bug.
 */
describe('dispatching a workflow against GitHub', () => {
  interface Call { url: string; method: string; body: unknown; headers: Record<string, string> }

  /** One repository as GitHub sees it, in the four terms that decide a
   *  dispatch. A test names the state and never a status code. */
  interface World {
    /** Null when no installation of this App covers the repository. */
    installation: { permissions: Record<string, string> } | null
    /** Whether GitHub serves the repository at all. True for any public
     *  repository, installed or not. */
    visible: boolean
    workflowPresent: boolean
    branchPresent: boolean
    /** What the dispatch itself answers, since the states inside the workflow
     *  file are not visible to any lookup. */
    dispatch: { status: number; body: string }
  }

  function world(over: Partial<World> = {}): World {
    return {
      installation: { permissions: { actions: 'write', contents: 'read', metadata: 'read' } },
      visible: true,
      workflowPresent: true,
      branchPresent: true,
      dispatch: { status: 204, body: '' },
      ...over,
    }
  }

  const notFound = '{"message":"Not Found","documentation_url":"https://docs.github.com/rest","status":"404"}'
  const notAccessible =
    '{"message":"Resource not accessible by integration","documentation_url":"https://docs.github.com/rest","status":"403"}'

  /**
   * Runs one body against a GitHub built from a World.
   *
   * The restore is in a finally rather than in an `after` hook, and that is
   * not a style choice: an `after` registered from inside a test runs when the
   * whole describe finishes, so a patched fetch would still be installed for
   * every test after this one and for every suite sharing the process. This
   * scopes it to exactly the call it is standing in for.
   */
  async function withGitHub(
    w: World,
    run: (client: RealGitHubClient, calls: Call[]) => Promise<void>,
  ): Promise<void> {
    const calls: Call[] = []
    const original = globalThis.fetch
    globalThis.fetch = (async (input: string | URL | Request, init?: RequestInit) => {
      const url = String(input)
      calls.push({
        url,
        method: init?.method ?? 'GET',
        body: init?.body ? JSON.parse(String(init.body)) : null,
        headers: (init?.headers ?? {}) as Record<string, string>,
      })
      const path = new URL(url).pathname
      // 204 has to be constructed with a null body. Passing an empty string
      // throws, which is the Response constructor being stricter than the
      // network is and worth knowing before writing a fake of anything.
      if (path.endsWith('/dispatches')) {
        return new Response(
          w.dispatch.status === 204 ? null : w.dispatch.body,
          { status: w.dispatch.status },
        )
      }
      if (/\/branches\/[^/]+$/.test(path)) {
        return new Response(w.branchPresent ? '{}' : notFound, { status: w.branchPresent ? 200 : 404 })
      }
      if (/\/actions\/workflows\/[^/]+$/.test(path)) {
        return new Response(w.workflowPresent ? '{}' : notFound, { status: w.workflowPresent ? 200 : 404 })
      }
      return new Response(w.visible ? '{}' : notFound, { status: w.visible ? 200 : 404 })
    }) as typeof fetch
    try {
      await run(realClient(tokensFor(w)), calls)
    } finally {
      globalThis.fetch = original
    }
  }

  /**
   * The installation-token port, which is where the installation lookup lives.
   *
   * It is not on the fetch path above on purpose: the real one needs the App
   * JWT, which an installation token cannot mint, and that separation is the
   * reason it is a port at all. `InstallationTokens.onRepository` is proved
   * against its own HTTP in githubapp.test.ts.
   */
  function tokensFor(w: World) {
    return {
      for: async () => 'ghs_installation_token',
      onRepository: async () =>
        w.installation === null ? null : { id: 4242, permissions: w.installation.permissions },
    }
  }

  function realClient(installationTokens?: {
    for(id: number): Promise<string>
    onRepository(repository: string): Promise<{ id: number; permissions: Record<string, string> } | null>
  }): RealGitHubClient {
    return new RealGitHubClient({
      clientId: 'id',
      clientSecret: 'secret',
      redirectUri: 'https://app.test/callback',
      apiBase: 'https://api.github.test',
      ...(installationTokens ? { installationTokens } : {}),
    })
  }

  /** The message from one refused dispatch. */
  async function refusalFor(w: World, ref = 'main'): Promise<string> {
    let said = ''
    await withGitHub(w, async (client) => {
      await assert.rejects(
        () => client.dispatchWorkflow(99, 'acme/storefront', 'antifailure.yml', ref, {}),
        (err: unknown) => {
          assert.ok(err instanceof GitHubError, `expected a GitHubError, got ${String(err)}`)
          said = err.message
          return true
        },
      )
    })
    return said
  }

  it('posts the ref and the inputs to the workflow dispatch endpoint', async () => {
    await withGitHub(world(), async (client, calls) => {
      await client.dispatchWorkflow(99, 'acme/storefront', 'antifailure.yml', 'main', {
        command: 'agents',
        workflows: 'sign-up',
      })
      const call = calls[0]!
      assert.equal(call.method, 'POST')
      assert.equal(
        call.url,
        'https://api.github.test/repos/acme/storefront/actions/workflows/antifailure.yml/dispatches',
      )
      // The slash between owner and name has to survive encoding, or every
      // dispatch goes to a repository whose name contains a %2F.
      assert.equal(call.url.includes('%2F'), false)
      assert.deepEqual(call.body, {
        ref: 'main',
        inputs: { command: 'agents', workflows: 'sign-up' },
      })
      assert.equal(call.headers.authorization, 'Bearer ghs_installation_token')
      // Nothing is looked up on the happy path. The diagnosis costs two more
      // requests and exists for the refusal, not for every dispatch.
      assert.equal(calls.length, 1)
    })
  })

  it('an ungranted Actions write says so, and says who can grant it', async () => {
    const said = await refusalFor(
      world({
        installation: { permissions: { contents: 'read', metadata: 'read' } },
        dispatch: { status: 403, body: notAccessible },
      }),
    )
    assert.match(said, /Actions write/)
    // The remedy is a person in the customer's GitHub organization, not
    // anything in this console, and the message has to say which.
    assert.match(said, /owner of the acme organization/)
    assert.match(said, /Nothing in this console can grant it/)
    // The three states this refusal used to be conflated with must not appear.
    assert.doesNotMatch(said, /workflows\/antifailure\.yml/)
    assert.doesNotMatch(said, /not installed/)
  })

  it('the same 403 for an App that was never given the repository says to install it', async () => {
    const said = await refusalFor(
      world({ installation: null, dispatch: { status: 403, body: notAccessible } }),
    )
    assert.match(said, /not installed on acme\/storefront/)
    assert.match(said, /Configure/)
    assert.doesNotMatch(said, /Actions write/)
  })

  it('a repository GitHub will not show at all says to check the name', async () => {
    const said = await refusalFor(
      world({ installation: null, visible: false, dispatch: { status: 404, body: notFound } }),
    )
    assert.match(said, /will not show this App a repository named acme\/storefront/)
    assert.match(said, /private/)
    assert.doesNotMatch(said, /Actions write/)
  })

  it('a missing workflow file names the path it has to be committed to', async () => {
    const said = await refusalFor(
      world({ workflowPresent: false, dispatch: { status: 404, body: notFound } }),
    )
    assert.match(said, /\.github\/workflows\/antifailure\.yml/)
    assert.match(said, /default branch/)
    assert.match(said, /examples\/github-workflow\.yml/)
    assert.doesNotMatch(said, /Actions write/)
    assert.doesNotMatch(said, /not installed/)
  })

  it('a branch nobody pushed is named, from the 422 and from the lookup alike', async () => {
    const fromDispatch = await refusalFor(
      world({
        dispatch: { status: 422, body: '{"message":"No ref found for: wip"}' },
      }),
      'wip',
    )
    assert.match(fromDispatch, /no branch named wip/)
    assert.match(fromDispatch, /leave the branch empty/)
  })

  it('422 names the default branch rule, which is the part nobody guesses', async () => {
    const said = await refusalFor(
      world({
        dispatch: {
          status: 422,
          body: '{"message":"Workflow does not have \'workflow_dispatch\' trigger"}',
        },
      }),
      'wip',
    )
    assert.match(said, /default branch/)
    assert.match(said, /wip/)
    assert.match(said, /workflow_dispatch/)
  })

  it('inputs the workflow does not declare name the four this console sends', async () => {
    const said = await refusalFor(
      world({
        dispatch: {
          status: 422,
          body: '{"message":"Unexpected inputs provided: [\\"command\\"]"}',
        },
      }),
    )
    assert.match(said, /command, workflows, duration and scale/)
    assert.match(said, /examples\/github-workflow\.yml/)
  })

  it('never puts GitHub JSON on a product screen', async () => {
    // Every refusal, including the one nothing can explain. The body that
    // reaches this used to be pasted into the console verbatim.
    const messages = [
      await refusalFor(world({ dispatch: { status: 403, body: notAccessible } })),
      await refusalFor(world({ dispatch: { status: 500, body: '<html>bad gateway</html>' } })),
      await refusalFor(world({ dispatch: { status: 422, body: notFound } })),
    ]
    for (const said of messages) {
      assert.doesNotMatch(said, /documentation_url/)
      assert.doesNotMatch(said, /[{}]/)
      assert.doesNotMatch(said, /<html>/)
      // A sentence a person can act on, not a status code on its own.
      assert.ok(said.length > 60, `too terse to act on: ${said}`)
    }
  })

  it('a refusal nothing explains still carries what GitHub said', async () => {
    const said = await refusalFor(
      world({ dispatch: { status: 403, body: '{"message":"API rate limit exceeded"}' } }),
    )
    assert.match(said, /API rate limit exceeded/)
    assert.match(said, /not a setting in this console/)
  })

  it('a diagnosis that fails leaves the refusal it was explaining', async () => {
    // The lookup throwing must not replace the answer with an exception about
    // a request the person never made.
    const original = globalThis.fetch
    globalThis.fetch = (async () => new Response(null, { status: 403 })) as typeof fetch
    try {
      const client = realClient({
        for: async () => 'ghs_installation_token',
        onRepository: async () => {
          throw new Error('the App JWT was refused')
        },
      })
      await assert.rejects(
        () => client.dispatchWorkflow(99, 'acme/storefront', 'antifailure.yml', 'main', {}),
        (err: unknown) => {
          assert.ok(err instanceof GitHubError)
          assert.doesNotMatch(err.message, /App JWT/)
          assert.match(err.message, /GitHub would not start antifailure\.yml/)
          return true
        },
      )
    } finally {
      globalThis.fetch = original
    }
  })

  it('a lookup that is refused rather than absent does not invent a missing file', async () => {
    // A rate limit is a 403 and a bad gateway is a 502. Reading either as "your
    // workflow file is not committed" sends somebody to commit a file that is
    // already committed, which is a false finding on the one screen that has to
    // be trusted.
    const original = globalThis.fetch
    globalThis.fetch = (async (input: string | URL | Request) =>
      new Response('{"message":"API rate limit exceeded"}', {
        status: String(input).includes('/dispatches') ? 403 : 429,
      })) as typeof fetch
    try {
      const client = realClient(tokensFor(world()))
      const blocker = await client.dispatchBlocker(99, 'acme/storefront', 'antifailure.yml', 'main')
      assert.equal(blocker, null)
    } finally {
      globalThis.fetch = original
    }
  })

  it('dispatchBlocker finds nothing when nothing is wrong, and asks about the branch only when given one', async () => {
    await withGitHub(world(), async (client, calls) => {
      assert.equal(await client.dispatchBlocker(99, 'acme/storefront', 'antifailure.yml'), null)
      // The console asks this while the branch box is still being typed in, so
      // an absent ref must not become a lookup.
      assert.equal(calls.filter((c) => c.url.includes('/branches/')).length, 0)
      assert.equal(await client.dispatchBlocker(99, 'acme/storefront', 'antifailure.yml', 'main'), null)
      assert.equal(calls.filter((c) => c.url.includes('/branches/main')).length, 1)
    })
  })

  it('dispatchBlocker reports the cause, so a reworded message is not a broken test', async () => {
    const cases: [Partial<World>, string][] = [
      [{ installation: null }, 'app-not-installed'],
      [{ installation: null, visible: false }, 'repository-not-visible'],
      [{ installation: { permissions: { metadata: 'read' } } }, 'permission-missing'],
      [{ installation: { permissions: { actions: 'read' } } }, 'permission-missing'],
      [{ workflowPresent: false }, 'workflow-missing'],
      [{ branchPresent: false }, 'branch-missing'],
    ]
    for (const [over, cause] of cases) {
      await withGitHub(world(over), async (client) => {
        const blocker = await client.dispatchBlocker(99, 'acme/storefront', 'antifailure.yml', 'wip')
        assert.equal(blocker?.cause, cause, `expected ${cause} for ${JSON.stringify(over)}`)
      })
    }
  })

  it('no App configured is a message naming the variables, not a crash', async () => {
    // No installationTokens, which is what a control plane running without a
    // GitHub App looks like. It must not reach the network at all.
    await assert.rejects(
      () => realClient().dispatchWorkflow(99, 'acme/storefront', 'antifailure.yml', 'main', {}),
      (err: unknown) => {
        assert.ok(err instanceof GitHubError)
        assert.match(err.message, /AF_GITHUB_APP_ID/)
        return true
      },
    )
  })
})

describe('the control plane acts', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  let admin: SignedIn
  let member: SignedIn

  /** An installation, so a dispatch has somewhere to go. seedOrg does not make
   *  one, because most of this product works without an App. */
  async function install(target: Org): Promise<number> {
    const id = Math.floor(Math.random() * 1e9)
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${target.orgId}, ${id}, ${target.slug}, 'Organization')`
    return id
  }

  async function countEnvironments(orgId: string, liveOnly: boolean): Promise<number> {
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM environments
      WHERE org_id = ${orgId} AND (${!liveOnly} OR state <> 'torn_down')`
    return Number(rows[0]!.n)
  }

  /**
   * Runs a migration file the way migrate() runs it.
   *
   * On a reserved connection and as one simple query, because the file carries
   * its own BEGIN and COMMIT and the pool refuses transaction control on a
   * connection it may hand to somebody else mid-transaction. Anything less
   * than this would be running a rewritten version of the file, which proves
   * nothing about the one that ships.
   */
  async function applyMigration(body: string): Promise<void> {
    const reserved = await h.admin.reserve()
    try {
      await reserved.unsafe(body).simple()
    } finally {
      reserved.release()
    }
  }

  /** Re-applies 0017 if the columns are not there, whatever went wrong. */
  async function restoreApprovalColumns(body: string): Promise<void> {
    const rows = await h.admin<{ column_name: string }[]>`
      SELECT column_name FROM information_schema.columns
      WHERE table_schema = 'public' AND table_name = 'network_rules'
        AND column_name = 'approved_at'`
    if (rows.length === 0) await applyMigration(body)
  }

  async function countPlanChanges(): Promise<number> {
    const rows = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'organization.plan_changed'`
    return Number(rows[0]!.n)
  }

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'verbs')
    owner = await signInAs(h, org, 'owner')
    admin = await signInAs(h, org, 'admin')
    member = await signInAs(h, org, 'member')
    await install(org)
    h.github.addWorkflow(org.repository, 'antifailure.yml')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  // -------------------------------------------------------------------------
  // Egress approval
  // -------------------------------------------------------------------------

  describe('an egress rule is not policy until somebody approves it', () => {
    it('a proposal is invisible to network.effective and visible in the queue', async () => {
      const host = `proposal-${Date.now()}.example.test`
      const proposed = data<{ ruleId: string; needsApproval: boolean }>(
        await callProcedure(h, member, 'network.propose', 'mutation', {
          repository: org.repository,
          host,
          mode: 'allow',
        }),
        'network.propose',
      )
      assert.equal(proposed.needsApproval, true)

      // The whole defect, in one assertion. Before the approval columns
      // existed this host was in the effective policy the instant a member
      // wrote it, and a member is the least privileged role that can.
      const effective = data<{ rules: { host: string }[] }>(
        await callProcedure(h, member, 'network.effective', 'query', { repository: org.repository }),
        'network.effective',
      )
      assert.equal(
        effective.rules.some((r) => r.host === host),
        false,
        'a proposal nobody approved is being enforced',
      )

      const pending = data<{ id: string; host: string; proposed_by: string | null }[]>(
        await callProcedure(h, member, 'network.pending', 'query', { repository: org.repository }),
        'network.pending',
      )
      const queued = pending.find((r) => r.host === host)
      assert.ok(queued, 'the proposal is not in the approval queue either, so it is a dead end')
      assert.equal(queued.id, proposed.ruleId)
      assert.ok(queued.proposed_by, 'the queue does not say who proposed it')

      // And approving it puts it into force.
      data(
        await callProcedure(h, admin, 'network.approve', 'mutation', { ruleId: proposed.ruleId }),
        'network.approve',
      )
      const afterApproval = data<{ rules: { host: string }[] }>(
        await callProcedure(h, member, 'network.effective', 'query', { repository: org.repository }),
        'network.effective',
      )
      assert.ok(
        afterApproval.rules.some((r) => r.host === host),
        'an approved rule is still not being enforced, so approval does nothing',
      )

      const stillPending = data<{ host: string }[]>(
        await callProcedure(h, member, 'network.pending', 'query', { repository: org.repository }),
        'network.pending',
      )
      assert.equal(
        stillPending.some((r) => r.host === host),
        false,
        'an approved rule is still in the queue',
      )
    })

    it('a member cannot approve and an admin can', async () => {
      const host = `who-approves-${Date.now()}.example.test`
      const proposed = data<{ ruleId: string }>(
        await callProcedure(h, member, 'network.propose', 'mutation', {
          repository: org.repository,
          host,
          mode: 'allow',
        }),
        'network.propose',
      )

      const refused = await callProcedure(h, member, 'network.approve', 'mutation', {
        ruleId: proposed.ruleId,
      })
      assert.equal(
        errorCode(refused.body),
        'FORBIDDEN',
        'the role that proposed the change was allowed to approve it as well',
      )

      // The rule is still pending afterwards, which is the part that matters:
      // a refusal that had already written the row would be a gate in name.
      const [row] = await h.admin<{ approved_at: Date | null }[]>`
        SELECT approved_at FROM network_rules WHERE id = ${proposed.ruleId}`
      assert.equal(row!.approved_at, null)

      data(
        await callProcedure(h, admin, 'network.approve', 'mutation', { ruleId: proposed.ruleId }),
        'admin approving',
      )
    })

    it('approving twice does not move who is accountable', async () => {
      const host = `twice-${Date.now()}.example.test`
      const proposed = data<{ ruleId: string }>(
        await callProcedure(h, member, 'network.propose', 'mutation', {
          repository: org.repository,
          host,
          mode: 'allow',
        }),
        'network.propose',
      )

      data(
        await callProcedure(h, admin, 'network.approve', 'mutation', { ruleId: proposed.ruleId }),
        'the first approval',
      )
      const [first] = await h.admin<{ approved_by: string; approved_at: Date }[]>`
        SELECT approved_by, approved_at FROM network_rules WHERE id = ${proposed.ruleId}`
      assert.equal(first!.approved_by, admin.userId)

      // A second approval by somebody else is NOT_FOUND rather than a silent
      // overwrite. Overwriting would mean the audit log and the row disagree
      // about who widened egress, and the row is the one an incident reads.
      const again = await callProcedure(h, owner, 'network.approve', 'mutation', {
        ruleId: proposed.ruleId,
      })
      assert.equal(errorCode(again.body), 'NOT_FOUND')

      const [second] = await h.admin<{ approved_by: string; approved_at: Date }[]>`
        SELECT approved_by, approved_at FROM network_rules WHERE id = ${proposed.ruleId}`
      assert.equal(second!.approved_by, admin.userId, 'the second approval moved the approver')
      assert.equal(
        second!.approved_at.getTime(),
        first!.approved_at.getTime(),
        'the second approval moved the time the decision was made',
      )
    })

    /**
     * The migration, against a row that existed before it.
     *
     * `UPDATE network_rules SET approved_at = created_at` is the line that
     * decides whether every customer's live egress policy survives this
     * change, and reading it is not proof of anything. So the columns come
     * off, a rule is written the way it was written last week, the migration
     * file runs verbatim, and the assertion is that the rule is still in the
     * effective policy afterwards.
     *
     * Not wrapped in a transaction on purpose. The file carries its own BEGIN
     * and COMMIT and is run exactly as migrate() runs it, which is the only
     * version of this test that proves anything about the file that ships.
     */
    it('rules written before the migration are still enforced after it', async () => {
      const before = await seedOrg(h.admin, 'premigration')
      const beforeAdmin = await signInAs(h, before, 'admin')
      const host = 'written-before-the-gate.example.test'
      const body = await readFile(
        path.join(migrationsDir, '0018_an_egress_rule_nobody_approved.sql'),
        'utf8',
      )

      try {
        await h.admin.unsafe(`
          ALTER TABLE network_rules
            DROP COLUMN proposed_by, DROP COLUMN approved_by, DROP COLUMN approved_at`)
        // network_rules_pending_idx goes with approved_at, because a partial
        // index cannot outlive the column its predicate reads. Dropping it by
        // name here failed for exactly that reason, which is the shape of the
        // schema confirming itself.

        await h.admin`
          INSERT INTO network_rules (org_id, repository_id, host, mode)
          VALUES (${before.orgId}, ${before.repoId}, ${host}, 'allow')`

        await applyMigration(body)

        const [row] = await h.admin<{ approved_at: Date; approved_by: string | null; created_at: Date }[]>`
          SELECT approved_at, approved_by, created_at FROM network_rules
          WHERE org_id = ${before.orgId} AND host = ${host}`
        assert.ok(row!.approved_at, 'the migration left an existing rule pending, so it stopped enforcing')
        assert.equal(
          row!.approved_at.getTime(),
          row!.created_at.getTime(),
          'an existing rule was backdated to something other than when it was written',
        )
        assert.equal(
          row!.approved_by,
          null,
          'the migration invented an approver for a rule nobody approved',
        )

        const effective = data<{ rules: { host: string }[] }>(
          await callProcedure(h, beforeAdmin, 'network.effective', 'query', {
            repository: before.repository,
          }),
          'network.effective',
        )
        assert.ok(
          effective.rules.some((r) => r.host === host),
          'a rule that was enforcing before the migration is not enforcing after it',
        )
      } finally {
        // The schema is shared by every suite in this run, so it is put back
        // whatever happened above. A failed assertion here must not leave the
        // next file looking at a table with no approval columns: migrate()
        // has already recorded 0017 as applied and would not repair it.
        await restoreApprovalColumns(body)
        await dropOrg(h.admin, before.orgId)
      }
    })
  })

  // -------------------------------------------------------------------------
  // The dispatch verbs
  // -------------------------------------------------------------------------

  describe('the verbs dispatch into the CI the customer already runs', () => {
    it('creating an environment dispatches on the default branch and writes no row here', async () => {
      const seen = h.github.dispatches.length
      const envsBefore = await countEnvironments(org.orgId, false)

      const answer = data<{ dispatched: boolean; ref: string; workflow: string }>(
        await callProcedure(h, member, 'environments.create', 'mutation', {
          repository: org.repository,
        }),
        'environments.create',
      )
      assert.equal(answer.dispatched, true)
      assert.equal(answer.ref, 'main', 'the default branch was not used when none was named')
      assert.equal(answer.workflow, 'antifailure.yml')

      const dispatch = h.github.dispatches[seen]
      assert.ok(dispatch, 'nothing was dispatched')
      assert.equal(dispatch.repository, org.repository)
      assert.equal(dispatch.ref, 'main')
      assert.equal(dispatch.inputs.command, 'up')
      // Only the inputs the workflow declares, and every one of them is a flag
      // `af` actually has. Sending anything else would be an input the customer's
      // workflow silently drops.
      assert.deepEqual(Object.keys(dispatch.inputs).sort(), ['command', 'duration', 'scale', 'workflows'])

      // The boundary, asserted rather than described. The control plane asked
      // GitHub to run the workflow and recorded nothing about an environment,
      // because the engine is what knows whether one came up.
      assert.equal(
        await countEnvironments(org.orgId, false),
        envsBefore,
        'the control plane invented an environment row',
      )

      const [entry] = await h.admin<{ action: string; target_id: string }[]>`
        SELECT action, target_id FROM audit_entries
        WHERE org_id = ${org.orgId} AND action = 'environment.requested'
        ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.target_id, org.repository, 'the request was not audited')
    })

    it('a named branch is the ref that is dispatched', async () => {
      const seen = h.github.dispatches.length
      data(
        await callProcedure(h, member, 'environments.create', 'mutation', {
          repository: org.repository,
          branch: 'a-feature-branch',
        }),
        'environments.create',
      )
      assert.equal(h.github.dispatches[seen]!.ref, 'a-feature-branch')
    })

    it('an organization with no installation is told what to install', async () => {
      const lonely = await seedOrg(h.admin, 'noapp')
      const session = await signInAs(h, lonely, 'admin')
      try {
        const res = await callProcedure(h, session, 'environments.create', 'mutation', {
          repository: lonely.repository,
        })
        assert.equal(errorCode(res.body), 'PRECONDITION_FAILED')
        assert.match(message(res.body), /GitHub App/, 'the refusal does not say what to do about it')
      } finally {
        await dropOrg(h.admin, lonely.orgId)
      }
    })

    it('GitHub refusing is an answer, not an internal error', async () => {
      // No workflow file registered for this repository. Distinct from a
      // missing installation and from an ungranted permission, which are the
      // two states this message used to be given for as well.
      const res = await callProcedure(h, member, 'environments.create', 'mutation', {
        repository: org.repository,
        workflow: 'not-there.yml',
      })
      assert.equal(
        errorCode(res.body),
        'PRECONDITION_FAILED',
        'GitHub saying no arrived as a fault in this control plane',
      )
      assert.match(message(res.body), /\.github\/workflows\/not-there\.yml/)
      assert.doesNotMatch(message(res.body), /Actions write/)
    })

    /**
     * The five states behind a refused dispatch, through the route a person
     * actually presses.
     *
     * One test per remedy, because the remedies are what differ: accepting a
     * permission on GitHub, adding a repository to an installation, checking a
     * name, committing a file, and pushing a branch are five afternoons, and
     * the message this replaced named three of them at once and left the
     * reader to pick.
     */
    it('every way a dispatch is refused reaches the person as its own remedy', async () => {
      // Each arrangement takes the repository it is about. Closing over the
      // suite's own `org` instead would arrange a state on a repository the
      // call never touches, and every case would quietly pass by dispatching
      // successfully, which is exactly what it did once.
      const cases: [string, (repo: string) => void, RegExp, RegExp][] = [
        [
          'the permission was never accepted',
          () => h.github.revokeActionsWrite(),
          /Actions write/,
          /workflows\//,
        ],
        [
          'the App does not hold this repository',
          (repo) => h.github.uninstallFrom(repo),
          /not installed on /,
          /Actions write/,
        ],
        [
          'GitHub will not show the repository',
          (repo) => h.github.hideRepository(repo),
          /will not show this App/,
          /Actions write/,
        ],
        [
          'the branch was never pushed',
          (repo) => h.github.removeBranch(repo, 'main'),
          /no branch named main/,
          /Actions write/,
        ],
        [
          'the workflow has no dispatch trigger',
          () => h.github.refuseDispatches('trigger-missing'),
          /workflow_dispatch/,
          /Actions write/,
        ],
      ]
      for (const [i, [what, arrange, names, doesNotName]] of cases.entries()) {
        const fresh = await seedOrg(h.admin, `refused${i}`)
        const session = await signInAs(h, fresh, 'admin')
        try {
          await install(fresh)
          h.github.addWorkflow(fresh.repository, 'antifailure.yml')
          arrange(fresh.repository)
          const res = await callProcedure(h, session, 'environments.create', 'mutation', {
            repository: fresh.repository,
          })
          assert.equal(errorCode(res.body), 'PRECONDITION_FAILED', what)
          assert.match(message(res.body), names, what)
          assert.doesNotMatch(message(res.body), doesNotName, what)
          // The whole reason this work exists: no JSON reaches a screen.
          assert.doesNotMatch(message(res.body), /documentation_url|[{}]/, what)
        } finally {
          h.github.reset()
          await dropOrg(h.admin, fresh.orgId)
        }
      }
    })

    /**
     * The same states, before anybody presses anything.
     *
     * A form that only fails on submit is the defect this closes: none of what
     * it reports is caused by anything typed into the form, and every one of
     * them was already true when the page loaded.
     */
    it('readiness answers before the button is pressed, and never throws on a supported state', async () => {
      const ready = data<{ status: string }>(
        await callProcedure(h, member, 'environments.readiness', 'query', {
          repository: org.repository,
        }),
        'environments.readiness',
      )
      assert.deepEqual(ready, { status: 'ready' })

      h.github.revokeActionsWrite()
      try {
        const blocked = data<{ status: string; cause: string; message: string }>(
          await callProcedure(h, member, 'environments.readiness', 'query', {
            repository: org.repository,
          }),
          'environments.readiness',
        )
        assert.equal(blocked.status, 'blocked')
        assert.equal(blocked.cause, 'permission-missing')
        assert.match(blocked.message, /Actions write/)
      } finally {
        h.github.reset()
      }

      // An organization that has not installed the App is the most ordinary
      // state this product has, and a query that threw on it would make the
      // page render its error state instead of its answer.
      const lonely = await seedOrg(h.admin, 'noapp-readiness')
      const session = await signInAs(h, lonely, 'admin')
      try {
        const answer = data<{ status: string; cause: string }>(
          await callProcedure(h, session, 'environments.readiness', 'query', {
            repository: lonely.repository,
          }),
          'environments.readiness',
        )
        assert.equal(answer.status, 'blocked')
        assert.equal(answer.cause, 'app-not-installed')
      } finally {
        await dropOrg(h.admin, lonely.orgId)
      }
    })

    it('a suspended organization cannot start new work', async () => {
      data(
        await callProcedure(h, owner, 'org.suspend', 'mutation', { reason: 'a live incident' }),
        'org.suspend',
      )
      try {
        const res = await callProcedure(h, member, 'environments.create', 'mutation', {
          repository: org.repository,
        })
        assert.equal(errorCode(res.body), 'PRECONDITION_FAILED')
        assert.match(message(res.body), /a live incident/)
        assert.match(
          message(res.body),
          /already running are untouched/,
          'the refusal does not say that nothing was torn down',
        )
      } finally {
        data(await callProcedure(h, owner, 'org.resume', 'mutation', {}), 'org.resume')
      }
    })

    it('agents and load run against an environment, on its own branch', async () => {
      const seen = h.github.dispatches.length
      data(
        await callProcedure(h, member, 'agents.run', 'mutation', {
          envId: org.envId,
          workflows: ['sign-up', 'checkout'],
        }),
        'agents.run',
      )
      const agents = h.github.dispatches[seen]!
      assert.equal(agents.inputs.command, 'agents')
      // The environment is named by its branch and nothing else, because that
      // is how the engine identifies one: `af test` acts on the checked out
      // branch and has no environment flag to be given an id.
      assert.equal(agents.ref, 'main', 'the run was not dispatched on the branch the environment is on')
      assert.equal(agents.inputs.workflows, 'sign-up,checkout')

      data(
        await callProcedure(h, member, 'load.run', 'mutation', {
          envId: org.envId,
          seconds: 90,
          scale: 2,
        }),
        'load.run',
      )
      const load = h.github.dispatches[seen + 1]!
      assert.equal(load.inputs.command, 'load')
      // A Go duration, because that is what `af load run --duration` parses.
      assert.equal(load.inputs.duration, '90s')
      assert.equal(load.inputs.scale, '2')
      assert.equal(load.inputs.workflows, '')
    })

    it('a torn down environment is refused before GitHub is asked', async () => {
      const gone = await seedOrg(h.admin, 'torndown')
      const session = await signInAs(h, gone, 'admin')
      await install(gone)
      await h.admin`
        UPDATE environments SET state = 'torn_down' WHERE org_id = ${gone.orgId}`
      const seen = h.github.dispatches.length
      try {
        const res = await callProcedure(h, session, 'agents.run', 'mutation', { envId: gone.envId })
        assert.equal(errorCode(res.body), 'PRECONDITION_FAILED')
        assert.equal(
          h.github.dispatches.length,
          seen,
          'a run was dispatched against an environment that no longer exists',
        )
      } finally {
        await dropOrg(h.admin, gone.orgId)
      }
    })
  })

  // -------------------------------------------------------------------------
  // Runtimes
  // -------------------------------------------------------------------------

  describe('the runtime registry answers against what is actually running', () => {
    it('registers, tags, and refuses a second runtime of the same name', async () => {
      data(
        await callProcedure(h, admin, 'runtimes.register', 'mutation', {
          name: 'eu-cluster',
          provider: 'kubernetes',
          labels: ['eu'],
          note: 'the Frankfurt cluster',
        }),
        'runtimes.register',
      )

      const again = await callProcedure(h, admin, 'runtimes.register', 'mutation', {
        name: 'eu-cluster',
        provider: 'local',
        labels: [],
      })
      assert.equal(
        errorCode(again.body),
        'BAD_REQUEST',
        'registering the same name twice silently moved a live runtime to another provider',
      )

      data(
        await callProcedure(h, admin, 'runtimes.tag', 'mutation', {
          name: 'eu-cluster',
          labels: ['eu', 'gpu'],
        }),
        'runtimes.tag',
      )
      const listed = data<{ name: string; labels: string[]; provider: string }[]>(
        await callProcedure(h, member, 'runtimes.list', 'query', { includeRemoved: false }),
        'runtimes.list',
      )
      const found = listed.find((r) => r.name === 'eu-cluster')
      assert.ok(found)
      assert.deepEqual(found.labels, ['eu', 'gpu'], 'tagging did not replace the labels')
      assert.equal(found.provider, 'kubernetes')
    })

    it('reports what is running somewhere nobody registered', async () => {
      // An environment comes up on a runtime the organization never agreed to.
      // Nothing refuses that and nothing should: the engine reports where it
      // ran, and the control plane's job is to make it visible.
      await h.admin`
        UPDATE environments SET runtime = 'somebodys-laptop'
        WHERE org_id = ${org.orgId} AND env_id = ${org.envId}`

      const listed = data<{ name: string; registered: boolean; environments: string }[]>(
        await callProcedure(h, member, 'runtimes.list', 'query', { includeRemoved: false }),
        'runtimes.list',
      )
      const stranger = listed.find((r) => r.name === 'somebodys-laptop')
      assert.ok(stranger, 'an environment is running somewhere the registry never mentions')
      assert.equal(stranger.registered, false)
      assert.equal(Number(stranger.environments), 1)

      // Registering it moves it across rather than duplicating it.
      data(
        await callProcedure(h, admin, 'runtimes.register', 'mutation', {
          name: 'somebodys-laptop',
          provider: 'local',
          labels: [],
        }),
        'runtimes.register',
      )
      const after = data<{ name: string; registered: boolean; environments: string }[]>(
        await callProcedure(h, member, 'runtimes.list', 'query', { includeRemoved: false }),
        'runtimes.list',
      )
      const rows = after.filter((r) => r.name === 'somebodys-laptop')
      assert.equal(rows.length, 1, 'registering a runtime listed it twice')
      assert.equal(rows[0]!.registered, true)
      assert.equal(Number(rows[0]!.environments), 1, 'the count did not follow the registration')
    })

    it('removing a runtime tears nothing down and shows what is still on it', async () => {
      const before = await countEnvironments(org.orgId, true)

      const removed = data<{ removed: boolean; environmentsStillRunning: number }>(
        await callProcedure(h, admin, 'runtimes.remove', 'mutation', { name: 'somebodys-laptop' }),
        'runtimes.remove',
      )
      assert.equal(removed.removed, true)
      assert.equal(
        removed.environmentsStillRunning,
        1,
        'removing a runtime did not say what was still running on it',
      )
      assert.equal(
        await countEnvironments(org.orgId, true),
        before,
        'removing a runtime tore something down',
      )

      // Back to being an unregistered runtime with a live environment on it,
      // which is the true state and the one worth showing.
      const listed = data<{ name: string; registered: boolean }[]>(
        await callProcedure(h, member, 'runtimes.list', 'query', { includeRemoved: false }),
        'runtimes.list',
      )
      const rows = listed.filter((r) => r.name === 'somebodys-laptop')
      assert.equal(rows.length, 1)
      assert.equal(rows[0]!.registered, false, 'a removed runtime is still shown as registered')

      const twice = await callProcedure(h, admin, 'runtimes.remove', 'mutation', {
        name: 'somebodys-laptop',
      })
      assert.equal(errorCode(twice.body), 'NOT_FOUND')

      // Still readable with its history, because environments name it.
      const withRemoved = data<{ name: string; removed_at: string | null; registered: boolean }[]>(
        await callProcedure(h, member, 'runtimes.list', 'query', { includeRemoved: true }),
        'runtimes.list',
      )
      assert.ok(
        withRemoved.find((r) => r.name === 'somebodys-laptop' && r.registered && r.removed_at !== null),
      )
    })
  })

  // -------------------------------------------------------------------------
  // The plan
  // -------------------------------------------------------------------------

  describe('the plan is a thing the quota check obeys', () => {
    it('changing the plan changes what may be created, and removes nothing', async () => {
      const start = data<{ plan: string; takesPayment: boolean; plans: { name: string }[] }>(
        await callProcedure(h, owner, 'billing.get', 'query', {}),
        'billing.get',
      )
      assert.equal(start.plan, 'free')
      assert.equal(start.takesPayment, false, 'this route claims to take money, and it does not')
      assert.ok(start.plans.length >= 3, 'the change control has nothing to choose between')

      // Up to a plan whose environment quota this fixture is nowhere near, so
      // the create below is refused for exactly one reason.
      data(await callProcedure(h, owner, 'billing.set', 'mutation', { plan: 'team' }), 'billing.set')
      const [entry] = await h.admin<{ detail: { plan: string; tookPayment: boolean } }[]>`
        SELECT detail FROM audit_entries
        WHERE org_id = ${org.orgId} AND action = 'organization.plan_changed'
        ORDER BY seq DESC LIMIT 1`
      assert.equal(entry!.detail.plan, 'team')
      assert.equal(entry!.detail.tookPayment, false)

      // Setting the plan it already has writes nothing.
      const entriesBefore = await countPlanChanges()
      const noop = data<{ changed: boolean }>(
        await callProcedure(h, owner, 'billing.set', 'mutation', { plan: 'team' }),
        'billing.set',
      )
      assert.equal(noop.changed, false)
      assert.equal(
        await countPlanChanges(),
        entriesBefore,
        'a plan change that changed nothing was audited',
      )

      // A plan with no room at all, to prove the quota is read from the plan
      // rather than from a constant. Nothing is torn down by the change: the
      // environments this organization holds are still here, and it is the
      // NEXT creation that is refused.
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${org.repoId}, ${`quota-${Date.now()}`}, 'main', 'running')`
      const held = await countEnvironments(org.orgId, true)

      data(await callProcedure(h, owner, 'billing.set', 'mutation', { plan: 'free' }), 'billing.set')
      assert.equal(
        await countEnvironments(org.orgId, true),
        held,
        'shrinking the plan tore an environment down',
      )

      // free allows three, and the fixture is at or past that now.
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${org.repoId}, ${`quota2-${Date.now()}`}, 'main', 'running'),
               (${org.orgId}, ${org.repoId}, ${`quota3-${Date.now()}`}, 'main', 'running')`
      const seen = h.github.dispatches.length
      const refused = await callProcedure(h, member, 'environments.create', 'mutation', {
        repository: org.repository,
      })
      assert.equal(
        errorCode(refused.body),
        'PRECONDITION_FAILED',
        'the plan was set to free and the quota let another environment through anyway',
      )
      assert.match(message(refused.body), /free plan/)
      assert.equal(
        h.github.dispatches.length,
        seen,
        'a workflow was dispatched for an environment the quota was going to refuse',
      )
    })
  })
})
