// The one question enterprise control plane code asks before doing anything:
// is this organization entitled to it, right now.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// THIS IS THE SECOND HALF OF A MECHANISM THAT ALREADY EXISTED, and the half
// that was missing. ee/engine/feature is the same idea for the engine: a
// context carries a licence, Enabled answers, and Declare records where each
// feature is enforced so that a feature nothing checks is visible as a list
// rather than as an assumption. It works, it is enforced at three sites, and it
// can never cover single sign-on, SCIM or custom roles, because none of them
// are in the engine. They are control plane features written in TypeScript and
// the licence key is never in the same process as them.
//
// Measured on 2026-09-08: twelve licensed features, three enforced. Of the nine
// that were not, three are built, tested against a real database over real
// HTTP, and gated by nothing at all, and all three are here.
//
// WHY THE AUTHORITY IS THE ENTITLEMENT CATALOGUE AND NOT A LICENCE KEY.
//
// There are two entitlement authorities in this product because there are two
// installation shapes. A self hosted engine reads a signed licence from its
// environment; nothing else could work, since there is nobody to ask. A hosted
// organization is whatever its plan and its overrides say, which is the control
// plane's own catalogue, and asking a licence key about it would mean shipping
// a licence key to every hosted customer so the server could read it back.
//
// So the rule is: a feature is decided by the authority that runs where it is
// enforced. The names are identical on both sides on purpose, which is what
// makes the two answers comparable rather than merely coexistent, and
// test/catalogue.test.ts fails when they stop matching.
//
// WHY THE DEFAULT IS NO. An organization whose plan the catalogue cannot answer
// for, a key nothing resolves, a database read that comes back empty: all of
// them answer false, because the direction this must fail in is towards the
// community behaviour. A gate that grants when it cannot tell is not a gate.

import { DEFAULT_PLAN, resolveEntitlements } from '@antifailure/api'
import { sql, type Db, type Pool } from '@antifailure/db'

/**
 * Every feature an enterprise licence can name.
 *
 * A copy of the Feature constants in ee/engine/license/license.go, and the copy
 * is deliberate: that is a Go package and this is TypeScript, and there is no
 * import that could join them. A copy with no gate is a copy that drifts, so
 * test/catalogue.test.ts parses the constants out of that file and fails when
 * the two sets differ. tools/licensegen holds a third copy under the same rule
 * and for the same reason.
 */
export const FEATURES = [
  'air_gapped',
  'audit_stream',
  'billing',
  'cloud_database',
  'cloud_runtime',
  'compliance_packs',
  'enterprise_dashboard',
  'enterprise_secrets',
  'multi_runtime',
  'policy_enforcement',
  'rbac',
  'scim',
  'sso',
  'support_access',
] as const

export type Feature = (typeof FEATURES)[number]

export function isFeature(value: string): value is Feature {
  return (FEATURES as readonly string[]).includes(value)
}

// ---------------------------------------------------------------------------
// Where each feature is enforced
// ---------------------------------------------------------------------------

/**
 * The registry, which exists for a test rather than for the request path.
 *
 * A feature a licence can name and nothing checks is a feature that is silently
 * free, and a check for a feature no licence can grant is dead code. Both are
 * invisible without a list, and both were true in this repository on the day
 * this file was written.
 *
 * THE FAILURE THIS SHAPE AVOIDS, because the engine's registry hit it. A site
 * string may name a symbol that cannot possibly enforce anything: the
 * compliance pack declared its site as Pack.Evaluate, which takes no context
 * and therefore cannot ask the licence a question, while the real refusal is
 * seventy lines away in command.go. The declaration was true about the feature
 * and false about the symbol, which is the same lie one level down. So the test
 * beside this file does not merely check that a site was declared: it reads the
 * named file and requires the named symbol to be in it, the way the control
 * plane's own entitlement catalogue has done since it was written.
 */
const sitesByFeature = new Map<Feature, string[]>()

/**
 * Records that a feature is enforced at a named site.
 *
 * `path/from/the/repository/root.ts:symbol`. Called from module scope in the
 * package that does the enforcing, so importing that package is what puts the
 * entry in the map, and a package nothing imports declares nothing. That is
 * deliberate: a registry populated by a list in one file would report a site
 * for code that is no longer reachable.
 */
export function declare(feature: Feature, site: string): void {
  const existing = sitesByFeature.get(feature)
  if (existing) {
    if (!existing.includes(site)) existing.push(site)
    return
  }
  sitesByFeature.set(feature, [site])
}

/** Everywhere a feature is enforced, in this process. */
export function sites(feature: Feature): readonly string[] {
  return [...(sitesByFeature.get(feature) ?? [])]
}

/** Every feature with at least one enforcement site, in this process. */
export function declared(): Feature[] {
  return [...sitesByFeature.keys()].sort()
}

// ---------------------------------------------------------------------------
// The question itself
// ---------------------------------------------------------------------------

/**
 * Raised when a feature is used by an organization that is not entitled to it.
 *
 * A distinct type because the answer is not an error in the ordinary sense and
 * must not be rendered as one. Every route that can raise it turns it into a
 * refusal a human or a provisioning robot can act on, and never into a 500: a
 * 500 makes a directory retry the same request forever, and it tells a person
 * that something is broken when what has actually happened is that their
 * organization is not on a plan that includes this.
 */
export class Unlicensed extends Error {
  readonly feature: Feature
  constructor(feature: Feature, message: string) {
    super(message)
    this.name = 'Unlicensed'
    this.feature = feature
  }
}

/** The sentence a refusal carries, in one place so six routes cannot invent six
 *  ways of saying it. Names the feature and what to do, because "not
 *  entitled" with no noun in it is a support ticket. */
export function refusal(feature: Feature): string {
  return (
    `This organization is not entitled to ${DESCRIPTIONS[feature]}. Ask an owner to change ` +
    `the plan, or an operator to grant it. Nothing was changed and nothing was removed.`
  )
}

const DESCRIPTIONS: Record<Feature, string> = {
  air_gapped: 'air gapped operation',
  audit_stream: 'audit streaming',
  billing: 'billing',
  cloud_database: 'managed cloud databases',
  cloud_runtime: 'managed cloud runtimes',
  compliance_packs: 'compliance packs',
  enterprise_dashboard: 'the enterprise dashboard',
  enterprise_secrets: 'enterprise secret managers',
  multi_runtime: 'customer owned runtimes',
  policy_enforcement: 'organization wide policy enforcement',
  rbac: 'custom roles',
  scim: 'directory provisioning',
  sso: 'single sign-on',
  support_access: 'operator support access',
}

/**
 * Whether one organization may use one feature.
 *
 * Reads the plan and the overrides through the control plane's own resolver, so
 * a grant made on the admin screen changes this answer with no deploy, and so
 * the sentence a refusal carries about WHY a limit is what it is comes from the
 * same place for a capability as it does for a quota.
 *
 * Inside a tenant transaction, which matters. The read policy on
 * entitlement_overrides limits the answer to global rows and this
 * organization's, so this cannot read a grant made to somebody else even if the
 * resolver's WHERE clause were wrong. Defence in depth means both.
 */
export async function licensed(
  pool: Pool,
  orgId: string,
  feature: Feature,
  now: Date,
): Promise<boolean> {
  return pool.withTenant({ orgId }, (db) => licensedIn(db, orgId, feature, now))
}

/**
 * The same question inside a transaction the caller already holds.
 *
 * Separate because opening a second connection in the middle of a provisioning
 * write is how a request ends up deadlocked against itself, and because a
 * caller that is already inside the tenant should not have to leave it to ask
 * whether it is allowed to be there.
 */
export async function licensedIn(
  db: Db,
  orgId: string,
  feature: Feature,
  now: Date,
): Promise<boolean> {
  const rows = await db.execute<{ plan: string }>(
    sql`SELECT plan FROM organizations WHERE id = ${orgId}`,
  )
  // No row means the organization is gone or invisible from here. False, which
  // is the community behaviour, rather than an exception: a feature check is
  // not the right place to discover that a tenant was deleted, and answering
  // true would be granting on the strength of a read that failed.
  const plan = rows[0]?.plan ?? DEFAULT_PLAN
  const entitlements = await resolveEntitlements(db, now, { orgId, plan })
  const resolved = entitlements.get(feature)
  return resolved?.value === true
}

/** The same question, raising rather than returning, for a caller whose next
 *  line would be to throw anyway. */
export async function requireFeature(
  pool: Pool,
  orgId: string,
  feature: Feature,
  now: Date,
): Promise<void> {
  if (!(await licensed(pool, orgId, feature, now))) {
    throw new Unlicensed(feature, refusal(feature))
  }
}
