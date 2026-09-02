// Turning something off without a deploy.
//
// The state that matters here is not `on`. It is `off`, reached during an
// incident, by somebody who is not going to wait for a build. Everything else
// in this file is arranged so that state is reachable in one write and cannot
// be overridden by anything: the kill switch is checked before targeting,
// before the rollout, before the list of people the flag was turned on for, and
// no target row can put it back.
//
// Three states rather than a boolean and a list of targets, because expressing
// "off for everybody, right now" as "delete all the targets" loses the
// configuration somebody will want back in twenty minutes, which is exactly
// when they are least able to reconstruct it.
//
// The order the evaluation runs in, which is the whole of the semantics:
//
//   1. killed        -> off, and nothing below is consulted
//   2. state 'off'   -> off
//   3. a DENY target -> off, even when the flag is `on`
//   4. state 'on'    -> on
//   5. an ALLOW target -> on
//   6. internal_only -> off for anybody outside the operator's organizations
//   7. rollout       -> on for a stable share, hashed per flag and per subject
//   8. otherwise     -> off
//
// Deny beats allow at step 3, and that is the lever that keeps a working
// rollout working. Taking ONE customer out of a feature that is fine for
// everybody else is the common incident, and doing it by turning the whole flag
// off punishes every other tenant for one tenant's problem.
//
// The same guard the entitlement catalogue carries applies here: a flag nothing
// reads is a switch that looks like a control and is not one, so every entry in
// KNOWN_FLAGS names the file that checks it and a test greps for it.

import { createHash } from 'node:crypto'
import { sql } from 'drizzle-orm'
import type { Db } from '@antifailure/db'

/** Everything a flag can be asked about. Every field is optional because a
 *  flag can be evaluated on a path that has no repository, or no user, and a
 *  target of a kind the subject cannot supply simply never matches rather than
 *  throwing on the request path. */
export interface FlagSubject {
  orgId?: string | null
  userId?: string | null
  /** The repository row, for a `project` target. */
  repositoryId?: string | null
  /** owner/name, for a `repository` target, which matches before a repository
   *  has ever been registered and can name a whole owner with `owner/*`. */
  repository?: string | null
  plan?: string | null
  /** `production`, `staging`. For turning something on in one deployment. */
  environment?: string | null
  /** Whether the subject belongs to the operator's own organization. Supplied
   *  by the caller rather than inferred, because "internal" is a fact about
   *  the deployment and this file must not invent a definition of it. */
  internal?: boolean
}

export interface FlagVerdict {
  key: string
  on: boolean
  /** Which rule decided, in the words the admin screen shows beside the flag.
   *  Never omitted: a flag that is on for a reason nobody can see is a flag
   *  nobody can debug during the incident it was turned on for. */
  because:
    | 'killed'
    | 'off'
    | 'denied'
    | 'on'
    | 'allowed'
    | 'internal-only'
    | 'rollout'
    | 'not-in-rollout'
    | 'unknown-flag'
}

export interface FlagSpec {
  description: string
  /** `file.ts:symbol`, or null with a reason, exactly as ENTITLEMENTS does. */
  checkedAt: string | null
  notCheckedBecause?: string
}

/**
 * The flags this build knows about.
 *
 * A flag that is not here can still exist in the database and still be
 * evaluated; this list is what the admin screen offers and what the test
 * checks. Keeping it means a flag with a call site can be told from one that
 * was added for a feature that never shipped.
 */
export const KNOWN_FLAGS: Record<string, FlagSpec> = {
  'billing.checkout': {
    description:
      'Whether a customer may start a new subscription. Turning this off during a payments ' +
      'incident stops new charges without touching anybody who is already subscribed.',
    checkedAt: 'routers/subscriptions.ts:refuseWhenKilled',
  },
  'billing.admin_writes': {
    description:
      'Whether the administrative surface may move money: refunds, credits, plan changes. The ' +
      'switch somebody reaches for when a script is refunding things it should not be.',
    checkedAt: 'admin/money.ts:refuseWhenKilled',
  },
}

// ---------------------------------------------------------------------------

interface FlagRow extends Record<string, unknown> {
  key: string
  state: string
  rollout_percent: number
  internal_only: boolean
  killed_at: string | Date | null
}

interface TargetRow extends Record<string, unknown> {
  flag_key: string
  kind: string
  value: string
  allow: boolean
}

/**
 * Evaluates one flag.
 *
 * A flag the database has never heard of is OFF, and that direction is the
 * whole reason this returns a verdict rather than a boolean. A missing flag
 * means either a call site that shipped before its row or a row somebody
 * deleted; defaulting to on would turn an unreleased feature on for everybody
 * the moment somebody mistyped a key, and that is not a failure anyone would
 * notice until a customer did.
 */
export async function evaluateFlag(
  db: Db,
  key: string,
  subject: FlagSubject,
): Promise<FlagVerdict> {
  const flags = await db.execute<FlagRow>(sql`
    SELECT key, state, rollout_percent, internal_only, killed_at
    FROM feature_flags WHERE key = ${key}`)
  const flag = flags[0]
  if (!flag) return { key, on: false, because: 'unknown-flag' }

  const targets = await db.execute<TargetRow>(sql`
    SELECT flag_key, kind, value, allow FROM feature_flag_targets WHERE flag_key = ${key}`)

  return decide(flag, targets, subject)
}

/**
 * The pure half, so the ORDER above can be tested exhaustively without a
 * database. Every branch below is a numbered step in the header comment; if the
 * two ever disagree, the header is the specification and this is the bug.
 */
export function decide(
  flag: FlagRow,
  targets: readonly TargetRow[],
  subject: FlagSubject,
): FlagVerdict {
  const key = flag.key

  // 1 and 2. The kill switch, before anything else can have an opinion.
  if (flag.killed_at !== null && flag.killed_at !== undefined) {
    return { key, on: false, because: 'killed' }
  }
  if (flag.state === 'off') return { key, on: false, because: 'off' }

  const matching = targets.filter((t) => matches(t, subject))

  // 3. Deny beats everything except the kill switch, including `state = 'on'`.
  if (matching.some((t) => !t.allow)) return { key, on: false, because: 'denied' }

  // 4.
  if (flag.state === 'on') return { key, on: true, because: 'on' }

  // 5.
  if (matching.some((t) => t.allow)) return { key, on: true, because: 'allowed' }

  // 6. Checked AFTER the explicit allow list, deliberately. A flag that is
  // internal only and has been explicitly turned on for one design partner
  // has to be on for that partner, or the allow target is a row that does
  // nothing and the operator who added it has no way to tell.
  if (flag.internal_only) {
    return subject.internal === true
      ? { key, on: true, because: 'allowed' }
      : { key, on: false, because: 'internal-only' }
  }

  // 7. The rollout.
  const percent = flag.rollout_percent ?? 0
  if (percent <= 0) return { key, on: false, because: 'not-in-rollout' }
  if (percent >= 100) return { key, on: true, because: 'rollout' }
  const bucket = bucketOf(key, subject)
  // Null when the subject carries nothing stable to hash. Refusing rather
  // than rolling a die: an unstable answer would flip the feature on and off
  // between two requests from the same caller, which is worse than off.
  if (bucket === null) return { key, on: false, because: 'not-in-rollout' }
  return bucket < percent
    ? { key, on: true, because: 'rollout' }
    : { key, on: false, because: 'not-in-rollout' }
}

function matches(target: TargetRow, subject: FlagSubject): boolean {
  switch (target.kind) {
    case 'user':
      return !!subject.userId && subject.userId === target.value
    case 'organization':
      return !!subject.orgId && subject.orgId === target.value
    case 'project':
      return !!subject.repositoryId && subject.repositoryId === target.value
    case 'repository':
      return matchesRepository(target.value, subject.repository ?? null)
    case 'plan':
      return !!subject.plan && subject.plan === target.value
    case 'environment':
      return !!subject.environment && subject.environment === target.value
    default:
      // A kind a newer deploy introduced. Never matching is the direction that
      // cannot turn something on for somebody it was not meant for.
      return false
  }
}

/**
 * `acme/app`, or `acme/*` for every repository an owner has.
 *
 * Only that one wildcard, and only as the whole final segment. A general glob
 * here would be a pattern language in a targeting rule, and the first person to
 * write `*` in it would turn a feature on for every customer.
 */
function matchesRepository(pattern: string, repository: string | null): boolean {
  if (!repository) return false
  if (pattern === repository) return true
  if (!pattern.endsWith('/*')) return false
  const owner = pattern.slice(0, -2)
  // The slash is required, so `acme/*` cannot match `acmecorp/app`.
  return owner !== '' && repository.startsWith(`${owner}/`)
}

/**
 * Which hundredth of the population a subject falls in, for one flag.
 *
 * Hashed with the flag key in the input, so two flags at ten percent do not
 * select the same tenth of the customers. Without that, a customer unlucky
 * enough to land in the first bucket would be in the first wave of every
 * rollout forever, and would experience the product as permanently broken
 * while everybody else saw a stable one.
 *
 * The organization is preferred over the user because a feature that is on for
 * half of one team is a feature that team reports as intermittent.
 */
export function bucketOf(key: string, subject: FlagSubject): number | null {
  const stable = subject.orgId ?? subject.userId ?? null
  if (!stable) return null
  const digest = createHash('sha256').update(`${key}:${stable}`).digest()
  // Four bytes is plenty of range for a hundred buckets and avoids the modulo
  // bias that one byte would give (256 is not a multiple of 100, so buckets 0
  // to 55 would be measurably more likely than the rest).
  return digest.readUInt32BE(0) % 100
}

// ---------------------------------------------------------------------------
// Kill switches
//
// A kill switch is NOT a flag read with the sense reversed, and confusing the
// two is how a feature flag system takes a product down.
//
// `evaluateFlag` answers "is this on", and an unknown flag is off, which is the
// safe direction for a rollout: a mistyped key leaves an unreleased feature
// unreleased. Gate an EXISTING capability on that same answer and the safe
// direction inverts: a control plane that has never had a `billing.checkout`
// row would refuse every checkout, on every installation, for a switch nobody
// has ever touched. Self-hosted installations have no rows at all.
//
// So a kill switch asks the opposite question and defaults the opposite way.
// Absent means NOT killed. Only a flag that exists and evaluates off actually
// stops anything, and only a person who created the row can have done that.
// ---------------------------------------------------------------------------

/** Why something was stopped, or null when it was not. */
export interface Killed {
  key: string
  because: FlagVerdict['because']
}

/**
 * Whether a capability has been switched off.
 *
 * Null means carry on, which is what a control plane with no flag rows always
 * answers.
 */
export async function killSwitch(
  db: Db,
  key: string,
  subject: FlagSubject,
): Promise<Killed | null> {
  const verdict = await evaluateFlag(db, key, subject)
  // The one case that must not stop anything: nobody has ever created this
  // flag, so nobody has ever turned it off.
  if (verdict.because === 'unknown-flag') return null
  return verdict.on ? null : { key, because: verdict.because }
}

/** The sentence a person reads when a kill switch stopped them.
 *
 *  It says the switch is deliberate and temporary, because the alternative
 *  reading of a sudden refusal is that the product is broken, and a customer
 *  who believes that opens a ticket instead of waiting twenty minutes. */
export function killedMessage(killed: Killed, what: string): string {
  return (
    `${what} is switched off right now. This is deliberate and temporary rather than a fault, ` +
    `and it applies to more than this request. Try again shortly, or ask support about ` +
    `${killed.key}.`
  )
}
