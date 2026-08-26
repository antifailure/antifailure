// A real Postgres, or a skipped suite that says so.
//
// Row-level security is the thing under test, and there is no way to test it
// against a fake: the whole point is that Postgres enforces it and the
// application cannot. A mock database would prove the application asks nicely,
// which is not the property anybody cares about.
//
// So the suite needs a database. When there is not one it skips loudly rather
// than passing quietly, because a green run that proved nothing is worse than a
// red one.

import { createHash, randomUUID } from 'node:crypto'
import postgres from 'postgres'
import { migrate } from '../src/migrate.ts'
import { createPool, type Pool } from '../src/client.ts'

export const adminUrl =
  process.env.AF_TEST_DATABASE_URL ?? 'postgres://postgres:test@127.0.0.1:55432/antifailure'

/** The URL the application connects on: the unprivileged role, not the owner. */
export function appUrl(base = adminUrl): string {
  const u = new URL(base)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
}

export interface Harness {
  admin: postgres.Sql
  pool: Pool
  close(): Promise<void>
}

let cached: Harness | null = null

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

export async function setup(): Promise<Harness> {
  if (cached) return cached

  const admin = postgres(adminUrl, { max: 4, connect_timeout: 10, onnotice: () => {} })
  await migrate(admin)
  // The role is created NOLOGIN by the migration, because a self-hosted
  // installation supplies its own credential. The suite gives it one.
  await admin.unsafe(
    `ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`,
  )

  const pool = createPool({ url: appUrl(), max: 5, connectTimeoutSeconds: 10 })

  cached = {
    admin,
    pool,
    async close() {
      await pool.close()
      await admin.end({ timeout: 5 })
      cached = null
    },
  }
  return cached
}

export interface Fixture {
  orgId: string
  userId: string
  repoId: string
  envId: string
  runId: string
  slug: string
}

/**
 * Creates one tenant with a row in every table, using the owner connection so
 * that the fixture itself does not depend on the policies being correct. A
 * fixture built through the application role would fail to distinguish "the
 * policy blocked the write" from "the policy blocked the read".
 */
export async function seedTenant(admin: postgres.Sql, label: string): Promise<Fixture> {
  const slug = `${label}-${randomUUID().slice(0, 8)}`
  const [org] = await admin<{ id: string }[]>`
    INSERT INTO organizations (slug, name) VALUES (${slug}, ${label}) RETURNING id`
  const orgId = org!.id

  const [user] = await admin<{ id: string }[]>`
    INSERT INTO users (github_id, github_login, email, name)
    VALUES (${Math.floor(Math.random() * 1e12)}, ${slug}, ${`${slug}@example.test`}, ${label})
    RETURNING id`
  const userId = user!.id

  await admin`INSERT INTO members (org_id, user_id, role) VALUES (${orgId}, ${userId}, 'owner')`

  const [repo] = await admin<{ id: string }[]>`
    INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${`${slug}/app`}) RETURNING id`
  const repoId = repo!.id

  const [env] = await admin<{ id: string }[]>`
    INSERT INTO environments (org_id, repository_id, env_id, branch, state)
    VALUES (${orgId}, ${repoId}, ${`env-${slug}`}, 'main', 'running') RETURNING id`
  const envId = env!.id

  const [run] = await admin<{ id: string }[]>`
    INSERT INTO runs (org_id, environment_id, kind, state)
    VALUES (${orgId}, ${envId}, 'test', 'complete') RETURNING id`
  const runId = run!.id

  await admin`
    INSERT INTO verdicts (org_id, run_id, workflow, value, summary)
    VALUES (${orgId}, ${runId}, 'sign-up', 'pass', ${`${label} signed up`})`
  await admin`
    INSERT INTO artifacts (org_id, run_id, kind, storage_key)
    VALUES (${orgId}, ${runId}, 'screenshot', ${`s3://bucket/${slug}/1.png`})`
  await admin`
    INSERT INTO golden_versions (org_id, repository_id, version, verified)
    VALUES (${orgId}, ${repoId}, '2026-01-01T00-00-00Z', true)`
  await admin`
    INSERT INTO masking_rules (org_id, repository_id, table_name, column_name, transform)
    VALUES (${orgId}, ${repoId}, 'users', 'email', 'email')`
  await admin`
    INSERT INTO network_rules (org_id, repository_id, host, mode)
    VALUES (${orgId}, ${repoId}, 'api.stripe.com', 'sandbox')`
  await admin`
    INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
    VALUES (${orgId}, ${Math.floor(Math.random() * 1e12)}, ${slug}, 'Organization')`
  await admin`
    INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
    VALUES (${orgId}, 'ci', ${tokenHash(slug)}, ${'aft_' + slug.slice(0, 6)})`
  await admin`
    INSERT INTO events (org_id, idempotency_key, env_id, environment_id, type, occurred_at, sequence)
    VALUES (${orgId}, ${`ev-${slug}`}, ${`env-${slug}`}, ${envId}, 'environment.ready', now(), 1)`
  await admin`
    INSERT INTO audit_entries (org_id, actor_label, action, target_type, origin, entry_hash)
    VALUES (${orgId}, ${label}, 'environment.created', 'environment', 'web', ${'seed-' + slug})`

  return { orgId, userId, repoId, envId, runId, slug }
}

export function tokenHash(value: string): Buffer {
  return createHash('sha256').update(value).digest()
}

/** Removes a tenant and everything cascading from it. */
export async function dropTenant(admin: postgres.Sql, orgId: string): Promise<void> {
  await admin`DELETE FROM audit_entries WHERE org_id = ${orgId}`
  await admin`DELETE FROM organizations WHERE id = ${orgId}`
}

/**
 * The Postgres error underneath whatever the query builder wrapped it in.
 *
 * Drizzle reports a failure as "Failed query: <sql>" and hangs the driver's
 * error off cause. Asserting on that outer message would pass for any failure
 * at all, including a typo in the test's own SQL, so every assertion about a
 * refusal goes through here and checks the SQLSTATE. 42501 is
 * insufficient_privilege, which is what both a row-level security violation
 * and an ownership check produce.
 */
export function pgError(err: unknown): { code?: string; message: string } {
  let cur: unknown = err
  for (let depth = 0; depth < 8 && cur; depth += 1) {
    const e = cur as { code?: string; message?: string; cause?: unknown }
    if (typeof e.code === 'string' && /^[0-9A-Z]{5}$/.test(e.code)) {
      return { code: e.code, message: e.message ?? '' }
    }
    cur = e.cause
  }
  return { message: err instanceof Error ? err.message : String(err) }
}
