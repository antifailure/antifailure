// Turning a verified assertion into a member, and the two ways that goes wrong.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// The seat limit is the first. AF-EE-004 says "the license covers N seats and
// they are all in use", and the sentence after it in the catalog is the one
// that matters: "No existing member was removed." A product that makes room by
// evicting somebody has turned a billing question into an outage for a person
// who did nothing, and the person it evicts is whoever the query happened to
// sort last. So the refusal is on the ADDITION, always, and it is a refusal a
// person can read and act on.
//
// Identity linking is the second, and it is the one worth being careful about,
// because getting it wrong hands somebody another person's account. An
// assertion asserts an email address. That assertion is authoritative for the
// domains the organization has PROVEN it controls, through the DNS TXT record
// in sso_domains, and it is worth nothing at all for any other domain. Without
// that restriction, an organization that configures single sign-on with a
// provider it controls can assert ada@gmail.com, or ada@a-competitor.test, and
// be linked to whoever holds that account here.
//
// So linking runs inside the organization and only for a verified domain:
//
//   A member of this organization already has this address. That is the same
//   person arriving by a new route, which is the case the specification calls
//   out (somebody who signed up with GitHub and now arrives through SSO). The
//   membership is kept, the role is kept, and an audit entry records the link.
//
//   Nobody in this organization has it. A new account is created, with a role
//   from the group claims or the connection's default, and the seat check runs
//   first.
//
// What deliberately does NOT happen is a search for a matching account outside
// this organization. Two people at two companies may hold the same address in
// this database, an assertion from one company is not evidence about the other,
// and merging them on the strength of it would be the account takeover this
// paragraph exists to prevent.

import { randomUUID } from 'node:crypto'
import type { Pool } from '@antifailure/db'
import { appendAudit, sql } from '@antifailure/db'
import type { Connection, Role } from './store.ts'

export class ProvisioningRefused extends Error {
  readonly code: string

  constructor(code: string, message: string) {
    super(message)
    this.name = 'ProvisioningRefused'
    this.code = code
  }
}

export interface Identity {
  email: string
  displayName?: string | null
  givenName?: string | null
  familyName?: string | null
  groups?: readonly string[]
}

export interface ProvisionOptions {
  pool: Pool
  connection: Connection
  identity: Identity
  now: Date
  /**
   * How many members the license covers, or null for no limit.
   *
   * Supplied by the host rather than read here, because the license is parsed
   * by the enterprise engine module and this is the control plane. A host that
   * passes nothing gets no limit, which is the right default for a self-hosted
   * installation that has not been sold seats.
   */
  seats?: number | null
  /** Where the assertion came from, for the audit entry. */
  origin?: string
}

export interface ProvisionResult {
  userId: string
  role: Role
  /** What happened, for the audit entry and for the tests. */
  outcome: 'linked' | 'created' | 'existing'
  /** True when the role changed because the group claims say so. */
  roleChanged: boolean
}

/**
 * Maps group claims to a role.
 *
 * Applied on every sign-in rather than only at creation, so that removing
 * somebody from a group in the directory takes effect at their next login
 * rather than never. That is the whole reason a customer asks for group
 * mapping: they want the directory to be the source of truth.
 *
 * Where several groups map, the MOST privileged wins. The alternative, taking
 * the first match, makes the result depend on the order the provider happened
 * to send the claims, which is a role that changes for no visible reason.
 */
export function roleFromGroups(
  groups: readonly string[],
  map: Record<string, Role>,
  fallback: Role,
): Role {
  const order: Role[] = ['viewer', 'member', 'admin', 'owner']
  let best = -1
  // Case-insensitively, because a directory administrator typing "Engineering"
  // into our mapping and the provider sending "engineering" is not a
  // configuration error anybody should have to debug.
  const lowered = new Map(Object.entries(map).map(([k, v]) => [k.toLowerCase(), v]))
  for (const group of groups) {
    const role = lowered.get(group.toLowerCase())
    if (!role) continue
    const rank = order.indexOf(role)
    if (rank > best) best = rank
  }
  return best === -1 ? fallback : order[best]!
}

/** Creates or links the member an assertion names. */
export async function provision(options: ProvisionOptions): Promise<ProvisionResult> {
  const { pool, connection, identity, now } = options
  const email = identity.email.trim().toLowerCase()
  if (!email.includes('@')) {
    throw new ProvisioningRefused('AF-EE-SSO-001', 'The assertion carries no usable email address.')
  }

  const domain = email.slice(email.lastIndexOf('@') + 1)
  const claimed = await verifiedDomain(pool, connection.orgId, domain)
  if (!claimed) {
    throw new ProvisioningRefused(
      'AF-EE-SSO-002',
      `This organization has not verified the domain ${domain}, and an identity provider's word ` +
        `about a domain it has not proven it controls is not evidence. Add ${domain} in the ` +
        `single sign-on settings and complete the DNS check.`,
    )
  }

  const desiredRole = roleFromGroups(
    identity.groups ?? [],
    connection.groupRoleMap,
    connection.defaultRole,
  )

  return pool.withTenant({ orgId: connection.orgId }, async (db) => {
    // THE DIRECTORY WINS. If SCIM has this person marked inactive, they do not
    // get in, whatever the provider's assertion says.
    //
    // Without this an offboarding is only as good as the provider's willingness
    // to keep telling us about it, and Microsoft Entra explicitly stops:
    // once it has soft-deleted somebody it answers a further deprovision with
    // `RedundantSoftDelete` and skips. So a person deprovisioned in the
    // directory who could still authenticate would sign back in, be granted a
    // membership again, and the directory would never send another
    // deactivation to take it away. Measured against a real tenant.
    //
    // Refusing here is also the honest order of authority: SCIM is the
    // directory's statement about who works here, and a SAML assertion is only
    // a statement about who is holding the password.
    const deactivated = await db.execute<{ id: string }>(sql`
      SELECT id FROM scim_resources
      WHERE org_id = ${connection.orgId} AND lower(user_name) = ${email} AND NOT active`)
    if (deactivated[0]) {
      throw new ProvisioningRefused(
        'AF-EE-SSO-003',
        `${email} is marked inactive by the directory, so the sign-in was refused. ` +
          `Reactivate them in your identity provider and the next sign-in will work.`,
      )
    }

    const existing = await db.execute<{ user_id: string; role: Role; source: string }>(sql`
      SELECT m.user_id, m.role, m.source
      FROM members m JOIN users u ON u.id = m.user_id
      WHERE m.org_id = ${connection.orgId} AND lower(u.email) = ${email}`)

    const found = existing[0]
    if (found) {
      // Already a member. Never re-checked against the seat limit: somebody who
      // is already here signing in again must not be refused because the plan
      // shrank, and taking a seat away from a person mid-session is the
      // eviction this whole file is written to avoid.
      let roleChanged = false

      // A role set by hand here is not overwritten by the directory, matching
      // the rule syncMembership already applies to GitHub: somebody promoted in
      // this product stays promoted. The directory owns roles it granted.
      const directoryOwnsRole = found.source === 'sso' || found.source === 'scim'
      if (directoryOwnsRole && found.role !== desiredRole && (identity.groups ?? []).length > 0) {
        await db.execute(sql`
          UPDATE members SET role = ${desiredRole}, updated_at = ${now.toISOString()}
          WHERE org_id = ${connection.orgId} AND user_id = ${found.user_id}`)
        roleChanged = true
      }

      if (found.source === 'github') {
        // The same person arriving by a new route. Recorded, because "this
        // GitHub account and this directory account are one person" is a
        // security-relevant decision and an auditor should be able to see when
        // it was made and on whose assertion.
        await db.execute(sql`
          UPDATE members SET source = 'sso', updated_at = ${now.toISOString()}
          WHERE org_id = ${connection.orgId} AND user_id = ${found.user_id}`)
        await appendAudit(db, {
          orgId: connection.orgId,
          actorUserId: found.user_id,
          actorLabel: email,
          action: 'sso.identity.linked',
          targetType: 'user',
          targetId: found.user_id,
          origin: options.origin ?? 'sso',
          detail: { email, connection: connection.displayName, previousSource: 'github' },
          occurredAt: now,
        })
        return { userId: found.user_id, role: found.role, outcome: 'linked', roleChanged }
      }

      await touchUser(db, found.user_id, identity, now)
      return {
        userId: found.user_id,
        role: roleChanged ? desiredRole : found.role,
        outcome: 'existing',
        roleChanged,
      }
    }

    // A new person. The seat check happens here and only here.
    if (options.seats !== null && options.seats !== undefined) {
      const used = await db.execute<{ n: string }>(
        sql`SELECT count(*) AS n FROM members WHERE org_id = ${connection.orgId}`,
      )
      const inUse = Number(used[0]?.n ?? 0)
      if (inUse >= options.seats) {
        throw new ProvisioningRefused(
          'AF-EE-004',
          `The license covers ${options.seats} seats and they are all in use, so ${email} was not ` +
            `added. Remove an inactive member, or contact licensing@antifailure.dev to add seats. ` +
            `No existing member was removed.`,
        )
      }
    }

    // Somebody this organisation already had, and deprovisioned, is ADOPTED
    // rather than duplicated.
    //
    // The lookup above joins through members, so a person whose membership was
    // deleted by a SCIM deprovision matches nothing here. Creating a fresh row
    // for them was measured against a real Entra tenant to break offboarding:
    // the directory kept managing the old row while the person signed in as the
    // new one, so a later deprovision reported success and revoked nothing. See
    // migration 0013, which also explains why this needs a SECURITY DEFINER
    // function rather than a plain SELECT.
    const orphan = await db.execute<{ id: string | null }>(
      sql`SELECT adoptable_directory_user(${email}) AS id`,
    )
    const adopted = orphan[0]?.id ?? null

    // The key is generated here rather than asked for back. INSERT ...
    // RETURNING has the SELECT policies applied to the returned row, and this
    // account is not readable until its membership row exists, which is written
    // on the next statement. Migration 0006 records the same trap on the GitHub
    // path, and 0012 records the ordering it forces.
    const userId = adopted ?? randomUUID()

    // github_id and github_login are null here, which migration 0012 made
    // possible: this account did not come from GitHub and there is no id to
    // invent. identity_source records where it did come from.
    if (!adopted) {
      await db.execute(sql`
        INSERT INTO users (id, email, name, identity_source, created_at, updated_at)
        VALUES (${userId}, ${email}, ${displayNameFor(identity)}, 'sso',
                ${now.toISOString()}, ${now.toISOString()})`)
    }

    await db.execute(sql`
      INSERT INTO members (org_id, user_id, role, source, created_at, updated_at)
      VALUES (${connection.orgId}, ${userId}, ${desiredRole}, 'sso',
              ${now.toISOString()}, ${now.toISOString()})`)

    await appendAudit(db, {
      orgId: connection.orgId,
      actorUserId: userId,
      actorLabel: email,
      action: 'sso.member.provisioned',
      targetType: 'user',
      targetId: userId,
      origin: options.origin ?? 'sso',
      detail: { email, role: desiredRole, connection: connection.displayName },
      occurredAt: now,
    })

    return { userId, role: desiredRole, outcome: 'created', roleChanged: false }
  })
}

/** Whether this organization has proven it controls a domain. */
export async function verifiedDomain(
  pool: Pool,
  orgId: string,
  domain: string,
): Promise<boolean> {
  const rows = await pool.withTenant({ orgId }, async (db) =>
    db.execute<{ n: string }>(sql`
      SELECT count(*) AS n FROM sso_domains
      WHERE org_id = ${orgId} AND domain = ${domain.toLowerCase()} AND verified_at IS NOT NULL`),
  )
  return Number(rows[0]?.n ?? 0) > 0
}

/* eslint-disable @typescript-eslint/no-explicit-any */
async function touchUser(db: any, userId: string, identity: Identity, now: Date): Promise<void> {
  const name = displayNameFor(identity)
  if (!name) return
  // COALESCE on the existing value rather than overwriting: a directory that
  // sends no display name should not blank one somebody set.
  await db.execute(sql`
    UPDATE users SET name = ${name}, updated_at = ${now.toISOString()} WHERE id = ${userId}`)
}
/* eslint-enable @typescript-eslint/no-explicit-any */

function displayNameFor(identity: Identity): string | null {
  if (identity.displayName) return identity.displayName
  const parts = [identity.givenName, identity.familyName].filter(Boolean)
  return parts.length ? parts.join(' ') : null
}
