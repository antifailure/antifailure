// The operator's view of the machinery, and the switches that stop it.
//
// Every read here is one call: the modules in health.ts, fleet.ts and
// firewall.ts take a Db and filter by nothing, so the SCOPE is whatever
// connection they are handed. `ctx.adminDb` is the operator pool, which
// reaches every tenant, and the same functions serve the per-organization
// console on a tenant connection. That is why there is no second copy of these
// queries and no authorization decision inside any of them: the boundary is
// the procedure, in one place, where it can be tested.
//
// THE ORDER OF WRITES IN THE EMERGENCY ROUTES IS THE POINT. The audit entry is
// INSERTed FIRST, inside the same transaction as the change, so the switch
// cannot commit without its record and a rollback takes both. Writing the
// record afterwards leaves a window where the installation is paused and
// nothing says who did it; writing it in its own transaction leaves a log that
// records things that did not happen. Neither is acceptable for a control that
// stops an installation.

import { TRPCError } from '@trpc/server'
import { z } from 'zod'
import { router } from '../trpc.ts'
import { adminAudit, adminProcedure, type AdminContext } from './trpc.ts'
import { healthChecks, worst } from './health.ts'
import { fleetBlastRadius, requestFleetTeardown, teardownLedger, twins } from './fleet.ts'
import { findings, summary } from './firewall.ts'
import { CONTROL_NAMES, CONTROLS, controlStates, setControl, type ControlName } from './controls.ts'

const controlName = z.enum(CONTROL_NAMES as unknown as [ControlName, ...ControlName[]])

const infraRouter = router({
  /**
   * Every health check, plus the single worst verdict.
   *
   * The summary is derived here rather than stored, so the light on the page
   * cannot disagree with the rows underneath it.
   */
  health: adminProcedure('admin.infra.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    const now = c.clock.now()
    const checks = await c.adminDb((db) => healthChecks(db, now))
    return { checks, verdict: worst(checks), at: now.toISOString() }
  }),

  twins: adminProcedure('admin.infra.read')
    .input(
      z.object({
        orgId: z.string().uuid().nullish(),
        scope: z.enum(['live', 'overdue', 'all']).optional(),
        limit: z.number().int().min(1).max(500).optional(),
      }).optional(),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb((db) => twins(db, c.clock.now(), input ?? {}))
    }),

  /**
   * The teardown ledger.
   *
   * Carries `standing`, which is not the `state` column. `pending` covers both
   * "asked for a second ago" and "asked for yesterday with nothing to reach",
   * and those are not the same situation to anybody looking at this page.
   */
  teardowns: adminProcedure('admin.infra.read')
    .input(
      z.object({
        orgId: z.string().uuid().nullish(),
        open: z.boolean().optional(),
        limit: z.number().int().min(1).max(500).optional(),
      }).optional(),
    )
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb((db) => teardownLedger(db, c.clock.now(), input ?? {}))
    }),

  firewall: adminProcedure('admin.infra.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    return c.adminDb(async (db) => ({
      summary: await summary(db),
      findings: await findings(db),
    }))
  }),

  /**
   * What tearing down everything in scope would touch, before anybody confirms.
   *
   * Computed rather than estimated. An operator confirming a blast radius
   * written in prose is confirming somebody's recollection of what the query
   * would return.
   */
  teardownRadius: adminProcedure('admin.infra.teardown')
    .input(z.object({ orgId: z.string().uuid().nullish() }).optional())
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb((db) => fleetBlastRadius(db, input ?? {}))
    }),

  /**
   * Records a teardown request for every live environment in scope.
   *
   * Writes rows. Sends nothing. The sweeper is what reaches a runtime, so the
   * response says per environment whether anything CAN be reached, and the
   * caller must not report "terminated" over a row that will sit pending until
   * it is abandoned.
   */
  teardownFleet: adminProcedure('admin.infra.teardown')
    .input(
      z.object({
        orgId: z.string().uuid().nullish(),
        reason: z.string().min(1).max(500),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const radius = await fleetBlastRadius(db, { orgId: input.orgId })
        // First, in the same transaction. If the requests fail, this rolls
        // back with them; if this fails, nothing is asked for.
        await adminAudit(db, c, {
          action: 'infra.fleet_teardown_requested',
          targetType: input.orgId ? 'organization' : 'installation',
          targetId: input.orgId ?? null,
          subjectOrgId: input.orgId ?? null,
          severity: input.orgId ? 'high' : 'critical',
          detail: { reason: input.reason, before: radius },
        })
        const requested = await requestFleetTeardown(
          db,
          c.clock.now(),
          // The operator's id, not a tenant user's. requested_by references
          // users, which an operator is deliberately not in, so this is null
          // and the audit entry is what names who asked.
          { userId: null },
          input.reason,
          { orgId: input.orgId },
        )
        const unreachable = requested.filter((r) => r.reachable === false).length
        return {
          radius,
          requested,
          recorded: requested.filter((r) => r.recorded).length,
          // Said out loud, because "requested" and "torn down" are different
          // things and a console that reports the second while doing the first
          // is the defect the teardown ledger exists to prevent.
          unreachable,
          pending:
            unreachable > 0
              ? `${unreachable} of these have no workflow run recorded, so no cancel can be sent for them. They will sit pending until the sweeper abandons them.`
              : 'Each environment disappears here when its runtime confirms it is gone.',
        }
      })
    }),
})

const emergencyRouter = router({
  /** Every switch and whether it is engaged, including the ones nobody has
   *  touched, so a fresh installation renders the full set. */
  controls: adminProcedure('admin.emergency.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    return c.adminDb(controlStates)
  }),

  /**
   * Engages or releases one switch.
   *
   * Gated on `admin.emergency.engage`, which only owner and super_admin hold.
   * Gated on the PERMISSION rather than on role rank, deliberately: ordering
   * roles and comparing ranks is how a permission model stops being a table
   * and starts being an assumption, and the assumption breaks the first time a
   * role does not fit the line.
   */
  set: adminProcedure('admin.emergency.engage')
    .input(
      z.object({
        name: controlName,
        engaged: z.boolean(),
        /** Required to engage, refused empty. A switch that stops an
         *  installation with no reason recorded is one the next person on call
         *  cannot safely release. */
        reason: z.string().max(500).nullish(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      if (input.engaged && (!input.reason || input.reason.trim() === '')) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message: `Engaging ${CONTROLS[input.name].title} needs a reason. It is what the next person on call reads before releasing it.`,
        })
      }
      return c.adminDb(async (db) => {
        const before = (await controlStates(db)).find((s) => s.name === input.name) ?? null
        // FIRST, and in the same transaction as the change below. A high
        // severity record that the installation was paused must not be able to
        // be missing while the pause is in force.
        await adminAudit(db, c, {
          action: input.engaged ? 'emergency.engaged' : 'emergency.released',
          targetType: 'platform_control',
          targetId: input.name,
          // Null on purpose. This concerns the installation, not one tenant,
          // and admin_audit_entries.subject_org_id is nullable precisely so
          // that an installation-wide event writes ONE row rather than a
          // fabricated row per organization.
          subjectOrgId: null,
          severity: input.engaged ? 'critical' : 'high',
          detail: {
            control: input.name,
            effect: CONTROLS[input.name].effect,
            enforcedBy: CONTROLS[input.name].enforcedBy,
            reason: input.reason ?? null,
            before: before && { engaged: before.engaged, reason: before.reason },
          },
        })
        const after = await setControl(
          db,
          c.clock.now(),
          input.name,
          input.engaged,
          // AdminActor.label IS the operator's address in this version: its
          // own doc comment says "the operator's email, which is what an audit
          // entry should name a year from now when the row may be gone". If a
          // separate `email` field is added later, this becomes that field,
          // because what belongs in the row is the identifying value and not a
          // display name, which is not unique.
          { label: c.admin.label },
          input.reason ?? null,
        )
        return after
      })
    }),
})

export const adminInfraRouter = router({
  infra: infraRouter,
  emergency: emergencyRouter,
})
