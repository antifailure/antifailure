// The real client, against a GitHub that answers the way GitHub answers.
//
// FakeRepositoryApi is what the lifecycle suite drives, and it deliberately
// enforces the RULES rather than the wire: which permission each call needs,
// that a check run's head commit is immutable, that a cancel is recorded and
// the run stops later. What it cannot exercise is the half that is only wrong
// on the wire, and that half is where this kind of code actually breaks: a path
// assembled with the wrong number of slashes, a 409 read as a failure, a body
// whose one malformed element throws away the page it arrived in.
//
// So this drives RealRepositoryApi over real HTTP against a server that answers
// the shapes GitHub answers, including the ones that are easy to get wrong:
//
//   403 Resource not accessible by integration   a permission, not a 404
//   404 on a cancel                              a run whose logs were deleted
//   409 on a cancel                              already finished, which is done
//   409 on a re-run                              still going, which is not
//
// The 403 is the one worth the whole file. GitHub checks the permission BEFORE
// it looks for the resource, so a missing permission and a missing workflow
// file are indistinguishable from the status code alone, and reading one as the
// other cost most of an evening on this repository already.

import { after, before, describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createServer, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { GitHubApiError, GitHubPermissionError, RealRepositoryApi } from '../src/github/api.ts'
import { COMMENT_MARKER, reachable, shaOfComment, commentBody } from '../src/github/render.ts'
import { decodeReport } from '../src/github/lifecycle.ts'
import { stateFromReport } from '../src/github/states.ts'

interface Recorded {
  method: string
  path: string
  body: string
  authorization: string | undefined
}

describe('the GitHub repository client', () => {
  let server: Server
  let base: string
  const seen: Recorded[] = []
  /** What the next matching request is answered with. */
  const answers = new Map<string, { status: number; body: unknown }>()

  before(async () => {
    server = createServer((req, res) => {
      let body = ''
      req.on('data', (chunk) => (body += chunk))
      req.on('end', () => {
        seen.push({
          method: req.method ?? '',
          path: req.url ?? '',
          body,
          authorization: req.headers.authorization,
        })
        const answer =
          answers.get(`${req.method} ${req.url}`) ?? answers.get(`${req.method} *`) ?? null
        if (!answer) {
          // 501 rather than 404, for the reason the Stripe harness gives: 404
          // is a real answer here, and a missing ROUTE must never be mistaken
          // for a missing OBJECT.
          res.writeHead(501, { 'content-type': 'application/json' })
          res.end(JSON.stringify({ message: `no route for ${req.method} ${req.url}` }))
          return
        }
        res.writeHead(answer.status, { 'content-type': 'application/json' })
        res.end(JSON.stringify(answer.body))
      })
    })
    await new Promise<void>((resolve) => server.listen(0, '127.0.0.1', resolve))
    base = `http://127.0.0.1:${(server.address() as AddressInfo).port}`
  })

  after(async () => {
    await new Promise<void>((resolve) => server.close(() => resolve()))
  })

  function client(): RealRepositoryApi {
    return new RealRepositoryApi({
      tokens: { for: async () => 'ghs_installation_token' },
      apiBase: base,
    })
  }

  function answer(route: string, status: number, body: unknown): void {
    answers.set(route, { status, body })
  }

  const checkInput = {
    name: 'Antifailure',
    headSha: 'a'.repeat(40),
    status: 'completed' as const,
    conclusion: 'action_required',
    output: { title: 'Nothing was verified', summary: 'Commit `aaaaaaa`.' },
  }

  it('creates a check run at the path GitHub serves, with the installation token', async () => {
    answers.clear()
    seen.length = 0
    answer('POST /repos/acme/app/check-runs', 201, { id: 42 })

    const id = await client().createCheckRun(7, 'acme/app', checkInput)
    assert.equal(id, 42)

    const request = seen.at(-1)!
    assert.equal(request.authorization, 'Bearer ghs_installation_token')
    const sent = JSON.parse(request.body) as Record<string, unknown>
    // snake_case on the wire and camelCase in the code, which is exactly the
    // kind of thing a fake agrees with its author about.
    assert.equal(sent.head_sha, 'a'.repeat(40))
    assert.equal(sent.conclusion, 'action_required')
    assert.equal(sent.name, 'Antifailure')
  })

  it('reads a 403 as the permission it is, rather than as a missing resource', async () => {
    answers.clear()
    answer('POST /repos/acme/app/check-runs', 403, {
      message: 'Resource not accessible by integration',
    })

    await assert.rejects(
      () => client().createCheckRun(7, 'acme/app', checkInput),
      (err: unknown) => {
        assert.ok(err instanceof GitHubPermissionError, 'a 403 was not read as a permission')
        assert.equal(err.permission, 'checks: write')
        // The remedy names the step people miss. An App's settings page can
        // read the permission while every installation still holds none of it.
        assert.match(err.remedy, /Accept new permissions/)
        return true
      },
    )
  })

  it('finds an existing check run for a commit rather than creating a second one', async () => {
    answers.clear()
    answer(
      `GET /repos/acme/app/commits/${'a'.repeat(40)}/check-runs?check_name=Antifailure&per_page=100`,
      200,
      {
        check_runs: [
          // A malformed entry first. One bad element must not make this
          // conclude there is no check run and put a second one on the commit.
          { id: 'not a number', name: 'Antifailure' },
          { id: 99, name: 'somebody else' },
          { id: 77, name: 'Antifailure' },
        ],
      },
    )
    assert.equal(await client().findCheckRun(7, 'acme/app', 'a'.repeat(40), 'Antifailure'), 77)
  })

  it('finds the comment it maintains by its marker, and skips what does not decode', async () => {
    answers.clear()
    answer('GET /repos/acme/app/issues/12/comments?per_page=100&sort=created&direction=desc', 200, [
      { id: 1, body: null },
      { id: 2, body: 'somebody else said this' },
      { id: 3, body: `${COMMENT_MARKER} sha=${'b'.repeat(40)} attempt=1 -->\nours` },
    ])
    const found = await client().findComment(7, 'acme/app', 12, COMMENT_MARKER)
    assert.equal(found?.id, 3)
    assert.equal(shaOfComment(found!.body), 'b'.repeat(40))
  })

  it('treats a cancel of a run that already finished as done', async () => {
    answers.clear()
    // 409 is GitHub saying the run is already terminal, which is the outcome
    // that was asked for rather than a failure.
    answer('POST /repos/acme/app/actions/runs/5/cancel', 409, { message: 'already completed' })
    await client().cancelWorkflowRun(7, 'acme/app', 5)
  })

  it('says plainly when GitHub has no such run', async () => {
    answers.clear()
    answer('POST /repos/acme/app/actions/runs/6/cancel', 404, { message: 'Not Found' })
    await assert.rejects(
      () => client().cancelWorkflowRun(7, 'acme/app', 6),
      (err: unknown) => {
        assert.ok(err instanceof GitHubApiError)
        assert.equal(err.status, 404)
        assert.match(err.message, /logs have been\s+deleted/)
        return true
      },
    )
  })

  it('does not read a re-run refused for being still going as a permission problem', async () => {
    answers.clear()
    answer('POST /repos/acme/app/actions/runs/8/rerun', 409, { message: 'still running' })
    await assert.rejects(
      () => client().rerunWorkflowRun(7, 'acme/app', 8),
      (err: unknown) => {
        assert.ok(err instanceof GitHubApiError)
        assert.ok(
          !(err instanceof GitHubPermissionError),
          'a run that is still going was reported as a missing grant, which sends somebody to the wrong page',
        )
        assert.match(err.message, /still going/)
        return true
      },
    )
  })

  it('reads an unrecognised run status as still running rather than as finished', async () => {
    answers.clear()
    // The safe direction. Teardown waits rather than declaring a live run over.
    answer('GET /repos/acme/app/actions/runs/9', 200, {
      id: 9,
      status: 42,
      head_sha: 'c'.repeat(40),
    })
    const run = await client().workflowRun(7, 'acme/app', 9)
    assert.equal(run?.status, 'unknown')
    assert.notEqual(run?.status, 'completed')
  })

  it('answers null for a run GitHub does not have, rather than throwing', async () => {
    answers.clear()
    answer('GET /repos/acme/app/actions/runs/10', 404, { message: 'Not Found' })
    assert.equal(await client().workflowRun(7, 'acme/app', 10), null)
  })
})

describe('what reaches a pull request comment', () => {
  it('does not offer an address only the runner can reach as a link', () => {
    // GitHub-flavoured Markdown auto-links a bare URL, so an environment at
    // http://127.0.0.1:46001 in a comment is a clickable link to whoever is
    // READING it, on their own machine. Wrapping it stops the auto-link.
    const rewritten = reachable('Environment `af-1` is at http://127.0.0.1:46001\n')
    assert.match(rewritten, /`http:\/\/127\.0\.0\.1:46001` \(on the runner\)/)
    assert.doesNotMatch(rewritten, /[^`]http:\/\/127\.0\.0\.1:46001[^`]/)
  })

  it('says a trace is on the runner rather than offering a path that does not exist', () => {
    const rewritten = reachable('Trace: `/home/runner/work/app/trace.zip`\n')
    assert.match(rewritten, /on the runner/)
    assert.match(rewritten, /upload-artifact/)
  })

  it('leaves an address a reader can actually reach alone', () => {
    // A negative control. A rewrite that caught everything would be a rewrite
    // that made every real link unusable, and nobody would notice from the
    // positive cases alone.
    const untouched = 'Open https://preview.example.com/orders in a browser\n'
    assert.equal(reachable(untouched), untouched)
  })

  it('carries the commit, the teardown state and a way to run it yourself', () => {
    const body = commentBody({
      state: 'failed',
      timedOut: false,
      headSha: 'd'.repeat(40),
      attempt: 1,
      detail: '1 passed, 1 failed, 0 flaky, 0 blocked, 0 unverified.',
      reportMarkdown: '<!-- antifailure:report -->\n### Antifailure: a check failed\n',
      envId: 'af-orders-9c1a',
      teardown: 'acknowledged',
      consoleBase: 'https://app.antifailure.dev',
      checksUnavailable: null,
    })

    assert.equal(shaOfComment(body), 'd'.repeat(40))
    assert.match(body, /Teardown: done/)
    assert.match(body, /git checkout d{40}/)
    assert.match(body, /https:\/\/app\.antifailure\.dev\/environments\?env=af-orders-9c1a/)
    // The engine's own marker is stripped. Two markers in one comment means the
    // workflow's fallback step finds THIS comment and updates it, and the
    // commit fence is gone on the next push.
    assert.doesNotMatch(body, /antifailure:report/)
  })

  it('says which grant is missing when there is no check to look at', () => {
    const body = commentBody({
      state: 'queued',
      timedOut: false,
      headSha: 'e'.repeat(40),
      attempt: 1,
      detail: null,
      reportMarkdown: null,
      envId: null,
      teardown: 'none',
      consoleBase: null,
      checksUnavailable: 'Open the App settings and grant checks: write.',
    })
    assert.match(body, /There is no check run for this commit/)
    // And no console link is offered when there is no console, because a link
    // to one that is not there is worse than a sentence.
    assert.doesNotMatch(body, /\]\(http/)
  })
})

// ---------------------------------------------------------------------------

describe('reading an engine report', () => {
  // The report crosses a version boundary: the engine that wrote it may be
  // older or newer than the control plane reading it. So one element that does
  // not decode is skipped and the rest is kept, because a report discarded over
  // one malformed field is a pull request with no answer on it, and an outcome
  // word this build has never heard of counts as unverified rather than as a
  // pass.

  it('counts every workflow verdict by name, not by position', () => {
    const decoded = decodeReport({
      Environment: 'af-1',
      Workflows: [
        { Name: 'a', Verdict: 'pass' },
        { Name: 'b', Verdict: 'fail' },
        { Name: 'c', Verdict: 'flaky' },
        { Name: 'd', Verdict: 'blocked' },
        { Name: 'e', Verdict: 'unverified' },
      ],
    })
    assert.deepEqual(decoded.counts, {
      passed: 1,
      failed: 1,
      flaky: 1,
      blocked: 1,
      unverified: 1,
    })
    assert.equal(decoded.environment, 'af-1')
  })

  it('reads a verdict it has never heard of as unverified, never as a pass', () => {
    const decoded = decodeReport({ Workflows: [{ Name: 'a', Verdict: 'brilliant' }] })
    assert.equal(decoded.counts.unverified, 1)
    assert.equal(decoded.counts.passed, 0)
    assert.equal(stateFromReport(decoded.counts), 'unverified')
  })

  it('keeps the workflows around an element that does not decode', () => {
    // The failure this shape exists to prevent: one malformed element throwing
    // away the whole collection, so a broken pull request reads as an empty
    // one.
    const decoded = decodeReport({
      Workflows: [{ Name: 'a', Verdict: 'fail' }, null, 'not an object', { Verdict: 'pass' }],
    })
    assert.equal(decoded.counts.failed, 1)
    assert.equal(decoded.counts.passed, 1)
    assert.equal(stateFromReport(decoded.counts), 'failed')
  })

  it('a violated invariant fails a run whose every workflow passed', () => {
    // The product leads with this. A report where the agents were happy and the
    // data is broken must not read as a pass.
    const decoded = decodeReport({
      Workflows: [{ Name: 'a', Verdict: 'pass' }],
      Invariants: [{ Name: 'no-orphaned-orders', Held: false }],
    })
    assert.equal(decoded.counts.failed, 1)
    assert.equal(stateFromReport(decoded.counts), 'failed')
  })

  it('an invariant that could not be asked is unverified, not violated', () => {
    // Held and Error are separate for the same reason failed and blocked are:
    // an invariant that could not be asked has found nothing, and reporting it
    // as a violation blames the change for our own gap.
    const decoded = decodeReport({
      Workflows: [{ Name: 'a', Verdict: 'pass' }],
      Invariants: [{ Name: 'no-orphaned-orders', Held: false, Error: 'no connection' }],
    })
    assert.equal(decoded.counts.failed, 0)
    assert.equal(decoded.counts.unverified, 1)
    assert.equal(stateFromReport(decoded.counts), 'unverified')
  })

  it('a run that reached no workflow at all verified nothing', () => {
    // Two of this repository's own examples declared no workflows, the report
    // headline was literally "Antifailure: Nothing ran", and the leg was green.
    assert.equal(stateFromReport(decodeReport({ Workflows: [] }).counts), 'unverified')
    assert.equal(stateFromReport(decodeReport({}).counts), 'unverified')
  })
})
