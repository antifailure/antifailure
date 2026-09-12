// Who may write which model, decided before anything is written.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A POLICY CANNOT GIVE ANYBODY A PERMISSION ITS AUTHOR DOES NOT HAVE. Without
// that rule the permission that lets somebody edit the model is every
// permission there is. An admin holds members.manage and deliberately does not
// hold billing.manage or organization.delete, and permissions.ts says why: those
// two have consequences outside the product and belong to whoever owns the
// relationship. A model an admin could write freely would let that admin define
// a role holding organization.delete, grant it to themselves, and delete the
// organization, and every row in the built-in table would still read correctly.
//
// The rule is applied to what CHANGES, not to the whole file. An owner may
// define a role an admin could not, and an admin re-applying the same file
// with an unrelated edit must not be refused for a role the owner wrote. So a
// permission is checked when it is added to a role and when a role holding it
// is newly granted to somebody. Removing is never refused: taking access away
// is what members.manage already permits.

import type { Permission } from '@antifailure/api'
import type { Model } from './roles.ts'
import type { PolicyFile } from './policyfile.ts'

export function escalations(current: Model, next: Model, held: readonly Permission[]): string[] {
  const holds = new Set<string>(held)
  const out: string[] = []
  const before = new Map(current.roles.map((r) => [r.id, new Set<string>(r.permissions)]))
  const after = new Map(next.roles.map((r) => [r.id, r]))

  for (const role of next.roles) {
    const had = before.get(role.id) ?? new Set<string>()
    for (const permission of role.permissions) {
      if (had.has(permission) || holds.has(permission)) continue
      out.push(
        `role ${role.id} would gain ${permission}, which your own role does not hold. ` +
          'A policy cannot give anybody a permission its author does not have.',
      )
    }
  }

  const granted = new Set(current.grants.map(grantKey))
  for (const grant of next.grants) {
    if (granted.has(grantKey(grant))) continue
    const role = after.get(grant.roleId)
    if (!role) continue
    const beyond = role.permissions.filter((p) => !holds.has(p))
    if (beyond.length === 0) continue
    out.push(
      `granting ${grant.roleId} to ${grant.userId} would give them ${beyond.join(', ')}, which ` +
        'your own role does not hold. A policy cannot give anybody a permission its author ' +
        'does not have.',
    )
  }
  return out
}

/**
 * The refusal for a file that carries approval policies.
 *
 * The file format has approvals and approvals.ts can decide one, and nothing in
 * the control plane asks it before a change is made. Storing a policy nothing
 * enforces would be a review requirement that reports itself as held, which is
 * the exact defect this package existed as for its whole life. So a file with
 * approvals is refused whole, saying so, rather than applied with that section
 * silently dropped: a person who wrote an approval requirement believes it is
 * in force, and the only honest answer is that it is not.
 */
export function approvalsRefusal(file: PolicyFile): string[] {
  if (file.approvals.length === 0) return []
  const n = file.approvals.length
  return [
    `this file carries ${n} approval ${n === 1 ? 'policy' : 'policies'}. They are part of the ` +
      'file format and this control plane does not enforce them, so storing one would record a ' +
      'review requirement that nothing checks. Remove the approvals section; nothing else in ' +
      'the file depends on it.',
  ]
}

function grantKey(g: Model['grants'][number]): string {
  return `${g.userId}|${g.roleId}|${g.scope.kind}|${g.scope.kind === 'organization' ? '' : (g.scope.name ?? '')}`
}
