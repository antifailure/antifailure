// The hosted commercial boundary, through each authenticated entry point.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { githubAppInstallUrlFrom, hostedRequiredPlanFrom } from '../src/hosted.ts'
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
