// The pull request the App opens to add the workflow file.
//
// The claims worth testing are the ones that were false before this existed
// and would be false again silently:
//
//   - installing the App on a repository ENQUEUES a setup, once, whichever
//     delivery names the repository first and however many times it is named;
//   - the sweeper does the four GitHub calls, records the pull request, and
//     does nothing to a repository that already has the file;
//   - a missing permission is recorded and not retried, and the grant is what
//     puts it back;
//   - a branch a dead sweeper left behind is reused rather than refused;
//   - a failure is retried five times and then said so;
//   - the file the App writes is the file the documentation shows.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createHmac, randomUUID } from 'node:crypto'
import { readFile } from 'node:fs/promises'
import { sql } from 'drizzle-orm'
import { handleDelivery } from '../src/github/webhook.ts'
import { FakeRepositoryApi } from '../src/github/fakeapi.ts'
import {
  CONTROL_PLANE_VARIABLE,
  SETUP_ATTEMPTS,
  SETUP_BRANCH,
  SETUP_DOCS_URL,
  SETUP_TITLE,
  WORKFLOW_PATH,
  WORKFLOW_TEMPLATE,
  setupPullRequestBody,
  sweepSetups,
} from '../src/github/setup.ts'
import { available, callProcedure, dropOrg, signInAs, startApi, type ApiHarness } from './harness.ts'
import type { Clock } from '../src/clock.ts'

const hasDatabase = await available()

const SECRET = 'a-setup-webhook-secret'

const frozen: Clock = {
  now: () => new Date('2026-08-28T12:00:00Z'),
  monotonicMs: () => 0,
  sleep: async () => {},
}

// ---------------------------------------------------------------------------

describe('the workflow file the App writes', () => {
  it('is byte for byte the file in examples/, which is the source of truth', async () => {
    // Three copies of this file exist: examples/, the engine's embed for
    // `af init`, and this one. A customer who reads the documentation, runs
    // `af init` and merges the App's pull request must get the same file from
    // all three, and the only thing holding them together is this comparison.
    // Four levels up from web/apps/api/test is the repository root.
    const source = await readFile(
      new URL('../../../../examples/github-workflow.yml', import.meta.url),
      'utf8',
    )
    assert.equal(
      WORKFLOW_TEMPLATE,
      source,
      'web/apps/api/src/github/setup/antifailure.yml differs from examples/github-workflow.yml. ' +
        'examples/ is the source of truth: copy it over the api copy rather than editing either by hand.',
    )
  })

  it('is not empty and is the workflow it claims to be', () => {
    assert.match(WORKFLOW_TEMPLATE, /^name: Antifailure$/m)
    assert.match(WORKFLOW_TEMPLATE, /uses: antifailure\/antifailure\/\.github\/workflows\/check\.yml@/)
  })
})

describe('the pull request body', () => {
  const body = setupPullRequestBody({
    repository: 'acme/app',
    defaultBranch: 'main',
    controlPlane: 'https://app.antifailure.dev',
  })

  it('names the one variable the hosted control plane needs, with its address', () => {
    assert.ok(body.includes(`\`${CONTROL_PLANE_VARIABLE}\``), 'the variable name is missing')
    assert.ok(body.includes('`https://app.antifailure.dev`'), 'the address is missing')
  })

  it('links to the documentation', () => {
    assert.ok(body.includes(SETUP_DOCS_URL))
  })

  it('names the optional secrets, and says none is required', () => {
    for (const name of [
      'ANTHROPIC_API_KEY',
      'AF_MASKING_KEY',
      'STRIPE_TEST_SECRET_KEY',
      'database.source_url_env',
    ]) {
      assert.ok(body.includes(name), `${name} is not named`)
    }
    assert.match(body, /No secret is required/)
  })

  it('says nothing runs until it is merged, and what forks wait for', () => {
    assert.match(body, /Nothing runs until this pull request is merged/)
    assert.ok(body.includes('`antifailure:allow`'))
  })

  it('contains no em dash', () => {
    assert.ok(!body.includes('—'), 'the body contains an em dash')
    assert.ok(!SETUP_TITLE.includes('—'))
  })

  it('says what to do with no control plane address rather than printing null', () => {
    const without = setupPullRequestBody({ repository: 'acme/app', defaultBranch: 'main', controlPlane: null })
    assert.ok(!without.includes('null'))
    assert.ok(without.includes(`\`${CONTROL_PLANE_VARIABLE}\``))
  })
})

// ---------------------------------------------------------------------------

describe(
  'what an installation delivery enqueues',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    // Unique per run, because installation_id is UNIQUE and a run killed
    // before its after hook leaves the row behind.
    const login = `setup-hook-${randomUUID().slice(0, 8)}`
    const account = { login, type: 'Organization' }
    const installationId =
      917_000_000 + Number(BigInt('0x' + randomUUID().slice(0, 8)) % 100_000_000n)
    const installation = { id: installationId, account }
    const clock = frozen
    const deps = () => ({ github: h.github, analytics: h.analytics })

    before(async () => {
      h = await startApi({ githubWebhookSecret: SECRET })
    })
    after(async () => {
      const rows = await h.admin<{ id: string }[]>`
        SELECT id FROM organizations WHERE github_login = ${login}`
      for (const row of rows) await dropOrg(h.admin, row.id)
      await h.close()
    })

    async function setups(): Promise<{ repository: string; state: string; attempts: number }[]> {
      const rows = await h.admin<{ repository: string; state: string; attempts: number }[]>`
        SELECT r.full_name AS repository, s.state, s.attempts
        FROM repository_setups s JOIN repositories r ON r.id = s.repository_id
        JOIN organizations o ON o.id = s.org_id
        WHERE o.github_login = ${login}
        ORDER BY r.full_name`
      // Plain objects: postgres.js answers with a Result, which deepEqual
      // tells apart from an array.
      return rows.map((r) => ({ repository: r.repository, state: r.state, attempts: r.attempts }))
    }

    it('ordering: repositories added before the installation event still enqueue, once', async () => {
      // GitHub promises no order. The handler creates the installation row on
      // either event, so the repositories event arriving first must queue its
      // repositories itself and must not throw for the row not existing yet.
      const out = await handleDelivery(h.pool, clock, 'installation_repositories', {
        action: 'added',
        installation,
        repositories_added: [{ id: 11, full_name: `${login}/early`, default_branch: 'main' }],
        repositories_removed: [],
      }, deps())
      assert.equal(out.handled, true)
      assert.match(out.detail, /1 setup pull requests queued/)
      assert.deepEqual(await setups(), [{ repository: `${login}/early`, state: 'queued', attempts: 0 }])
    })

    it('the installation event enqueues one row per repository, and none twice', async () => {
      const out = await handleDelivery(h.pool, clock, 'installation', {
        action: 'created',
        installation,
        repositories: [
          { id: 11, full_name: `${login}/early`, default_branch: 'main' },
          { id: 12, full_name: `${login}/app`, default_branch: 'develop' },
          { id: 13, full_name: `${login}/web`, default_branch: 'main' },
        ],
      }, deps())
      assert.equal(out.handled, true)
      // Two, not three: `early` was queued by the repositories event above.
      assert.match(out.detail, /2 setup pull requests queued/)
      assert.deepEqual(await setups(), [
        { repository: `${login}/app`, state: 'queued', attempts: 0 },
        { repository: `${login}/early`, state: 'queued', attempts: 0 },
        { repository: `${login}/web`, state: 'queued', attempts: 0 },
      ])
    })

    it('a repository added later is enqueued, and a redelivery adds nothing', async () => {
      const payload = {
        action: 'added',
        installation,
        repositories_added: [{ id: 14, full_name: `${login}/api`, default_branch: 'main' }],
        repositories_removed: [],
      }
      const first = await handleDelivery(h.pool, clock, 'installation_repositories', payload, deps())
      assert.match(first.detail, /1 setup pull requests queued/)
      const again = await handleDelivery(h.pool, clock, 'installation_repositories', payload, deps())
      assert.ok(!/queued/.test(again.detail), `a redelivery queued again: ${again.detail}`)
      const rows = await setups()
      assert.equal(rows.filter((r) => r.repository === `${login}/api`).length, 1)
      assert.equal(rows.length, 4)
    })

    it('the same delivery replayed through the route is fenced before the handler runs', async () => {
      // The handler's ON CONFLICT is one fence; the delivery ledger is the
      // other, and it is the one that stops a captured delivery being handled
      // at all. Both are asserted because either alone would pass a test that
      // the other had silently lost.
      const body = JSON.stringify({
        action: 'added',
        installation,
        repositories_added: [{ id: 15, full_name: `${login}/replayed`, default_branch: 'main' }],
        repositories_removed: [],
      })
      const deliveryId = `setup-replay-${randomUUID()}`
      const send = () =>
        h.fetch('/webhooks/github', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            'x-github-event': 'installation_repositories',
            'x-github-delivery': deliveryId,
            'x-hub-signature-256':
              'sha256=' + createHmac('sha256', SECRET).update(body, 'utf8').digest('hex'),
          },
          body,
        })
      const first = (await (await send()).json()) as { detail: string; replay?: boolean }
      assert.match(first.detail, /1 setup pull requests queued/)
      const second = (await (await send()).json()) as { detail: string; replay?: boolean }
      assert.equal(second.replay, true)
      const rows = await setups()
      assert.equal(rows.filter((r) => r.repository === `${login}/replayed`).length, 1)
      await h.admin`DELETE FROM github_deliveries WHERE delivery_id = ${deliveryId}`
    })

    it('an accepted permission puts every refused setup back in the queue', async () => {
      await h.admin`
        UPDATE repository_setups SET state = 'needs_permission', attempts = 1,
          last_error = 'The GitHub App installation does not hold the contents: write permission.'
        WHERE repository_id IN (
          SELECT id FROM repositories WHERE full_name IN (${`${login}/app`}, ${`${login}/web`}))`
      // And one that failed for another reason, which the grant says nothing
      // about and must not touch.
      await h.admin`
        UPDATE repository_setups SET state = 'failed', attempts = 5, last_error = 'gave up'
        WHERE repository_id = (SELECT id FROM repositories WHERE full_name = ${`${login}/api`})`

      const out = await handleDelivery(h.pool, clock, 'installation', {
        action: 'new_permissions_accepted',
        installation,
        repositories: [
          { id: 11, full_name: `${login}/early` },
          { id: 12, full_name: `${login}/app` },
          { id: 13, full_name: `${login}/web` },
          { id: 14, full_name: `${login}/api` },
        ],
      }, deps())
      assert.match(out.detail, /2 refused setups retried/)
      assert.ok(!/queued/.test(out.detail), 'a permission grant re-queued repositories that were already queued')

      const rows = await h.admin<{ repository: string; state: string; attempts: number; last_error: string | null }[]>`
        SELECT r.full_name AS repository, s.state, s.attempts, s.last_error
        FROM repository_setups s JOIN repositories r ON r.id = s.repository_id
        WHERE r.full_name IN (${`${login}/app`}, ${`${login}/web`}, ${`${login}/api`})
        ORDER BY r.full_name`
      assert.deepEqual(rows.map((r) => ({ ...r })), [
        { repository: `${login}/api`, state: 'failed', attempts: 5, last_error: 'gave up' },
        { repository: `${login}/app`, state: 'queued', attempts: 0, last_error: null },
        { repository: `${login}/web`, state: 'queued', attempts: 0, last_error: null },
      ])
    })

    it('a created delivery that is not a grant leaves a refused setup alone', async () => {
      await h.admin`
        UPDATE repository_setups SET state = 'needs_permission', attempts = 1
        WHERE repository_id = (SELECT id FROM repositories WHERE full_name = ${`${login}/web`})`
      await handleDelivery(h.pool, clock, 'installation', {
        action: 'created',
        installation,
        repositories: [{ id: 13, full_name: `${login}/web` }],
      }, deps())
      const [row] = await h.admin<{ state: string }[]>`
        SELECT s.state FROM repository_setups s JOIN repositories r ON r.id = s.repository_id
        WHERE r.full_name = ${`${login}/web`}`
      assert.equal(row?.state, 'needs_permission')
    })
  },
)

// ---------------------------------------------------------------------------

describe(
  'the setup sweep',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    let api: FakeRepositoryApi
    let orgId: string
    const slug = `setup-${randomUUID().slice(0, 8)}`
    const installationId =
      916_000_000 + Number(BigInt('0x' + randomUUID().slice(0, 8)) % 100_000_000n)
    const HEAD = 'a1b2c3d4e5f60718293a4b5c6d7e8f9012345678'

    before(async () => {
      api = new FakeRepositoryApi()
      api.grant('contents: write', 'pull requests: write')
      h = await startApi({ githubApi: api })
      // A run killed before its after hook leaves its organization behind, and
      // the sweeper reads every due row in the database, so a dead
      // predecessor's rows would be swept here and fail whichever assertion
      // they reached first. Only ones old enough to belong to a run that is
      // certainly over.
      await h.admin`
        DELETE FROM organizations
        WHERE slug LIKE 'setup-%' AND created_at < now() - interval '1 hour'`
      const [org] = await h.admin<{ id: string }[]>`
        INSERT INTO organizations (slug, name, github_login) VALUES (${slug}, 'setup', ${slug})
        RETURNING id`
      orgId = org!.id
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${orgId}, ${installationId}, ${slug}, 'Organization')`
    })

    after(async () => {
      await dropOrg(h.admin, orgId)
      await h.close()
    })

    function deps() {
      return {
        pool: h.pool,
        clock: h.clock,
        api,
        consoleBase: 'https://plane.test/',
        holder: 'the-test',
      }
    }

    /** A connected repository with a queued setup, the state the webhook
     *  handler leaves behind. */
    async function connect(name: string, defaultBranch = 'main'): Promise<string> {
      const full = `${slug}/${name}`
      const [repo] = await h.admin<{ id: string }[]>`
        INSERT INTO repositories (org_id, full_name, default_branch)
        VALUES (${orgId}, ${full}, ${defaultBranch}) RETURNING id`
      await h.admin`
        INSERT INTO repository_setups (org_id, repository_id, requested_at, updated_at)
        VALUES (${orgId}, ${repo!.id}, ${h.clock.now()}, ${h.clock.now()})`
      api.addBranch(full, defaultBranch, HEAD)
      return full
    }

    async function setupOf(full: string) {
      const [row] = await h.admin<{
        state: string
        attempts: number
        branch: string | null
        pull_request_number: number | null
        pull_request_url: string | null
        last_error: string | null
        lease_holder: string | null
        leased_until: Date | null
        finished_at: Date | null
      }[]>`
        SELECT s.state, s.attempts, s.branch, s.pull_request_number, s.pull_request_url,
               s.last_error, s.lease_holder, s.leased_until, s.finished_at
        FROM repository_setups s JOIN repositories r ON r.id = s.repository_id
        WHERE r.full_name = ${full}`
      assert.ok(row, `no setup row for ${full}`)
      return row!
    }

    function callsFor(full: string) {
      return api.calls.filter((c) => c.repository === full).map((c) => c.method)
    }

    it('a repository that already has the file is recorded present, after one read', async () => {
      const full = await connect('already')
      api.addFile({ repository: full, branch: 'main', path: WORKFLOW_PATH, content: 'their own' })

      const swept = await sweepSetups(deps())
      assert.equal(swept.present, 1)
      const row = await setupOf(full)
      assert.equal(row.state, 'present')
      assert.equal(row.pull_request_number, null)
      assert.ok(row.finished_at, 'present is terminal and was not stamped finished')
      assert.equal(row.lease_holder, null, 'the lease was not released')
      assert.deepEqual(callsFor(full), ['fileExists'])
      assert.equal(api.pullRequests.some((p) => p.repository === full), false)
    })

    it('opens the pull request in four calls and records it', async () => {
      const full = await connect('fresh', 'develop')

      const swept = await sweepSetups(deps())
      assert.equal(swept.opened, 1)
      assert.deepEqual(callsFor(full), ['fileExists', 'branchHead', 'createBranch', 'putFile', 'createPullRequest'])

      const opened = api.pullRequests.find((p) => p.repository === full)
      assert.ok(opened, 'no pull request was opened')
      assert.equal(opened!.title, SETUP_TITLE)
      assert.equal(opened!.head, SETUP_BRANCH)
      assert.equal(opened!.base, 'develop', 'the pull request is not against the default branch')
      assert.ok(opened!.body.includes(`\`${CONTROL_PLANE_VARIABLE}\``), 'the body does not name the variable')
      assert.ok(opened!.body.includes('`https://plane.test`'), 'the body does not carry the address, trimmed')
      assert.ok(opened!.body.includes(SETUP_DOCS_URL), 'the body does not link the documentation')
      assert.ok(!opened!.body.includes('—'), 'the body contains an em dash')

      // The branch was made from the default branch's head and the file on it
      // is the template, byte for byte.
      assert.equal(api.branchSha(full, SETUP_BRANCH), HEAD)
      assert.equal(api.fileOn(full, SETUP_BRANCH, WORKFLOW_PATH)?.content, WORKFLOW_TEMPLATE)
      assert.equal(api.fileOn(full, 'develop', WORKFLOW_PATH), undefined, 'the default branch was written to')

      const row = await setupOf(full)
      assert.equal(row.state, 'opened')
      assert.equal(row.branch, SETUP_BRANCH)
      assert.equal(row.pull_request_number, opened!.number)
      assert.equal(row.pull_request_url, opened!.url)
      assert.equal(row.attempts, 1)
      assert.ok(row.finished_at)
    })

    it('a branch a dead sweeper left behind is reused, and the pull request still opens', async () => {
      const full = await connect('halfway')
      // The branch exists at an older commit, the way it does when the last
      // holder died after createBranch. It must not be moved: somebody may
      // have pushed to it.
      const older = 'ffffffffffffffffffffffffffffffffffffffff'
      api.addBranch(full, SETUP_BRANCH, older)

      const swept = await sweepSetups(deps())
      assert.equal(swept.opened, 1)
      assert.equal(api.branchSha(full, SETUP_BRANCH), older, 'the existing branch was moved')
      const row = await setupOf(full)
      assert.equal(row.state, 'opened')
      assert.ok(api.pullRequests.some((p) => p.repository === full))
    })

    it('a pull request that already exists for the branch is found rather than duplicated', async () => {
      const full = await connect('twice')
      // The last holder died after opening the pull request and before
      // recording it. GitHub refuses a second one with the same head.
      api.addBranch(full, SETUP_BRANCH, HEAD)
      const before = await api.createPullRequest(installationId, full, {
        title: SETUP_TITLE,
        head: SETUP_BRANCH,
        base: 'main',
        body: 'opened by the last holder',
      })

      await sweepSetups(deps())
      const row = await setupOf(full)
      assert.equal(row.state, 'opened')
      assert.equal(row.pull_request_number, before.number)
      assert.equal(api.pullRequests.filter((p) => p.repository === full).length, 1)
    })

    it('a missing Contents write is recorded with the remedy, and not retried', async () => {
      const full = await connect('refused')
      api.revoke('contents: write')
      try {
        // Read is still held by every installation, so the file check answers
        // and the refusal lands on the first write.
        api.grant('contents: read')
        const swept = await sweepSetups(deps())
        assert.equal(swept.needsPermission, 1)
        const row = await setupOf(full)
        assert.equal(row.state, 'needs_permission')
        assert.match(row.last_error!, /contents: write/)
        assert.match(row.last_error!, /Accept new permissions/)
        assert.deepEqual(callsFor(full), ['fileExists', 'branchHead', 'createBranch'])

        // Not retried: a second sweep does not touch it.
        api.calls.length = 0
        h.clock.advance(60 * 60 * 1000)
        await sweepSetups(deps())
        assert.deepEqual(callsFor(full), [])
        assert.equal((await setupOf(full)).attempts, 1)
      } finally {
        api.revoke('contents: read')
        api.grant('contents: write')
      }
    })

    it('a failure is retried, and the fifth one is the last', async () => {
      const full = await connect('flaky')
      for (let attempt = 1; attempt < SETUP_ATTEMPTS; attempt += 1) {
        api.breakWith(`the network went away on attempt ${attempt}`, 502)
        const swept = await sweepSetups(deps())
        assert.equal(swept.retried, 1, `attempt ${attempt} was not counted as a retry`)
        const row = await setupOf(full)
        assert.equal(row.state, 'queued', `attempt ${attempt} did not go back to the queue`)
        assert.equal(row.attempts, attempt)
        assert.equal(row.leased_until, null, 'the lease was not released on retry')
        assert.match(row.last_error!, new RegExp(`attempt ${attempt}`))
      }
      api.breakWith('the network went away for good', 502)
      const swept = await sweepSetups(deps())
      assert.equal(swept.failed, 1)
      const row = await setupOf(full)
      assert.equal(row.state, 'failed')
      assert.equal(row.attempts, SETUP_ATTEMPTS)
      assert.match(row.last_error!, /Given up after 5 attempts/)
      assert.match(row.last_error!, /for good/)
      assert.ok(row.finished_at)

      // And it stays failed: nothing sweeps it again.
      api.calls.length = 0
      await sweepSetups(deps())
      assert.deepEqual(callsFor(full), [])
    })

    it('a lease that expired is taken over rather than waited on forever', async () => {
      const full = await connect('orphaned')
      await h.admin`
        UPDATE repository_setups SET state = 'leased', lease_holder = 'a-dead-replica',
          leased_until = ${new Date(h.clock.now().getTime() - 1000)}, attempts = 1
        WHERE repository_id = (SELECT id FROM repositories WHERE full_name = ${full})`
      const swept = await sweepSetups(deps())
      assert.equal(swept.opened, 1)
      const row = await setupOf(full)
      assert.equal(row.state, 'opened')
      assert.equal(row.attempts, 2)
    })

    it('an archived repository is skipped and says so', async () => {
      const full = await connect('archived')
      await h.admin`UPDATE repositories SET archived_at = now() WHERE full_name = ${full}`
      const swept = await sweepSetups(deps())
      assert.equal(swept.skipped, 1)
      const row = await setupOf(full)
      assert.equal(row.state, 'skipped')
      assert.match(row.last_error!, /archived/)
      assert.deepEqual(callsFor(full), [])
    })

    it('repositories.setup lists the rows for the organization, oldest request first', async () => {
      const org = { orgId, slug, repoId: '', repository: `${slug}/fresh`, envId: '' }
      const viewer = await signInAs(h, org, 'viewer')
      const res = await callProcedure(h, viewer, 'repositories.setup', 'query', undefined)
      assert.equal(res.status, 200, JSON.stringify(res.body))
      const rows = (res.body as { result: { data: { repository: string; state: string; pull_request_url: string | null }[] } })
        .result.data
      const fresh = rows.find((r) => r.repository === `${slug}/fresh`)
      assert.ok(fresh, 'the opened repository is not listed')
      assert.equal(fresh!.state, 'opened')
      assert.match(fresh!.pull_request_url!, /\/pull\/\d+$/)
      assert.ok(rows.some((r) => r.repository === `${slug}/refused` && r.state === 'needs_permission'))
      assert.ok(!rows.some((r) => r.repository === `${slug}/archived`), 'an archived repository is listed')
      // Requested in this order by the tests above.
      const names = rows.map((r) => r.repository)
      assert.ok(names.indexOf(`${slug}/already`) < names.indexOf(`${slug}/flaky`))
    })

    it('a tenant can read its setups and cannot write them', async () => {
      // The policy is SELECT for a tenant. An UPDATE on a connection with a
      // tenant and no account matches nothing, and the row is unchanged.
      await h.pool.withTenant({ orgId }, async (db) => {
        await db.execute(sql`
          UPDATE repository_setups SET state = 'opened', pull_request_number = 999
          WHERE org_id = ${orgId}::uuid AND state = 'failed'`)
      })
      const row = await setupOf(`${slug}/flaky`)
      assert.equal(row.state, 'failed')
      assert.equal(row.pull_request_number, null)
    })
  },
)
