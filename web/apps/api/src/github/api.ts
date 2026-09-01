// Acting on one repository as the App: checks, the comment, and cancelling.
//
// Separate from auth/github.ts on purpose. That file is the OAuth client, and
// everything it does is on behalf of a PERSON who signed in. Everything here is
// on behalf of an INSTALLATION, with a token minted from the App's private key,
// and the two have different lifetimes, different failure modes and different
// blast radii. Mixing them produced the class of bug where a call that needed
// an installation token was made with somebody's user token and worked in
// testing because the tester was an administrator.
//
// THE PERMISSION THIS CANNOT ASSUME IT HAS. Publishing a check run needs
// `checks: write`, which the App does not hold today. Widening an App's
// permissions does not grant them: GitHub raises a request against every
// existing installation and nothing changes until a person accepts it, so the
// App's settings page can read `checks: write` while every installation still
// refuses. That is not a hypothetical here, it is what cost most of an hour on
// `actions: write` already. So every call that can hit it returns a
// GitHubPermissionError naming the permission and the two clicks, and the
// caller degrades rather than failing the delivery: the comment still lands,
// because `pull-requests: write` IS granted.

export class GitHubApiError extends Error {
  readonly status: number

  constructor(message: string, status: number) {
    super(message)
    this.name = 'GitHubApiError'
    this.status = status
  }
}

/**
 * A refusal that a person can fix by granting something.
 *
 * Distinct from GitHubApiError because the two want opposite handling. An API
 * error is a failure and the delivery should be retried; a permission refusal
 * will answer identically forever, so retrying it is a loop, and the useful
 * thing to do is record it and tell somebody which permission is missing.
 */
export class GitHubPermissionError extends GitHubApiError {
  readonly permission: string
  /** What a person has to do, in the order they have to do it. */
  readonly remedy: string

  constructor(permission: string, remedy: string) {
    super(`The GitHub App installation does not hold the ${permission} permission. ${remedy}`, 403)
    this.name = 'GitHubPermissionError'
    this.permission = permission
    this.remedy = remedy
  }
}

/** The two clicks, written once, because half of this instruction is the half
 *  people miss: the App's settings page is not enough. */
export function grantRemedy(permission: string): string {
  return (
    `Open the App's settings, Permissions and events, Repository permissions, ` +
    `set ${permission} to Read and write, and Save. Then open the organization's ` +
    `Installed GitHub Apps, Review request, and Accept new permissions. The second ` +
    `step is not optional: widening an App's permissions raises a request against ` +
    `every installation and grants nothing until somebody accepts it.`
  )
}

export interface CheckOutput {
  title: string
  summary: string
  /** The long half, in Markdown. GitHub caps it at 65535 characters and
   *  answers 422 for a longer one, so it is cut here rather than there. */
  text?: string
}

export interface CheckRunInput {
  /** The check's name, which is what a repository makes required. It is stable
   *  across every run of every commit, and changing it silently un-requires the
   *  check on every repository that named it. */
  name: string
  headSha: string
  status: 'queued' | 'in_progress' | 'completed'
  conclusion?: string
  detailsUrl?: string
  startedAt?: string
  completedAt?: string
  output: CheckOutput
}

export interface WorkflowRunStatus {
  id: number
  status: string
  conclusion: string | null
  headSha: string
}

export interface IssueComment {
  id: number
  body: string
}

/** What this control plane does to a repository, once somebody has installed
 *  the App on it. Narrow on purpose: one method per question, and no method
 *  that reads a line of anybody's code. */
export interface RepositoryApi {
  /**
   * The check run this App already published for a commit, or null.
   *
   * Asked of GitHub rather than remembered, because remembering is what breaks
   * under the ordering this whole feature is about: two deliveries about one
   * pull request can be handled at the same moment, and both would create a
   * check run because neither had written its id down yet. Two check runs with
   * the same name on one commit is what a repository requiring that name sees
   * as a flapping check. Asking first makes the second writer adopt the first
   * one's run, and it also survives a restored database, where the id this
   * control plane cached is somebody else's.
   */
  findCheckRun(
    installationId: number,
    repository: string,
    headSha: string,
    name: string,
  ): Promise<number | null>
  createCheckRun(installationId: number, repository: string, input: CheckRunInput): Promise<number>
  updateCheckRun(
    installationId: number,
    repository: string,
    checkRunId: number,
    input: CheckRunInput,
  ): Promise<void>
  /** The comment this control plane maintains, found by the marker it hides in
   *  the first line of its own body. Null when there is none yet. */
  findComment(
    installationId: number,
    repository: string,
    issueNumber: number,
    marker: string,
  ): Promise<IssueComment | null>
  createComment(
    installationId: number,
    repository: string,
    issueNumber: number,
    body: string,
  ): Promise<number>
  updateComment(
    installationId: number,
    repository: string,
    commentId: number,
    body: string,
  ): Promise<void>
  /** Asks GitHub to stop a workflow run. The only route this control plane has
   *  into the customer's runtime, and the reason it is enough: `af ci` tears
   *  the environment down on a cancelled job. */
  cancelWorkflowRun(installationId: number, repository: string, runId: number): Promise<void>
  /** Asks GitHub to run the same workflow run again, on the same commit.
   *
   *  Re-running the run rather than dispatching the workflow, because a
   *  dispatch names a REF and a ref moves. Somebody pressing Re-run on the
   *  check for an older commit would get a run against whatever the branch
   *  points at now, reported under the commit they asked about. */
  rerunWorkflowRun(installationId: number, repository: string, runId: number): Promise<void>
  /** Null when GitHub has no such run, which is the answer for a run that was
   *  deleted along with its logs. */
  workflowRun(
    installationId: number,
    repository: string,
    runId: number,
  ): Promise<WorkflowRunStatus | null>
}

export interface RepositoryApiConfig {
  /** Mints installation tokens. This is InstallationTokens; the interface is
   *  narrowed so a test can pass something simpler. */
  tokens: { for(installationId: number): Promise<string> }
  apiBase?: string
  fetchImpl?: typeof fetch
}

/** GitHub's own cap on the check output's long half. */
const MAX_OUTPUT_TEXT = 65_535
/** And on a comment body. */
const MAX_COMMENT_BODY = 65_536

export class RealRepositoryApi implements RepositoryApi {
  private readonly config: RepositoryApiConfig

  constructor(config: RepositoryApiConfig) {
    this.config = config
  }

  private get base(): string {
    return this.config.apiBase ?? 'https://api.github.com'
  }

  private get fetchImpl(): typeof fetch {
    return this.config.fetchImpl ?? fetch
  }

  private static path(repository: string, suffix: string): string {
    return `/repos/${repository.split('/').map(encodeURIComponent).join('/')}${suffix}`
  }

  private async call(
    installationId: number,
    method: string,
    path: string,
    body: unknown,
    permission: string,
  ): Promise<Response> {
    const token = await this.config.tokens.for(installationId)
    const res = await this.fetchImpl(new URL(path, this.base), {
      method,
      headers: {
        authorization: `Bearer ${token}`,
        accept: 'application/vnd.github+json',
        'x-github-api-version': '2022-11-28',
        'user-agent': 'antifailure',
        ...(body === undefined ? {} : { 'content-type': 'application/json' }),
      },
      ...(body === undefined ? {} : { body: JSON.stringify(body) }),
    })
    if (res.status === 403) {
      // "Resource not accessible by integration" is what GitHub says for a
      // permission the installation does not hold, and it is checked BEFORE
      // the resource is looked for, which is how a 403 hid a missing workflow
      // file for an entire evening. So a 403 is reported as the permission and
      // never as "not found".
      throw new GitHubPermissionError(permission, grantRemedy(permission))
    }
    return res
  }

  private static async refuse(res: Response, what: string): Promise<never> {
    const body = await res.text().catch(() => '')
    throw new GitHubApiError(
      `GitHub refused to ${what}: ${res.status}. ${body.slice(0, 200)}`,
      res.status,
    )
  }

  async findCheckRun(
    installationId: number,
    repository: string,
    headSha: string,
    name: string,
  ): Promise<number | null> {
    const res = await this.call(
      installationId,
      'GET',
      RealRepositoryApi.path(
        repository,
        `/commits/${encodeURIComponent(headSha)}/check-runs?check_name=${encodeURIComponent(name)}&per_page=100`,
      ),
      undefined,
      'checks: write',
    )
    if (res.status === 404) return null
    if (!res.ok)
      await RealRepositoryApi.refuse(res, `list the check runs on ${repository}@${headSha}`)
    const json = (await res.json()) as { check_runs?: unknown }
    if (!Array.isArray(json.check_runs)) return null
    for (const item of json.check_runs) {
      // One element at a time, skipping what does not decode. A single
      // malformed entry must not make this conclude there is no check run and
      // create a second one.
      const row = item as { id?: unknown; name?: unknown }
      if (typeof row.id === 'number' && row.name === name) return row.id
    }
    return null
  }

  async createCheckRun(
    installationId: number,
    repository: string,
    input: CheckRunInput,
  ): Promise<number> {
    const res = await this.call(
      installationId,
      'POST',
      RealRepositoryApi.path(repository, '/check-runs'),
      checkBody(input),
      'checks: write',
    )
    if (!res.ok) await RealRepositoryApi.refuse(res, `create a check run on ${repository}`)
    const json = (await res.json()) as { id?: number }
    if (typeof json.id !== 'number') {
      throw new GitHubApiError('GitHub created a check run and did not say which one.', 502)
    }
    return json.id
  }

  async updateCheckRun(
    installationId: number,
    repository: string,
    checkRunId: number,
    input: CheckRunInput,
  ): Promise<void> {
    const res = await this.call(
      installationId,
      'PATCH',
      RealRepositoryApi.path(repository, `/check-runs/${checkRunId}`),
      checkBody(input),
      'checks: write',
    )
    if (!res.ok) await RealRepositoryApi.refuse(res, `update check run ${checkRunId}`)
  }

  async findComment(
    installationId: number,
    repository: string,
    issueNumber: number,
    marker: string,
  ): Promise<IssueComment | null> {
    // Newest first, because the comment this control plane maintains is on a
    // busy pull request and the default oldest-first paging would walk every
    // human comment before reaching it. One page is enough in practice and the
    // fallback when it is not is that a second comment is created, which is
    // visible and recoverable, rather than that a wrong one is overwritten.
    const res = await this.call(
      installationId,
      'GET',
      RealRepositoryApi.path(
        repository,
        `/issues/${issueNumber}/comments?per_page=100&sort=created&direction=desc`,
      ),
      undefined,
      'pull requests: write',
    )
    if (!res.ok)
      await RealRepositoryApi.refuse(res, `list the comments on ${repository}#${issueNumber}`)
    const json = await res.json()
    if (!Array.isArray(json)) return null
    for (const item of json) {
      // Read one element at a time and skip what does not decode. A single
      // comment whose body came back as null must not discard the whole page
      // and make this control plane post a second comment beside its own.
      const row = item as { id?: unknown; body?: unknown }
      if (typeof row.id !== 'number' || typeof row.body !== 'string') continue
      if (row.body.includes(marker)) return { id: row.id, body: row.body }
    }
    return null
  }

  async createComment(
    installationId: number,
    repository: string,
    issueNumber: number,
    body: string,
  ): Promise<number> {
    const res = await this.call(
      installationId,
      'POST',
      RealRepositoryApi.path(repository, `/issues/${issueNumber}/comments`),
      { body: body.slice(0, MAX_COMMENT_BODY) },
      'pull requests: write',
    )
    if (!res.ok) await RealRepositoryApi.refuse(res, `comment on ${repository}#${issueNumber}`)
    const json = (await res.json()) as { id?: number }
    if (typeof json.id !== 'number') {
      throw new GitHubApiError('GitHub created a comment and did not say which one.', 502)
    }
    return json.id
  }

  async updateComment(
    installationId: number,
    repository: string,
    commentId: number,
    body: string,
  ): Promise<void> {
    const res = await this.call(
      installationId,
      'PATCH',
      RealRepositoryApi.path(repository, `/issues/comments/${commentId}`),
      { body: body.slice(0, MAX_COMMENT_BODY) },
      'pull requests: write',
    )
    // 404 is the answer for a comment somebody deleted, and it is not a
    // failure: the caller forgets the id and posts a new one. Reported as a
    // 404 GitHubApiError so the caller can tell it from a refusal.
    if (!res.ok) await RealRepositoryApi.refuse(res, `update comment ${commentId}`)
  }

  async cancelWorkflowRun(
    installationId: number,
    repository: string,
    runId: number,
  ): Promise<void> {
    const res = await this.call(
      installationId,
      'POST',
      RealRepositoryApi.path(repository, `/actions/runs/${runId}/cancel`),
      undefined,
      'actions: write',
    )
    // 202 is the ordinary answer. 409 is GitHub saying the run is already in a
    // terminal state, which is the outcome asked for rather than a failure:
    // cancelling something that has already stopped is done.
    if (res.status === 202 || res.status === 409) return
    if (res.status === 404) {
      throw new GitHubApiError(
        `GitHub has no workflow run ${runId} in ${repository}. A run whose logs have been ` +
          `deleted answers this, and so does a run in a repository the App can no longer see.`,
        404,
      )
    }
    await RealRepositoryApi.refuse(res, `cancel workflow run ${runId}`)
  }

  async rerunWorkflowRun(installationId: number, repository: string, runId: number): Promise<void> {
    const res = await this.call(
      installationId,
      'POST',
      RealRepositoryApi.path(repository, `/actions/runs/${runId}/rerun`),
      undefined,
      'actions: write',
    )
    if (res.status === 201) return
    // 403 with this body is not a permission problem, so it is separated from
    // the one the call() wrapper turns into a GitHubPermissionError above: it
    // is GitHub refusing to re-run a run that is still going. Answering
    // "grant a permission" to that would send somebody to the wrong page.
    if (res.status === 409) {
      throw new GitHubApiError(
        `Workflow run ${runId} in ${repository} is still going, so it cannot be re-run yet.`,
        409,
      )
    }
    await RealRepositoryApi.refuse(res, `re-run workflow run ${runId}`)
  }

  async workflowRun(
    installationId: number,
    repository: string,
    runId: number,
  ): Promise<WorkflowRunStatus | null> {
    const res = await this.call(
      installationId,
      'GET',
      RealRepositoryApi.path(repository, `/actions/runs/${runId}`),
      undefined,
      'actions: read',
    )
    if (res.status === 404) return null
    if (!res.ok) await RealRepositoryApi.refuse(res, `read workflow run ${runId}`)
    const json = (await res.json()) as {
      id?: unknown
      status?: unknown
      conclusion?: unknown
      head_sha?: unknown
    }
    return {
      id: typeof json.id === 'number' ? json.id : runId,
      // A status this control plane does not recognise is reported as it
      // arrived rather than coerced. The caller compares against 'completed'
      // and treats anything else as still running, which is the safe direction:
      // it waits rather than declaring a live run finished.
      status: typeof json.status === 'string' ? json.status : 'unknown',
      conclusion: typeof json.conclusion === 'string' ? json.conclusion : null,
      headSha: typeof json.head_sha === 'string' ? json.head_sha : '',
    }
  }
}

function checkBody(input: CheckRunInput): Record<string, unknown> {
  return {
    name: input.name,
    head_sha: input.headSha,
    status: input.status,
    ...(input.conclusion ? { conclusion: input.conclusion } : {}),
    ...(input.detailsUrl ? { details_url: input.detailsUrl } : {}),
    ...(input.startedAt ? { started_at: input.startedAt } : {}),
    ...(input.completedAt ? { completed_at: input.completedAt } : {}),
    output: {
      title: input.output.title.slice(0, 255),
      summary: input.output.summary.slice(0, 1024),
      ...(input.output.text ? { text: input.output.text.slice(0, MAX_OUTPUT_TEXT) } : {}),
    },
  }
}
