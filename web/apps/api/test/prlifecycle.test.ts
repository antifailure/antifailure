// The pull request lifecycle, one ordering per test.
//
// THE DONE-CHECK FOR THIS FEATURE IS A TABLE OF ORDERINGS WITH A VERIFIED
// OUTCOME IN EVERY CELL, and an empty cell is an unshipped bug. Everything here
// is event driven across three systems that make no promises to each other
// about order: GitHub's deliveries, GitHub Actions' run events, and a job in
// somebody else's continuous integration reporting back over the internet.
// Testing that the happy ordering lands in the right state is not testing this.
//
// The orderings, and each has a test below with the same name:
//
//   open then workflow          the ordinary one
//   workflow then open          Actions is faster than the pull request event
//   request then callback       the ordinary one
//   callback then request       a job reports before its check exists
//   engine event before callback  the environment lands before the verdict
//   synchronize during an old run a push while a check is running
//   close before ready          closed before anything started
//   close during a run          closed while a check is running
//   reopen during teardown      reopened before the teardown is confirmed
//   duplicate delivery          see githubapp.test.ts, which owns the fence
//   timeout                     nothing reported before the deadline
//   missing callback            the run finished and said nothing
//   fork approval then a new sha the approval is void
//   concurrent deliveries       see githubapp.test.ts
//
// Every entry point that can reach this state is exercised: a GitHub App
// delivery, a GitHub Actions run event, the API a job calls, the console's
// teardown verb, and the sweepers that stand in for cron.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import {
  createHmac,
  createSign,
  generateKeyPairSync,
  randomUUID,
  type KeyObject,
} from 'node:crypto'
import { FakeRepositoryApi } from '../src/github/fakeapi.ts'
import { ACTIONS_ISSUER, CALLBACK_AUDIENCE } from '../src/github/oidc.ts'
import {
  DEFAULT_DEADLINE_MS,
  FORK_APPROVAL_LABEL,
  sweepGenerations,
  sweepTeardowns,
  TEARDOWN_ATTEMPTS,
  TEARDOWN_LEASE_MS,
  TIMED_OUT_DETAIL,
  WORKFLOW_ENGINE_TTL_MS,
} from '../src/github/lifecycle.ts'
import { CHECK_NAME, COMMENT_MARKER } from '../src/github/render.ts'
import { checkShapeFor, GENERATION_STATES } from '../src/github/states.ts'
import {
  available,
  callProcedure,
  dropOrg,
  signInAs,
  startApi,
  type ApiHarness,
  type Org,
} from './harness.ts'

const hasDatabase = await available()
const SECRET = 'lifecycle-webhook-secret'

// ---------------------------------------------------------------------------
// A GitHub Actions identity, signed the way GitHub signs one.
//
// A real key pair rather than a stub verifier, because the thing being proved
// is that a token nobody could have minted is refused, and a stub that returns
// the claims it was handed cannot show that.
// ---------------------------------------------------------------------------

const identityKey = generateKeyPairSync('rsa', { modulusLength: 2048 })
const IDENTITY_KID = 'test-actions-key'

function jwks(): string {
  const jwk = identityKey.publicKey.export({ format: 'jwk' }) as Record<string, unknown>
  return JSON.stringify({ keys: [{ ...jwk, kid: IDENTITY_KID, use: 'sig', alg: 'RS256' }] })
}

function base64url(value: string): string {
  return Buffer.from(value, 'utf8').toString('base64url')
}

interface IdentityClaims {
  repository: string
  runId: number
  audience?: string
  issuer?: string
  expiresInSeconds?: number
  key?: KeyObject
  algorithm?: string
}

function identityToken(claims: IdentityClaims, now: Date): string {
  const header = { alg: claims.algorithm ?? 'RS256', typ: 'JWT', kid: IDENTITY_KID }
  const seconds = Math.floor(now.getTime() / 1000)
  const payload = {
    iss: claims.issuer ?? ACTIONS_ISSUER,
    aud: claims.audience ?? CALLBACK_AUDIENCE,
    iat: seconds - 10,
    exp: seconds + (claims.expiresInSeconds ?? 600),
    repository: claims.repository,
    repository_owner: claims.repository.split('/')[0],
    run_id: String(claims.runId),
    run_attempt: '1',
    ref: 'refs/pull/1/merge',
    event_name: 'pull_request',
    job_workflow_ref: `${claims.repository}/.github/workflows/antifailure.yml@refs/heads/main`,
    sha: 'e'.repeat(40),
  }
  const signingInput = `${base64url(JSON.stringify(header))}.${base64url(JSON.stringify(payload))}`
  const signature = createSign('RSA-SHA256')
    .update(signingInput)
    .sign(claims.key ?? identityKey.privateKey)
  return `${signingInput}.${signature.toString('base64url')}`
}

// ---------------------------------------------------------------------------

const sha = (seed: string): string =>
  createHmac('sha1', 'sha-seed').update(seed).digest('hex').padEnd(40, '0').slice(0, 40)

describe(
  'the pull request lifecycle',
  { skip: hasDatabase ? false : 'no Postgres at AF_TEST_DATABASE_URL' },
  () => {
    let h: ApiHarness
    let api: FakeRepositoryApi
    let org: Org
    let repository: string
    // Unique per run, like the slug below. installation_id is UNIQUE, and the
    // row only goes away when its organization does, so a constant here makes
    // the fixture seedable exactly once per database: any run killed before its
    // after hook leaves the row behind and every later run dies in before with
    // a 23505, which reads as a broken control plane rather than as a test that
    // tried to seed itself twice.
    const installationId =
      918_000_000 + Number(BigInt('0x' + randomUUID().slice(0, 8)) % 100_000_000n)
    // Unique to this PROCESS, not to this file. The delivery ledger is durable,
    // so a counter that restarts at one makes every delivery of a second run a
    // replay of the first run's, answered without the handler running. The
    // symptom is a test that passes on a fresh database and fails on a re-run,
    // reporting the state its predecessor left rather than anything about the
    // code, which is the least debuggable shape a test can have.
    const deliveryRun = randomUUID().slice(0, 8)
    let deliveries = 0

    before(async () => {
      api = new FakeRepositoryApi()
      // Everything the App is documented to hold TODAY, plus checks, which it
      // does not. Individual tests revoke one to prove the degraded path.
      api.grant('checks: write', 'pull requests: write', 'actions: write', 'actions: read')

      h = await startApi({
        githubWebhookSecret: SECRET,
        githubApi: api,
        // The key set shares the harness's clock, which is why it is handed
        // over as a function rather than as a built ActionsKeys: a token minted
        // against the fake clock and read against the wall clock is expired or
        // issued in the future depending on the day.
        actionsJwks: jwks,
      })

      // A run killed before its after hook leaves its organization behind, and
      // the sweeper tests below read every overdue row in the database rather
      // than only their own, because that is what the sweeper itself does. A
      // dead predecessor's rows therefore fail this suite for reasons that have
      // nothing to do with the code under test, and the failure lands on
      // whichever assertion the stale row reached first. Clear them here, and
      // only ones old enough to belong to a run that is certainly over, so a
      // second copy of this suite running beside this one is left alone.
      await h.admin`
        DELETE FROM organizations
        WHERE slug LIKE 'lifecycle-%' AND created_at < now() - interval '1 hour'`

      org = await seedInstalledOrg()
      repository = org.repository
    })

    after(async () => {
      await h.admin`DELETE FROM github_deliveries WHERE delivery_id LIKE ${'lifecycle-' + deliveryRun + '-%'}`
      await dropOrg(h.admin, org.orgId)
      await h.close()
    })

    async function seedInstalledOrg(): Promise<Org> {
      const slug = `lifecycle-${randomUUID().slice(0, 8)}`
      const [row] = await h.admin<{ id: string }[]>`
        INSERT INTO organizations (slug, name, github_login) VALUES (${slug}, 'lifecycle', ${slug})
        RETURNING id`
      const orgId = row!.id
      const full = `${slug}/app`
      const [repo] = await h.admin<{ id: string }[]>`
        INSERT INTO repositories (org_id, full_name) VALUES (${orgId}, ${full}) RETURNING id`
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${orgId}, ${installationId}, ${slug}, 'Organization')`
      return { orgId, slug, repoId: repo!.id, repository: full, envId: `env-${slug}` }
    }

    function sign(body: string): string {
      return 'sha256=' + createHmac('sha256', SECRET).update(body, 'utf8').digest('hex')
    }

    async function deliver(event: string, payload: unknown): Promise<Response> {
      const body = JSON.stringify(payload)
      return h.fetch('/webhooks/github', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          'x-github-event': event,
          'x-github-delivery': `lifecycle-${deliveryRun}-${(deliveries += 1)}`,
          'x-hub-signature-256': sign(body),
        },
        body,
      })
    }

    function pullRequestPayload(
      action: string,
      number: number,
      headSha: string,
      extra: Record<string, unknown> = {},
    ): Record<string, unknown> {
      const fork = extra.fork === true
      return {
        action,
        number,
        pull_request: {
          number,
          title: `pull request ${number}`,
          draft: extra.draft === true,
          state: extra.state ?? 'open',
          merged: extra.merged === true,
          head: {
            sha: headSha,
            ref: `feature-${number}`,
            repo: { full_name: fork ? 'somebody-else/app' : repository },
          },
          base: { ref: 'main', repo: { full_name: repository } },
        },
        repository: { full_name: repository, owner: { login: org.slug, type: 'Organization' } },
        organization: { login: org.slug },
        installation: { id: installationId },
        ...(extra.label ? { label: { name: extra.label }, sender: { login: 'maintainer' } } : {}),
      }
    }

    function workflowRunPayload(
      action: string,
      headSha: string,
      runId: number,
      conclusion: string | null = null,
    ): Record<string, unknown> {
      return {
        action,
        workflow_run: {
          id: runId,
          head_sha: headSha,
          status: action === 'completed' ? 'completed' : 'in_progress',
          conclusion,
        },
        repository: { full_name: repository, owner: { login: org.slug } },
        organization: { login: org.slug },
        installation: { id: installationId },
      }
    }

    async function generation(headSha: string) {
      const rows = await h.admin<
        {
          state: string
          detail: string | null
          check_run_id: string | null
          workflow_run_id: string | null
          env_id: string | null
          attempt: number
          reported_by: string | null
        }[]
      >`
        SELECT state::text AS state, detail, check_run_id::text AS check_run_id,
               workflow_run_id::text AS workflow_run_id, env_id, attempt, reported_by
        FROM pr_generations WHERE head_sha = ${headSha}`
      return rows[0] ?? null
    }

    function checkFor(headSha: string) {
      return api.checks.find((c) => c.headSha === headSha && c.name === CHECK_NAME)
    }

    function commentFor(number: number) {
      return api.issueComments.find(
        (c) => c.issueNumber === number && c.body.includes(COMMENT_MARKER),
      )
    }

    /** The credential a job would hold, through the endpoint a job calls. */
    async function callbackFor(headSha: string, runId: number): Promise<string | null> {
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken({ repository, runId }, h.clock.now())}`,
        },
        body: JSON.stringify({ head_sha: headSha }),
      })
      if (res.status !== 200) return null
      const body = (await res.json()) as { token?: string }
      return body.token ?? null
    }

    async function report(
      token: string,
      headSha: string,
      verdicts: string[],
      markdown = '<!-- antifailure:report -->\n### Antifailure: it ran\n',
    ): Promise<{ status: number; body: Record<string, unknown> }> {
      const res = await h.fetch('/v1/pr/report', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
        body: JSON.stringify({
          head_sha: headSha,
          markdown,
          report: {
            Environment: `env-${headSha.slice(0, 6)}`,
            URL: 'http://127.0.0.1:46001',
            Workflows: verdicts.map((v, i) => ({ Name: `workflow-${i}`, Verdict: v })),
          },
        }),
      })
      return { status: res.status, body: (await res.json()) as Record<string, unknown> }
    }

    // -----------------------------------------------------------------------
    // The state mapping, before any ordering
    // -----------------------------------------------------------------------

    it('maps all seven states, and blocked, unverified and cancelled are not passes', () => {
      const titles = new Set<string>()
      for (const state of GENERATION_STATES) {
        const shape = checkShapeFor(state)
        titles.add(shape.title)
        if (state === 'passed') {
          assert.equal(shape.conclusion, 'success')
          assert.equal(shape.passes, true)
        } else {
          assert.equal(
            shape.passes,
            false,
            `${state} passes a required check, which would let it merge behind a green tick`,
          )
        }
      }
      // Seven distinct titles. GitHub's conclusion vocabulary is smaller than
      // ours, so blocked and unverified share action_required, and the title is
      // where the two stay apart in the first line a person reads.
      assert.equal(titles.size, GENERATION_STATES.length, 'two states render the same title')

      // And the timeout, which is unverified with a conclusion of its own.
      const timedOut = checkShapeFor('unverified', true)
      assert.equal(timedOut.conclusion, 'timed_out')
      assert.equal(timedOut.passes, false)
      assert.notEqual(timedOut.title, checkShapeFor('unverified', false).title)
    })

    // -----------------------------------------------------------------------
    // ordering: open then workflow
    // -----------------------------------------------------------------------

    it('ordering: open then workflow', async () => {
      const head = sha('open-then-workflow')
      assert.equal((await deliver('pull_request', pullRequestPayload('opened', 11, head))).status, 200)

      const queued = await generation(head)
      assert.equal(queued?.state, 'queued')
      assert.equal(checkFor(head)?.status, 'queued')
      assert.ok(commentFor(11), 'a pull request with a queued check has no comment')
      assert.match(commentFor(11)!.body, new RegExp(`sha=${head}`))

      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5001))
      assert.equal((await generation(head))?.state, 'running')
      assert.equal(checkFor(head)?.status, 'in_progress')
      // Bound, because cancelling this run is the only route into the runtime.
      assert.equal((await generation(head))?.workflow_run_id, '5001')
    })

    // -----------------------------------------------------------------------
    // ordering: workflow then open
    // -----------------------------------------------------------------------

    it('ordering: workflow then open', async () => {
      // GitHub does not promise the pull request event lands before the run
      // event, and Actions is routinely faster. A run event for a commit with
      // no generation must not create one, must not throw, and must not be
      // treated as a failure: there is genuinely nothing to say yet.
      const head = sha('workflow-then-open')
      const early = await deliver('workflow_run', workflowRunPayload('in_progress', head, 5002))
      assert.equal(early.status, 200)
      assert.equal(await generation(head), null, 'a run event invented a generation')

      await deliver('pull_request', pullRequestPayload('opened', 12, head))
      assert.equal((await generation(head))?.state, 'queued')

      // And the run event that follows still binds, so nothing is lost by the
      // early one having arrived first.
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5002))
      const now = await generation(head)
      assert.equal(now?.state, 'running')
      assert.equal(now?.workflow_run_id, '5002')
    })

    // -----------------------------------------------------------------------
    // ordering: request then callback
    // -----------------------------------------------------------------------

    it('ordering: request then callback', async () => {
      const head = sha('request-then-callback')
      await deliver('pull_request', pullRequestPayload('opened', 13, head))
      api.addWorkflowRun({ id: 5003, repository, status: 'in_progress', conclusion: null, headSha: head })
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5003))

      const token = await callbackFor(head, 5003)
      assert.ok(token, 'no callback credential was issued for a running check')

      const answered = await report(token!, head, ['pass', 'pass'])
      assert.equal(answered.status, 200)
      assert.equal(answered.body.state, 'passed')

      const done = await generation(head)
      assert.equal(done?.state, 'passed')
      // Attributed to the WORKFLOW that reported, out of the identity token it
      // proved, rather than to anything the report said about itself.
      assert.match(done!.reported_by!, /antifailure\.yml@refs\/heads\/main attempt 1/)
      assert.equal(checkFor(head)?.conclusion, 'success')
      assert.equal(checkFor(head)?.status, 'completed')
      // The environment the report named, on the comment, as a console link
      // rather than as the runner's own address.
      assert.match(commentFor(13)!.body, /Environment \[env-/)
      assert.doesNotMatch(
        commentFor(13)!.body,
        /[^`]http:\/\/127\.0\.0\.1:46001/,
        'a loopback address reached the comment as a link',
      )
    })

    it('one report per credential, so a leaked one cannot rewrite a result', async () => {
      const head = sha('spent-credential')
      await deliver('pull_request', pullRequestPayload('opened', 14, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5004))
      const token = (await callbackFor(head, 5004))!

      assert.equal((await report(token, head, ['pass'])).status, 200)
      const second = await report(token, head, ['fail'])
      assert.equal(second.status, 409)
      assert.equal((await generation(head))?.state, 'passed')
    })

    // -----------------------------------------------------------------------
    // ordering: callback then request
    // -----------------------------------------------------------------------

    it('ordering: callback then request', async () => {
      // A job asking for a credential for a commit this control plane has never
      // heard of. It is refused with a sentence rather than served, because
      // issuing one would mean accepting a result for a check nobody asked for
      // and nobody is waiting on.
      const head = sha('callback-then-request')
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken({ repository, runId: 5005 }, h.clock.now())}`,
        },
        body: JSON.stringify({ head_sha: head }),
      })
      assert.equal(res.status, 409)
      assert.match((await res.text()), /no check is waiting/)

      // And once the pull request event lands, the same job's next attempt
      // works. A late-created row self-resolves rather than waiting for an
      // event that has already happened.
      await deliver('pull_request', pullRequestPayload('opened', 15, head))
      assert.ok(await callbackFor(head, 5005))
    })

    // -----------------------------------------------------------------------
    // The engine's own credential, minted from the same identity
    //
    // Until this existed the engine's control plane sink read
    // AF_CONTROL_PLANE_TOKEN, and nothing in any workflow this project ships
    // ever set it, so the sink was never built and a CI run reported no events
    // at all. These prove the exchange that replaced that variable, and they
    // prove it by using what it hands back rather than by inspecting it.
    // -----------------------------------------------------------------------

    /** The credential the engine would hold, through the endpoint it calls. */
    async function engineTokenFor(runId: number): Promise<Response> {
      return h.fetch('/v1/engine/token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken({ repository, runId }, h.clock.now())}`,
        },
        body: '{}',
      })
    }

    /** One event, sent the way the engine's sink sends a batch. */
    async function sendEvent(token: string, id: string): Promise<Response> {
      return h.fetch('/v1/events', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
        body: JSON.stringify({
          events: [{
            id,
            type: 'environment.ready',
            envId: 'env-minted',
            sequence: 1,
            occurredAt: h.clock.now().toISOString(),
          }],
        }),
      })
    }

    it('a workflow identity buys a credential the ingestion endpoint accepts', async () => {
      const res = await engineTokenFor(6001)
      assert.equal(res.status, 200)
      const body = (await res.json()) as { token?: string; expires_in?: number }
      assert.ok(body.token, 'no token came back')
      assert.equal(body.expires_in, Math.floor(WORKFLOW_ENGINE_TTL_MS / 1000))

      // The whole point, and the only assertion that proves it: the credential
      // works on the endpoint the sink actually posts to. A token that came
      // back but was refused here would be the dead path this change exists to
      // remove.
      const sent = await sendEvent(body.token!, 'ev-minted-1')
      assert.equal(sent.status, 202)
      const [row] = await h.admin`
        SELECT id FROM events
        WHERE idempotency_key = ${'ev-minted-1'} AND org_id = ${org.orgId}`
      assert.ok(row, 'the event never reached the database')
    })

    it('the credential expires, and the column that says so is enforced', async () => {
      const res = await engineTokenFor(6002)
      const { token } = (await res.json()) as { token: string }

      // Good now.
      assert.equal((await sendEvent(token, 'ev-before-expiry')).status, 202)

      // And not a moment past its life. expires_at existed on engine_tokens
      // since migration 0012 and nothing read it, which cost nothing while
      // every token was permanent and would have made "short lived" a comment
      // rather than a property the moment one was not.
      h.clock.advance(WORKFLOW_ENGINE_TTL_MS + 1000)
      const late = await sendEvent(token, 'ev-after-expiry')
      assert.equal(late.status, 401)
      const [row] = await h.admin`
        SELECT id FROM events WHERE idempotency_key = ${'ev-after-expiry'}`
      assert.equal(row, undefined, 'an expired credential still wrote an event')
    })

    it('a repository this control plane has never heard of gets no credential', async () => {
      const res = await h.fetch('/v1/engine/token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken(
            { repository: 'somebody-else/not-connected', runId: 6003 }, h.clock.now(),
          )}`,
        },
        body: '{}',
      })
      assert.equal(res.status, 409)
      assert.match(await res.text(), /not connected to this control plane/)
    })

    it('an identity minted for another audience buys nothing', async () => {
      // The default audience GitHub mints is the repository owner's URL, which
      // every workflow in the organization can obtain. Accepting one here would
      // let any workflow in the org report as this repository, so this route
      // has to check it and not merely trust that the caller asked correctly.
      const res = await h.fetch('/v1/engine/token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken(
            { repository, runId: 6004, audience: 'https://github.com/somebody' }, h.clock.now(),
          )}`,
        },
        body: '{}',
      })
      assert.equal(res.status, 401)
      assert.match(await res.text(), /wrong_audience/)
    })

    it('an identity nobody could have signed buys nothing', async () => {
      const impostor = generateKeyPairSync('rsa', { modulusLength: 2048 })
      const res = await h.fetch('/v1/engine/token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken(
            { repository, runId: 6005, key: impostor.privateKey }, h.clock.now(),
          )}`,
        },
        body: '{}',
      })
      assert.equal(res.status, 401)
    })

    it('no identity at all is told how to present one', async () => {
      const res = await h.fetch('/v1/engine/token', { method: 'POST', body: '{}' })
      assert.equal(res.status, 401)
      assert.match(await res.text(), /id-token: write/)
    })

    // -----------------------------------------------------------------------
    // ordering: engine event before callback
    // -----------------------------------------------------------------------

    it('ordering: engine event before callback', async () => {
      // The engine reports the environment over /v1/events while the job is
      // still running, and the report follows. Neither may lose the other: the
      // environment has to survive the report, and the report has to survive
      // the environment already being known.
      const head = sha('engine-before-callback')
      await deliver('pull_request', pullRequestPayload('opened', 16, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5006))

      const envId = `env-${head.slice(0, 6)}`
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state, pull_request)
        VALUES (${org.orgId}, ${org.repoId}, ${envId}, ${'feature-16'}, 'running', 16)`

      const token = (await callbackFor(head, 5006))!
      assert.equal((await report(token, head, ['pass'])).status, 200)

      const done = await generation(head)
      assert.equal(done?.env_id, envId, 'the report did not carry the environment through')
      assert.match(commentFor(16)!.body, new RegExp(envId))
    })

    // -----------------------------------------------------------------------
    // ordering: synchronize during an old run
    // -----------------------------------------------------------------------

    it('ordering: synchronize during an old run', async () => {
      const first = sha('sync-first')
      const second = sha('sync-second')
      await deliver('pull_request', pullRequestPayload('opened', 17, first))
      api.addWorkflowRun({
        id: 5007,
        repository,
        status: 'in_progress',
        conclusion: null,
        headSha: first,
      })
      await deliver('workflow_run', workflowRunPayload('in_progress', first, 5007))
      const oldToken = (await callbackFor(first, 5007))!

      // The push.
      await deliver('pull_request', pullRequestPayload('synchronize', 17, second))

      const superseded = await generation(first)
      assert.equal(superseded?.state, 'cancelled')
      assert.equal((await generation(second))?.state, 'queued')

      // The old commit's own check says cancelled, which is right: that check
      // belongs to that commit.
      assert.equal(checkFor(first)?.conclusion, 'cancelled')
      assert.equal(checkFor(second)?.status, 'queued')
      // Two commits, two check runs, and neither was moved onto the other.
      assert.notEqual(checkFor(first)!.id, checkFor(second)!.id)

      // THE COMMENT REPORTS THE HEAD. This is the compare-and-set: the
      // superseded generation published its own check and did not touch the
      // comment.
      assert.match(commentFor(17)!.body, new RegExp(`sha=${second}`))

      // And the old job's credential is dead, so a run finishing after its
      // replacement cannot report a result for a commit nobody is waiting on.
      const late = await report(oldToken, first, ['pass'])
      assert.equal(late.status, 409)
      assert.match(commentFor(17)!.body, new RegExp(`sha=${second}`))
    })

    it('a result for an older commit never becomes the comment', async () => {
      // The same property from the other direction, and this is the one that
      // would be silently wrong: the old run is allowed to FINISH, and what it
      // may not do is overwrite the newer answer.
      const first = sha('stale-first')
      const second = sha('stale-second')
      await deliver('pull_request', pullRequestPayload('opened', 18, first))
      await deliver('workflow_run', workflowRunPayload('in_progress', first, 5008))
      await deliver('pull_request', pullRequestPayload('synchronize', 18, second))

      const bodyBefore = commentFor(18)!.body
      // The old run finishes, badly.
      await deliver('workflow_run', workflowRunPayload('completed', first, 5008, 'failure'))

      assert.equal(commentFor(18)!.body, bodyBefore, 'an older commit rewrote the comment')
      // Its own check is still updated, because that check is about that commit.
      assert.equal(checkFor(first)?.status, 'completed')
    })

    it('a comment with no commit in it is still not overwritten by an old run', async () => {
      // The case the SHA in the comment body cannot cover, and it is a real
      // one: a comment written by an older build of this control plane, or by
      // the workflow's own step, carries no fence to read. So the writer
      // compares its own head against the pull request BEFORE it looks at
      // GitHub, and that comparison is the only thing standing between an old
      // run and a comment it should not touch.
      const first = sha('unfenced-first')
      const second = sha('unfenced-second')
      await deliver('pull_request', pullRequestPayload('opened', 33, first))
      await deliver('workflow_run', workflowRunPayload('in_progress', first, 5021))
      await deliver('pull_request', pullRequestPayload('synchronize', 33, second))

      // Replace the comment with one carrying the marker and no commit, the
      // way an older writer would have left it.
      const existing = commentFor(33)!
      await api.updateComment(installationId, repository, existing.id, `${COMMENT_MARKER} -->\nolder`)
      const before = commentFor(33)!.body

      await deliver('workflow_run', workflowRunPayload('completed', first, 5021, 'failure'))
      assert.equal(commentFor(33)!.body, before, 'an old run overwrote a comment it could not read')
    })

    // -----------------------------------------------------------------------
    // ordering: close before ready, and close during a run
    // -----------------------------------------------------------------------

    it('ordering: close before ready', async () => {
      const head = sha('close-before-ready')
      await deliver('pull_request', pullRequestPayload('opened', 19, head))
      // Closed before any workflow run event arrived, so there is nothing to
      // cancel and nothing to tear down. What must NOT happen is a teardown
      // request nothing can act on, which would read as a leak forever.
      await deliver(
        'pull_request',
        pullRequestPayload('closed', 19, head, { state: 'closed' }),
      )
      assert.equal((await generation(head))?.state, 'cancelled')
      const requests = await h.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM teardown_requests WHERE org_id = ${org.orgId}
          AND generation_id = (SELECT id FROM pr_generations WHERE head_sha = ${head})`
      assert.equal(requests[0]!.n, 0, 'a teardown was asked for with nothing to reach')
    })

    it('ordering: close during a run, and the teardown reaches the runtime', async () => {
      const head = sha('close-during-run')
      await deliver('pull_request', pullRequestPayload('opened', 20, head))
      api.addWorkflowRun({
        id: 5009,
        repository,
        status: 'in_progress',
        conclusion: null,
        headSha: head,
      })
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5009))

      await deliver('pull_request', pullRequestPayload('closed', 20, head, { state: 'closed' }))
      assert.equal((await generation(head))?.state, 'cancelled')

      const pending = await h.admin<{ state: string }[]>`
        SELECT state FROM teardown_requests WHERE workflow_run_id = 5009`
      assert.equal(pending[0]?.state, 'pending')

      // THE SWEEP IS WHAT REACHES THE RUNTIME. A cancel that was accepted is
      // not an acknowledgement: GitHub records the request and the run stops
      // some time later, so a pass that acknowledged here would be marking a
      // row and calling it cleanup.
      //
      // Asserted on THIS request rather than on the sweep's totals. The sweep
      // works through everything that is due, including requests other tests in
      // this file left behind, so a total is a number about the file rather
      // than about this ordering.
      await sweepTeardowns(lifecycle())
      const afterFirst = await h.admin<{ state: string; last_error: string | null }[]>`
        SELECT state, last_error FROM teardown_requests WHERE workflow_run_id = 5009`
      assert.equal(afterFirst[0]?.state, 'pending', 'a live run was reported as cleaned up')
      assert.match(afterFirst[0]!.last_error!, /terminal state/)
      assert.equal(api.workflowRunById(5009)?.cancelRequests, 1, 'nothing asked GitHub to stop')

      // The run reaches a terminal state, which is the acknowledgement.
      api.finishWorkflowRun(5009, 'cancelled')
      h.clock.advance(TEARDOWN_LEASE_MS + 1000)
      await sweepTeardowns(lifecycle())
      const done = await h.admin<{ state: string; acknowledged_at: Date | null }[]>`
        SELECT state, acknowledged_at FROM teardown_requests WHERE workflow_run_id = 5009`
      assert.equal(done[0]?.state, 'acknowledged')
      assert.ok(done[0]?.acknowledged_at)
    })

    // -----------------------------------------------------------------------
    // ordering: reopen during teardown
    // -----------------------------------------------------------------------

    it('ordering: reopen during teardown', async () => {
      const head = sha('reopen-during-teardown')
      await deliver('pull_request', pullRequestPayload('opened', 21, head))
      api.addWorkflowRun({
        id: 5010,
        repository,
        status: 'in_progress',
        conclusion: null,
        headSha: head,
      })
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5010))
      await deliver('pull_request', pullRequestPayload('closed', 21, head, { state: 'closed' }))

      // Reopened while the teardown is still pending. GitHub restarts the
      // workflow on a reopen, so the check has to be waiting for that run
      // rather than stuck on the cancellation of the last one: a check that
      // stayed cancelled would contradict the run that is about to report.
      await deliver('pull_request', pullRequestPayload('reopened', 21, head))
      const now = await generation(head)
      // One row per head, so the reopen reuses it rather than putting a second
      // check on one commit.
      const rows = await h.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM pr_generations WHERE head_sha = ${head}`
      assert.equal(rows[0]!.n, 1, 'reopening created a second generation for one commit')
      assert.equal(now?.state, 'queued', 'a reopened pull request left its check cancelled')
      assert.equal(now?.detail, null)

      // And the teardown that was already asked for is still asked for. A
      // reopen does not un-request cleanup of a run that was already stopped.
      const requests = await h.admin<{ state: string }[]>`
        SELECT state FROM teardown_requests WHERE workflow_run_id = 5010`
      assert.ok(['pending', 'leased', 'acknowledged'].includes(requests[0]!.state))
    })

    // -----------------------------------------------------------------------
    // ordering: missing callback
    // -----------------------------------------------------------------------

    it('ordering: missing callback, on a run GitHub calls successful', async () => {
      // THE DEFECT THIS WHOLE FEATURE EXISTS DOWNSTREAM OF. A green workflow
      // run means the job exited zero, and `af ci` exits zero on a run that
      // verified nothing. Pull request 49 demonstrated six blocked workflows
      // inside a successful job.
      const head = sha('missing-callback')
      await deliver('pull_request', pullRequestPayload('opened', 22, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5011))
      await deliver('workflow_run', workflowRunPayload('completed', head, 5011, 'success'))

      const done = await generation(head)
      assert.equal(done?.state, 'unverified', 'a green job with no report was read as a pass')
      assert.equal(checkFor(head)?.conclusion, 'action_required')
      assert.notEqual(checkFor(head)?.conclusion, 'success')
      assert.match(commentFor(22)!.body, /nothing was verified/i)
    })

    it('a run that failed before reporting is blocked, not failed', async () => {
      // Blocked and failed are different claims. A job that died before the
      // check ran has found nothing about the change, and reporting it as a
      // failure of the change blames the author for our own gap.
      const head = sha('run-failed-early')
      await deliver('pull_request', pullRequestPayload('opened', 23, head))
      await deliver('workflow_run', workflowRunPayload('completed', head, 5012, 'failure'))
      assert.equal((await generation(head))?.state, 'blocked')
      assert.equal(checkFor(head)?.conclusion, 'action_required')
    })

    it('what GitHub says about the job does not overwrite what the job said about the code', async () => {
      const head = sha('report-then-run-event')
      await deliver('pull_request', pullRequestPayload('opened', 24, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5013))
      const token = (await callbackFor(head, 5013))!
      assert.equal((await report(token, head, ['fail'])).status, 200)
      assert.equal((await generation(head))?.state, 'failed')

      // The run then ends successfully, which it can: `af ci` exits zero on
      // some verdicts. The recorded failure stands.
      await deliver('workflow_run', workflowRunPayload('completed', head, 5013, 'success'))
      assert.equal((await generation(head))?.state, 'failed')
      assert.equal(checkFor(head)?.conclusion, 'failure')
    })

    // -----------------------------------------------------------------------
    // The Re-run button, in both of its shapes
    // -----------------------------------------------------------------------

    for (const shape of ['check_run', 'check_suite'] as const) {
      it(`${shape} rerequested queues another attempt on the same commit`, async () => {
        // GitHub has two Re-run buttons and they send different events: one on
        // a single check, one on the checks page for all of them. Handling only
        // the first leaves the one most people press doing nothing at all, with
        // no error anywhere.
        const head = sha(`rerun-${shape}`)
        const number = shape === 'check_run' ? 40 : 41
        const runId = shape === 'check_run' ? 5030 : 5031
        await deliver('pull_request', pullRequestPayload('opened', number, head))
        api.addWorkflowRun({ id: runId, repository, status: 'in_progress', conclusion: null, headSha: head })
        await deliver('workflow_run', workflowRunPayload('in_progress', head, runId))
        await deliver('workflow_run', workflowRunPayload('completed', head, runId, 'failure'))
        assert.equal((await generation(head))?.state, 'blocked')
        assert.equal((await generation(head))?.attempt, 1)
        // GitHub's own view of the run, which is what a re-run acts on. The
        // delivery above says the run ended; this is the run having ended.
        api.finishWorkflowRun(runId, 'failure')

        await deliver(shape, {
          action: 'rerequested',
          [shape]: { id: 900, head_sha: head },
          repository: { full_name: repository, owner: { login: org.slug } },
          organization: { login: org.slug },
          installation: { id: installationId },
        })

        const again = await generation(head)
        assert.equal(again?.state, 'queued')
        assert.equal(again?.attempt, 2)
        assert.equal(again?.detail, null)
        // Re-running the RUN, not dispatching the workflow: a dispatch names a
        // ref and a ref moves, so somebody pressing Re-run on an older commit
        // would get a run against whatever the branch points at now.
        assert.equal(api.workflowRunById(runId)?.reruns, 1)
      })
    }

    // -----------------------------------------------------------------------
    // ordering: timeout
    // -----------------------------------------------------------------------

    it('ordering: timeout, and the check says so rather than spinning', async () => {
      const head = sha('timeout')
      await deliver('pull_request', pullRequestPayload('opened', 25, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5014))
      assert.equal((await generation(head))?.state, 'running')

      // Nothing happens for longer than the deadline.
      h.clock.advance(DEFAULT_DEADLINE_MS + 60_000)
      const swept = await sweepGenerations(lifecycle())
      assert.ok(swept.timedOut >= 1)

      assert.equal((await generation(head))?.state, 'unverified')
      // The sentence, not merely the state. A timeout and a run that reported
      // `unverified` itself are the same state and different conclusions, and
      // the recorded detail is the only thing that separates them, so a
      // reworded sentence that quietly stopped matching would silently collapse
      // the two.
      assert.equal((await generation(head))?.detail, TIMED_OUT_DETAIL)
      // timed_out rather than action_required, because a reader should know
      // nothing came back at all rather than that something needs approving.
      assert.equal(checkFor(head)?.conclusion, 'timed_out')

      // And the credential is gone, so a job that wakes up an hour later
      // cannot report a result for a check that already gave up.
      const late = await h.fetch('/v1/pr/report', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: 'Bearer whatever' },
        body: JSON.stringify({ head_sha: head, markdown: '', report: {} }),
      })
      assert.equal(late.status, 409)
    })

    // -----------------------------------------------------------------------
    // ordering: fork approval followed by a new sha
    // -----------------------------------------------------------------------

    it('ordering: a fork is blocked until a maintainer approves that exact commit', async () => {
      const head = sha('fork-first')
      await deliver('pull_request', pullRequestPayload('opened', 26, head, { fork: true }))

      const blocked = await generation(head)
      assert.equal(blocked?.state, 'blocked')
      assert.match(blocked!.detail!, new RegExp(FORK_APPROVAL_LABEL))
      assert.equal(checkFor(head)?.conclusion, 'action_required')

      // And no credential is issued, so a fork's job cannot report even if the
      // customer's workflow somehow ran with an identity.
      assert.equal(await callbackFor(head, 5015), null)

      await deliver(
        'pull_request',
        pullRequestPayload('labeled', 26, head, { fork: true, label: FORK_APPROVAL_LABEL }),
      )
      assert.equal((await generation(head))?.state, 'queued')
      assert.ok(await callbackFor(head, 5015), 'an approved fork commit was still refused')
    })

    it('ordering: fork approval followed by a new sha withdraws the approval', async () => {
      const first = sha('fork-approved')
      const second = sha('fork-pushed')
      await deliver('pull_request', pullRequestPayload('opened', 27, first, { fork: true }))
      await deliver(
        'pull_request',
        pullRequestPayload('labeled', 27, first, { fork: true, label: FORK_APPROVAL_LABEL }),
      )
      assert.equal((await generation(first))?.state, 'queued')

      // The push. A maintainer approved code they read; this is code nobody
      // read, and carrying the approval forward is the whole attack.
      await deliver('pull_request', pullRequestPayload('synchronize', 27, second, { fork: true }))

      const approved = await h.admin<{ approved_sha: string | null }[]>`
        SELECT approved_sha FROM pull_requests WHERE number = 27 AND repository_id = ${org.repoId}`
      assert.equal(approved[0]?.approved_sha, null, 'the approval survived a push')
      assert.equal((await generation(second))?.state, 'blocked')
      assert.equal(await callbackFor(second, 5016), null)
    })

    // -----------------------------------------------------------------------
    // Entry points other than a delivery
    // -----------------------------------------------------------------------

    it('the console teardown verb writes a request rather than marking the row', async () => {
      // The defect: it used to set state = torn_down and return, with a comment
      // saying the engine reads this and does the removing. Nothing read it.
      const envId = `env-console-${randomUUID().slice(0, 6)}`
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${org.repoId}, ${envId}, 'main', 'running')`
      const owner = await signInAs(h, org, 'owner', 'teardown')

      const answered = await callProcedure(h, owner, 'environments.teardown', 'mutation', { envId })
      assert.equal(answered.status, 200)

      const state = await h.admin<{ state: string }[]>`
        SELECT state::text AS state FROM environments WHERE env_id = ${envId}`
      assert.equal(state[0]?.state, 'running', 'the row was marked torn down before anything was')

      const request = await h.admin<{ state: string }[]>`
        SELECT state FROM teardown_requests WHERE env_id = ${envId}`
      assert.equal(request[0]?.state, 'pending')
    })

    it('a teardown with no route to the runtime is given up on and says so', async () => {
      // Honest rather than optimistic. An environment with no workflow run
      // holding it is one this control plane has no route to: it holds no
      // cluster credential and no address, by design. Reporting it torn down
      // would be the same lie in a different place.
      const envId = `env-unreachable-${randomUUID().slice(0, 6)}`
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${org.repoId}, ${envId}, 'main', 'running')`
      await h.admin`
        INSERT INTO teardown_requests (org_id, environment_id, env_id, repository_id, reason)
        SELECT ${org.orgId}, id, ${envId}, ${org.repoId}, 'no route'
        FROM environments WHERE env_id = ${envId}`

      for (let attempt = 0; attempt <= TEARDOWN_ATTEMPTS; attempt += 1) {
        await sweepTeardowns(lifecycle())
        h.clock.advance(TEARDOWN_LEASE_MS + 1000)
      }
      const row = await h.admin<{ state: string; last_error: string | null }[]>`
        SELECT state, last_error FROM teardown_requests WHERE env_id = ${envId}`
      assert.equal(row[0]?.state, 'abandoned')
      assert.match(row[0]!.last_error!, /af down/)

      // And the environment is NOT marked torn down, because it was not.
      const env = await h.admin<{ state: string }[]>`
        SELECT state::text AS state FROM environments WHERE env_id = ${envId}`
      assert.equal(env[0]?.state, 'running')
    })

    it('an engine that reported the teardown itself is acknowledgement enough', async () => {
      const envId = `env-engine-said-${randomUUID().slice(0, 6)}`
      await h.admin`
        INSERT INTO environments (org_id, repository_id, env_id, branch, state)
        VALUES (${org.orgId}, ${org.repoId}, ${envId}, 'main', 'torn_down')`
      await h.admin`
        INSERT INTO teardown_requests (org_id, env_id, repository_id, workflow_run_id, reason)
        VALUES (${org.orgId}, ${envId}, ${org.repoId}, 9999, 'engine said so')`

      const swept = await sweepTeardowns(lifecycle())
      assert.equal(swept.acknowledged, 1)
      // Nothing was asked of GitHub, because the environment was already gone
      // and cancelling a run that had finished cleanly would be work for
      // nothing.
      assert.equal(api.workflowRunById(9999), undefined)
    })

    // -----------------------------------------------------------------------
    // The permission that is not granted yet
    // -----------------------------------------------------------------------

    it('with no checks permission, the comment still lands and says which grant is missing', async () => {
      api.revoke('checks: write')
      try {
        const head = sha('no-checks-permission')
        const res = await deliver('pull_request', pullRequestPayload('opened', 28, head))
        assert.equal(res.status, 200, 'a missing permission failed the delivery')
        assert.equal(checkFor(head), undefined)

        const comment = commentFor(28)
        assert.ok(comment, 'nothing was published at all')
        assert.match(comment!.body, /There is no check run for this commit/)
        assert.match(comment!.body, /Accept new permissions/)
      } finally {
        api.grant('checks: write')
      }
    })

    // -----------------------------------------------------------------------
    // The identity a job proves
    // -----------------------------------------------------------------------

    it('a token this control plane did not verify buys nothing', async () => {
      const head = sha('forged-identity')
      await deliver('pull_request', pullRequestPayload('opened', 29, head))

      const other = generateKeyPairSync('rsa', { modulusLength: 2048 })
      const forged = identityToken(
        { repository, runId: 5017, key: other.privateKey },
        h.clock.now(),
      )
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: { 'content-type': 'application/json', authorization: `Bearer ${forged}` },
        body: JSON.stringify({ head_sha: head }),
      })
      assert.equal(res.status, 401)
      assert.match(await res.text(), /signature/i)
    })

    it('an unsigned token is refused before any claim is read', async () => {
      const head = sha('alg-none')
      await deliver('pull_request', pullRequestPayload('opened', 30, head))
      const header = base64url(JSON.stringify({ alg: 'none', typ: 'JWT' }))
      const payload = base64url(
        JSON.stringify({
          iss: ACTIONS_ISSUER,
          aud: CALLBACK_AUDIENCE,
          exp: Math.floor(h.clock.now().getTime() / 1000) + 600,
          repository,
          run_id: '5018',
        }),
      )
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${header}.${payload}.`,
        },
        body: JSON.stringify({ head_sha: head }),
      })
      assert.equal(res.status, 401)
      assert.match(await res.text(), /RS256/)
    })

    it('a token minted for a different audience is refused', async () => {
      const head = sha('wrong-audience')
      await deliver('pull_request', pullRequestPayload('opened', 31, head))
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken(
            { repository, runId: 5019, audience: 'https://github.com/somebody' },
            h.clock.now(),
          )}`,
        },
        body: JSON.stringify({ head_sha: head }),
      })
      assert.equal(res.status, 401)
      assert.match(await res.text(), /issued for/)
    })

    it('a token for another repository cannot report on this one', async () => {
      const head = sha('other-repository')
      await deliver('pull_request', pullRequestPayload('opened', 32, head))
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken(
            { repository: 'somebody-else/app', runId: 5020 },
            h.clock.now(),
          )}`,
        },
        body: JSON.stringify({ head_sha: head }),
      })
      // The repository comes from the signed token and not from the body, so
      // this looks up somebody else's repository and finds nothing.
      assert.equal(res.status, 409)
    })

    // -----------------------------------------------------------------------
    // A suspended organization
    //
    // The suspension was read at /v1/events and nowhere else, so a stopped
    // organization was issued a working credential and refused only when it
    // tried to use it. Nothing crossed a tenant boundary and nothing could be
    // ingested, so this is noise rather than a breach, but it is the expensive
    // kind of noise: the customer is shown a failure on the reporting path when
    // the answer is their billing state, and that is where they go looking.
    // -----------------------------------------------------------------------

    it('a suspended organization is refused the credential rather than the report', async () => {
      const head = sha('suspended-org')
      await deliver('pull_request', pullRequestPayload('opened', 33, head))

      await h.admin`
        UPDATE organizations
        SET suspended_at = now(), suspended_reason = 'unpaid invoice'
        WHERE id = ${org.orgId}`
      try {
        const res = await h.fetch('/v1/pr/callback-token', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            authorization: `Bearer ${identityToken({ repository, runId: 5021 }, h.clock.now())}`,
          },
          body: JSON.stringify({ head_sha: head }),
        })
        assert.equal(res.status, 409)
        const body = (await res.json()) as { error: string }
        // The message names the suspension AND its reason. "409" on its own
        // sends the reader back to the check, which is where this defect used
        // to send them.
        assert.match(body.error, /suspended/i)
        assert.match(body.error, /unpaid invoice/)
        assert.doesNotMatch(body.error, /no check is waiting/)

        // Nothing was minted. The refusal has to happen BEFORE the write, not
        // beside it: a row carrying a callback hash is a credential that exists.
        const [row] = await h.admin<{ callback_hash: Buffer | null; state: string }[]>`
          SELECT g.callback_hash, g.state::text AS state
          FROM pr_generations g JOIN pull_requests p ON p.id = g.pull_request_id
          WHERE p.repository_id = ${org.repoId}::uuid AND g.head_sha = ${head}`
        assert.ok(row, 'the generation the delivery created is missing')
        assert.equal(row!.callback_hash, null)
        assert.equal(row!.state, 'queued')
      } finally {
        await h.admin`
          UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
          WHERE id = ${org.orgId}`
      }

      // And the same call succeeds once the suspension lifts, which is what
      // separates this from a check that refuses everybody.
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken({ repository, runId: 5022 }, h.clock.now())}`,
        },
        body: JSON.stringify({ head_sha: head }),
      })
      assert.equal(res.status, 200)
      const issued = (await res.json()) as { token: string }
      assert.ok(issued.token)
    })

    it('the suspension is read where the tenant is known, not where the account is', async () => {
      // The same organization, reached through a SECOND installation whose
      // account login is not the one on the organization row. An organization
      // can hold more than one installation, which is why installationFor looks
      // an installation up by owner rather than taking the organization's
      // first.
      //
      // This is the case that separates a suspension read under the tenant from
      // one read on the GitHub account connection. On that connection the
      // organizations table is reachable only through the policy matching
      // organizations.github_login against the account, so from here the row is
      // invisible, and a check written there reads zero rows and lets the mint
      // through. The refusal below is the correct scope working; a mint, or a
      // refusal that talks about a missing check, is the wrong one.
      const second = `second-${randomUUID().slice(0, 8)}`
      const full = `${second}/app`
      await h.admin`
        INSERT INTO github_installations (org_id, installation_id, account_login, account_type)
        VALUES (${org.orgId}, ${installationId + 1}, ${second}, 'Organization')`
      await h.admin`INSERT INTO repositories (org_id, full_name) VALUES (${org.orgId}, ${full})`
      await h.admin`
        UPDATE organizations
        SET suspended_at = now(), suspended_reason = 'an incident'
        WHERE id = ${org.orgId}`
      try {
        const res = await h.fetch('/v1/pr/callback-token', {
          method: 'POST',
          headers: {
            'content-type': 'application/json',
            authorization: `Bearer ${identityToken(
              { repository: full, runId: 5023 },
              h.clock.now(),
            )}`,
          },
          body: JSON.stringify({ head_sha: sha('second-account') }),
        })
        assert.equal(res.status, 409)
        const body = (await res.json()) as { error: string }
        assert.match(body.error, /suspended/i)
        assert.match(body.error, /an incident/)
        // The tell of the wrong scope. With no generation on this commit, an
        // implementation whose suspension read came back empty falls straight
        // through to the generation lookup and answers with this instead.
        assert.doesNotMatch(body.error, /no check is waiting/)
      } finally {
        await h.admin`
          UPDATE organizations SET suspended_at = NULL, suspended_reason = NULL
          WHERE id = ${org.orgId}`
        await h.admin`DELETE FROM repositories WHERE full_name = ${full}`
        await h.admin`
          DELETE FROM github_installations WHERE installation_id = ${installationId + 1}`
      }
    })

    function lifecycle() {
      return {
        pool: h.pool,
        clock: h.clock,
        api,
        consoleBase: 'http://app.test',
      }
    }
  },
)
