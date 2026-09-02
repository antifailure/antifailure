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
//
// The inputs a dispatch sends are exactly the flags the engine's CLI has, and
// nothing else. `af up` takes no runtime and `af load run` has no profile, so
// neither is sent: an input the workflow cannot act on is the same dead socket
// this whole change exists to close, moved one process along.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import { DEFAULT_PLAN } from '../limits.ts'
import {
  checkCostCapWithEntitlements,
  checkQuotaWithEntitlements,
  resolveEntitlements,
} from '../entitlements.ts'
import { environmentHoursSince } from '../costs.ts'
import { GitHubError, blockerFor, type DispatchCause } from '../auth/github.ts'

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
    repository: string
  }>(sql`
    SELECT e.env_id, e.branch, e.state::text AS state, r.full_name AS repository
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
  return { repository: env.repository, ref: env.branch, envId: env.env_id }
}

// ---------------------------------------------------------------------------
// environments.readiness
// ---------------------------------------------------------------------------

/**
 * Whether a dispatch would work, asked before anybody fills in the form.
 *
 * Three states and not two. `blocked` is something GitHub said, `ready` is
 * nothing GitHub said would stop it, and `unknown` is this control plane not
 * having been able to ask. Collapsing `unknown` into either of the others is
 * the mistake worth avoiding: reported as blocked it is a false alarm on a
 * page's primary action, and reported as ready it is a promise made on no
 * evidence. It renders as nothing at all.
 */
type Readiness =
  | { status: 'ready' }
  | { status: 'blocked'; cause: DispatchCause; message: string }
  | { status: 'unknown' }

/**
 * Guarded by `environments.create` rather than by a permission of its own.
 *
 * The question this answers is "would the button work", so the people who
 * should be told are exactly the people who have the button. A new permission
 * here would guard one query, which is how this repository ends up with
 * permissions that no route reads.
 *
 * It refuses nothing and writes nothing, so a wrong answer costs a sentence on
 * a screen. That is what lets it report a missing installation as a state
 * instead of throwing the way `environments.create` does: a query that throws
 * on a supported state makes a page look broken, and an organization that has
 * not installed the App yet is the most ordinary state this product has.
 */
export const environmentReadiness = orgProcedure('environments.create')
  .input(z.object({ repository: z.string(), workflow: workflowFile }))
  .query(async ({ ctx, input }): Promise<Readiness> => {
    const c = ctx as OrgContext
    const installation = await c.pool.withTenant(c.tenant, async (db) => {
      const rows = await db.execute<Installation>(sql`
        SELECT installation_id, account_login FROM github_installations
        WHERE suspended_at IS NULL ORDER BY created_at ASC LIMIT 1`)
      return rows[0] ?? null
    })
    if (!installation) {
      return {
        status: 'blocked',
        ...blockerFor('app-not-installed', {
          repository: input.repository,
          workflow: input.workflow,
        }),
      }
    }
    try {
      const blocker = await c.github.dispatchBlocker(
        Number(installation.installation_id),
        input.repository,
        input.workflow,
      )
      return blocker ? { status: 'blocked', ...blocker } : { status: 'ready' }
    } catch {
      // GitHub being unreachable is not evidence about the customer's setup.
      // Saying so is the only honest answer, and the screen shows nothing.
      return { status: 'unknown' }
    }
  })

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
      workflow: workflowFile,
      /**
       * The lifetime the environment will be created with, in hours, when the
       * caller knows it.
       *
       * Nothing supplies it today, and the comment here used to claim af up
       * did. It does not: af up talks to no tRPC route, and the only caller of
       * this procedure is the console, which dispatches a workflow and has
       * never read the repository's manifest. So every dispatched run reserves
       * the plan's whole per-run allowance below. That is the conservative
       * direction and it is why this is still worth accepting rather than
       * deleting: the field is the seam a caller that DOES know the lifetime
       * reserves honestly through, and reserving less for a run that did not
       * say would let an unstated run slip past a cap that a stated one of the
       * same size is refused for.
       *
       * Bounded here as well as by the cap, because it is a number from a
       * client and an unbounded one would be arithmetic on infinity in the
       * projection below. A year is far above any lifetime the caps allow, so
       * the refusal a caller actually sees is the cap's, which names a number
       * they can act on, rather than a validation error about a field they did
       * not know they were sending.
       */
      ttlHours: z.number().positive().max(8760).optional(),
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

      // The plan says what this organization gets; an override says what it was
      // SOLD. Resolved here, inside the transaction that is already open, and
      // in the same tenant scope, so the read costs one more statement rather
      // than one more round trip and cannot see another tenant's grants.
      //
      // Both scopes that can apply to a creation are passed. The repository is
      // the `project` scope, because capacity is routinely sold for one
      // repository rather than for a whole organization, and the acting user is
      // the `user` scope. Leaving either out would make a grant at that scope a
      // row that changes nothing, which is the exact failure the catalogue's
      // `enforcedAt` field exists to prevent.
      const entitlements = await resolveEntitlements(db, c.clock.now(), {
        orgId: c.actor.orgId,
        plan,
        userId: c.actor.userId,
        repositoryId: repo.id,
      })

      const quota = checkQuotaWithEntitlements(
        entitlements, 'environments', Number(row?.environments ?? 0),
      )
      if (!quota.allowed) {
        throw new TRPCError({ code: 'PRECONDITION_FAILED', message: quota.reason })
      }

      // The cost caps, in environment-hours, on the same call and for the same
      // reason: refusing after the workflow has started means the customer
      // paid for a run this control plane was always going to reject.
      //
      // The quota above answers "how many at once" and this answers "how
      // much", and an organization can sit inside the first while a loop
      // creating and tearing down one environment at a time runs up a month of
      // environment time in an afternoon.
      const now = c.clock.now()
      const used = await environmentHoursSince(
        db, c.actor.orgId, new Date(now.getTime() - 24 * 60 * 60 * 1000), now,
      )
      // What this run commits to is the lifetime the environment will be
      // created with: the engine stamps runtime.ttl on the resources at
      // creation and the reaper enforces it, so the hours asked for here are
      // the hours that will actually be held.
      // Absent means the caller did not say, and the conservative reading of
      // "unknown lifetime" is the most the plan would allow for one: reserving
      // less would let an unstated run slip past the daily cap that a stated
      // one of the same size is refused for.
      //
      // Read off the ENTITLEMENT rather than off the plan, because an
      // organization sold a longer per-run cap must have its unstated runs
      // reserved at the cap it actually holds. Reserving the plan's smaller
      // number here would refuse a run that the next line was about to allow.
      const runHours = input.ttlHours ?? entitlements.number('perRunHours')
      const cap = checkCostCapWithEntitlements(entitlements, runHours, used)
      if (!cap.allowed) {
        throw new TRPCError({ code: 'PRECONDITION_FAILED', message: cap.reason })
      }

      return { ref: input.branch ?? repo.default_branch }
    })

    const installation = await installationFor(c)
    await dispatch(c, installation, input.repository, input.workflow, prepared.ref, {
      command: 'up',
      workflows: '',
      duration: '',
      scale: '',
    })

    await c.pool.withTenant(c.tenant, async (db) => {
      await audit(db, c, {
        action: 'environment.requested',
        targetType: 'repository',
        targetId: input.repository,
        detail: { ref: prepared.ref, workflow: input.workflow },
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
        // Comma separated, because a workflow_dispatch input is a string and
        // there is no list type. The workflow splits it back into one --only
        // per name.
        workflows: (input.workflows ?? []).join(','),
        duration: '',
        scale: '',
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
        /** Seconds, sent to `af load run --duration`. Absent leaves the
         *  command's own default, which is a minute. */
        seconds: z.number().int().min(1).max(3600).optional(),
        /** Multiplier on production's rate, sent to `--scale`. */
        scale: z.number().min(0.01).max(100).optional(),
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
        workflows: '',
        // A Go duration, because that is what `af load run --duration` parses.
        // The input is seconds so that the console cannot send `1 hour` and
        // have the engine refuse it after the job has started.
        duration: input.seconds === undefined ? '' : `${input.seconds}s`,
        scale: input.scale === undefined ? '' : String(input.scale),
      })

      await c.pool.withTenant(c.tenant, async (db) => {
        await audit(db, c, {
          action: 'load.run_requested',
          targetType: 'environment',
          targetId: target.envId,
          detail: { repository: target.repository, seconds: input.seconds ?? null, scale: input.scale ?? null },
        })
      })

      return { dispatched: true, envId: target.envId, repository: target.repository, ref: target.ref }
    }),
})
