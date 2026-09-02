// The operator's money, entitlement and flag routes.
//
// Every procedure here is `adminProcedure(permission)`, which is the only way
// to build one: the permission and the route are declared in the same act, the
// same rule `orgProcedure` states for the tenant surface. There is no route in
// this file that could be reached without an operator session and a permission.
//
// WHY THIS IS A SEPARATE ROUTER FROM `appRouter`, mounted at its own endpoint.
//
// The tenant tree is walked by permissions.test.ts, which asserts that every
// procedure in it declares a TENANT permission or is deliberately public. An
// operator route declares an operator permission, which is a different catalog
// with different roles, so putting these in that tree would mean teaching that
// test to skip a third of what it walks, and a matrix test with an exception
// list is a matrix test that stops being the thing that catches an unguarded
// route. Two trees, two matrices, one rule each.
//
// WHAT THE ROUTES DO NOT DO. None of them writes a local copy of anything
// Stripe owns. The reads go to Stripe, the writes go through the ledger to
// Stripe, and the `subscriptions` and `invoices` rows are moved by the webhook,
// which is the only path that handles ordering and retries. See money.ts.

import { z } from 'zod'
import { TRPCError } from '@trpc/server'
import { sql } from 'drizzle-orm'
import { router } from '../trpc.ts'
import { adminAudit, adminProcedure, type AdminContext } from './trpc.ts'
import {
  applyDiscount,
  cancelSubscription,
  changePlan,
  creditCustomer,
  extendTrial,
  reactivateSubscription,
  refundCharge,
  resendInvoice,
  retryPayment,
  type MoneyContext,
} from './money.ts'
import { ENTITLEMENTS, applyOverrides } from '../entitlements.ts'
import { KNOWN_FLAGS } from '../flags.ts'
import { PAID_PLANS } from '../billing/plans.ts'

const uuid = z.string().uuid()
/** Every money action says why, and an empty reason is refused at the edge as
 *  well as in the ledger. Two checks because the message differs: here it names
 *  the field, and there it names the action. */
const reason = z.string().trim().min(8).max(500)

/**
 * The organization an operator is acting on, read once so its slug can go on
 * the audit entry.
 *
 * Not optional. `subject_org_label` is what still names the tenant a year from
 * now when the row is gone, and looking it up at the start of an action is the
 * only moment it is certainly still there.
 */
async function subject(c: AdminContext, orgId: string): Promise<{ slug: string; plan: string }> {
  const rows = await c.adminDb(async (db) =>
    db.execute<{ slug: string; plan: string }>(sql`
      SELECT slug, plan FROM organizations WHERE id = ${orgId}`),
  )
  const row = rows[0]
  if (!row) {
    throw new TRPCError({ code: 'NOT_FOUND', message: `No organization ${orgId}.` })
  }
  return row
}

/** The money context, from an operator request. */
async function moneyContext(c: AdminContext, orgId: string): Promise<MoneyContext & { slug: string }> {
  if (!c.stripe) {
    throw new TRPCError({
      code: 'PRECONDITION_FAILED',
      message:
        'This installation has no Stripe configuration, so there is nothing to act on. Set ' +
        'AF_STRIPE_SECRET_KEY and the price variables.',
    })
  }
  const org = await subject(c, orgId)
  return {
    stripe: c.stripe,
    withAdmin: c.adminDb,
    now: c.clock.now(),
    adminUserId: c.admin.adminUserId,
    actorLabel: c.admin.label,
    orgLabel: org.slug,
    ip: c.ip ?? null,
    slug: org.slug,
  }
}

/** The Stripe customer an organization is billed as, or null. */
async function customerFor(c: AdminContext, orgId: string): Promise<string | null> {
  const rows = await c.adminDb(async (db) =>
    db.execute<{ stripe_customer_id: string }>(sql`
      SELECT stripe_customer_id FROM billing_customers WHERE org_id = ${orgId}`),
  )
  return rows[0]?.stripe_customer_id ?? null
}

// ---------------------------------------------------------------------------
// Billing
// ---------------------------------------------------------------------------

export const adminBillingRouter = router({
  /**
   * Everything the billing screen shows for one customer, read from STRIPE
   * rather than from the local mirror.
   *
   * The local rows exist and are deliberately not what this returns. An
   * operator looking at a customer during a support call needs what the
   * provider believes right now, and the mirror is whatever the last webhook
   * left behind: on the one occasion the two disagree, the mirror is the one
   * that is wrong and the operator is the person least able to tell.
   *
   * What IS read locally is the ledger, because it is the platform's own record
   * of what operators did and Stripe has never heard of it.
   */
  customer: adminProcedure('admin.billing.read')
    .input(z.object({ orgId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const org = await subject(c, input.orgId)
      const customerId = await customerFor(c, input.orgId)

      const operations = await c.adminDb(async (db) =>
        db.execute<Record<string, unknown>>(sql`
          SELECT idempotency_key, action, target_type, target_id, actor_label, reason,
                 state, amount_minor, currency, provider_object_id, error_message,
                 started_at, finished_at
          FROM admin_operations WHERE org_id = ${input.orgId}
          ORDER BY started_at DESC LIMIT 50`),
      )

      if (!customerId || !c.stripe) {
        // A real answer rather than an empty one. An organization with no
        // Stripe customer has never started a checkout, and a screen that
        // rendered blank would read as a failed load.
        return {
          org: { id: input.orgId, slug: org.slug, plan: org.plan },
          takesPayment: c.stripe !== null,
          customer: null,
          subscriptions: [],
          invoices: [],
          charges: [],
          operations,
        }
      }

      const client = c.stripe.client
      // Read in parallel: four independent round trips to the same provider,
      // and an operator on a support call is waiting for all of them.
      const [customer, subscriptions, invoices, charges] = await Promise.all([
        client.getCustomer(customerId),
        client.listSubscriptions(customerId, 10),
        client.listInvoices(customerId, 20),
        client.listCharges(customerId, 20),
      ])

      return {
        org: { id: input.orgId, slug: org.slug, plan: org.plan },
        takesPayment: true,
        customer: customer
          ? {
              id: customer.id,
              email: customer.email,
              // Stripe's sign, flipped once here so no screen has to hold it:
              // a negative balance at Stripe is credit the customer may spend.
              creditMinor: customer.balance < 0 ? -customer.balance : 0,
              owedMinor: customer.balance > 0 ? customer.balance : 0,
              currency: customer.currency,
              delinquent: customer.delinquent,
              discountCoupon: customer.discountCoupon,
            }
          : null,
        subscriptions,
        invoices,
        charges,
        operations,
      }
    }),

  refund: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        chargeId: z.string().min(3),
        /** Absent means the whole charge. */
        amountMinor: z.number().int().positive().optional(),
        category: z.enum(['duplicate', 'fraudulent', 'requested_by_customer']).optional(),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return refundCharge(await moneyContext(c, input.orgId), input)
    }),

  credit: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        amountMinor: z.number().int().positive(),
        currency: z.string().length(3),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const customerId = await customerFor(c, input.orgId)
      if (!customerId) {
        throw new TRPCError({
          code: 'PRECONDITION_FAILED',
          message: 'That organization has no Stripe customer, so there is nothing to credit.',
        })
      }
      return creditCustomer(await moneyContext(c, input.orgId), { ...input, customerId })
    }),

  changePlan: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        subscriptionId: z.string().min(3),
        plan: z.enum(PAID_PLANS as unknown as [string, ...string[]]),
        /** Whether the customer is billed for the difference today. Required
         *  rather than defaulted: it decides whether somebody is charged now,
         *  and that is the operator's decision to make out loud. */
        prorate: z.boolean(),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return changePlan(await moneyContext(c, input.orgId), {
        ...input,
        plan: input.plan as 'team' | 'enterprise',
      })
    }),

  extendTrial: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        subscriptionId: z.string().min(3),
        until: z.string().datetime(),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return extendTrial(await moneyContext(c, input.orgId), {
        ...input,
        until: new Date(input.until),
      })
    }),

  cancel: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        subscriptionId: z.string().min(3),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      cancelSubscription(await moneyContext(ctx as AdminContext, input.orgId), input),
    ),

  reactivate: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        subscriptionId: z.string().min(3),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      reactivateSubscription(await moneyContext(ctx as AdminContext, input.orgId), input),
    ),

  discount: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        subscriptionId: z.string().min(3),
        coupon: z.string().min(1).max(200),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      applyDiscount(await moneyContext(ctx as AdminContext, input.orgId), input),
    ),

  retryPayment: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        invoiceId: z.string().min(3),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      retryPayment(await moneyContext(ctx as AdminContext, input.orgId), input),
    ),

  resendInvoice: adminProcedure('admin.billing.write')
    .input(
      z.object({
        orgId: uuid,
        invoiceId: z.string().min(3),
        reason,
        idempotencyKey: z.string().min(8).max(200).optional(),
      }),
    )
    .mutation(async ({ ctx, input }) =>
      resendInvoice(await moneyContext(ctx as AdminContext, input.orgId), input),
    ),
})

// ---------------------------------------------------------------------------
// Entitlements
// ---------------------------------------------------------------------------

interface OverrideRow extends Record<string, unknown> {
  id: string
  scope: string
  scope_id: string | null
  feature: string
  value: unknown
  reason: string
  ticket: string | null
  created_by_label: string
  created_at: string | Date
  expires_at: string | Date | null
}

export const adminEntitlementsRouter = router({
  /** What one organization is entitled to, resolved, with the plan's own value
   *  beside it and the grant that moved it. The same shape the customer's own
   *  Plan screen gets, so the operator and the customer are looking at one
   *  answer rather than two that can disagree. */
  forOrganization: adminProcedure('admin.entitlements.read')
    .input(z.object({ orgId: uuid }))
    .query(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      const org = await subject(c, input.orgId)
      const rows = await c.adminDb(async (db) =>
        db.execute<OverrideRow>(sql`
          SELECT id, scope, scope_id, feature, value, reason, ticket,
                 created_by_label, created_at, expires_at
          FROM entitlement_overrides
          WHERE revoked_at IS NULL
            AND (expires_at IS NULL OR expires_at > ${c.clock.now().toISOString()})
            AND (scope = 'global' OR org_id = ${input.orgId})`),
      )
      const resolved = applyOverrides(org.plan, rows)
      return {
        org: { id: input.orgId, slug: org.slug, plan: org.plan },
        entitlements: resolved.all().map((e) => ({
          ...e,
          unit: ENTITLEMENTS[e.key]?.unit ?? null,
          description: ENTITLEMENTS[e.key]?.description ?? '',
          enforced: ENTITLEMENTS[e.key]?.enforcedAt !== null,
          notEnforcedBecause: ENTITLEMENTS[e.key]?.notEnforcedBecause ?? null,
          override: e.override
            ? { ...e.override, grantedAt: e.override.grantedAt.toISOString(),
                expiresAt: e.override.expiresAt?.toISOString() ?? null }
            : null,
        })),
      }
    }),

  /**
   * Grants a limit other than the plan's.
   *
   * REVOKE THEN INSERT, in one transaction, rather than an upsert. The live
   * unique index would let an upsert quietly replace a grant, and then "who
   * changed this and what was it before" has no answer. Replacing a grant is
   * two recorded facts because it is two facts.
   */
  grant: adminProcedure('admin.entitlements.write')
    .input(
      z.object({
        scope: z.enum(['global', 'organization', 'project', 'user']),
        /** Null only for global; the database enforces the same rule. */
        scopeId: uuid.nullable(),
        orgId: uuid.nullable(),
        feature: z.string().min(1).max(100),
        value: z.union([z.number(), z.boolean()]),
        reason,
        ticket: z.string().max(200).optional(),
        /** Absent is forever, and the screen makes somebody choose it. */
        expiresAt: z.string().datetime().nullable(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      if (!ENTITLEMENTS[input.feature]) {
        throw new TRPCError({
          code: 'BAD_REQUEST',
          message:
            `There is no entitlement called ${input.feature}. Granting one this build cannot ` +
            'read would be a row that changes nothing.',
        })
      }
      const spec = ENTITLEMENTS[input.feature]!
      if (spec.kind === 'number' && typeof input.value !== 'number') {
        throw new TRPCError({ code: 'BAD_REQUEST', message: `${input.feature} is a number.` })
      }
      if (spec.kind === 'boolean' && typeof input.value !== 'boolean') {
        throw new TRPCError({ code: 'BAD_REQUEST', message: `${input.feature} is true or false.` })
      }

      return c.adminDb(async (db) => {
        const previous = await db.execute<{ id: string; value: unknown }>(sql`
          UPDATE entitlement_overrides
          SET revoked_at = ${c.clock.now().toISOString()},
              revoked_by_label = ${c.admin.label},
              revoked_reason = ${'Replaced by a new grant'}
          WHERE revoked_at IS NULL AND scope = ${input.scope}
            AND coalesce(scope_id, '00000000-0000-0000-0000-000000000000'::uuid)
              = coalesce(${input.scopeId}::uuid, '00000000-0000-0000-0000-000000000000'::uuid)
            AND feature = ${input.feature}
          RETURNING id, value`)

        const created = await db.execute<{ id: string }>(sql`
          INSERT INTO entitlement_overrides
            (scope, scope_id, org_id, feature, value, reason, ticket,
             created_by_label, expires_at)
          VALUES (${input.scope}, ${input.scopeId}, ${input.orgId}, ${input.feature},
                  ${JSON.stringify(input.value)}::jsonb, ${input.reason},
                  ${input.ticket ?? null}, ${c.admin.label}, ${input.expiresAt})
          RETURNING id`)

        await adminAudit(db, c, {
          action: 'entitlement.granted',
          targetType: 'entitlement',
          targetId: input.feature,
          subjectOrgId: input.orgId,
          // Capacity is money by another name, so it is recorded at the same
          // severity as a refund rather than as a configuration change.
          severity: 'high',
          detail: {
            scope: input.scope,
            scopeId: input.scopeId,
            value: input.value,
            reason: input.reason,
            ticket: input.ticket ?? null,
            expiresAt: input.expiresAt,
            replaced: previous[0] ? { id: previous[0].id, value: previous[0].value } : null,
          },
        })
        return { id: created[0]!.id, replaced: previous[0]?.id ?? null }
      })
    }),

  revoke: adminProcedure('admin.entitlements.write')
    .input(z.object({ id: uuid, reason }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{ org_id: string | null; feature: string; value: unknown }>(sql`
          UPDATE entitlement_overrides
          SET revoked_at = ${c.clock.now().toISOString()},
              revoked_by_label = ${c.admin.label},
              revoked_reason = ${input.reason}
          WHERE id = ${input.id} AND revoked_at IS NULL
          RETURNING org_id, feature, value`)
        const row = rows[0]
        if (!row) {
          // Already revoked, or never existed. Not an error worth a stack
          // trace, and not a silent success either: an operator who thinks
          // they just removed capacity has to be told they did not.
          throw new TRPCError({
            code: 'NOT_FOUND',
            message: 'That grant is already revoked or does not exist. Nothing was changed.',
          })
        }
        await adminAudit(db, c, {
          action: 'entitlement.revoked',
          targetType: 'entitlement',
          targetId: row.feature,
          subjectOrgId: row.org_id,
          severity: 'high',
          detail: { id: input.id, value: row.value, reason: input.reason },
        })
        return { revoked: true }
      })
    }),
})

// ---------------------------------------------------------------------------
// Flags
// ---------------------------------------------------------------------------

export const adminFlagsRouter = router({
  list: adminProcedure('admin.flags.read').query(async ({ ctx }) => {
    const c = ctx as AdminContext
    return c.adminDb(async (db) => {
      const flags = await db.execute<{ key: string } & Record<string, unknown>>(sql`
        SELECT key, description, state, rollout_percent, internal_only,
               killed_at, killed_by_label, killed_reason, updated_at, updated_by_label
        FROM feature_flags ORDER BY key`)
      const targets = await db.execute<Record<string, unknown>>(sql`
        SELECT id, flag_key, kind, value, allow, org_id, reason, created_by_label, created_at
        FROM feature_flag_targets ORDER BY flag_key, created_at`)
      return {
        flags: flags.map((f) => ({
          ...f,
          key: f.key,
          // Whether anything actually reads it. A flag with no call site is a
          // switch that looks like a control, so the screen says so rather
          // than letting an operator flip it during an incident and wonder
          // why nothing changed.
          checkedAt: KNOWN_FLAGS[f.key]?.checkedAt ?? null,
          known: KNOWN_FLAGS[f.key] !== undefined,
        })),
        targets,
      }
    })
  }),

  /** Creates or updates a flag. */
  set: adminProcedure('admin.flags.write')
    .input(
      z.object({
        key: z.string().regex(/^[a-z][a-z0-9]*([._-][a-z0-9]+)*$/).max(100),
        description: z.string().min(8).max(500),
        state: z.enum(['off', 'on', 'targeted']),
        rolloutPercent: z.number().int().min(0).max(100),
        internalOnly: z.boolean(),
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const before = await db.execute<Record<string, unknown>>(sql`
          SELECT state, rollout_percent, internal_only FROM feature_flags WHERE key = ${input.key}`)
        await db.execute(sql`
          INSERT INTO feature_flags
            (key, description, state, rollout_percent, internal_only, updated_by_label)
          VALUES (${input.key}, ${input.description}, ${input.state},
                  ${input.rolloutPercent}, ${input.internalOnly}, ${c.admin.label})
          ON CONFLICT (key) DO UPDATE SET
            description = excluded.description,
            state = excluded.state,
            rollout_percent = excluded.rollout_percent,
            internal_only = excluded.internal_only,
            updated_at = now(),
            updated_by_label = excluded.updated_by_label,
            -- Turning a flag back on CLEARS the kill, so the three kill columns
            -- describe the kill that is in force rather than the last one there
            -- ever was. An incident timeline reconstructed from a stale
            -- killed_at is a timeline that names the wrong afternoon.
            killed_at = CASE WHEN excluded.state = 'off' THEN feature_flags.killed_at ELSE NULL END,
            killed_by_label = CASE WHEN excluded.state = 'off' THEN feature_flags.killed_by_label ELSE NULL END,
            killed_reason = CASE WHEN excluded.state = 'off' THEN feature_flags.killed_reason ELSE NULL END`)
        await adminAudit(db, c, {
          action: 'flag.set',
          targetType: 'flag',
          targetId: input.key,
          // A flag change is platform-wide by definition, so notice rather
          // than high: it concerns no one tenant, and marking every one of
          // them high would make `high` mean nothing on the day it matters.
          severity: 'notice',
          detail: { before: before[0] ?? null, after: input },
        })
        return { key: input.key }
      })
    }),

  /**
   * The kill switch, and it is a separate route from `set` on purpose.
   *
   * Turning a flag off during an incident and turning it off because the
   * experiment ended are the same UPDATE and completely different events. One
   * route for both would record them identically, and the one worth finding six
   * months later is the first. This one demands a reason and stamps who and
   * when; `set` does not.
   */
  kill: adminProcedure('admin.flags.write')
    .input(z.object({ key: z.string().max(100), reason }))
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{ key: string }>(sql`
          UPDATE feature_flags
          SET state = 'off', killed_at = ${c.clock.now().toISOString()},
              killed_by_label = ${c.admin.label}, killed_reason = ${input.reason},
              updated_at = now(), updated_by_label = ${c.admin.label}
          WHERE key = ${input.key}
          RETURNING key`)
        if (!rows[0]) {
          throw new TRPCError({ code: 'NOT_FOUND', message: `There is no flag called ${input.key}.` })
        }
        await adminAudit(db, c, {
          action: 'flag.killed',
          targetType: 'flag',
          targetId: input.key,
          // The one flag event that is high. Somebody reading an incident
          // timeline is looking for exactly this line.
          severity: 'high',
          detail: { reason: input.reason },
        })
        return { killed: true }
      })
    }),

  target: adminProcedure('admin.flags.write')
    .input(
      z.object({
        flagKey: z.string().max(100),
        kind: z.enum(['user', 'organization', 'project', 'repository', 'plan', 'environment']),
        value: z.string().min(1).max(300),
        allow: z.boolean(),
        orgId: uuid.nullable(),
        reason,
      }),
    )
    .mutation(async ({ ctx, input }) => {
      const c = ctx as AdminContext
      return c.adminDb(async (db) => {
        const rows = await db.execute<{ id: string }>(sql`
          INSERT INTO feature_flag_targets
            (flag_key, kind, value, allow, org_id, reason, created_by_label)
          VALUES (${input.flagKey}, ${input.kind}, ${input.value}, ${input.allow},
                  ${input.orgId}, ${input.reason}, ${c.admin.label})
          ON CONFLICT (flag_key, kind, value) DO UPDATE SET
            allow = excluded.allow, reason = excluded.reason,
            created_by_label = excluded.created_by_label
          RETURNING id`)
        await adminAudit(db, c, {
          action: input.allow ? 'flag.targeted' : 'flag.denied',
          targetType: 'flag',
          targetId: input.flagKey,
          subjectOrgId: input.orgId,
          // A DENY is the lever that pulls one customer out of a rollout that
          // is working for everybody else, which is an incident action; an
          // allow is an ordinary rollout step.
          severity: input.allow ? 'notice' : 'high',
          detail: { kind: input.kind, value: input.value, reason: input.reason },
        })
        return { id: rows[0]!.id }
      })
    }),
})

/** The operator tree this lane owns, for the mount in server.ts. */
export const adminMoneyRouter = router({
  billing: adminBillingRouter,
  entitlements: adminEntitlementsRouter,
  flags: adminFlagsRouter,
})
