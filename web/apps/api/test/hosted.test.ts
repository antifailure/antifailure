// The hosted commercial boundary, through each authenticated entry point.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  HOSTED_GATE_EXEMPT,
  githubAppInstallUrlFrom,
  hostedRequiredPlanFrom,
} from '../src/hosted.ts'
import { hashEngineToken } from '../src/ingest.ts'
import { fileURLToPath } from 'node:url'
import {
  adminUrl,
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  startApi,
  stripeAgainstMockPack,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'

function data<T>(body: unknown): T {
  const b = body as { result?: { data?: T }; error?: { message?: string } }
  assert.ok(b.result, `expected a result, got: ${JSON.stringify(b.error ?? b).slice(0, 500)}`)
  return b.result.data as T
}

const hasDatabase = await available()

describe('the hosted plan configuration', () => {
  it('is off when unset and accepts only enterprise', () => {
    assert.equal(hostedRequiredPlanFrom(undefined), null)
    assert.equal(hostedRequiredPlanFrom(' enterprise '), 'enterprise')
    assert.throws(() => hostedRequiredPlanFrom('team'), /must be enterprise or unset/)
  })

  it('accepts only the public GitHub App installation address shape', () => {
    assert.equal(
      githubAppInstallUrlFrom('https://github.com/apps/antifailure/installations/new'),
      'https://github.com/apps/antifailure/installations/new',
    )
    assert.throws(() => githubAppInstallUrlFrom('javascript:alert(1)'), /must be an https/)
    assert.throws(() => githubAppInstallUrlFrom('https://example.com/install'), /must be an https/)
  })
})

describe('starting up with the gate set', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  /**
   * Runs the real entry point as a subprocess and reports how it ended.
   *
   * A start-up refusal is a gate, and a gate nobody has watched fail is
   * decoration. The refusals below cannot be reached from createServer,
   * because they are decisions main.ts makes about its own environment before
   * it builds one, so the only honest way to check them is to run it.
   */
  async function start(env: Record<string, string>): Promise<{ code: number | null; out: string }> {
    const { spawn } = await import('node:child_process')
    const url = new URL('../src/main.ts', import.meta.url)
    const child = spawn(process.execPath, [fileURLToPath(url)], {
      env: {
        ...process.env,
        AF_DATABASE_URL: adminUrl,
        AF_MIGRATION_DATABASE_URL: adminUrl,
        AF_GITHUB_CLIENT_ID: 'id',
        AF_GITHUB_CLIENT_SECRET: 'secret',
        AF_GITHUB_REDIRECT_URI: 'https://app.test/auth/github/callback',
        AF_MIGRATE: '0',
        AF_PORT: '0',
        ...env,
      },
      stdio: ['ignore', 'pipe', 'pipe'],
    })
    let out = ''
    child.stdout.on('data', (c) => { out += String(c) })
    child.stderr.on('data', (c) => { out += String(c) })
    return await new Promise((resolve) => {
      // A refusal that never arrives would otherwise hang the suite, and a
      // process still listening is itself the failure being tested for.
      const timer = setTimeout(() => {
        child.kill('SIGKILL')
        resolve({ code: null, out })
      }, 30_000)
      child.on('exit', (code) => {
        clearTimeout(timer)
        child.kill('SIGKILL')
        resolve({ code, out })
      })
    })
  }

  it('refuses to start when the gate is set and billing is off', async () => {
    // The combination nobody could satisfy: every request refused, and the
    // billing path that resolves the refusal unable to take money. Saying so
    // once at start-up beats saying it to every customer.
    const { code, out } = await start({ AF_HOSTED_REQUIRED_PLAN: 'enterprise' })
    assert.equal(code, 2, `expected a refusal, got ${code}: ${out}`)
    assert.match(out, /AF_HOSTED_REQUIRED_PLAN is set but billing is off/)
  })

  it('refuses a plan it does not sell and an installation address it cannot trust', async () => {
    const plan = await start({ AF_HOSTED_REQUIRED_PLAN: 'team' })
    assert.equal(plan.code, 2, plan.out)
    assert.match(plan.out, /must be enterprise or unset/)

    const url = await start({ AF_GITHUB_APP_INSTALL_URL: 'https://example.com/install' })
    assert.equal(url.code, 2, url.out)
    assert.match(url.out, /must be an https:\/\/github\.com\/apps/)
  })
})

describe('enterprise-only hosted access', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn
  let engineToken: string
  let cliToken: string

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    h = await startApi({
      stripe: stripe.billing,
      hostedRequiredPlan: 'enterprise',
      githubAppInstallUrl: 'https://github.com/apps/antifailure/installations/new',
    })
    org = await seedOrg(h.admin, 'hosted-gate')
    owner = await signInAs(h, org, 'owner')
    engineToken = `aft_${'hosted-gate-token'.padEnd(43, 'x')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix, kind)
      VALUES (${org.orgId}, 'hosted gate', ${hashEngineToken(engineToken)},
              ${engineToken.slice(0, 12)}, 'engine')`
    cliToken = `afu_${'hosted-cli-token'.padEnd(43, 'x')}`
    await h.admin`
      INSERT INTO engine_tokens
        (org_id, user_id, name, token_hash, prefix, kind, scopes, expires_at)
      VALUES (${org.orgId}, ${owner.userId}, 'hosted cli', ${hashEngineToken(cliToken)},
              ${cliToken.slice(0, 12)}, 'cli', ARRAY['providers.view'],
              now() + interval '1 day')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('reports the gate and the self-service installation address in the session', async () => {
    const response = await h.fetch('/auth/session', { headers: { cookie: owner.cookie } })
    assert.equal(response.status, 200)
    const session = await response.json() as Record<string, unknown>
    assert.equal(session.plan, 'free')
    assert.equal(session.hostedRequiredPlan, 'enterprise')
    assert.equal(session.hostedAccess, false)
    assert.equal(
      session.githubAppInstallUrl,
      'https://github.com/apps/antifailure/installations/new',
    )
  })

  it('refuses an operational browser procedure while leaving billing reachable', async () => {
    const refused = await callProcedure(
      h, owner, 'repositories.list', 'query', { includeArchived: false },
    )
    assert.equal(errorCode(refused.body), 'FORBIDDEN')
    assert.match(JSON.stringify(refused.body), /requires the enterprise plan/)

    const billing = await callProcedure(h, owner, 'billing.get', 'query', {})
    assert.equal(billing.status, 200, JSON.stringify(billing.body))
  })

  it('cannot self-entitle through the administrative plan route', async () => {
    const refused = await callProcedure(h, owner, 'billing.set', 'mutation', {
      plan: 'enterprise',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${org.orgId}`
    assert.equal(row!.plan, 'free')
  })

  it('leaves Stripe checkout reachable because it is how the gate is resolved', async () => {
    const wrongPlan = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'team',
      seats: 1,
      successUrl: 'https://app.test/plan?checkout=success',
      cancelUrl: 'https://app.test/plan',
    })
    assert.equal(errorCode(wrongPlan.body), 'PRECONDITION_FAILED')

    const checkout = await callProcedure(h, owner, 'subscriptions.checkout', 'mutation', {
      plan: 'enterprise',
      seats: 1,
      successUrl: 'https://app.test/plan?checkout=success',
      cancelUrl: 'https://app.test/plan',
    })
    assert.equal(checkout.status, 200, JSON.stringify(checkout.body))
    assert.match(JSON.stringify(checkout.body), /checkout\.stripe\.com/)
  })

  it('refuses CLI and engine tokens before payment and accepts both after the enterprise grant', async () => {
    const request = () => h.fetch('/v1/events', {
      method: 'POST',
      headers: {
        authorization: `Bearer ${engineToken}`,
        'content-type': 'application/json',
      },
      body: JSON.stringify({ events: [] }),
    })

    const refused = await request()
    assert.equal(refused.status, 402)
    assert.match(await refused.text(), /requires the enterprise plan/)

    const cliRefused = await h.fetch('/v1/providers', {
      headers: { authorization: `Bearer ${cliToken}` },
    })
    assert.equal(cliRefused.status, 402)
    assert.match(await cliRefused.text(), /requires the enterprise plan/)

    // The webhook is the production writer. Updating the same column here keeps
    // this test focused on whether every authenticated entry point observes it;
    // billing.test.ts proves the signed webhook is what moves the column.
    await h.admin`UPDATE organizations SET plan = 'enterprise' WHERE id = ${org.orgId}`

    const accepted = await request()
    assert.equal(accepted.status, 202, await accepted.text())

    const cliAccepted = await h.fetch('/v1/providers', {
      headers: { authorization: `Bearer ${cliToken}` },
    })
    assert.equal(cliAccepted.status, 200, await cliAccepted.text())

    const browser = await callProcedure(
      h, owner, 'repositories.list', 'query', { includeArchived: false },
    )
    assert.equal(browser.status, 200, JSON.stringify(browser.body))
  })
})

/**
 * The exits, which a lapsed plan may not close.
 *
 * This suite exists ONLY on the merged tree, because that is the only place the
 * defect exists. `w-authbilling-codex` put the hosted gate in shared tRPC
 * middleware exempting one permission; `w-enterprise-mgmt` added the routes a
 * customer uses to export their data, delete their organization, close their
 * account and revoke a leaked session. Neither branch is wrong alone, git
 * merges `orgProcedure` clean, and the result was a hosted service on which a
 * customer whose subscription had lapsed could only pay.
 *
 * The line these assertions encode, which is also written next to
 * HOSTED_GATE_EXEMPT: a permission is exempt when refusing it TRAPS somebody in
 * the product, and gated when refusing it merely stops them using something
 * they have not paid for.
 *
 * It has been proved able to fail: removing `sessions.manage` from
 * HOSTED_GATE_EXEMPT turns "sessions.manage: seeing who is signed in" red with
 * the hosted refusal in the message, and the summary names that permission.
 */
describe('a lapsed plan does not close the exits', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    h = await startApi({ stripe: stripe.billing, hostedRequiredPlan: 'enterprise' })
    org = await seedOrg(h.admin, 'hosted-exits')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  /** The state under test, asserted rather than assumed: no hosted plan. */
  it('is an organization with no hosted plan', async () => {
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${org.orgId}`
    assert.equal(row!.plan, 'free')
  })

  /**
   * Every exempt permission gets a cell, named by the permission, so a failure
   * says which exit closed rather than which route broke.
   *
   * Each route is one that reaches its handler without changing anything a
   * later cell depends on. `account.close` is called with a confirmation that
   * cannot match, so what it proves is that the request got PAST the gate and
   * was answered by the route's own validation.
   */
  const exits: Array<{
    permission: string
    what: string
    route: string
    type: 'query' | 'mutation'
    input: Record<string, unknown>
  }> = [
    {
      permission: 'billing.manage',
      what: 'the path that resolves the refusal',
      route: 'org.billingContact', type: 'query', input: {},
    },
    {
      permission: 'data.export',
      what: 'taking your own data out',
      route: 'exports.organization', type: 'mutation', input: {},
    },
    {
      permission: 'organization.delete',
      what: 'watching a deletion you have already asked for',
      route: 'deletion.status', type: 'query', input: {},
    },
    {
      permission: 'sessions.manage',
      what: 'seeing who is signed in',
      route: 'sessions.list', type: 'query', input: { includeRevoked: false },
    },
    {
      permission: 'account.close',
      what: 'a person leaving',
      route: 'account.close', type: 'mutation',
      input: { confirm: 'this cannot be anybody\'s label' },
    },
  ]

  for (const exit of exits) {
    it(`${exit.permission}: ${exit.what}`, async () => {
      const { status, body } = await callProcedure(h, owner, exit.route, exit.type, exit.input)
      const said = JSON.stringify(body)
      assert.doesNotMatch(
        said,
        /requires the enterprise plan/,
        `${exit.permission} was refused by the plan gate on ${exit.route}. ` +
          'A lapsed customer would be trapped in the product.',
      )
      if (exit.route === 'account.close') {
        // The one cell that must NOT succeed: it reached its own validation,
        // which is the proof that the gate let it through.
        assert.equal(errorCode(body), 'BAD_REQUEST')
        assert.match(said, /to confirm/)
        const [row] = await h.admin<{ closed_at: Date | null }[]>`
          SELECT closed_at FROM users WHERE id = ${owner.userId}`
        assert.equal(row!.closed_at, null, 'the account was closed by a test that must not close it')
      } else {
        assert.equal(status, 200, said)
      }
    })
  }

  /**
   * The read that makes every one of the above REACHABLE rather than merely
   * permitted, and the reason it is under account.close.
   *
   * Exempting the permissions was not enough. The console's settings page reads
   * org.settings, which is environments.view and stays gated, so a lapsed
   * customer had the page refused and every exempt control on it went with it.
   * account.context is the exit screen's own read, and it has to answer for
   * every role that holds an exit: an admin holds data.export and
   * sessions.manage and does not hold organization.delete, so a read under that
   * permission would have left an admin with exits and no page.
   */
  for (const role of ['owner', 'admin', 'member', 'viewer'] as const) {
    it(`account.context is reachable by a ${role} with no hosted plan`, async () => {
      const who = role === 'owner' ? owner : await signInAs(h, org, role)
      const { status, body } = await callProcedure(h, who, 'account.context', 'query', {})
      assert.equal(status, 200, JSON.stringify(body))
      const got = data<{
        organization: { slug: string; name: string; plan: string }
        hostedRequiredPlan: string | null
        hostedAccess: boolean
        role: string
        permissions: string[]
        exportRetentionDays: number
        sessions: { count: number } | null
      }>(body)

      // The load-bearing field. deletion.request refuses anything but an exact
      // match on the slug, and the only other route exposing it is gated, so
      // without this a lapsed owner is asked to type a string the product
      // refuses to show them.
      assert.equal(got.organization.slug, org.slug)
      assert.equal(got.organization.plan, 'free')
      assert.equal(got.hostedRequiredPlan, 'enterprise')
      assert.equal(got.hostedAccess, false)
      assert.equal(got.role, role)
      assert.ok(got.exportRetentionDays > 0)

      // The screen renders only the exits this person holds, so the permission
      // list has to be this role's real one rather than everybody's.
      assert.ok(got.permissions.includes('account.close'))
      assert.equal(got.permissions.includes('organization.delete'), role === 'owner')
      assert.equal(got.permissions.includes('data.export'), role === 'owner' || role === 'admin')

      // The count only, and only for the roles that may read who is signed in.
      if (role === 'owner' || role === 'admin') {
        assert.ok(got.sessions && got.sessions.count >= 1, 'no session count for a role that holds sessions.manage')
      } else {
        assert.equal(got.sessions, null, 'a role without sessions.manage was handed a session count')
      }
    })
  }

  /**
   * The other half, without which the set proves nothing. If everything were
   * reachable the assertions above would pass with the gate deleted.
   */
  it('environments.view is still refused, because it is the product doing work', async () => {
    const refused = await callProcedure(h, owner, 'org.settings', 'query', {})
    assert.equal(errorCode(refused.body), 'FORBIDDEN')
    assert.match(JSON.stringify(refused.body), /requires the enterprise plan/)
  })

  it('organization.settings is still refused', async () => {
    const refused = await callProcedure(h, owner, 'org.rename', 'mutation', { name: 'Renamed' })
    assert.equal(errorCode(refused.body), 'FORBIDDEN')
    assert.match(JSON.stringify(refused.body), /requires the enterprise plan/)
  })

  /**
   * The set itself, read rather than described. A permission added to
   * HOSTED_GATE_EXEMPT without a cell above would otherwise be exempt and
   * untested, which is the state that produced this defect in the first place.
   */
  it('every exempt permission has a cell above it', () => {
    assert.deepEqual(
      [...HOSTED_GATE_EXEMPT].sort(),
      exits.map((e) => e.permission).sort(),
    )
  })
})
