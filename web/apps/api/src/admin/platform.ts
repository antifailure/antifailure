// The Developer Platform lane of the operator portal.
//
// WHAT THIS FILE IS FOR. The portal has six navigation groups and they were
// built in parallel. One module per group, mounted once, is what let that
// happen without two people editing the same file: the Developer Platform
// sections own THIS file and console/app/admin/platform, and nothing else. A
// lane that added its routes to router.ts instead would put six writers in one
// object literal, and a duplicate key in an object literal is the one merge
// conflict git does not report.
//
// THE SECTIONS THIS LANE OWNS: Repositories & Pull Requests, MCP Management,
// API Keys, and Integrations & Webhooks.
//
// PERMISSION PREFIXES RESERVED FOR IT: admin.repos.*, admin.keys.*,
// admin.webhooks.*, admin.mcp.*, admin.deploys.*. The catalog and the
// reservations are in permissions.ts, and adding a permission under another
// lane's prefix is the kind of thing a review misses.
//
// A KEY IS NEVER RETURNED BY A ROUTE IN THIS FILE. An operator needs to know
// that a credential exists, when it was last used and how to revoke it. The
// value itself answers no operator question and holding it is the whole risk.
// Only the hash is stored, so there is nothing here that could return one even
// if a route asked.
//
// WHAT IS AND IS NOT HERE, in the shape router.ts states for its own three
// writes. Reads across every tenant, and two writes: revoke a credential, and
// revoke an OIDC repository binding. Both were chosen because the enforcement
// already exists somewhere a test can watch it:
//
//   revoke a token    -> engine_tokens.revoked_at, read by authenticateEngine
//                        in ingest.ts on every POST /v1/events before anything
//                        else, so the next event from that credential is
//                        refused.
//   revoke a binding  -> oidc_repository_bindings.revoked_at, read by
//                        src/github/exchange.ts before it mints anything, and
//                        the same statement revokes the tokens that binding
//                        already produced, because a revocation that leaves
//                        live credentials behind is not a revocation.
//
// WHAT IS DELIBERATELY ABSENT, AND WHY IT IS NOT A GAP.
//
// There is no rotate button. Rotation means minting a replacement, and a
// replacement is a secret: an operator route that minted one would have to
// return it through this portal, and a portal that can display a customer's
// credential is a portal that can leak one. Only the hash is stored, so there
// is no route anywhere in this product that can show a token after the moment
// it was created. The honest operator action is revoke, and the customer mints
// the replacement themselves with `af token create`. The console says exactly
// that rather than offering a button that would have to lie.
//
// There is no outbound webhook anything. This product has no outbound webhook
// subscription table, no delivery attempt table for one, and no signing secret
// store. What it has is two INBOUND delivery ledgers, github_deliveries and
// billing_events, which record what arrived here and whether it was handled. A
// delivery log is what an operator opens this page for; drawing an outbound one
// over an inbound table would answer the wrong question confidently.
//
// SCIM tokens are not listed. scim_tokens has existed since 0014 and nothing
// under src/ reads it, so a SCIM token authenticates nothing today. Listing one
// beside credentials that do work would present a row that cannot be used as
// though revoking it mattered.

import { z } from 'zod'
import { sql } from 'drizzle-orm'
import { TRPCError } from '@trpc/server'
import { router } from '../trpc.ts'
import { adminProcedure, adminAudit, type AdminContext } from './trpc.ts'
import {
  MCP_ELSEWHERE,
  MCP_REGISTRATION_FILE,
  MCP_TOOLS,
  MCP_UNKNOWN_FIELD_REFUSAL,
} from './mcp.ts'

/**
 * A page of rows, and how to ask for the next one.
 *
 * A local copy of router.ts's helper rather than an import, and the reason is
 * structural: router.ts imports this module to mount it, so importing back from
 * it would be a cycle. Nine lines duplicated is the cheaper of the two, and the
 * shape is pinned by the same tests either way.
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

function iso(value: Date | string): string {
  return value instanceof Date ? value.toISOString() : new Date(value).toISOString()
}

function isoOrNull(value: Date | string | null): string | null {
  return value === null ? null : iso(value)
}

/**
 * A keyset cursor over two columns.
 *
 * Two rather than one because none of the sort keys on this surface is unique
 * on its own. Two organizations can both own `acme/app`, two tokens can be
 * created in the same millisecond, and a delivery ledger receives in bursts. A
 * single column cursor over any of those skips rows or repeats them, and the
 * reader cannot tell which.
 *
 * A tab is the separator because no value on either side of one can contain a
 * tab: a GitHub full name is `owner/name` from GitHub's own character set, a
 * timestamp is ISO 8601, and the second half is always an identifier.
 */
const CURSOR_SEPARATOR = '\t'

/* Both halves fall back to NULL rather than to an empty string wherever one of
 * these is interpolated. A bound parameter is cast before the guard beside it is
 * evaluated, so `''::timestamptz` raises 22007 on the very first page, when
 * there is no cursor at all, even though the comparison it belongs to is
 * unreachable. NULL casts to anything. */
function splitCursor(cursor: string | null | undefined): { a: string; b: string } | null {
  if (!cursor) return null
  const at = cursor.indexOf(CURSOR_SEPARATOR)
  if (at < 0) return null
  return { a: cursor.slice(0, at), b: cursor.slice(at + 1) }
}

function joinCursor(a: string, b: string): string {
  return `${a}${CURSOR_SEPARATOR}${b}`
}

const page = z.object({
  limit: z.number().int().min(1).max(200).default(50),
  cursor: z.string().nullish(),
})

const uuid = z.string().uuid()

/**
 * Every credential write says why, in at least eight characters.
 *
 * The stricter minimum from routers.ts rather than the min(1) on the tenant
 * routes, and deliberately so: revoking a customer's credential stops their
 * pipeline within seconds, and "x" as a reason is a row that cannot be
 * defended to the customer who asks the next morning.
 */
const reason = z.string().trim().min(8).max(500)

// Every write below reads the organization's slug on the same row it is acting
// on, rather than through a second lookup. subject_org_label is what still
// names the tenant a year from now when the row is gone, and reading it from
// the join the write already needs is one query instead of two.

// ---------------------------------------------------------------------------
// Repositories and pull requests
// ---------------------------------------------------------------------------
//
// The question this answers: a customer says the check on one pull request
// never appeared, and support has a repository name and a number. The path is
// repository, then its pull requests, then that pull request's generations,
// because a generation is where the answer lives. state and detail together say
// what happened in one sentence, which is what pr_generations.detail was
// written to hold.

const repositoriesRouter = router({
  list: adminProcedure('admin.repos.read')
    .input(page.extend({ query: z.string().trim().max(200).optional() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const key = splitCursor(input.cursor)
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          full_name: string
          default_branch: string
          private: boolean
          archived_at: Date | string | null
          created_at: Date | string
          open_pull_requests: string
          last_pull_request_at: Date | string | null
        }>(sql`
          SELECT r.id, r.org_id, o.slug AS org_slug, r.full_name, r.default_branch,
                 r.private, r.archived_at, r.created_at,
                 (SELECT count(*) FROM pull_requests p
                   WHERE p.repository_id = r.id AND p.state = 'open') AS open_pull_requests,
                 (SELECT max(p.updated_at) FROM pull_requests p
                   WHERE p.repository_id = r.id) AS last_pull_request_at
          FROM repositories r
          JOIN organizations o ON o.id = r.org_id
          WHERE (${input.query ?? null}::text IS NULL
                 OR r.full_name ILIKE ${'%' + (input.query ?? '') + '%'}
                 OR o.slug ILIKE ${'%' + (input.query ?? '') + '%'})
            AND (${key?.a ?? null}::text IS NULL
                 OR (r.full_name, r.id::text) > (${key?.a ?? null}, ${key?.b ?? null}))
          ORDER BY r.full_name ASC, r.id ASC
          LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => joinCursor(r.full_name, r.id),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          fullName: r.full_name,
          defaultBranch: r.default_branch,
          private: r.private,
          archived: r.archived_at !== null,
          createdAt: iso(r.created_at),
          openPullRequests: Number(r.open_pull_requests),
          lastPullRequestAt: isoOrNull(r.last_pull_request_at),
        }),
      )
    }),

  get: adminProcedure('admin.repos.read')
    .input(z.object({ repositoryId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          org_name: string
          full_name: string
          default_branch: string
          private: boolean
          github_id: string | null
          archived_at: Date | string | null
          created_at: Date | string
        }>(sql`
          SELECT r.id, r.org_id, o.slug AS org_slug, o.name AS org_name, r.full_name,
                 r.default_branch, r.private, r.github_id::text AS github_id,
                 r.archived_at, r.created_at
          FROM repositories r
          JOIN organizations o ON o.id = r.org_id
          WHERE r.id = ${input.repositoryId}::uuid`),
      )
      const row = rows[0]
      if (!row) {
        throw new TRPCError({ code: 'NOT_FOUND', message: 'No repository with that id.' })
      }
      // The installations are a SECOND query rather than a join, because an
      // organization can hold more than one and a join would have paired the
      // repository with whichever one sorted first. An empty array is a real
      // and important state: it is the first thing to check when a customer
      // reports that nothing arrives, since a repository row survives an
      // installation being removed.
      const installations = await c.adminDb(async (db) =>
        db.execute<{
          account_login: string
          account_type: string
          suspended_at: Date | string | null
        }>(sql`
          SELECT account_login, account_type, suspended_at
          FROM github_installations
          WHERE org_id = ${row.org_id}::uuid
          ORDER BY account_login ASC
          LIMIT 20`),
      )
      return {
        id: row.id,
        orgId: row.org_id,
        orgSlug: row.org_slug,
        orgName: row.org_name,
        fullName: row.full_name,
        defaultBranch: row.default_branch,
        private: row.private,
        githubId: row.github_id,
        archived: row.archived_at !== null,
        createdAt: iso(row.created_at),
        installations: installations.map((g) => ({
          accountLogin: g.account_login,
          accountType: g.account_type,
          suspended: g.suspended_at !== null,
        })),
      }
    }),

  /**
   * One repository's pull requests, with the newest generation attached.
   *
   * The generation comes back on the same row through a LATERAL rather than as
   * a second round trip, because the question is "which of these is stuck" and
   * that cannot be answered from the pull request columns alone. One extra
   * query per row from the client would be fifty queries to read one screen.
   */
  pullRequests: adminProcedure('admin.repos.read')
    .input(
      page.extend({
        repositoryId: uuid,
        state: z.enum(['open', 'closed', 'merged']).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          number: number
          title: string | null
          state: string
          draft: boolean
          from_fork: boolean
          head_ref: string
          base_ref: string
          head_repository: string
          head_sha: string
          approved_sha: string | null
          approved_by: string | null
          approved_at: Date | string | null
          opened_at: Date | string | null
          updated_at: Date | string
          generation_state: string | null
          generation_detail: string | null
          generation_attempt: number | null
          generation_head_sha: string | null
          generation_verdict: unknown
          generation_updated_at: Date | string | null
        }>(sql`
          SELECT p.id, p.number, p.title, p.state, p.draft, p.from_fork,
                 p.head_ref, p.base_ref, p.head_repository, p.head_sha,
                 p.approved_sha, p.approved_by, p.approved_at,
                 p.opened_at, p.updated_at,
                 g.state::text AS generation_state, g.detail AS generation_detail,
                 g.attempt AS generation_attempt, g.head_sha AS generation_head_sha,
                 g.verdict AS generation_verdict, g.updated_at AS generation_updated_at
          FROM pull_requests p
          LEFT JOIN LATERAL (
            SELECT gg.state, gg.detail, gg.attempt, gg.head_sha, gg.verdict, gg.updated_at
            FROM pr_generations gg
            WHERE gg.pull_request_id = p.id
            ORDER BY gg.updated_at DESC
            LIMIT 1
          ) g ON true
          WHERE p.repository_id = ${input.repositoryId}::uuid
            AND (${input.state ?? null}::text IS NULL OR p.state = ${input.state ?? ''})
            AND (${input.cursor ?? null}::text IS NULL
                 OR p.number < ${input.cursor ? Number(input.cursor) : 0})
          ORDER BY p.number DESC
          LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => String(r.number),
        (r) => ({
          id: r.id,
          number: r.number,
          title: r.title,
          state: r.state,
          draft: r.draft,
          fromFork: r.from_fork,
          headRef: r.head_ref,
          baseRef: r.base_ref,
          headRepository: r.head_repository,
          headSha: r.head_sha,
          // The approval is of a COMMIT, never of the pull request, so it is
          // reported beside the current head rather than as a boolean. An
          // approval that no longer matches the head is the state the column
          // exists to make visible.
          approvedSha: r.approved_sha,
          approvedBy: r.approved_by,
          approvedAt: isoOrNull(r.approved_at),
          approvalCoversHead: r.approved_sha !== null && r.approved_sha === r.head_sha,
          openedAt: isoOrNull(r.opened_at),
          updatedAt: iso(r.updated_at),
          latestGeneration:
            r.generation_state === null
              ? null
              : {
                  state: r.generation_state,
                  detail: r.generation_detail,
                  attempt: r.generation_attempt ?? 1,
                  headSha: r.generation_head_sha,
                  verdict: r.generation_verdict,
                  updatedAt: isoOrNull(r.generation_updated_at),
                },
        }),
      )
    }),

  /** Every generation for one pull request, newest first.
   *
   *  Bounded rather than paged, and the bound is safe because of a UNIQUE the
   *  schema already carries: one row per (pull_request_id, head_sha), with
   *  `attempt` bumped in place when somebody presses Re-run. So fifty rows
   *  means fifty distinct heads, and a pull request that has pushed more than
   *  fifty commits with a check on each is itself the finding. */
  generations: adminProcedure('admin.repos.read')
    .input(z.object({ pullRequestId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          head_sha: string
          attempt: number
          state: string
          detail: string | null
          check_run_id: string | null
          workflow_run_id: string | null
          reported_by: string | null
          env_id: string | null
          verdict: unknown
          superseded_by: string | null
          queued_at: Date | string | null
          started_at: Date | string | null
          finished_at: Date | string | null
          deadline_at: Date | string | null
          updated_at: Date | string
        }>(sql`
          SELECT id, head_sha, attempt, state::text AS state, detail,
                 check_run_id::text AS check_run_id,
                 workflow_run_id::text AS workflow_run_id,
                 reported_by, env_id, verdict, superseded_by::text AS superseded_by,
                 queued_at, started_at, finished_at, deadline_at, updated_at
          FROM pr_generations
          WHERE pull_request_id = ${input.pullRequestId}::uuid
          ORDER BY updated_at DESC
          LIMIT 50`),
      )
      return rows.map((r) => ({
        id: r.id,
        headSha: r.head_sha,
        attempt: r.attempt,
        state: r.state,
        detail: r.detail,
        // Null while the installation does not hold `checks: write`, which is
        // a state to serve rather than hide: the comment still lands and this
        // is how an operator sees which permission is missing.
        checkRunId: r.check_run_id,
        workflowRunId: r.workflow_run_id,
        reportedBy: r.reported_by,
        envId: r.env_id,
        verdict: r.verdict,
        supersededBy: r.superseded_by,
        queuedAt: isoOrNull(r.queued_at),
        startedAt: isoOrNull(r.started_at),
        finishedAt: isoOrNull(r.finished_at),
        deadlineAt: isoOrNull(r.deadline_at),
        updatedAt: iso(r.updated_at),
      }))
    }),
})

// ---------------------------------------------------------------------------
// Credentials
// ---------------------------------------------------------------------------
//
// COLUMN SAFETY IS THE WHOLE POINT ON THIS SURFACE. RLS is row level and cannot
// restrict a column, and the operator pool holds BYPASSRLS, so nothing in the
// database stops `SELECT *` here. Every query below names its columns and none
// of them names token_hash. router.ts states the same rule; this is the table
// where breaking it would be worst.
//
// What is shown instead of a secret: the prefix, which is the first twelve
// characters and is what the customer's own console shows them, so an operator
// and a customer can agree which credential they are talking about without
// either of them holding one.

const KEY_KINDS = ['engine', 'cli', 'oidc', 'mcp'] as const

const credentialsRouter = router({
  list: adminProcedure('admin.keys.read')
    .input(
      page.extend({
        query: z.string().trim().max(200).optional(),
        kind: z.enum(KEY_KINDS).optional(),
        /** Live means not revoked and not expired, evaluated against the
         *  server's clock rather than the database's, so this filter and
         *  authenticateEngine answer the same question. */
        liveOnly: z.boolean().default(false),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const key = splitCursor(input.cursor)
      const now = c.clock.now().toISOString()
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          name: string
          prefix: string
          kind: string
          scopes: string[] | null
          created_at: Date | string
          last_used_at: Date | string | null
          expires_at: Date | string | null
          revoked_at: Date | string | null
          created_by_login: string | null
          created_by_email: string | null
          acts_as_login: string | null
          binding_repository: string | null
        }>(sql`
          SELECT t.id, t.org_id, o.slug AS org_slug, t.name, t.prefix, t.kind,
                 t.scopes, t.created_at, t.last_used_at, t.expires_at, t.revoked_at,
                 c.github_login AS created_by_login, c.email AS created_by_email,
                 a.github_login AS acts_as_login,
                 b.repository AS binding_repository
          FROM engine_tokens t
          JOIN organizations o ON o.id = t.org_id
          LEFT JOIN users c ON c.id = t.created_by
          -- Who the credential ACTS AS, which is not who created it and is null
          -- for an engine token on purpose. 0012 states the reason: a machine is
          -- not a person, and putting a machine's actions in a human's audit
          -- trail is the thing the null prevents.
          LEFT JOIN users a ON a.id = t.user_id
          LEFT JOIN oidc_repository_bindings b ON b.id = t.binding_id
          WHERE (${input.query ?? null}::text IS NULL
                 OR o.slug ILIKE ${'%' + (input.query ?? '') + '%'}
                 OR t.name ILIKE ${'%' + (input.query ?? '') + '%'}
                 OR t.prefix ILIKE ${'%' + (input.query ?? '') + '%'})
            AND (${input.kind ?? null}::text IS NULL OR t.kind = ${input.kind ?? ''})
            AND (${input.liveOnly ? 1 : 0} = 0
                 OR (t.revoked_at IS NULL
                     AND (t.expires_at IS NULL OR t.expires_at > ${now}::timestamptz)))
            AND (${key?.a ?? null}::text IS NULL
                 OR (t.created_at, t.id::text) < (${key?.a ?? null}::timestamptz, ${key?.b ?? null}))
          ORDER BY t.created_at DESC, t.id DESC
          LIMIT ${input.limit + 1}`),
      )
      const nowMs = c.clock.now().getTime()
      return pageOf(
        rows,
        input.limit,
        (r) => joinCursor(iso(r.created_at), r.id),
        (r) => {
          const expired =
            r.expires_at !== null && new Date(r.expires_at).getTime() <= nowMs
          return {
            id: r.id,
            orgId: r.org_id,
            orgSlug: r.org_slug,
            name: r.name,
            prefix: r.prefix,
            kind: r.kind,
            scopes: r.scopes ?? [],
            createdAt: iso(r.created_at),
            lastUsedAt: isoOrNull(r.last_used_at),
            expiresAt: isoOrNull(r.expires_at),
            revokedAt: isoOrNull(r.revoked_at),
            expired,
            // One word for the state, computed once here, so the console does
            // not reimplement the precedence and disagree with the server about
            // which of revoked and expired wins.
            standing: r.revoked_at !== null ? 'revoked' : expired ? 'expired' : 'live',
            createdBy: r.created_by_login ?? r.created_by_email,
            actsAs: r.acts_as_login,
            bindingRepository: r.binding_repository,
          }
        },
      )
    }),

  /**
   * Stops a credential working, now.
   *
   * The effect sentence is returned by the route rather than written in the
   * console, the same way tenants.suspend does it: two sentences that mean the
   * same thing today are two sentences that disagree after somebody edits one.
   *
   * Idempotent on an already revoked token, because during an incident the same
   * action gets taken twice and the second attempt must not read as a new
   * problem. The audit entry is written only when something changed, so the
   * chain does not gain a row for an action that did nothing.
   */
  revoke: adminProcedure('admin.keys.revoke')
    .input(z.object({ tokenId: uuid, reason }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          org_id: string
          org_slug: string
          name: string
          prefix: string
          kind: string
          revoked_at: Date | string | null
        }>(sql`
          SELECT t.id, t.org_id, o.slug AS org_slug, t.name, t.prefix, t.kind, t.revoked_at
          FROM engine_tokens t JOIN organizations o ON o.id = t.org_id
          WHERE t.id = ${input.tokenId}::uuid`)
        const token = rows[0]
        if (!token) {
          throw new TRPCError({ code: 'NOT_FOUND', message: 'No credential with that id.' })
        }
        if (token.revoked_at !== null) {
          return {
            revoked: true,
            alreadyRevoked: true,
            prefix: token.prefix,
            effect: 'It was already revoked. Nothing changed and nothing was recorded.',
          }
        }

        // The record first, in the same transaction. If the UPDATE fails the
        // entry goes with it, and the UPDATE cannot commit without it.
        await adminAudit(db, c, {
          action: 'credential.revoked',
          targetType: 'engine_token',
          // The prefix, never the id alone, because the prefix is what the
          // customer sees in their own console and in `af token list`, so this
          // is the entry they can match against what stopped working.
          targetId: token.prefix,
          subjectOrgId: token.org_id,
          subjectOrgLabel: token.org_slug,
          severity: 'high',
          detail: { name: token.name, kind: token.kind, reason: input.reason },
        })
        await db.execute(sql`
          UPDATE engine_tokens SET revoked_at = ${c.clock.now().toISOString()}
          WHERE id = ${token.id}::uuid AND revoked_at IS NULL`)

        return {
          revoked: true,
          alreadyRevoked: false,
          prefix: token.prefix,
          effect:
            'The next request presenting this credential is refused. Anything already running ' +
            'keeps running until it next calls the control plane. Nobody can recover the value: ' +
            (token.kind === 'mcp'
              ? 'the customer reconnects their MCP client and approves a new credential.'
              : 'the customer creates a replacement with af token create.'),
        }
      })
    }),

  /** The GitHub OIDC repository bindings, which are what let a workflow trade
   *  its own identity for a short lived token. A binding nobody recognises is
   *  the thing somebody scrolls this list looking for. */
  bindings: adminProcedure('admin.keys.read')
    .input(page.extend({ query: z.string().trim().max(200).optional() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const key = splitCursor(input.cursor)
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          created_at: Date | string
          last_used_at: Date | string | null
          revoked_at: Date | string | null
          created_by_login: string | null
          live_tokens: string
        }>(sql`
          SELECT b.id, b.org_id, o.slug AS org_slug, b.repository, b.created_at,
                 b.last_used_at, b.revoked_at, u.github_login AS created_by_login,
                 (SELECT count(*) FROM engine_tokens t
                   WHERE t.binding_id = b.id AND t.revoked_at IS NULL) AS live_tokens
          FROM oidc_repository_bindings b
          JOIN organizations o ON o.id = b.org_id
          LEFT JOIN users u ON u.id = b.created_by
          WHERE (${input.query ?? null}::text IS NULL
                 OR b.repository ILIKE ${'%' + (input.query ?? '') + '%'}
                 OR o.slug ILIKE ${'%' + (input.query ?? '') + '%'})
            AND (${key?.a ?? null}::text IS NULL
                 OR (b.created_at, b.id::text) < (${key?.a ?? null}::timestamptz, ${key?.b ?? null}))
          ORDER BY b.created_at DESC, b.id DESC
          LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => joinCursor(iso(r.created_at), r.id),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          repository: r.repository,
          createdAt: iso(r.created_at),
          lastUsedAt: isoOrNull(r.last_used_at),
          revokedAt: isoOrNull(r.revoked_at),
          createdBy: r.created_by_login,
          // Tokens this binding has produced that are still live. The number an
          // operator needs before revoking, because revoking the binding
          // revokes them too and this says how much that will stop.
          liveTokens: Number(r.live_tokens),
        }),
      )
    }),

  /**
   * Revokes a binding and, in the same statement, every live token it minted.
   *
   * Both halves, because revoking only the binding would stop new tokens being
   * issued while the ones already issued kept working, which is the shape of a
   * revocation that does not revoke. This is what src/github/exchange.ts does
   * for the customer's own command, and doing less here would mean the operator
   * button was weaker than the one the customer already has.
   */
  revokeBinding: adminProcedure('admin.keys.revoke')
    .input(z.object({ bindingId: uuid, reason }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{
          id: string
          org_id: string
          org_slug: string
          repository: string
          revoked_at: Date | string | null
        }>(sql`
          SELECT b.id, b.org_id, o.slug AS org_slug, b.repository, b.revoked_at
          FROM oidc_repository_bindings b JOIN organizations o ON o.id = b.org_id
          WHERE b.id = ${input.bindingId}::uuid`)
        const binding = rows[0]
        if (!binding) {
          throw new TRPCError({ code: 'NOT_FOUND', message: 'No binding with that id.' })
        }
        if (binding.revoked_at !== null) {
          return {
            revoked: true,
            alreadyRevoked: true,
            repository: binding.repository,
            tokensRevoked: 0,
            effect: 'It was already revoked. Nothing changed and nothing was recorded.',
          }
        }

        const now = c.clock.now().toISOString()
        await adminAudit(db, c, {
          action: 'oidc_binding.revoked',
          targetType: 'oidc_repository_binding',
          targetId: binding.id,
          subjectOrgId: binding.org_id,
          subjectOrgLabel: binding.org_slug,
          severity: 'high',
          detail: { repository: binding.repository, reason: input.reason },
        })
        await db.execute(sql`
          UPDATE oidc_repository_bindings SET revoked_at = ${now}
          WHERE id = ${binding.id}::uuid AND revoked_at IS NULL`)
        const killed = await db.execute<{ id: string }>(sql`
          UPDATE engine_tokens SET revoked_at = ${now}
          WHERE binding_id = ${binding.id}::uuid AND revoked_at IS NULL
          RETURNING id`)

        return {
          revoked: true,
          alreadyRevoked: false,
          repository: binding.repository,
          tokensRevoked: killed.length,
          effect:
            'No workflow in that repository can trade its identity for a token again, and every ' +
            'token this binding had already minted stops working on its next request.',
        }
      })
    }),
})

// ---------------------------------------------------------------------------
// Integrations and inbound webhooks
// ---------------------------------------------------------------------------
//
// Two ledgers with the same shape, kept as two routes rather than one union.
// They record different things from different senders with different identifier
// spaces, and a union would need a discriminator that the console would then
// have to branch on anyway. Two routes say which sender is being asked about.

const DELIVERY_SOURCES = ['github', 'stripe'] as const

/**
 * One row of either ledger, in one shape.
 *
 * The two SELECTs below are written to produce exactly these columns, aliasing
 * where the tables disagree, so the mapping afterwards is one function rather
 * than two that drift. Stripe has no `action`, which is the one place the two
 * ledgers genuinely differ, and it is selected as a literal null rather than
 * omitted so the shape holds.
 */
interface DeliveryRow extends Record<string, unknown> {
  id: string
  org_slug: string | null
  account: string | null
  event: string
  action: string | null
  received_at: Date | string
  handled_at: Date | string | null
  outcome: string | null
}

const integrationsRouter = router({
  /** Every GitHub App installation, with the repositories it covers. The first
   *  thing to check when a customer reports that nothing arrives. */
  installations: adminProcedure('admin.webhooks.read')
    .input(page.extend({ query: z.string().trim().max(200).optional() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const key = splitCursor(input.cursor)
      const rows = await c.adminDb(async (db) =>
        db.execute<{
          id: string
          org_id: string
          org_slug: string
          installation_id: string
          account_login: string
          account_type: string
          suspended_at: Date | string | null
          created_at: Date | string
          repositories: string
          last_delivery_at: Date | string | null
        }>(sql`
          SELECT gi.id, gi.org_id, o.slug AS org_slug,
                 gi.installation_id::text AS installation_id,
                 gi.account_login, gi.account_type, gi.suspended_at, gi.created_at,
                 (SELECT count(*) FROM repositories r WHERE r.org_id = gi.org_id) AS repositories,
                 (SELECT max(d.received_at) FROM github_deliveries d
                   WHERE d.org_id = gi.org_id) AS last_delivery_at
          FROM github_installations gi
          JOIN organizations o ON o.id = gi.org_id
          WHERE (${input.query ?? null}::text IS NULL
                 OR gi.account_login ILIKE ${'%' + (input.query ?? '') + '%'}
                 OR o.slug ILIKE ${'%' + (input.query ?? '') + '%'})
            AND (${key?.a ?? null}::text IS NULL
                 OR (gi.account_login, gi.id::text) > (${key?.a ?? null}, ${key?.b ?? null}))
          ORDER BY gi.account_login ASC, gi.id ASC
          LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => joinCursor(r.account_login, r.id),
        (r) => ({
          id: r.id,
          orgId: r.org_id,
          orgSlug: r.org_slug,
          installationId: r.installation_id,
          accountLogin: r.account_login,
          accountType: r.account_type,
          suspended: r.suspended_at !== null,
          createdAt: iso(r.created_at),
          repositories: Number(r.repositories),
          lastDeliveryAt: isoOrNull(r.last_delivery_at),
        }),
      )
    }),

  /**
   * What arrived, and whether it was handled.
   *
   * `unhandledOnly` reads the same partial index the sweeper uses, so asking
   * for the small set costs what the sweeper's own query costs rather than a
   * scan of the ledger.
   *
   * A row with no organization is kept rather than filtered out. 0021 says why
   * the column is nullable: a delivery about an account this installation has
   * never seen resolves to nobody, and those are precisely the rows worth
   * looking at when somebody reports that their events go nowhere.
   */
  deliveries: adminProcedure('admin.webhooks.read')
    .input(
      page.extend({
        source: z.enum(DELIVERY_SOURCES),
        unhandledOnly: z.boolean().default(false),
        query: z.string().trim().max(200).optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const key = splitCursor(input.cursor)
      const like = '%' + (input.query ?? '') + '%'
      const rows = await c.adminDb(async (db) =>
        input.source === 'github'
          ? db.execute<DeliveryRow>(sql`
              SELECT d.delivery_id AS id, o.slug AS org_slug, d.account_login AS account,
                     d.event, d.action, d.received_at, d.handled_at, d.outcome
              FROM github_deliveries d
              LEFT JOIN organizations o ON o.id = d.org_id
              WHERE (${input.unhandledOnly ? 1 : 0} = 0 OR d.handled_at IS NULL)
                AND (${input.query ?? null}::text IS NULL
                     OR d.event ILIKE ${like}
                     OR d.account_login ILIKE ${like}
                     OR o.slug ILIKE ${like})
                AND (${key?.a ?? null}::text IS NULL
                     OR (d.received_at, d.delivery_id)
                        < (${key?.a ?? null}::timestamptz, ${key?.b ?? null}))
              ORDER BY d.received_at DESC, d.delivery_id DESC
              LIMIT ${input.limit + 1}`)
          : db.execute<DeliveryRow>(sql`
              SELECT b.stripe_event_id AS id, o.slug AS org_slug,
                     b.stripe_customer_id AS account,
                     b.type AS event, NULL::text AS action,
                     b.received_at, b.processed_at AS handled_at, b.outcome
              FROM billing_events b
              LEFT JOIN organizations o ON o.id = b.org_id
              -- outcome rather than processed_at, and the difference is not
              -- cosmetic. billing_events carries a partial index on
              -- outcome = 'unresolved', which is the set this filter is for, and
              -- 'unresolved' is what the webhook writes when it could not decide
              -- what an event meant. A row with a processed_at and outcome
              -- 'stale' was handled; a row with neither is the one somebody is
              -- looking for.
              WHERE (${input.unhandledOnly ? 1 : 0} = 0 OR b.outcome = 'unresolved')
                AND (${input.query ?? null}::text IS NULL
                     OR b.type ILIKE ${like}
                     OR o.slug ILIKE ${like})
                AND (${key?.a ?? null}::text IS NULL
                     OR (b.received_at, b.stripe_event_id)
                        < (${key?.a ?? null}::timestamptz, ${key?.b ?? null}))
              ORDER BY b.received_at DESC, b.stripe_event_id DESC
              LIMIT ${input.limit + 1}`),
      )
      return pageOf(
        rows,
        input.limit,
        (r) => joinCursor(iso(r.received_at), r.id),
        (r) => ({
          id: r.id,
          orgSlug: r.org_slug,
          account: r.account,
          event: r.event,
          action: r.action,
          receivedAt: iso(r.received_at),
          handledAt: isoOrNull(r.handled_at),
          outcome: r.outcome,
        }),
      )
    }),
})

// ---------------------------------------------------------------------------
// MCP
// ---------------------------------------------------------------------------

const mcpRouter = router({
  /**
   * Hosted client registrations and issued credentials, not open sockets or
   * tool executions. The separate local tool catalog is retained for callers
   * that link to the checkout protocol; those sessions send no telemetry here.
   */
  surface: adminProcedure('admin.mcp.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    const now = c.clock.now().toISOString()
    const recorded = await c.adminDb(async (db) => {
      const totals = await db.execute<{ clients: string; active: string; revoked: string; expired: string }>(sql`
        SELECT (SELECT count(*)::text FROM mcp_clients) AS clients,
          count(*) FILTER (WHERE revoked_at IS NULL AND expires_at > ${now}::timestamptz)::text AS active,
          count(*) FILTER (WHERE revoked_at IS NOT NULL)::text AS revoked,
          count(*) FILTER (WHERE revoked_at IS NULL AND expires_at <= ${now}::timestamptz)::text AS expired
FROM engine_tokens WHERE kind = 'mcp'`)
      const rows = await db.execute<{
        id: string; prefix: string; org_slug: string; client_name: string; user_login: string;
        scopes: string[]; created_at: Date | string; last_used_at: Date | string | null;
        expires_at: Date | string; revoked_at: Date | string | null;
      }>(sql`
        SELECT t.id, t.prefix, o.slug AS org_slug, cl.client_name,
          u.github_login AS user_login, t.scopes, t.created_at, t.last_used_at, t.expires_at, t.revoked_at
        FROM engine_tokens t JOIN organizations o ON o.id = t.org_id
        JOIN mcp_clients cl ON cl.client_id = t.mcp_client_id
        JOIN users u ON u.id = t.user_id
        WHERE t.kind = 'mcp'
        ORDER BY t.created_at DESC, t.id DESC LIMIT 51`)
      return {
        counts: { clients: Number(totals[0]?.clients ?? 0), active: Number(totals[0]?.active ?? 0),
          revoked: Number(totals[0]?.revoked ?? 0), expired: Number(totals[0]?.expired ?? 0) },
        hasMore: rows.length > 50,
        connections: rows.slice(0, 50).map((r) => ({
          id: r.id, prefix: r.prefix, orgSlug: r.org_slug, clientName: r.client_name,
          userLogin: r.user_login, scopes: r.scopes, createdAt: iso(r.created_at),
          lastAuthenticatedAt: isoOrNull(r.last_used_at), expiresAt: iso(r.expires_at),
          standing: r.revoked_at !== null ? 'revoked' : new Date(r.expires_at).getTime() <= c.clock.now().getTime() ? 'expired' : 'active',
        })),
      }
    })
    return {
    ...recorded,
    at: now,
    endpoint: /^https?:\/\//.test(c.appBaseUrl) ? `${c.appBaseUrl.replace(/\/+$/, '')}/mcp` : null,
    recordsAnything: true,
    why: 'Hosted OAuth registrations and credentials are recorded here. Local af mcp sessions stay on the developer\'s machine.',
    tools: MCP_TOOLS.map((t) => ({
      name: t.name,
      does: t.does,
      refuses: t.refuses,
      servedBy: t.servedBy,
    })),
    registeredIn: MCP_REGISTRATION_FILE,
    unknownFieldRefusal: MCP_UNKNOWN_FIELD_REFUSAL,
    command: MCP_ELSEWHERE.command,
    documentation: MCP_ELSEWHERE.documentation,
    }
  }),
})

/**
 * The Developer Platform namespace, mounted once at `admin.platform` by
 * admin/router.ts.
 *
 * ONE MOUNT, FOUR SECTIONS. The route path and the permission are deliberately
 * not the same word here. The path says which lane owns the file, so six agents
 * cannot land in one object literal; the permission says what the operator is
 * allowed to do, and those are different questions. `admin.platform.keys.revoke`
 * carries `admin.keys.revoke`, and a role that holds it can revoke a credential
 * whatever the module it happens to live in is called.
 *
 * Every path under this object inherits three things from the `admin.` prefix
 * it sits below without anybody arranging them: the operator route matrix walks
 * it, adminProcedure guards it, and maintenance mode exempts it so an operator
 * can still reach these during an outage.
 */
export const platformRouter = router({
  repositories: repositoriesRouter,
  keys: credentialsRouter,
  integrations: integrationsRouter,
  mcp: mcpRouter,
})
