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

import { createHash, randomBytes, randomUUID } from 'node:crypto'
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

/** Seconds to wait for the probe. Three was right on an idle laptop and is
 *  wrong on a loaded one; see below. */
export const connectTimeoutSeconds = Number(process.env.AF_TEST_CONNECT_TIMEOUT ?? 30)

/**
 * Whether there is a database, and when "no" is an acceptable answer.
 *
 * The probe used to connect with a three second timeout and skip on failure.
 * That is correct for somebody without Docker and dangerous on a busy machine:
 * under load the PROBE times out, every suite in the file skips, and the run
 * exits 0 having proved nothing. Measured on this machine while eleven agents
 * were working: 8.3s, 29.9s, 2.0s for the same connection. So the failure the
 * skip exists to prevent arrives through the skip, and it arrives precisely
 * when tests are being run in bulk.
 *
 * That matters more here than anywhere else in the repository, because this is
 * the suite that proves one tenant cannot read another's rows. It is the single
 * test nobody can afford to have quietly optional.
 *
 * So: a generous timeout, three attempts, and two ways to say that "no
 * database" is not an acceptable answer. AF_REQUIRE_DATABASE=1 is the explicit
 * one and belongs in CI. Setting AF_TEST_DATABASE_URL is the implicit one:
 * naming a database is a statement that one is supposed to be there.
 */
export async function available(): Promise<boolean> {
  let last: unknown = null
  for (let attempt = 0; attempt < 3; attempt += 1) {
    try {
      const probe = postgres(adminUrl, {
        max: 1,
        connect_timeout: connectTimeoutSeconds,
        onnotice: () => {},
      })
      await probe`SELECT 1`
      await probe.end({ timeout: 5 })
      return true
    } catch (err) {
      last = err
    }
  }

  if (process.env.AF_REQUIRE_DATABASE === '1' || process.env.AF_TEST_DATABASE_URL) {
    throw new Error(
      `no database at ${adminUrl} after three attempts of ${connectTimeoutSeconds}s.\n` +
        `Refusing to skip: this suite proves cross-tenant isolation, and a green run that ` +
        `proved nothing is worse than a red one.\n` +
        `Underlying error: ${last instanceof Error ? last.message : String(last)}`,
    )
  }
  return false
}

export async function setup(): Promise<Harness> {
  if (cached) return cached

  // The same timeout as the probe. A probe that succeeds and a pool that then
  // times out on its first query is the same bug moved one line down.
  const admin = postgres(adminUrl, { max: 4, connect_timeout: connectTimeoutSeconds, onnotice: () => {} })
  await migrate(admin)
  // The role is created NOLOGIN by the migration, because a self-hosted
  // installation supplies its own credential. The suite gives it one.
  await admin.unsafe(
    `ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`,
  )

  const pool = createPool({ url: appUrl(), max: 5, connectTimeoutSeconds })

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
  /** The seeded single sign-on connection, for suites that need one. */
  connectionId: string
  /** The seeded Stripe customer, for the billing policies. */
  customerId: string
  /** The seeded workload definition, its first version, and one finished run. */
  workloadId: string
  workloadVersionId: string
  workloadRunId: string
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
    INSERT INTO runtimes (org_id, name, provider, labels)
    VALUES (${orgId}, 'default', 'local', ${admin.array(['seed'])})`
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

  // Single sign-on and provisioning. Every one of these tables carries org_id,
  // so the cross-tenant suite picks them up from the database and requires a
  // row per tenant to prove against: a query that returns nothing because the
  // fixture never inserted anything looks exactly like isolation working.
  const [connection] = await admin<{ id: string }[]>`
    INSERT INTO sso_connections (
      org_id, handle, kind, display_name, enabled, default_role,
      idp_entity_id, idp_sso_url, idp_certificates)
    VALUES (${orgId}, ${randomBytes(32).toString('base64url')}, 'saml',
            ${`${label} directory`}, true, 'member',
            ${`https://idp.${slug}.test/metadata`},
            ${`https://idp.${slug}.test/sso`},
            ${admin.array([`seed-certificate-${slug}`])})
    RETURNING id`
  const connectionId = connection!.id

  await admin`
    INSERT INTO sso_connection_secrets (connection_id, org_id, oidc_client_secret)
    VALUES (${connectionId}, ${orgId}, ${Buffer.from(`seed-secret-${slug}`)})`
  await admin`
    INSERT INTO sso_domains (org_id, connection_id, domain, verification_token, verified_at)
    VALUES (${orgId}, ${connectionId}, ${`${slug}.test`}, ${`token-${slug}`}, now())`
  await admin`
    INSERT INTO sso_login_states (state, org_id, connection_id, expires_at)
    VALUES (${`state-${slug}`}, ${orgId}, ${connectionId}, now() + interval '10 minutes')`
  await admin`
    INSERT INTO sso_assertions_seen (org_id, connection_id, assertion_id, expires_at)
    VALUES (${orgId}, ${connectionId}, ${`assertion-${slug}`}, now() + interval '10 minutes')`
  await admin`
    INSERT INTO sso_break_glass_codes (org_id, code_hash)
    VALUES (${orgId}, ${tokenHash(`break-glass-${slug}`)})`

  await admin`
    INSERT INTO scim_tokens (org_id, name, token_hash, prefix)
    VALUES (${orgId}, 'directory', ${tokenHash(`scim-${slug}`)}, ${'afs_' + slug.slice(0, 6)})`
  const [resource] = await admin<{ id: string }[]>`
    INSERT INTO scim_resources (org_id, user_id, external_id, user_name, display_name)
    VALUES (${orgId}, ${userId}, ${`ext-${slug}`}, ${`${slug}@example.test`}, ${label})
    RETURNING id`
  const [group] = await admin<{ id: string }[]>`
    INSERT INTO scim_groups (org_id, external_id, display_name, role)
    VALUES (${orgId}, ${`extgrp-${slug}`}, ${`${label} engineers`}, 'member')
    RETURNING id`
  await admin`
    INSERT INTO scim_group_members (org_id, group_id, member_ref, resource_id)
    VALUES (${orgId}, ${group!.id}, ${`ext-${slug}`}, ${resource!.id})`

  // A provider key and its budget. The ciphertext here is not a sealed key and
  // is not meant to be: this fixture exists so the cross-tenant suite has a row
  // of each to attack, and what it proves is that Postgres refuses another
  // tenant's row. Whether the bytes decrypt is src/providers/seal.ts's problem
  // and is tested there.
  await admin`
    INSERT INTO provider_keys (org_id, provider, ciphertext, nonce, fingerprint, last4)
    VALUES (${orgId}, 'anthropic', ${Buffer.from(`not-a-key-${slug}`)},
            ${Buffer.from('000000000000')}, ${'fp-' + slug.slice(0, 12)}, '0000')`
  await admin`
    INSERT INTO provider_budgets (org_id, provider, period, cap_usd, spent_usd)
    VALUES (${orgId}, 'anthropic', date_trunc('month', now())::date, 100, 0)`

  // Billing. Every one of these carries org_id, so the cross-tenant suite
  // picks them up from the database and needs a row per tenant to attack: a
  // query that returns nothing because the fixture never inserted anything
  // looks exactly like isolation working.
  const customerId = `cus_${slug.replace(/-/g, '')}`
  await admin`
    INSERT INTO billing_customers (org_id, stripe_customer_id, email)
    VALUES (${orgId}, ${customerId}, ${`billing@${slug}.test`})`
  await admin`
    INSERT INTO payment_methods (
      org_id, stripe_payment_method_id, stripe_customer_id, brand, last4,
      exp_month, exp_year, last_event_at)
    VALUES (${orgId}, ${`pm_${slug}`}, ${customerId}, 'visa', '4242', 12, 2034, now())`
  await admin`
    INSERT INTO subscriptions (
      org_id, stripe_subscription_id, stripe_customer_id, plan, price_id,
      status, current_period_start, current_period_end, last_event_at)
    VALUES (${orgId}, ${`sub_${slug}`}, ${customerId}, 'team', ${`price_${label}`},
            'active', now(), now() + interval '30 days', now())`
  await admin`
    INSERT INTO invoices (
      org_id, stripe_invoice_id, stripe_customer_id, stripe_subscription_id,
      number, status, amount_due, amount_paid, last_event_at)
    VALUES (${orgId}, ${`in_${slug}`}, ${customerId}, ${`sub_${slug}`},
            ${`AF-${slug.slice(0, 6)}`}, 'paid', 4900, 4900, now())`
  await admin`
    INSERT INTO billing_events (
      stripe_event_id, org_id, stripe_customer_id, type, event_created_at, outcome)
    VALUES (${`evt_${slug}`}, ${orgId}, ${customerId}, 'invoice.paid', now(), 'applied')`

  // Workload Studio. Every one of these carries org_id, so the cross-tenant
  // suite picks them up from the database and needs a row per tenant to attack:
  // a query that returns nothing because the fixture never inserted anything
  // looks exactly like isolation working.
  //
  // The shapes are not interchangeable. workload_run_results carries a CHECK
  // that refuses a result of one kind wearing another kind's columns, so this
  // fixture writes a browser_workflow result for a browser_workflow definition
  // and a change to either half fails here rather than in production.
  const [workload] = await admin<{ id: string }[]>`
    INSERT INTO workloads (org_id, repository_id, slug, name, kind, created_by)
    VALUES (${orgId}, ${repoId}, 'checkout', ${`${label} checkout`}, 'browser_workflow', ${userId})
    RETURNING id`
  const workloadId = workload!.id

  const [version] = await admin<{ id: string }[]>`
    INSERT INTO workload_versions (org_id, workload_id, version, body, body_digest, created_by)
    VALUES (${orgId}, ${workloadId}, 1,
            ${JSON.stringify({ workflows: ['sign-up'] })}::jsonb,
            ${tokenHash(`workload-${slug}`).toString('hex')}, ${userId})
    RETURNING id`
  const workloadVersionId = version!.id

  const [workloadRun] = await admin<{ id: string }[]>`
    INSERT INTO workload_runs (
      org_id, workload_id, workload_version_id, environment_id, state,
      requested_by, request_key, repository, git_ref, deadline_at, finished_at, verdict)
    VALUES (${orgId}, ${workloadId}, ${workloadVersionId}, ${envId}, 'succeeded',
            ${userId}, ${`req-${slug}`}, ${`${slug}/app`}, 'main',
            now() + interval '1 hour', now(), 'pass')
    RETURNING id`
  const workloadRunId = workloadRun!.id

  await admin`
    INSERT INTO workload_run_results (org_id, workload_run_id, kind, workflows, workflows_passed, workflows_failed, duration_ms)
    VALUES (${orgId}, ${workloadRunId}, 'browser_workflow', 1, 1, 0, 1200)`
  await admin`
    INSERT INTO workload_route_metrics (org_id, workload_run_id, route, sent, errors, p95_ms)
    VALUES (${orgId}, ${workloadRunId}, 'GET /checkout', 10, 0, 42)`
  await admin`
    INSERT INTO workload_threshold_verdicts (org_id, workload_run_id, name, measure, threshold, observed, value)
    VALUES (${orgId}, ${workloadRunId}, 'checkout is quick', 'p95_below_ms', 200, 42, 'pass')`
  await admin`
    INSERT INTO workload_evidence (org_id, workload_run_id, kind, availability, locator)
    VALUES (${orgId}, ${workloadRunId}, 'trace', 'runner_local', ${`/home/runner/${slug}/trace.zip`})`
  await admin`
    INSERT INTO runtime_commands (org_id, kind, environment_id, expires_at, requested_by)
    VALUES (${orgId}, 'environment.teardown', ${envId}, now() + interval '1 hour', ${userId})`

  // The pull request lifecycle. Every one of these carries org_id, so the
  // cross-tenant suite finds them in the database and needs a row per tenant to
  // attack: a query returning nothing because the fixture never inserted
  // anything looks exactly like isolation working.
  const headSha = `${'0'.repeat(33)}${slug.replace(/[^0-9a-f]/g, '0').slice(0, 7)}`
  const [pullRequest] = await admin<{ id: string }[]>`
    INSERT INTO pull_requests (
      org_id, repository_id, number, head_sha, head_ref, base_ref, head_repository)
    VALUES (${orgId}, ${repoId}, 1, ${headSha}, 'feature', 'main', ${`${slug}/app`})
    RETURNING id`
  // OVERDUE, deliberately. The sweeper policies admit a row only when it is
  // already past its deadline, so a fixture whose generation is not overdue
  // cannot see whether those policies leak across tenants: the row condition
  // would be false and the cross-tenant read would come back empty for the
  // wrong reason. A fixture that does not satisfy a policy's predicate proves
  // nothing about that policy.
  const [generation] = await admin<{ id: string }[]>`
    INSERT INTO pr_generations (org_id, pull_request_id, head_sha, deadline_at)
    VALUES (${orgId}, ${pullRequest!.id}, ${headSha}, now() - interval '1 minute')
    RETURNING id`
  await admin`
    INSERT INTO teardown_requests (org_id, environment_id, env_id, repository_id, generation_id, reason)
    VALUES (${orgId}, ${envId}, ${`env-${slug}`}, ${repoId}, ${generation!.id}, 'seed')`
  await admin`
    INSERT INTO github_deliveries (delivery_id, org_id, account_login, event, action)
    VALUES (${`delivery-${slug}`}, ${orgId}, ${slug}, 'pull_request', 'opened')`

  return {
    orgId, userId, repoId, envId, runId, slug, connectionId, customerId,
    workloadId, workloadVersionId, workloadRunId,
  }
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
