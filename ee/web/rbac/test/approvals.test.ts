// Approval policies, tested against the ways they are defeated in practice.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  decide, policyFor, toYAML, fromYAML, dryRun, render, PolicyFileError,
  type ApprovalPolicy, type Approver, type Proposal, type PolicyFile,
} from '../src/index.ts'

const policy: ApprovalPolicy = {
  kind: 'masking.rules',
  approvals: 2,
  requires: 'masking.approve',
  reason: 'A wrong masking rule is a data incident rather than an inconvenience.',
}

function approver(userId: string, over: Partial<Approver> = {}): Approver {
  return { userId, label: userId, qualified: true, member: true, ...over }
}

function proposal(over: Partial<Proposal> = {}): Proposal {
  return { id: 'p1', kind: 'masking.rules', proposedBy: 'ada', digest: 'v1', approvals: [], ...over }
}

const at = new Date('2026-01-01T00:00:00Z')

describe('approvals', () => {
  it('needs the stated number from other people', () => {
    const pending = decide(policy, proposal({
      approvals: [{ approver: approver('grace'), digest: 'v1', at }],
    }))
    assert.equal(pending.state, 'pending')
    assert.equal(pending.needed, 1)
    // The reason travels with the requirement, because a requirement with no
    // reason reads as an obstruction and gets routed around.
    assert.match(pending.reason, /data incident/)

    const approved = decide(policy, proposal({
      approvals: [
        { approver: approver('grace'), digest: 'v1', at },
        { approver: approver('hopper'), digest: 'v1', at },
      ],
    }))
    assert.equal(approved.state, 'approved')
    assert.deepEqual(approved.by, ['grace', 'hopper'])
  })

  it('never counts the proposer', () => {
    // An approval process one person can complete alone is a log entry, not a
    // control.
    const decision = decide({ ...policy, approvals: 1 }, proposal({
      approvals: [{ approver: approver('ada'), digest: 'v1', at }],
    }))
    assert.equal(decision.state, 'pending')
  })

  it('counts one person once however many times they approve', () => {
    const decision = decide(policy, proposal({
      approvals: [
        { approver: approver('grace'), digest: 'v1', at },
        { approver: approver('grace'), digest: 'v1', at },
      ],
    }))
    assert.equal(decision.state, 'pending', 'one person approved twice and it counted twice')
  })

  it('drops the approval of somebody who has left', () => {
    // The point of requiring a named role is that somebody in it has looked,
    // and somebody who is no longer a member has not.
    const decision = decide({ ...policy, approvals: 1 }, proposal({
      approvals: [{ approver: approver('grace', { member: false }), digest: 'v1', at }],
    }))
    assert.equal(decision.state, 'pending')
  })

  it('ignores an approval from somebody without the required permission', () => {
    const decision = decide({ ...policy, approvals: 1 }, proposal({
      approvals: [{ approver: approver('grace', { qualified: false }), digest: 'v1', at }],
    }))
    assert.equal(decision.state, 'pending')
    assert.match(decision.reason, /masking\.approve/)
  })

  it('counts anybody when the policy names no permission', () => {
    const open: ApprovalPolicy = {
      kind: 'network.policy', approvals: 1, reason: 'Loosening egress needs a second pair of eyes.',
    }
    const decision = decide(open, proposal({
      kind: 'network.policy',
      approvals: [{ approver: approver('grace', { qualified: false }), digest: 'v1', at }],
    }))
    assert.equal(decision.state, 'approved')
  })

  it('discards approvals when the proposal is amended', () => {
    // Amending after approval and shipping it is the most obvious way to defeat
    // review, and an approval naming what it approved is the only defence.
    const decision = decide(policy, proposal({
      digest: 'v2',
      approvals: [
        { approver: approver('grace'), digest: 'v1', at },
        { approver: approver('hopper'), digest: 'v1', at },
      ],
    }))
    assert.equal(decision.state, 'stale')
    assert.match(decision.reason, /changed after it was approved/)
  })

  it('keeps approvals given after an amendment', () => {
    const decision = decide({ ...policy, approvals: 1 }, proposal({
      digest: 'v2',
      approvals: [
        { approver: approver('grace'), digest: 'v1', at },
        { approver: approver('hopper'), digest: 'v2', at },
      ],
    }))
    assert.equal(decision.state, 'approved')
    assert.deepEqual(decision.by, ['hopper'])
  })

  it('a policy of zero approves immediately', () => {
    const decision = decide({ ...policy, approvals: 0 }, proposal())
    assert.equal(decision.state, 'approved')
  })

  it('the strictest policy wins where several match', () => {
    // Adding a policy must never permit more than the set without it.
    const chosen = policyFor([
      { kind: 'masking.rules', approvals: 1, reason: 'r' },
      { kind: 'masking.rules', approvals: 2, reason: 'r' },
      { kind: 'network.policy', approvals: 5, reason: 'r' },
    ], 'masking.rules')
    assert.equal(chosen?.approvals, 2)

    const qualified = policyFor([
      { kind: 'masking.rules', approvals: 2, reason: 'r' },
      { kind: 'masking.rules', approvals: 2, requires: 'masking.approve', reason: 'r' },
    ], 'masking.rules')
    assert.equal(qualified?.requires, 'masking.approve',
      'at the same count, requiring a permission is stricter than not')

    assert.equal(policyFor([], 'masking.rules'), undefined)
  })
})

// ---------------------------------------------------------------------------

function file(over: Partial<PolicyFile> = {}): PolicyFile {
  return {
    version: 1,
    roles: [{
      id: 'approver', name: 'Approver', description: 'Approves masking changes.',
      permissions: ['masking.approve'],
    }],
    groups: [],
    grants: [{ userId: 'grace', roleId: 'approver', scope: { kind: 'organization' } }],
    approvals: [policy],
    ...over,
  }
}

describe('the policy file', () => {
  it('round trips through YAML', () => {
    const original = file()
    const parsed = fromYAML(toYAML(original))
    assert.deepEqual(parsed, original)
  })

  it('carries a header explaining what it is', () => {
    // It lands in a repository and somebody who did not export it opens it.
    const text = toYAML(file())
    assert.match(text, /^#/)
    assert.match(text, /reviewed as a pull request/)
  })

  it('refuses a version it does not understand', () => {
    assert.throws(() => fromYAML('version: 2\n'), PolicyFileError)
  })

  it('refuses text that is not YAML, and says so', () => {
    assert.throws(() => fromYAML('roles: [\n  unclosed'), /not valid YAML/)
  })

  it('refuses an approval policy with no reason', () => {
    assert.throws(
      () => fromYAML(toYAML(file({
        approvals: [{ kind: 'masking.rules', approvals: 1, reason: '' }],
      }))),
      /no reason/,
    )
  })

  it('refuses an approval policy for something approvals do not apply to', () => {
    assert.throws(
      () => fromYAML(toYAML(file({
        approvals: [{ kind: 'nonsense' as never, approvals: 1, reason: 'r' }],
      }))),
      /is not something approvals apply to/,
    )
  })

  it('applies the role model’s own validation', () => {
    // A file and a model must not be judged by different rules.
    assert.throws(
      () => fromYAML(toYAML(file({
        grants: [{ userId: 'g', roleId: 'ghost', scope: { kind: 'organization' } }],
      }))),
      /does not exist/,
    )
  })
})

describe('the dry run', () => {
  it('says nothing would change when nothing would', () => {
    assert.equal(render(dryRun(file(), file())), 'Nothing would change.')
  })

  it('names what is added, changed, and removed', () => {
    const next = file({
      roles: [{
        id: 'approver', name: 'Approver', description: 'Approves masking changes.',
        permissions: ['masking.approve', 'masking.edit'],
      }, {
        id: 'reader', name: 'Reader', description: 'Reads environments.',
        permissions: ['environments.view'],
      }],
      // grace's grant is replaced by hopper's. Somebody keeps masking.approve,
      // so the refusal below does not fire and this test can see the diff.
      grants: [{ userId: 'hopper', roleId: 'approver', scope: { kind: 'organization' } }],
    })
    const diff = dryRun(file(), next)
    const text = render(diff)

    assert.match(text, /added\s+role reader/)
    assert.match(text, /changed\s+role approver/)
    assert.match(text, /removed\s+grant grace/)
    assert.match(text, /added\s+grant hopper/)
  })

  it('refuses a file that would make a required approval unreachable', () => {
    // Every change of that kind would be permanently pending, and the person
    // applying the file is usually the one who would then have to fix it.
    const next = file({ grants: [] })
    const diff = dryRun(file(), next)

    assert.ok(diff.refusals.length > 0)
    assert.match(diff.refusals[0]!, /permanently pending/)
    assert.match(render(diff), /will not be applied/)
  })

  it('does not refuse when the policy needs no particular permission', () => {
    const open = file({
      approvals: [{ kind: 'masking.rules', approvals: 2, reason: 'r' }],
      grants: [],
    })
    assert.deepEqual(dryRun(file(), open).refusals, [])
  })

  it('does not refuse a policy of zero approvals', () => {
    const none = file({
      approvals: [{ kind: 'masking.rules', approvals: 0, requires: 'masking.approve', reason: 'r' }],
      grants: [],
    })
    assert.deepEqual(dryRun(file(), none).refusals, [])
  })
})
