// The runtime registry.
//
// A runtime is a place an organization has agreed environments may run. The
// control plane holds the name, the provider and whatever labels the
// organization uses to tell them apart, and holds no credential and no
// address: it never connects to one. What the registry is FOR is the list a
// person picks from when they ask for an environment, and the name that
// travels to the customer's CI as a workflow input.
//
// That is the whole reason it is not a settings page nobody reads.
// `environments.create` refuses a runtime that is not registered here, so
// removing one is an act with an effect, not a row disappearing from a table.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'

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
   * everybody who can ask for an environment has to be able to see the list
   * they are choosing from. Managing the registry is the privileged act;
   * reading it is not.
   */
  list: orgProcedure('environments.view')
    .input(z.object({ includeRemoved: z.boolean().default(false) }).default({ includeRemoved: false }))
    .query(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      return c.pool.withTenant(c.tenant, async (db) =>
        db.execute(sql`
          SELECT r.id, r.name, r.provider, r.labels, r.note, r.created_at, r.removed_at,
                 u.github_login AS registered_by,
                 (SELECT count(*) FROM environments e
                   WHERE e.runtime = r.name AND e.state <> 'torn_down') AS environments
          FROM runtimes r
          LEFT JOIN users u ON u.id = r.registered_by
          WHERE (${input.includeRemoved} OR r.removed_at IS NULL)
          ORDER BY r.removed_at NULLS FIRST, r.name`),
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
        // setup script, and every environment created after that would be
        // dispatched somewhere nobody chose.
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
   * Marks a runtime removed. It stops being offered and stays readable.
   *
   * Deleting the row instead would leave every environment that recorded this
   * name pointing at nothing, and those environments are the history somebody
   * reads during an incident. The count of live environments comes back in the
   * answer so that the console can say what was still using it, which is the
   * sentence a person wants after pressing this and not before.
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
