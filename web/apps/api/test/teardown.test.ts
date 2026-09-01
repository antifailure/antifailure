// Teardown, and the runtime commands that make it more than a note to nobody.
//
// WHAT WAS WRONG. `environments.teardown` set a column and carried a comment
// saying the engine that holds the containers reads the row and does the
// removing. Nothing reads the row: there is no query anywhere in the engine
// against `environments`, no poller, and no endpoint that would serve one. So
// the containers, the database branch, the proxy and the DNS record stayed
// exactly where they were while the console said the environment was gone.
//
// WHAT CLOSES IT, and every hop of this is real today rather than waiting on an
// engine change:
//
//   the route writes a durable command and dispatches the customer's own
//     workflow with `command: down`, which runs `af down`
//   `af down` emits `env.destroyed`
//   the control plane sink maps that to `environment.torn_down`
//   ingestion projects it and acknowledges the command
//
// So the last test in this file is the one that matters most: the whole loop,
// driven end to end through the same HTTP endpoints an engine uses.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { randomUUID, createHash } from 'node:crypto'
import { COMMAND_TTL_MS } from '../src/workloads/commands.ts'
import {
  available, startApi, seedOrg, signInAs, callProcedure, errorCode, dropOrg,
  type ApiHarness, type Org, type SignedIn,
} from './harness.ts'

const hasDatabase = await available()

type Answer = { status: number; body: any }

describe('tearing an environment down', { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' }, () => {
  let h: ApiHarness
  let org: Org
  let other: Org
  let owner: SignedIn
  let token: string
  let otherToken: string

  before(async () => {
    h = await startApi()
    org = await seedOrg(h.admin, 'teardown')
    other = await seedOrg(h.admin, 'teardown-other')
    owner = await signInAs(h, org, 'owner')

    await h.admin`
      INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
      VALUES (${org.orgId}, ${Math.floor(Math.random() * 1e9)}, ${org.slug}, 'Organization')`
    h.github.addWorkflow(org.repository, 'antifailure.yml')

    token = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${org.orgId}, 'ci', ${createHash('sha256').update(token).digest()}, 'aft_td')`
    otherToken = `aft_${randomUUID().replace(/-/g, '')}`
    await h.admin`
      INSERT INTO engine_tokens (org_id, name, token_hash, prefix)
      VALUES (${other.orgId}, 'ci', ${createHash('sha256').update(otherToken).digest()}, 'aft_to')`
  })

  after(async () => {
    await dropOrg(h.admin, org.orgId)
    await dropOrg(h.admin, other.orgId)
    await h.close()
  })

  /** An environment of this test's own, so no test can settle another's. */
  async function environment(): Promise<string> {
    const envId = `env-${randomUUID().slice(0, 8)}`
    await h.admin`
      INSERT INTO environments (org_id, repository_id, env_id, branch, state)
      VALUES (${org.orgId}, ${org.repoId}, ${envId}, 'main', 'running')`
    return envId
  }

  async function teardown(envId: string): Promise<Answer> {
    return callProcedure(h, owner, 'environments.teardown', 'mutation', { envId })
  }

  async function commandFor(envId: string): Promise<{
    id: string; state: string; outcome: string | null; detail: string | null; attempts: number
  }> {
    const rows = await h.admin<{
      id: string; state: string; outcome: string | null; detail: string | null; attempts: number
    }[]>`
      SELECT c.id, c.state::text AS state, c.outcome, c.detail, c.attempts
      FROM runtime_commands c JOIN environments e ON e.id = c.environment_id
      WHERE e.env_id = ${envId} AND c.kind = 'environment.teardown'
      ORDER BY c.requested_at DESC LIMIT 1`
    return rows[0]!
  }

  async function post(path: string, body: unknown, bearer = token): Promise<{ status: number; body: any }> {
    const res = await h.fetch(path, {
      method: 'POST',
      headers: { authorization: `Bearer ${bearer}`, 'content-type': 'application/json' },
      body: JSON.stringify(body),
    })
    return { status: res.status, body: await res.json() }
  }

  // -------------------------------------------------------------------------

  it('dispatches af down through the customer own workflow, and records the command', async () => {
    const envId = await environment()
    const before = h.github.dispatches.length
    const asked = await teardown(envId)
    assert.equal(asked.status, 200, JSON.stringify(asked.body))
    assert.equal(asked.body.result.data.dispatched, true)

    // The one lever the control plane has into a customer's runtime. `af down`
    // takes no flags beyond a branch, and the dispatch already ran on the
    // environment's branch, so the inputs are the four the workflow declares
    // with nothing invented.
    const dispatch = h.github.dispatches[before]!
    assert.deepEqual(dispatch.inputs, { command: 'down', workflows: '', duration: '', scale: '' })
    assert.equal(dispatch.ref, 'main')

    const command = await commandFor(envId)
    assert.equal(command.state, 'pending')
    assert.match(command.detail!, /dispatched antifailure\.yml/)
  })

  it('records the request even when the dispatch is refused', async () => {
    // No App, a workflow with no `down` case, Actions disabled, GitHub down.
    // Every one of those used to lose the request entirely; now the durable
    // half survives and an engine can claim it.
    const envId = await environment()
    h.github.refuseDispatches('no workflow_dispatch trigger')
    try {
      const asked = await teardown(envId)
      assert.equal(asked.status, 200, JSON.stringify(asked.body))
      assert.equal(asked.body.result.data.dispatched, false)
      assert.match(asked.body.result.data.detail, /refused/)
    } finally {
      h.github.refuseDispatches(null)
    }
    const command = await commandFor(envId)
    assert.equal(command.state, 'pending')
    assert.match(command.detail!, /the dispatch was refused/)
  })

  it('pressing the button twice joins the first request rather than queueing a second', async () => {
    const envId = await environment()
    const first = await teardown(envId)
    assert.equal(first.status, 200)
    // The row is already torn_down, so the second press is a NOT_FOUND from the
    // environment update, and no second command exists. Two partial unique
    // indexes back that up in the database, so two presses landing together
    // cannot both read nothing and both write.
    const second = await teardown(envId)
    assert.equal(errorCode(second.body), 'NOT_FOUND')

    const commands = await h.admin<{ n: string }[]>`
      SELECT count(*) AS n FROM runtime_commands c
      JOIN environments e ON e.id = c.environment_id
      WHERE e.env_id = ${envId} AND c.kind = 'environment.teardown'`
    assert.equal(Number(commands[0]!.n), 1)
  })

  it('the engine reporting the environment gone is what acknowledges it', async () => {
    // The whole loop, and every hop is what ships today. This is the test that
    // distinguishes a teardown from a note to nobody.
    const envId = await environment()
    await teardown(envId)
    assert.equal((await commandFor(envId)).state, 'pending')

    const res = await h.fetch('/v1/events', {
      method: 'POST',
      headers: { authorization: `Bearer ${token}`, 'content-type': 'application/json' },
      body: JSON.stringify({
        events: [
          {
            id: randomUUID(),
            // What internal/controlplane/sink.go maps env.destroyed to.
            type: 'environment.torn_down',
            envId,
            sequence: 20,
            occurredAt: h.clock.now().toISOString(),
            payload: { repository: org.repository, branch: 'main' },
          },
        ],
      }),
    })
    assert.equal(res.status, 202)

    const command = await commandFor(envId)
    assert.equal(command.state, 'acknowledged')
    assert.equal(command.outcome, 'done')
    assert.match(command.detail!, /the engine reported the environment torn down/)
  })

  it('says a teardown was never confirmed rather than leaving it pending forever', async () => {
    const envId = await environment()
    await teardown(envId)
    h.clock.advance(COMMAND_TTL_MS + 1000)
    // Resolved by the next thing this organization does, which is the shape
    // that works: every policy on this table keys on current_org(), so a sweep
    // with no tenant set matches nothing and reports success.
    await callProcedure(h, owner, 'environments.list', 'query', { limit: 5 })

    const command = await commandFor(envId)
    assert.equal(command.state, 'expired')
    assert.match(command.detail!, /no runtime confirmed this/)
  })

  it('shows the console whether a runtime confirmed it, beside the optimistic row', async () => {
    const envId = await environment()
    await teardown(envId)
    const read: Answer = await callProcedure(h, owner, 'environments.get', 'query', { envId })
    assert.equal(read.status, 200, JSON.stringify(read.body))
    // The row says torn_down the moment somebody asks, because the quota counts
    // what is not torn down. The command is what says whether it happened, and
    // it is a to-one, so an object or null and never an array.
    assert.equal(read.body.result.data.state, 'torn_down')
    assert.ok(!Array.isArray(read.body.result.data.teardown))
    assert.equal(read.body.result.data.teardown.state, 'pending')
  })

  // -------------------------------------------------------------------------
  // The claim path, for a runtime the dispatch could not reach
  // -------------------------------------------------------------------------

  it('hands a pending command to an engine that asks, with a lease', async () => {
    const envId = await environment()
    h.github.refuseDispatches('actions are disabled')
    try {
      await teardown(envId)
    } finally {
      h.github.refuseDispatches(null)
    }

    const claimed = await post('/v1/commands/claim', { envId })
    assert.equal(claimed.status, 200)
    assert.equal(claimed.body.commands.length, 1)
    assert.equal(claimed.body.commands[0].kind, 'environment.teardown')
    assert.equal(claimed.body.commands[0].envId, envId)
    assert.ok(claimed.body.commands[0].leaseExpiresAt)

    // A second engine polling the same environment finds nothing, rather than
    // both of them tearing the same thing down.
    const again = await post('/v1/commands/claim', { envId })
    assert.equal(again.body.commands.length, 0)

    const acknowledged = await post(`/v1/commands/${claimed.body.commands[0].id}/ack`, {
      outcome: 'done', detail: 'removed 7 resources',
    })
    assert.equal(acknowledged.status, 200)
    const command = await commandFor(envId)
    assert.equal(command.state, 'acknowledged')
    assert.equal(command.detail, 'removed 7 resources')
    assert.equal(Number(command.attempts), 1)
  })

  it('records a teardown that could not be carried out, rather than calling it done', async () => {
    const envId = await environment()
    h.github.refuseDispatches('actions are disabled')
    try {
      await teardown(envId)
    } finally {
      h.github.refuseDispatches(null)
    }
    const claimed = await post('/v1/commands/claim', { envId })
    const failed = await post(`/v1/commands/${claimed.body.commands[0].id}/ack`, {
      outcome: 'failed', detail: 'the provider refused: 3 resources are pending',
    })
    assert.equal(failed.status, 200)
    const command = await commandFor(envId)
    assert.equal(command.state, 'failed')
    assert.equal(command.outcome, 'failed')
    assert.match(command.detail!, /3 resources are pending/)
  })

  it('refuses an acknowledgement from a token that does not hold the lease', async () => {
    const envId = await environment()
    await teardown(envId)
    const command = await commandFor(envId)
    // Never claimed, so nothing holds it. A 200 here would put the old defect
    // back one level up: an acknowledgement nobody applied, reported as applied.
    const refused = await post(`/v1/commands/${command.id}/ack`, { outcome: 'done' })
    assert.equal(refused.status, 409)
    assert.match(JSON.stringify(refused.body), /not claimed by this token/)
    assert.equal((await commandFor(envId)).state, 'pending')
  })

  it('will not hand one organization another organization commands', async () => {
    const envId = await environment()
    h.github.refuseDispatches('actions are disabled')
    try {
      await teardown(envId)
    } finally {
      h.github.refuseDispatches(null)
    }
    const stranger = await post('/v1/commands/claim', { envId }, otherToken)
    assert.equal(stranger.status, 200)
    assert.equal(stranger.body.commands.length, 0, 'a token reached another organization commands')
    assert.equal((await commandFor(envId)).state, 'pending')
  })

  it('lets a second engine take a command whose lease has run out', async () => {
    const envId = await environment()
    h.github.refuseDispatches('actions are disabled')
    try {
      await teardown(envId)
    } finally {
      h.github.refuseDispatches(null)
    }
    const first = await post('/v1/commands/claim', { envId })
    assert.equal(first.body.commands.length, 1)

    // The engine that claimed it died. Without this the command is stranded for
    // the rest of its life, which is the same silent nothing in slower clothes.
    h.clock.advance(20 * 60 * 1000)
    const second = await post('/v1/commands/claim', { envId })
    assert.equal(second.body.commands.length, 1)
    assert.equal(second.body.commands[0].id, first.body.commands[0].id)
    assert.equal(second.body.commands[0].attempts, 2)
  })

  it('is still reachable while the organization is suspended', async () => {
    // A suspension stops new work. Stopping what is already running is the
    // opposite of new work, and refusing it would leave a suspended
    // organization unable to stop paying for what it has up.
    const envId = await environment()
    h.github.refuseDispatches('actions are disabled')
    try {
      await teardown(envId)
    } finally {
      h.github.refuseDispatches(null)
    }
    await h.admin`
      UPDATE organizations SET suspended_at = now(), suspended_reason = 'testing'
      WHERE id = ${org.orgId}`
    try {
      const claimed = await post('/v1/commands/claim', { envId })
      assert.equal(claimed.status, 200)
      assert.equal(claimed.body.commands.length, 1)
    } finally {
      await h.admin`
        UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
        WHERE id = ${org.orgId}`
    }
  })
})
