// The API under test, against a real database and a GitHub that behaves.

import { randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { createPool, migrate, type Pool } from '@antifailure/db'
import { createServer } from '../src/server.ts'
import { FakeClock } from '../src/clock.ts'
import { FakeGitHub } from '../src/auth/fakegithub.ts'
import { issueSession } from '../src/auth/session.ts'
import type { Role } from '../src/permissions.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

export function appUrl(): string {
  const u = new URL(adminUrl)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
}

export async function available(): Promise<boolean> {
  try {
    const probe = postgres(adminUrl, { max: 1, connect_timeout: 3, onnotice: () => {} })
    await probe`SELECT 1`
    await probe.end({ timeout: 2 })
    return true
  } catch {
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
  fetch: (path: string, init?: RequestInit) => Promise<Response>
  close(): Promise<void>
}

export async function startApi(): Promise<ApiHarness> {
  const admin = postgres(adminUrl, { max: 4, connect_timeout: 10, onnotice: () => {} })
  await migrate(admin)
  await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)

  const pool = createPool({ url: appUrl(), max: 6 })
  const clock = new FakeClock()
  const github = new FakeGitHub(clock)
  const { app } = createServer({
    pool,
    github,
    clock,
    // The test client speaks plain HTTP, and a Secure cookie would not come
    // back. Production defaults the other way and there is a test for that.
    secureCookies: false,
    appBaseUrl: 'http://app.test/',
  })

  return {
    app,
    admin,
    pool,
    clock,
    github,
    fetch: async (path, init) => app.fetch(new Request(`http://api.test${path}`, init)),
    async close() {
      await pool.close()
      await admin.end({ timeout: 5 })
    },
  }
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
  await admin`DELETE FROM organizations WHERE id = ${orgId}`
}
