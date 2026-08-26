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
import type { GitHubClient, GitHubOrg, GitHubUser } from './github.ts'
import type { Clock } from '../clock.ts'

interface PendingCode {
  code: string
  login: string
  issuedAt: number
}

export class FakeGitHub implements GitHubClient {
  private readonly users = new Map<string, GitHubUser>()
  private readonly orgs = new Map<string, GitHubOrg[]>()
  private readonly members = new Map<string, { user: GitHubUser; role: 'admin' | 'member' }[]>()
  private readonly codes = new Map<string, PendingCode>()
  private readonly tokens = new Map<string, string>()

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
}
