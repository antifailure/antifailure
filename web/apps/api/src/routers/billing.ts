// The plan, and nothing else.
//
// THIS TAKES NO MONEY. There is no Stripe customer, no subscription, no
// checkout, no invoice and no meter behind these two routes, and nothing here
// should be read as the beginning of one. Setting the plan is an
// administrative act by somebody who already holds `billing.manage`, and it is
// audited as one.
//
// It is also OFF unless an operator turns it on, and that is not a detail. The
// person holding `billing.manage` is the org owner, which the first person into
// any organization becomes, and on a plane serving anybody but its operator
// that person setting their own plan is a stranger writing their own
// entitlement. `AF_OPERATOR_SETS_PLAN` is the operator saying the two are the
// same person here. See `hosted.ts`.
//
// The reason it is worth building on its own is that the enforcement already
// exists and had nothing to point at. `PLAN_QUOTAS` defines free, team and
// enterprise, `checkQuota` refuses over the limit, and `organizations.plan`
// has been whatever the row was seeded with since it was added, because
// `billing.manage` guarded no route. So the quota system worked and could not
// be exercised.
//
// A payment integration is a separate piece of work with its own decisions
// waiting on it -- whether pricing is per seat or metered, which is a schema
// decision that is much worse to retrofit than to make -- and half of one
// wired to a live card is worse than none.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router, orgProcedure, audit, type OrgContext } from '../trpc.ts'
import { checkQuota, DEFAULT_PLAN, PLAN_QUOTAS } from '../limits.ts'
import { planSetRefusal } from '../hosted.ts'

const PLANS = Object.keys(PLAN_QUOTAS) as [string, ...string[]]

interface OrgRow extends Record<string, unknown> {
  plan: string
  environments: string
  goldens: string
}

async function plans(c: OrgContext) {
  return c.pool.withTenant(c.tenant, async (db) => {
    const rows = await db.execute<OrgRow>(sql`
      SELECT o.plan,
             (SELECT count(*) FROM environments e
               WHERE e.org_id = o.id AND e.state <> 'torn_down') AS environments,
             (SELECT count(*) FROM golden_versions g WHERE g.org_id = o.id) AS goldens
      FROM organizations o WHERE o.id = ${c.actor.orgId}`)
    const row = rows[0]
    if (!row) {
      throw new TRPCError({
        code: 'NOT_FOUND',
        message: `No organization named ${c.actor.orgId} in this organization.`,
      })
    }
    const plan = row.plan || DEFAULT_PLAN
    return {
      plan,
      // Every plan, with its limits and what this organization is holding
      // against each. A change control that shows only the current plan makes
      // somebody guess whether the one they are about to choose is smaller
      // than what they already have.
      plans: Object.entries(PLAN_QUOTAS).map(([name, quota]) => ({
        name,
        current: name === plan,
        quota,
        environments: checkQuota(name, 'environments', Number(row.environments)),
        goldens: checkQuota(name, 'goldens', Number(row.goldens)),
      })),
      holding: { environments: Number(row.environments), goldens: Number(row.goldens) },
      // Said in the payload rather than only in a comment, because the console
      // renders it and somebody reading the API has to know too.
      takesPayment: c.stripe !== null,
      hostedRequiredPlan: c.hostedRequiredPlan,
      // Whether `set` below would do anything, so the console can leave the
      // control out rather than drawing one that always refuses. A button that
      // is always refused reads as broken rather than as not offered.
      operatorSetsPlan: c.operatorSetsPlan,
    }
  })
}

export const billingRouter = router({
  /**
   * Guarded by `billing.manage` rather than `environments.view`, even though
   * `org.status` already returns the plan name to anybody who can see the
   * organization. What is here and not there is every plan's limits beside
   * what this organization holds, which is the shape of a commercial decision
   * rather than an operational one.
   */
  get: orgProcedure('billing.manage').query(async ({ ctx }) => plans(ctx as OrgContext)),

  set: orgProcedure('billing.manage')
    .input(z.object({ plan: z.enum(PLANS), reason: z.string().max(500).optional() }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as OrgContext
      // DEFAULT DENY, and the inversion is the whole fix.
      //
      // This used to refuse only `if (c.stripe || c.hostedRequiredPlan)`, which
      // is a guard that reads correctly and protects nobody: it asks whether
      // billing was configured, and the dangerous plane is precisely the one
      // where it was not. A hosted control plane whose operator has not reached
      // Stripe yet configures neither, so both were null, so an org owner could
      // call this with `enterprise` and grant themselves five hundred
      // environments. The first person into any organization is its owner, and
      // an owner holds `billing.manage`, so that was every signed-in tenant.
      //
      // Now the question is the one that actually decides it: has whoever runs
      // this installation SAID they set plans by hand? Unset means no, so a
      // plane nobody has configured refuses, and the operator who legitimately
      // wants the route sets AF_OPERATOR_SETS_PLAN=1 once. main.ts refuses to
      // start if that is combined with Stripe or the hosted gate, so reaching
      // this line with the flag on means this process takes no money at all.
      if (!c.operatorSetsPlan) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message: planSetRefusal(c.stripe !== null || c.hostedRequiredPlan !== null),
        })
      }
      const changed = await c.pool.withTenant(c.tenant, async (db) => {
        const rows = await db.execute<{ plan: string }>(sql`
          UPDATE organizations
          SET plan = ${input.plan}, updated_at = ${c.clock.now().toISOString()}
          WHERE id = ${c.actor.orgId} AND plan <> ${input.plan}
          RETURNING plan`)
        // Setting the plan it already has is not an error and is not an audit
        // entry either. A no-op that writes a "changed the plan" line into the
        // log makes the log worse at the one job it has.
        if (rows.length === 0) return false
        await audit(db, c, {
          action: 'organization.plan_changed',
          targetType: 'organization',
          targetId: c.actor.orgId,
          detail: { plan: input.plan, reason: input.reason ?? null, tookPayment: false },
        })
        return true
      })

      // Deliberately nothing is torn down when a plan shrinks. A quota that is
      // over its limit refuses the NEXT creation and removes nothing, which is
      // the rule checkQuota already states and the only one that does not
      // destroy a customer's running work because of a billing change.
      return { ...(await plans(c)), changed }
    }),
})
