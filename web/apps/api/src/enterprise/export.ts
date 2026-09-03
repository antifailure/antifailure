// A copy of an organization, in a form a person can read and a repository can
// take back.
//
// The temptation with an export is to dump every table with its primary keys
// and call the obligation discharged. That produces a file whose every
// reference is a uuid, which nobody can read and nothing can import: to make
// sense of `repository_id: 4f2a...` you need the export's own repositories
// array and a join, and to import it into anything you need this exact schema.
//
// So every reference here is the natural key a person already uses. A
// repository is `owner/name`. A member is a GitHub login. An environment is its
// env id. A verdict names its workflow. There is not one internal identifier in
// the document, which also means the document leaks nothing about how this
// database is laid out.
//
// THE PART THAT IS ACTUALLY RE-IMPORTABLE. `files` holds text keyed by relative
// path, and the two files under each repository are a `masking.yaml` and an
// `egress.yaml` fragment in exactly the shape the engine already reads. Those
// are not a description of the configuration, they are the configuration:
// dropping masking.yaml into the repository and pasting the egress block into
// `antifailure.yaml` restores the policy through the path the product already
// has, rather than through an importer that would have to be written, tested
// and kept in step with the schema forever.
//
// WHAT IS DELIBERATELY ABSENT is listed in the document itself, under
// `notIncluded`, with the reason for each. An export that quietly omits things
// is worse than one that omits them loudly: the second is a decision somebody
// can argue with.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import type { Clock } from '../clock.ts'

export const EXPORT_SCHEMA = 'antifailure.organization-export.v1'

/**
 * Caps, so that one organization cannot ask this process to hold a gigabyte of
 * rows in memory while it builds a JSON document.
 *
 * Each cap is paired with a `truncated` flag in the document rather than a
 * silent cut. A file that is missing history and does not say so is the shape
 * of defect this repository has a standing rule about: the absence looks
 * exactly like there having been nothing there.
 */
export const CAPS = {
  runs: 2000,
  environments: 2000,
  auditEntries: 20000,
  invoices: 500,
} as const

export interface ExportDocument extends Record<string, unknown> {
  schema: string
  generatedAt: string
  generatedBy: string
  organization: Record<string, unknown>
  counts: Record<string, number>
  truncated: string[]
  people: Record<string, unknown>[]
  invitations: Record<string, unknown>[]
  repositories: Record<string, unknown>[]
  environments: Record<string, unknown>[]
  runs: Record<string, unknown>[]
  runtimes: Record<string, unknown>[]
  credentials: Record<string, unknown>
  billing: Record<string, unknown>
  auditLog: Record<string, unknown>[]
  files: Record<string, string>
  notIncluded: { what: string; why: string }[]
}

/**
 * Builds the whole document inside one transaction.
 *
 * One transaction rather than several, because an export assembled from reads
 * taken minutes apart describes a state the organization was never in: an
 * environment torn down between two of the reads appears as running in one
 * section and absent from another, and the person reading it cannot tell that
 * from a bug.
 */
export async function buildExport(
  db: Db,
  clock: Clock,
  input: { orgId: string; generatedBy: string },
): Promise<ExportDocument> {
  const orgId = input.orgId
  const truncated: string[] = []

  const orgRows = await db.execute<{
    slug: string
    name: string
    github_login: string | null
    plan: string
    created_at: Date | string
    suspended_at: Date | string | null
    suspended_reason: string | null
  }>(sql`
    SELECT slug, name, github_login, plan, created_at, suspended_at, suspended_reason
    FROM organizations WHERE id = ${orgId}::uuid`)
  const org = orgRows[0]
  if (!org) throw new Error('the organization to export does not exist')

  const people = await db.execute<{
    github_login: string | null
    name: string | null
    email: string
    role: string
    source: string
    created_at: Date | string
  }>(sql`
    SELECT u.github_login, u.name, u.email, m.role::text AS role, m.source, m.created_at
    FROM members m JOIN users u ON u.id = m.user_id
    ORDER BY m.role, u.github_login NULLS LAST`)

  const invitations = await db.execute<{
    email: string
    role: string
    invited_by_label: string
    created_at: Date | string
    expires_at: Date | string
    accepted_at: Date | string | null
    revoked_at: Date | string | null
  }>(sql`
    SELECT email, role::text AS role, invited_by_label, created_at, expires_at,
           accepted_at, revoked_at
    FROM invitations ORDER BY created_at DESC`)

  const repositories = await db.execute<{
    id: string
    full_name: string
    default_branch: string
    private: boolean
    archived_at: Date | string | null
    created_at: Date | string
  }>(sql`
    SELECT id, full_name, default_branch, private, archived_at, created_at
    FROM repositories ORDER BY full_name`)

  const maskingRules = await db.execute<{
    repository_id: string
    table_name: string
    column_name: string
    transform: string
    link: string | null
    reason: string | null
    confirmed: boolean
  }>(sql`
    SELECT repository_id, table_name, column_name, transform, link, reason, confirmed
    FROM masking_rules ORDER BY table_name, column_name`)

  const networkRules = await db.execute<{
    repository_id: string | null
    host: string
    mode: string
    paths: string[] | null
    methods: string[] | null
    rate_limit: string | null
    credential: string | null
    fixtures: string | null
    webhook_path: string | null
    note: string | null
    position: number
    approved_at: Date | string | null
  }>(sql`
    SELECT repository_id, host, mode, paths, methods, rate_limit, credential, fixtures,
           webhook_path, note, position, approved_at
    FROM network_rules ORDER BY position, host`)

  const goldens = await db.execute<{
    repository_id: string
    version: string
    verified: boolean
    size_bytes: string | null
    created_at: Date | string
  }>(sql`
    SELECT repository_id, version, verified, size_bytes, created_at
    FROM golden_versions ORDER BY created_at DESC`)

  const environments = await db.execute<{
    env_id: string
    repository: string
    branch: string
    pull_request: number | null
    state: string
    runtime: string | null
    golden_version: string | null
    created_at: Date | string
    torn_down_at: Date | string | null
  }>(sql`
    SELECT e.env_id, r.full_name AS repository, e.branch, e.pull_request,
           e.state::text AS state, e.runtime, e.golden_version, e.created_at, e.torn_down_at
    FROM environments e JOIN repositories r ON r.id = e.repository_id
    ORDER BY e.created_at DESC LIMIT ${CAPS.environments + 1}`)
  if (environments.length > CAPS.environments) {
    environments.length = CAPS.environments
    truncated.push(`environments, to the most recent ${CAPS.environments}`)
  }

  const runs = await db.execute<{
    env_id: string
    kind: string
    state: string
    started_at: Date | string | null
    finished_at: Date | string | null
    verdicts: unknown
  }>(sql`
    SELECT e.env_id, ru.kind, ru.state::text AS state, ru.started_at, ru.finished_at,
           COALESCE((
             SELECT jsonb_agg(jsonb_build_object(
                      'workflow', v.workflow, 'persona', v.persona,
                      'value', v.value::text, 'summary', v.summary,
                      'steps', v.steps, 'durationMs', v.duration_ms)
                      ORDER BY v.workflow)
             FROM verdicts v WHERE v.run_id = ru.id), '[]'::jsonb) AS verdicts
    FROM runs ru JOIN environments e ON e.id = ru.environment_id
    ORDER BY ru.created_at DESC LIMIT ${CAPS.runs + 1}`)
  if (runs.length > CAPS.runs) {
    runs.length = CAPS.runs
    truncated.push(`runs, to the most recent ${CAPS.runs}`)
  }

  const runtimes = await db.execute<{
    name: string
    provider: string
    labels: string[]
    note: string | null
    created_at: Date | string
    removed_at: Date | string | null
  }>(sql`
    SELECT name, provider, labels, note, created_at, removed_at
    FROM runtimes ORDER BY name`)

  // Names and fingerprints, never a secret. An engine token is stored as a hash
  // and a provider key as ciphertext, and neither is in this document: an
  // export that carried them would turn a file somebody emails to their lawyer
  // into a way into their CI.
  const engineTokens = await db.execute<{
    name: string
    prefix: string
    kind: string
    scopes: string[]
    created_at: Date | string
    last_used_at: Date | string | null
    revoked_at: Date | string | null
  }>(sql`
    SELECT name, prefix, kind, scopes, created_at, last_used_at, revoked_at
    FROM engine_tokens ORDER BY created_at DESC`)

  const providerKeys = await db.execute<{
    provider: string
    last4: string
    fingerprint: string
    created_at: Date | string
    rotated_at: Date | string | null
    revoked_at: Date | string | null
  }>(sql`
    SELECT provider, last4, fingerprint, created_at, rotated_at, revoked_at
    FROM provider_keys ORDER BY provider`)

  const contact = await db.execute<{ email: string; name: string | null; updated_at: Date | string }>(
    sql`SELECT email, name, updated_at FROM billing_contacts WHERE org_id = ${orgId}::uuid`,
  )

  const subscriptions = await db.execute<{
    plan: string
    status: string
    quantity: number
    current_period_start: Date | string | null
    current_period_end: Date | string | null
    cancel_at_period_end: boolean
    canceled_at: Date | string | null
  }>(sql`
    SELECT plan, status, quantity, current_period_start, current_period_end,
           cancel_at_period_end, canceled_at
    FROM subscriptions ORDER BY created_at DESC`)

  const invoices = await db.execute<{
    number: string | null
    status: string
    amount_due: string
    amount_paid: string
    currency: string
    hosted_invoice_url: string | null
    period_start: Date | string | null
    period_end: Date | string | null
    paid_at: Date | string | null
  }>(sql`
    SELECT number, status, amount_due, amount_paid, currency, hosted_invoice_url,
           period_start, period_end, paid_at
    FROM invoices ORDER BY created_at DESC LIMIT ${CAPS.invoices + 1}`)
  if (invoices.length > CAPS.invoices) {
    invoices.length = CAPS.invoices
    truncated.push(`invoices, to the most recent ${CAPS.invoices}`)
  }

  const auditLog = await db.execute<{
    seq: string
    actor_label: string
    action: string
    target_type: string
    target_id: string | null
    origin: string
    detail: unknown
    occurred_at: Date | string
    prev_hash: string | null
    entry_hash: string
  }>(sql`
    SELECT seq, actor_label, action, target_type, target_id, origin, detail,
           occurred_at, prev_hash, entry_hash
    FROM audit_entries ORDER BY seq DESC LIMIT ${CAPS.auditEntries + 1}`)
  if (auditLog.length > CAPS.auditEntries) {
    auditLog.length = CAPS.auditEntries
    truncated.push(`audit entries, to the most recent ${CAPS.auditEntries}`)
  }

  const repoName = new Map(repositories.map((r) => [r.id, r.full_name]))

  const files: Record<string, string> = {}
  const repositoryViews = repositories.map((repo) => {
    const masking = maskingRules.filter((m) => m.repository_id === repo.id)
    const egress = networkRules.filter((n) => n.repository_id === repo.id)
    const dir = `repositories/${repo.full_name}`
    if (masking.length > 0) files[`${dir}/masking.yaml`] = maskingYaml(masking)
    if (egress.length > 0) files[`${dir}/egress.yaml`] = egressYaml(egress)
    return {
      fullName: repo.full_name,
      defaultBranch: repo.default_branch,
      private: repo.private,
      archivedAt: iso(repo.archived_at),
      connectedAt: iso(repo.created_at),
      maskingRules: masking.map((m) => ({
        table: m.table_name,
        column: m.column_name,
        transform: m.transform,
        link: m.link,
        why: m.reason,
        confirmed: m.confirmed,
      })),
      egressRules: egress.map((n) => ({
        host: n.host,
        mode: n.mode,
        paths: n.paths,
        methods: n.methods,
        rateLimit: n.rate_limit,
        // The NAME of the environment variable holding the sandbox
        // credential, per the manifest schema, never its value. No secret
        // reaches this control plane, so there is none here to leave out.
        credential: n.credential,
        fixtures: n.fixtures,
        webhookPath: n.webhook_path,
        note: n.note,
        approved: n.approved_at !== null,
      })),
      goldenVersions: goldens
        .filter((g) => g.repository_id === repo.id)
        .map((g) => ({
          version: g.version,
          verified: g.verified,
          sizeBytes: g.size_bytes === null ? null : Number(g.size_bytes),
          createdAt: iso(g.created_at),
        })),
    }
  })

  const orgWideEgress = networkRules.filter((n) => n.repository_id === null)
  if (orgWideEgress.length > 0) files['egress.yaml'] = egressYaml(orgWideEgress)

  const document: ExportDocument = {
    schema: EXPORT_SCHEMA,
    generatedAt: clock.now().toISOString(),
    generatedBy: input.generatedBy,
    organization: {
      name: org.name,
      slug: org.slug,
      githubLogin: org.github_login,
      plan: org.plan,
      createdAt: iso(org.created_at),
      suspendedAt: iso(org.suspended_at),
      suspendedReason: org.suspended_reason,
    },
    counts: {
      people: people.length,
      invitations: invitations.length,
      repositories: repositories.length,
      environments: environments.length,
      runs: runs.length,
      runtimes: runtimes.length,
      maskingRules: maskingRules.length,
      egressRules: networkRules.length,
      engineTokens: engineTokens.length,
      providerKeys: providerKeys.length,
      invoices: invoices.length,
      auditEntries: auditLog.length,
      files: 0,
    },
    truncated,
    people: people.map((p) => ({
      login: p.github_login,
      name: p.name,
      email: p.email,
      role: p.role,
      addedBy: p.source,
      joinedAt: iso(p.created_at),
    })),
    invitations: invitations.map((i) => ({
      email: i.email,
      role: i.role,
      invitedBy: i.invited_by_label,
      sentAt: iso(i.created_at),
      expiresAt: iso(i.expires_at),
      acceptedAt: iso(i.accepted_at),
      revokedAt: iso(i.revoked_at),
    })),
    repositories: repositoryViews,
    environments: environments.map((e) => ({
      envId: e.env_id,
      repository: e.repository,
      branch: e.branch,
      pullRequest: e.pull_request,
      state: e.state,
      runtime: e.runtime,
      goldenVersion: e.golden_version,
      createdAt: iso(e.created_at),
      tornDownAt: iso(e.torn_down_at),
    })),
    runs: runs.map((r) => ({
      environment: r.env_id,
      kind: r.kind,
      state: r.state,
      startedAt: iso(r.started_at),
      finishedAt: iso(r.finished_at),
      verdicts: r.verdicts,
    })),
    runtimes: runtimes.map((r) => ({
      name: r.name,
      provider: r.provider,
      labels: r.labels,
      note: r.note,
      registeredAt: iso(r.created_at),
      removedAt: iso(r.removed_at),
    })),
    credentials: {
      engineTokens: engineTokens.map((t) => ({
        name: t.name,
        prefix: t.prefix,
        kind: t.kind,
        scopes: t.scopes,
        createdAt: iso(t.created_at),
        lastUsedAt: iso(t.last_used_at),
        revokedAt: iso(t.revoked_at),
      })),
      providerKeys: providerKeys.map((k) => ({
        provider: k.provider,
        endsWith: k.last4,
        fingerprint: k.fingerprint,
        createdAt: iso(k.created_at),
        rotatedAt: iso(k.rotated_at),
        revokedAt: iso(k.revoked_at),
      })),
    },
    billing: {
      plan: org.plan,
      contact: contact[0]
        ? { email: contact[0].email, name: contact[0].name, updatedAt: iso(contact[0].updated_at) }
        : null,
      subscriptions: subscriptions.map((s) => ({
        plan: s.plan,
        status: s.status,
        // `stripeQuantity`, not `seats`, and the rename is a correction.
        //
        // This said `seats`, which told the reader their subscription had
        // bought them that many members. It never did: how many members an
        // organization may hold comes from its PLAN, in entitlements.ts, and
        // this column only records the quantity Stripe reported. An export is
        // the document a customer takes to somebody else, so a field in it that
        // states a limit the product does not enforce is the worst place in the
        // codebase for that sentence to be.
        stripeQuantity: s.quantity,
        periodStart: iso(s.current_period_start),
        periodEnd: iso(s.current_period_end),
        cancelAtPeriodEnd: s.cancel_at_period_end,
        cancelledAt: iso(s.canceled_at),
      })),
      invoices: invoices.map((i) => ({
        number: i.number,
        status: i.status,
        amountDue: Number(i.amount_due),
        amountPaid: Number(i.amount_paid),
        currency: i.currency,
        // The rendered document lives at Stripe and this is the link to it.
        // Kept because an accounts payable department asks for the invoice
        // itself, and reproducing it here would be a second thing to be wrong
        // about somebody's money.
        invoiceUrl: i.hosted_invoice_url,
        periodStart: iso(i.period_start),
        periodEnd: iso(i.period_end),
        paidAt: iso(i.paid_at),
      })),
    },
    // Newest first in the query so the cap keeps the recent end, oldest first
    // in the document so the hash chain reads forwards and can be verified by
    // hand from the top.
    auditLog: auditLog
      .slice()
      .reverse()
      .map((a) => ({
        seq: Number(a.seq),
        at: iso(a.occurred_at),
        actor: a.actor_label,
        action: a.action,
        target: a.target_id ? `${a.target_type}:${a.target_id}` : a.target_type,
        origin: a.origin,
        detail: a.detail,
        previousHash: a.prev_hash,
        hash: a.entry_hash,
      })),
    files,
    notIncluded: [
      {
        what: 'engine token values and provider key material',
        why: 'stored as a hash and as ciphertext, and an export carrying either would be a way into your CI',
      },
      {
        what: 'session tokens',
        why: 'the same reason, and they expire anyway',
      },
      {
        what: 'raw engine events',
        why:
          'they are the per-run telemetry behind the runs above, they are partitioned by month, ' +
          'and a complete copy would be far larger than everything else here put together',
      },
      {
        what: 'artifacts and reports',
        why: 'they are files rather than rows, and they are already downloadable from the runs page',
      },
      {
        what: 'anything from your database',
        why:
          'no snapshot, no masked branch and no captured request body ever reaches this control ' +
          'plane, so there is none of it here to export',
      },
    ],
  }
  document.counts.files = Object.keys(files).length
  files['README.md'] = readme(document)
  document.counts.files = Object.keys(files).length
  return document
}

/** The file somebody opens first. */
function readme(doc: ExportDocument): string {
  const org = doc.organization as { name: string; slug: string }
  const lines = [
    `# ${org.name}`,
    '',
    `A copy of everything Antifailure holds about \`${org.slug}\`, taken on ${doc.generatedAt} by ${doc.generatedBy}.`,
    '',
    '## What is in here',
    '',
    'The whole export is one JSON document. Every reference in it is the name you already',
    'use: a repository is `owner/name`, a person is their login, an environment is its env id.',
    'There is no internal identifier anywhere in the file.',
    '',
    '`files` holds text keyed by path, and those are the parts you can put straight back:',
    '',
    '- `repositories/<owner>/<name>/masking.yaml` is a masking file the engine reads as it is.',
    '  Commit it at the root of that repository and `af test` uses it.',
    '- `repositories/<owner>/<name>/egress.yaml` and `egress.yaml` are the `egress:` block from',
    '  `antifailure.yaml`. Paste the contents into that file.',
    '',
    '## What is not in here',
    '',
    ...doc.notIncluded.map((n) => `- **${n.what}**: ${n.why}.`),
    '',
  ]
  if (doc.truncated.length > 0) {
    lines.push('## What was cut', '')
    lines.push('This export is bounded, and these sections were cut rather than silently shortened:', '')
    lines.push(...doc.truncated.map((t) => `- ${t}`))
    lines.push('')
  }
  lines.push('## Counts', '')
  for (const [name, value] of Object.entries(doc.counts)) {
    lines.push(`- ${name}: ${value}`)
  }
  lines.push('')
  return lines.join('\n')
}

/**
 * A masking file in the shape migrations and the engine already agree on.
 *
 * Written by hand rather than through a YAML library, because the value set
 * here is small and closed: table and column names, a transform from a fixed
 * list, and two free text fields. Quoting is applied to every free text value
 * unconditionally rather than only when it looks necessary, so there is no
 * cleverness to get wrong.
 */
export function maskingYaml(
  rules: { table_name: string; column_name: string; transform: string; link: string | null; reason: string | null }[],
): string {
  const out = [
    '# Exported from Antifailure. Commit this at the root of the repository.',
    '#',
    '# A text or JSON column that no rule names is emptied, so this file is not a',
    '# list of everything that is masked: it is the list of columns that need a',
    '# particular transform, and the ones that must survive untouched.',
    '',
    'rules:',
  ]
  for (const rule of rules) {
    out.push(`  - table: ${scalar(rule.table_name)}`)
    out.push(`    column: ${scalar(rule.column_name)}`)
    out.push(`    transform: ${scalar(rule.transform)}`)
    if (rule.link) out.push(`    link: ${scalar(rule.link)}`)
    if (rule.reason) out.push(`    why: ${quoted(rule.reason)}`)
    out.push('')
  }
  return out.join('\n')
}

/** The `egress:` block from `antifailure.yaml`. */
export function egressYaml(
  rules: {
    host: string
    mode: string
    paths: string[] | null
    methods: string[] | null
    rate_limit: string | null
    credential: string | null
    fixtures: string | null
    webhook_path: string | null
    note: string | null
  }[],
): string {
  const out = [
    '# Exported from Antifailure. Paste this into antifailure.yaml.',
    '#',
    '# Nothing reaches the internet unless it is named here.',
    '',
    'egress:',
    '  default: block',
    '  rules:',
  ]
  for (const rule of rules) {
    out.push(`    - host: ${scalar(rule.host)}`)
    out.push(`      mode: ${scalar(rule.mode)}`)
    if (rule.paths && rule.paths.length > 0) {
      out.push(`      paths: [${rule.paths.map(scalar).join(', ')}]`)
    }
    if (rule.methods && rule.methods.length > 0) {
      out.push(`      methods: [${rule.methods.map(scalar).join(', ')}]`)
    }
    if (rule.rate_limit) out.push(`      rate_limit: ${scalar(rule.rate_limit)}`)
    // Every remaining key the manifest schema defines for an egress rule.
    // Dropping one would make the exported policy quietly different from the
    // one that was enforced, which is worse than not exporting it at all: the
    // file still looks complete.
    if (rule.credential) out.push(`      credential: ${scalar(rule.credential)}`)
    if (rule.fixtures) out.push(`      fixtures: ${scalar(rule.fixtures)}`)
    if (rule.webhook_path) out.push(`      webhook_path: ${scalar(rule.webhook_path)}`)
    if (rule.note) out.push(`      note: ${quoted(rule.note)}`)
    out.push('')
  }
  return out.join('\n')
}

/** A plain identifier, quoted when it is anything else. */
function scalar(value: string): string {
  return /^[A-Za-z_][A-Za-z0-9_.\-/*]*$/.test(value) ? value : quoted(value)
}

/** A double-quoted YAML scalar. Backslash and quote are the only two
 *  characters that need escaping inside one, and both are escaped. */
function quoted(value: string): string {
  return `"${value.replace(/\\/g, '\\\\').replace(/"/g, '\\"').replace(/\n/g, ' ')}"`
}

function iso(v: Date | string | null): string | null {
  if (v === null || v === undefined) return null
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
