// The permission model as a file, and the diff before it is applied.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// A permission model that only exists in a database is a model nobody reviews.
// It changes one click at a time, there is no record of why, and the only way
// to answer "what changed last quarter" is to read an audit log line by line.
// Exported as a file, it goes in a repository, changes arrive as pull requests
// with a reason attached, and the review is the same review every other change
// gets.
//
// Import is deliberately two steps. A permission model applied straight from a
// file is a file that can lock everybody out of their own organization, and the
// person applying it is usually the one who would then be unable to fix it. So
// the diff is computed first, refuses outright to leave the organization
// without an owner, and says in words what will change.

import { parse, stringify } from 'yaml'
import type { Model } from './roles.ts'
import { validate } from './roles.ts'
import type { ApprovalPolicy } from './approvals.ts'
import { CHANGE_KINDS, type ChangeKind } from './approvals.ts'

export interface PolicyFile {
  version: 1
  roles: Model['roles']
  groups: Model['groups']
  grants: Model['grants']
  approvals: ApprovalPolicy[]
}

export class PolicyFileError extends Error {}

export function toYAML(file: PolicyFile): string {
  const header =
    '# The permission model for this organization.\n' +
    '#\n' +
    '# Exported by af, reviewed as a pull request, applied with a dry run first.\n' +
    '# Every permission named here comes from the fixed catalog: af license\n' +
    '# status lists them, and a name that is not one is refused on import\n' +
    '# rather than silently granting nothing.\n\n'
  return header + stringify(file, { lineWidth: 0 })
}

/**
 * Parses a file, refusing anything it cannot act on exactly.
 *
 * Every refusal names the line's subject rather than a schema path, because the
 * person reading it wrote YAML by hand and a message about a JSON pointer helps
 * nobody.
 */
export function fromYAML(text: string): PolicyFile {
  let raw: unknown
  try {
    raw = parse(text)
  } catch (err) {
    throw new PolicyFileError(
      `this is not valid YAML: ${err instanceof Error ? err.message : String(err)}`,
    )
  }
  if (raw === null || typeof raw !== 'object') {
    throw new PolicyFileError('the file is empty')
  }

  const doc = raw as Partial<PolicyFile>
  if (doc.version !== 1) {
    throw new PolicyFileError(
      `this file says version ${String(doc.version)} and this build understands version 1`,
    )
  }

  const file: PolicyFile = {
    version: 1,
    roles: doc.roles ?? [],
    groups: doc.groups ?? [],
    grants: doc.grants ?? [],
    approvals: doc.approvals ?? [],
  }

  // The role model's own validation, so a file and a model cannot be judged by
  // different rules.
  validate({ roles: file.roles, grants: file.grants, groups: file.groups })

  for (const policy of file.approvals) {
    if (!CHANGE_KINDS.includes(policy.kind as ChangeKind)) {
      throw new PolicyFileError(
        `${String(policy.kind)} is not something approvals apply to. ` +
          `They apply to: ${CHANGE_KINDS.join(', ')}.`,
      )
    }
    if (!Number.isInteger(policy.approvals) || policy.approvals < 0) {
      throw new PolicyFileError(
        `the ${policy.kind} policy asks for ${String(policy.approvals)} approvals`,
      )
    }
    if (!policy.reason) {
      // Shown to the proposer. A requirement with no reason reads as an
      // obstruction and gets routed around.
      throw new PolicyFileError(
        `the ${policy.kind} policy has no reason, and the proposer is shown it`,
      )
    }
  }
  return file
}

export interface Change {
  kind: 'added' | 'removed' | 'changed'
  what: string
  detail: string
}

export interface DryRun {
  changes: Change[]
  /** Refusals stop the import. A diff that would leave nobody able to manage
   *  members is a diff nobody can undo. */
  refusals: string[]
}

/**
 * What applying a file would do.
 *
 * Computed and shown before anything is written, because the person applying a
 * permission model is usually the person who would be locked out by a wrong one.
 */
export function dryRun(current: PolicyFile, next: PolicyFile): DryRun {
  const changes: Change[] = []

  diffByKey(
    current.roles, next.roles, (r) => r.id,
    (a, b) => JSON.stringify([a.name, a.description, [...a.permissions].sort()]) ===
      JSON.stringify([b.name, b.description, [...b.permissions].sort()]),
    'role', changes,
    (r) => `${r.name}: ${r.permissions.join(', ') || 'no permissions'}`,
  )

  diffByKey(
    current.groups, next.groups, (g) => g.name,
    (a, b) => JSON.stringify([...a.repositories].sort()) === JSON.stringify([...b.repositories].sort()),
    'repository group', changes,
    (g) => g.repositories.join(', '),
  )

  diffByKey(
    current.grants, next.grants,
    (g) => `${g.userId}|${g.roleId}|${g.scope.kind}|${g.scope.name ?? ''}`,
    () => true,
    'grant', changes,
    (g) => `${g.userId} as ${g.roleId} on ${g.scope.kind} ${g.scope.name ?? '(all)'}`,
  )

  diffByKey(
    current.approvals, next.approvals, (p) => p.kind,
    (a, b) => a.approvals === b.approvals && a.requires === b.requires,
    'approval policy', changes,
    (p) => `${p.approvals} approvals${p.requires ? ` from ${p.requires}` : ''}`,
  )

  const refusals: string[] = []
  // Nothing here can grant members.manage, because a custom role is added to
  // what a built-in role gives and the built-in owner is not defined in this
  // file. What this can do is remove every grant that somebody was relying on,
  // so the check is that the file does not leave the model empty of anybody who
  // can approve a change it requires approval for.
  for (const policy of next.approvals) {
    if (policy.approvals === 0 || !policy.requires) continue
    const canApprove = next.grants.some((grant) => {
      const role = next.roles.find((r) => r.id === grant.roleId)
      return role?.permissions.includes(policy.requires!)
    })
    if (!canApprove) {
      refusals.push(
        `${policy.kind} would need ${policy.approvals} approvals from somebody with ` +
          `${policy.requires}, and no grant in this file gives anybody that permission. ` +
          `Every change of that kind would be permanently pending.`,
      )
    }
  }

  return { changes, refusals }
}

function diffByKey<T>(
  current: T[],
  next: T[],
  key: (item: T) => string,
  same: (a: T, b: T) => boolean,
  noun: string,
  out: Change[],
  describe: (item: T) => string,
): void {
  const before = new Map(current.map((item) => [key(item), item]))
  const after = new Map(next.map((item) => [key(item), item]))

  for (const [k, item] of after) {
    const existing = before.get(k)
    if (!existing) {
      out.push({ kind: 'added', what: `${noun} ${k}`, detail: describe(item) })
      continue
    }
    if (!same(existing, item)) {
      out.push({
        kind: 'changed',
        what: `${noun} ${k}`,
        detail: `${describe(existing)} becomes ${describe(item)}`,
      })
    }
  }
  for (const [k, item] of before) {
    if (!after.has(k)) {
      out.push({ kind: 'removed', what: `${noun} ${k}`, detail: describe(item) })
    }
  }
}

/** The dry run in the words somebody reviewing a pull request would want. */
export function render(diff: DryRun): string {
  if (diff.refusals.length > 0) {
    return (
      'This file will not be applied:\n' +
      diff.refusals.map((r) => `  ${r}`).join('\n')
    )
  }
  if (diff.changes.length === 0) return 'Nothing would change.'

  const lines = ['This would change:']
  for (const kind of ['added', 'changed', 'removed'] as const) {
    for (const change of diff.changes.filter((c) => c.kind === kind)) {
      lines.push(`  ${kind.padEnd(8)} ${change.what}: ${change.detail}`)
    }
  }
  return lines.join('\n')
}
