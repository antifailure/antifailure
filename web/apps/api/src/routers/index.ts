// The router tree.
//
// Every procedure here is built with orgProcedure(permission) or is explicitly
// public. There is no third kind, and the matrix test proves it by walking the
// tree rather than by reading this file.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { verifyAuditChain, type Db } from '@antifailure/db'
import { PolicyEngine, type Egress, type EgressRule, type Mode } from '@antifailure/policy'
import { router, publicProcedure, orgProcedure, audit, registerRouter, type OrgContext } from '../trpc.ts'
import { PERMISSIONS, PERMISSION_DESCRIPTIONS, ROLES, ROLE_PERMISSIONS, rolesWith } from '../permissions.ts'

const uuid = z.string().uuid()

// ---------------------------------------------------------------------------
// Environments
// ---------------------------------------------------------------------------

const environmentsRouter = router({
  list: orgProcedure('environments.view')
    .input(
      z.object({
        repository: z.string().optional(),
        branch: z.string().optional(),
        state: z
          .enum(['queued', 'creating', 'running', 'sleeping', 'failed', 'torn_down'])
          .optional(),
        createdBy: uuid.optional(),
        // The filters a person actually reaches for, plus a cursor. Every list
        // is paginated: an organization with ten thousand environments must not
        // be able to ask for all of them and take the API down for everyone
        // else on the replica.
        limit: z.number().int().min(1).max(200).default(50),
        cursor: z.string().optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<EnvironmentRow>(sql`
          SELECT e.id, e.env_id, e.branch, e.pull_request, e.state, e.preview_url,
                 e.runtime, e.golden_version, e.created_at, e.updated_at, e.expires_at,
                 r.full_name AS repository
          FROM environments e
          JOIN repositories r ON r.id = e.repository_id
          WHERE (${input.repository ?? null}::text IS NULL OR r.full_name = ${input.repository ?? null})
            AND (${input.branch ?? null}::text IS NULL OR e.branch = ${input.branch ?? null})
            AND (${input.state ?? null}::text IS NULL OR e.state::text = ${input.state ?? null})
            AND (${input.createdBy ?? null}::uuid IS NULL OR e.created_by = ${input.createdBy ?? null}::uuid)
            AND (${input.cursor ?? null}::text IS NULL OR e.created_at < ${input.cursor ?? null}::timestamptz)
          ORDER BY e.created_at DESC
          LIMIT ${input.limit + 1}`)

        const page = rows.slice(0, input.limit)
        return {
          environments: page,
          // The cursor is the last row's timestamp rather than an offset.
          // Offsets skip rows when something is inserted between pages, which
          // in a list ordered by creation time is constantly.
          nextCursor:
            rows.length > input.limit ? asIso(page[page.length - 1]!.created_at) : null,
        }
      })
    }),

  get: orgProcedure('environments.view')
    .input(z.object({ envId: z.string() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<EnvironmentRow>(sql`
          SELECT e.id, e.env_id, e.branch, e.pull_request, e.state, e.preview_url,
                 e.runtime, e.golden_version, e.created_at, e.updated_at, e.expires_at,
                 r.full_name AS repository
          FROM environments e JOIN repositories r ON r.id = e.repository_id
          WHERE e.env_id = ${input.envId}`)
        const env = rows[0]
        if (!env) throw notFound('environment', input.envId)
        return env
      })
    }),

  teardown: orgProcedure('environments.teardown')
    .input(z.object({ envId: z.string(), reason: z.string().max(500).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ id: string; state: string }>(sql`
          UPDATE environments
          SET state = 'torn_down', torn_down_at = ${c.clock.now().toISOString()},
              updated_at = ${c.clock.now().toISOString()}
          WHERE env_id = ${input.envId} AND state <> 'torn_down'
          RETURNING id, state::text AS state`)
        if (rows.length === 0) throw notFound('environment', input.envId)

        await audit(db, c, {
          action: 'environment.torn_down',
          targetType: 'environment',
          targetId: input.envId,
          detail: { reason: input.reason ?? null },
        })
        // The row is marked here; the engine that holds the containers reads
        // this and does the removing. The control plane has no route into a
        // developer's machine and must never pretend otherwise.
        return { requested: true, envId: input.envId }
      })
    }),
})

interface EnvironmentRow extends Record<string, unknown> {
  id: string
  env_id: string
  branch: string
  pull_request: number | null
  state: string
  preview_url: string | null
  runtime: string | null
  golden_version: string | null
  created_at: Date | string
  updated_at: Date | string
  expires_at: Date | string | null
  repository: string
}

// ---------------------------------------------------------------------------
// Runs and verdicts
// ---------------------------------------------------------------------------

const runsRouter = router({
  list: orgProcedure('environments.view')
    .input(z.object({ envId: z.string(), limit: z.number().int().min(1).max(100).default(25) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT r.id, r.kind, r.state, r.started_at, r.finished_at, r.created_at
          FROM runs r JOIN environments e ON e.id = r.environment_id
          WHERE e.env_id = ${input.envId}
          ORDER BY r.created_at DESC LIMIT ${input.limit}`),
      )
    }),

  verdicts: orgProcedure('environments.view')
    .input(z.object({ runId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT workflow, persona, value, summary, steps, duration_ms, reproduction
          FROM verdicts WHERE run_id = ${input.runId} ORDER BY workflow`),
      )
    }),

  artifacts: orgProcedure('environments.view')
    .input(z.object({ runId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT id, kind, step, content_type, size_bytes, sha256, retained
          FROM artifacts WHERE run_id = ${input.runId} ORDER BY step NULLS LAST, kind`),
      )
    }),
})

// ---------------------------------------------------------------------------
// Network policy
// ---------------------------------------------------------------------------

const MODES: readonly Mode[] = ['block', 'allow', 'capture', 'mock', 'sandbox', 'synth']

const networkRouter = router({
  effective: orgProcedure('environments.view')
    .input(z.object({ repository: z.string().optional() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const egress = await effectiveEgress(c, input.repository)
      const engine = new PolicyEngine(egress)
      return {
        default: engine.default(),
        // In the order that decides, which is the order a reader has to see
        // them in to predict anything.
        rules: engine.compiledRules(),
        hosts: engine.hosts(),
      }
    }),

  explain: orgProcedure('environments.view')
    .input(
      z.object({
        repository: z.string().optional(),
        host: z.string().min(1),
        port: z.number().int().min(0).max(65535).optional(),
        method: z.string().max(20).optional(),
        path: z.string().max(2000).optional(),
        tls: z.boolean().optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      const egress = await effectiveEgress(c, input.repository)
      const engine = new PolicyEngine(egress)
      const { decision, chain } = engine.explain(input)
      return {
        decision,
        chain,
        inspectsHost: engine.inspectsHost(input.host, input.port ?? 0),
      }
    }),

  decisions: orgProcedure('environments.view')
    .input(z.object({ envId: z.string().optional(), limit: z.number().int().min(1).max(500).default(100) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT payload->>'host' AS host, payload->>'mode' AS mode, count(*) AS requests
          FROM events
          WHERE type = 'network.decision'
            AND (${input.envId ?? null}::text IS NULL OR env_id = ${input.envId ?? null})
          GROUP BY 1, 2 ORDER BY count(*) DESC LIMIT ${input.limit}`),
      )
    }),

  propose: orgProcedure('network.edit')
    .input(
      z.object({
        repository: z.string(),
        host: z.string().min(1).max(253),
        mode: z.enum(MODES as [Mode, ...Mode[]]),
        paths: z.array(z.string()).max(50).optional(),
        methods: z.array(z.string()).max(20).optional(),
        note: z.string().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      // Compiled before it is stored. A rule that the engine would refuse must
      // not sit in the control plane looking like policy: the page would show
      // an enforcement that no environment is applying.
      try {
        new PolicyEngine({ rules: [{ host: input.host, mode: input.mode, paths: input.paths, methods: input.methods }] })
      } catch (err) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: err instanceof Error ? err.message : 'the rule cannot be compiled',
        })
      }

      return c.pool.withTenant(c.tenant, async (db) => {
        const repo = await repositoryId(db, input.repository)
        await db.execute(sql`
          INSERT INTO network_rules (org_id, repository_id, host, mode, paths, methods, note)
          VALUES (${c.actor.orgId}, ${repo}, ${input.host}, ${input.mode},
                  ${sql.param(input.paths ?? null)}::text[],
                  ${sql.param(input.methods ?? null)}::text[],
                  ${input.note ?? null})`)
        await audit(db, c, {
          action: 'network.rule_proposed',
          targetType: 'repository',
          targetId: input.repository,
          detail: { host: input.host, mode: input.mode },
        })
        return { proposed: true }
      })
    }),
})

async function effectiveEgress(c: OrgContext, repository?: string): Promise<Egress> {
  return c.pool.withTenant(c.tenant, async (db) => {
    const rows = await db.execute<{
      host: string
      mode: string
      paths: string[] | null
      methods: string[] | null
      rate_limit: string | null
      credential: string | null
      fixtures: string | null
      webhook_path: string | null
      note: string | null
      repository_id: string | null
    }>(sql`
      SELECT n.host, n.mode, n.paths, n.methods, n.rate_limit, n.credential,
             n.fixtures, n.webhook_path, n.note, n.repository_id
      FROM network_rules n
      LEFT JOIN repositories r ON r.id = n.repository_id
      WHERE n.repository_id IS NULL
         OR (${repository ?? null}::text IS NOT NULL AND r.full_name = ${repository ?? null})
      ORDER BY n.position ASC, n.created_at ASC`)

    // Organization-wide rules first, so that a repository rule with the same
    // specificity loses the tie. Precedence between scopes is decided here and
    // specificity decides within a scope, which is the rule from 5.1.
    const ordered = [...rows].sort((a, b) => {
      const aWide = a.repository_id === null ? 0 : 1
      const bWide = b.repository_id === null ? 0 : 1
      return aWide - bWide
    })

    const rules: EgressRule[] = ordered.map((r) => ({
      host: r.host,
      mode: r.mode as Mode,
      paths: r.paths ?? undefined,
      methods: r.methods ?? undefined,
      rate_limit: r.rate_limit ?? undefined,
      credential: r.credential ?? undefined,
      fixtures: r.fixtures ?? undefined,
      webhook_path: r.webhook_path ?? undefined,
      note: r.note ?? undefined,
    }))
    return { default: 'block', rules }
  })
}

// ---------------------------------------------------------------------------
// Masking
// ---------------------------------------------------------------------------

const maskingRouter = router({
  rules: orgProcedure('environments.view')
    .input(z.object({ repository: z.string() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT m.table_name, m.column_name, m.transform, m.link, m.reason, m.confirmed
          FROM masking_rules m JOIN repositories r ON r.id = m.repository_id
          WHERE r.full_name = ${input.repository}
          ORDER BY m.table_name, m.column_name`),
      )
    }),

  attestations: orgProcedure('environments.view')
    .input(z.object({ repository: z.string() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT g.version, g.verified, g.attestation, g.created_at, g.size_bytes
          FROM golden_versions g JOIN repositories r ON r.id = g.repository_id
          WHERE r.full_name = ${input.repository}
          ORDER BY g.created_at DESC LIMIT 50`),
      )
    }),

  propose: orgProcedure('masking.edit')
    .input(
      z.object({
        repository: z.string(),
        table: z.string().min(1).max(200),
        column: z.string().min(1).max(200),
        transform: z.string().min(1).max(100),
        link: z.string().max(100).optional(),
        reason: z.string().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const repo = await repositoryId(db, input.repository)
        await db.execute(sql`
          INSERT INTO masking_rules (org_id, repository_id, table_name, column_name, transform, link, reason)
          VALUES (${c.actor.orgId}, ${repo}, ${input.table}, ${input.column},
                  ${input.transform}, ${input.link ?? null}, ${input.reason ?? null})
          ON CONFLICT (org_id, repository_id, table_name, column_name)
          DO UPDATE SET transform = EXCLUDED.transform, link = EXCLUDED.link,
                        reason = EXCLUDED.reason, confirmed = false,
                        updated_at = ${c.clock.now().toISOString()}`)
        await audit(db, c, {
          action: 'masking.rule_proposed',
          targetType: 'repository',
          targetId: input.repository,
          detail: { table: input.table, column: input.column, transform: input.transform },
        })
        // Deliberately not applied anywhere. A masking rule takes effect by
        // being committed to the repository and read by the engine, so the
        // control plane's job ends at opening a pull request. There is no path
        // by which this application changes what an environment masks without
        // review, and that is the point of it never holding the data.
        return { proposed: true, needsPullRequest: true }
      })
    }),

  approve: orgProcedure('masking.approve')
    .input(z.object({ repository: z.string(), table: z.string(), column: z.string() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ id: string }>(sql`
          UPDATE masking_rules m SET confirmed = true, updated_at = ${c.clock.now().toISOString()}
          FROM repositories r
          WHERE m.repository_id = r.id AND r.full_name = ${input.repository}
            AND m.table_name = ${input.table} AND m.column_name = ${input.column}
          RETURNING m.id`)
        if (rows.length === 0) throw notFound('masking rule', `${input.table}.${input.column}`)
        await audit(db, c, {
          action: 'masking.rule_approved',
          targetType: 'repository',
          targetId: input.repository,
          detail: { table: input.table, column: input.column },
        })
        return { approved: true }
      })
    }),
})

// ---------------------------------------------------------------------------
// Audit
// ---------------------------------------------------------------------------

const auditRouter = router({
  list: orgProcedure('audit.read')
    .input(
      z.object({
        action: z.string().optional(),
        limit: z.number().int().min(1).max(500).default(100),
        before: z.number().int().optional(),
      }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT seq, actor_label, action, target_type, target_id, origin, detail, occurred_at
          FROM audit_entries
          WHERE (${input.action ?? null}::text IS NULL OR action = ${input.action ?? null})
            AND (${input.before ?? null}::bigint IS NULL OR seq < ${input.before ?? null})
          ORDER BY seq DESC LIMIT ${input.limit}`),
      )
    }),

  verify: orgProcedure('audit.export')
    .query(async ({ ctx }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, (db) => verifyAuditChain(db, c.actor.orgId))
    }),

  export: orgProcedure('audit.export')
    .input(z.object({ format: z.enum(['json', 'csv']).default('json') }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<Record<string, unknown>>(sql`
          SELECT seq, actor_label, action, target_type, target_id, origin, detail,
                 occurred_at, prev_hash, entry_hash
          FROM audit_entries ORDER BY seq ASC`)
        // The export is itself audited. An export is a file of who did what
        // leaving the system, which is exactly the kind of action the log
        // exists to record.
        await audit(db, c, {
          action: 'audit.exported',
          targetType: 'organization',
          targetId: c.actor.orgId,
          detail: { format: input.format, entries: rows.length },
        })
        return { format: input.format, entries: rows.length, rows }
      })
    }),
})

// ---------------------------------------------------------------------------
// Members, tokens, and the permission catalog
// ---------------------------------------------------------------------------

const membersRouter = router({
  list: orgProcedure('environments.view').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) =>
      db.execute(sql`
        SELECT u.github_login, u.name, u.avatar_url, m.role, m.source, m.created_at
        FROM members m JOIN users u ON u.id = m.user_id
        ORDER BY m.created_at ASC`),
    )
  }),

  setRole: orgProcedure('members.manage')
    .input(z.object({ githubLogin: z.string(), role: z.enum(ROLES) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        // Refused before the write rather than repaired after it. An
        // organization with no owner cannot grant anyone the permission to
        // become one, so it is unrecoverable without a database console.
        if (input.role !== 'owner') {
          const owners = await db.execute<{ n: string; is_target: boolean }>(sql`
            SELECT count(*) AS n,
                   bool_or(u.github_login = ${input.githubLogin}) AS is_target
            FROM members m JOIN users u ON u.id = m.user_id WHERE m.role = 'owner'`)
          const row = owners[0]
          if (row && Number(row.n) === 1 && row.is_target) {
            throw new TRPCError({
              code: 'BAD_REQUEST',
              message:
                'This is the only owner. Make somebody else an owner first, or the organization ' +
                'is left with nobody who can manage members or billing.',
            })
          }
        }

        const rows = await db.execute<{ user_id: string }>(sql`
          UPDATE members m SET role = ${input.role}, source = 'manual',
                               updated_at = ${c.clock.now().toISOString()}
          FROM users u WHERE u.id = m.user_id AND u.github_login = ${input.githubLogin}
          RETURNING m.user_id`)
        if (rows.length === 0) throw notFound('member', input.githubLogin)
        await audit(db, c, {
          action: 'member.role_changed',
          targetType: 'member',
          targetId: input.githubLogin,
          detail: { role: input.role },
        })
        return { changed: true }
      })
    }),
})

const tokensRouter = router({
  list: orgProcedure('tokens.manage').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) =>
      db.execute(sql`
        SELECT id, name, prefix, created_at, last_used_at, revoked_at
        FROM engine_tokens ORDER BY created_at DESC`),
    )
  }),

  revoke: orgProcedure('tokens.manage')
    .input(z.object({ id: uuid }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ name: string }>(sql`
          UPDATE engine_tokens SET revoked_at = ${c.clock.now().toISOString()}
          WHERE id = ${input.id} AND revoked_at IS NULL RETURNING name`)
        if (rows.length === 0) throw notFound('token', input.id)
        await audit(db, c, {
          action: 'token.revoked',
          targetType: 'engine_token',
          targetId: input.id,
          detail: { name: rows[0]!.name },
        })
        return { revoked: true }
      })
    }),
})

// ---------------------------------------------------------------------------

export const appRouter = router({
  health: publicProcedure.query(() => ({ ok: true })),

  /** The permission catalog, which is what the documentation table is built
   *  from. Public because it describes the product rather than any tenant. */
  permissions: publicProcedure.query(() => ({
    permissions: PERMISSIONS.map((p) => ({
      name: p,
      description: PERMISSION_DESCRIPTIONS[p],
      roles: rolesWith(p),
    })),
    roles: ROLES.map((r) => ({ name: r, permissions: ROLE_PERMISSIONS[r] })),
  })),

  environments: environmentsRouter,
  runs: runsRouter,
  network: networkRouter,
  masking: maskingRouter,
  audit: auditRouter,
  members: membersRouter,
  tokens: tokensRouter,
})

export type AppRouter = typeof appRouter

// So that the permission each route declares can be read without a request
// having been made against it.
registerRouter(appRouter)

// ---------------------------------------------------------------------------

async function repositoryId(db: Db, fullName: string): Promise<string> {
  const rows = await db.execute<{ id: string }>(sql`
    SELECT id FROM repositories WHERE full_name = ${fullName}`)
  if (rows.length === 0) throw notFound('repository', fullName)
  return rows[0]!.id
}

function notFound(kind: string, id: string): TRPCError {
  return new TRPCError({
    code: 'NOT_FOUND',
    // Deliberately the same message whether the row belongs to another tenant
    // or does not exist. Distinguishing them turns this endpoint into a way to
    // ask whether another organization has a repository by that name.
    message: `No ${kind} named ${id} in this organization.`,
  })
}

function asIso(v: Date | string): string {
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
