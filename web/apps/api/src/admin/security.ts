// The Security & Governance lane of the operator portal.
//
// THE THREE QUESTIONS THIS LANE ANSWERS, and nothing else. Each section here
// exists because somebody asks it out loud during an incident or a review, and
// the product could not answer it before:
//
//   Security Center   -> what is the standing set of ways into this
//                        installation, who holds one, and which of them nobody
//                        has touched in a long time.
//   Audit Logs        -> what did the people at this company do, and can the
//                        record be shown to somebody who does not trust us.
//   Data Governance   -> for one named person, what do we hold, where is it,
//                        and what happens to it if they ask us to erase them.
//
// WHAT IS DELIBERATELY NOT HERE, and why that is the point of the file rather
// than an apology. There is no vulnerability table, no security finding table,
// no threat feed, no device inventory, no retention policy table, no consent
// record and no subject request table in this schema. Section 3 of the portal
// survey enumerates the 64 tables; none of those exist. So this file computes
// nothing from data that is not there, and the pages it feeds say out loud
// which capabilities are unwired and what a working one would need. A
// compliance dashboard drawn over invented numbers is the single worst artifact
// this lane could produce: it is read by somebody making a legal claim on
// behalf of the company, and every figure on it would be a lie that looked
// audited.
//
// WHY THE GOVERNANCE PERMISSIONS ARE NOT admin.security.*. The paths here all
// live under `admin.security` because that is this navigation group's mounted
// namespace and a route outside it is a route the group's pages cannot find.
// The PERMISSIONS are a different axis: reading what egress credentials exist
// and reading a named person's data map are different jobs held by different
// people, so they are `admin.security.read` and `admin.governance.read`. A
// permission is a capability, not a path, and collapsing the two would force
// whoever may see a credential inventory to also see everything the company
// holds about an individual customer.
//
// THE POOL. Every read here goes through ctx.adminDb, which is the operator
// pool: a separate connection whose role holds the BYPASSRLS attribute. That
// distinction is load bearing on THIS lane above all others. A `current_setting`
// predicate is a claim any code path can make, and a GRANT naming the operator
// role also applies to anything granted membership of it, because Postgres
// applies a policy when the current user HAS THE PRIVILEGES OF the named role.
// Only the role ATTRIBUTE is uninheritable, and only it produces "this
// connection may read every tenant" as a property of the credential rather than
// an assertion of the code. See admin-pool.ts for the measurements. Getting
// this wrong on a governance page means showing an operator a person's data
// they should not see, or reporting that the company holds nothing about
// somebody when RLS simply hid it.
//
// COLUMN SAFETY IS AN APPLICATION PROPERTY. RLS is row level and cannot
// restrict a column, so every query below names its columns. Deliberately never
// named anywhere in this file: engine_tokens.token_hash, provider_keys.
// ciphertext and .nonce, sso_connection_secrets in its entirety,
// sso_break_glass_codes.code_hash, organization_deletion_exports.token_hash and
// .document, admin_users.password_hash and .password_salt. What IS named is
// engine_tokens.prefix and provider_keys.last4, because both are the non-secret
// identifiers the customer's own console already shows them, and an operator
// matching a token against a request log needs the prefix to do it.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { verifyAdminAuditChain, type Db } from '@antifailure/db'
import { router } from '../trpc.ts'
import { adminProcedure, adminAudit, type AdminContext } from './trpc.ts'
import { stepOf } from '../enterprise/deletion.ts'

/** A page of rows and how to ask for the next one. Keyset, never OFFSET, for
 *  the reason router.ts gives: an operator paging a list that is being written
 *  to gets duplicates from OFFSET and the duplicates look like real data. */
const page = z.object({
  limit: z.number().int().min(1).max(200).default(50),
  cursor: z.string().nullish(),
})

const uuid = z.string().uuid()

/** Ninety days, the line this file draws between a credential somebody is
 *  using and one nobody has thought about. Stated once so the console's copy
 *  and the query cannot drift, and stated as a constant rather than inlined
 *  because it is a judgement rather than a fact and a reader should be able to
 *  find it and argue with it. */
const STALE_DAYS = 90

/** How long a credential gets to go unused before "never used" means something.
 *  A token minted an hour ago and not yet used is a token somebody is still
 *  setting up, not a finding. */
const UNUSED_GRACE_DAYS = 30

function daysBefore(now: Date, days: number): string {
  return new Date(now.getTime() - days * 24 * 60 * 60 * 1000).toISOString()
}

function iso(value: Date | string | null): string | null {
  if (value === null) return null
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}

function num(value: unknown): number {
  return Number(value ?? 0)
}

/**
 * Turns one extra row into a cursor.
 *
 * Copied in shape from router.ts rather than imported, because that one is not
 * exported and reaching into another lane's file for a four line helper is how
 * two lanes end up editing one function. Asking for limit + 1 answers "is there
 * more" without a count query, which on cross-tenant tables is the expensive
 * one.
 */
function pageOf<Row, Out>(
  rows: Row[],
  limit: number,
  cursorOf: (row: Row) => string,
  map: (row: Row) => Out,
): { rows: Out[]; nextCursor: string | null } {
  const more = rows.length > limit
  const visible = more ? rows.slice(0, limit) : rows
  return {
    rows: visible.map(map),
    nextCursor: more && visible.length > 0 ? cursorOf(visible[visible.length - 1]!) : null,
  }
}

/**
 * A keyset cursor over a timestamp and a tie breaking id.
 *
 * A timestamp alone is not a key: two credentials created in the same
 * millisecond, which is what a bulk import produces, make the page boundary
 * ambiguous and rows fall through it. The id is what makes the ordering total.
 */
function encodeCursor(at: Date | string, id: string): string {
  return `${iso(at)}|${id}`
}

function decodeCursor(cursor: string | null | undefined): { at: string; id: string } | null {
  if (!cursor) return null
  const cut = cursor.indexOf('|')
  if (cut <= 0) return null
  const at = cursor.slice(0, cut)
  const id = cursor.slice(cut + 1)
  if (Number.isNaN(Date.parse(at)) || id.length === 0) return null
  return { at, id }
}

// ---------------------------------------------------------------------------
// Security Center
// ---------------------------------------------------------------------------

/**
 * Every standing credential on this installation, in one shape.
 *
 * THREE TABLES AND ONE LIST, and that is the decision the page rests on. Engine
 * tokens, provider keys and OIDC repository bindings are three tables because
 * they are three mechanisms, and they are one question: what can currently act
 * against this installation without a person present. Three separate lists
 * would each look short and reassuring, and the operator would have to remember
 * that the other two exist. A union means forgetting is not possible.
 */
export type CredentialKind = 'engine_token' | 'provider_key' | 'oidc_binding'

/**
 * Why a credential is being surfaced.
 *
 * `expired` is DELIBERATELY not one of these, and the distinction matters
 * enough to name here. Both authentication paths compare `expires_at` against
 * the process clock and refuse, at ingest.ts and at auth/device.ts, and both do
 * it before the constant-time hash comparison. So an expired token is already
 * dead: the row is untidy, not exposed. Calling that a security finding trains
 * an operator to read the word "finding" as noise, which is how the one that
 * mattered gets scrolled past.
 */
export type CredentialFlag =
  /** Live, and never presented since it was minted. A grace period is applied,
   *  so a credential somebody is still wiring up is not on the list. */
  | 'never_used'
  /** Live, used once, and not since the staleness line. */
  | 'idle'
  /** A provider key that has never been rotated and is older than the line. The
   *  only one of these three that holds a CUSTOMER's secret rather than one of
   *  ours, which is why age alone is worth surfacing. */
  | 'unrotated'

/**
 * The three tables as one relation, with every judgement about a row made in
 * SQL and made once.
 *
 * WHY THE FLAGS ARE COMPUTED HERE AND NOT IN TYPESCRIPT. The list is keyset
 * paged and the summary is a set of counts, and they have to agree: a summary
 * that says four credentials are idle above a list that pages through three is
 * the kind of disagreement nobody can debug from the screen. Filtering in
 * TypeScript after the page was fetched would also mean a page of fifty rows
 * could yield two, so the pager would have to over-fetch and guess, and the
 * cursor it handed back would skip everything it discarded. Both problems
 * disappear when the predicate is in the WHERE clause. The cost is that these
 * three booleans are the definition of a finding in this product, so they are
 * written once, here, and both routes read them.
 *
 * The comparisons take the request's clock as parameters rather than calling
 * `now()`, for the reason ingest.ts gives about token expiry: a database side
 * comparison reads the wall clock and quietly ignores an injected clock, which
 * is how a time dependent number ships untested.
 */
function scoredCredentials(nowIso: string, staleIso: string, graceIso: string) {
  return sql`
    creds AS (
      SELECT 'engine_token'::text AS credential_kind, t.id, t.org_id, o.slug AS org_slug,
             t.name AS label, t.prefix AS handle, u.github_login AS created_by,
             t.created_at, t.last_used_at, t.expires_at,
             NULL::timestamptz AS rotated_at, t.revoked_at
        FROM engine_tokens t
        JOIN organizations o ON o.id = t.org_id
        LEFT JOIN users u ON u.id = t.created_by
      UNION ALL
      SELECT 'provider_key'::text, p.id, p.org_id, o.slug,
             p.provider, p.last4, u.github_login,
             p.created_at, NULL::timestamptz, NULL::timestamptz,
             p.rotated_at, p.revoked_at
        FROM provider_keys p
        JOIN organizations o ON o.id = p.org_id
        LEFT JOIN users u ON u.id = p.created_by
      UNION ALL
      SELECT 'oidc_binding'::text, b.id, b.org_id, o.slug,
             b.repository, NULL::text, u.github_login,
             b.created_at, b.last_used_at, NULL::timestamptz,
             NULL::timestamptz, b.revoked_at
        FROM oidc_repository_bindings b
        JOIN organizations o ON o.id = b.org_id
        LEFT JOIN users u ON u.id = b.created_by
    ),
    scored AS (
      SELECT c.*,
             (c.revoked_at IS NOT NULL) AS is_revoked,
             (c.revoked_at IS NULL
              AND c.expires_at IS NOT NULL
              AND c.expires_at <= ${nowIso}::timestamptz) AS is_expired,
             (c.revoked_at IS NULL
              AND (c.expires_at IS NULL OR c.expires_at > ${nowIso}::timestamptz)) AS is_live,
             -- A provider key records no use at all, so "never used" and "idle"
             -- are not askable of one. Saying nothing is the honest answer;
             -- inventing a usage signal from created_at would be a measurement
             -- nobody took.
             (c.revoked_at IS NULL
              AND (c.expires_at IS NULL OR c.expires_at > ${nowIso}::timestamptz)
              AND c.credential_kind <> 'provider_key'
              AND c.last_used_at IS NULL
              AND c.created_at <= ${graceIso}::timestamptz) AS flag_never_used,
             (c.revoked_at IS NULL
              AND (c.expires_at IS NULL OR c.expires_at > ${nowIso}::timestamptz)
              AND c.credential_kind <> 'provider_key'
              AND c.last_used_at IS NOT NULL
              AND c.last_used_at < ${staleIso}::timestamptz) AS flag_idle,
             (c.revoked_at IS NULL
              AND c.credential_kind = 'provider_key'
              AND c.rotated_at IS NULL
              AND c.created_at < ${staleIso}::timestamptz) AS flag_unrotated
      FROM creds c
    )`
}

interface CredentialRow extends Record<string, unknown> {
  credential_kind: CredentialKind
  id: string
  org_id: string
  org_slug: string
  /** The name a person gave it, or the provider, or the repository. */
  label: string
  /** The non-secret identifier: a token prefix, a key's last four, or null. */
  handle: string | null
  /** Who minted it, when the table records that. */
  created_by: string | null
  created_at: Date | string
  last_used_at: Date | string | null
  expires_at: Date | string | null
  rotated_at: Date | string | null
  revoked_at: Date | string | null
  is_revoked: boolean
  is_expired: boolean
  is_live: boolean
  flag_never_used: boolean
  flag_idle: boolean
  flag_unrotated: boolean
}

function flagsOf(row: CredentialRow): CredentialFlag[] {
  const flags: CredentialFlag[] = []
  if (row.flag_never_used) flags.push('never_used')
  if (row.flag_idle) flags.push('idle')
  if (row.flag_unrotated) flags.push('unrotated')
  return flags
}

function stateOf(row: CredentialRow): 'live' | 'expired' | 'revoked' {
  if (row.is_revoked) return 'revoked'
  if (row.is_expired) return 'expired'
  return 'live'
}

export const securityRouter = router({
  /**
   * The standing security posture of the installation, in one read.
   *
   * ONE ROUTE RATHER THAN SIX, because the page is a single answer to a single
   * question and six round trips would let five of them land and one fail,
   * which renders as a page that is mostly right. A partially correct security
   * summary is worse than an error: the reader has no way to tell which panel
   * is the stale one.
   *
   * Every count here is over a whole table with no time window, so none of them
   * needs an index that does not exist, and none of them is a moving figure
   * that means something different each time it is read.
   */
  posture: adminProcedure('admin.security.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    const now = c.clock.now()
    const nowIso = now.toISOString()
    const stale = daysBefore(now, STALE_DAYS)
    const grace = daysBefore(now, UNUSED_GRACE_DAYS)

    return c.adminDb(async (db) => {
      // One row per credential kind, using the same scored relation the list
      // pages over, so the number in the summary and the length of the list it
      // links to are the same fact rather than two.
      const credentials = await db.execute<{
        credential_kind: CredentialKind
        live: string
        expired: string
        revoked: string
        never_expiring: string
        never_used: string
        idle: string
        unrotated: string
      }>(sql`
        WITH ${scoredCredentials(nowIso, stale, grace)}
        SELECT credential_kind,
               count(*) FILTER (WHERE is_live)::text AS live,
               count(*) FILTER (WHERE is_expired)::text AS expired,
               count(*) FILTER (WHERE is_revoked)::text AS revoked,
               count(*) FILTER (WHERE is_live AND expires_at IS NULL)::text AS never_expiring,
               count(*) FILTER (WHERE flag_never_used)::text AS never_used,
               count(*) FILTER (WHERE flag_idle)::text AS idle,
               count(*) FILTER (WHERE flag_unrotated)::text AS unrotated
        FROM scored GROUP BY credential_kind`)

      const [sso] = await db.execute<{
        connections: string
        enabled: string
        bypassable: string
      }>(sql`
        SELECT count(*)::text AS connections,
               count(*) FILTER (WHERE enabled)::text AS enabled,
               -- Enabled and NOT enforced. The connection works, and every
               -- member can still sign in the old way, so the organization
               -- believes it has single sign-on and does not have it. That is
               -- the one SSO state worth surfacing on its own.
               count(*) FILTER (WHERE enabled AND NOT enforced)::text AS bypassable
        FROM sso_connections`)

      const [glass] = await db.execute<{ outstanding: string; used: string }>(sql`
        SELECT count(*) FILTER (WHERE used_at IS NULL)::text AS outstanding,
               count(*) FILTER (WHERE used_at IS NOT NULL)::text AS used
        FROM sso_break_glass_codes`)

      const [operators] = await db.execute<{
        total: string
        unprovisioned: string
        suspended: string
      }>(sql`
        SELECT count(*)::text AS total,
               -- No password has ever been set, so nothing can be signed in
               -- against: there is no password that hashes to NULL. An account
               -- waiting for setup and one that is live look identical in every
               -- other column on the operators page.
               count(*) FILTER (WHERE password_hash IS NULL)::text AS unprovisioned,
               count(*) FILTER (WHERE suspended_at IS NOT NULL)::text AS suspended
        FROM admin_users`)

      const [sessions] = await db.execute<{ live: string; impersonating: string }>(sql`
        SELECT count(*) FILTER (WHERE revoked_at IS NULL
                                  AND expires_at > ${nowIso}::timestamptz)::text AS live,
               count(*) FILTER (WHERE revoked_at IS NULL
                                  AND expires_at > ${nowIso}::timestamptz
                                  AND impersonated_user_id IS NOT NULL)::text AS impersonating
        FROM admin_sessions`)

      // The list rather than only the count. An operator currently acting as a
      // customer is the most consequential state this product has, and "1" is
      // not an answer to it: the useful answer is who, as whom, and why.
      const impersonations = await db.execute<{
        id: string
        operator: string
        acting_as: string | null
        reason: string | null
        created_at: Date | string
        expires_at: Date | string
      }>(sql`
        SELECT s.id, a.email AS operator, u.github_login AS acting_as,
               s.impersonation_reason AS reason, s.created_at, s.expires_at
        FROM admin_sessions s
        JOIN admin_users a ON a.id = s.admin_user_id
        LEFT JOIN users u ON u.id = s.impersonated_user_id
        WHERE s.impersonated_user_id IS NOT NULL
          AND s.revoked_at IS NULL
          AND s.expires_at > ${nowIso}::timestamptz
        ORDER BY s.created_at DESC LIMIT 20`)

      // The severe end of the operator chain. Served by
      // admin_audit_severity_idx, which is PARTIAL on ('high','critical')
      // exactly so this is not a scan of a table whose ordinary contents are
      // one info row per operator request.
      const severe = await db.execute<{
        seq: string
        actor_label: string
        action: string
        subject_org_label: string | null
        severity: string
        occurred_at: Date | string
      }>(sql`
        SELECT seq, actor_label, action, subject_org_label, severity, occurred_at
        FROM admin_audit_entries
        WHERE severity IN ('high', 'critical')
        ORDER BY seq DESC LIMIT 10`)

      const byKind = (kind: CredentialKind) => credentials.find((r) => r.credential_kind === kind)
      const engine = byKind('engine_token')
      const keys = byKind('provider_key')
      const bindings = byKind('oidc_binding')

      return {
        /** The staleness lines the flags were computed with, so the page states
         *  its own definitions instead of hard coding a second copy of them. */
        thresholds: { staleDays: STALE_DAYS, unusedGraceDays: UNUSED_GRACE_DAYS },
        engineTokens: {
          live: num(engine?.live),
          expired: num(engine?.expired),
          revoked: num(engine?.revoked),
          neverExpiring: num(engine?.never_expiring),
          neverUsed: num(engine?.never_used),
          idle: num(engine?.idle),
        },
        providerKeys: {
          live: num(keys?.live),
          revoked: num(keys?.revoked),
          unrotated: num(keys?.unrotated),
        },
        oidcBindings: {
          live: num(bindings?.live),
          revoked: num(bindings?.revoked),
          neverUsed: num(bindings?.never_used),
          idle: num(bindings?.idle),
        },
        sso: {
          connections: num(sso?.connections),
          enabled: num(sso?.enabled),
          bypassable: num(sso?.bypassable),
          breakGlassOutstanding: num(glass?.outstanding),
          breakGlassUsed: num(glass?.used),
        },
        operators: {
          total: num(operators?.total),
          unprovisioned: num(operators?.unprovisioned),
          suspended: num(operators?.suspended),
          liveSessions: num(sessions?.live),
          impersonatingSessions: num(sessions?.impersonating),
        },
        impersonations: impersonations.map((r) => ({
          id: r.id,
          operator: r.operator,
          actingAs: r.acting_as,
          reason: r.reason,
          startedAt: iso(r.created_at)!,
          expiresAt: iso(r.expires_at)!,
        })),
        severeEvents: severe.map((r) => ({
          seq: Number(r.seq),
          actor: r.actor_label,
          action: r.action,
          organization: r.subject_org_label,
          severity: r.severity,
          occurredAt: iso(r.occurred_at)!,
        })),
      }
    })
  }),

  /** Every standing credential, paged, filtered by kind and by state. */
  credentials: adminProcedure('admin.security.read')
    .input(
      page.extend({
        kind: z.enum(['engine_token', 'provider_key', 'oidc_binding']).optional(),
        /** `flagged` is not a fourth state. It is `live` narrowed to the rows
         *  carrying at least one flag, and it is narrowed in the WHERE clause
         *  so that the pager pages over the filtered list rather than over the
         *  unfiltered one with the rejects removed afterwards. */
        state: z.enum(['live', 'flagged', 'expired', 'revoked']).default('live'),
        orgId: uuid.optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const nowIso = now.toISOString()
      const stale = daysBefore(now, STALE_DAYS)
      const grace = daysBefore(now, UNUSED_GRACE_DAYS)
      const cursor = decodeCursor(input.cursor)
      const state = input.state

      return c.adminDb(async (db) => {
        const rows = await db.execute<CredentialRow>(sql`
          WITH ${scoredCredentials(nowIso, stale, grace)}
          SELECT * FROM scored
          WHERE (${state}::text <> 'live' OR is_live)
            AND (${state}::text <> 'expired' OR is_expired)
            AND (${state}::text <> 'revoked' OR is_revoked)
            AND (${state}::text <> 'flagged'
                 OR flag_never_used OR flag_idle OR flag_unrotated)
            AND (${input.kind ?? null}::text IS NULL
                 OR credential_kind = ${input.kind ?? null})
            AND (${input.orgId ?? null}::uuid IS NULL
                 OR org_id = ${input.orgId ?? null}::uuid)
            AND (${cursor?.at ?? null}::timestamptz IS NULL
                 OR (created_at, id) < (${cursor?.at ?? null}::timestamptz,
                                        ${cursor?.id ?? null}::uuid))
          ORDER BY created_at DESC, id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(rows, input.limit, (r) => encodeCursor(r.created_at, r.id), (r) => ({
          id: r.id,
          kind: r.credential_kind,
          organization: r.org_slug,
          orgId: r.org_id,
          label: r.label,
          handle: r.handle,
          createdBy: r.created_by,
          createdAt: iso(r.created_at)!,
          lastUsedAt: iso(r.last_used_at),
          expiresAt: iso(r.expires_at),
          rotatedAt: iso(r.rotated_at),
          revokedAt: iso(r.revoked_at),
          state: stateOf(r),
          flags: flagsOf(r),
        }))
      })
    }),

  sso: adminProcedure('admin.security.read')
    .input(page)
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor)
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          org_id: string
          org_slug: string
          kind: string
          display_name: string
          enabled: boolean
          enforced: boolean
          default_role: string
          created_at: Date | string
          updated_at: Date | string
          certificates: number
          break_glass_outstanding: string
          break_glass_used: string
        }>(sql`
          SELECT s.id, s.org_id, o.slug AS org_slug, s.kind, s.display_name,
                 s.enabled, s.enforced, s.default_role, s.created_at, s.updated_at,
                 -- The count, never the certificates. A page that can print an
                 -- identity provider's signing material is a page that can leak
                 -- one, and the count is what answers "is this configured".
                 coalesce(array_length(s.idp_certificates, 1), 0) AS certificates,
                 (SELECT count(*) FROM sso_break_glass_codes g
                   WHERE g.org_id = s.org_id AND g.used_at IS NULL)::text AS break_glass_outstanding,
                 (SELECT count(*) FROM sso_break_glass_codes g
                   WHERE g.org_id = s.org_id AND g.used_at IS NOT NULL)::text AS break_glass_used
          FROM sso_connections s
          JOIN organizations o ON o.id = s.org_id
          WHERE (${cursor?.at ?? null}::timestamptz IS NULL
                 OR (s.created_at, s.id) < (${cursor?.at ?? null}::timestamptz, ${cursor?.id ?? null}::uuid))
          ORDER BY s.created_at DESC, s.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(rows, input.limit, (r) => encodeCursor(r.created_at, r.id), (r) => ({
          id: r.id,
          orgId: r.org_id,
          organization: r.org_slug,
          kind: r.kind,
          displayName: r.display_name,
          enabled: r.enabled,
          enforced: r.enforced,
          defaultRole: r.default_role,
          certificates: Number(r.certificates ?? 0),
          breakGlassOutstanding: num(r.break_glass_outstanding),
          breakGlassUsed: num(r.break_glass_used),
          createdAt: iso(r.created_at)!,
          updatedAt: iso(r.updated_at)!,
        }))
      })
    }),

  // -------------------------------------------------------------------------
  // Data Governance
  // -------------------------------------------------------------------------

  /**
   * Every organization erasure this installation has been asked for.
   *
   * The step is DERIVED by stepOf from enterprise/deletion.ts rather than
   * recomputed here, and that import is the point of the route. The tenant's
   * own page and this one now cannot disagree about which step a deletion is
   * on, and a deletion stuck at a step is stuck at the same step in both.
   */
  deletions: adminProcedure('admin.governance.read')
    .input(
      page.extend({
        /** `open` is everything that has neither purged nor been called off,
         *  which is the queue somebody is actually waiting on. */
        state: z.enum(['open', 'stuck', 'finished', 'all']).default('open'),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const now = c.clock.now()
      const cursor = decodeCursor(input.cursor)

      return c.adminDb(async (db) => {
        const rows = await db.execute<DeletionRecord>(sql`
          SELECT d.id, d.org_id, d.org_slug, d.org_name, d.requested_by_label,
                 d.requested_at, d.reason,
                 d.work_stopped_at, d.environments_stopped, d.runs_cancelled,
                 d.subscription_cancelled_at, d.subscription_id, d.entitlement_ends_at,
                 d.credentials_revoked_at, d.engine_tokens_revoked, d.provider_keys_revoked,
                 d.sessions_revoked, d.installations_revoked,
                 d.exported_at, d.purged_at, d.cancelled_at,
                 d.last_error_at, d.last_error_step, d.last_error_message, d.attempts,
                 -- The export's shape, never its document and never its token
                 -- hash. The document is the customer's entire account and this
                 -- page has no business holding it in a response.
                 e.expires_at AS export_expires_at, e.size_bytes AS export_size_bytes,
                 e.destroyed_at AS export_destroyed_at, e.download_count AS export_downloads
          FROM organization_deletions d
          LEFT JOIN organization_deletion_exports e ON e.deletion_id = d.id
          WHERE (${input.state}::text <> 'open'
                 OR (d.purged_at IS NULL AND d.cancelled_at IS NULL))
            AND (${input.state}::text <> 'finished'
                 OR d.purged_at IS NOT NULL OR d.cancelled_at IS NOT NULL)
            AND (${input.state}::text <> 'stuck'
                 OR (d.last_error_at IS NOT NULL
                     AND d.purged_at IS NULL AND d.cancelled_at IS NULL))
            AND (${cursor?.at ?? null}::timestamptz IS NULL
                 OR (d.requested_at, d.id) < (${cursor?.at ?? null}::timestamptz, ${cursor?.id ?? null}::uuid))
          ORDER BY d.requested_at DESC, d.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(rows, input.limit, (r) => encodeCursor(r.requested_at, r.id), (r) => {
          const step = stepOf(r, now)
          const size = r.export_size_bytes === null ? null : Number(r.export_size_bytes)
          return {
            id: r.id,
            orgId: r.org_id,
            organization: r.org_name,
            slug: r.org_slug,
            requestedBy: r.requested_by_label,
            requestedAt: iso(r.requested_at)!,
            reason: r.reason,
            step,
            waitingUntil: step === 'await_entitlement_end' ? iso(r.entitlement_ends_at) : null,
            purgedAt: iso(r.purged_at),
            cancelledAt: iso(r.cancelled_at),
            revoked:
              r.credentials_revoked_at === null
                ? null
                : {
                    at: iso(r.credentials_revoked_at)!,
                    engineTokens: r.engine_tokens_revoked ?? 0,
                    providerKeys: r.provider_keys_revoked ?? 0,
                    sessions: r.sessions_revoked ?? 0,
                    installations: r.installations_revoked ?? 0,
                  },
            export:
              r.export_expires_at === null
                ? null
                : {
                    // A document AND not destroyed. The row is created when the
                    // deletion is requested, long before the export step fills
                    // it, so "not destroyed" alone reports an empty row as a
                    // download somebody could take. Same test viewOf makes.
                    available: r.export_destroyed_at === null && (size ?? 0) > 0,
                    expiresAt: iso(r.export_expires_at),
                    sizeBytes: size,
                    downloads: r.export_downloads ?? 0,
                    destroyedAt: iso(r.export_destroyed_at),
                  },
            lastError:
              r.last_error_at === null
                ? null
                : {
                    at: iso(r.last_error_at)!,
                    step: r.last_error_step ?? 'unknown',
                    message: r.last_error_message ?? 'no message was recorded',
                  },
            attempts: r.attempts,
          }
        })
      })
    }),

  /**
   * The masking rules every customer has declared, across the installation.
   *
   * THE ONLY PII INVENTORY THIS PRODUCT HAS. A masking rule names a table and a
   * column in a customer's own database that must be transformed before it is
   * cloned into a twin, so the set of them is the closest thing here to a
   * register of where personal data lives in the data the product handles. It
   * is a customer-authored register rather than a vendor-authored one, and the
   * page says so, because a rule nobody wrote is a column nobody masked.
   *
   * `confirmed` is a single boolean and NOT an approval workflow. There is no
   * approver, no timestamp and no second party in the schema, so the page
   * reports it as "confirmed by the customer" and never as "approved".
   */
  masking: adminProcedure('admin.governance.read')
    .input(
      page.extend({
        confirmed: z.boolean().optional(),
        orgId: uuid.optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor)
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          table_name: string
          column_name: string
          transform: string
          reason: string | null
          confirmed: boolean
          updated_at: Date | string
        }>(sql`
          SELECT m.id, m.org_id, o.slug AS org_slug, r.full_name AS repository,
                 m.table_name, m.column_name, m.transform, m.reason, m.confirmed,
                 m.updated_at
          FROM masking_rules m
          JOIN organizations o ON o.id = m.org_id
          JOIN repositories r ON r.id = m.repository_id
          WHERE (${input.confirmed ?? null}::boolean IS NULL
                 OR m.confirmed = ${input.confirmed ?? null}::boolean)
            AND (${input.orgId ?? null}::uuid IS NULL OR m.org_id = ${input.orgId ?? null}::uuid)
            AND (${cursor?.at ?? null}::timestamptz IS NULL
                 OR (m.updated_at, m.id) < (${cursor?.at ?? null}::timestamptz, ${cursor?.id ?? null}::uuid))
          ORDER BY m.updated_at DESC, m.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(rows, input.limit, (r) => encodeCursor(r.updated_at, r.id), (r) => ({
          id: r.id,
          orgId: r.org_id,
          organization: r.org_slug,
          repository: r.repository,
          table: r.table_name,
          column: r.column_name,
          transform: r.transform,
          reason: r.reason,
          confirmed: r.confirmed,
          updatedAt: iso(r.updated_at)!,
        }))
      })
    }),

  /**
   * Accounts whose last organization is gone.
   *
   * WHY THIS IS A ROUTE AND NOT A FOOTNOTE. The deletion state machine's purge
   * step is `DELETE FROM organizations`, and every org-scoped table cascades
   * from it. `users` does not: a user row is global, keyed on a GitHub account,
   * and shared across organizations, so deleting one organization must not
   * delete a person who is also in another. The consequence nobody wrote down
   * is that purging the LAST organization somebody belonged to leaves their
   * row, with their email, their name and their avatar, behind forever. There
   * is no route in this product that deletes it.
   *
   * That is the residue an erasure request is actually about, so it is
   * countable and it is listed, rather than being a sentence in a comment that
   * nobody reads.
   */
  orphanedAccounts: adminProcedure('admin.governance.read')
    .input(page)
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const cursor = decodeCursor(input.cursor)
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          github_login: string
          email: string | null
          name: string | null
          created_at: Date | string
        }>(sql`
          SELECT u.id, u.github_login, u.email, u.name, u.created_at
          FROM users u
          WHERE NOT EXISTS (SELECT 1 FROM members m WHERE m.user_id = u.id)
            AND (${cursor?.at ?? null}::timestamptz IS NULL
                 OR (u.created_at, u.id) < (${cursor?.at ?? null}::timestamptz, ${cursor?.id ?? null}::uuid))
          ORDER BY u.created_at DESC, u.id DESC
          LIMIT ${input.limit + 1}`)

        return pageOf(rows, input.limit, (r) => encodeCursor(r.created_at, r.id), (r) => ({
          id: r.id,
          githubLogin: r.github_login,
          email: r.email,
          name: r.name,
          createdAt: iso(r.created_at)!,
        }))
      })
    }),

  /**
   * What this installation holds about one named person, and what would happen
   * to it if they asked to be erased.
   *
   * THE QUESTION A CUSTOMER'S LAWYER ASKS, answered from the live catalog
   * rather than from a hand written list. Every table that references
   * `users(id)` is discovered by reading pg_constraint at request time, so a
   * table somebody adds next month appears here with no edit to this file. A
   * hand written inventory is a document that is correct on the day it is
   * written and is the thing that makes the answer wrong a year later, which is
   * exactly when it is quoted.
   *
   * The foreign key's ON DELETE action is carried through unchanged, because it
   * IS the erasure answer: CASCADE means the row goes when the person does,
   * SET NULL means the row stays and is de-identified, and NO ACTION means
   * deleting the person is refused while the row exists. Nobody has to trust a
   * description of the behaviour; this is the behaviour, read from the
   * database that enforces it.
   *
   * WHAT IT DOES NOT DO. It does not dump the data. A copy of everything the
   * product holds about a person is a different artifact with different rules,
   * and the only data export this product builds is the organization scoped one
   * in the deletion pipeline. Saying "here is where it is" precisely is worth
   * more than saying "here is some of it" vaguely.
   */
  subject: adminProcedure('admin.governance.read')
    .input(
      z.object({
        userId: uuid.optional(),
        /** An email address, a GitHub login, or part of either. */
        query: z.string().trim().min(1).max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      if (!input.userId && !input.query) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: 'Give a user id, an email address or a GitHub login to look up.',
        })
      }
      return c.adminDb(async (db) => {
        const subject = await resolveSubject(db, input)
        if (subject.kind === 'candidates') {
          // Nobody was identified, so nothing is recorded beyond the automatic
          // per-request read entry. An entry naming a person the operator did
          // not actually open would put somebody in the log for a typo.
          return {
            subject: null,
            candidates: subject.rows,
            map: null,
            retained: null,
            erasure: ERASURE_STATEMENT,
            countCeiling: COUNT_CEILING,
          }
        }

        // BEFORE the data map is read, inside the same transaction. The
        // automatic read auditing on every operator query records the ROUTE;
        // this records the PERSON, which is the only version of the entry a
        // regulator asking "who looked at me" can use. Written first, so the
        // operator cannot get the answer without the record existing.
        await adminAudit(db, c, {
          action: 'governance.subject_inspected',
          targetType: 'user',
          targetId: subject.user.id,
          severity: 'notice',
          detail: { subject: subject.user.github_login },
        })

        return {
          subject: {
            id: subject.user.id,
            githubLogin: subject.user.github_login,
            email: subject.user.email,
            name: subject.user.name,
            createdAt: iso(subject.user.created_at)!,
            organizations: subject.organizations,
          },
          candidates: [],
          map: await subjectMap(db, subject.user.id),
          retained: RETAINED_BY_DESIGN,
          erasure: ERASURE_STATEMENT,
          countCeiling: COUNT_CEILING,
        }
      })
    }),

  /**
   * The same answer as a file, for the person who has to hand it to somebody.
   *
   * WHY THIS IS A SEPARATE PERMISSION FROM READING IT. A lookup answers a
   * question in the room and is gone. A file is a document about a named
   * individual that leaves the system, gets attached to an email, and outlives
   * every access control this product has. That is the same reasoning that
   * keeps admin.audit.export off every role but two, applied to the other kind
   * of document this portal can produce.
   *
   * JSON AND NOT A SPREADSHEET. The document is a subject, a set of
   * memberships, a table of locations and a list of what is kept on purpose.
   * Four shapes, and flattening them into one CSV grid would produce a file
   * where three of the four are footnotes in a column. The audit export is CSV
   * as well as JSON because it genuinely is one table of one kind of row.
   *
   * WHAT THE DOCUMENT DOES NOT CONTAIN, said inside the document itself rather
   * than only here. It is a MAP and not a dump: where the data is, how much of
   * it there is, and what an erasure would do to each place. This product
   * builds exactly one export of data itself, the organization scoped one in
   * the deletion pipeline, and there is no subject scoped equivalent. A file
   * that implied otherwise would be handed to a regulator.
   */
  subjectExport: adminProcedure('admin.governance.export')
    .input(
      z.object({
        userId: uuid,
        /** Why the document was produced. Recorded on the audit entry, because
         *  "somebody exported a person's data map" without a reason is the
         *  entry an investigation cannot use. Eight characters minimum, the
         *  same floor the money routes use for the same reason. */
        reason: z.string().trim().min(8).max(500),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const found = await resolveSubject(db, { userId: input.userId })
        if (found.kind !== 'one') {
          throw new TRPCError({ code: 'NOT_FOUND', message: 'No account with that id.' })
        }
        const map = await subjectMap(db, found.user.id)

        // Before the document is returned and inside the transaction that
        // produces it, so a file cannot leave without the entry saying it did.
        // High, for what it is: a document naming an individual, leaving.
        await adminAudit(db, c, {
          action: 'governance.subject_exported',
          targetType: 'user',
          targetId: found.user.id,
          severity: 'high',
          detail: {
            subject: found.user.github_login,
            reason: input.reason,
            locations: map.length,
          },
        })

        const document = JSON.stringify(
          {
            document: 'subject data map',
            about:
              'Where this installation holds data about one person, and what an erasure would ' +
              'do to each place. It is not a copy of the data itself: this product builds one ' +
              'data export, and it is scoped to an organization rather than to a person.',
            producedAt: c.clock.now().toISOString(),
            producedBy: c.admin.email,
            reason: input.reason,
            subject: {
              id: found.user.id,
              githubLogin: found.user.github_login,
              email: found.user.email,
              name: found.user.name,
              createdAt: iso(found.user.created_at),
            },
            organizations: found.organizations,
            countCeiling: COUNT_CEILING,
            locations: map,
            retainedByDesign: RETAINED_BY_DESIGN,
            erasure: ERASURE_STATEMENT,
          },
          null,
          2,
        )

        return {
          filename: `subject-${found.user.github_login}.json`,
          contentType: 'application/json',
          subject: found.user.github_login,
          locations: map.length,
          document,
        }
      })
    }),
})

/**
 * What erasing this person would actually do, stated in the document.
 *
 * NOT A POLICY AND NOT A PROMISE. Every sentence here is a fact about code that
 * exists in this repository, and each one was checked against it rather than
 * remembered:
 *
 *   there is no statement anywhere in the product that deletes a `users` row,
 *   so there is no per person erasure to describe;
 *   the organization erasure that DOES exist ends in
 *   `DELETE FROM organizations`, which cascades to every organization scoped
 *   table and leaves `users` alone;
 *   and the audit chains keep the actor label deliberately, because it is
 *   hashed into the entry.
 *
 * Written here rather than as copy on the page because it belongs in the FILE.
 * A page can be read alongside its caveats; a document that leaves the building
 * has to carry them.
 */
const ERASURE_STATEMENT = {
  perSubject:
    'Not implemented. No code path in this product deletes a users row, so there is no ' +
    'operation to run and nothing here should be read as one. Implementing it would need a ' +
    'deletion that walks the locations listed above in foreign key order, a decision about ' +
    'every reference whose on-delete is "set null" rather than "cascade", and an answer for ' +
    'the audit chains, which cannot be rewritten without breaking their hashes.',
  perOrganization:
    'Implemented and running. An organization can be erased through the deletion pipeline: it ' +
    'stops work, cancels the subscription, waits out the paid period, revokes every credential, ' +
    'produces a downloadable export, and then deletes the organization row, which cascades to ' +
    'every table scoped to it. See the erasure requests section of Data Governance.',
  residue:
    'Erasing the last organization somebody belonged to leaves their users row behind, holding ' +
    'their GitHub login, email address, display name and avatar URL. Those accounts are listed ' +
    'under orphaned accounts.',
  retention:
    'There is no retention policy table in this schema, so nothing expires on a schedule and no ' +
    'policy is enforced anywhere. The two time limits that do exist are specific: a deletion ' +
    'export stays downloadable for seven days and is then destroyed, and artifacts carry a ' +
    'single retained flag. A working retention policy would need a table of rules, a sweeper ' +
    'that reads it, and a record of what each run deleted.',
} as const

// ---------------------------------------------------------------------------
// The audit chain's export, and its verification
//
// These two are NOT in the router above. They belong to `admin.audit.*`, which
// is where audit.list already lives and where the console already looks, and
// moving a route somebody already calls in order to file it more tidily breaks
// the portal to tidy a directory. They are exported as a plain object and
// spread into the audit router in router.ts, which is a visible one line edit
// there rather than an invisible second mount.
// ---------------------------------------------------------------------------

/** The columns an export carries. prev_hash and entry_hash are included on
 *  purpose: without them the file cannot be verified by anybody but us, and
 *  tamper evidence only the vendor can check is not evidence. */
interface ExportRow extends Record<string, unknown> {
  seq: string
  actor_label: string
  action: string
  target_type: string
  target_id: string | null
  subject_org_label: string | null
  origin: string
  severity: string
  detail: unknown
  occurred_at: Date | string
  prev_hash: string | null
  entry_hash: string
}

const EXPORT_FIELDS = [
  'seq',
  'occurredAt',
  'actor',
  'action',
  'targetType',
  'targetId',
  'organization',
  'origin',
  'severity',
  'detail',
  'prevHash',
  'entryHash',
] as const

/** One field of one CSV row. RFC 4180 quoting: a field containing a quote, a
 *  comma or a newline is quoted and its quotes are doubled. Written out rather
 *  than reached for from a package, because this is the whole of it. */
function csvField(value: unknown): string {
  const text =
    value === null || value === undefined
      ? ''
      : typeof value === 'string'
        ? value
        : JSON.stringify(value)
  return /["\n\r,]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text
}

export const auditChainRoutes = {
  /**
   * Whether the operator chain still hashes to what it says it does.
   *
   * WIRING SOMETHING THAT WAS ALREADY WRITTEN. verifyAdminAuditChain has
   * existed in @antifailure/db since the chain did, is tested, and had ZERO
   * production call sites: nothing outside a test had ever asked it a question.
   * A tamper evident log whose tamper evidence nobody can run is a log with a
   * comment about tamper evidence.
   *
   * Gated on admin.audit.export rather than admin.audit.read, matching that
   * permission's own description, which says export "and verify its hashes".
   * Reading the log is oversight and every role has it; running the verifier
   * walks the whole chain and is the act of answering for it.
   */
  verify: adminProcedure('admin.audit.export')
    .input(
      z
        .object({
          fromSeq: z.number().int().positive().optional(),
          toSeq: z.number().int().positive().optional(),
        })
        .default({}),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const report = await verifyAdminAuditChain(db, {
          fromSeq: input.fromSeq ?? null,
          toSeq: input.toSeq ?? null,
        })
        return {
          ok: report.ok,
          entries: report.entries,
          firstSeq: report.firstSeq,
          lastSeq: report.lastSeq,
          head: report.head,
          problems: report.problems,
        }
      })
    }),

  /**
   * The operator chain as a file, with a verification of the same rows.
   *
   * A MUTATION, and it changes nothing about the chain except by adding one
   * entry to it. That is the correct shape rather than a pedantic one: an
   * export is a file of every operator action leaving the system, it is the
   * kind of act this log exists to record, and a tRPC mutation is the only
   * shape that gets the CSRF check in server.ts. The tenant side made the same
   * call for the same reason: `audit.export` there is a mutation too.
   *
   * VERIFIED OVER THE EXPORTED RANGE, not over the whole chain. A slice of the
   * history shipped alongside a verification of something larger is a
   * verification of a different document from the one in the reader's hands.
   * The range walk seeds itself from the hash of the entry before the range, so
   * an intact slice reports intact rather than reporting its own first row as a
   * broken link.
   */
  export: adminProcedure('admin.audit.export')
    .input(
      z.object({
        format: z.enum(['json', 'csv']).default('json'),
        /** The ceiling is a real ceiling and the response says whether it was
         *  reached, so an export that stopped early can never be read as the
         *  whole chain. */
        limit: z.number().int().min(1).max(10_000).default(1_000),
        fromSeq: z.number().int().positive().optional(),
        toSeq: z.number().int().positive().optional(),
        severity: z.enum(['info', 'notice', 'high', 'critical']).optional(),
        orgId: z.string().uuid().optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const from = input.fromSeq ?? null
      const to = input.toSeq ?? null

      return c.adminDb(async (db) => {
        // Ascending, because a hash chain read backwards is a list and read
        // forwards is a chain. The file is meant to be verifiable in the order
        // it was written.
        const rows = await db.execute<ExportRow>(sql`
          SELECT seq, actor_label, action, target_type, target_id, subject_org_label,
                 origin, severity, detail, occurred_at, prev_hash, entry_hash
          FROM admin_audit_entries
          WHERE (${from}::bigint IS NULL OR seq >= ${from})
            AND (${to}::bigint IS NULL OR seq <= ${to})
            AND (${input.severity ?? null}::text IS NULL OR severity = ${input.severity ?? null})
            AND (${input.orgId ?? null}::uuid IS NULL OR subject_org_id = ${input.orgId ?? null}::uuid)
          ORDER BY seq ASC
          LIMIT ${input.limit}`)

        const first = rows.length > 0 ? Number(rows[0]!.seq) : null
        const last = rows.length > 0 ? Number(rows[rows.length - 1]!.seq) : null

        // Verified over the range the file actually covers. A filtered export
        // is a SUBSET of a contiguous range, so the verification covers the
        // range and says so: the chain between those two sequence numbers is
        // intact or it is not, whether or not every entry in it was exported.
        const report =
          first === null || last === null
            ? { ok: true, entries: 0, head: null, firstSeq: null, lastSeq: null, problems: [] }
            : await verifyAdminAuditChain(db, { fromSeq: first, toSeq: last })

        const entries = rows.map((r) => ({
          seq: Number(r.seq),
          occurredAt: iso(r.occurred_at)!,
          actor: r.actor_label,
          action: r.action,
          targetType: r.target_type,
          targetId: r.target_id,
          organization: r.subject_org_label,
          origin: r.origin,
          severity: r.severity,
          detail: r.detail,
          prevHash: r.prev_hash,
          entryHash: r.entry_hash,
        }))

        const truncated = rows.length === input.limit
        const document =
          input.format === 'csv'
            ? [
                EXPORT_FIELDS.join(','),
                ...entries.map((e) =>
                  EXPORT_FIELDS.map((f) => csvField(e[f as keyof typeof e])).join(','),
                ),
              ].join('\n')
            : JSON.stringify(
                {
                  chain: 'admin_audit_entries',
                  exportedAt: c.clock.now().toISOString(),
                  exportedBy: c.admin.email,
                  filters: {
                    fromSeq: from,
                    toSeq: to,
                    severity: input.severity ?? null,
                    orgId: input.orgId ?? null,
                    limit: input.limit,
                  },
                  entryCount: entries.length,
                  truncated,
                  verification: {
                    ok: report.ok,
                    coveredFromSeq: report.firstSeq,
                    coveredToSeq: report.lastSeq,
                    entriesWalked: report.entries,
                    head: report.head,
                    problems: report.problems,
                  },
                  entries,
                },
                null,
                2,
              )

        // The export records itself, in the same transaction that produced it,
        // so a file cannot leave without the entry saying it did. Written after
        // the rows are read because the entry has to name how many, and before
        // the transaction commits because the operator must not receive the
        // document unless the record commits with it.
        //
        // HIGH rather than notice. This is a file of every operator action on
        // the installation leaving the system, held by two roles precisely
        // because of what it is, and it is one of the first entries an incident
        // review wants to see.
        await adminAudit(db, c, {
          action: 'audit.exported',
          targetType: 'admin_audit_chain',
          targetId: first === null ? null : `${first}-${last}`,
          severity: 'high',
          detail: {
            format: input.format,
            entries: entries.length,
            truncated,
            fromSeq: from,
            toSeq: to,
            severity: input.severity ?? null,
            verified: report.ok,
          },
        })

        return {
          format: input.format,
          filename: `admin-audit-${first ?? 0}-${last ?? 0}.${input.format}`,
          contentType: input.format === 'csv' ? 'text/csv' : 'application/json',
          entryCount: entries.length,
          truncated,
          firstSeq: first,
          lastSeq: last,
          verification: {
            ok: report.ok,
            entriesWalked: report.entries,
            problems: report.problems,
          },
          document,
        }
      })
    }),
}

// ---------------------------------------------------------------------------
// The subject data map
// ---------------------------------------------------------------------------

interface SubjectUser extends Record<string, unknown> {
  id: string
  github_login: string
  email: string | null
  name: string | null
  created_at: Date | string
}

type Resolved =
  | { kind: 'one'; user: SubjectUser; organizations: { slug: string; role: string }[] }
  | { kind: 'candidates'; rows: { id: string; githubLogin: string; email: string | null; name: string | null }[] }

/**
 * Finds exactly one person, or offers the ones it found.
 *
 * Exact before fuzzy, and that ordering is what stops a lookup for
 * `sam@example.com` returning `samuel@example.com` alongside it and leaving the
 * operator to pick. An exact match on an address or a login is one person, and
 * anything else is a search that has to be narrowed by a human before a data
 * map with somebody's name on it is produced.
 */
async function resolveSubject(
  db: Db,
  input: { userId?: string; query?: string },
): Promise<Resolved> {
  const exact = await db.execute<SubjectUser>(sql`
    SELECT id, github_login, email, name, created_at FROM users
    WHERE (${input.userId ?? null}::uuid IS NOT NULL AND id = ${input.userId ?? null}::uuid)
       OR (${input.query ?? null}::text IS NOT NULL
           AND (lower(email) = lower(${input.query ?? null})
                OR lower(github_login) = lower(${input.query ?? null})))
    LIMIT 2`)

  if (exact.length === 1) {
    const user = exact[0]!
    const organizations = await db.execute<{ slug: string; role: string }>(sql`
      SELECT o.slug, m.role FROM members m
      JOIN organizations o ON o.id = m.org_id
      WHERE m.user_id = ${user.id}::uuid
      ORDER BY o.slug ASC`)
    return { kind: 'one', user, organizations }
  }

  const like = `%${(input.query ?? '').replace(/[%_\\]/g, (ch) => `\\${ch}`)}%`
  const candidates = await db.execute<SubjectUser>(sql`
    SELECT id, github_login, email, name, created_at FROM users
    WHERE ${input.query ?? null}::text IS NOT NULL
      AND (email ILIKE ${like} OR github_login ILIKE ${like} OR name ILIKE ${like})
    ORDER BY github_login ASC LIMIT 10`)

  return {
    kind: 'candidates',
    rows: candidates.map((r) => ({
      id: r.id,
      githubLogin: r.github_login,
      email: r.email,
      name: r.name,
    })),
  }
}

/** What Postgres will do to a referencing row when the person is deleted. */
type OnDelete = 'cascade' | 'set null' | 'set default' | 'restrict' | 'no action'

const ON_DELETE: Record<string, OnDelete> = {
  c: 'cascade',
  n: 'set null',
  d: 'set default',
  r: 'restrict',
  a: 'no action',
}

/**
 * The ceiling on every count in the map.
 *
 * A COUNT WITH A CEILING RATHER THAN AN EXACT ONE, and it is not laziness. Some
 * of these columns have no index, and `runs` and `workload_runs` are the
 * tables an operator page must never be able to sequentially scan while
 * customers are using the database. The bounded form stops at the ceiling, so
 * the worst case is fixed however large the table is, and the page reports
 * "1000 or more" rather than a number it did not really count. An operator
 * answering an erasure request needs to know a table holds data about somebody;
 * whether that is 1,204 rows or 1,205 changes nothing they will do.
 */
const COUNT_CEILING = 1000

/** A table and column name from the catalog is not user input, but it is
 *  interpolated as SQL, so it is checked against the shape Postgres allows an
 *  unquoted identifier to have and anything else is skipped rather than run. */
const IDENTIFIER = /^[a-z_][a-z0-9_]*$/

export interface SubjectMapEntry {
  table: string
  column: string
  onDelete: OnDelete
  rows: number
  /** True when the count stopped at the ceiling rather than at the end. */
  atLeast: boolean
}

async function subjectMap(db: Db, userId: string): Promise<SubjectMapEntry[]> {
  // Every single-column foreign key that points at users(id), read from the
  // catalog at request time. Multi-column keys are excluded because none exists
  // and a two column key would need a different count; if one is ever added it
  // is absent from this list rather than silently miscounted.
  const references = await db.execute<{
    table_name: string
    column_name: string
    on_delete: string
  }>(sql`
    SELECT c.conrelid::regclass::text AS table_name,
           a.attname AS column_name,
           c.confdeltype AS on_delete
    FROM pg_constraint c
    JOIN pg_attribute a ON a.attrelid = c.conrelid AND a.attnum = c.conkey[1]
    WHERE c.contype = 'f'
      AND c.confrelid = 'users'::regclass
      AND array_length(c.conkey, 1) = 1
    ORDER BY 1, 2`)

  const out: SubjectMapEntry[] = []
  for (const ref of references) {
    // `conrelid::regclass` schema-qualifies only when the table is outside the
    // search path, which none of these are. Anything that does not look like a
    // bare identifier is skipped rather than interpolated.
    if (!IDENTIFIER.test(ref.table_name) || !IDENTIFIER.test(ref.column_name)) continue
    const counted = await db.execute<{ n: string }>(sql`
      SELECT count(*)::text AS n FROM (
        SELECT 1 FROM ${sql.raw(ref.table_name)}
        WHERE ${sql.raw(ref.column_name)} = ${userId}::uuid
        LIMIT ${COUNT_CEILING}
      ) bounded`)
    const rows = num(counted[0]?.n)
    out.push({
      table: ref.table_name,
      column: ref.column_name,
      onDelete: ON_DELETE[ref.on_delete] ?? 'no action',
      rows,
      atLeast: rows >= COUNT_CEILING,
    })
  }
  return out
}

/**
 * The places a person's identity survives an erasure on purpose.
 *
 * NOT A GAP AND NOT AN OVERSIGHT, which is why it is a named constant with the
 * reason attached rather than a missing row in the map above. `actor_label` in
 * both audit chains is TEXT with no foreign key, and it is one of the fields
 * folded into `auditEntryHash`. Rewriting it to erase somebody would change a
 * hashed field, and the verifier would then report every one of those entries
 * as `altered`, which is its word for tampered. The chain would stop being able
 * to tell "somebody edited history" from "nothing happened", on the entries
 * most likely to matter.
 *
 * So an erasure request against this product cannot include the audit trail,
 * and the honest thing is to say that to the person asking rather than to
 * quietly leave it off a list of what is held.
 *
 * These are not counted. Neither column is indexed, and counting them would be
 * a sequential scan of the two largest append-only tables in the database from
 * an operator page. The FACT is what answers the question; the number does not
 * change the answer.
 */
const RETAINED_BY_DESIGN = [
  {
    table: 'audit_entries',
    column: 'actor_label',
    why:
      'Each customer\'s own hash-chained log records who acted as free text, with no foreign key, ' +
      'so the record survives the account. The label is one of the fields the entry hash covers, ' +
      'so changing it would make the chain report itself as tampered with.',
  },
  {
    table: 'admin_audit_entries',
    column: 'actor_label',
    why:
      'The operator chain records the same way, for the same reason. It is also where an ' +
      'operator looking this person up is recorded, including this lookup.',
  },
  {
    table: 'organization_deletions',
    column: 'requested_by_label',
    why:
      'Who asked for an organization to be erased is kept as text after the organization and its ' +
      'members are gone, because a deletion record with no requester is a deletion nobody can ' +
      'account for.',
  },
] as const

/** The row shape stepOf reads, named here so the query above and the derivation
 *  cannot drift. Structurally the DeletionRow that enterprise/deletion.ts
 *  declares; that interface is not exported, and duplicating the FIELDS is
 *  safe in a way duplicating the DERIVATION would not be. */
interface DeletionRecord extends Record<string, unknown> {
  id: string
  org_id: string
  org_slug: string
  org_name: string
  requested_by_label: string
  requested_at: Date | string
  reason: string | null
  work_stopped_at: Date | string | null
  environments_stopped: number | null
  runs_cancelled: number | null
  subscription_cancelled_at: Date | string | null
  subscription_id: string | null
  entitlement_ends_at: Date | string | null
  credentials_revoked_at: Date | string | null
  engine_tokens_revoked: number | null
  provider_keys_revoked: number | null
  sessions_revoked: number | null
  installations_revoked: number | null
  exported_at: Date | string | null
  purged_at: Date | string | null
  cancelled_at: Date | string | null
  last_error_at: Date | string | null
  last_error_step: string | null
  last_error_message: string | null
  attempts: number
  export_expires_at: Date | string | null
  export_size_bytes: string | null
  export_destroyed_at: Date | string | null
  export_downloads: number | null
}
