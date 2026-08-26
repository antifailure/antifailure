// Approval policies: the changes that need somebody else to agree.
//
// Not MIT. Covered by the Antifailure Enterprise License; see ee/LICENSE.md.
//
// Two settings in this product are the ones where a mistake is a data incident
// rather than an inconvenience: which columns are masked, and which hosts an
// environment may reach. The community edition separates proposing from
// approving for both. This adds the part a large organization asks for, which
// is that the approval must come from somebody else, sometimes from more than
// one somebody, and sometimes only from a named role.
//
// Four rules, each of which is a way approval workflows are defeated in
// practice rather than in theory.
//
// The proposer never counts. An approval process one person can complete alone
// is a log entry, not a control.
//
// One person counts once. Approving twice from two roles is the same person
// agreeing with themselves.
//
// An approval is for a specific change. It records what it approved, so
// amending the proposal after approval and shipping it is not possible: the
// approvals no longer match and the request returns to pending.
//
// An approver who leaves does not take the request with them. Their approval is
// dropped and the request returns to pending, because the point of requiring a
// named role is that somebody in that role has looked, and somebody who is no
// longer in it has not.

import type { Permission } from '@antifailure/api'

/** What a proposal is about. */
export const CHANGE_KINDS = [
  'masking.rules',
  'network.policy',
  'network.loosening',
  'environment.regulated',
] as const
export type ChangeKind = (typeof CHANGE_KINDS)[number]

export interface ApprovalPolicy {
  kind: ChangeKind
  /** How many distinct people must agree. */
  approvals: number
  /** When set, an approval only counts from somebody holding this permission.
   *  Named by permission rather than by role, so a custom role that carries it
   *  qualifies without the policy having to list every role that might. */
  requires?: Permission
  /** Why this change needs review, shown to the proposer so the requirement
   *  does not read as an obstruction. */
  reason: string
}

export interface Approver {
  userId: string
  label: string
  /** Whether this person holds the permission the policy requires, evaluated
   *  when the approval is given. */
  qualified: boolean
  /** Whether they are still a member. Checked at decision time rather than at
   *  approval time, so somebody who has left stops counting. */
  member: boolean
}

export interface Approval {
  approver: Approver
  /** The digest of the proposal as it stood when this was given. */
  digest: string
  at: Date
}

export interface Proposal {
  id: string
  kind: ChangeKind
  proposedBy: string
  /** A digest of the change itself, so an amendment is detectable. */
  digest: string
  approvals: Approval[]
}

export type Decision =
  | { state: 'approved'; by: string[] }
  | { state: 'pending'; needed: number; reason: string }
  | { state: 'stale'; reason: string }

/**
 * Decides whether a proposal may proceed.
 *
 * Pure, and takes the current membership rather than reading it, so the same
 * proposal decides differently the moment somebody leaves and the caller does
 * not have to remember to re-check.
 */
export function decide(policy: ApprovalPolicy, proposal: Proposal): Decision {
  if (policy.approvals <= 0) {
    return { state: 'approved', by: [] }
  }

  // Approvals for an older version of the change do not count. Amending a
  // proposal after it has been approved and shipping it is the most obvious way
  // to defeat review, and the only defence is that an approval names what it
  // approved.
  const current = proposal.approvals.filter((a) => a.digest === proposal.digest)
  if (current.length < proposal.approvals.length) {
    const dropped = proposal.approvals.length - current.length
    if (current.length === 0) {
      return {
        state: 'stale',
        reason:
          `This proposal changed after it was approved, so its ${dropped} ` +
          `approval${dropped === 1 ? '' : 's'} no longer applies. Ask for review again.`,
      }
    }
  }

  const counted = new Map<string, Approval>()
  for (const approval of current) {
    // The proposer never counts. An approval process one person can complete
    // alone is a log entry, not a control.
    if (approval.approver.userId === proposal.proposedBy) continue
    // Somebody who has left has not looked at this, whatever they clicked
    // before they went.
    if (!approval.approver.member) continue
    if (policy.requires && !approval.approver.qualified) continue
    // One person counts once, however many roles they hold.
    counted.set(approval.approver.userId, approval)
  }

  if (counted.size >= policy.approvals) {
    return {
      state: 'approved',
      by: [...counted.values()].map((a) => a.approver.label).sort(),
    }
  }

  const needed = policy.approvals - counted.size
  return {
    state: 'pending',
    needed,
    reason:
      `${needed} more approval${needed === 1 ? '' : 's'} needed` +
      (policy.requires ? ` from somebody with ${policy.requires}` : '') +
      `. ${policy.reason}`,
  }
}

/**
 * The policy that applies to a change, or none.
 *
 * The strictest wins where several match, for the same reason it does
 * everywhere else here: adding a policy must never permit more than the set
 * without it.
 */
export function policyFor(
  policies: ApprovalPolicy[],
  kind: ChangeKind,
): ApprovalPolicy | undefined {
  const matching = policies.filter((p) => p.kind === kind)
  if (matching.length === 0) return undefined
  return matching.reduce((strictest, p) => {
    if (p.approvals > strictest.approvals) return p
    // A policy naming a required permission is stricter than one that does not,
    // at the same count.
    if (p.approvals === strictest.approvals && p.requires && !strictest.requires) return p
    return strictest
  })
}
