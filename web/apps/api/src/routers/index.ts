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
import {
  accountRouter,
  deletionRouter,
  exportsRouter,
  invitationsRouter,
  organizationSettings,
  sessionsRouter,
} from './enterprise.ts'
import { PERMISSIONS, PERMISSION_DESCRIPTIONS, ROLES, ROLE_PERMISSIONS, rolesWith } from '../permissions.ts'
import { checkQuota, DEFAULT_PLAN } from '../limits.ts'
import { capsFor, costAttribution, environmentHoursSince } from '../costs.ts'
import { syncMembership, SignInError } from '../auth/signin.ts'
import { GitHubError } from '../auth/github.ts'
import {
  createEnvironment, agentsRouter, loadRouter, dispatch, installationFor, workflowFile,
} from './dispatch.ts'
import { runtimesRouter } from './runtimes.ts'
import { workloadsRouter } from './workloads.ts'
import { expireOverdueCommands } from '../workloads/commands.ts'
import { billingRouter } from './billing.ts'
import { subscriptionsRouter } from './subscriptions.ts'

const uuid = z.string().uuid()

// ---------------------------------------------------------------------------
// Environments
// ---------------------------------------------------------------------------

const environmentsRouter = router({
  // Lives in dispatch.ts because it acts rather than reads, and everything
  // that acts goes out through the customer's own CI. See that file's header.
  create: createEnvironment,

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
        // Housekeeping on a read, and the same shape workloads.list uses. A
        // teardown that expired has to stop reading as though it were still
        // going, and every policy on runtime_commands keys on current_org(), so
        // a sweeper with no tenant set would match nothing and report success.
        // One indexed update against a partial index holding only the commands
        // that can still expire, so it costs nothing when there is nothing.
        await expireOverdueCommands(db, { now: c.clock.now() })
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
        // Housekeeping on a read, and the same shape workloads.list uses. A
        // teardown that expired has to stop reading as though it were still
        // going, and every policy on runtime_commands keys on current_org(), so
        // a sweeper with no tenant set would match nothing and report success.
        // One indexed update against a partial index holding only the commands
        // that can still expire, so it costs nothing when there is nothing.
        await expireOverdueCommands(db, { now: c.clock.now() })
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

  /**
   * Where an organization's environment time went, and what is left of the
   * daily cap.
   *
   * environments.view rather than billing.manage, deliberately. The person who
   * left a branch up over a weekend is the one who can tear it down, and a
   * number only an owner may look at is a number nobody acts on until the
   * invoice arrives.
   *
   * The window is the same rolling twenty four hours the admission check uses,
   * from the same clock and the same function, so what this page shows and
   * what a refusal says can never disagree.
   */
  costs: orgProcedure('environments.view')
    .input(z.object({ hours: z.number().int().positive().max(720).default(24) }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const now = c.clock.now()
        const since = new Date(now.getTime() - input.hours * 60 * 60 * 1000)
        const plans = await db.execute<{ plan: string }>(sql`
          SELECT plan FROM organizations WHERE id = ${c.actor.orgId}`)
        const plan = plans[0]?.plan || DEFAULT_PLAN
        const caps = capsFor(plan)
        const used = await environmentHoursSince(db, c.actor.orgId, since, now)
        return {
          plan,
          windowHours: input.hours,
          since: since.toISOString(),
          usedHours: Math.round(used * 100) / 100,
          caps,
          /** Never below zero: an organization that is over reads 0 left,
           *  not a negative allowance somebody has to interpret. */
          remainingDayHours: Math.max(0, Math.round((caps.perDayHours - used) * 100) / 100),
          environments: await costAttribution(db, c.actor.orgId, since, now),
        }
      })
    }),

  /**
   * Asks for an environment to be removed, and records the asking.
   *
   * IT NO LONGER MARKS THE ROW TORN DOWN. That is the whole change and it is
   * the difference between a button that works and one that looks like it
   * does. This used to set `state = 'torn_down'` and return, with a comment
   * saying the engine holding the containers reads this and does the removing.
   * Nothing read it. Not the engine, not a sweeper, not anything: the containers
   * kept running and the console said they were gone, which is worse than the
   * button not existing, because somebody who saw "torn down" stopped looking.
   *
   * So it writes a request, and `sweepTeardowns` works through them. The
   * environments row moves only on an ACKNOWLEDGEMENT: the workflow run holding
   * it reached a terminal state at GitHub, or the engine reported the teardown
   * over /v1/events. Where there is no route to the runtime at all, the request
   * is given up on after its attempts and says so in as many words, naming
   * `af down`, rather than reporting a cleanup that never happened.
   */
  teardown: orgProcedure('environments.teardown')
    .input(z.object({
      envId: z.string(),
      reason: z.string().max(500).optional(),
      workflow: workflowFile,
    }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{
          id: string
          state: string
          repository_id: string
        }>(sql`
          SELECT id, state::text AS state, repository_id FROM environments
          WHERE env_id = ${input.envId}`)
        const environment = rows[0]
        if (!environment) throw notFound('environment', input.envId)
        if (environment.state === 'torn_down') {
          return { requested: false, envId: input.envId, teardown: 'acknowledged' as const }
        }

        // The workflow run that built it, when this environment came from a
        // pull request. It is the only route this control plane has into the
        // machine holding the containers, so the request carries it: without
        // one the sweeper has nothing to reach and says so instead of pretending.
        const runs = await db.execute<{ workflow_run_id: string | null; generation_id: string }>(sql`
          SELECT g.workflow_run_id::text AS workflow_run_id, g.id AS generation_id
          FROM pr_generations g
          WHERE g.env_id = ${input.envId}
          ORDER BY g.queued_at DESC LIMIT 1`)
        const run = runs[0] ?? null

        const requested = await db.execute<{ id: string }>(sql`
          INSERT INTO teardown_requests (
            org_id, environment_id, env_id, repository_id, workflow_run_id, generation_id,
            reason, requested_by, requested_at, updated_at)
          SELECT ${c.actor.orgId}::uuid, ${environment.id}::uuid, ${input.envId},
                 ${environment.repository_id}::uuid, ${run?.workflow_run_id ?? null}::bigint,
                 ${run?.generation_id ?? null}::uuid,
                 ${input.reason ?? 'asked for in the console'}, ${c.actor.userId}::uuid,
                 ${c.clock.now().toISOString()}::timestamptz, ${c.clock.now().toISOString()}::timestamptz
          -- One live request per environment. Pressing the button twice is a
          -- person wondering whether the first press worked, not a second
          -- instruction, and two rows would be two cancels and one confusing
          -- history.
          WHERE NOT EXISTS (
            SELECT 1 FROM teardown_requests t
            WHERE t.env_id = ${input.envId} AND t.state IN ('pending', 'leased'))
          RETURNING id`)

        await audit(db, c, {
          action: 'environment.teardown_requested',
          targetType: 'environment',
          targetId: input.envId,
          detail: {
            reason: input.reason ?? null,
            // Said in the audit entry as well as in the response, because the
            // question a reader of the log asks first is whether there was
            // anything to reach.
            route: run?.workflow_run_id ? 'the workflow run that built it' : 'none',
          },
        })
        return {
          requested: true,
          envId: input.envId,
          teardown: 'pending' as const,
          // Said out loud, because "requested" and "removed" are different
          // things and this endpoint used to report the second while doing the
          // first. `already` covers the second press.
          pending:
            requested.length === 0
              ? 'A teardown was already asked for and has not been confirmed yet.'
              : 'The environment disappears here when the runtime confirms it is gone.',
        }
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
// Repositories
//
// Small, and it exists because three other screens are useless without it.
// masking.rules, masking.attestations and network.effective all take a
// repository full name, and until now the only way to learn one was to read an
// environment row and hope the organization had an environment. A tenant with
// a repository connected and nothing built yet had no way to see its own
// masking rules.
// ---------------------------------------------------------------------------

const repositoriesRouter = router({
  list: orgProcedure('environments.view')
    .input(z.object({ includeArchived: z.boolean().default(false) }).default({ includeArchived: false }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT id, full_name, default_branch, private, archived_at, created_at
          FROM repositories
          WHERE (${input.includeArchived} OR archived_at IS NULL)
          ORDER BY full_name`),
      )
    }),
})

// ---------------------------------------------------------------------------
// Runs and verdicts
// ---------------------------------------------------------------------------

const runsRouter = router({
  /**
   * The organization's runs, newest first, across every environment.
   *
   * `list` takes an envId, which is the right shape for "what has this
   * environment done" and the wrong one for the question somebody opening a
   * console actually has, which is "what happened". Answering that by listing
   * environments and then fanning out one query per environment is N+1 against
   * a replica, so it is one query here.
   */
  recent: orgProcedure('environments.view')
    .input(
      z
        .object({
          envId: z.string().optional(),
          state: z.enum(['queued', 'running', 'complete', 'failed', 'cancelled']).optional(),
          limit: z.number().int().min(1).max(200).default(50),
          before: z.string().optional(),
        })
        .default({ limit: 50 }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<Record<string, unknown>>(sql`
          SELECT r.id, r.kind, r.state, r.started_at, r.finished_at, r.created_at,
                 e.env_id, e.branch, e.pull_request, rep.full_name AS repository,
                 (SELECT count(*) FROM verdicts v WHERE v.run_id = r.id) AS verdicts,
                 (SELECT count(*) FROM verdicts v
                   WHERE v.run_id = r.id AND v.value IN ('fail', 'blocked')) AS failing
          FROM runs r
          JOIN environments e ON e.id = r.environment_id
          JOIN repositories rep ON rep.id = e.repository_id
          WHERE (${input.envId ?? null}::text IS NULL OR e.env_id = ${input.envId ?? null})
            AND (${input.state ?? null}::text IS NULL OR r.state::text = ${input.state ?? null})
            AND (${input.before ?? null}::text IS NULL
                 OR r.created_at < ${input.before ?? null}::timestamptz)
          ORDER BY r.created_at DESC
          LIMIT ${input.limit + 1}`)
        const page = rows.slice(0, input.limit)
        return {
          runs: page,
          nextCursor:
            rows.length > input.limit ? asIso(page[page.length - 1]!.created_at as string) : null,
        }
      })
    }),

  /** One run, with the environment it belongs to. A detail page that had to
   *  scan a list to title itself would break the moment the run fell off it. */
  get: orgProcedure('environments.view')
    .input(z.object({ runId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<Record<string, unknown>>(sql`
          SELECT r.id, r.kind, r.state, r.started_at, r.finished_at, r.created_at,
                 e.env_id, e.branch, e.pull_request, rep.full_name AS repository
          FROM runs r
          JOIN environments e ON e.id = r.environment_id
          JOIN repositories rep ON rep.id = e.repository_id
          WHERE r.id = ${input.runId}`)
        const run = rows[0]
        if (!run) throw notFound('run', input.runId)
        return run
      })
    }),

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
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<ArtifactRow>(sql`
          SELECT id, kind, step, content_type, size_bytes, sha256, retained
          FROM artifacts WHERE run_id = ${input.runId} ORDER BY step NULLS LAST, kind`)
        // size_bytes is a bigint, and the driver hands a bigint over as a
        // string so that a value beyond 2^53 is not quietly rounded. That is
        // the right default and it is the wrong shape to publish: a client
        // that formats it does `size / 1024` and gets a number from a string
        // by coercion, then does the same to something that is genuinely text
        // and gets NaN, and nothing throws in either case.
        //
        // So it is converted here, at the boundary that declares the shape,
        // rather than left for every caller to guess at. The range is not a
        // risk: 2^53 bytes is nine petabytes, and an artifact is a video of a
        // browser session.
        return rows.map((row) => ({ ...row, size_bytes: asNumber(row.size_bytes) }))
      })
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
        const rows = await db.execute<{ id: string }>(sql`
          INSERT INTO network_rules (org_id, repository_id, host, mode, paths, methods, note, proposed_by)
          VALUES (${c.actor.orgId}, ${repo}, ${input.host}, ${input.mode},
                  ${sql.param(input.paths ?? null)}::text[],
                  ${sql.param(input.methods ?? null)}::text[],
                  ${input.note ?? null}, ${c.actor.userId})
          RETURNING id`)
        await audit(db, c, {
          action: 'network.rule_proposed',
          targetType: 'repository',
          targetId: input.repository,
          detail: { host: input.host, mode: input.mode },
        })
        // Inert until approved. Returning the id is the whole point of the
        // shape: the caller has something to hand to `network.approve`, and a
        // proposal that cannot be named is a proposal nobody can act on.
        return { proposed: true, ruleId: rows[0]!.id, needsApproval: true }
      })
    }),

  /**
   * The approval queue.
   *
   * A read of policy state rather than a policy action, so it is guarded by
   * `environments.view` like the other policy reads. Somebody who can see the
   * effective policy should be able to see what is waiting to change it; a
   * viewer who can read the audit log and not the queue learns about the
   * change only after it happened.
   */
  pending: orgProcedure('environments.view')
    .input(z.object({ repository: z.string().optional() }).default({}))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT n.id, n.host, n.mode, n.paths, n.methods, n.note, n.created_at,
                 r.full_name AS repository, u.github_login AS proposed_by
          FROM network_rules n
          LEFT JOIN repositories r ON r.id = n.repository_id
          LEFT JOIN users u ON u.id = n.proposed_by
          WHERE n.approved_at IS NULL
            -- Filtered exactly the way effectiveEgress filters, which means an
            -- organization-wide proposal shows up on every repository rather
            -- than nowhere. It is going to apply to that repository once it is
            -- approved, so hiding it from the queue there would mean somebody
            -- approving a change to a repository they were never shown.
            AND (${input.repository ?? null}::text IS NULL
                 OR n.repository_id IS NULL
                 OR r.full_name = ${input.repository ?? null})
          ORDER BY n.created_at ASC
          LIMIT 200`),
      )
    }),

  /**
   * Approve a proposed egress rule, which is what makes it enforce.
   *
   * The permission exists to be the gate on loosening egress, so this is the
   * one route in the file where the interesting case is the one that widens
   * access rather than the one that narrows it. The audit entry records the
   * mode for that reason: "approved a rule" is not reviewable, "approved
   * api.example.com allow" is.
   */
  approve: orgProcedure('network.approve')
    .input(z.object({ ruleId: z.string().uuid() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        // Re-approving an already approved rule must not move the approver, or
        // a second click rewrites who is accountable for a decision somebody
        // else made. The WHERE clause makes the second call a no-op that
        // reports NOT_FOUND rather than a silent overwrite.
        const rows = await db.execute<{ id: string; host: string; mode: string; repository: string | null }>(sql`
          UPDATE network_rules
          SET approved_at = ${c.clock.now().toISOString()},
              approved_by = ${c.actor.userId},
              updated_at = ${c.clock.now().toISOString()}
          WHERE id = ${input.ruleId} AND approved_at IS NULL
          RETURNING id, host, mode,
                    (SELECT full_name FROM repositories r WHERE r.id = network_rules.repository_id) AS repository`)
        if (rows.length === 0) throw notFound('pending egress rule', input.ruleId)
        const rule = rows[0]!
        await audit(db, c, {
          action: 'network.rule_approved',
          // An organization-wide rule belongs to no repository, and writing a
          // sentence into the target id to say so would put prose in the
          // column an export filters on. It is recorded as what it is.
          targetType: rule.repository === null ? 'organization' : 'repository',
          targetId: rule.repository ?? c.actor.orgId,
          detail: { host: rule.host, mode: rule.mode, ruleId: rule.id },
        })
        return { approved: true, host: rule.host, mode: rule.mode }
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
      -- Approved only. An unapproved rule is a request, and this function
      -- answers "what is enforced". Before the approval columns existed the
      -- two were the same query, so a proposal was policy the moment it was
      -- written.
      WHERE n.approved_at IS NOT NULL
        AND (n.repository_id IS NULL
             OR (${repository ?? null}::text IS NOT NULL AND r.full_name = ${repository ?? null}))
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
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<AuditRow>(sql`
          SELECT seq, actor_label, action, target_type, target_id, origin, detail, occurred_at
          FROM audit_entries
          WHERE (${input.action ?? null}::text IS NULL OR action = ${input.action ?? null})
            AND (${input.before ?? null}::bigint IS NULL OR seq < ${input.before ?? null})
          ORDER BY seq DESC LIMIT ${input.limit}`)
        // seq is a bigserial and arrives as a string, for the same reason
        // size_bytes does. It is the cursor this endpoint pages by, so a caller
        // that sends back what it was given sends a string where the input
        // schema wants a number. Converted here so the value that comes out is
        // the value that can go back in.
        return rows.map((row) => ({ ...row, seq: asNumber(row.seq) }))
      })
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

  /**
   * Reconciles this organization's members against GitHub's.
   *
   * The reason this route exists rather than the sync running only at sign-in:
   * sign-in can only ever speak for the person signing in. Somebody added to
   * the GitHub organization yesterday is not here until they happen to sign in,
   * and somebody REMOVED from it keeps whatever role they had until they sign
   * in again, which a person who has been removed has no reason to do. This is
   * the route that acts on everybody at once, and the one that takes access
   * away.
   *
   * It is also, deliberately, the caller syncMembership never had. The function
   * was written, tested and complete, and nothing invoked it, which is a
   * feature that reads as finished from every angle except the only one that
   * counts.
   */
  sync: orgProcedure('members.manage').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    const installation = await c.pool.withTenant(c.tenant, async (db) => {
      const rows = await db.execute<{ installation_id: string; account_login: string }>(sql`
        SELECT installation_id, account_login FROM github_installations
        WHERE suspended_at IS NULL ORDER BY created_at ASC LIMIT 1`)
      return rows[0] ?? null
    })
    if (!installation) {
      throw new TRPCError({
        code: 'PRECONDITION_FAILED',
        message:
          'This organization has no active GitHub App installation, so there is no membership ' +
          'to read. Install the App on the organization first.',
      })
    }
    try {
      return await syncMembership(c.pool, c.clock, c.github, {
        orgId: c.actor.orgId,
        installationId: Number(installation.installation_id),
        orgLogin: installation.account_login,
        actorLabel: c.actor.label,
      })
    } catch (error) {
      // Two refusals that are answers rather than faults, and both used to
      // arrive as a 500 that reads as a bug in this control plane.
      //
      // SignInError: syncMembership will not apply an empty member list,
      // because doing so would remove every owner and an outage looks exactly
      // like an organization where everybody left.
      //
      // GitHubError: no App is configured, or GitHub would not answer. Its
      // messages are written to be read by the operator who has to fix it --
      // "Membership sync needs a GitHub App. Set AF_GITHUB_APP_ID..." -- and
      // they are worth nothing behind a generic internal error.
      if (error instanceof SignInError || error instanceof GitHubError) {
        throw new TRPCError({ code: 'PRECONDITION_FAILED', message: error.message })
      }
      throw error
    }
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

  /**
   * Takes somebody out of the organization.
   *
   * `members.sync` already removes people, and only the ones GitHub has removed
   * from the GitHub organization. That is right for the case it was built for
   * and useless for the two an enterprise actually has: somebody invited by
   * email who is in no GitHub organization at all, and somebody who has to lose
   * access now rather than after their GitHub membership is changed by
   * somebody else.
   *
   * Their sessions go with them, in the same transaction. Removing a membership
   * and leaving a live session is a person who is no longer a member and can
   * still read every page until the session happens to expire, which is up to
   * twelve hours: the role is re-read on every request, so the session resolves
   * to no role, but a removal that depends on that is a removal that depends on
   * a detail somewhere else.
   */
  remove: orgProcedure('members.manage')
    .input(z.object({ githubLogin: z.string().min(1).max(120) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const found = await db.execute<{ user_id: string; role: string }>(sql`
          SELECT m.user_id, m.role::text AS role FROM members m JOIN users u ON u.id = m.user_id
          WHERE u.github_login = ${input.githubLogin}`)
        const member = found[0]
        if (!member) throw notFound('member', input.githubLogin)

        // Refused before the write rather than repaired after it, the same as
        // setRole. An organization with no owner cannot grant anybody the
        // permission to become one.
        if (member.role === 'owner') {
          const owners = await db.execute<{ n: string }>(sql`
            SELECT count(*) AS n FROM members WHERE role = 'owner'`)
          if (Number(owners[0]?.n ?? 0) <= 1) {
            throw new TRPCError({
              code: 'BAD_REQUEST',
              message:
                'This is the only owner. Make somebody else an owner first, or the organization ' +
                'is left with nobody who can manage members or billing.',
            })
          }
        }

        await db.execute(sql`
          DELETE FROM members WHERE user_id = ${member.user_id}::uuid`)
        const sessions = await db.execute<{ id: string }>(sql`
          UPDATE sessions SET revoked_at = ${c.clock.now().toISOString()}
          WHERE user_id = ${member.user_id}::uuid AND org_id = ${c.actor.orgId}::uuid
            AND revoked_at IS NULL
          RETURNING id`)

        await audit(db, c, {
          action: 'member.removed',
          targetType: 'member',
          targetId: input.githubLogin,
          detail: { role: member.role, sessionsRevoked: sessions.length },
        })
        return { removed: true, sessionsRevoked: sessions.length }
      })
    }),
})

// ---------------------------------------------------------------------------
// The kill switch
// ---------------------------------------------------------------------------

const orgRouter = router({
  status: orgProcedure('environments.view').query(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      const rows = await db.execute<{
        slug: string
        plan: string
        suspended_at: Date | string | null
        suspended_reason: string | null
        environments: string
        goldens: string
      }>(sql`
        SELECT o.slug, o.plan, o.suspended_at, o.suspended_reason,
               (SELECT count(*) FROM environments e
                 WHERE e.org_id = o.id AND e.state <> 'torn_down') AS environments,
               (SELECT count(*) FROM golden_versions g WHERE g.org_id = o.id) AS goldens
        FROM organizations o WHERE o.id = ${c.actor.orgId}`)
      const row = rows[0]
      if (!row) throw notFound('organization', c.actor.orgId)

      const plan = row.plan || DEFAULT_PLAN
      return {
        slug: row.slug,
        plan,
        suspended: row.suspended_at !== null,
        suspendedReason: row.suspended_reason,
        quotas: {
          environments: checkQuota(plan, 'environments', Number(row.environments)),
          goldens: checkQuota(plan, 'goldens', Number(row.goldens)),
        },
      }
    })
  }),

  /**
   * Stops this organization creating anything new.
   *
   * Deliberately does not tear anything down. An incident is the worst possible
   * moment to discover that the mitigation destroyed the evidence, so what is
   * running keeps running and can still be read.
   */
  suspend: orgProcedure('members.manage')
    .input(z.object({ reason: z.string().min(1).max(500) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await db.execute(sql`
          UPDATE organizations
          SET suspended_at = ${c.clock.now().toISOString()},
              suspended_reason = ${input.reason},
              suspended_by = ${c.actor.label},
              updated_at = ${c.clock.now().toISOString()}
          WHERE id = ${c.actor.orgId}`)
        await audit(db, c, {
          action: 'organization.suspended',
          targetType: 'organization',
          targetId: c.actor.orgId,
          detail: { reason: input.reason },
        })
        return { suspended: true, running: 'environments already running are untouched' }
      })
    }),

  resume: orgProcedure('members.manage').mutation(async ({ ctx }) => {
    const c = ctx as OrgContext
    return c.pool.withTenant(c.tenant, async (db) => {
      await db.execute(sql`
        UPDATE organizations
        SET suspended_at = NULL, suspended_reason = NULL, suspended_by = NULL,
            updated_at = ${c.clock.now().toISOString()}
        WHERE id = ${c.actor.orgId}`)
      await audit(db, c, {
        action: 'organization.resumed',
        targetType: 'organization',
        targetId: c.actor.orgId,
      })
      return { suspended: false }
    })
  }),

  // Settings, the display name, and the billing contact. Defined in
  // routers/enterprise.ts and spread in here rather than given a router of
  // their own, because two routers named after the same noun is how a console
  // ends up asking org.status on one screen and organization.get on the next
  // for facts about the same row.
  ...organizationSettings,
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

  repositories: repositoriesRouter,
  environments: environmentsRouter,
  runs: runsRouter,
  agents: agentsRouter,
  load: loadRouter,
  network: networkRouter,
  masking: maskingRouter,
  audit: auditRouter,
  members: membersRouter,
  runtimes: runtimesRouter,
  workloads: workloadsRouter,
  billing: billingRouter,
  tokens: tokensRouter,
  org: orgRouter,
  subscriptions: subscriptionsRouter,
  invitations: invitationsRouter,
  sessions: sessionsRouter,
  exports: exportsRouter,
  deletion: deletionRouter,
  account: accountRouter,
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

interface ArtifactRow extends Record<string, unknown> {
  size_bytes: string | number | null
}

interface AuditRow extends Record<string, unknown> {
  seq: string | number
}

/**
 * A bigint, as a number.
 *
 * Null stays null; anything beyond what a double represents exactly is left as
 * the string it arrived as, because rounding it silently is worse than
 * publishing a type union nobody expected. Neither of the two columns this is
 * used on can reach that: nine petabytes in one artifact, and nine quadrillion
 * audit entries.
 */
function asNumber(value: string | number | null | undefined): number | string | null {
  if (value === null || value === undefined) return null
  if (typeof value === 'number') return value
  const parsed = Number(value)
  if (!Number.isSafeInteger(parsed)) return value
  return parsed
}

function asIso(v: Date | string): string {
  return (v instanceof Date ? v : new Date(v)).toISOString()
}
