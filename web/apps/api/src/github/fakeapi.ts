// A GitHub repository that behaves, without the network.
//
// The same philosophy as auth/fakegithub.ts: it enforces the rules the real one
// enforces, because those rules are what the lifecycle has to handle. A fake
// that accepts every check run proves the happy path and nothing else, and the
// happy path is not where this breaks. What breaks it is an installation that
// does not hold `checks: write`, which is the state EVERY installation is in
// today and will be in until a person clicks Accept.
//
// So permissions are opt in here. A fake with no granted permissions refuses
// exactly the way the real one does, and a test that wants the granted world
// has to say so.

import {
  GitHubApiError,
  GitHubPermissionError,
  grantRemedy,
  type CheckRunInput,
  type IssueComment,
  type RepositoryApi,
  type WorkflowRunStatus,
} from './api.ts'

export interface FakeCheckRun extends CheckRunInput {
  id: number
  repository: string
  installationId: number
  /** How many times this run has been written since it was created. The
   *  lifecycle promises one stable check per head, so a test can assert that a
   *  second push did not create a second run. */
  updates: number
}

export interface FakeComment extends IssueComment {
  repository: string
  issueNumber: number
  /** Every body this comment has ever held, oldest first. A test asserting
   *  that an old head did not overwrite a new one needs the history, not the
   *  final state. */
  history: string[]
}

export interface FakeWorkflowRun {
  id: number
  repository: string
  status: string
  conclusion: string | null
  headSha: string
  cancelRequests: number
  reruns: number
}

export class FakeRepositoryApi implements RepositoryApi {
  private readonly granted = new Set<string>()
  private readonly checkRuns = new Map<number, FakeCheckRun>()
  private readonly comments = new Map<number, FakeComment>()
  private readonly runs = new Map<number, FakeWorkflowRun>()
  private nextId = 1000
  private failure: { message: string; status: number } | null = null

  /** Grants a permission, the way a person accepting a request does. */
  grant(...permissions: string[]): void {
    for (const p of permissions) this.granted.add(p)
  }

  /** Takes one back, for the test that proves the degraded path. */
  revoke(permission: string): void {
    this.granted.delete(permission)
  }

  /** Makes the next call fail the way a network or a rate limit does, which is
   *  a different thing from a refusal and has to be handled differently. */
  breakWith(message: string, status = 502): void {
    this.failure = { message, status }
  }

  mend(): void {
    this.failure = null
  }

  /** Registers a workflow run, the way GitHub having started one does. */
  addWorkflowRun(run: Omit<FakeWorkflowRun, 'cancelRequests' | 'reruns'>): void {
    this.runs.set(run.id, { ...run, cancelRequests: 0, reruns: 0 })
  }

  finishWorkflowRun(id: number, conclusion: string): void {
    const run = this.runs.get(id)
    if (!run) throw new Error(`fakeapi: no workflow run ${id}`)
    run.status = 'completed'
    run.conclusion = conclusion
  }

  get checks(): readonly FakeCheckRun[] {
    return [...this.checkRuns.values()]
  }

  get issueComments(): readonly FakeComment[] {
    return [...this.comments.values()]
  }

  workflowRunById(id: number): FakeWorkflowRun | undefined {
    return this.runs.get(id)
  }

  private require(permission: string): void {
    if (this.failure) {
      const { message, status } = this.failure
      this.failure = null
      throw new GitHubApiError(message, status)
    }
    if (!this.granted.has(permission)) {
      throw new GitHubPermissionError(permission, grantRemedy(permission))
    }
  }

  async findCheckRun(
    _installationId: number,
    repository: string,
    headSha: string,
    name: string,
  ): Promise<number | null> {
    this.require('checks: write')
    for (const run of this.checkRuns.values()) {
      if (run.repository === repository && run.headSha === headSha && run.name === name) {
        return run.id
      }
    }
    return null
  }

  async rerunWorkflowRun(
    _installationId: number,
    _repository: string,
    runId: number,
  ): Promise<void> {
    this.require('actions: write')
    const run = this.runs.get(runId)
    if (!run) throw new GitHubApiError(`no workflow run ${runId}`, 404)
    if (run.status !== 'completed') {
      throw new GitHubApiError(`workflow run ${runId} is still going`, 409)
    }
    run.status = 'queued'
    run.conclusion = null
    run.reruns += 1
  }

  async createCheckRun(
    installationId: number,
    repository: string,
    input: CheckRunInput,
  ): Promise<number> {
    this.require('checks: write')
    const id = this.nextId++
    this.checkRuns.set(id, { ...input, id, repository, installationId, updates: 0 })
    return id
  }

  async updateCheckRun(
    _installationId: number,
    _repository: string,
    checkRunId: number,
    input: CheckRunInput,
  ): Promise<void> {
    this.require('checks: write')
    const existing = this.checkRuns.get(checkRunId)
    if (!existing) throw new GitHubApiError(`no check run ${checkRunId}`, 404)
    // The head SHA of a check run is immutable at GitHub, and a caller trying
    // to move one is a caller that has confused two commits. Refusing here is
    // what makes that a test failure instead of a silently wrong check.
    if (existing.headSha !== input.headSha) {
      throw new GitHubApiError(
        `check run ${checkRunId} is for ${existing.headSha} and was updated with ${input.headSha}`,
        422,
      )
    }
    Object.assign(existing, input, { updates: existing.updates + 1 })
  }

  async findComment(
    _installationId: number,
    repository: string,
    issueNumber: number,
    marker: string,
  ): Promise<IssueComment | null> {
    this.require('pull requests: write')
    for (const comment of this.comments.values()) {
      if (comment.repository !== repository || comment.issueNumber !== issueNumber) continue
      if (comment.body.includes(marker)) return { id: comment.id, body: comment.body }
    }
    return null
  }

  async createComment(
    _installationId: number,
    repository: string,
    issueNumber: number,
    body: string,
  ): Promise<number> {
    this.require('pull requests: write')
    const id = this.nextId++
    this.comments.set(id, { id, repository, issueNumber, body, history: [body] })
    return id
  }

  async updateComment(
    _installationId: number,
    _repository: string,
    commentId: number,
    body: string,
  ): Promise<void> {
    this.require('pull requests: write')
    const comment = this.comments.get(commentId)
    if (!comment) throw new GitHubApiError(`no comment ${commentId}`, 404)
    comment.body = body
    comment.history.push(body)
  }

  /** Deletes a comment, the way a person clicking delete does. The lifecycle
   *  has to survive it: the id it remembers stops resolving. */
  deleteComment(commentId: number): void {
    this.comments.delete(commentId)
  }

  async cancelWorkflowRun(
    _installationId: number,
    _repository: string,
    runId: number,
  ): Promise<void> {
    this.require('actions: write')
    const run = this.runs.get(runId)
    if (!run) throw new GitHubApiError(`no workflow run ${runId}`, 404)
    run.cancelRequests += 1
    // GitHub does not stop the run inside the API call. It records the request
    // and the run reaches `completed` some time later, which is why the
    // teardown lease has to come back and look rather than assuming.
  }

  async workflowRun(
    _installationId: number,
    _repository: string,
    runId: number,
  ): Promise<WorkflowRunStatus | null> {
    this.require('actions: read')
    const run = this.runs.get(runId)
    if (!run) return null
    return { id: run.id, status: run.status, conclusion: run.conclusion, headSha: run.headSha }
  }
}
