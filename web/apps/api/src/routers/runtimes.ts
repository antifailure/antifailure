// The runtime registry.
//
// A runtime is a place an organization has agreed its environments may run.
// The control plane holds the name, the provider and the labels, and holds no
// credential and no address: it never connects to one.
//
// What makes this a feature rather than a settings page nobody reads is that
// `list` answers the registry AGAINST REALITY. Every environment reports the
// runtime it came up on, so the list carries both the runtimes somebody
// registered and the runtimes environments are actually running on that nobody
// registered, each with its live count. The second half is the useful one: an
// environment running somewhere the organization never agreed to is a thing
// worth seeing, and it is invisible in a table of what was registered.
//
// What this deliberately does NOT do is send the runtime name to the engine.
// `af up` has no runtime flag, the manifest does no environment interpolation,
// and there is no path by which a name chosen here could change where an
// environment comes up. Passing one as a workflow input anyway would be a
// control that looks like it works, which is the exact defect this branch
// exists to remove rather than relocate.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext, adopted } from '../trpc.ts'

/** The providers the engine knows how to be. Kept in step with
 *  `RuntimeProvider` in engine/pkg/schema/manifest.go by hand, because the two
 *  processes share no code and a value this list accepts and the engine does
 *  not is an environment that fails in the customer's CI. */
const PROVIDERS = ['local', 'kubernetes'] as const

/** A name a person types and reads back in a URL and a log line. Deliberately
 *  narrow: a runtime called `eu west` or `../prod` is a name that will be
 *  quoted wrong by something eventually. */
const runtimeName = z
  .string()
  .min(1)
  .max(100)
  .regex(/^[a-z0-9][a-z0-9-]*$/, 'lower case letters, digits and hyphens')

const label = z.string().min(1).max(60).regex(/^[a-z0-9][a-z0-9-]*$/, 'lower case letters, digits and hyphens')

export const runtimesRouter = router({
  /**
   * Guarded by `environments.view` rather than `runtimes.manage`, because
   * everybody who can read an environment has to be able to see where it is
   * running. Managing the registry is the privileged act; reading it is not.
   */
  list: orgProcedure('environments.view')
    .input(z.object({ includeRemoved: z.boolean().default(false) }).default({ includeRemoved: false }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT r.id, r.name, r.provider, r.labels, r.note, r.created_at, r.removed_at,
                 u.github_login AS registered_by, true AS registered,
                 (SELECT count(*) FROM environments e
                   WHERE e.runtime = r.name AND e.state <> 'torn_down') AS environments
          FROM runtimes r
          LEFT JOIN users u ON u.id = r.registered_by
          WHERE (${input.includeRemoved} OR r.removed_at IS NULL)

          UNION ALL

          -- What is actually running somewhere nobody registered. A registry
          -- that only lists what was registered answers the easy half of the
          -- question and hides the half worth acting on.
          SELECT NULL::uuid, e.runtime, NULL::text, NULL::text[], NULL::text,
                 NULL::timestamptz, NULL::timestamptz, NULL::text, false,
                 count(*)
          FROM environments e
          WHERE e.runtime IS NOT NULL AND e.state <> 'torn_down'
            AND NOT EXISTS (
              SELECT 1 FROM runtimes r
              WHERE r.name = e.runtime AND r.removed_at IS NULL)
          GROUP BY e.runtime

          ORDER BY registered DESC, removed_at NULLS FIRST, name`),
      )
    }),

  register: orgProcedure('runtimes.manage')
    .input(
      z.object({
        name: runtimeName,
        provider: z.enum(PROVIDERS),
        labels: z.array(label).max(20).default([]),
        note: z.string().max(500).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        // Refused rather than upserted. An upsert here would silently move a
        // live runtime from local to kubernetes because somebody re-ran a
        // setup script, and the registry would then describe a place that does
        // not exist while still counting real environments against it.
        const existing = await db.execute<{ id: string }>(sql`
          SELECT id FROM runtimes WHERE name = ${input.name} AND removed_at IS NULL`)
        if (existing.length > 0) {
          throw new TRPCError({
            code: 'BAD_REQUEST',
            message:
              `A runtime named ${input.name} is already registered. Change its labels with ` +
              `runtimes.tag, or remove it first if it is meant to be a different place.`,
          })
        }

        const rows = await db.execute<{ id: string }>(sql`
          INSERT INTO runtimes (org_id, name, provider, labels, note, registered_by)
          VALUES (${c.actor.orgId}, ${input.name}, ${input.provider},
                  ${sql.param(input.labels)}::text[], ${input.note ?? null}, ${c.actor.userId})
          RETURNING id`)
        await audit(db, c, {
          action: 'runtime.registered',
          targetType: 'runtime',
          targetId: input.name,
          detail: { provider: input.provider, labels: input.labels },
        })
        await adopted(db, c, 'runtime_registered')
        return { registered: true, id: rows[0]!.id, name: input.name }
      })
    }),

  /** Replaces the labels rather than adding to them, so that removing one is
   *  possible at all. A tag call with an empty array clears them. */
  tag: orgProcedure('runtimes.manage')
    .input(z.object({ name: runtimeName, labels: z.array(label).max(20) }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ id: string }>(sql`
          UPDATE runtimes SET labels = ${sql.param(input.labels)}::text[],
                              updated_at = ${c.clock.now().toISOString()}
          WHERE name = ${input.name} AND removed_at IS NULL
          RETURNING id`)
        if (rows.length === 0) throw notFound(input.name)
        await audit(db, c, {
          action: 'runtime.tagged',
          targetType: 'runtime',
          targetId: input.name,
          detail: { labels: input.labels },
        })
        return { tagged: true, labels: input.labels }
      })
    }),

  /**
   * Marks a runtime removed. It stays readable and stops being registered.
   *
   * Deleting the row instead would leave every environment that recorded this
   * name pointing at nothing, and those environments are the history somebody
   * reads during an incident. The count of live environments comes back in the
   * answer so that the console can say what is still on it, which is the
   * sentence a person wants after pressing this and not before. Nothing is torn
   * down: an environment on a runtime nobody has registered any more is exactly
   * what the unregistered half of `list` is there to show.
   */
  remove: orgProcedure('runtimes.manage')
    .input(z.object({ name: runtimeName }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ id: string; environments: string }>(sql`
          UPDATE runtimes SET removed_at = ${c.clock.now().toISOString()},
                              updated_at = ${c.clock.now().toISOString()}
          WHERE name = ${input.name} AND removed_at IS NULL
          RETURNING id,
                    (SELECT count(*) FROM environments e
                      WHERE e.runtime = runtimes.name AND e.state <> 'torn_down') AS environments`)
        if (rows.length === 0) throw notFound(input.name)
        const still = Number(rows[0]!.environments)
        await audit(db, c, {
          action: 'runtime.removed',
          targetType: 'runtime',
          targetId: input.name,
          detail: { environmentsStillRunning: still },
        })
        // Nothing is torn down. Removing a runtime stops new environments
        // being asked for on it, in the same spirit as the organization kill
        // switch: the worst moment to destroy what is running is the moment
        // somebody is trying to stop things spreading.
        return { removed: true, environmentsStillRunning: still }
      })
    }),
})

function notFound(name: string): TRPCError {
  return new TRPCError({
    code: 'NOT_FOUND',
    message: `No runtime named ${name} is registered in this organization.`,
  })
}
