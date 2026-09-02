// What an organization is allowed, once somebody has been sold something the
// price list does not have a column for.
//
// PLAN_QUOTAS and PLAN_COST_CAPS are the price list, and they are right: three
// plans, fixed numbers, enforced. What they cannot express is the customer on
// `team` who was sold forty environments, the design partner using a capability
// nobody else has, and the trial that got another week because somebody asked
// on a Friday. Every one of those is ordinary commercial reality and every one
// of them, done by moving the plan, charges the wrong amount.
//
// So this file is the join between the plan's answer and the overrides table,
// and the ORDER is the whole of it: user, then project, then organization, then
// global, then the plan. Most specific wins. A reader who assumed the other
// direction would have an organization-wide cap silently overriding the one
// grant somebody made for a single person, which is the shape of every
// entitlement bug worth having.
//
// ---------------------------------------------------------------------------
// The thing this file is most likely to become, and the guard against it
// ---------------------------------------------------------------------------
//
// An entitlement catalogue that nothing reads is worse than no catalogue,
// because a grant that changes no behaviour looks like a working feature to
// everybody: the operator sees the row, the customer is told the limit moved,
// and the next `af up` is refused by the plan's number anyway. The failure is
// invisible from every direction except the one that matters.
//
// `enforcedAt` on every entry is the guard. It names the file and the call that
// reads this entitlement, or it is null and says so out loud, and
// entitlements.test.ts walks the catalogue and fails on an entry whose
// `enforcedAt` names a call site that does not exist. An entitlement that is
// reported but not enforced is allowed here; an entitlement that CLAIMS to be
// enforced and is not is a lie the test refuses to let ship.

import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'
import { PLAN_QUOTAS, DEFAULT_PLAN } from './limits.ts'
import type { QuotaVerdict } from './limits.ts'
// hours and round come from costs.ts rather than being written again here.
// Two formatters for one number is how two code paths start disagreeing about
// the same value: the local copy this replaced rounded to one decimal place
// and never said "minutes", so a sub-hour cap read differently depending on
// which function refused it.
import { PLAN_COST_CAPS, hours, round } from './costs.ts'
import type { CapVerdict } from './costs.ts'

export type EntitlementValue = number | boolean

export interface EntitlementSpec {
  kind: 'number' | 'boolean'
  /** One line, in the words that go on the admin screen beside the value. */
  description: string
  /** The unit, for a number, so the console never renders a bare integer. */
  unit?: string
  /** What each plan gives. Read from the existing tables where one exists, so
   *  that the plan's number is defined once and cannot drift from the number
   *  that is already enforced. */
  byPlan: Record<string, EntitlementValue>
  /**
   * Where the resolved value is READ, or null when nothing reads it yet.
   *
   * `file.ts:symbol`. The test greps for the symbol in the file, so this
   * cannot be a comforting sentence about intent; it has to name something
   * that exists.
   */
  enforcedAt: string | null
  /**
   * Why it is not enforced, when it is not. Required in that case, because
   * "reported but not enforced" is a legitimate state and an undocumented one
   * is indistinguishable from a bug.
   */
  notEnforcedBecause?: string
}

const plans = Object.keys(PLAN_QUOTAS)

/** The plan's own number for a quota, as a byPlan map. Derived rather than
 *  retyped: two lists would disagree and the one that would be wrong is this
 *  one, which decides what somebody who paid gets. */
function fromQuota(field: 'environments' | 'goldens' | 'artifactGigabytes'): Record<string, number> {
  return Object.fromEntries(plans.map((p) => [p, PLAN_QUOTAS[p]![field]]))
}

function fromCaps(field: 'perRunHours' | 'perDayHours'): Record<string, number> {
  return Object.fromEntries(
    plans.map((p) => [p, (PLAN_COST_CAPS[p] ?? PLAN_COST_CAPS[DEFAULT_PLAN]!)[field]]),
  )
}

export const ENTITLEMENTS: Record<string, EntitlementSpec> = {
  environments: {
    kind: 'number',
    unit: 'environments',
    description: 'Live environments this organization may hold at once.',
    byPlan: fromQuota('environments'),
    enforcedAt: 'routers/dispatch.ts:checkQuotaWithEntitlements',
  },
  goldens: {
    kind: 'number',
    unit: 'goldens',
    description: 'Golden snapshots this organization may keep.',
    byPlan: fromQuota('goldens'),
    // Honest rather than aspirational. `checkQuota(plan, 'goldens', n)` is
    // called on the two REPORTING paths, billing.get and org.status, and on no
    // path that can refuse anything: goldens are written by the ingest of an
    // engine's events, and refusing one there would discard work a customer's
    // CI has already done and paid for. Raising this override moves the number
    // on the screen and nothing else, and saying so here is what stops the
    // next person believing otherwise.
    enforcedAt: null,
    notEnforcedBecause:
      'Goldens arrive through event ingest after the work is done. Refusing one there would ' +
      'discard a customer run rather than prevent it, so the quota is reported and not enforced.',
  },
  artifactGigabytes: {
    kind: 'number',
    unit: 'GB',
    description: 'Artifact storage this organization may keep.',
    byPlan: fromQuota('artifactGigabytes'),
    enforcedAt: null,
    notEnforcedBecause:
      'Artifact bytes are reported by the engine after an upload. There is no admission point ' +
      'in the control plane to refuse at, so the quota is reported and not enforced.',
  },
  perRunHours: {
    kind: 'number',
    unit: 'environment-hours',
    description: 'The longest one run may hold an environment.',
    byPlan: fromCaps('perRunHours'),
    enforcedAt: 'routers/dispatch.ts:checkCostCapWithEntitlements',
  },
  perDayHours: {
    kind: 'number',
    unit: 'environment-hours per day',
    description: 'Environment-hours this organization may accrue in a rolling day.',
    byPlan: fromCaps('perDayHours'),
    enforcedAt: 'routers/dispatch.ts:checkCostCapWithEntitlements',
  },
  seats: {
    kind: 'number',
    unit: 'seats',
    description: 'Members and open invitations together.',
    // Not derived, because there was no seat limit before this. The numbers
    // are the environment quota's shape: generous enough that no honest team
    // meets them by accident, present so that a seat sold is a seat counted.
    //
    // Counting OPEN INVITATIONS against the limit is the decision worth
    // defending. Counting only accepted members means an organization at its
    // limit can send a hundred invitations and go over the moment they are
    // accepted, and the refusal then lands on the person accepting rather than
    // on the person who oversold, which is the wrong end.
    byPlan: { free: 5, team: 50, enterprise: 1000 },
    enforcedAt: 'routers/enterprise.ts:seatVerdict',
  },
  apiRateMultiplier: {
    kind: 'number',
    unit: 'x',
    description: 'Multiplies every per-organization rate limit.',
    byPlan: { free: 1, team: 1, enterprise: 1 },
    enforcedAt: null,
    notEnforcedBecause:
      'The rate limiter reads ENDPOINT_LIMITS synchronously on the request path and has no ' +
      'database read to hang an override off. Wiring it needs a cache with an invalidation ' +
      'story, which is a separate piece of work rather than a line in this one.',
  },
  retentionDays: {
    kind: 'number',
    unit: 'days',
    description: 'How long events and audit entries are kept.',
    byPlan: { free: 30, team: 90, enterprise: 365 },
    enforcedAt: null,
    notEnforcedBecause:
      'Retention is applied by the partition sweeper, which runs outside a tenant and does not ' +
      'read this yet.',
  },
}

export type EntitlementKey = string

// ---------------------------------------------------------------------------
// Resolution
// ---------------------------------------------------------------------------

/** Where a value came from. `plan` means no override applied. */
export type EntitlementSource = 'plan' | 'global' | 'organization' | 'project' | 'user'

/** Most specific first. The array IS the precedence; nothing compares strings. */
const PRECEDENCE: readonly EntitlementSource[] = ['plan', 'global', 'organization', 'project', 'user']

function rank(source: EntitlementSource): number {
  return PRECEDENCE.indexOf(source)
}

/** The override that decided a value, for the screen that has to say so. */
export interface OverrideRef {
  id: string
  scope: Exclude<EntitlementSource, 'plan'>
  reason: string
  ticket: string | null
  grantedBy: string
  grantedAt: Date
  expiresAt: Date | null
}

export interface Resolved<T extends EntitlementValue = EntitlementValue> {
  key: string
  value: T
  /** What the plan alone would have said, so a screen can show both. */
  planValue: T
  source: EntitlementSource
  /** Null when the plan decided. Non-null is what the console marks as an
   *  override, which is the requirement that a one-off grant must never be
   *  mistaken for the plan's normal behaviour. */
  override: OverrideRef | null
}

export interface Subject {
  orgId: string
  plan: string
  /** The acting user, when the check concerns one. */
  userId?: string | null
  /** The repository the check concerns, when it concerns one. `project` scope
   *  keys on this. */
  repositoryId?: string | null
}

export class Entitlements {
  private readonly resolved: Map<string, Resolved>
  /**
   * The plan these were resolved against.
   *
   * Carried so a refusal can NAME it. "This organization is holding 4 of 3
   * environments, which is what the plan allows" tells somebody nothing they
   * can act on; "what the free plan allows" tells them what upgrading buys.
   * That sentence is the whole of the self-service path out of a refusal, and
   * dropping the plan from it turns a refusal into a support ticket.
   */
  readonly plan: string

  constructor(plan: string, resolved: Map<string, Resolved>) {
    this.plan = plan
    this.resolved = resolved
  }

  /** Every entitlement, resolved, for the screen and for the API. */
  all(): Resolved[] {
    return [...this.resolved.values()]
  }

  get(key: string): Resolved | undefined {
    return this.resolved.get(key)
  }

  /**
   * A numeric entitlement.
   *
   * Throws for a key the catalogue does not have, rather than returning zero.
   * A typo that produced 0 would be a limit of zero on somebody's account,
   * which refuses everything and looks exactly like a suspended customer.
   */
  number(key: string): number {
    const r = this.resolved.get(key)
    if (!r) throw new Error(`No entitlement named ${key}.`)
    if (typeof r.value !== 'number') {
      throw new Error(`Entitlement ${key} is not a number.`)
    }
    return r.value
  }

  boolean(key: string): boolean {
    const r = this.resolved.get(key)
    if (!r) throw new Error(`No entitlement named ${key}.`)
    return r.value === true
  }
}

interface OverrideRow extends Record<string, unknown> {
  id: string
  scope: string
  feature: string
  value: unknown
  reason: string
  ticket: string | null
  created_by_label: string
  created_at: string | Date
  expires_at: string | Date | null
}

/**
 * Every entitlement for one subject, in one query.
 *
 * Runs inside whatever transaction it is given, which on the request path is a
 * tenant transaction: the read policy on entitlement_overrides already limits
 * the answer to global rows and this organization's, so a bug in the WHERE
 * clause below cannot reach another tenant's grants. The clause is still
 * written correctly, because defence in depth means both, not either.
 *
 * The expiry is compared HERE rather than left to a sweeper. An expiry that
 * only takes effect when a cron job runs is an expiry that does not take effect
 * during the outage that stopped the cron job, which is when an over-generous
 * grant costs the most.
 */
export async function resolveEntitlements(
  db: Db,
  now: Date,
  subject: Subject,
): Promise<Entitlements> {
  const rows = await db.execute<OverrideRow>(sql`
    SELECT id, scope, feature, value, reason, ticket, created_by_label, created_at, expires_at
    FROM entitlement_overrides
    WHERE revoked_at IS NULL
      AND (expires_at IS NULL OR expires_at > ${now.toISOString()})
      AND (
        scope = 'global'
        OR (scope = 'organization' AND scope_id = ${subject.orgId})
        OR (scope = 'project' AND scope_id = ${subject.repositoryId ?? null})
        OR (scope = 'user' AND scope_id = ${subject.userId ?? null}
            AND org_id = ${subject.orgId})
      )`)

  return applyOverrides(subject.plan, rows)
}

/**
 * The pure half, so the precedence and the coercion can be tested without a
 * database and so the resolver has one place to be wrong rather than two.
 */
export function applyOverrides(plan: string, rows: readonly OverrideRow[]): Entitlements {
  const out = new Map<string, Resolved>()

  for (const [key, spec] of Object.entries(ENTITLEMENTS)) {
    const planValue = spec.byPlan[plan] ?? spec.byPlan[DEFAULT_PLAN]
    if (planValue === undefined) {
      // A plan the catalogue has never heard of. The same direction
      // planForStatus takes: fall back rather than guess, because a missing
      // entry must not become a limit of zero on a paying customer.
      continue
    }
    out.set(key, { key, value: planValue, planValue, source: 'plan', override: null })
  }

  for (const row of rows) {
    const spec = ENTITLEMENTS[row.feature]
    // An override for an entitlement this build does not have. Skipped rather
    // than surfaced as an error: the row may belong to a newer deploy that has
    // rolled back, and refusing to resolve ANY entitlement because one row is
    // unfamiliar would take the whole organization's limits out over a value
    // nobody read.
    if (!spec) continue

    const current = out.get(row.feature)
    if (!current) continue

    const scope = row.scope as Exclude<EntitlementSource, 'plan'>
    if (!PRECEDENCE.includes(scope)) continue
    // Later rows do not win by being later. A user grant beats an organization
    // grant however they came back from the database, because the ORDER BY of
    // a query is not a place to put a security-relevant rule.
    if (rank(scope) <= rank(current.source)) continue

    const value = coerce(spec, row.value)
    // A value of the wrong shape falls through to whatever was already there,
    // which is the plan or a less specific override.
    //
    // The direction matters. Coercing an unreadable value to 0 would set the
    // limit to zero and refuse everything, and a customer whose account stops
    // working because an operator typed a string into a number field would
    // have no way to know that is what happened. Ignoring it leaves them on
    // the plan they pay for, which is wrong in the harmless direction.
    if (value === null) continue

    out.set(row.feature, {
      key: row.feature,
      value,
      planValue: current.planValue,
      source: scope,
      override: {
        id: row.id,
        scope,
        reason: row.reason,
        ticket: row.ticket,
        grantedBy: row.created_by_label,
        grantedAt: asDate(row.created_at),
        expiresAt: row.expires_at === null ? null : asDate(row.expires_at),
      },
    })
  }

  return new Entitlements(plan, out)
}

/**
 * One override value, or null when it is not the shape the entitlement is.
 *
 * jsonb comes back from the driver already parsed, so a numeric override is a
 * number and a boolean is a boolean. The string branch is not defensive
 * padding: `'40'::jsonb` and `'"40"'::jsonb` are both valid jsonb and only the
 * first is a number, and an operator pasting a quoted value into an admin form
 * is not a hypothetical. Accepting the second, once, as a number is kinder
 * than refusing a grant somebody meant; accepting anything else is guessing.
 */
function coerce(spec: EntitlementSpec, raw: unknown): EntitlementValue | null {
  if (spec.kind === 'number') {
    if (typeof raw === 'number' && Number.isFinite(raw)) return Math.trunc(raw)
    if (typeof raw === 'string' && raw.trim() !== '') {
      const n = Number(raw)
      if (Number.isFinite(n)) return Math.trunc(n)
    }
    return null
  }
  if (typeof raw === 'boolean') return raw
  if (raw === 'true') return true
  if (raw === 'false') return false
  return null
}

function asDate(v: string | Date): Date {
  return v instanceof Date ? v : new Date(v)
}

// ---------------------------------------------------------------------------
// The verdicts
//
// These are the functions the call sites named in `enforcedAt` actually call.
// They live here rather than beside each call site so that the message a
// customer reads when they are refused says the same thing about an override
// wherever the refusal came from, and so that adding a fifth caller cannot
// invent a fifth way of explaining a limit.
//
// Each mirrors the plan-only function it replaces and returns the same verdict
// shape, so a caller swapping to one of these changes what the limit IS and
// changes nothing about how a refusal is reported.
// ---------------------------------------------------------------------------


/** How a refusal names the limit it hit: the plan, or the grant that moved it.
 *
 *  Said out loud in the refusal because the alternative is a customer who was
 *  told they had forty environments being refused at forty with a message that
 *  says the team plan allows twenty five. That reads as the grant having been
 *  lost, and generates a support ticket that a sentence would have prevented. */
function because(r: Resolved, plan: string): string {
  if (r.override === null) return `the ${plan} plan`
  const until = r.override.expiresAt ? `, until ${r.override.expiresAt.toISOString().slice(0, 10)}` : ''
  // "a organization override" is the kind of defect that makes a customer trust
  // the rest of the sentence less, and the scope is the only word here that can
  // start with a vowel. Chosen from the four scopes rather than by a rule about
  // vowels, because a rule would be wrong the first time a scope is named
  // something like "unit".
  const article = r.override.scope === 'organization' ? 'an' : 'a'
  return `${article} ${r.override.scope} override${until}`
}

/**
 * The environment quota, with overrides applied.
 *
 * Deliberately the same shape as `checkQuota` and deliberately NOT a wrapper
 * that takes the larger of the two: an override may lower a limit as well as
 * raise it, because capping a tenant that is running away with the cluster is
 * the same mechanism as selling one extra capacity, and a function that could
 * only ever grant would leave the containment case to a deploy.
 */
export function checkQuotaWithEntitlements(
  entitlements: Entitlements,
  key: 'environments' | 'goldens' | 'artifactGigabytes',
  current: number,
): QuotaVerdict {
  const resolved = entitlements.get(key)
  if (!resolved || typeof resolved.value !== 'number') {
    // Nothing resolved. Refusing here would be a limit of zero on a customer
    // whose catalogue entry was removed by a deploy, so this allows and the
    // absence is visible on the admin screen instead.
    return { allowed: true, current, limit: Number.POSITIVE_INFINITY, reason: '' }
  }
  const limit = resolved.value
  if (current < limit) return { allowed: true, current, limit, reason: '' }
  const spec = ENTITLEMENTS[key]
  const noun = spec?.unit ?? key
  // Two shapes, and the first is checkQuota's sentence unchanged.
  //
  // "holding 4 of 3 environments on the free plan" is what this product has
  // always said, and it is the version a support macro or a documentation page
  // would quote. Only the case that did not exist before, a limit that came
  // from a grant rather than from the price list, gets new wording, because
  // "on an organization override" does not read as English.
  return {
    allowed: false,
    current,
    limit,
    reason:
      resolved.override === null
        ? `This organization is holding ${current} of ${limit} ${noun} on the ` +
          `${entitlements.plan} plan. Tear one down, or change the plan. Nothing that already ` +
          `exists was removed.`
        : `This organization is holding ${current} of ${limit} ${noun}, and ${limit} is what ` +
          `${because(resolved, entitlements.plan)} allows. Tear one down, or change the plan. ` +
          `Nothing that already exists was removed.`,
  }
}

/**
 * The cost caps, with overrides applied.
 *
 * The per-run cap is checked and reported first when both are broken, for the
 * reason `checkCostCap` gives: it is the one the caller can fix in the same
 * breath by lowering runtime.ttl, while the daily cap only clears with time.
 */
export function checkCostCapWithEntitlements(
  entitlements: Entitlements,
  runHours: number,
  usedDayHours: number,
): CapVerdict {
  const perRun = entitlements.get('perRunHours')
  const perDay = entitlements.get('perDayHours')

  if (perRun && typeof perRun.value === 'number' && runHours > perRun.value) {
    return {
      allowed: false,
      kind: 'per-run',
      current: round(runHours),
      limit: perRun.value,
      reason:
        `This run would hold an environment for ${hours(runHours)}, and ${because(perRun, entitlements.plan)} allows ` +
        `${hours(perRun.value)} in one run. Lower runtime.ttl in the manifest, or ask an owner of ` +
        `this organization to change the plan. Nothing was created and nothing was removed.`,
    }
  }

  // The PROJECTED total rather than the current one, the same as checkCostCap:
  // admitting a run that is itself larger than the remaining allowance is how a
  // cap is passed by exactly one run, every time, and the customer sees a limit
  // that does not hold.
  if (perDay && typeof perDay.value === 'number' && usedDayHours + runHours > perDay.value) {
    return {
      allowed: false,
      kind: 'per-day',
      // What has been USED, not the projection. The projection is in the
      // sentence; this field is the fact, and a caller totalling it would be
      // counting a run that never happened.
      current: round(usedDayHours),
      limit: perDay.value,
      // Word for word what checkCostCap says, with only the clause naming the
      // limit's source replaced.
      //
      // Not a stylistic choice. This exact paragraph is quoted in
      // docs/src/content/docs/reference/environment-lifetime.md, and rewriting
      // it left the documentation describing a message the product no longer
      // produces. It also lost two things the doc calls out as deliberate:
      // "in the last 24 hours" rather than "in the last day", which is the
      // window the number is actually computed over, and "this run would need
      // another N hours", which is the part that tells somebody how much to
      // shorten it by.
      reason:
        `This organization has used ${hours(usedDayHours)} of environment time in the last ` +
        `24 hours, and ${because(perDay, entitlements.plan)} allows ${hours(perDay.value)}. ` +
        `This run would need another ${hours(runHours)}. Tear down an environment you are ` +
        `finished with, wait for the window to move, or ask an owner of this organization to ` +
        `change the plan. Nothing was created and nothing was removed.`,
    }
  }

  return { allowed: true, kind: null, current: round(usedDayHours), limit: perDay && typeof perDay.value === 'number' ? perDay.value : 0, reason: '' }
}

/**
 * Whether one more person may be added, counting members and open invitations
 * together.
 *
 * Both, and the reason is in the catalogue entry above: counting only accepted
 * members lets an organization at its limit send a hundred invitations, and the
 * refusal then lands on the person accepting rather than on the person who
 * oversold.
 */
export function seatVerdict(
  entitlements: Entitlements,
  members: number,
  openInvitations: number,
): QuotaVerdict {
  const resolved = entitlements.get('seats')
  const held = members + openInvitations
  if (!resolved || typeof resolved.value !== 'number') {
    return { allowed: true, current: held, limit: Number.POSITIVE_INFINITY, reason: '' }
  }
  const limit = resolved.value
  if (held < limit) return { allowed: true, current: held, limit, reason: '' }
  const invitations =
    openInvitations === 1
      ? ' (one of them an invitation that has not been accepted)'
      : openInvitations > 1
        ? ` (${openInvitations} of them invitations that have not been accepted)`
        : ''
  return {
    allowed: false,
    current: held,
    limit,
    reason:
      `This organization is using ${held} of ${limit} seats${invitations}, and ${limit} is ` +
      `what ${because(resolved, entitlements.plan)} allows. Withdraw an invitation, remove a ` +
      `member, or change the plan. Nobody was removed.`,
  }
}

