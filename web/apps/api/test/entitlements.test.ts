// What an override actually CHANGES.
//
// The thing worth testing here is not that a row can be written. It is that a
// row changes an answer somebody gets, at a call site that could refuse them,
// and that the refusal comes back first so the test cannot pass by the limit
// never having been reached at all. Every case below is red-then-green: the
// same request is made twice with one row inserted between, and the assertion
// is that the first was refused and the second was not.
//
// The alternative, which is what an entitlement system usually ships as, is a
// table, a service that reads it, an admin screen that writes it, and no call
// site: a grant that looks like a working feature to the operator, to the
// customer, and to every unit test, and does nothing.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import {
  ENTITLEMENTS,
  applyOverrides,
  checkQuotaWithEntitlements,
  checkCostCapWithEntitlements,
  seatVerdict,
} from '../src/entitlements.ts'
import {
  available,
  callProcedure,
  dropOrg,
  errorCode,
  seedOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
  type SignedIn,
} from './harness.ts'

const srcDir = path.join(path.dirname(fileURLToPath(import.meta.url)), '..', 'src')

// ---------------------------------------------------------------------------
// The catalogue's own claim
// ---------------------------------------------------------------------------

describe('the entitlement catalogue', () => {
  it('every entitlement that claims to be enforced names a call site that exists', async () => {
    for (const [key, spec] of Object.entries(ENTITLEMENTS)) {
      if (spec.enforcedAt === null) {
        // Not enforced is allowed. Not enforced and not SAYING SO is not:
        // "reported but not enforced" is indistinguishable from a bug unless
        // somebody wrote down which one it is.
        assert.ok(
          spec.notEnforcedBecause && spec.notEnforcedBecause.length > 20,
          `${key} is not enforced and does not say why`,
        )
        continue
      }
      const [file, symbol] = spec.enforcedAt.split(':')
      assert.ok(file && symbol, `${key} has an unreadable enforcedAt: ${spec.enforcedAt}`)
      const source = await readFile(path.join(srcDir, file), 'utf8')
      assert.ok(
        source.includes(symbol),
        `${key} claims to be enforced at ${spec.enforcedAt}, and ${file} never calls ${symbol}`,
      )
    }
  })

  it('every plan the quota table knows has a value for every entitlement', () => {
    for (const [key, spec] of Object.entries(ENTITLEMENTS)) {
      for (const plan of ['free', 'team', 'enterprise']) {
        assert.notEqual(
          spec.byPlan[plan], undefined,
          `${key} has no value for the ${plan} plan, so a ${plan} customer would fall back to free`,
        )
      }
    }
  })
})

// ---------------------------------------------------------------------------
// Precedence and coercion, without a database
// ---------------------------------------------------------------------------

function row(over: Record<string, unknown>) {
  return {
    id: 'id', scope: 'organization', feature: 'environments', value: 1,
    reason: 'because', ticket: null, created_by_label: 'ops',
    created_at: new Date('2026-01-01T00:00:00Z'), expires_at: null,
    ...over,
  } as never
}

describe('resolution', () => {
  it('the plan decides when nothing overrides it', () => {
    const e = applyOverrides('team', [])
    assert.equal(e.number('environments'), 25)
    assert.equal(e.get('environments')!.source, 'plan')
    assert.equal(e.get('environments')!.override, null)
  })

  it('the most specific scope wins however the rows came back', () => {
    // Deliberately in the WRONG order. A resolver that let the last row win
    // would pass with these sorted and fail in production the first time a
    // query planner chose a different index.
    const e = applyOverrides('team', [
      row({ scope: 'user', value: 4 }),
      row({ scope: 'global', value: 1 }),
      row({ scope: 'project', value: 3 }),
      row({ scope: 'organization', value: 2 }),
    ])
    assert.equal(e.number('environments'), 4)
    assert.equal(e.get('environments')!.source, 'user')

    const reversed = applyOverrides('team', [
      row({ scope: 'organization', value: 2 }),
      row({ scope: 'project', value: 3 }),
      row({ scope: 'global', value: 1 }),
      row({ scope: 'user', value: 4 }),
    ])
    assert.equal(reversed.number('environments'), 4)
  })

  it('an override may lower a limit as well as raise one', () => {
    // Containment is the same mechanism as a sale. A resolver that only ever
    // granted would leave capping a runaway tenant to a deploy.
    const e = applyOverrides('enterprise', [row({ value: 1 })])
    assert.equal(e.number('environments'), 1)
    assert.equal(e.get('environments')!.planValue, 500)
  })

  it('the plan value is kept beside the override, so a screen can show both', () => {
    const e = applyOverrides('free', [row({ value: 40 })])
    const r = e.get('environments')!
    assert.equal(r.value, 40)
    assert.equal(r.planValue, 3)
    assert.equal(r.override!.reason, 'because')
  })

  it('a value of the wrong shape falls back to the plan rather than to zero', () => {
    // The direction is the whole test. Coercing an unreadable value to 0 would
    // set the limit to zero, refuse everything, and look to the customer
    // exactly like a suspended account.
    for (const bad of [null, {}, [], 'forty', true]) {
      const e = applyOverrides('team', [row({ value: bad })])
      assert.equal(e.number('environments'), 25, `${JSON.stringify(bad)} did not fall back`)
      assert.equal(e.get('environments')!.source, 'plan')
    }
  })

  it('a quoted number is accepted, because an admin form is a text box', () => {
    const e = applyOverrides('free', [row({ value: '40' })])
    assert.equal(e.number('environments'), 40)
  })

  it('an override for an entitlement this build has never heard of is ignored', () => {
    // A row written by a newer deploy that has since rolled back must not take
    // out every OTHER entitlement for that organization.
    const e = applyOverrides('team', [
      row({ feature: 'quantum_environments', value: 9 }),
      row({ feature: 'environments', value: 40 }),
    ])
    assert.equal(e.number('environments'), 40)
    assert.equal(e.get('quantum_environments'), undefined)
  })

  it('a plan nobody has heard of falls back to free rather than to nothing', () => {
    const e = applyOverrides('platinum-plus', [])
    assert.equal(e.number('environments'), 3)
  })
})

describe('the verdicts', () => {
  it('a refusal says whether the limit came from the plan or from a grant', () => {
    const plan = checkQuotaWithEntitlements(applyOverrides('free', []), 'environments', 3)
    assert.equal(plan.allowed, false)
    assert.match(plan.reason, /the plan/)

    const granted = checkQuotaWithEntitlements(
      applyOverrides('free', [row({ value: 5 })]), 'environments', 5,
    )
    assert.equal(granted.allowed, false)
    assert.match(granted.reason, /organization override/)
  })

  it('a refusal names an expiry when the grant has one', () => {
    const e = applyOverrides('free', [
      row({ value: 5, expires_at: new Date('2026-12-25T00:00:00Z') }),
    ])
    assert.match(checkQuotaWithEntitlements(e, 'environments', 5).reason, /until 2026-12-25/)
  })

  it('the cost cap uses the projected total, not the current one', () => {
    const e = applyOverrides('free', [])
    // free is 24 per run and 72 per day.
    assert.equal(checkCostCapWithEntitlements(e, 10, 65).allowed, false)
    assert.equal(checkCostCapWithEntitlements(e, 10, 60).allowed, true)
    // The per-run cap is reported first when both are broken, because it is
    // the one the caller can fix in the same breath.
    assert.equal(checkCostCapWithEntitlements(e, 100, 5000).kind, 'per-run')
  })

  it('an override moves the cost cap', () => {
    const e = applyOverrides('free', [row({ feature: 'perDayHours', value: 500 })])
    assert.equal(checkCostCapWithEntitlements(e, 10, 65).allowed, true)
  })

  it('seats count open invitations, and the message says so', () => {
    const e = applyOverrides('free', [])
    assert.equal(seatVerdict(e, 5, 0).allowed, false)
    assert.equal(seatVerdict(e, 4, 1).allowed, false)
    assert.match(seatVerdict(e, 4, 1).reason, /one of them an invitation/)
    assert.equal(seatVerdict(e, 4, 0).allowed, true)
  })
})

// ---------------------------------------------------------------------------
// The wiring, against a real database and the real routes
// ---------------------------------------------------------------------------

describe('an override changes what a route answers', async () => {
  if (!(await available())) {
    it('skipped: no database', () => {})
    return
  }

  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'ent')
    owner = await signInAs(h, org, 'owner')
    // Note on the expiry test below: the resolver compares against the
    // request's own clock, which is the FakeClock, not the wall clock. An
    // expiry of "a minute ago" in real time is still years in the future to
    // this suite, so the expired case uses a fixed date in 2000.
    // The free plan allows three live environments; seedOrg made one. Two more
    // takes the organization to its limit with nothing granted.
    await h.admin`
      INSERT INTO environments (org_id, repository_id, env_id, branch, state)
      VALUES (${org.orgId}, ${org.repoId}, ${'env-a'}, 'main', 'running'),
             (${org.orgId}, ${org.repoId}, ${'env-b'}, 'main', 'running')`
    // The App has to be installed or every create is refused one gate LATER
    // than the one under test, and the test would pass on the wrong refusal.
    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e9)}, ${org.slug}, 'Organization')`
    h.github.addWorkflow(org.repository, 'antifailure.yml')
    h.github.addWorkflow(`${org.slug}/other`, 'antifailure.yml')
  })

  after(async () => {
    await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${org.orgId}`
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  async function create() {
    return callProcedure(h, owner, 'environments.create', 'mutation', {
      repository: org.repository,
    })
  }

  it('is refused at the plan limit before any grant exists', async () => {
    const refused = await create()
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(
      JSON.stringify(refused.body), /holding 3 of 3 environments/,
      'the refusal did not come from the quota',
    )
  })

  it('an organization grant lets the same request through', async () => {
    await h.admin`
      INSERT INTO entitlement_overrides
        (scope, scope_id, org_id, feature, value, reason, created_by_label)
      VALUES ('organization', ${org.orgId}, ${org.orgId}, 'environments', ${'40'}::jsonb,
              'Sold 40 on a bespoke contract, AF-118', 'ops@antifailure.test')`
    const allowed = await create()
    assert.equal(errorCode(allowed.body), null, JSON.stringify(allowed.body))
  })

  it('an expired grant stops applying without anything having to sweep it', async () => {
    await h.admin`
      UPDATE entitlement_overrides
      SET expires_at = ${'2000-01-02T00:00:00Z'}, created_at = ${'2000-01-01T00:00:00Z'}
      WHERE org_id = ${org.orgId}`
    const refused = await create()
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(JSON.stringify(refused.body), /holding 3 of 3/)
  })

  it('a revoked grant stops applying', async () => {
    await h.admin`
      UPDATE entitlement_overrides
      SET expires_at = NULL, revoked_at = now(), revoked_by_label = 'ops', revoked_reason = 'done'
      WHERE org_id = ${org.orgId}`
    assert.equal(errorCode((await create()).body), 'PRECONDITION_FAILED')
  })

  it('a grant for a DIFFERENT organization does nothing for this one', async () => {
    const other = await seedOrg(h.admin, 'ent-other')
    try {
      await h.admin`
        INSERT INTO entitlement_overrides
          (scope, scope_id, org_id, feature, value, reason, created_by_label)
        VALUES ('organization', ${other.orgId}, ${other.orgId}, 'environments', ${'40'}::jsonb,
                'somebody else', 'ops@antifailure.test')`
      assert.equal(errorCode((await create()).body), 'PRECONDITION_FAILED')
    } finally {
      await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${other.orgId}`
      await dropOrg(h.admin, other.orgId)
    }
  })

  it('a project grant applies to its own repository and not to another', async () => {
    const [second] = await h.admin<{ id: string }[]>`
      INSERT INTO repositories (org_id, full_name) VALUES (${org.orgId}, ${`${org.slug}/other`})
      RETURNING id`
    try {
      await h.admin`
        INSERT INTO entitlement_overrides
          (scope, scope_id, org_id, feature, value, reason, created_by_label)
        VALUES ('project', ${org.repoId}, ${org.orgId}, 'environments', ${'40'}::jsonb,
                'this repository only', 'ops@antifailure.test')`

      assert.equal(errorCode((await create()).body), null, 'the granted repository was refused')

      const otherRepo = await callProcedure(h, owner, 'environments.create', 'mutation', {
        repository: `${org.slug}/other`,
      })
      assert.equal(
        errorCode(otherRepo.body), 'PRECONDITION_FAILED',
        'a project grant leaked to a repository it does not name',
      )
    } finally {
      await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${org.orgId}`
      await h.admin`DELETE FROM repositories WHERE id = ${second!.id}`
    }
  })

  it('the plan screen reports the limit that is actually enforced', async () => {
    // The failure this guards is not cosmetic. Before the reporting paths were
    // moved onto the resolver, dispatch refused at forty and billing.get said
    // twenty five, so a customer who had been sold extra capacity was told they
    // were over a limit they were not over. A reported limit that disagrees
    // with the enforced one is worse than no reported limit.
    await h.admin`
      INSERT INTO entitlement_overrides
        (scope, scope_id, org_id, feature, value, reason, ticket, created_by_label)
      VALUES ('organization', ${org.orgId}, ${org.orgId}, 'environments', ${'40'}::jsonb,
              'Sold 40 on a bespoke contract', 'AF-118', 'ops@antifailure.test')`
    try {
      const answer = await callProcedure(h, owner, 'billing.get', 'query', undefined)
      const body = answer.body as {
        result: { data: { entitlements: {
          key: string; value: number; planValue: number
          override: { scope: string; reason: string; ticket: string | null } | null
        }[] } }
      }
      const envs = body.result.data.entitlements.find((e) => e.key === 'environments')!
      assert.equal(envs.value, 40)
      // The plan's own number travels beside it, or the screen cannot show
      // what was changed without a second request.
      assert.equal(envs.planValue, 3)
      assert.equal(envs.override!.scope, 'organization')
      assert.equal(envs.override!.reason, 'Sold 40 on a bespoke contract')
      assert.equal(envs.override!.ticket, 'AF-118')

      // And org.status, which is what the rest of the console reads.
      const status = await callProcedure(h, owner, 'org.status', 'query', undefined)
      const quotas = (status.body as {
        result: { data: { quotas: { environments: { limit: number; allowed: boolean } } } }
      }).result.data.quotas
      assert.equal(quotas.environments.limit, 40)
      assert.equal(quotas.environments.allowed, true, 'reported as over a limit it is not over')

      // Every entitlement the catalogue has, so a new one cannot be added to
      // the resolver and quietly left off the screen.
      assert.equal(
        body.result.data.entitlements.length,
        Object.keys(ENTITLEMENTS).length,
        'the plan screen is not showing every entitlement',
      )
    } finally {
      await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${org.orgId}`
    }
  })

  it('the tenant role cannot write itself a grant', async () => {
    const { sql } = await import('drizzle-orm')
    let thrown: unknown
    try {
      await h.pool.withTenant({ orgId: org.orgId, userId: owner.userId }, async (db) => {
        await db.execute(sql`
          INSERT INTO entitlement_overrides
            (scope, scope_id, org_id, feature, value, reason, created_by_label)
          VALUES ('organization', ${org.orgId}, ${org.orgId}, 'environments', '999'::jsonb,
                  'self service', 'me')`)
      })
    } catch (e) {
      thrown = e
    }
    assert.ok(thrown, 'the tenant role wrote itself an entitlement override')
    // drizzle wraps the driver's error, so the reason is on the cause. Read
    // both rather than only the wrapper, or this asserts on the word "Failed".
    const said = `${(thrown as Error).message} ${(thrown as { cause?: Error }).cause?.message ?? ''}`
    assert.match(said, /permission denied/i, said)
  })
})

describe('seats are enforced where they can be refused', async () => {
  if (!(await available())) {
    it('skipped: no database', () => {})
    return
  }

  let h: ApiHarness
  let org: Org
  let owner: SignedIn

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'seats')
    owner = await signInAs(h, org, 'owner')
  })

  after(async () => {
    await h.admin`DELETE FROM entitlement_overrides WHERE org_id = ${org.orgId}`
    await dropOrg(h.admin, org.orgId)
    await h.close()
  })

  it('refuses the invitation that would go past the free plan and then accepts it once granted', async () => {
    // The free plan is five seats. One owner exists, so four invitations fit.
    for (let i = 0; i < 4; i += 1) {
      const ok = await callProcedure(h, owner, 'invitations.create', 'mutation', {
        email: `seat-${i}@example.test`, role: 'member',
      })
      assert.equal(errorCode(ok.body), null, `invitation ${i} was refused: ${JSON.stringify(ok.body)}`)
    }

    const refused = await callProcedure(h, owner, 'invitations.create', 'mutation', {
      email: 'seat-over@example.test', role: 'member',
    })
    assert.equal(errorCode(refused.body), 'PRECONDITION_FAILED')
    assert.match(
      JSON.stringify(refused.body), /5 of 5 seats/,
      'the refusal did not come from the seat limit',
    )
    // The invitations that have not been accepted are what took it over, and
    // the message has to say so or the reader counts members and finds one.
    assert.match(JSON.stringify(refused.body), /invitations that have not been accepted/)

    await h.admin`
      INSERT INTO entitlement_overrides
        (scope, scope_id, org_id, feature, value, reason, created_by_label)
      VALUES ('organization', ${org.orgId}, ${org.orgId}, 'seats', ${'25'}::jsonb,
              'Pilot expanded to 25 people, AF-204', 'ops@antifailure.test')`

    const allowed = await callProcedure(h, owner, 'invitations.create', 'mutation', {
      email: 'seat-over@example.test', role: 'member',
    })
    assert.equal(errorCode(allowed.body), null, JSON.stringify(allowed.body))
  })
})
