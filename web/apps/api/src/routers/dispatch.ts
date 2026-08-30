// The verbs, and the one way the control plane is allowed to act.
//
// `environments.create`, `agents.run` and `load.run` are what the product
// does, and all three were declared permissions guarding no route: the hosted
// console could watch environments and could not ask for one.
//
// The tempting fix is to run them here. That would be the wrong product. The
// engine works against a masked branch of the customer's production database,
// their secrets, and their third-party credentials, all of which live in their
// cloud and none of which may cross into this service. A control plane that
// executed an environment would need every one of them, and the sentence this
// whole architecture rests on -- raw snapshots and captured bodies never leave
// the customer's cloud -- would stop being true.
//
// So a verb here dispatches a workflow run in the customer's own repository,
// through the GitHub App installation this control plane already holds. The
// work happens where it already happened; what changes is that a person can
// start it from the console instead of pushing a commit. Nothing about the
// data boundary moves.
//
// What these routes deliberately do NOT do is write an environments or runs
// row. The engine reports those over /v1/events when the work actually starts,
// and a row invented here would be a ghost the moment a runner failed to pick
// the job up: an environment on the page that no engine has ever heard of.
// `environments.teardown` takes the same position from the other direction.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import { checkQuota, DEFAULT_PLAN } from '../limits.ts'
import { GitHubError } from '../auth/github.ts'

/**
 * The workflow file a dispatch targets.
 *
 * `antifailure.yml` is the name examples/github-workflow.yml tells people to
 * copy it to, so the default is the documented path rather than a guess. It is
 * overridable because a repository that already has a workflow of that name,
 * or that keeps its checks under a different one, would otherwise be unable to
 * use any of this.
 */
const workflowFile = z
  .string()
  .min(1)
  .max(100)
  .regex(/^[A-Za-z0-9._-]+\.ya?ml$/, 'a workflow file name, such as antifailure.yml')
  .default('antifailure.yml')

/** A git ref a dispatch may name. Not a commit: GitHub refuses a SHA here. */
const gitRef = z.string().min(1).max(255)

interface Installation extends Record<string, unknown> {
  installation_id: string
  account_login: string
}

/**
 * The installation to dispatch through, or a refusal that says what to do.
 *
 * PRECONDITION_FAILED rather than an internal error, in the same voice as
 * `members.sync`: an organization with no App installed is a supported state
 * of this product, not a fault in it, and the person reading the message is
 * the one who can fix it.
 */
async function installationFor(c: OrgContext): Promise<Installation> {
  const installation = await c.pool.withTenant(c.tenant, async (db) => {
    const rows = await db.execute<Installation>(sql`
      SELECT installation_id, account_login FROM github_installations
      WHERE suspended_at IS NULL ORDER BY created_at ASC LIMIT 1`)
    return rows[0] ?? null
  })
  if (!installation) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        'This organization has no active GitHub App installation, so there is no repository ' +
        'to dispatch a workflow in. Install the App on the organization first.',
    })
  }
  return installation
}

/**
 * Refuses while the organization is suspended.
 *
 * The kill switch's whole promise is that it stops an organization creating
 * anything new while leaving what runs running. Until these routes existed
 * there was nothing in the tRPC surface for it to stop, and `org.suspend` only
 * reached the ingestion path. A verb that dispatched work during a suspension
 * would be the switch quietly not working.
 */
async function refuseWhileSuspended(c: OrgContext): Promise<void> {
  const reason = await c.pool.withTenant(c.tenant, async (db) => {
    const rows = await db.execute<{ suspended_reason: string | null }>(sql`
      SELECT suspended_reason FROM organizations
      WHERE id = ${c.actor.orgId} AND suspended_at IS NOT NULL`)
    if (rows.length === 0) return null
    return rows[0]!.suspended_reason ?? 'no reason was recorded'
  })
  if (reason !== null) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        `This organization is suspended, so it cannot start new work: ${reason}. ` +
        `Environments that are already running are untouched.`,
    })
  }
}

/**
 * Hands GitHub's refusals to the caller as answers.
 *
 * Every one of these is something the operator can act on: install the App,
 * add the workflow file, grant Actions write, push the branch. Behind a 500
 * they read as a bug in this control plane and the sentence that names the fix
 * is thrown away, which is exactly what `members.sync` found.
 */
async function dispatch(
  c: OrgContext,
  installation: Installation,
  repository: string,
  workflow: string,
  ref: string,
  inputs: Record<string, string>,
): Promise<void> {
  try {
    await c.github.dispatchWorkflow(
      Number(installation.installation_id),
      repository,
      workflow,
      ref,
      inputs,
    )
  } catch (error) {
    if (error instanceof GitHubError) {
      throw new TRPCError({ code: 'PRECONDITION_FAILED', message: error.message })
    }
    throw error
  }
}

function noRepository(fullName: string): TRPCError {
  return new TRPCError({
    code: 'NOT_FOUND',
    // The same message whether the repository belongs to another tenant or
    // does not exist, for the same reason routers/index.ts gives: a
    // distinguishing message is a way to ask whether another organization has
    // a repository by that name.
    message: `No repository named ${fullName} in this organization.`,
  })
}

interface Target {
  repository: string
  ref: string
  envId: string
  runtime: string
}

/**
 * The repository and branch behind an environment id.
 *
 * A run is asked for by environment, and the dispatch needs a repository and a
 * ref, so this is the join that turns one into the other. It refuses a torn
 * down environment: dispatching agents at something that no longer exists
 * produces a run that fails in the customer's CI for a reason the console
 * already knew.
 */
async function targetFor(db: Db, envId: string): Promise<Target> {
  const rows = await db.execute<{
    env_id: string
    branch: string
    state: string
    runtime: string | null
    repository: string
  }>(sql`
    SELECT e.env_id, e.branch, e.state::text AS state, e.runtime, r.full_name AS repository
    FROM environments e JOIN repositories r ON r.id = e.repository_id
    WHERE e.env_id = ${envId}`)
  const env = rows[0]
  if (!env) {
    throw new TRPCError({
      code: 'NOT_FOUND',
      message: `No environment named ${envId} in this organization.`,
    })
  }
  if (env.state === 'torn_down') {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message: `${envId} has been torn down, so there is nothing left to run against.`,
    })
  }
  return {
    repository: env.repository,
    ref: env.branch,
    envId: env.env_id,
    runtime: env.runtime ?? '',
  }
}

// ---------------------------------------------------------------------------
// environments.create
// ---------------------------------------------------------------------------

export const createEnvironment = orgProcedure('environments.create')
  .input(
    z.object({
      repository: z.string(),
      /** Absent means the repository's default branch, which is what the
       *  console offers when somebody has not chosen. */
      branch: gitRef.optional(),
      /** A registered runtime's name. Checked against the registry rather than
       *  passed through, or the workflow receives a name for a place this
       *  organization never agreed environments may run. */
      runtime: z.string().max(100).optional(),
      workflow: workflowFile,
    }),
  )
  .mutation(async ({ ctx, input }) => {
    const c = ctx as OrgContext
    await refuseWhileSuspended(c)

    const prepared = await c.pool.withTenant(c.tenant, async (db) => {
      const repos = await db.execute<{ id: string; default_branch: string }>(sql`
        SELECT id, default_branch FROM repositories WHERE full_name = ${input.repository}`)
      const repo = repos[0]
      if (!repo) throw noRepository(input.repository)

      if (input.runtime) {
        const found = await db.execute<{ name: string }>(sql`
          SELECT name FROM runtimes WHERE name = ${input.runtime} AND removed_at IS NULL`)
        if (found.length === 0) {
          throw new TRPCError({
            code: 'NOT_FOUND',
            message:
              `No runtime named ${input.runtime} is registered in this organization. ` +
              `Register it on the Environments page, or leave the runtime unset and let ` +
              `the manifest decide.`,
          })
        }
      }

      // The quota is checked here rather than when the engine reports the
      // environment, because refusing after the workflow has already started
      // means the customer pays for a run this control plane was always going
      // to reject. PLAN_QUOTAS and checkQuota already worked; nothing called
      // them on a path that could refuse anything.
      const counts = await db.execute<{ plan: string; environments: string }>(sql`
        SELECT o.plan,
               (SELECT count(*) FROM environments e
                 WHERE e.org_id = o.id AND e.state <> 'torn_down') AS environments
        FROM organizations o WHERE o.id = ${c.actor.orgId}`)
      const row = counts[0]
      const plan = row?.plan || DEFAULT_PLAN
      const quota = checkQuota(plan, 'environments', Number(row?.environments ?? 0))
      if (!quota.allowed) {
        throw new TRPCError({ code: 'PRECONDITION_FAILED', message: quota.reason })
      }

      return { ref: input.branch ?? repo.default_branch }
    })

    const installation = await installationFor(c)
    await dispatch(c, installation, input.repository, input.workflow, prepared.ref, {
      command: 'up',
      env_id: '',
      runtime: input.runtime ?? '',
      workflows: '',
      profile: '',
    })

    await c.pool.withTenant(c.tenant, async (db) => {
      await audit(db, c, {
        action: 'environment.requested',
        targetType: 'repository',
        targetId: input.repository,
        detail: { ref: prepared.ref, workflow: input.workflow, runtime: input.runtime ?? null },
      })
    })

    return {
      dispatched: true,
      repository: input.repository,
      ref: prepared.ref,
      workflow: input.workflow,
      // Said out loud in the response, because the console shows it and
      // because "created" would be a lie: what exists after this call is a
      // queued GitHub Actions run, not an environment.
      pending: 'The environment appears here when the engine reports it.',
    }
  })

// ---------------------------------------------------------------------------
// agents.run and load.run
// ---------------------------------------------------------------------------

export const agentsRouter = router({
  run: orgProcedure('agents.run')
    .input(
      z.object({
        envId: z.string(),
        /** Which workflows from the manifest to exercise. Empty means all of
         *  them, which is what `af ci` does. */
        workflows: z.array(z.string().min(1).max(200)).max(50).optional(),
        workflow: workflowFile,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await refuseWhileSuspended(c)
      const target = await c.pool.withTenant(c.tenant, (db) => targetFor(db, input.envId))
      const installation = await installationFor(c)

      await dispatch(c, installation, target.repository, input.workflow, target.ref, {
        command: 'agents',
        env_id: target.envId,
        runtime: target.runtime,
        // Comma separated, because a workflow_dispatch input is a string and
        // there is no list type. The engine splits it.
        workflows: (input.workflows ?? []).join(','),
        profile: '',
      })

      await c.pool.withTenant(c.tenant, async (db) => {
        await audit(db, c, {
          action: 'agents.run_requested',
          targetType: 'environment',
          targetId: target.envId,
          detail: { repository: target.repository, workflows: input.workflows ?? null },
        })
      })

      return { dispatched: true, envId: target.envId, repository: target.repository, ref: target.ref }
    }),
})

export const loadRouter = router({
  run: orgProcedure('load.run')
    .input(
      z.object({
        envId: z.string(),
        /** A profile named in the manifest. Absent means the manifest's
         *  default, which is the shape most people want. */
        profile: z.string().max(100).optional(),
        workflow: workflowFile,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      await refuseWhileSuspended(c)
      const target = await c.pool.withTenant(c.tenant, (db) => targetFor(db, input.envId))
      const installation = await installationFor(c)

      await dispatch(c, installation, target.repository, input.workflow, target.ref, {
        command: 'load',
        env_id: target.envId,
        runtime: target.runtime,
        workflows: '',
        profile: input.profile ?? '',
      })

      await c.pool.withTenant(c.tenant, async (db) => {
        await audit(db, c, {
          action: 'load.run_requested',
          targetType: 'environment',
          targetId: target.envId,
          detail: { repository: target.repository, profile: input.profile ?? null },
        })
      })

      return { dispatched: true, envId: target.envId, repository: target.repository, ref: target.ref }
    }),
})
