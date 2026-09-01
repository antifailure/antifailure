// The API under test, against a real database and a GitHub that behaves.

import { randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { createPool, migrate, type Pool } from '@antifailure/db'
import { createServer } from '../src/server.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import { RecordingMailer } from '../src/auth/mail.ts'
import { issueSession } from '../src/auth/session.ts'
import type { Role } from '../src/permissions.ts'
import { findConsoleBuild } from '../src/console/static.ts'
import { RealStripeClient } from '../src/billing/stripe.ts'
import type { StripeConfig } from '../src/billing/plans.ts'
import type { Billing } from '../src/billing/index.ts'
import { MockPack, loadPack } from './mockpack.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export function appUrl(): string {
  const u = new URL(adminUrl)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
}

/**
 * Whether there is a database to test against.
 *
 * Two things here are deliberate and both were paid for.
 *
 * The timeout is thirty seconds rather than three. Three is plenty on an idle
 * machine and nowhere near enough on a busy one: measured on a loaded laptop,
 * accepting a connection took between two and thirty seconds, so the probe
 * timed out, the suite skipped, and the run went green having tested nothing.
 * A skip that machine load can cause is a pass with extra steps, and it is
 * invisible precisely when the machine is busy, which is when tests are being
 * run in bulk.
 *
 * AF_REQUIRE_DATABASE=1 turns the skip into a failure. Somewhere, usually
 * continuous integration, there has to be a place where "no database" is not an
 * acceptable answer, or every one of these suites is optional forever.
 */
export async function available(): Promise<boolean> {
  const timeout = Number(process.env.AF_TEST_CONNECT_TIMEOUT ?? 30)
  try {
    const probe = postgres(adminUrl, { max: 1, connect_timeout: timeout, onnotice: () => {} })
    await probe`SELECT 1`
    await probe.end({ timeout: 5 })
    return true
  } catch (err) {
    if (process.env.AF_REQUIRE_DATABASE === '1') {
      throw new Error(
        `AF_REQUIRE_DATABASE is set and ${adminUrl} did not answer within ${timeout}s: ` +
          `${err instanceof Error ? err.message : String(err)}`,
      )
    }
    return false
  }
}

export interface ApiHarness {
  /** The Hono app itself, so a test can read the routes it actually serves
   *  rather than a list of the routes somebody remembered to write down. */
  app: ReturnType<typeof createServer>['app']
  admin: postgres.Sql
  pool: Pool
  clock: FakeClock
  github: FakeGitHub
  /** Every message the sign-in path tried to send. Nothing leaves the process. */
  mailer: RecordingMailer
  fetch: (path: string, init?: RequestInit) => Promise<Response>
  close(): Promise<void>
}

export interface StartApiOptions {
  /** Who may sign in. Undefined leaves the server open, which is its default. */
  signInAllowlist?: ReadonlySet<string> | null
  /** The secret that seals provider keys. Undefined means none is configured,
   *  which is a state the server has to serve rather than crash in, and there
   *  are tests for that. */
  sealingKey?: Buffer | null
  /** The GitHub App's webhook secret. Undefined means no App, and the webhook
   *  endpoint refuses every delivery rather than accepting unsigned ones. */
  githubWebhookSecret?: string | null
  /** What each model costs, for the budgeted model proxy. */
  modelPrices?: Record<string, { inputPerMillion: number; outputPerMillion: number }>
  /** Where the providers live, so no test reaches a real one. */
  providerBases?: Record<string, string>
  /** A directory holding an exported console. Undefined means the server runs
   *  without one, which is a real way to run it and has its own test. */
  consoleDir?: string
  /** Billing, against the engine's own Stripe mock pack. Undefined means this
   *  server takes no money, which is the self-hosted default and has its own
   *  tests. */
  stripe?: Billing | null
}

export async function startApi(options: StartApiOptions = {}): Promise<ApiHarness> {
  const admin = postgres(adminUrl, {
    max: 4,
    connect_timeout: Number(process.env.AF_TEST_CONNECT_TIMEOUT ?? 30),
    onnotice: () => {},
  })
  await migrate(admin)
  await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

  const pool = createPool({
    url: appUrl(),
    max: 6,
    connectTimeoutSeconds: Number(process.env.AF_TEST_CONNECT_TIMEOUT ?? 30),
  })
  const clock = new FakeClock()
  const github = new FakeGitHub(clock)
  const mailer = new RecordingMailer()
  const { app } = createServer({
    pool,
    github,
    clock,
    // Configured, so the two routes exist and the catalog test covers them.
    // Nothing is sent: the mailer keeps what it was given.
    emailSignIn: { mailer, baseUrl: 'http://api.test', productName: 'Antifailure' },
    // The test client speaks plain HTTP, and a Secure cookie would not come
    // back. Production defaults the other way and there is a test for that.
    secureCookies: false,
    appBaseUrl: 'http://app.test/',
    signInAllowlist: options.signInAllowlist ?? null,
    sealingKey: options.sealingKey ?? null,
    githubWebhookSecret: options.githubWebhookSecret ?? null,
    ...(options.modelPrices ? { modelPrices: options.modelPrices } : {}),
    ...(options.providerBases ? { providerBases: options.providerBases } : {}),
    ...(options.consoleDir ? { consoleBuild: await findConsoleBuild(options.consoleDir) } : {}),
    stripe: options.stripe ?? null,
  })

  return {
    app,
    admin,
    pool,
    clock,
    github,
    mailer,
    fetch: async (path, init) => app.fetch(new Request(`http://api.test${path}`, init)),
    async close() {
      await pool.close()
      await admin.end({ timeout: 5 })
    },
  }
}

/**
 * A Stripe that behaves, without the network and without an account.
 *
 * The client is the one that ships. Only its transport is replaced, with a
 * fetch that hands the request to `engine/internal/mockpack` running the
 * product's own Stripe pack. That is deliberately not a FakeStripeClient: a
 * fake agrees with whatever its author believed the response shape was, and
 * this arrangement is what found five defects in the shipped pack.
 *
 * A request no route matches answers 501 rather than 404, because 404 is a real
 * answer here: getSubscription reads it as "Stripe has never heard of this",
 * and a missing ROUTE must never be mistaken for a missing OBJECT.
 */
export async function stripeAgainstMockPack(
  overrides: Partial<StripeConfig> = {},
): Promise<{ billing: Billing; pack: MockPack; config: StripeConfig }> {
  const pack = new MockPack([await loadPack('stripe')])
  const config: StripeConfig = {
    secretKey: 'sk_test_afmock',
    webhookSecret: 'whsec_afmocktestsecret',
    prices: { team: 'price_team_afmock', enterprise: 'price_enterprise_afmock' },
    apiBase: 'https://api.stripe.com',
    fetch: async (input, init) => {
      const url = new URL(input instanceof Request ? input.url : String(input))
      const method = init?.method ?? (input instanceof Request ? input.method : 'GET')
      const body = typeof init?.body === 'string' ? init.body : ''
      const answer = pack.answer(url.hostname, method, url.pathname, body)
      if (!answer) {
        return new Response(
          JSON.stringify({
            error: {
              type: 'invalid_request_error',
              message: `no mock pack route for ${method} ${url.pathname}`,
            },
          }),
          { status: 501, headers: { 'content-type': 'application/json' } },
        )
      }
      return new Response(answer.body, {
        status: answer.status,
        headers: { 'content-type': 'application/json' },
      })
    },
    ...overrides,
  }
  return { billing: { config, client: new RealStripeClient(config) }, pack, config }
}

export interface Org {
  orgId: string
  slug: string
  repoId: string
  repository: string
  envId: string
}

export async function seedOrg(admin: postgres.Sql, label: string): Promise<Org> {
  const slug = `${label}-${randomUUID().slice(0, 8)}`
  const [org] = await admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name, github_login) VALUES (${slug}, ${label}, ${slug})
    RETURNING id`
  const orgId = org!.id
  const repository = `${slug}/app`
  const [repo] = await admin<{ id: string }[]>`
    INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${repository}) RETURNING id`
  const envId = `env-${slug}`
  await admin`
    INSERT INTO environments (org_id, repository_id, env_id, branch, state)
    VALUES (${orgId}, ${repo!.id}, ${envId}, 'main', 'running')`
  return { orgId, slug, repoId: repo!.id, repository, envId }
}

export interface SignedIn {
  userId: string
  token: string
  csrfToken: string
  cookie: string
}

/** Creates a member with a role and returns a working session for them. */
export async function signInAs(
  h: ApiHarness,
  org: Org,
  role: Role,
  /** Only for making the login readable in the database while debugging. */
  label: string = role,
): Promise<SignedIn> {
  const login = `${label}-${randomUUID().slice(0, 6)}`
  const [user] = await h.admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${`${login}@example.test`}, ${label})
    RETURNING id`
  await h.admin`
    INSERT INTO members (org_id, user_id, role, source)
    VALUES (${org.orgId}, ${user!.id}, ${role}, 'manual')`

  const issued = await issueSession(h.pool, h.clock, { userId: user!.id, orgId: org.orgId })
  return {
    userId: user!.id,
    token: issued.token,
    csrfToken: issued.csrfToken,
    cookie: `af_session=${issued.token}`,
  }
}

/** Calls a tRPC procedure the way the browser client would. */
export async function callProcedure(
  h: ApiHarness,
  session: SignedIn | null,
  path: string,
  type: 'query' | 'mutation',
  input: unknown,
): Promise<{ status: number; body: unknown }> {
  // A hundred milliseconds of simulated time per call, because otherwise the
  // suite makes every request it will ever make at the same instant.
  //
  // POST /trpc/* is 20 a second with a burst of 200, keyed on the organization,
  // which falls back to the address for these tests, so every request in a file
  // shares one bucket. The harness clock only moves when somebody moves it, so
  // the bucket never refills and the two hundredth request in a file is refused
  // no matter how long the file took to run. The permission matrix crossed that
  // line the moment two branches each added a route, and the tests that failed
  // were the last two in the file rather than anything to do with the routes:
  // "a demoted member kept the old role" is what a 429 looks like when the
  // assertion is reading an error code.
  //
  // Advancing here rather than in each test, because auth.test.ts and
  // console.test.ts each discovered this separately and each fixed it in their
  // own file, which leaves the next file to discover it again. A hundred
  // milliseconds refills two tokens for the one this call spends, so no
  // sequence of ordinary calls can starve, and it is far below anything any
  // test asserts about expiry.
  //
  // This does not weaken the limiter or hide a regression in it. The tests that
  // prove it refuses a burst, in extensions.test.ts, call app.request directly
  // with their own address and never come through here.
  h.clock.advance(100)

  const headers: Record<string, string> = { 'content-type': 'application/json' }
  if (session) {
    headers.cookie = session.cookie
    headers['x-antifailure-csrf'] = session.csrfToken
  }

  const res =
    type === 'query'
      ? await h.fetch(
          `/trpc/${path}?input=${encodeURIComponent(JSON.stringify(input ?? {}))}`,
          { headers },
        )
      : await h.fetch(`/trpc/${path}`, {
          method: 'POST',
          headers,
          body: JSON.stringify(input ?? {}),
        })

  const text = await res.text()
  let body: unknown = text
  try {
    body = JSON.parse(text)
  } catch {
    // Left as text; the assertion will say what came back.
  }
  return { status: res.status, body }
}

/** The tRPC error code out of a response, or null if it succeeded. */
export function errorCode(body: unknown): string | null {
  const b = body as { error?: { data?: { code?: string } } }
  return b?.error?.data?.code ?? null
}

export async function dropOrg(admin: postgres.Sql, orgId: string): Promise<void> {
  await admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
  // Deliberately not cascaded from organizations, because the deletion record
  // is the one row that has to outlive the organization it is about. See
  // migrations/0021. A suite that left them behind would hit the partial unique
  // index the next time it deleted the same organization.
  await admin`DELETE FROM organization_deletions WHERE org_id = ${orgId}`
  await admin`DELETE FROM organizations WHERE id = ${orgId}`
}

/** A signed-in session for somebody who belongs to no organization, which is
 *  the state an invited person is in when they open the link. */
export async function signInWithNoOrganization(
  h: ApiHarness,
  label = 'invitee',
): Promise<SignedIn & { email: string }> {
  const login = `${label}-${randomUUID().slice(0, 6)}`
  const email = `${login}@example.test`
  const [user] = await h.admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 1e12)}, ${login}, ${email}, ${label})
    RETURNING id`
  const issued = await issueSession(h.pool, h.clock, { userId: user!.id, orgId: null })
  return {
    userId: user!.id,
    email,
    token: issued.token,
    csrfToken: issued.csrfToken,
    cookie: `af_session=${issued.token}`,
  }
}
