#!/usr/bin/env node
// Filling a LOCAL preview database so the operator portal has pages to render.
//
// WHY THIS EXISTS. The console is a static export with no server of its own and
// every screen in it is a fetch against the control plane. Served on its own it
// renders its error state, and the operator portal on top of that cannot be
// signed into at all: admin_users rows are written only by admin.operators.create,
// which itself needs an operator session, so a fresh database has nobody who can
// make the first operator. That is correct for a shipped installation, whose
// password is provisioned at deploy, and it is a wall for anybody trying to look
// at the pixels. This script is the local way through it.
//
// WHAT IT IS NOT. It is not a bootstrap command and nothing here belongs to the
// product. It writes a fixed, committed, obviously local password, and it
// refuses to run against a host that is not this machine, so the credential
// below cannot be pointed at anything real. If you want the operator bootstrap
// a real deployment needs, this is not it and must never be mistaken for it.
//
// WHAT IT SEEDS. Rows, in the real tables, through the real schema. Never
// numbers typed into a page. seedStaging already builds the tenant side of the
// product, so it is reused rather than reimplemented, and this file adds the
// three things it does not know about: the operators, the platform's feature
// flags, which are also its kill switches, and enough extra organizations that
// a list runs past its first page and paging can be seen working.

import { createRequire } from 'node:module'
import { fileURLToPath, pathToFileURL } from 'node:url'
import path from 'node:path'

const here = path.dirname(fileURLToPath(import.meta.url))
const repoRoot = path.resolve(here, '..', '..')
const webDir = path.join(repoRoot, 'web')

// `postgres` is resolved out of the web workspace rather than imported by name.
// This file lives under tools/, which has no node_modules above it, so a plain
// `import postgres from 'postgres'` cannot resolve from here however the
// workspace was installed. Everything else this script imports lives inside
// web/ and resolves its own dependencies from its own directory, which is why
// only this one line needs the indirection.
const requireFrom = createRequire(import.meta.url)
const postgres = (
  await import(pathToFileURL(requireFrom.resolve('postgres', { paths: [webDir] })).href)
).default as typeof import('postgres').default

const { migrate } = await import(path.join(webDir, 'packages/db/src/migrate.ts'))
const { resetStaging, seedStaging } = await import(path.join(webDir, 'packages/db/src/staging.ts'))
const { createAdminPool } = await import(path.join(webDir, 'packages/db/src/admin-pool.ts'))
const { appendAdminAudit } = await import(path.join(webDir, 'packages/db/src/admin-audit.ts'))
const { hashPassword } = await import(path.join(webDir, 'apps/api/src/admin/session.ts'))

// ---------------------------------------------------------------------------
// The local credential.
//
// Committed on purpose and printed on purpose. A password nobody can read out
// of the script is a password every agent running this has to be told
// separately, and the guard below is what makes writing it down safe: this
// script will not touch a database that is not on this machine.
// ---------------------------------------------------------------------------
export const PREVIEW_OPERATOR_EMAIL = 'operator@preview.local'
export const PREVIEW_OPERATOR_PASSWORD = 'preview-only-not-a-secret'

/** Hosts this script will write to. Anything else is refused by name. */
const LOCAL_HOSTS = new Set(['localhost', '127.0.0.1', '::1', '[::1]', '0.0.0.0'])

/**
 * Refuses a database that is not on this machine.
 *
 * A hostname check rather than a flag somebody remembers to pass, because the
 * thing being prevented is a mistake and not an attack: this script truncates
 * every tenant table and then writes a published password into admin_users. A
 * connection string pasted from a deployment is the whole failure mode, and it
 * would look exactly like a successful preview until somebody signed in.
 */
function refuseRemote(url: string): void {
  const host = new URL(url).hostname
  if (LOCAL_HOSTS.has(host)) return
  console.error(
    `tools/preview refuses to seed ${host}: it truncates every tenant table and writes a ` +
      'password that is published in this repository. It runs against localhost only.',
  )
  process.exit(2)
}

function required(name: string): string {
  const value = process.env[name]
  if (!value) {
    console.error(`${name} is not set. tools/preview/up.sh sets it; run that instead.`)
    process.exit(2)
  }
  return value
}

const adminUrl = required('AF_PREVIEW_DATABASE_URL')
refuseRemote(adminUrl)

const appUrl = (() => {
  const u = new URL(adminUrl)
  u.username = 'antifailure_app'
  u.password = 'app-test-password'
  return u.toString()
})()

const operatorUrl = (() => {
  const u = new URL(adminUrl)
  u.username = 'antifailure_admin'
  u.password = 'admin-test-password'
  return u.toString()
})()

const scale = Number(process.env.AF_PREVIEW_SCALE ?? 3)

/** The raw client this script writes through, named once so the helpers below
 *  can say what they take. */
type Sql = ReturnType<typeof postgres>

// Roles, operators and organization names. All of them fixed rather than
// random, so two runs of this script produce the same portal and a screenshot
// taken today can be compared with one taken tomorrow.
const OPERATORS: { email: string; name: string; role: string; suspended?: boolean; unprovisioned?: boolean }[] = [
  { email: PREVIEW_OPERATOR_EMAIL, name: 'Preview Operator', role: 'owner' },
  { email: 'rhea.okafor@preview.local', name: 'Rhea Okafor', role: 'super_admin' },
  { email: 'tomas.lindqvist@preview.local', name: 'Tomas Lindqvist', role: 'infrastructure' },
  { email: 'nadia.haddad@preview.local', name: 'Nadia Haddad', role: 'security' },
  { email: 'yusuf.demir@preview.local', name: 'Yusuf Demir', role: 'billing' },
  { email: 'clara.mendes@preview.local', name: 'Clara Mendes', role: 'support' },
  { email: 'ines.duarte@preview.local', name: 'Ines Duarte', role: 'support' },
  { email: 'peter.novak@preview.local', name: 'Peter Novak', role: 'analytics' },
  { email: 'mira.solberg@preview.local', name: 'Mira Solberg', role: 'read_only' },
  { email: 'oskar.wojcik@preview.local', name: 'Oskar Wojcik', role: 'support', suspended: true },
  { email: 'lena.fischer@preview.local', name: 'Lena Fischer', role: 'security', unprovisioned: true },
]

const FLAGS: {
  key: string
  description: string
  state: 'off' | 'on' | 'targeted'
  rollout: number
  internalOnly?: boolean
  killed?: string
}[] = [
  { key: 'billing.admin_writes', description: 'Operators may refund, credit and change a plan by hand.', state: 'on', rollout: 0 },
  { key: 'runs.new', description: 'New runs are accepted. Turning this off is the run kill switch.', state: 'on', rollout: 0 },
  { key: 'signups.open', description: 'A new organization may be created.', state: 'on', rollout: 0 },
  { key: 'maintenance.mode', description: 'Every write is refused and the console says why.', state: 'off', rollout: 0 },
  { key: 'twins.parallel-restore', description: 'Restore a safe state onto two twins at once.', state: 'targeted', rollout: 25 },
  { key: 'console.admin-portal', description: 'The operator portal is reachable at all.', state: 'targeted', rollout: 100, internalOnly: true },
  { key: 'masking.inline-preview', description: 'Show the masked value beside the rule that produced it.', state: 'targeted', rollout: 10 },
  { key: 'network.egress-audit', description: 'Record every refused egress attempt, not only the sampled ones.', state: 'on', rollout: 0 },
  { key: 'runner.model-proxy-v2', description: 'The budgeted model proxy, second implementation.', state: 'off', rollout: 0, killed: 'Latency regression on the first afternoon it was on.' },
  { key: 'reports.weekly-digest', description: 'Send an organization a weekly summary of its runs.', state: 'targeted', rollout: 50 },
]

// The extra tenants, so the customers list runs past its first page. Names
// rather than numbers, because a table of "org-41" proves paging works and
// proves nothing about how the page reads.
const EXTRA_ORG_WORDS = [
  'harbour', 'lantern', 'meridian', 'juniper', 'quarry', 'thistle', 'cobalt',
  'bramble', 'ferrite', 'kestrel', 'saltire', 'vellum', 'wrenfield', 'orchard',
  'pennant', 'basalt', 'canopy', 'driftwood', 'emberly', 'foxglove', 'granite',
  'hollow', 'inkwell', 'jetty', 'keystone', 'lyre', 'marlow', 'nettle',
  'obsidian', 'plinth', 'quillon', 'ridgeway', 'sandpiper', 'tallow', 'umber',
  'vantage', 'willowby', 'yarrow', 'zephyr', 'alder', 'birchall', 'cinder',
  'dovetail', 'elmgate', 'fathom', 'gable', 'heron', 'ivory', 'jackdaw',
  'larkspur', 'moorland', 'northgate', 'oakhurst', 'palisade', 'quintain',
  'rookery', 'sable', 'tanager', 'verity', 'wolds',
]

const PLANS = ['free', 'team', 'enterprise']

function line(text: string): void {
  console.log(`  ${text}`)
}

async function main(): Promise<void> {
  const admin = postgres(adminUrl, { max: 1, connect_timeout: 30, onnotice: () => {} })
  try {
    console.log('schema')
    const migrated = await migrate(admin)
    line(
      migrated.applied.length > 0
        ? `applied ${migrated.applied.length} migrations`
        : `already current, ${migrated.alreadyApplied.length} migrations`,
    )

    // The two logins the control plane connects with. Migration 0001 creates
    // antifailure_app NOLOGIN and 0023 creates antifailure_admin the same way,
    // so that a real installation supplies its own passwords rather than
    // inheriting one from a public repository. This is the local installation
    // supplying them, and it uses the same values the test harness does so that
    // a preview database and a test database are interchangeable.
    await admin.unsafe(`ALTER ROLE antifailure_app LOGIN PASSWORD 'app-test-password'`)
    await admin.unsafe(`
      DO $$ BEGIN
        IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'antifailure_admin') THEN
          CREATE ROLE antifailure_admin NOLOGIN BYPASSRLS;
        ELSE
          ALTER ROLE antifailure_admin BYPASSRLS;
        END IF;
      END $$;
      ALTER ROLE antifailure_admin LOGIN PASSWORD 'admin-test-password';
    `)
    line('antifailure_app and antifailure_admin can log in')

    console.log('tenants')
    await resetStaging(admin)
    const counts = await seedStaging(admin, { appUrl, scale, log: line })
    line(
      `${counts.organizations} organizations, ${counts.users} users, ${counts.repositories} repositories, ` +
        `${counts.environments} environments, ${counts.runs} runs, ${counts.events} events`,
    )

    const extra = await seedExtraTenants(admin)
    line(`${extra} further organizations, so the customers list needs a second page`)

    console.log('operators')
    const operators = await seedOperators(admin)
    line(`${operators.length} operators, ${OPERATORS.filter((o) => o.suspended).length} suspended, ` +
      `${OPERATORS.filter((o) => o.unprovisioned).length} never provisioned`)

    console.log('platform')
    await seedFlags(admin)
    line(`${FLAGS.length} feature flags, which are also this installation's kill switches`)

    const switched = await seedPlatformControls(admin)
    line(
      switched === null
        ? 'no platform_controls table on this branch, so no switch was engaged'
        : `${switched} platform control engaged, so the incidents page has a live one to show`,
    )

    const audited = await seedAdminAudit(operators)
    line(`${audited} operator audit entries, appended through the real chain`)
  } finally {
    await admin.end({ timeout: 5 })
  }

  console.log('')
  console.log('Sign in to the operator portal with')
  console.log(`  email     ${PREVIEW_OPERATOR_EMAIL}`)
  console.log(`  password  ${PREVIEW_OPERATOR_PASSWORD}`)
}

/**
 * More organizations, each with people and environments.
 *
 * Members and environments rather than the organization row alone, because the
 * customers list renders a count of each and a column of zeroes reads as a bug
 * in the query rather than as a tenant that has not started yet.
 */
async function seedExtraTenants(admin: Sql): Promise<number> {
  const [repoRow] = await admin<{ id: string }[]>`SELECT id FROM repositories LIMIT 1`
  const goldenRows = await admin<{ version: string }[]>`SELECT version FROM golden_versions LIMIT 1`
  const golden = goldenRows[0]?.version ?? null

  let made = 0
  for (const [index, word] of EXTRA_ORG_WORDS.entries()) {
    const slug = `${word}-labs`
    const plan = PLANS[index % PLANS.length]!
    const suspended = index % 17 === 0 && index > 0
    const created = new Date(Date.UTC(2025, 8, 1) + index * 86_400_000 * 3)
    const [org] = await admin<{ id: string }[]>`
      INSERT INTO organizations (slug, name, github_login, plan, created_at, updated_at,
                                 suspended_at, suspended_reason)
      VALUES (${slug}, ${`${title(word)} Labs`}, ${slug}, ${plan}, ${created}, ${created},
              ${suspended ? created : null},
              ${suspended ? 'Payment failed three times and support could not reach anybody.' : null})
      ON CONFLICT (slug) DO NOTHING
      RETURNING id`
    if (!org) continue
    made += 1

    const people = 1 + (index % 4)
    for (let i = 0; i < people; i += 1) {
      const [user] = await admin<{ id: string }[]>`
        INSERT INTO users (github_id, github_login, email, name, avatar_url, created_at, updated_at)
        VALUES (${2_000_000 + index * 10 + i}, ${`${word}${i}`},
                ${`${word}.${i}@${word}labs.com`}, ${`${title(word)} Person ${i + 1}`},
                ${`https://avatars.githubusercontent.com/u/${2_000_000 + index * 10 + i}?v=4`},
                ${created}, ${created})
        ON CONFLICT (github_id) DO NOTHING
        RETURNING id`
      if (!user) continue
      await admin`
        INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
        VALUES (${org.id}, ${user.id}, ${i === 0 ? 'owner' : 'member'}, 'github', ${created}, ${created})`
    }

    // One repository and one environment each. Enough for the counts to be
    // real, and deliberately not a second full tenant: the three organizations
    // seedStaging builds are where the depth is, and sixty copies of that would
    // be a seeder nobody waits for.
    const [repo] = await admin<{ id: string }[]>`
      INSERT INTO repositories (org_id, full_name, default_branch, github_id, private, created_at, updated_at)
      VALUES (${org.id}, ${`${slug}/platform`}, 'main', ${3_000_000 + index}, true, ${created}, ${created})
      ON CONFLICT DO NOTHING
      RETURNING id`
    const repositoryId = repo?.id ?? repoRow?.id
    if (!repositoryId || !golden) continue

    const envState = index % 5 === 0 ? 'torn_down' : index % 3 === 0 ? 'sleeping' : 'running'
    const [owner] = await admin<{ id: string }[]>`
      SELECT user_id AS id FROM members WHERE org_id = ${org.id} LIMIT 1`
    if (!owner) continue
    await admin`
      INSERT INTO environments
        (org_id, repository_id, env_id, branch, pull_request, state, preview_url, runtime,
         golden_version, created_by, last_sequence, created_at, updated_at, expires_at, torn_down_at)
      VALUES (${org.id}, ${repositoryId}, ${`af-${word}-main-0`}, 'main', ${100 + index}, ${envState},
              ${envState === 'torn_down' ? null : `http://af-${word}-main-0.preview.antifailure.dev`},
              'local', ${golden}, ${owner.id}, ${index}, ${created}, ${created},
              ${new Date(created.getTime() + 7 * 86_400_000)},
              ${envState === 'torn_down' ? new Date(created.getTime() + 86_400_000) : null})
      ON CONFLICT DO NOTHING`
  }
  return made
}

/** The operators, one of whom is the account this preview is signed in as. */
async function seedOperators(
  admin: Sql,
): Promise<{ id: string; label: string; role: string }[]> {
  const made: { id: string; label: string; role: string }[] = []
  for (const [index, op] of OPERATORS.entries()) {
    // One hash, computed once and reused, because scrypt at this cost parameter
    // takes a noticeable fraction of a second and eleven of them is a pause
    // somebody will read as the script having hung.
    const creds = op.unprovisioned ? null : await hashPassword(PREVIEW_OPERATOR_PASSWORD)
    const signedIn = new Date(Date.now() - index * 3_600_000)
    const [row] = await admin<{ id: string }[]>`
      INSERT INTO admin_users
        (email, name, role, password_hash, password_salt, password_set_at,
         is_root, suspended_at, suspended_reason, last_signed_in_at, created_at, updated_at)
      VALUES (${op.email}, ${op.name}, ${op.role},
              ${creds?.hash ?? null}, ${creds?.salt ?? null}, ${creds ? signedIn : null},
              ${index === 0}, ${op.suspended ? signedIn : null},
              ${op.suspended ? 'Left the company. Suspended rather than deleted so the audit chain keeps its name.' : null},
              ${op.unprovisioned || op.suspended ? null : signedIn},
              ${signedIn}, ${signedIn})
      ON CONFLICT (email) DO UPDATE SET
        password_hash = EXCLUDED.password_hash,
        password_salt = EXCLUDED.password_salt,
        password_set_at = EXCLUDED.password_set_at
      RETURNING id`
    if (row) made.push({ id: row.id, label: op.name, role: op.role })
  }
  return made
}

/** The flags, and the targets that make a targeted one readable. */
async function seedFlags(admin: Sql): Promise<void> {
  const orgs = await admin<{ id: string; slug: string }[]>`
    SELECT id, slug FROM organizations ORDER BY created_at LIMIT 6`

  for (const flag of FLAGS) {
    const killedAt = flag.killed ? new Date(Date.now() - 4 * 86_400_000) : null
    await admin`
      INSERT INTO feature_flags
        (key, description, state, rollout_percent, internal_only,
         killed_at, killed_by_label, killed_reason, updated_by_label)
      VALUES (${flag.key}, ${flag.description}, ${flag.state}, ${flag.rollout},
              ${flag.internalOnly ?? false},
              ${killedAt}, ${flag.killed ? 'Rhea Okafor' : null}, ${flag.killed ?? null},
              ${'Rhea Okafor'})
      ON CONFLICT (key) DO UPDATE SET
        description = EXCLUDED.description,
        state = EXCLUDED.state,
        rollout_percent = EXCLUDED.rollout_percent`

    if (flag.state !== 'targeted') continue
    for (const [index, org] of orgs.slice(0, 3).entries()) {
      await admin`
        INSERT INTO feature_flag_targets (flag_key, kind, value, allow, org_id, reason, created_by_label)
        VALUES (${flag.key}, 'organization', ${org.slug}, ${index !== 2}, ${org.id},
                ${index === 2
                  ? 'Asked to be taken out of the rollout after a bad afternoon.'
                  : 'Asked for early access and is watching the run output with us.'},
                'Rhea Okafor')
        ON CONFLICT DO NOTHING`
    }
  }
}

/**
 * One engaged kill switch, when this branch has the table for them.
 *
 * Conditional on the table existing, because platform_controls arrives with the
 * operator shell and this seeder has to work on a branch that predates it. A
 * seeder that fails on half the branches is a seeder every lane works around.
 *
 * One engaged and the rest untouched, which is the shape the schema describes:
 * a row exists only once somebody has pulled a switch, so an installation with
 * no rows is the ordinary state and the page has to render both.
 */
async function seedPlatformControls(admin: Sql): Promise<number | null> {
  const [present] = await admin<{ ok: boolean }[]>`
    SELECT to_regclass('public.platform_controls') IS NOT NULL AS ok`
  if (!present?.ok) return null
  await admin`
    INSERT INTO platform_controls (name, engaged_at, reason, engaged_by, updated_at)
    VALUES ('signups', ${new Date(Date.now() - 5 * 3_600_000)},
            'Holding new sign ups while the onboarding queue drains.', 'Rhea Okafor', now())
    ON CONFLICT (name) DO UPDATE SET
      engaged_at = EXCLUDED.engaged_at,
      reason = EXCLUDED.reason,
      engaged_by = EXCLUDED.engaged_by`
  return 1
}

/**
 * Operator audit entries, appended through appendAdminAudit.
 *
 * Through the helper rather than by INSERT, for the reason the tenant seeder
 * gives about its own chain: rows written directly do not hash into the chain
 * the verifier later walks, so an audit page seeded by hand would render a log
 * that fails its own verification. A preview that shows a broken chain teaches
 * everybody looking at it the wrong thing.
 */
async function seedAdminAudit(
  operators: { id: string; label: string; role: string }[],
): Promise<number> {
  if (operators.length === 0) return 0
  const pool = createAdminPool({ url: operatorUrl, max: 2 })
  const actions: { action: string; targetType: string; severity: 'info' | 'notice' | 'high' | 'critical'; detail: Record<string, unknown> }[] = [
    { action: 'admin.signed_in', targetType: 'admin_user', severity: 'info', detail: { method: 'password' } },
    { action: 'admin.tenant.viewed', targetType: 'organization', severity: 'info', detail: {} },
    { action: 'admin.flag.state_changed', targetType: 'feature_flag', severity: 'notice', detail: { from: 'targeted', to: 'on' } },
    { action: 'admin.tenant.suspended', targetType: 'organization', severity: 'high', detail: { reason: 'payment failed' } },
    { action: 'admin.operator.created', targetType: 'admin_user', severity: 'high', detail: { role: 'support' } },
    { action: 'admin.impersonation.started', targetType: 'user', severity: 'critical', detail: { ticket: 'SUP-4821' } },
    { action: 'admin.flag.killed', targetType: 'feature_flag', severity: 'critical', detail: { key: 'runner.model-proxy-v2' } },
    { action: 'admin.session.revoked', targetType: 'session', severity: 'notice', detail: {} },
  ]

  let written = 0
  try {
    const orgs = await pool.sql<{ id: string; slug: string }[]>`
      SELECT id, slug FROM organizations ORDER BY created_at LIMIT 8`
    // Sixty entries so the audit page pages, spread over the operators and over
    // three weeks, because an audit log where every row shares a timestamp
    // cannot show whether its ordering is right.
    for (let i = 0; i < 60; i += 1) {
      const operator = operators[i % operators.length]!
      const act = actions[i % actions.length]!
      const org = orgs.length > 0 ? orgs[i % orgs.length]! : null
      await pool.withOperator({ adminUserId: operator.id, label: operator.label }, (db) =>
        appendAdminAudit(db, {
          adminUserId: operator.id,
          actorLabel: operator.label,
          action: act.action,
          targetType: act.targetType,
          targetId: act.targetType === 'organization' ? (org?.id ?? null) : null,
          origin: 'admin',
          subjectOrgId: org?.id ?? null,
          subjectOrgLabel: org?.slug ?? null,
          severity: act.severity,
          detail: act.detail,
          occurredAt: new Date(Date.now() - (60 - i) * 8 * 3_600_000),
        }),
      )
      written += 1
    }
  } finally {
    await pool.close()
  }
  return written
}

function title(word: string): string {
  return word.charAt(0).toUpperCase() + word.slice(1)
}

try {
  await main()
} catch (err) {
  console.error(err instanceof Error ? (err.stack ?? err.message) : String(err))
  process.exit(1)
}
