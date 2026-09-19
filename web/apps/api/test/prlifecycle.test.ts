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
//   unclaimed workflow          the customer's own Antifailure workflow
//                               finished and no run ever claimed the commit
//   unclaimed then callback     a job claims after that check concluded
//   claim then workflow         the ordinary one, said the other way round
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
  unclaimedDetail,
  WORKFLOW_ENGINE_TTL_MS,
} from '../src/github/lifecycle.ts'
import { CONTROL_PLANE_VARIABLE, SETUP_DOCS_URL, WORKFLOW_PATH } from '../src/github/setup.ts'
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
  /** Which attempt of that run. Re-running a workflow run from the Actions tab
   *  keeps the run id and increments this, and it is the only thing in the
   *  token that tells a re-run apart from the attempt it replaces. */
  runAttempt?: number
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
    run_attempt: String(claims.runAttempt ?? 1),
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
      /** Which workflow, the way GitHub says it, and which attempt of the run.
       *  Absent means a run this suite has no opinion about, which is what
       *  every test before the unclaimed ones sends. GitHub carries
       *  `run_attempt` on every workflow run, 1 on the first and higher on each
       *  re-run of it, and a delivery that omitted it is read as the first. */
      workflow: { name?: string; path?: string; event?: string; runAttempt?: number } = {},
    ): Record<string, unknown> {
      const { runAttempt, ...rest } = workflow
      return {
        action,
        workflow_run: {
          id: runId,
          head_sha: headSha,
          status: action === 'completed' ? 'completed' : 'in_progress',
          conclusion,
          ...(runAttempt === undefined ? {} : { run_attempt: runAttempt }),
          ...rest,
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
          verdict: unknown
          finished_at: string | null
          deadline_at: string
        }[]
      >`
        SELECT state::text AS state, detail, check_run_id::text AS check_run_id,
               workflow_run_id::text AS workflow_run_id, env_id, attempt, reported_by,
               verdict, finished_at::text AS finished_at, deadline_at::text AS deadline_at
        FROM pr_generations WHERE head_sha = ${headSha}`
      return rows[0] ?? null
    }

    /** Every check run of this name on the commit, oldest first.
     *
     *  More than one is the ordinary case after a re-run: a completed check run
     *  cannot be moved back to in_progress at GitHub, so another attempt is
     *  another check run. */
    function checksFor(headSha: string) {
      return api.checks.filter((c) => c.headSha === headSha && c.name === CHECK_NAME)
    }

    /** The one a person sees, which is the most recent of them. GitHub shows
     *  the latest check run of a name and a required rule reads that one, so a
     *  test that read the first would be reading the attempt nobody is looking
     *  at. */
    function checkFor(headSha: string) {
      const runs = checksFor(headSha)
      return runs[runs.length - 1]
    }

    function commentFor(number: number) {
      return api.issueComments.find(
        (c) => c.issueNumber === number && c.body.includes(COMMENT_MARKER),
      )
    }

    /** The exchange a job makes, with the whole answer it gets back.
     *
     *  The status and the sentence, not only whether a token came back. The
     *  defect this suite grew for is a refusal the caller could not tell from a
     *  fork: the workflow read `.token`, found nothing, and could not say
     *  whether it had been turned away or never asked. A helper that returns
     *  null for every non-200 cannot see that either. */
    async function claim(
      headSha: string,
      runId: number,
      runAttempt = 1,
    ): Promise<{ status: number; token: string | null; error: string | null }> {
      const res = await h.fetch('/v1/pr/callback-token', {
        method: 'POST',
        headers: {
          'content-type': 'application/json',
          authorization: `Bearer ${identityToken({ repository, runId, runAttempt }, h.clock.now())}`,
        },
        body: JSON.stringify({ head_sha: headSha }),
      })
      const body = (await res.json()) as { token?: string; error?: string }
      return { status: res.status, token: body.token ?? null, error: body.error ?? null }
    }

    /** The credential a job would hold, through the endpoint a job calls. */
    async function callbackFor(headSha: string, runId: number): Promise<string | null> {
      return (await claim(headSha, runId)).token
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
      // The Details link has to open something the runs page can select on.
      // It carried only the commit, which that page never reads, so every
      // click from GitHub landed on the generic list.
      assert.equal(checkFor(head)?.detailsUrl, `http://app.test/runs?pr=11&commit=${head}`)
      assert.ok(commentFor(11), 'a pull request with a queued check has no comment')
      assert.match(commentFor(11)!.body, new RegExp(`sha=${head}`))

      // A run event alone moves nothing, and that is the fix for the defect
      // that made this repository's own check say "Nothing was verified" on
      // every pull request. Seventeen workflows run on a commit here and the
      // App is delivered an event for all of them, so a handler that acts on
      // the first one it sees is a handler acting on a stranger.
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5001))
      assert.equal((await generation(head))?.state, 'queued')

      // The run that is the check says so, by trading a workflow identity
      // GitHub signed for a credential good for this commit. `run_id` in that
      // identity is GitHub's own, so the binding cannot be claimed by a job
      // that is not the job it says it is.
      assert.ok(await callbackFor(head, 5001), 'a running check was issued no credential')
      assert.equal((await generation(head))?.state, 'running')
      assert.equal(checkFor(head)?.status, 'in_progress')
      // Bound, because cancelling this run is the only route into the runtime.
      assert.equal((await generation(head))?.workflow_run_id, '5001')

      // And from here its own run events are heard, because it is now the run.
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5001))
      assert.equal((await generation(head))?.state, 'running')
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

      // And nothing is lost by the early one having arrived first: the run
      // introduces itself when it reaches the step that does so, and binds
      // then. It is the claim rather than the run event that binds, because
      // the claim is the only one of the two that proves which run it is.
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5002))
      assert.equal((await generation(head))?.state, 'queued')

      assert.ok(await callbackFor(head, 5002))
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

    // The report the console shows a person is this one, read back whole. The
    // callback stores it and nothing else does, so a run detail that only read
    // the runs and verdicts tables would show a pull request that reported
    // everything as a pull request that reported nothing.
    it('runs.report reads the whole stored report back for a pull request', async () => {
      const head = sha('report-readback')
      await deliver('pull_request', pullRequestPayload('opened', 78, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5078))
      const token = (await callbackFor(head, 5078))!
      const md =
        '<!-- antifailure:report -->\n### Antifailure: a check failed\n\n' +
        '| Workflow | Result |\n| --- | --- |\n| `read-own-orders` | FAILED |\n'
      assert.equal((await report(token, head, ['pass', 'fail'], md)).status, 200)

      const member = await signInAs(h, org, 'member')
      const unwrap = <T,>(res: { body: unknown }): T =>
        (res.body as { result: { data: T } }).result.data
      const body = unwrap<{
        headSha: string
        markdown: string
        counts: { passed: number; failed: number }
        environment: string | null
      }>(await callProcedure(h, member, 'runs.report', 'query', { pr: 78 }))
      assert.equal(body.headSha, head)
      assert.equal(body.markdown, md, 'the stored report did not come back whole')
      assert.equal(body.counts.passed, 1)
      assert.equal(body.counts.failed, 1)
      assert.equal(body.environment, `env-${head.slice(0, 6)}`)

      // A pull request nobody reported reads back as null rather than as the
      // newest other pull request's report.
      const none = unwrap<unknown>(
        await callProcedure(h, member, 'runs.report', 'query', { pr: 99999 }),
      )
      assert.equal(none, null)
    })

    for (const [name, extra, conclusion] of [
      ['load policy failure', { Findings: [{ Level: 'fail', Rule: 'load_regression' }] }, 'failure'],
      ['incomplete load', { Load: { Sent: 0, Unavailable: 'all routes refused' } }, 'action_required'],
    ] as const) {
      it(`a reported ${name} reaches the GitHub check`, async () => {
        const head = sha(name)
        await deliver('pull_request', pullRequestPayload('opened', 301, head))
        await deliver('workflow_run', workflowRunPayload('in_progress', head, 5301))
        const token = await callbackFor(head, 5301)
        const response = await h.fetch('/v1/pr/report', {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
          body: JSON.stringify({
            head_sha: head,
            report: { Workflows: [{ Name: 'read', Verdict: 'pass' }], ...extra },
          }),
        })
        if (response.status !== 200) throw new Error(`report refused: ${response.status}`)
        assert.equal(checkFor(head)?.conclusion, conclusion)
      })
    }

    // -----------------------------------------------------------------------
    // ordering: exploration report then hosted conclusion
    // -----------------------------------------------------------------------

    for (const [name, exploration, conclusion] of [
      ['missing exploration', { Declared: ['goal'], Results: [] }, 'action_required'],
      ['observed exploration', { Declared: ['goal'], Results: [{ name: 'goal', outcome: { verdict: 'pass' }, visited: ['/runs'], evidence: { trace: 'trace.zip' } }] }, 'success'],
    ] as const) {
      it(`a reported ${name} reaches the GitHub check`, async () => {
        const head = sha(name)
        await deliver('pull_request', pullRequestPayload('opened', 302, head))
        await deliver('workflow_run', workflowRunPayload('in_progress', head, 5302))
        const token = await callbackFor(head, 5302)
        const response = await h.fetch('/v1/pr/report', {
          method: 'POST',
          headers: { 'content-type': 'application/json', authorization: `Bearer ${token}` },
          body: JSON.stringify({ head_sha: head, report: { Workflows: [{ Verdict: 'pass' }], Exploration: exploration } }),
        })
        if (response.status !== 200) throw new Error(`report refused: ${response.status}`)
        assert.equal(checkFor(head)?.conclusion, conclusion)
      })
    }

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
      // The run introduces itself, which is what gives the control plane a run
      // id to cancel. Cancelling that run is the only route it has into the
      // runtime holding the environment, so a run that never said which run it
      // was is a run whose environment nothing can reach.
      assert.ok(await callbackFor(head, 5009))
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
      assert.ok(await callbackFor(head, 5010))
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
      //
      // The run here is the check: it introduced itself, so this control plane
      // knows whose silence this is. That distinction is the whole of the
      // guard above it. A run that never introduced itself and exits green
      // says nothing about the commit and is left to the deadline; THIS run
      // said it was the check, ran, and came back with nothing, and that is a
      // finding rather than a gap.
      const head = sha('missing-callback')
      await deliver('pull_request', pullRequestPayload('opened', 22, head))
      assert.ok(await callbackFor(head, 5011))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5011))
      await deliver('workflow_run', workflowRunPayload('completed', head, 5011, 'success'))

      const done = await generation(head)
      assert.equal(done?.state, 'unverified', 'a green job with no report was read as a pass')
      assert.equal(checkFor(head)?.conclusion, 'action_required')
      assert.notEqual(checkFor(head)?.conclusion, 'success')
      assert.match(commentFor(22)!.body, /nothing was verified/i)
    })

    it('a stranger\u2019s workflow run cannot end a check it never ran', async () => {
      // THE DEFECT THAT MADE THIS REPOSITORY'S OWN CHECK SAY "Nothing was
      // verified" ON EVERY PULL REQUEST IT HAS EVER HAD.
      //
      // A GitHub App is delivered `workflow_run` for EVERY workflow in the
      // repository. This control plane used to bind the first delivery it saw
      // for a commit and then let that run's completion decide the check. One
      // workflow per repository gets away with it. On commit ada5644 of this
      // repository, which has seventeen, the run that ended the generation was
      // `Security`, green fifty seconds in, while the job running `af ci` was
      // still building its database twenty minutes from an answer.
      //
      // So the check was completed and amber before the check had run, and it
      // was amber for the honest reason that nothing had reported, which is
      // why it read as true and went unfixed. The lie was not in the sentence.
      // It was in having listened to a stranger.
      const head = sha('stranger-run')
      await deliver('pull_request', pullRequestPayload('opened', 60, head))

      // Somebody else's workflow, start to finish, green. It is not the check.
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 7001))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7001, 'success'))

      const afterStranger = await generation(head)
      assert.equal(
        afterStranger?.state,
        'queued',
        'a workflow run that never claimed this commit ended the check anyway',
      )
      assert.equal(afterStranger?.workflow_run_id, null, 'a stranger\u2019s run was bound')
      assert.notEqual(checkFor(head)?.status, 'completed')

      // The run that IS the check says so, the way a job says so: by trading a
      // workflow identity for a credential good for this commit.
      const token = (await callbackFor(head, 7002))!
      assert.equal((await generation(head))?.workflow_run_id, '7002')
      assert.equal((await generation(head))?.state, 'running')

      // And now its own completion means something.
      assert.equal((await report(token, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    // -----------------------------------------------------------------------
    // ordering: unclaimed workflow
    // -----------------------------------------------------------------------

    /** The customer's own Antifailure workflow, answering a pull request. */
    const antifailure = { name: 'Antifailure', path: WORKFLOW_PATH, event: 'pull_request' }

    it('ordering: unclaimed workflow, the customer\u2019s own run finished and nobody claimed the commit', async () => {
      // THE FIRST PULL REQUEST AFTER INSTALLING THE APP. The App posted this
      // check the moment the pull request opened, the workflow it committed
      // ran green, and with nothing pointing the run at this control plane the
      // check read "Waiting for a runner" for forty five minutes and then
      // "Nothing was verified: the run never reported back", beside a green
      // job of the same name. The stranger guard above is right to ignore a
      // run that never introduced itself; this run is the one file the App
      // commits, on the event the check is about, and its finishing without
      // a word is the finding, said now and said with the variable's name.
      const head = sha('unclaimed-workflow')
      await deliver('pull_request', pullRequestPayload('opened', 62, head))
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 7101, null, antifailure))
      assert.equal((await generation(head))?.state, 'queued', 'an unclaimed start moved the check')

      await deliver('workflow_run', workflowRunPayload('completed', head, 7101, 'success', antifailure))
      const done = await generation(head)
      assert.equal(done?.state, 'unverified')
      assert.equal(done?.workflow_run_id, null, 'concluding is not binding')
      // The sentence, and what it has to carry: the variable, this control
      // plane's address, and the page with the file. Each is a separate
      // assertion because each is a separate thing a reader has to be told.
      assert.equal(done?.detail, unclaimedDetail('http://app.test'))
      assert.ok(done!.detail!.includes(`\`${CONTROL_PLANE_VARIABLE}\``), 'the variable is not named')
      assert.ok(done!.detail!.includes('`http://app.test`'), 'this control plane is not named')
      assert.ok(done!.detail!.includes(SETUP_DOCS_URL), 'the page is not named')
      assert.notEqual(done?.detail, TIMED_OUT_DETAIL)
      // action_required, not timed_out: something came back, and it was the
      // wrong shape, which is a thing a person has to fix rather than wait on.
      assert.equal(checkFor(head)?.status, 'completed')
      assert.equal(checkFor(head)?.conclusion, 'action_required')
      assert.match(commentFor(62)!.body, new RegExp(CONTROL_PLANE_VARIABLE))

      // And the sweeper, forty five minutes later, has nothing to add: the
      // check concluded once, with the better sentence.
      h.clock.advance(DEFAULT_DEADLINE_MS + 60_000)
      await sweepGenerations(lifecycle())
      assert.equal((await generation(head))?.detail, unclaimedDetail('http://app.test'))
    })

    it('ordering: unclaimed then callback, a late claim cannot reopen the verdict', async () => {
      // A job that introduces itself after the workflow that ran it has
      // finished is not a job on this commit's check: the Re-run button is
      // the route to another attempt, and it bumps the attempt so that the
      // record says so.
      const head = sha('unclaimed-then-callback')
      await deliver('pull_request', pullRequestPayload('opened', 63, head))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7102, 'success', antifailure))
      assert.equal((await generation(head))?.state, 'unverified')

      assert.equal(await callbackFor(head, 7103), null, 'a concluded check issued a credential')
      assert.equal((await generation(head))?.state, 'unverified')
      assert.equal((await generation(head))?.workflow_run_id, null)
    })

    it('ordering: claim then workflow, the claimed run\u2019s silence is the older sentence', async () => {
      // The other ordering of the same two events. A run that claimed the
      // commit and then said nothing is a job that ran and did not post its
      // report, and that sentence names `af ci`, not a variable: the variable
      // was plainly set, or the claim could not have happened.
      const head = sha('claim-then-workflow')
      await deliver('pull_request', pullRequestPayload('opened', 64, head))
      assert.ok(await callbackFor(head, 7104))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7104, 'success', antifailure))
      const done = await generation(head)
      assert.equal(done?.state, 'unverified')
      assert.match(done!.detail!, /af ci/)
      assert.ok(!done!.detail!.includes(CONTROL_PLANE_VARIABLE), 'a claimed run was told to set the variable')
    })

    it('ordering: claim then a second run of the workflow finishes, and the claimed one still owns the verdict', async () => {
      // Two runs of the same file on one commit: a `labeled` delivery beside
      // the `opened` one, or a Re-run somebody pressed in the Actions tab.
      // The run that claimed the commit is the check. The other finishing,
      // whatever it concludes, is a stranger with a familiar name.
      const head = sha('claimed-then-another')
      await deliver('pull_request', pullRequestPayload('opened', 69, head))
      assert.ok(await callbackFor(head, 7110))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7111, 'success', antifailure))
      const still = await generation(head)
      assert.equal(still?.state, 'running', 'an unclaimed twin ended a claimed check')
      assert.equal(still?.workflow_run_id, '7110')
      assert.notEqual(checkFor(head)?.status, 'completed')
    })

    it('the customer\u2019s own workflow failing before it claimed is blocked, not the variable sentence', async () => {
      const head = sha('unclaimed-failed')
      await deliver('pull_request', pullRequestPayload('opened', 65, head))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7105, 'failure', antifailure))
      const done = await generation(head)
      assert.equal(done?.state, 'blocked')
      assert.match(done!.detail!, /Read the job log/)
      assert.equal(checkFor(head)?.conclusion, 'action_required')
    })

    for (const conclusion of ['skipped', 'cancelled']) {
      it(`a ${conclusion} run of the customer\u2019s own workflow concludes nothing`, async () => {
        // A `labeled` event that is not the approval label skips the job by
        // design, and a push cancels the run it supersedes. Neither says
        // whether the workflow can report, and the real run may be claiming
        // the commit this second: an `opened` and a `labeled` delivered
        // together start two runs, of which one is skipped in seconds.
        const head = sha(`unclaimed-${conclusion}`)
        await deliver('pull_request', pullRequestPayload('opened', 66, head))
        await deliver('workflow_run', workflowRunPayload('completed', head, 7106, conclusion, antifailure))
        assert.equal((await generation(head))?.state, 'queued')
        assert.notEqual(checkFor(head)?.status, 'completed')
      })
    }

    it('a dispatched run of the customer\u2019s own workflow is never the check', async () => {
      // The same file runs on workflow_dispatch when the console asks for an
      // environment, on the branch a pull request is on, so its head is a
      // pull request's head. Only a pull_request run answers the check.
      const head = sha('unclaimed-dispatch')
      await deliver('pull_request', pullRequestPayload('opened', 67, head))
      await deliver(
        'workflow_run',
        workflowRunPayload('completed', head, 7107, 'success', { ...antifailure, event: 'workflow_dispatch' }),
      )
      assert.equal((await generation(head))?.state, 'queued')
    })

    it('a stranger that names itself is still a stranger', async () => {
      // The seventeen workflow repository, with the fields a real delivery
      // carries: a security scan, green, on the pull request event.
      const head = sha('named-stranger')
      await deliver('pull_request', pullRequestPayload('opened', 68, head))
      await deliver(
        'workflow_run',
        workflowRunPayload('completed', head, 7108, 'success', {
          name: 'Security',
          path: '.github/workflows/security.yml',
          event: 'pull_request',
        }),
      )
      assert.equal((await generation(head))?.state, 'queued')
      assert.notEqual(checkFor(head)?.status, 'completed')
    })

    it('a stranger\u2019s run that fails cannot block a check either', async () => {
      // The same guard from the other side, because the failure direction is
      // the one an outside contributor would see: a lint workflow that goes
      // red on somebody's branch would have reported the change as blocked
      // before Antifailure had looked at a line of it.
      const head = sha('stranger-run-red')
      await deliver('pull_request', pullRequestPayload('opened', 61, head))
      await deliver('workflow_run', workflowRunPayload('completed', head, 7003, 'failure'))
      assert.equal((await generation(head))?.state, 'queued')
      assert.notEqual(checkFor(head)?.status, 'completed')
    })

    it('a run that failed before reporting is blocked, not failed', async () => {
      // Blocked and failed are different claims. A job that died before the
      // check ran has found nothing about the change, and reporting it as a
      // failure of the change blames the author for our own gap.
      const head = sha('run-failed-early')
      await deliver('pull_request', pullRequestPayload('opened', 23, head))
      assert.ok(await callbackFor(head, 5012))
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
        assert.ok(await callbackFor(head, runId))
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
    // ordering: the third Re-run button, the one in the Actions tab
    //
    // GITHUB HAS THREE RE-RUN BUTTONS AND THIS CONTROL PLANE HEARD TWO OF THEM.
    //
    // The two above are the buttons on a CHECK: one check, or the checks page.
    // Both send `rerequested`, and handleRerequest reopens the generation for
    // them. The one in the Actions tab re-runs the WORKFLOW RUN, and GitHub
    // answers it by starting a second ATTEMPT of the same run id and delivering
    // `workflow_run` for it. No `check_run` and no `check_suite` is sent,
    // because no check asked for anything, so nothing reopened the generation
    // and the second attempt's claim met a concluded row and was refused.
    //
    // What that cost, on this repository, on pull requests 224 through 229: the
    // second attempt did the whole twenty minutes, asked for a credential, was
    // refused, and the workflow read the refusal as the fork case and exited
    // zero. The job was GREEN, the check on the commit went on reporting the
    // attempt that had been replaced, and nothing anywhere said that the run
    // which had just finished had verified nothing. A check that reports the
    // result of a run it refused is worse than no check.
    //
    // So a re-run is a second generation for the same commit whether or not a
    // button on a check started it, and the party that knows a re-run is
    // happening is the run itself. It says so the same way it says which run it
    // is: by trading a workflow identity for a credential, and that identity
    // names the attempt.
    // -----------------------------------------------------------------------

    it('ordering: a passed attempt is re-run from the Actions tab and the re-run fails', async () => {
      // THE REPRODUCTION, and it is the worst direction of the two: the check
      // was green, somebody re-ran it, the re-run found a failure, and the
      // pull request went on showing the green tick from the attempt that had
      // been replaced.
      const head = sha('rerun-passed-then-failed')
      await deliver('pull_request', pullRequestPayload('opened', 90, head))

      const first = await claim(head, 5900, 1)
      assert.equal(first.status, 200)
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5900))
      assert.equal((await report(first.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success')
      const firstCheck = checkFor(head)!.id
      const deadlineWas = (await generation(head))!.deadline_at

      // Re-run, in the Actions tab. No check_run and no check_suite delivery,
      // because GitHub sends neither for it: a second attempt of run 5900.
      h.clock.advance(60_000)
      const again = await claim(head, 5900, 2)
      assert.equal(
        again.status,
        200,
        `the re-run was refused a credential, so it cannot report and the check keeps the ` +
          `replaced attempt's verdict: ${again.error}`,
      )
      assert.ok(again.token, 'the re-run got no credential')

      const reopened = await generation(head)
      assert.equal(reopened?.state, 'running', 'the re-run left the concluded state standing')
      assert.equal(reopened?.attempt, 2, 'the re-run did not count as another attempt')
      assert.equal(reopened?.detail, null, 'the re-run kept the replaced attempt’s sentence')
      assert.equal(reopened?.verdict, null, 'the re-run kept the replaced attempt’s verdict')
      assert.equal(reopened?.finished_at, null, 'a running attempt is recorded as finished')
      assert.equal(reopened?.workflow_run_id, String(5900))
      assert.match(reopened!.reported_by!, /attempt 2$/)
      assert.ok(
        reopened!.deadline_at > deadlineWas,
        'the re-run inherited the deadline of the attempt it replaced, so the sweeper would time ' +
          'it out for time the previous attempt spent',
      )
      // One generation row per head, which is what a repository makes required.
      const rows = await h.admin<{ n: number }[]>`
        SELECT count(*)::int AS n FROM pr_generations WHERE head_sha = ${head}`
      assert.equal(rows[0]!.n, 1, 'the re-run put a second check on one commit')

      // WHAT THE PULL REQUEST SAYS WHILE THE RE-RUN IS RUNNING, which is the
      // half a state machine test cannot see. A completed check run cannot be
      // moved back to in_progress at GitHub, so the attempt that is running now
      // is a NEW check run, and GitHub shows the most recent one of a name.
      assert.equal(checksFor(head).length, 2, 'the re-run has no check run of its own')
      assert.equal(checkFor(head)?.status, 'in_progress')
      assert.equal(checkFor(head)?.conclusion, undefined)
      assert.notEqual(checkFor(head)!.id, firstCheck, 'the completed check run was written over')

      // And the re-run's own answer is the answer.
      assert.equal((await report(again.token!, head, ['fail'])).status, 200)
      assert.equal((await generation(head))?.state, 'failed')
      assert.equal(checkFor(head)?.conclusion, 'failure')
      assert.match(commentFor(90)!.body, /attempt=2/)
    })

    it('ordering: a failed attempt is re-run from the Actions tab and the re-run passes', async () => {
      // The direction a person actually presses the button for. A red check
      // that can never go green without a push is a pull request nobody can
      // land, and re-running it was silently a no-op.
      const head = sha('rerun-failed-then-passed')
      await deliver('pull_request', pullRequestPayload('opened', 91, head))
      const first = await claim(head, 5901, 1)
      assert.equal((await report(first.token!, head, ['fail'])).status, 200)
      assert.equal(checkFor(head)?.conclusion, 'failure')

      h.clock.advance(60_000)
      const again = await claim(head, 5901, 2)
      assert.equal(again.status, 200, `the re-run was refused: ${again.error}`)
      assert.equal((await report(again.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success', 'the check kept the failure it was re-run for')
      assert.equal(checkFor(head)?.status, 'completed')
    })

    for (const concluded of [
      { state: 'unverified', how: 'exited zero having reported nothing' },
      { state: 'blocked', how: 'ended red before the check ran' },
      { state: 'cancelled', how: 'was cancelled' },
      { state: 'failed', how: 'reported a failing check' },
      { state: 'passed', how: 'reported a passing check' },
    ] as const) {
      it(`ordering: the workflow run is re-run after an attempt that ${concluded.how}`, async () => {
        // Every terminal state a first attempt can leave, because the refusal
        // this replaces quoted the state back and so was reachable from all
        // five, and because a re-run is the ordinary answer to four of them.
        const head = sha(`rerun-run-${concluded.state}`)
        const number = 92 + GENERATION_STATES.indexOf(concluded.state)
        const runId = 5910 + GENERATION_STATES.indexOf(concluded.state)
        await deliver('pull_request', pullRequestPayload('opened', number, head))

        const first = await claim(head, runId, 1)
        assert.ok(first.token, `the first attempt was refused a credential: ${first.error}`)
        await deliver('workflow_run', workflowRunPayload('in_progress', head, runId))
        if (concluded.state === 'failed' || concluded.state === 'passed') {
          const verdict = concluded.state === 'failed' ? 'fail' : 'pass'
          assert.equal((await report(first.token, head, [verdict])).status, 200)
        } else {
          const conclusion =
            concluded.state === 'unverified'
              ? 'success'
              : concluded.state === 'cancelled'
                ? 'cancelled'
                : 'failure'
          await deliver('workflow_run', workflowRunPayload('completed', head, runId, conclusion))
        }
        const done = await generation(head)
        assert.equal(done?.state, concluded.state, 'the first attempt did not reach its state')
        assert.equal(done?.attempt, 1)

        h.clock.advance(60_000)
        const again = await claim(head, runId, 2)
        assert.equal(again.status, 200, `a re-run after ${concluded.state} was refused: ${again.error}`)
        const reopened = await generation(head)
        assert.equal(reopened?.state, 'running')
        assert.equal(reopened?.attempt, 2)
        assert.equal(reopened?.detail, null)
        assert.equal(reopened?.verdict, null)
        assert.notEqual(checkFor(head)?.status, 'completed')

        assert.equal((await report(again.token!, head, ['pass'])).status, 200)
        assert.equal((await generation(head))?.state, 'passed')
        assert.equal(checkFor(head)?.conclusion, 'success')
      })
    }

    it('ordering: a second run claims while the attempt that was running has not ended', async () => {
      // A second run of the same commit arriving before the first has ended.
      // GitHub will not start a second ATTEMPT of a run that is still going,
      // but a second RUN on the same commit is ordinary here: a `labeled`
      // delivery beside an `opened` one, a dispatch from the console, a
      // workflow somebody added after the first run started.
      //
      // The credential moves to whoever claimed last, because the row holds one
      // hash. So the BINDING has to move with it. It did not: the check stayed
      // bound to the first run, whose completion then ended the generation as
      // "nothing was reported" and withdrew the credential of the run that was
      // at that moment about to report.
      const head = sha('rerun-while-running')
      await deliver('pull_request', pullRequestPayload('opened', 97, head))
      const first = await claim(head, 5920, 1)
      assert.ok(first.token)
      assert.equal((await generation(head))?.workflow_run_id, '5920')

      const second = await claim(head, 5921, 1)
      assert.equal(second.status, 200, `a second run was refused while the first ran: ${second.error}`)
      assert.equal(
        (await generation(head))?.workflow_run_id,
        '5921',
        'the credential moved to the second run and the binding did not, so the first run’s ' +
          'completion ends the check the second one is reporting on',
      )

      // The first run finishes, saying nothing. It is not the check any more.
      await deliver('workflow_run', workflowRunPayload('completed', head, 5920, 'success'))
      assert.equal(
        (await generation(head))?.state,
        'running',
        'the run that was replaced ended the attempt that replaced it',
      )
      assert.equal((await report(second.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    it('ordering: the re-run claims before the delivery saying the first attempt finished', async () => {
      // The same two events the other way round, and the ordering GitHub
      // actually produces most often: a person presses Re-run the moment the run
      // ends, the second attempt reaches its first step in seconds, and
      // `workflow_run` completed for the attempt it replaced is still in flight.
      // The row therefore still says running when the re-run claims, so this
      // does not go through the reopen at all, and the check has to come out of
      // it saying the same thing: attempt 2, and attempt 1's completion ignored.
      const head = sha('rerun-before-completion')
      await deliver('pull_request', pullRequestPayload('opened', 106, head))
      const first = await claim(head, 5997, 1)
      assert.ok(first.token)
      await deliver('workflow_run', workflowRunPayload('in_progress', head, 5997, null, { runAttempt: 1 }))

      const again = await claim(head, 5997, 2)
      assert.equal(again.status, 200, `the re-run was refused: ${again.error}`)
      assert.equal((await generation(head))?.attempt, 2, 'the attempt that is running is not counted')
      assert.match((await generation(head))!.reported_by!, /attempt 2$/)

      // Attempt 1's completion, arriving now. It is over, and it has nothing to
      // say about the attempt that replaced it.
      await deliver('workflow_run', workflowRunPayload('completed', head, 5997, 'success', { runAttempt: 1 }))
      assert.equal(
        (await generation(head))?.state,
        'running',
        'the first attempt’s completion ended the attempt that replaced it, so the credential the ' +
          'running attempt holds is withdrawn and its report is refused',
      )
      assert.equal((await report(again.token!, head, ['pass'])).status, 200)
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    it('ordering: two re-runs of the same check, one after the other', async () => {
      const head = sha('rerun-twice')
      await deliver('pull_request', pullRequestPayload('opened', 98, head))
      const first = await claim(head, 5930, 1)
      assert.equal((await report(first.token!, head, ['fail'])).status, 200)

      h.clock.advance(60_000)
      const second = await claim(head, 5930, 2)
      assert.equal(second.status, 200, `the first re-run was refused: ${second.error}`)
      assert.equal((await report(second.token!, head, ['fail'])).status, 200)
      assert.equal((await generation(head))?.attempt, 2)

      h.clock.advance(60_000)
      const third = await claim(head, 5930, 3)
      assert.equal(third.status, 200, `the second re-run was refused: ${third.error}`)
      assert.equal((await generation(head))?.attempt, 3)
      assert.equal((await report(third.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success')
      // Three attempts, three check runs, and the one a person sees is the
      // third. The first two stay as they concluded, which is what a check run
      // at GitHub does: it cannot be moved back out of completed.
      assert.equal(checksFor(head).length, 3)
      assert.match(commentFor(98)!.body, /attempt=3/)
    })

    it('the attempt that already reported cannot claim again and wipe its own result', async () => {
      // The other side of the reopen. A credential is good for one report, but
      // the IDENTITY that bought it stays valid for the minutes GitHub signed
      // it for, so a replayed identity could ask for a second credential on a
      // commit whose answer is already recorded. Reopening on ANY claim would
      // let that erase a verdict somebody has read and put the check back to
      // running with nothing on the way.
      const head = sha('rerun-same-attempt')
      await deliver('pull_request', pullRequestPayload('opened', 99, head))
      const token = (await claim(head, 5940, 1)).token!
      assert.equal((await report(token, head, ['pass'])).status, 200)

      const replay = await claim(head, 5940, 1)
      assert.equal(replay.status, 409, 'the same run and attempt bought a second credential')
      assert.equal(replay.token, null)
      assert.match(replay.error ?? '', /already had its turn/i)
      const still = await generation(head)
      assert.equal(still?.state, 'passed', 'a replayed identity reopened a reported check')
      assert.equal(still?.attempt, 1)
      assert.ok(still?.verdict, 'a replayed identity discarded the recorded verdict')
      assert.equal(checkFor(head)?.conclusion, 'success')
      assert.equal(checksFor(head).length, 1, 'a refused claim still made a new check run')
    })

    it('an earlier attempt’s identity cannot reopen what a later attempt concluded', async () => {
      // The same replay one step further out. Attempt 1's identity is still
      // valid while attempt 2 runs and reports, so "not the attempt already
      // recorded" is not enough: it has to be a LATER one. Otherwise a token
      // captured from the run that was replaced can reopen the answer the run
      // that replaced it recorded.
      const head = sha('rerun-earlier-attempt')
      await deliver('pull_request', pullRequestPayload('opened', 100, head))
      const first = await claim(head, 5950, 1)
      assert.equal((await report(first.token!, head, ['fail'])).status, 200)
      const second = await claim(head, 5950, 2)
      assert.equal((await report(second.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')

      const stale = await claim(head, 5950, 1)
      assert.equal(stale.status, 409, 'an earlier attempt reopened a later attempt’s verdict')
      assert.match(stale.error ?? '', /already had its turn/i)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal((await generation(head))?.attempt, 2)
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    it('ordering: two claims from the same re-run arrive together, and one credential is issued', async () => {
      // THE READ AND THE WRITE ARE TWO STATEMENTS, and a job whose first request
      // timed out asks again, so one attempt can be inside the window between
      // them twice. Nothing about the timing is left to chance: a transaction
      // here holds the generation's row lock, both claims read the concluded
      // check and queue on that lock, and only once BOTH are seen waiting in
      // pg_stat_activity is the lock released. Under READ COMMITTED the second
      // UPDATE then re-reads the row the first one wrote, so the comparison in
      // its WHERE clause, and nothing else, decides what it does. Without it
      // both are issued a credential, the attempt is reopened twice, and the
      // first credential is one the row no longer holds.
      const head = sha('rerun-concurrent-claims')
      await deliver('pull_request', pullRequestPayload('opened', 303, head))
      const first = await claim(head, 6010, 1)
      assert.equal((await report(first.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')

      let racing: Promise<Awaited<ReturnType<typeof claim>>[]> | undefined
      await h.admin.begin(async (tx) => {
        await tx`SELECT id FROM pr_generations WHERE head_sha = ${head} FOR UPDATE`
        racing = Promise.all([claim(head, 6010, 2), claim(head, 6010, 2)])
        const deadline = Date.now() + 10_000
        for (;;) {
          const [row] = await h.admin<{ n: number }[]>`
            SELECT count(*)::int AS n FROM pg_stat_activity
            WHERE pid <> pg_backend_pid() AND wait_event_type = 'Lock'
              AND query LIKE '%UPDATE pr_generations%'`
          if (row!.n >= 2) break
          if (Date.now() > deadline) {
            assert.fail(
              `only ${row!.n} of the two claims reached the write while the row was held, so ` +
                `this ordering was never produced and nothing below would be measuring it`,
            )
          }
          await new Promise((resolve) => setTimeout(resolve, 20))
        }
      })

      const answers = await racing!
      const statuses = answers.map((a) => a.status)
      const issued = answers.filter((a) => a.status === 200)
      const refused = answers.filter((a) => a.status !== 200)
      assert.equal(issued.length, 1, `two claims for one attempt were answered ${statuses}`)
      assert.equal(refused[0]?.status, 409)
      assert.match(refused[0]?.error ?? '', /changed underneath this claim/)

      const reopened = await generation(head)
      assert.equal(reopened?.state, 'running')
      assert.equal(reopened?.attempt, 2, 'one attempt was counted twice')
      assert.equal(reopened?.verdict, null, 'the reopened check kept the replaced verdict')
      assert.equal((await report(issued[0]!.token!, head, ['pass'])).status, 200)
      assert.equal((await generation(head))?.state, 'passed')
      assert.equal(checkFor(head)?.conclusion, 'success')
      assert.equal(checksFor(head).length, 2, 'one attempt was given two check runs')
    })

    it('ordering: the replaced attempt’s completion arrives after the re-run claimed', async () => {
      // GitHub delivers `workflow_run` completed for attempt 1 whenever it
      // delivers it, and a redelivery can be minutes late. By then attempt 2
      // may already be running. That delivery must not conclude the attempt
      // that replaced it: doing so writes "nothing was reported" over a check
      // that is running and withdraws the credential the running attempt holds.
      const head = sha('rerun-stale-completion')
      await deliver('pull_request', pullRequestPayload('opened', 101, head))
      const first = await claim(head, 5960, 1)
      assert.equal((await report(first.token!, head, ['pass'])).status, 200)

      h.clock.advance(60_000)
      const again = await claim(head, 5960, 2)
      assert.equal(again.status, 200, `the re-run was refused: ${again.error}`)
      assert.equal((await generation(head))?.state, 'running')

      // Attempt 1, arriving now.
      await deliver('workflow_run', workflowRunPayload('completed', head, 5960, 'success', { runAttempt: 1 }))
      const still = await generation(head)
      assert.equal(
        still?.state,
        'running',
        'the completion of the attempt that was replaced ended the attempt running now',
      )
      assert.equal(still?.detail, null)
      assert.equal((await report(again.token!, head, ['fail'])).status, 200)
      assert.equal((await generation(head))?.state, 'failed')
      assert.equal(checkFor(head)?.conclusion, 'failure')

      // And the re-run's own completion, which IS this attempt's, is heard: it
      // arrives after the report, so it has nothing to add.
      await deliver('workflow_run', workflowRunPayload('completed', head, 5960, 'success', { runAttempt: 2 }))
      assert.equal((await generation(head))?.state, 'failed')
    })

    it('ordering: a re-run that never reports is timed out on its own deadline', async () => {
      // The callback that never arrives. The attempt is reopened with a fresh
      // deadline, so the sweeper does not time it out for the time the previous
      // attempt spent, and it does time it out once its own deadline passes.
      const head = sha('rerun-never-reports')
      await deliver('pull_request', pullRequestPayload('opened', 102, head))
      const first = await claim(head, 5970, 1)
      assert.equal((await report(first.token!, head, ['pass'])).status, 200)

      h.clock.advance(DEFAULT_DEADLINE_MS + 60_000)
      const again = await claim(head, 5970, 2)
      assert.equal(again.status, 200, `the re-run was refused: ${again.error}`)
      // The previous attempt's deadline is long past, and this one is not. The
      // sweeper's own count is about every overdue row in the database, so the
      // claim being made here is about THIS row: it survives a pass that
      // happens while the deadline it inherited is already behind the clock.
      await sweepGenerations(lifecycle())
      assert.equal(
        (await generation(head))?.state,
        'running',
        'the re-run inherited the deadline of the attempt it replaced, so the sweeper gave up on ' +
          'it for time the previous attempt spent',
      )

      h.clock.advance(DEFAULT_DEADLINE_MS + 60_000)
      assert.ok((await sweepGenerations(lifecycle())).timedOut >= 1)
      const gave = await generation(head)
      assert.equal(gave?.state, 'unverified')
      assert.equal(gave?.detail, TIMED_OUT_DETAIL)
      assert.equal(checkFor(head)?.conclusion, 'timed_out')
    })

    it('a check the sweeper gave up on is re-runnable, and stops saying it timed out', async () => {
      // The other entry point into `unverified`, and not the one the loop above
      // drives: there the run said it had finished, here nobody said anything
      // and the deadline sweeper wrote the state. A re-run is the ordinary
      // answer to a check that timed out, and the sentence the sweeper wrote
      // must not outlive the attempt it was about, because `timed_out` and
      // `action_required` are told apart by that sentence alone.
      const head = sha('rerun-after-timeout')
      await deliver('pull_request', pullRequestPayload('opened', 103, head))
      assert.ok((await claim(head, 5980, 1)).token)
      h.clock.advance(DEFAULT_DEADLINE_MS + 60_000)
      assert.ok((await sweepGenerations(lifecycle())).timedOut >= 1)
      assert.equal((await generation(head))?.detail, TIMED_OUT_DETAIL)
      assert.equal(checkFor(head)?.conclusion, 'timed_out')

      const again = await claim(head, 5980, 2)
      assert.equal(again.status, 200, `a re-run after a timeout was refused: ${again.error}`)
      const reopened = await generation(head)
      assert.equal(reopened?.state, 'running')
      assert.equal(
        reopened?.detail,
        null,
        'the sweeper’s sentence outlived the attempt it was about, so a running check reads ' +
          'as one that already gave up',
      )
      assert.notEqual(checkFor(head)?.status, 'completed')
      assert.equal((await report(again.token!, head, ['pass'])).status, 200)
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    it('ordering: a re-run of an older commit reports on that commit and leaves the head alone', async () => {
      // A re-run claiming after a newer commit has its own run. The claim is
      // for the commit the re-run is on, and the newer commit's check is
      // somebody else's row: the answer has to land on the older commit and
      // nothing about the head may move. The comment is the one shared surface,
      // and it stays about the head.
      const older = sha('rerun-older-commit')
      const newer = sha('rerun-newer-commit')
      await deliver('pull_request', pullRequestPayload('opened', 104, older))
      const first = await claim(older, 5990, 1)
      assert.equal((await report(first.token!, older, ['pass'])).status, 200)

      await deliver('pull_request', pullRequestPayload('synchronize', 104, newer))
      assert.equal((await generation(newer))?.state, 'queued')
      const newRun = await claim(newer, 5991, 1)
      assert.equal(newRun.status, 200)
      assert.match(commentFor(104)!.body, new RegExp(`sha=${newer}`))

      h.clock.advance(60_000)
      const again = await claim(older, 5990, 2)
      assert.equal(again.status, 200, `the re-run of the older commit was refused: ${again.error}`)
      assert.equal((await report(again.token!, older, ['fail'])).status, 200)
      assert.equal((await generation(older))?.state, 'failed')
      assert.equal(checkFor(older)?.conclusion, 'failure')
      // The head is untouched: its own attempt is still running and its own
      // credential still works.
      assert.equal((await generation(newer))?.state, 'running')
      assert.equal((await generation(newer))?.attempt, 1)
      assert.match(
        commentFor(104)!.body,
        new RegExp(`sha=${newer}`),
        'a result for an older commit became the comment on a newer head',
      )
      assert.equal((await report(newRun.token!, newer, ['pass'])).status, 200)
      assert.equal((await generation(newer))?.state, 'passed')
    })

    it('another run’s re-run cannot discard the verdict of the run that reported', async () => {
      // The console's "start an environment" verb dispatches the same workflow
      // file on a pull request's head, and that run holds the same shape of
      // identity as the check's: it claims, and it never reports, because it runs
      // `af up` rather than the check. Re-running THAT from the Actions tab is
      // attempt 2 of a run this check never heard of, and reopening for it would
      // mean asking for an environment silently discarded a recorded verdict and
      // left the check running until the deadline.
      //
      // So a re-run reopens the check only for the run that was checking the
      // commit. Another run wanting a verdict of its own is what the Re-run
      // button is for: it sends `rerequested`, and that is handled above.
      const head = sha('rerun-other-run')
      await deliver('pull_request', pullRequestPayload('opened', 109, head))
      const checking = await claim(head, 6001, 1)
      assert.equal((await report(checking.token!, head, ['pass'])).status, 200)

      const stranger = await claim(head, 6002, 2)
      assert.equal(stranger.status, 409, 'a run that was never the check reopened a recorded verdict')
      assert.match(stranger.error ?? '', /already passed/)
      assert.doesNotMatch(stranger.error ?? '', /already had its turn/i)
      const still = await generation(head)
      assert.equal(still?.state, 'passed')
      assert.equal(still?.attempt, 1)
      assert.equal(still?.workflow_run_id, '6001')
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    it('a re-run of a commit that never had a check is refused, and says which refusal it is', async () => {
      // The nightly runs on a schedule against the default branch, where there
      // is no pull request and so no check to report to. That refusal is not
      // the re-run one and must not be worded as though a re-run would fix it,
      // because the workflow now decides whether to fail the job on the
      // strength of the sentence it gets back.
      const head = sha('rerun-no-check')
      const nothing = await claim(head, 5995, 2)
      assert.equal(nothing.status, 409)
      assert.match(nothing.error ?? '', /no check is waiting/i)
      assert.doesNotMatch(nothing.error ?? '', /already had its turn/i)
    })

    it('the Re-run button and the run’s own claim are one attempt, not two', async () => {
      // Both entry points on one press. The button sends `rerequested`, which
      // reopens the generation and asks GitHub to re-run the run; GitHub then
      // starts attempt 2, which claims. The claim must not count a second
      // attempt on top of the button’s, and the check must end up reporting
      // the attempt that ran.
      const head = sha('rerun-button-then-claim')
      await deliver('pull_request', pullRequestPayload('opened', 105, head))
      api.addWorkflowRun({ id: 5996, repository, status: 'in_progress', conclusion: null, headSha: head })
      const first = await claim(head, 5996, 1)
      assert.equal((await report(first.token!, head, ['fail'])).status, 200)
      api.finishWorkflowRun(5996, 'failure')

      await deliver('check_run', {
        action: 'rerequested',
        check_run: { id: 901, head_sha: head },
        repository: { full_name: repository, owner: { login: org.slug } },
        organization: { login: org.slug },
        installation: { id: installationId },
      })
      assert.equal((await generation(head))?.state, 'queued')
      assert.equal((await generation(head))?.attempt, 2)
      assert.equal(api.workflowRunById(5996)?.reruns, 1)
      // The button reopened the check, so the attempt that is coming needs a
      // check run of its own rather than the completed one it was re-run from:
      // that one has concluded, and a concluded check run cannot be moved back
      // out of completed, so writing to it would leave the failure this was
      // re-run for on the pull request for as long as the new attempt ran.
      assert.equal(checksFor(head).length, 2, 'the queued attempt has no check run of its own')
      assert.equal(checkFor(head)?.status, 'queued')
      assert.equal(
        checksFor(head)[0]?.refusedRegressions,
        0,
        'something wrote to the concluded check run, which GitHub would not have moved',
      )

      const again = await claim(head, 5996, 2)
      assert.equal(again.status, 200, `the run GitHub re-ran was refused: ${again.error}`)
      assert.equal((await generation(head))?.attempt, 2, 'one press counted as two attempts')
      assert.equal((await generation(head))?.state, 'running')
      assert.equal((await report(again.token!, head, ['pass'])).status, 200)
      assert.equal(checkFor(head)?.conclusion, 'success')
    })

    // -----------------------------------------------------------------------
    // ordering: timeout
    // -----------------------------------------------------------------------

    it('ordering: timeout, and the check says so rather than spinning', async () => {
      const head = sha('timeout')
      await deliver('pull_request', pullRequestPayload('opened', 25, head))
      assert.ok(await callbackFor(head, 5014))
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
