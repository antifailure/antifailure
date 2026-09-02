// The plan, and nothing else.
//
// THIS TAKES NO MONEY. There is no Stripe customer, no subscription, no
// checkout, no invoice and no meter behind these two routes, and nothing here
// should be read as the beginning of one. Setting the plan is an
// administrative act by somebody who already holds `billing.manage`, and it is
// audited as one.
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
import { ENTITLEMENTS, resolveEntitlements } from '../entitlements.ts'

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

    // What this organization is ACTUALLY entitled to, which is the plan unless
    // somebody sold them something else. Resolved here so that the screen a
    // customer reads and the check that refuses them agree; before this they
    // could not, because the screen read PLAN_QUOTAS and dispatch reads the
    // override.
    const entitlements = await resolveEntitlements(db, c.clock.now(), {
      orgId: c.actor.orgId,
      plan,
      userId: c.actor.userId,
    })

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
        // The OTHER plans are shown at their own published numbers, deliberately.
        // An override applies to this organization on the plan it is on; saying
        // what a different plan would give has to be the price list, or the
        // comparison somebody makes before upgrading is against a number nobody
        // else gets.
        environments: checkQuota(name, 'environments', Number(row.environments)),
        goldens: checkQuota(name, 'goldens', Number(row.goldens)),
      })),
      holding: { environments: Number(row.environments), goldens: Number(row.goldens) },
      /**
       * Every entitlement as it actually applies, with the plan's own value
       * beside it and the grant that moved it.
       *
       * The console renders an override as an override. That is the whole
       * requirement: a one-off grant that looks like the plan's normal
       * behaviour is a number nobody can explain six months later, and the
       * first person to ask is the customer's finance department wondering why
       * their limit is not the one on the pricing page.
       */
      entitlements: entitlements.all().map((e) => ({
        key: e.key,
        value: e.value,
        planValue: e.planValue,
        unit: ENTITLEMENTS[e.key]?.unit ?? null,
        description: ENTITLEMENTS[e.key]?.description ?? '',
        // Null when the plan decided, which is what the screen keys on.
        override:
          e.override === null
            ? null
            : {
                scope: e.override.scope,
                reason: e.override.reason,
                ticket: e.override.ticket,
                grantedBy: e.override.grantedBy,
                grantedAt: e.override.grantedAt.toISOString(),
                expiresAt: e.override.expiresAt?.toISOString() ?? null,
              },
      })),
      // Said in the payload rather than only in a comment, because the console
      // renders it and somebody reading the API has to know too.
      takesPayment: c.stripe !== null,
      hostedRequiredPlan: c.hostedRequiredPlan,
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
      if (c.stripe || c.hostedRequiredPlan) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message:
            'This installation derives paid plans from Stripe. Use checkout or the billing portal; the plan cannot be set directly.',
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
