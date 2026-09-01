// The Studio API.
//
// Every route here follows the same three steps and the order is the point:
// resolve what has gone overdue, then decide, then write. Resolving first means
// a workload whose last run is stuck does not refuse the next one; deciding
// against rows that have just been resolved means a decision is never made
// against a state the database is about to leave.
//
// Where a decision could race, the database makes it. Two starts on one
// workload are decided by a partial unique index rather than by a read and a
// write, and the same is true of two teardown requests and two cancels. A read
// followed by a decision answers a question about a moment that has already
// passed, and this is a control plane several people click at once.

import { randomUUID } from 'node:crypto'
import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import {
  dispatch, installationFor, refuseWhileSuspended, workflowFile,
} from './dispatch.ts'
import {
  WORKLOAD_KINDS, dispatchInputs, parseBody, BodyRefused, type WorkloadBody, type WorkloadKind,
} from '../workloads/bodies.ts'
import {
  appendVersion, findWorkload, readRun, resolveOverdueRuns, startRun, requestCancel,
  cancelUnclaimed, markSuperseded, runColumns, runJoins, versionColumns, workloadColumns,
  TERMINAL_STATES,
} from '../workloads/store.ts'
import { createCommand, expireOverdueCommands, recordDispatch } from '../workloads/commands.ts'
import { compileExploration, ExplorationRefused } from '../workloads/promote.ts'

const slug = z
  .string()
  .min(1)
  .max(63)
  .regex(/^[a-z0-9][a-z0-9-]*$/, 'lower case letters, digits and hyphens, starting with one of the first two')

/**
 * Brings this organization's overdue rows to rest before anything is decided.
 *
 * Called at the top of every route rather than on a schedule, and
 * workloads/store.ts says why at length: every policy on these tables keys on
 * current_org(), so a sweep with no tenant matches nothing and reports success,
 * which is the exact shape of a sweeper this repository has already shipped
 * twice.
 */
async function settle(c: OrgContext, db: Parameters<typeof resolveOverdueRuns>[0]): Promise<void> {
  const now = c.clock.now()
  await resolveOverdueRuns(db, now)
  await expireOverdueCommands(db, { now })
}

function notFound(slugOrId: string): TRPCError {
  return new TRPCError({
    code: 'NOT_FOUND',
    // The same message whether it belongs to another tenant or does not exist,
    // for the reason routers/index.ts gives: a distinguishing message is a way
    // to ask what another organization has.
    message: `No workload named ${slugOrId} in this organization.`,
  })
}

function refuseBody(error: unknown): never {
  if (error instanceof BodyRefused) {
    throw new TRPCError({ code: 'BAD_REQUEST', message: error.message })
  }
  throw error
}

export const workloadsRouter = router({
  // -------------------------------------------------------------------------
  // Reading
  // -------------------------------------------------------------------------

  list: orgProcedure('workloads.view')
    .input(
      z
        .object({
          repository: z.string().optional(),
          kind: z.enum(WORKLOAD_KINDS).optional(),
          includeArchived: z.boolean().default(false),
          limit: z.number().int().min(1).max(200).default(50),
        })
        .default({ includeArchived: false, limit: 50 }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        return db.execute(sql`
          SELECT ${workloadColumns},
                 (SELECT count(*) FROM workload_runs wr WHERE wr.workload_id = w.id) AS runs,
                 latest.state AS last_state,
                 latest.verdict AS last_verdict,
                 latest.requested_at AS last_run_at
          FROM workloads w
          JOIN repositories r ON r.id = w.repository_id
          -- One LATERAL rather than three correlated subqueries, and not only
          -- because it is one index lookup instead of three. Three independent
          -- ORDER BY ... LIMIT 1 subqueries can each pick a different row when
          -- two runs share a requested_at, so the state, the verdict and the
          -- time would describe up to three different runs and the row would
          -- read as a run that never happened.
          LEFT JOIN LATERAL (
            SELECT wr.state::text AS state, wr.verdict::text AS verdict, wr.requested_at
            FROM workload_runs wr
            WHERE wr.workload_id = w.id
            ORDER BY wr.requested_at DESC, wr.id DESC
            LIMIT 1
          ) latest ON true
          WHERE (${input.includeArchived} OR w.archived_at IS NULL)
            AND (${input.repository ?? null}::text IS NULL OR r.full_name = ${input.repository ?? null})
            AND (${input.kind ?? null}::text IS NULL OR w.kind::text = ${input.kind ?? null})
          ORDER BY w.created_at DESC
          LIMIT ${input.limit}`)
      })
    }),

  get: orgProcedure('workloads.view')
    .input(z.object({ slug }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const workload = await findWorkload(db, input.slug)
        if (!workload) throw notFound(input.slug)
        const versions = await db.execute(sql`
          SELECT ${versionColumns} FROM workload_versions
          WHERE workload_id = ${workload.id} ORDER BY version DESC LIMIT 20`)
        const runs = await db.execute(sql`
          SELECT ${runColumns} ${runJoins}
          WHERE wr.workload_id = ${workload.id}
          ORDER BY wr.requested_at DESC LIMIT 20`)
        return { workload, versions, runs }
      })
    }),

  runs: orgProcedure('workloads.view')
    .input(
      z
        .object({
          slug: slug.optional(),
          envId: z.string().optional(),
          state: z
            .enum([
              'requested', 'accepted', 'running',
              'succeeded', 'failed', 'cancelled', 'timed_out', 'abandoned',
            ])
            .optional(),
          limit: z.number().int().min(1).max(200).default(50),
        })
        .default({ limit: 50 }),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        return db.execute(sql`
          SELECT ${runColumns} ${runJoins}
          WHERE (${input.slug ?? null}::text IS NULL OR w.slug = ${input.slug ?? null})
            AND (${input.envId ?? null}::text IS NULL OR e.env_id = ${input.envId ?? null})
            AND (${input.state ?? null}::text IS NULL OR wr.state::text = ${input.state ?? null})
          ORDER BY wr.requested_at DESC
          LIMIT ${input.limit}`)
      })
    }),

  /**
   * One run, with everything measured about it.
   *
   * Four queries rather than one join, because three of them are one-to-many
   * against the run and a single join would return the product of the three:
   * forty routes and ten thresholds is four hundred rows carrying the same
   * aggregate forty times, and a console that then de-duplicates in JavaScript
   * is a console that gets the de-duplication wrong.
   */
  inspect: orgProcedure('workloads.view')
    .input(z.object({ runId: z.string().uuid() }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const run = await readRun(db, input.runId)
        if (!run) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No workload run ${input.runId} in this organization.`,
          })
        }
        const [result] = await db.execute(sql`
          SELECT kind::text AS kind, requests, failures, error_rate, target_rate, achieved_rate,
                 p50_ms, p90_ms, p95_ms, p99_ms, max_ms, sessions, iterations, scheduled_ms,
                 workflows, workflows_passed, workflows_failed, steps, findings, goal_reached,
                 duration_ms, source, refused_routes, recorded_at
          FROM workload_run_results WHERE workload_run_id = ${input.runId}`)
        const routes = await db.execute(sql`
          SELECT route, sent, errors, p50_ms, p90_ms, p95_ms, p99_ms, max_ms,
                 baseline_p95_ms, p95_increase
          FROM workload_route_metrics WHERE workload_run_id = ${input.runId}
          ORDER BY position`)
        const thresholds = await db.execute(sql`
          SELECT name, scope, measure, threshold, observed, value::text AS value, detail
          FROM workload_threshold_verdicts WHERE workload_run_id = ${input.runId}
          ORDER BY position`)
        const evidence = await db.execute(sql`
          SELECT kind, label, availability::text AS availability, locator, sha256, size_bytes
          FROM workload_evidence WHERE workload_run_id = ${input.runId}
          ORDER BY recorded_at`)
        const cancel = await db.execute(sql`
          SELECT id, state::text AS state, outcome, detail, requested_at, acknowledged_at
          FROM runtime_commands
          WHERE kind = 'workload.cancel' AND workload_run_id = ${input.runId}
          ORDER BY requested_at DESC LIMIT 1`)
        // A to-one embed is an object or null and a to-many is an array. Said
        // explicitly because getting it backwards on the reading side is what
        // makes one surprising row blank a whole page.
        return {
          run,
          result: result ?? null,
          routes,
          thresholds,
          evidence,
          cancel: cancel[0] ?? null,
        }
      })
    }),

  // -------------------------------------------------------------------------
  // Authoring
  // -------------------------------------------------------------------------

  create: orgProcedure('workloads.edit')
    .input(
      z.object({
        repository: z.string(),
        slug,
        name: z.string().min(1).max(200),
        kind: z.enum(WORKLOAD_KINDS),
        description: z.string().max(2000).optional(),
        /** The first version. A workload with no version is a workload that
         *  cannot be run, so it is created with one rather than in two steps. */
        body: z.unknown(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      // Validated before the transaction opens, so a malformed body costs a
      // round trip rather than a lock on the repository row.
      let parsed: { body: WorkloadBody }
      try {
        parsed = parseBody(input.kind as WorkloadKind, input.body)
      } catch (error) {
        refuseBody(error)
      }

      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const repos = await db.execute<{ id: string }>(sql`
          SELECT id FROM repositories WHERE full_name = ${input.repository}`)
        const repo = repos[0]
        if (!repo) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No repository named ${input.repository} in this organization.`,
          })
        }

        // ON CONFLICT DO NOTHING rather than a catch. A statement that raises
        // inside this transaction cannot be recovered from: postgres.js records
        // the first failure and rethrows it after the callback returns, whatever
        // the callback did about it. See workloads/store.ts.
        const rows = await db.execute<{ id: string }>(sql`
          INSERT INTO workloads (org_id, repository_id, slug, name, kind, description, created_by)
          VALUES (${c.actor.orgId}, ${repo.id}, ${input.slug}, ${input.name},
                  ${input.kind}::workload_kind, ${input.description ?? null}, ${c.actor.userId})
          ON CONFLICT DO NOTHING
          RETURNING id`)
        const workloadId = rows[0]?.id
        if (!workloadId) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message:
              `${input.repository} already has a workload called ${input.slug}. ` +
              `Archive that one or choose another name.`,
          })
        }

        const { version } = await appendVersion(db, {
          orgId: c.actor.orgId,
          workloadId,
          kind: input.kind as WorkloadKind,
          rawBody: parsed.body,
          createdBy: c.actor.userId,
        })

        await audit(db, c, {
          action: 'workload.created',
          targetType: 'workload',
          targetId: input.slug,
          detail: { repository: input.repository, kind: input.kind, version: version.version },
        })
        return { id: workloadId, slug: input.slug, version: version.version }
      })
    }),

  addVersion: orgProcedure('workloads.edit')
    .input(
      z.object({
        slug,
        body: z.unknown(),
        notes: z.string().max(2000).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const workload = await findWorkload(db, input.slug)
        if (!workload) throw notFound(input.slug)

        let outcome: Awaited<ReturnType<typeof appendVersion>>
        try {
          outcome = await appendVersion(db, {
            orgId: c.actor.orgId,
            workloadId: workload.id,
            kind: workload.kind,
            rawBody: input.body,
            notes: input.notes ?? null,
            createdBy: c.actor.userId,
          })
        } catch (error) {
          refuseBody(error)
        }

        if (outcome.created) {
          await audit(db, c, {
            action: 'workload.version_added',
            targetType: 'workload',
            targetId: input.slug,
            detail: { version: outcome.version.version },
          })
        }
        return {
          version: outcome.version.version,
          created: outcome.created,
          // Said out loud rather than answered with a silent new version. A
          // save that changed nothing is the ordinary case for a form somebody
          // opened and closed, and a history full of identical versions is the
          // same noise as a comment posted once per push.
          note: outcome.created ? null : 'That is what the latest version already says, so nothing was added.',
        }
      })
    }),

  archive: orgProcedure('workloads.edit')
    .input(z.object({ slug, reason: z.string().max(500).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        // Refused while a run of it is going, rather than archived out from
        // under one. An archived workload is invisible to the list, and a run
        // whose definition vanished from the page while it was still going is a
        // run nobody can find to cancel.
        const live = await db.execute<{ id: string }>(sql`
          SELECT wr.id FROM workload_runs wr
          JOIN workloads w ON w.id = wr.workload_id
          WHERE w.slug = ${input.slug} AND w.archived_at IS NULL
            AND wr.state IN ('requested', 'accepted', 'running')`)
        if (live.length > 0) {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `${input.slug} has a run going, as ${live[0]!.id}. Cancel it or wait for it to ` +
              `finish: archiving now would hide the run somebody may need to stop.`,
          })
        }

        const rows = await db.execute<{ id: string }>(sql`
          UPDATE workloads SET archived_at = ${c.clock.now().toISOString()}::timestamptz,
                               updated_at = ${c.clock.now().toISOString()}::timestamptz
          WHERE slug = ${input.slug} AND archived_at IS NULL
          RETURNING id`)
        if (rows.length === 0) throw notFound(input.slug)

        await audit(db, c, {
          action: 'workload.archived',
          targetType: 'workload',
          targetId: input.slug,
          detail: { reason: input.reason ?? null },
        })
        // Archived, not deleted: every run of it stays readable, and its
        // versions are what those runs mean.
        return { archived: true, slug: input.slug }
      })
    }),

  /**
   * Turns an exploration into a browser workflow somebody can run every time.
   *
   * The compilation and its honesty live in workloads/promote.ts. What this
   * route adds is that the result is a VERSION: attributed to the run or the
   * document it came from, immutable once written, and sitting in a history
   * beside whatever the workload said before.
   */
  promote: orgProcedure('workloads.edit')
    .input(
      z.object({
        repository: z.string(),
        /** The workload to add a version to. Absent creates one named after
         *  the exploration, which is what a first promotion wants. */
        slug: slug.optional(),
        /** The exploration run this came from, when it came from one. */
        fromRunId: z.string().uuid().optional(),
        /** The document `af explore --json` produces. */
        exploration: z.unknown(),
        persona: z.string().max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      let compiled: ReturnType<typeof compileExploration>
      try {
        compiled = compileExploration(input.exploration, input.persona ?? null)
      } catch (error) {
        if (error instanceof ExplorationRefused) {
          throw new TRPCError({ code: 'BAD_REQUEST', message: error.message })
        }
        throw error
      }

      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const repos = await db.execute<{ id: string }>(sql`
          SELECT id FROM repositories WHERE full_name = ${input.repository}`)
        const repo = repos[0]
        if (!repo) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No repository named ${input.repository} in this organization.`,
          })
        }

        if (input.fromRunId) {
          // Checked rather than trusted. A promotion that names a run in
          // another organization would attach this organization's version to a
          // row it cannot see, and the foreign key would take it: the policy
          // stops the READ, and a foreign key check runs as the system.
          const source = await db.execute<{ kind: string }>(sql`
            SELECT w.kind::text AS kind FROM workload_runs wr
            JOIN workloads w ON w.id = wr.workload_id WHERE wr.id = ${input.fromRunId}`)
          if (!source[0]) {
            throw new TRPCError({
              code: 'NOT_FOUND',
              message: `No workload run ${input.fromRunId} in this organization.`,
            })
          }
          if (source[0].kind !== 'exploration') {
            throw new TRPCError({
              code: 'BAD_REQUEST',
              message:
                `Run ${input.fromRunId} is a ${source[0].kind} workload, and only an exploration ` +
                `produces the discovery a workflow can be compiled from.`,
            })
          }
        }

        const target = input.slug ?? compiled.slug
        let workload = await findWorkload(db, target)
        if (workload && workload.kind !== 'browser_workflow') {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message:
              `${target} is a ${workload.kind} workload. A promotion produces a browser workflow, ` +
              `and a workload cannot change kind: the versions already written are in the old ` +
              `kind's shape.`,
          })
        }
        if (!workload) {
          const rows = await db.execute<{ id: string }>(sql`
            INSERT INTO workloads (org_id, repository_id, slug, name, kind, description, created_by)
            VALUES (${c.actor.orgId}, ${repo.id}, ${target}, ${compiled.name},
                    'browser_workflow', ${compiled.description}, ${c.actor.userId})
            RETURNING id`)
          workload = { ...(await findWorkload(db, target))!, id: rows[0]!.id }
        }

        const { version, created } = await appendVersion(db, {
          orgId: c.actor.orgId,
          workloadId: workload.id,
          kind: 'browser_workflow',
          rawBody: compiled.body,
          notes: compiled.dropped.join('\n'),
          source: 'promoted',
          promotedFromRunId: input.fromRunId ?? null,
          createdBy: c.actor.userId,
        })

        await audit(db, c, {
          action: 'workload.promoted',
          targetType: 'workload',
          targetId: target,
          detail: {
            repository: input.repository,
            version: version.version,
            fromRunId: input.fromRunId ?? null,
            dropped: compiled.dropped.length,
          },
        })

        return {
          slug: target,
          version: version.version,
          created,
          /** What the compilation could not carry, in the words a person reads.
           *  Returned rather than only stored, because the console shows it at
           *  the moment somebody decides whether to keep the promotion. */
          dropped: compiled.dropped,
          /** The block to paste into antifailure.yaml. Until it is there,
           *  `af test --only` cannot find the workflow this version selects. */
          manifestBlock: compiled.manifestBlock,
        }
      })
    }),

  // -------------------------------------------------------------------------
  // Running
  // -------------------------------------------------------------------------

  start: orgProcedure('workloads.run')
    .input(
      z.object({
        slug,
        envId: z.string(),
        /** Which version to run. Absent means the latest, which is what a
         *  console's Run button means. */
        version: z.number().int().min(1).optional(),
        /** Makes a repeated request one run. A console sends the same value on
         *  a double click; absent means every call is a new run. */
        requestKey: z.string().min(1).max(200).optional(),
        workflow: workflowFile,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await refuseWhileSuspended(c)

      const prepared = await c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const workload = await findWorkload(db, input.slug)
        if (!workload) throw notFound(input.slug)

        const versions = await db.execute<{ id: string; version: number; body: WorkloadBody }>(sql`
          SELECT id, version, body FROM workload_versions
          WHERE workload_id = ${workload.id}
            AND (${input.version ?? null}::int IS NULL OR version = ${input.version ?? null}::int)
          ORDER BY version DESC LIMIT 1`)
        const version = versions[0]
        if (!version) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `${input.slug} has no version ${input.version ?? 'at all'}.`,
          })
        }

        const environments = await db.execute<{
          id: string; env_id: string; branch: string; state: string; repository: string
        }>(sql`
          SELECT e.id, e.env_id, e.branch, e.state::text AS state, r.full_name AS repository
          FROM environments e JOIN repositories r ON r.id = e.repository_id
          WHERE e.env_id = ${input.envId}`)
        const environment = environments[0]
        if (!environment) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No environment named ${input.envId} in this organization.`,
          })
        }
        if (environment.state === 'torn_down') {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message: `${input.envId} has been torn down, so there is nothing left to run against.`,
          })
        }
        if (environment.repository !== workload.repository) {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `${input.slug} belongs to ${workload.repository} and ${input.envId} is an ` +
              `environment of ${environment.repository}. A workload names routes and workflows ` +
              `out of one repository's manifest, so running it against another one measures ` +
              `nothing.`,
          })
        }

        const started = await startRun(db, {
          orgId: c.actor.orgId,
          workloadId: workload.id,
          versionId: version.id,
          environmentId: environment.id,
          repository: environment.repository,
          gitRef: environment.branch,
          workflowFile: input.workflow,
          // Random when the caller does not supply one, because absent means
          // "every call is a new run" and a timestamp is not a unique value: two
          // requests in the same millisecond would collide and the second would
          // be answered with the first one's run. Found by firing two starts at
          // once against an injected clock, which is exactly the case a real
          // console produces when a page fires on two renders.
          requestKey: input.requestKey ?? randomUUID(),
          requestedBy: c.actor.userId,
          now: c.clock.now(),
        })

        if (started.created) {
          await audit(db, c, {
            action: 'workload.run_requested',
            targetType: 'workload',
            targetId: input.slug,
            detail: { envId: environment.env_id, version: version.version, runId: started.runId },
          })
        }
        return {
          started,
          workload,
          version,
          environment,
        }
      })

      if (!prepared.started.created) {
        return {
          runId: prepared.started.runId,
          dispatched: false,
          note: prepared.started.reason ?? null,
        }
      }

      // The dispatch happens outside the transaction that wrote the run,
      // deliberately. A dispatch inside it would hold a row lock open across a
      // call to GitHub, and a GitHub that is slow would then be a control plane
      // that is slow for everybody in the same organization.
      const plan = dispatchInputs(prepared.workload.kind, prepared.version.body)
      const installation = await installationFor(c)
      try {
        await dispatch(
          c,
          installation,
          prepared.environment.repository,
          input.workflow,
          prepared.environment.branch,
          plan.inputs,
        )
      } catch (error) {
        // The run stays. It was written before the dispatch on purpose: a run
        // requested and never started is exactly the state somebody has to be
        // able to see, and throwing the row away would leave the failure
        // visible only in whatever the console did with this error.
        const message = error instanceof TRPCError ? error.message : String(error)
        await c.pool.withTenant(c.tenant, async (db) => {
          await db.execute(sql`
            UPDATE workload_runs SET
              detail = ${`the dispatch was refused: ${message}`.slice(0, 2000)},
              updated_at = ${c.clock.now().toISOString()}::timestamptz
            WHERE id = ${prepared.started.runId}`)
        })
        if (plan.needsUpdatedWorkflow && error instanceof TRPCError) {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `${message} A ${prepared.workload.kind} workload needs the workflow inputs added ` +
              `for Studio: copy the current examples/github-workflow.yml over ` +
              `.github/workflows/${input.workflow} on the default branch. The run is recorded ` +
              `either way and an engine can still claim it.`,
          })
        }
        throw error
      }

      await c.pool.withTenant(c.tenant, async (db) => {
        await db.execute(sql`
          UPDATE workload_runs SET dispatched_at = ${c.clock.now().toISOString()}::timestamptz,
                                   updated_at = ${c.clock.now().toISOString()}::timestamptz
          WHERE id = ${prepared.started.runId}`)
      })

      return {
        runId: prepared.started.runId,
        dispatched: true,
        repository: prepared.environment.repository,
        ref: prepared.environment.branch,
        version: prepared.version.version,
        // Said out loud, because "started" would be a lie: what exists after
        // this call is a queued GitHub Actions run and a row saying somebody
        // asked for one.
        pending: 'The run reports here when the engine picks it up.',
      }
    }),

  cancel: orgProcedure('workloads.run')
    .input(z.object({ runId: z.string().uuid(), reason: z.string().max(500).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const outcome = await requestCancel(db, {
          runId: input.runId,
          reason: input.reason ?? null,
          by: c.actor.userId,
          now: c.clock.now(),
        })
        if (outcome === 'not_found') {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No workload run ${input.runId} in this organization.`,
          })
        }
        if (outcome === 'already_finished') {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `Run ${input.runId} has already finished, so there is nothing to cancel. ` +
              `Nothing was changed.`,
          })
        }

        // Settled here when nobody has it. A run still in `requested` has never
        // been claimed, so no engine will ever send a terminal event for it and
        // waiting for one would leave it open until its deadline.
        const settledNow = await cancelUnclaimed(db, { runId: input.runId, now: c.clock.now() })

        const command = settledNow
          ? null
          : await createCommand(db, {
              orgId: c.actor.orgId,
              kind: 'workload.cancel',
              workloadRunId: input.runId,
              requestedBy: c.actor.userId,
              now: c.clock.now(),
            })

        if (outcome === 'requested') {
          await audit(db, c, {
            action: 'workload.run_cancelled',
            targetType: 'workload_run',
            targetId: input.runId,
            detail: { reason: input.reason ?? null, stoppedImmediately: settledNow },
          })
        }
        return {
          requested: true,
          /** True when the run had not been claimed by anything, so it is
           *  already over. False means a command is waiting for a runtime to
           *  confirm, and `commandId` is what to watch. */
          stopped: settledNow,
          commandId: command?.id ?? null,
          alreadyRequested: outcome === 'already_requested',
        }
      })
    }),

  /**
   * Runs the same version again.
   *
   * The SAME version, deliberately, and not the latest. A retry answers "was
   * that a fluke", and answering it with a definition somebody edited in the
   * meantime answers a different question while looking like it answered this
   * one. Running the latest is `start`, which is a different button.
   */
  retry: orgProcedure('workloads.run')
    .input(z.object({ runId: z.string().uuid(), workflow: workflowFile }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await refuseWhileSuspended(c)

      const prepared = await c.pool.withTenant(c.tenant, async (db) => {
        await settle(c, db)
        const rows = await db.execute<{
          id: string
          workload_id: string
          workload_version_id: string
          environment_id: string
          env_id: string
          repository: string
          git_ref: string
          state: string
          attempt: number
          superseded_by: string | null
          kind: WorkloadKind
          slug: string
          body: WorkloadBody
          environment_state: string
        }>(sql`
          SELECT wr.id, wr.workload_id, wr.workload_version_id, wr.environment_id,
                 e.env_id, wr.repository, wr.git_ref, wr.state::text AS state, wr.attempt,
                 wr.superseded_by, w.kind::text AS kind, w.slug, v.body,
                 e.state::text AS environment_state
          FROM workload_runs wr
          JOIN workloads w ON w.id = wr.workload_id
          JOIN workload_versions v ON v.id = wr.workload_version_id
          JOIN environments e ON e.id = wr.environment_id
          WHERE wr.id = ${input.runId}`)
        const original = rows[0]
        if (!original) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: `No workload run ${input.runId} in this organization.`,
          })
        }
        if (!(TERMINAL_STATES as readonly string[]).includes(original.state)) {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `Run ${input.runId} is still ${original.state}. Retrying now would put two copies ` +
              `of one workload against one environment, which measure each other rather than the ` +
              `application.`,
          })
        }
        if (original.superseded_by) {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message:
              `Run ${input.runId} has already been retried, as ${original.superseded_by}. ` +
              `Retry that one instead: two independent successors to one failure is a history ` +
              `nobody can read.`,
          })
        }
        if (original.environment_state === 'torn_down') {
          throw new TRPCError({
            code: 'PRECONDITION_FAILED',
            message: `${original.env_id} has been torn down, so there is nothing left to run against.`,
          })
        }

        const started = await startRun(db, {
          orgId: c.actor.orgId,
          workloadId: original.workload_id,
          versionId: original.workload_version_id,
          environmentId: original.environment_id,
          repository: original.repository,
          gitRef: original.git_ref,
          workflowFile: input.workflow,
          requestKey: `retry:${input.runId}`,
          requestedBy: c.actor.userId,
          attempt: Number(original.attempt) + 1,
          retryOf: input.runId,
          now: c.clock.now(),
        })

        if (started.created) {
          await markSuperseded(db, { runId: input.runId, by: started.runId, now: c.clock.now() })
          await audit(db, c, {
            action: 'workload.run_retried',
            targetType: 'workload_run',
            targetId: input.runId,
            detail: { runId: started.runId, attempt: Number(original.attempt) + 1 },
          })
        }
        return { started, original }
      })

      if (!prepared.started.created) {
        return {
          runId: prepared.started.runId,
          dispatched: false,
          note: prepared.started.reason ?? null,
        }
      }

      const plan = dispatchInputs(prepared.original.kind, prepared.original.body)
      const installation = await installationFor(c)
      await dispatch(
        c,
        installation,
        prepared.original.repository,
        input.workflow,
        prepared.original.git_ref,
        plan.inputs,
      )
      await c.pool.withTenant(c.tenant, async (db) => {
        await db.execute(sql`
          UPDATE workload_runs SET dispatched_at = ${c.clock.now().toISOString()}::timestamptz,
                                   updated_at = ${c.clock.now().toISOString()}::timestamptz
          WHERE id = ${prepared.started.runId}`)
      })

      return {
        runId: prepared.started.runId,
        dispatched: true,
        retryOf: input.runId,
        pending: 'The run reports here when the engine picks it up.',
      }
    }),
})

/** Exported so the teardown route can record how a command reached a runtime
 *  without importing the whole router. */
export { recordDispatch }
