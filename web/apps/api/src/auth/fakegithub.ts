// A GitHub that does what GitHub does, without the network.
//
// It is not a stub that returns whatever the test wants. It enforces the rules
// the real one enforces, because those rules are what the sign-in code has to
// handle: a code is single use, a code expires, a code issued for one client is
// not valid for another, and an account with no verified address cannot be
// identified.
//
// A fake that says yes to everything proves the happy path works and nothing
// else, and the happy path is not where sign-in breaks.

import { randomBytes } from 'node:crypto'
import {
  GitHubError,
  blockerFor,
  type DispatchBlocker,
  type DispatchCause,
  type GitHubClient,
  type GitHubOrg,
  type GitHubUser,
} from './github.ts'
import type { Clock } from '../clock.ts'

interface PendingCode {
  code: string
  login: string
  issuedAt: number
}

/** One dispatch, exactly as the control plane asked for it. */
export interface Dispatched {
  installationId: number
  repository: string
  workflow: string
  ref: string
  inputs: Record<string, string>
}

export class FakeGitHub implements GitHubClient {
  private readonly users = new Map<string, GitHubUser>()
  private readonly orgs = new Map<string, GitHubOrg[]>()
  private readonly members = new Map<string, { user: GitHubUser; role: 'admin' | 'member' }[]>()
  private readonly codes = new Map<string, PendingCode>()
  private unreachable = false
  private readonly tokens = new Map<string, string>()
  private readonly workflows = new Set<string>()
  private readonly dispatched: Dispatched[] = []
  private dispatchRefusal: DispatchCause | null = null
  // The three states GitHub reports that are not about the workflow file.
  // Held as exceptions rather than as requirements, so that a test which only
  // cares about a dispatch does not have to declare an installation, a
  // permission grant and a branch before it can ask for one.
  private readonly uninstalled = new Set<string>()
  private readonly invisible = new Set<string>()
  private readonly absentBranches = new Set<string>()
  private actionsWrite = true
  private readonly revokedAuthorizations: string[] = []
  private grantRefusal = false

  /** How long a code is good for. GitHub's is ten minutes. */
  readonly codeTtlMs = 10 * 60 * 1000

  private readonly clock: Clock

  constructor(clock: Clock) {
    this.clock = clock
  }

  addUser(user: Omit<GitHubUser, 'avatarUrl'> & { avatarUrl?: string | null }): GitHubUser {
    const full: GitHubUser = { avatarUrl: null, ...user, email: user.email.toLowerCase() }
    this.users.set(full.login, full)
    return full
  }

  addOrganization(login: string, org: GitHubOrg): void {
    this.orgs.set(login, [...(this.orgs.get(login) ?? []), org])
  }

  setMembers(orgLogin: string, members: { user: GitHubUser; role: 'admin' | 'member' }[]): void {
    this.members.set(orgLogin, members)
  }

  /** Simulates the user approving the prompt, returning the callback code. */
  approve(login: string): string {
    if (!this.users.has(login)) throw new Error(`fakegithub: no user ${login}`)
    const code = randomBytes(8).toString('hex')
    this.codes.set(code, { code, login, issuedAt: this.clock.now().getTime() })
    return code
  }

  authorizeUrl(state: string): string {
    return `https://github.test/login/oauth/authorize?state=${encodeURIComponent(state)}`
  }

  async exchangeCode(code: string): Promise<{ user: GitHubUser; accessToken: string }> {
    const pending = this.codes.get(code)
    if (!pending) throw new Error('GitHub refused the code exchange: bad_verification_code')
    // Single use. Deleted before the expiry check so that a replay of an
    // expired code reports the same thing as a replay of a fresh one, which is
    // what stops the error message from telling an attacker which it was.
    this.codes.delete(code)
    if (this.clock.now().getTime() - pending.issuedAt > this.codeTtlMs) {
      throw new Error('GitHub refused the code exchange: bad_verification_code')
    }
    const user = this.users.get(pending.login)!
    const accessToken = randomBytes(16).toString('hex')
    this.tokens.set(accessToken, user.login)
    return { user, accessToken }
  }

  async organizationsFor(accessToken: string): Promise<GitHubOrg[]> {
    const login = this.tokens.get(accessToken)
    if (!login) throw new Error('GitHub /user/orgs answered 401')
    return this.orgs.get(login) ?? []
  }

  async membersOf(
    _installationId: number,
    orgLogin: string,
  ): Promise<{ user: GitHubUser; role: 'admin' | 'member' }[]> {
    return this.members.get(orgLogin) ?? []
  }

  /**
   * Answers from the same member list membersOf reads, so a test cannot set up
   * an organization where the two disagree.
   *
   * Null for somebody the list does not name, which is what the real client
   * returns when GitHub will not say, and what makes the "leave the role
   * alone" branch in sign-in reachable from a test.
   */
  async roleIn(
    _installationId: number,
    orgLogin: string,
    login: string,
  ): Promise<'admin' | 'member' | null> {
    if (this.unreachable) throw new Error('GitHub is unreachable')
    const found = (this.members.get(orgLogin) ?? []).find(
      (m) => m.user.login.toLowerCase() === login.toLowerCase(),
    )
    return found ? found.role : null
  }

  /** Makes roleIn throw, the way a network failure or a rate limit does. */
  breakRoleLookups(broken = true): void {
    this.unreachable = broken
  }

  /**
   * Says that a repository has a workflow file that accepts a dispatch.
   *
   * Registering it is required rather than optional, for the same reason the
   * code exchange is single use: the interesting case for a dispatch is the
   * one where GitHub says no, and a fake that accepts every dispatch cannot
   * reach it. A repository with no registered workflow refuses exactly the way
   * the real client does when the file is not on the default branch.
   */
  addWorkflow(repository: string, workflow: string): void {
    this.workflows.add(`${repository}#${workflow}`)
  }

  /** The App is not installed on this repository, though GitHub will still
   *  show it. */
  uninstallFrom(repository: string): void {
    this.uninstalled.add(repository)
  }

  /** GitHub will not show this App the repository at all: a wrong name, or a
   *  private repository nobody added to the installation. */
  hideRepository(repository: string): void {
    this.invisible.add(repository)
    this.uninstalled.add(repository)
  }

  /** The installation exists and was never granted Actions write, which is the
   *  state every installation of a widened App is in until somebody accepts. */
  revokeActionsWrite(): void {
    this.actionsWrite = false
  }

  /** A branch nobody pushed. */
  removeBranch(repository: string, ref: string): void {
    this.absentBranches.add(`${repository}#${ref}`)
  }

  /**
   * Puts every dispatch exception back, leaving the registered workflows and
   * the dispatch log alone.
   *
   * One suite shares one fake across its tests, so a test that revokes a
   * permission to reach the refusal would otherwise revoke it for everything
   * after it. Restoring in a finally is what keeps a failed assertion from
   * turning into a different failure three tests later.
   */
  reset(): void {
    this.uninstalled.clear()
    this.invisible.clear()
    this.absentBranches.clear()
    this.actionsWrite = true
    this.dispatchRefusal = null
  }

  /** Every dispatch the control plane asked for, in order. */
  get dispatches(): readonly Dispatched[] {
    return this.dispatched
  }

  /**
   * Makes the next dispatch fail the way GitHub does when the workflow file
   * exists and will not accept this particular dispatch.
   *
   * A cause and not a sentence, because these two are the states no lookup can
   * find: what a file triggers on and which inputs it declares are inside the
   * file. Naming them as causes is what keeps the fake and the real client
   * refusing the same set of things.
   */
  refuseDispatches(cause: 'trigger-missing' | 'inputs-refused' | null = 'trigger-missing'): void {
    this.dispatchRefusal = cause
  }

  async dispatchBlocker(
    _installationId: number,
    repository: string,
    workflow: string,
    ref?: string,
  ): Promise<DispatchBlocker | null> {
    const subject = { repository, workflow, ref }
    // The same order the real client asks in, because the order is a claim
    // about GitHub and not an implementation detail: a repository that is both
    // uninstalled and missing a workflow file reports the installation, since
    // installing is what has to happen first.
    if (this.uninstalled.has(repository)) {
      return blockerFor(
        this.invisible.has(repository) ? 'repository-not-visible' : 'app-not-installed',
        subject,
      )
    }
    if (!this.actionsWrite) return blockerFor('permission-missing', subject)
    if (!this.workflows.has(`${repository}#${workflow}`)) {
      return blockerFor('workflow-missing', subject)
    }
    if (ref !== undefined && ref !== '' && this.absentBranches.has(`${repository}#${ref}`)) {
      return blockerFor('branch-missing', subject)
    }
    return null
  }

  async dispatchWorkflow(
    installationId: number,
    repository: string,
    workflow: string,
    ref: string,
    inputs: Record<string, string>,
  ): Promise<void> {
    // GitHubError, not a plain Error, and this is the one place in the fake
    // where the exact type matters. The routes turn a GitHubError into a
    // refusal the caller can read and let anything else become a 500, so a
    // fake that threw something untyped would make the refusal path
    // unreachable from a test and leave the 500 path the only one exercised.
    if (this.dispatchRefusal) {
      throw new GitHubError(
        blockerFor(this.dispatchRefusal, { repository, workflow, ref }).message,
      )
    }
    const blocker = await this.dispatchBlocker(installationId, repository, workflow, ref)
    if (blocker) throw new GitHubError(blocker.message)
    this.dispatched.push({ installationId, repository, workflow, ref, inputs })
  }

  /**
   * The installations this fake believes exist, and whether the App is set up
   * at all.
   *
   * `installed` starts empty and `appConfigured` starts true, so a test that
   * says nothing gets the honest answer for an App that is configured and an
   * installation GitHub has already forgotten: configured, not removed. A test
   * that wants to prove the removal happened adds one first.
   */
  private readonly installed = new Set<number>()
  private appConfigured = true
  private revokeRefusal: string | null = null
  readonly revoked: number[] = []

  addInstallation(installationId: number): void {
    this.installed.add(installationId)
  }

  /** No GitHub App at all, which is the ordinary self-hosted case. */
  withoutApp(): void {
    this.appConfigured = false
  }

  /** Makes the next uninstall fail the way GitHub does when it will not answer. */
  refuseRevocations(reason: string | null = 'GitHub is having a moment'): void {
    this.revokeRefusal = reason
  }

  async revokeInstallation(
    installationId: number,
  ): Promise<{ configured: boolean; removed: boolean }> {
    if (!this.appConfigured) return { configured: false, removed: false }
    if (this.revokeRefusal) {
      throw new GitHubError(`GitHub refused to uninstall ${installationId}: ${this.revokeRefusal}`)
    }
    this.revoked.push(installationId)
    const removed = this.installed.delete(installationId)
    return { configured: true, removed }
  }

  /**
   * Withdraws a grant, and behaves like the real endpoint: the token stops
   * working afterwards.
   *
   * That last part is what makes this worth faking rather than counting. A
   * caller that revoked the authorization and then went on to ask GitHub
   * another question with the same token would pass a test that only recorded
   * the call, and would fail against the real API.
   */
  async revokeAuthorization(accessToken: string): Promise<{ revoked: boolean }> {
    if (this.grantRefusal) return { revoked: false }
    const login = this.tokens.get(accessToken)
    if (login) this.revokedAuthorizations.push(login)
    this.tokens.delete(accessToken)
    return { revoked: Boolean(login) }
  }

  /** Whose authorization has been withdrawn, oldest first. */
  authorizationsRevoked(): string[] {
    return [...this.revokedAuthorizations]
  }

  /** Makes the next withdrawal fail, the way an unreachable GitHub does. */
  refuseAuthorizationRevocation(refuse = true): void {
    this.grantRefusal = refuse
  }
}
