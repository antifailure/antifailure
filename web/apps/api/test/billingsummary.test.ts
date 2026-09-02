// getOrgBillingSummary, against a real database rather than a stubbed one.
//
// The function exists for a caller outside this lane, which changes what is
// worth testing. Its return shape is somebody else's dependency, so the tests
// below assert the awkward parts of it: that "no subscription" is null rather
// than a throw, that "no limit" is null rather than Infinity or 0, and that an
// organization the handle cannot see is indistinguishable from one that does
// not exist. Those are the three answers a caller is most likely to get wrong
// by guessing.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID } from 'node:crypto'
import { getOrgBillingSummary } from '../src/billing-summary.ts'
import { available, seedOrg, startApi, type ApiHarness, type Org } from './harness.ts'

const hasDatabase = await available()

describe('one organization s billing summary', { skip: hasDatabase ? false : 'no database' }, () => {
  let h: ApiHarness
  let org: Org
  const now = new Date('2026-03-01T00:00:00.000Z')

  const summary = (orgId: string, asOrg?: string) =>
    h.pool.withTenant({ orgId: asOrg ?? orgId }, (db) => getOrgBillingSummary(db, orgId, now))

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'summary')
  })
  after(async () => {
    await h?.close()
  })

  it('answers with the plan and slug on the organization row', async () => {
    const s = await summary(org.orgId)
    assert.ok(s)
    assert.equal(s.orgId, org.orgId)
    assert.equal(s.slug, org.slug)
    assert.equal(s.plan, 'free')
  })

  it('returns null for an organization that does not exist', async () => {
    // Not a throw. A caller that has been handed an id from somewhere else
    // needs to distinguish "gone" from "broken", and an exception makes every
    // deleted organization look like an outage.
    assert.equal(await summary(randomUUID()), null)
  })

  it('returns the same null for an organization this handle may not read', async () => {
    // The interesting half. Under the tenant pool, asking about somebody else's
    // organization must answer exactly what asking about a nonexistent one
    // answers, or the function is a membership oracle: a caller could learn
    // which ids are real by watching the two cases diverge.
    const other = await seedOrg(h.admin, 'summary-other')
    assert.equal(await summary(other.orgId, org.orgId), null)
  })

  it('has no subscription until one is written, and says so with null', async () => {
    const s = await summary(org.orgId)
    assert.equal(s!.subscription, null)
  })

  it('counts a member and an open invitation as seats, and ignores the rest', async () => {
    const [user] = await h.admin<{ id: string }[]>`
      INSERT INTO users (email, name) VALUES (${`seat-${randomUUID()}@example.com`}, 'Seat')
      RETURNING id`
    await h.admin`
      INSERT INTO members (org_id, user_id, role) VALUES (${org.orgId}, ${user!.id}, 'member')`

    const invite = async (label: string, columns: Record<string, unknown>) => {
      await h.admin`
        INSERT INTO invitations ${h.admin({
          org_id: org.orgId,
          email: `${label}-${randomUUID()}@example.com`,
          role: 'member',
          token_hash: Buffer.from(randomUUID()),
          invited_by_label: 'a test',
          expires_at: new Date('2026-04-01T00:00:00.000Z'),
          ...columns,
        })}`
    }
    await invite('open', {})
    await invite('expired', { expires_at: new Date('2026-02-01T00:00:00.000Z') })
    await invite('revoked', { revoked_at: new Date('2026-02-01T00:00:00.000Z') })
    // accepted_at without accepted_user_id is refused by
    // invitations_accepted_together, which is the schema being right.
    await invite('accepted', {
      accepted_at: new Date('2026-02-01T00:00:00.000Z'),
      accepted_user_id: user!.id,
    })

    const s = await summary(org.orgId)
    assert.equal(s!.seats.members, 1)
    // One open invitation. An expired one has released its seat, a revoked one
    // never held it, and an accepted one is already counted as a member.
    assert.equal(s!.seats.openInvitations, 1)
    assert.equal(s!.seats.used, 2)
    assert.equal(s!.seats.limit, 5, 'the free plan allows five')
    assert.equal(s!.seats.atLimit, false)
  })

  it('reports the newest subscription, not the active one', async () => {
    // A cancelled subscription is still the answer to what this customer is on
    // until something replaces it. Filtering on status would return null for an
    // organization in exactly the state somebody calls support about.
    const write = async (plan: string, status: string, at: string, quantity: number) => {
      await h.admin`
        INSERT INTO subscriptions ${h.admin({
          org_id: org.orgId,
          stripe_subscription_id: `sub_${randomUUID().slice(0, 8)}`,
          stripe_customer_id: 'cus_summary',
          plan,
          quantity,
          status,
          current_period_end: new Date('2026-04-15T00:00:00.000Z'),
          last_event_at: new Date(at),
        })}`
    }
    await write('team', 'active', '2026-01-01T00:00:00.000Z', 3)
    await write('free', 'canceled', '2026-02-01T00:00:00.000Z', 1)

    const s = await summary(org.orgId)
    assert.equal(s!.subscription!.status, 'canceled')
    assert.equal(s!.subscription!.plan, 'free')
    assert.equal(s!.subscription!.quantity, 1)
    // A Date, not the `2026-04-14 19:00:00-05` string the driver returns for a
    // timestamptz read through a raw statement. A caller typed against this
    // interface calls .getTime() on it, and the string version throws there
    // rather than here.
    assert.ok(s!.subscription!.currentPeriodEnd instanceof Date, 'currentPeriodEnd is not a Date')
    assert.equal(
      s!.subscription!.currentPeriodEnd!.toISOString(),
      '2026-04-15T00:00:00.000Z',
    )
    // The organization row still says free while the newest subscription says
    // free too. They are separate fields because they routinely disagree, and a
    // caller reading only one of them reads the wrong one half the time.
    assert.equal(s!.plan, 'free')
  })

  it('carries a live override with its scope and the reason somebody typed', async () => {
    await h.admin`
      INSERT INTO entitlement_overrides ${h.admin({
        scope: 'organization',
        scope_id: org.orgId,
        org_id: org.orgId,
        feature: 'seats',
        value: h.admin.json(40),
        reason: 'sold forty seats on the call',
        ticket: 'AF-1234',
        created_by_label: 'an operator',
      })}`

    const s = await summary(org.orgId)
    const seats = s!.overrides.find((o) => o.feature === 'seats')
    assert.ok(seats, 'the seats override is not in the summary')
    assert.equal(seats.scope, 'organization')
    assert.equal(seats.value, 40)
    assert.equal(seats.planValue, 5, 'the plan value is carried so a caller can show both')
    assert.equal(seats.reason, 'sold forty seats on the call')
    assert.equal(seats.ticket, 'AF-1234')
    assert.equal(seats.grantedBy, 'an operator')
    assert.ok(seats.grantedAt instanceof Date, 'grantedAt is not a Date')
    // And the override is what the seat limit now reads, rather than the plan.
    assert.equal(s!.seats.limit, 40)
  })

  it('leaves out an override that was revoked and one that has expired', async () => {
    const grant = async (feature: string, columns: Record<string, unknown>) => {
      await h.admin`
        INSERT INTO entitlement_overrides ${h.admin({
          scope: 'organization',
          scope_id: org.orgId,
          org_id: org.orgId,
          feature,
          value: h.admin.json(99),
          reason: `a ${feature} grant`,
          created_by_label: 'an operator',
          ...columns,
        })}`
    }
    // Revocation is three columns or none. Two of them is refused by
    // entitlement_overrides_revocation, which is this migration's own rule that
    // a withdrawal must say who and why, exactly as a grant must.
    await grant('retentionDays', {
      revoked_at: new Date('2026-02-01T00:00:00.000Z'),
      revoked_by_label: 'an operator',
      revoked_reason: 'withdrawn',
    })
    // created_at is pinned rather than left to default. The default is the
    // DATABASE clock, which is the real one, and entitlement_overrides_expiry
    // requires expires_at > created_at. An expiry chosen to be in the past
    // relative to this suite's fake `now` is also in the past relative to the
    // real one, so the insert is refused and the test fails for a reason that
    // has nothing to do with what it is checking.
    await grant('apiRateMultiplier', {
      created_at: new Date('2026-01-01T00:00:00.000Z'),
      expires_at: new Date('2026-02-01T00:00:00.000Z'),
    })

    const s = await summary(org.orgId)
    const features = s!.overrides.map((o) => o.feature)
    assert.ok(!features.includes('retentionDays'), 'a revoked grant is still being reported')
    assert.ok(!features.includes('apiRateMultiplier'), 'an expired grant is still being reported')
    // The live one from the previous test is still there, so this is not
    // passing by reporting nothing at all.
    assert.ok(features.includes('seats'))
  })
})
