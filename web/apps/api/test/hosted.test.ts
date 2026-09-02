// The hosted commercial boundary, through each authenticated entry point.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  HOSTED_GATE_EXEMPT,
  githubAppInstallUrlFrom,
  hostedRequiredPlanFrom,
  operatorSetsPlanFrom,
} from '../src/hosted.ts'
import { readFile, readdir } from 'node:fs/promises'
import path from 'node:path'
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

  it('treats plans set by hand as off unless an operator says otherwise', () => {
    assert.equal(operatorSetsPlanFrom(undefined), false)
    assert.equal(operatorSetsPlanFrom(''), false)
    assert.equal(operatorSetsPlanFrom('0'), false)
    assert.equal(operatorSetsPlanFrom('false'), false)
    assert.equal(operatorSetsPlanFrom('1'), true)
    assert.equal(operatorSetsPlanFrom(' TRUE '), true)
    // Anything else is a typo, and a typo that reads as off would be a plane
    // whose operator believes they turned the route on.
    assert.throws(() => operatorSetsPlanFrom('yes'), /must be 1, 0 or unset/)
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

  it('refuses to start when plans are set by hand on a plane that takes money', async () => {
    // The contradiction, refused where it cannot be reached around. A route
    // that grants a plan and a checkout that sells the same plan on one
    // process is a product nobody has to pay for, and it stops here rather
    // than in whichever procedure happens to carry the check today.
    const stripe = await start({
      AF_OPERATOR_SETS_PLAN: '1',
      AF_STRIPE_SECRET_KEY: 'sk_test_not_a_real_key',
      AF_STRIPE_WEBHOOK_SECRET: 'whsec_not_a_real_secret',
      AF_STRIPE_PRICE_TEAM: 'price_team',
      AF_STRIPE_PRICE_ENTERPRISE: 'price_enterprise',
    })
    assert.equal(stripe.code, 2, `expected a refusal, got ${stripe.code}: ${stripe.out}`)
    assert.match(stripe.out, /AF_OPERATOR_SETS_PLAN/)

    const value = await start({ AF_OPERATOR_SETS_PLAN: 'yes' })
    assert.equal(value.code, 2, value.out)
    assert.match(value.out, /must be 1, 0 or unset/)
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

/**
 * The plan nobody paid for.
 *
 * THE CONFIGURATION UNDER TEST IS THE ONE PRODUCTION BOOTS IN, and that is the
 * only reason this suite exists. No Stripe, no plan gate, sign-in open to
 * whoever the allowlist admits. Every other suite in this file configures
 * something; this one configures nothing, because a hosted plane whose operator
 * has not got to billing yet is not a hypothetical, it is the state the hosted
 * control plane is in the day the code that can take money first reaches it.
 *
 * `billing.set` used to refuse only when Stripe or the gate was configured, so
 * in exactly this configuration an org owner, which is what the first person
 * into any organization becomes, could call it with `enterprise` and take a
 * five hundred environment, twenty thousand env-hour plan for nothing.
 *
 * It has been proved able to fail: with the refusal removed from
 * `routers/billing.ts`, the first cell below goes red reporting that the
 * organization is on the enterprise plan.
 */
describe('a control plane that takes no money does not hand out its own plans', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    // Nothing configured, deliberately. This is the whole fixture.
    h = await startApi()
    org = await seedOrg(h.admin, 'plan-unconfigured')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function planOf(): Promise<string> {
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${org.orgId}`
    return row!.plan
  }

  it('refuses an owner who asks for the enterprise plan', async () => {
    assert.equal(await planOf(), 'free')
    const refused = await callProcedure(h, owner, 'billing.set', 'mutation', {
      plan: 'enterprise',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED', JSON.stringify(refused.body))
    assert.match(JSON.stringify(refused.body), /AF_OPERATOR_SETS_PLAN/)
    assert.equal(await planOf(), 'free', 'an owner granted themselves the enterprise plan')
  })

  it('refuses every other plan too, and writes no audit entry', async () => {
    for (const plan of ['team', 'free']) {
      const refused = await callProcedure(h, owner, 'billing.set', 'mutation', { plan })
      assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED', `${plan}: ${JSON.stringify(refused.body)}`)
    }
    const entries = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'organization.plan_changed'`
    assert.equal(Number(entries[0]!.n), 0, 'a refused plan change was audited as a change')
    assert.equal(await planOf(), 'free')
  })

  it('says so in the payload the console renders, so no button is offered', async () => {
    // The other half of the same defect. The route refusing while the console
    // still draws "Move to enterprise" is a product that offers a control it
    // always refuses, which reads as broken rather than as refused.
    const { status, body } = await callProcedure(h, owner, 'billing.get', 'query', {})
    assert.equal(status, 200, JSON.stringify(body))
    const got = data<{ takesPayment: boolean; operatorSetsPlan: boolean }>(body)
    assert.equal(got.takesPayment, false)
    assert.equal(got.operatorSetsPlan, false)
  })
})

/**
 * The self-hosted operator, who is the reason the route exists at all.
 *
 * One person runs the control plane, runs the database, and decides what their
 * own organization is on. Refusing them would be refusing somebody who can
 * already write the column with psql, so the route stays, behind one variable
 * they set once.
 */
describe('an operator who says they set plans by hand may set them', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    h = await startApi({ operatorSetsPlan: true })
    org = await seedOrg(h.admin, 'plan-self-hosted')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('moves the plan and audits it', async () => {
    const { status, body } = await callProcedure(h, owner, 'billing.set', 'mutation', {
      plan: 'team',
      reason: 'self-hosted, and I am the operator',
    })
    assert.equal(status, 200, JSON.stringify(body))
    const got = data<{ plan: string; changed: boolean; operatorSetsPlan: boolean }>(body)
    assert.equal(got.plan, 'team')
    assert.equal(got.changed, true)
    assert.equal(got.operatorSetsPlan, true)

    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${org.orgId}`
    assert.equal(row!.plan, 'team')

    const [entry] = await h.admin<{ detail: { plan: string; tookPayment: boolean } }[]>`
      SELECT detail FROM audit_entries
      WHERE org_id = ${org.orgId} AND action = 'organization.plan_changed'
      ORDER BY seq DESC LIMIT 1`
    assert.equal(entry!.detail.plan, 'team')
    assert.equal(entry!.detail.tookPayment, false)
  })
})

/**
 * Stripe configured, no plan gate. The middle configuration, and the one this
 * file had no cell for: the enterprise-only suite above proves the gate refuses
 * it, and proving that only with the gate on would leave a plane that takes
 * money but sells every plan untested.
 */
describe('a plane that takes money never grants a plan by hand', {
  skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL',
}, () => {
  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    const stripe = await stripeAgainstMockPack()
    h = await startApi({ stripe: stripe.billing })
    org = await seedOrg(h.admin, 'plan-stripe-only')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('refuses and names Stripe rather than a variable', async () => {
    const refused = await callProcedure(h, owner, 'billing.set', 'mutation', { plan: 'team' })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED', JSON.stringify(refused.body))
    assert.match(JSON.stringify(refused.body), /derives paid plans from Stripe/)
    const [row] = await h.admin<{ plan: string }[]>`
      SELECT plan FROM organizations WHERE id = ${org.orgId}`
    assert.equal(row!.plan, 'free')
  })
})

/**
 * The guard against the next procedure, rather than against this one.
 *
 * A refusal inside `billing.set` protects `billing.set`. It says nothing about
 * the route somebody adds next year that also writes the column, and a guard
 * that covers one caller of a shared piece of state is the shape of defect this
 * whole file exists because of. So the column's writers are enumerated from the
 * source: two, one gated on the operator's declaration and one gated on a
 * signed Stripe delivery. A third has to be classified deliberately, here,
 * before the suite goes green again.
 */
describe('the writers of organizations.plan', () => {
  const expected = ['routers/billing.ts', 'billing/webhook.ts'].sort()

  async function sources(dir: string): Promise<string[]> {
    const entries = await readdir(dir, { withFileTypes: true })
    const out: string[] = []
    for (const e of entries) {
      const full = path.join(dir, e.name)
      if (e.isDirectory()) out.push(...(await sources(full)))
      else if (e.name.endsWith('.ts')) out.push(full)
    }
    return out
  }

  it('are the two the product intends and nothing else', async () => {
    const root = fileURLToPath(new URL('../src', import.meta.url))
    const found: string[] = []
    for (const file of await sources(root)) {
      const text = await readFile(file, 'utf8')
      for (const m of text.matchAll(/(?:UPDATE|INSERT INTO)\s+organizations\b[\s\S]*?`/gi)) {
        if (/\bplan\b/i.test(m[0])) {
          found.push(path.relative(root, file))
          break
        }
      }
    }
    assert.deepEqual(
      [...new Set(found)].sort(),
      expected,
      'a route that writes organizations.plan was added or removed. A new one is a new way to ' +
        'grant an entitlement: gate it the way billing.set is gated, then add it here.',
    )
  })

  it('finds the known writers at all, so a broken scan cannot pass quietly', async () => {
    // The negative control. If the pattern stops matching, the assertion above
    // goes green having compared two empty lists.
    const text = await readFile(
      fileURLToPath(new URL('../src/routers/billing.ts', import.meta.url)),
      'utf8',
    )
    const hits = [...text.matchAll(/(?:UPDATE|INSERT INTO)\s+organizations\b[\s\S]*?`/gi)]
      .filter((m) => /\bplan\b/i.test(m[0]))
    assert.equal(hits.length, 1, 'the scan no longer finds the write it was written against')
  })
})
