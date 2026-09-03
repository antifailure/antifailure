// The side effect firewall, from the side that can see every tenant at once.
//
// The per-organization console already explains one organization's egress
// policy and does it well. This is not a second copy of that. It answers the
// question the per-organization view structurally cannot: across every
// organization on this installation, which rules are configured in a way that
// cannot do what they claim, and how long have they been that way.
//
// One of those conditions is worth the whole file.
//
// A rule in sandbox mode promises that the credential leaving the environment
// is a sandbox credential, substituted at the boundary so the application never
// holds one. The substitution is `applySandbox` in engine/cmd/af-proxy, and its
// first statement is:
//
//     if credential == "" { return }
//
// So a sandbox rule with no credential configured forwards the request with
// whatever header the application set, unchanged, to the real provider. The
// proxy's live credential tripwire catches the shapes it recognises and
// refuses those. Anything it does not recognise goes out. In the request log
// that call is indistinguishable from a working sandbox call in every column
// except one: `substituted` is false.
//
// There is no tolerable quantity of that, so it is not scored against a
// threshold and it is not sorted by count. Every instance is failing, from the
// first one, forever.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

/** The modes that substitute a credential at the boundary. */
const SUBSTITUTING_MODES = ['sandbox'] as const

export interface RuleRow {
  id: string
  orgId: string
  orgSlug: string
  /** Null for a rule that applies to every repository in the organization. */
  repository: string | null
  host: string
  mode: string
  paths: string[] | null
  methods: string[] | null
  credential: string | null
  note: string | null
  position: number
  proposedBy: string | null
  approvedBy: string | null
  approvedAt: Date | string | null
  createdAt: Date | string
  updatedAt: Date | string
}

/** Why a rule is being surfaced, and what it means. */
export type FindingKind =
  /** Sandbox mode with nothing to substitute. The live credential goes out. */
  | 'sandbox-without-credential'
  /** Proposed and never decided. Inert, so the host is falling through to the
   *  default, which is usually not what the proposer intended. */
  | 'never-approved'
  /** Allow mode on a rule nobody has approved, or approved long ago and never
   *  revisited. An allow is the one mode that lets a side effect reach a real
   *  third party. */
  | 'allow'

export interface Finding {
  kind: FindingKind
  rule: RuleRow
  /** What is actually happening, in the words an operator reads. */
  says: string
  /** Whether this can ever be acceptable. A finding that is always wrong is
   *  never scored, filtered by count, or hidden behind a threshold. */
  severity: 'failing' | 'review'
}

const SELECT_RULES = sql`
  SELECT n.id, n.org_id, o.slug AS org_slug, r.full_name AS repository,
         n.host, n.mode, n.paths, n.methods, n.credential, n.note, n.position,
         pu.github_login AS proposed_by, au.github_login AS approved_by,
         n.approved_at, n.created_at, n.updated_at
  FROM network_rules n
  JOIN organizations o ON o.id = n.org_id
  LEFT JOIN repositories r ON r.id = n.repository_id
  LEFT JOIN users pu ON pu.id = n.proposed_by
  LEFT JOIN users au ON au.id = n.approved_by`

interface Raw extends Record<string, unknown> {
  id: string
  org_id: string
  org_slug: string
  repository: string | null
  host: string
  mode: string
  paths: string[] | null
  methods: string[] | null
  credential: string | null
  note: string | null
  position: number
  proposed_by: string | null
  approved_by: string | null
  approved_at: Date | string | null
  created_at: Date | string
  updated_at: Date | string
}

function toRule(r: Raw): RuleRow {
  return {
    id: r.id,
    orgId: r.org_id,
    orgSlug: r.org_slug,
    repository: r.repository,
    host: r.host,
    mode: r.mode,
    paths: r.paths,
    methods: r.methods,
    credential: r.credential,
    note: r.note,
    position: r.position,
    proposedBy: r.proposed_by,
    approvedBy: r.approved_by,
    approvedAt: r.approved_at,
    createdAt: r.created_at,
    updatedAt: r.updated_at,
  }
}

/**
 * Whether a rule promises a substitution it cannot perform.
 *
 * Exported on its own because it is the one predicate in this file that has to
 * be exactly right, and a predicate buried inside a query is a predicate no
 * test can call. Empty string and null are the same condition: `applySandbox`
 * compares against the empty string, and a column that was set to '' is as
 * unsubstitutable as one that was never set.
 */
export function forwardsTheLiveCredential(rule: {
  mode: string
  credential: string | null
}): boolean {
  if (!SUBSTITUTING_MODES.includes(rule.mode.toLowerCase() as 'sandbox')) return false
  return rule.credential === null || rule.credential.trim() === ''
}

/**
 * Every rule that is configured in a way somebody should look at.
 *
 * Returned as one list rather than three, ordered with the always-wrong
 * condition first, because an operator opening this page needs the sandbox
 * findings to be impossible to scroll past. Sorting by organization would bury
 * a live credential leak under a page of ordinary allows.
 */
export async function findings(db: Db): Promise<Finding[]> {
  const rows = await db.execute<Raw>(sql`${SELECT_RULES} ORDER BY o.slug, n.position, n.host`)

  const out: Finding[] = []
  for (const raw of rows) {
    const rule = toRule(raw)
    if (forwardsTheLiveCredential(rule)) {
      out.push({
        kind: 'sandbox-without-credential',
        rule,
        says:
          `Requests to ${rule.host} are recorded as sandbox calls and go out carrying whatever ` +
          `credential the application set. Nothing is substituted, because no sandbox credential ` +
          `is configured on this rule. In the request log these are identical to working sandbox ` +
          `calls except that substituted is false.`,
        severity: 'failing',
      })
      // Deliberately no `continue`. A rule can be both unsubstitutable and
      // unapproved, and reporting only the first would let fixing the
      // credential silently drop the approval finding off the page.
    }
    if (rule.approvedAt === null) {
      out.push({
        kind: 'never-approved',
        rule,
        says:
          `Proposed${rule.proposedBy ? ` by ${rule.proposedBy}` : ''} and never decided, so it is ` +
          `inert: nothing applies it and ${rule.host} falls through to whatever the default is. ` +
          `A rule that looks present and does nothing is the shape people misread as protection.`,
        severity: 'review',
      })
      continue
    }
    if (rule.mode.toLowerCase() === 'allow') {
      out.push({
        kind: 'allow',
        rule,
        says:
          `Allow is the one mode where a side effect reaches the real ${rule.host} from an ` +
          `environment running unreviewed code against a copy of production data. ` +
          `Approved${rule.approvedBy ? ` by ${rule.approvedBy}` : ''}.`,
        severity: 'review',
      })
    }
  }

  const rank: Record<FindingKind, number> = {
    'sandbox-without-credential': 0,
    allow: 1,
    'never-approved': 2,
  }
  return out.sort((a, b) => rank[a.kind] - rank[b.kind])
}

export interface FirewallSummary {
  /** Rules across every organization in scope. */
  rules: number
  /** Organizations that have configured any egress rule at all. */
  organizations: number
  /** Rules that promise a substitution they cannot perform. Never acceptable. */
  forwardingLiveCredentials: number
  /** Rules proposed and never decided, so inert. */
  neverApproved: number
  /** Approved rules in allow mode. */
  allowed: number
}

export async function summary(db: Db): Promise<FirewallSummary> {
  const rows = await db.execute<{
    rules: string
    organizations: string
    forwarding: string
    never_approved: string
    allowed: string
  }>(sql`
    SELECT count(*) AS rules,
           count(DISTINCT org_id) AS organizations,
           count(*) FILTER (
             WHERE lower(mode) = 'sandbox'
               AND (credential IS NULL OR btrim(credential) = '')) AS forwarding,
           count(*) FILTER (WHERE approved_at IS NULL) AS never_approved,
           count(*) FILTER (WHERE approved_at IS NOT NULL AND lower(mode) = 'allow') AS allowed
    FROM network_rules`)
  const r = rows[0]
  return {
    rules: Number(r?.rules ?? 0),
    organizations: Number(r?.organizations ?? 0),
    forwardingLiveCredentials: Number(r?.forwarding ?? 0),
    neverApproved: Number(r?.never_approved ?? 0),
    allowed: Number(r?.allowed ?? 0),
  }
}
