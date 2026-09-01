// GitHub, behind an interface, so that the sign-in flow can be tested.
//
// The interface is narrow on purpose: one call per question the application
// asks, each returning exactly what the application stores. A wider one would
// tempt callers into passing GitHub's
// response shape around, and then the fake has to reproduce GitHub's response
// shape rather than its behaviour, which is how a fake stops being worth
// anything.

export interface GitHubUser {
  id: number
  login: string
  email: string
  name: string | null
  avatarUrl: string | null
}

export interface GitHubOrg {
  id: number
  login: string
}

export interface GitHubInstallation {
  id: number
  accountLogin: string
  accountType: 'Organization' | 'User'
}

export interface GitHubClient {
  /** Where to send the browser to start the exchange. */
  authorizeUrl(state: string): string
  /** Trades the callback code for a user. Throws if the code is not valid. */
  exchangeCode(code: string): Promise<{ user: GitHubUser; accessToken: string }>
  /** The organizations the user belongs to, for membership sync. */
  organizationsFor(accessToken: string): Promise<GitHubOrg[]>
  /** Everyone in an organization, with the role GitHub reports. */
  membersOf(
    installationId: number,
    orgLogin: string,
  ): Promise<{ user: GitHubUser; role: 'admin' | 'member' }[]>
  /**
   * The role GitHub reports for one person in one organization.
   *
   * Null means "could not be established", which is deliberately not the same
   * answer as "member". Sign-in leans on the difference: it writes `member` for
   * somebody who has no membership row yet, and leaves an existing role exactly
   * as it was when this returns null, so a rate limit cannot demote an
   * administrator out of their own organization.
   */
  roleIn(
    installationId: number,
    orgLogin: string,
    login: string,
  ): Promise<'admin' | 'member' | null>
  /**
   * Asks a workflow in the customer's repository to run.
   *
   * This is the whole of how the control plane acts. It does not bring an
   * environment up, start an agent, or drive load: the engine does all of that
   * inside the customer's own CI, where their database and their secrets
   * already are. Running it here would mean a snapshot of their production
   * shape crossing into this service, which is the one thing the architecture
   * refuses.
   *
   * `repository` is the full name, `owner/name`, because that is what the
   * repositories table stores and splitting it at the call site is one more
   * place to get it wrong. `ref` is a branch or a tag, not a commit: GitHub
   * refuses a SHA here.
   */
  dispatchWorkflow(
    installationId: number,
    repository: string,
    workflow: string,
    ref: string,
    inputs: Record<string, string>,
  ): Promise<void>
  /**
   * Uninstalls the App from an account, for an organization that is being
   * deleted.
   *
   * `removed` false means GitHub had no such installation, which is success
   * rather than failure: a deletion that is re-entered reaches this twice.
   * The refusal that matters is a throw, and it stops the deletion rather than
   * letting it purge an organization whose App is still installed and still
   * able to dispatch workflows.
   *
   * Returns `configured: false` when no GitHub App is set up at all, which is
   * the ordinary self-hosted case. The caller records that it did not call
   * GitHub rather than recording that GitHub said no.
   */
  revokeInstallation(installationId: number): Promise<{ configured: boolean; removed: boolean }>
}

export interface GitHubConfig {
  clientId: string
  clientSecret: string
  redirectUri: string
  apiBase?: string
  webBase?: string
  /** Mints installation tokens, and removes installations. Absent when no
   *  GitHub App is configured, and membersOf says so rather than returning an
   *  empty list. */
  installationTokens?: {
    for(installationId: number): Promise<string>
    revoke(installationId: number): Promise<{ removed: boolean }>
  }
}

export class GitHubError extends Error {}

/** The real client. */
export class RealGitHubClient implements GitHubClient {
  private readonly config: GitHubConfig

  constructor(config: GitHubConfig) {
    this.config = config
  }

  authorizeUrl(state: string): string {
    const base = this.config.webBase ?? 'https://github.com'
    const url = new URL('/login/oauth/authorize', base)
    url.searchParams.set('client_id', this.config.clientId)
    url.searchParams.set('redirect_uri', this.config.redirectUri)
    url.searchParams.set('state', state)
    // read:org is needed to sync membership. user:email is needed because a
    // user with a private email address has no address on the profile.
    url.searchParams.set('scope', 'read:user user:email read:org')
    return url.toString()
  }

  async exchangeCode(code: string): Promise<{ user: GitHubUser; accessToken: string }> {
    const webBase = this.config.webBase ?? 'https://github.com'
    const tokenRes = await fetch(new URL('/login/oauth/access_token', webBase), {
      method: 'POST',
      headers: { accept: 'application/json', 'content-type': 'application/json' },
      body: JSON.stringify({
        client_id: this.config.clientId,
        client_secret: this.config.clientSecret,
        code,
        redirect_uri: this.config.redirectUri,
      }),
    })
    if (!tokenRes.ok) throw new GitHubError(`GitHub refused the code exchange (${tokenRes.status})`)
    const payload = (await tokenRes.json()) as { access_token?: string; error?: string }
    // GitHub answers a bad code with 200 and an error field, so the status is
    // not enough to tell success from failure here.
    if (!payload.access_token) {
      throw new GitHubError(`GitHub refused the code exchange: ${payload.error ?? 'no token returned'}`)
    }
    const user = await this.user(payload.access_token)
    return { user, accessToken: payload.access_token }
  }

  private async user(accessToken: string): Promise<GitHubUser> {
    const profile = (await this.get(accessToken, '/user')) as {
      id: number
      login: string
      email: string | null
      name: string | null
      avatar_url: string | null
    }
    let email = profile.email
    if (!email) {
      const emails = (await this.get(accessToken, '/user/emails')) as {
        email: string
        primary: boolean
        verified: boolean
      }[]
      // An unverified address must never identify an account: anyone can add
      // somebody else's address to their own GitHub account, and matching on it
      // would let them claim that person's membership.
      email = emails.find((e) => e.primary && e.verified)?.email ?? null
    }
    if (!email) {
      throw new GitHubError(
        'GitHub returned no verified email address for this account, so there is nothing to identify it by.',
      )
    }
    return {
      id: profile.id,
      login: profile.login,
      email: email.toLowerCase(),
      name: profile.name,
      avatarUrl: profile.avatar_url,
    }
  }

  async organizationsFor(accessToken: string): Promise<GitHubOrg[]> {
    // Paged, for the reason membersOf is paged, with a worse consequence.
    // /user/orgs returns thirty per page by default, and this list decides
    // which organizations somebody may enter. Truncating it does not shrink a
    // list somebody reads: it silently withholds the tenant they came here
    // for, and the console renders the empty state that means "nobody has
    // installed the App" to a person whose App is installed. The failure is
    // invisible from inside, because thirty organizations is a plausible
    // number to have.
    const out: GitHubOrg[] = []
    const seen = new Set<number>()
    for (let page = 1; page <= 20; page++) {
      const batch = await this.get(accessToken, `/user/orgs?per_page=100&page=${page}`)
      // A page that is not a list is a shape this code will not guess at, and
      // continuing would loop twenty times over the same surprise.
      if (!Array.isArray(batch) || batch.length === 0) break
      for (const item of batch) {
        // One malformed entry must not discard the organizations around it.
        // This list is assembled from a foreign boundary and then decides
        // access, so a single odd row costing somebody every tenant is the
        // expensive direction to fail in.
        if (typeof item !== 'object' || item === null) continue
        const org = item as { id?: unknown; login?: unknown }
        if (typeof org.id !== 'number' || typeof org.login !== 'string' || !org.login) continue
        if (seen.has(org.id)) continue
        seen.add(org.id)
        out.push({ id: org.id, login: org.login })
      }
      if (batch.length < 100) break
    }
    return out
  }

  /**
   * Everyone in an organization, with the role GitHub reports.
   *
   * Needs an INSTALLATION token, not a user token. A user token can only see
   * the members a user can see, which for an outside collaborator is almost
   * nobody, so a sync built on one would quietly shrink the member list
   * depending on whose token happened to run it.
   *
   * It THROWS when no App is configured rather than returning an empty list.
   * That distinction is the whole reason this stayed unimplemented for so long:
   * a sync that returns nobody looks exactly like an organization where
   * everybody left, and a caller that reconciles against it would remove every
   * member on its first run. An exception cannot be mistaken for an answer.
   */
  async membersOf(
    installationId: number,
    orgLogin: string,
  ): Promise<{ user: GitHubUser; role: 'admin' | 'member' }[]> {
    const tokens = this.config.installationTokens
    if (!tokens) {
      throw new GitHubError(
        'Membership sync needs a GitHub App. Set AF_GITHUB_APP_ID, ' +
          'AF_GITHUB_APP_PRIVATE_KEY and AF_GITHUB_APP_WEBHOOK_SECRET.',
      )
    }
    const token = await tokens.for(installationId)
    const base = this.config.apiBase ?? 'https://api.github.com'

    const members: { user: GitHubUser; role: 'admin' | 'member' }[] = []
    // Paged, because an organization with more than thirty members would
    // otherwise be silently truncated to its first page, and a truncated list
    // fed to a reconciler removes everybody past member thirty.
    for (let page = 1; page <= 20; page++) {
      const url = new URL(`/orgs/${encodeURIComponent(orgLogin)}/members`, base)
      url.searchParams.set('per_page', '100')
      url.searchParams.set('page', String(page))
      // The role each member holds, which /members alone does not carry.
      url.searchParams.set('role', 'all')

      const res = await fetch(url, {
        headers: {
          authorization: `Bearer ${token}`,
          accept: 'application/vnd.github+json',
          'x-github-api-version': '2022-11-28',
          'user-agent': 'antifailure',
        },
      })
      if (!res.ok) {
        throw new GitHubError(
          `GitHub refused the member list for ${orgLogin}: ${res.status}`,
        )
      }
      const batch = (await res.json()) as { id: number; login: string }[]
      if (!Array.isArray(batch) || batch.length === 0) break

      for (const m of batch) {
        // The role comes from a second call per member, because GitHub reports
        // it on the membership resource rather than in the list. Worth the
        // calls: without it every member syncs as a plain member and an
        // organization owner loses their own admin rights on first sync.
        const role = (await this.roleOf(token, base, orgLogin, m.login)) ?? 'member'
        members.push({
          user: {
            id: m.id,
            login: m.login,
            // The member list carries no email and no name, and an installation
            // token cannot read a private address. Left empty rather than
            // invented; sign-in fills both in from the person's own token.
            email: '',
            name: null,
            avatarUrl: null,
          },
          role,
        })
      }
      if (batch.length < 100) break
    }
    return members
  }

  async roleIn(
    installationId: number,
    orgLogin: string,
    login: string,
  ): Promise<'admin' | 'member' | null> {
    const tokens = this.config.installationTokens
    // No App, no installation token, no way to read a membership. Null rather
    // than 'member', because the caller's fallback is its own decision to make.
    if (!tokens) return null
    try {
      const token = await tokens.for(installationId)
      const base = this.config.apiBase ?? 'https://api.github.com'
      return await this.roleOf(token, base, orgLogin, login)
    } catch {
      return null
    }
  }

  /**
   * Dispatches a workflow run, and reads GitHub's refusals as the answers they
   * are rather than passing a status code up.
   *
   * Every failure here is a state the operator can fix and none of them is a
   * fault in this control plane, so each carries the sentence that names the
   * fix. The two that arrive most often are worth calling out because GitHub's
   * own message for them is misleading:
   *
   * 404 is not "the repository is gone". It is what GitHub answers for a
   * workflow file that does not exist, for a repository this installation was
   * not given, and for an App that holds no `actions: write` permission, and
   * they are indistinguishable from here on purpose: GitHub will not confirm
   * the existence of something the caller may not see.
   *
   * 422 is the workflow file existing and not accepting a dispatch. Either it
   * has no `workflow_dispatch` trigger, or the ref does not exist on the
   * default branch, which is the rule people trip over: GitHub reads the
   * trigger list from the DEFAULT branch, so a workflow that only gained
   * `workflow_dispatch` on a feature branch cannot be dispatched at all.
   */
  async revokeInstallation(installationId: number): Promise<{ configured: boolean; removed: boolean }> {
    const tokens = this.config.installationTokens
    // No App configured at all. Reported rather than thrown: a self-hosted
    // control plane with no GitHub App has no installation to remove, and a
    // deletion must not stop on the absence of a thing that was never there.
    if (!tokens) return { configured: false, removed: false }
    const { removed } = await tokens.revoke(installationId)
    return { configured: true, removed }
  }

  async dispatchWorkflow(
    installationId: number,
    repository: string,
    workflow: string,
    ref: string,
    inputs: Record<string, string>,
  ): Promise<void> {
    const tokens = this.config.installationTokens
    if (!tokens) {
      throw new GitHubError(
        'Dispatching a workflow needs a GitHub App. Set AF_GITHUB_APP_ID, ' +
          'AF_GITHUB_APP_PRIVATE_KEY and AF_GITHUB_APP_WEBHOOK_SECRET.',
      )
    }
    const token = await tokens.for(installationId)
    const base = this.config.apiBase ?? 'https://api.github.com'
    const path =
      `/repos/${repository.split('/').map(encodeURIComponent).join('/')}` +
      `/actions/workflows/${encodeURIComponent(workflow)}/dispatches`

    const res = await fetch(new URL(path, base), {
      method: 'POST',
      headers: {
        authorization: `Bearer ${token}`,
        accept: 'application/vnd.github+json',
        'content-type': 'application/json',
        'x-github-api-version': '2022-11-28',
        'user-agent': 'antifailure',
      },
      body: JSON.stringify({ ref, inputs }),
    })
    // 204 and nothing else. GitHub returns no body and no run id, so there is
    // deliberately nothing here to return: the run appears in the customer's
    // Actions tab, and it reaches this control plane the same way every other
    // run does, as events the engine sends.
    if (res.status === 204) return

    const body = await res.text().catch(() => '')
    if (res.status === 404) {
      throw new GitHubError(
        `GitHub cannot see ${workflow} in ${repository}. Either the file is not at ` +
          `.github/workflows/${workflow} on the default branch, or the App was not ` +
          `installed on this repository, or it has not been granted the Actions ` +
          `write permission. All three answer 404.`,
      )
    }
    if (res.status === 422) {
      throw new GitHubError(
        `GitHub refused to dispatch ${workflow} in ${repository}: ${body.slice(0, 200)}. ` +
          `A workflow can only be dispatched if the copy on the DEFAULT branch declares ` +
          `workflow_dispatch, and the ref ${ref} has to exist.`,
      )
    }
    throw new GitHubError(
      `GitHub refused to dispatch ${workflow} in ${repository}: ${res.status}. ${body.slice(0, 200)}`,
    )
  }

  /**
   * Null when GitHub would not say, rather than a guess.
   *
   * membersOf reads that null as 'member', which is the safe direction for a
   * list that is about to be reconciled. roleIn passes it through, because the
   * safe direction there is "change nothing".
   */
  private async roleOf(
    token: string,
    base: string,
    orgLogin: string,
    login: string,
  ): Promise<'admin' | 'member' | null> {
    const res = await fetch(
      new URL(`/orgs/${encodeURIComponent(orgLogin)}/memberships/${encodeURIComponent(login)}`, base),
      {
        headers: {
          authorization: `Bearer ${token}`,
          accept: 'application/vnd.github+json',
          'x-github-api-version': '2022-11-28',
          'user-agent': 'antifailure',
        },
      },
    )
    // Anything but a clear "admin" is a member. Guessing upward on a failed
    // read would hand somebody administrative rights because of a rate limit.
    if (!res.ok) return null
    const body = (await res.json()) as { role?: string }
    return body.role === 'admin' ? 'admin' : 'member'
  }

  private async get(accessToken: string, path: string): Promise<unknown> {
    const base = this.config.apiBase ?? 'https://api.github.com'
    const res = await fetch(new URL(path, base), {
      headers: {
        authorization: `Bearer ${accessToken}`,
        accept: 'application/vnd.github+json',
        'user-agent': 'antifailure-control-plane',
      },
    })
    if (!res.ok) throw new GitHubError(`GitHub ${path} answered ${res.status}`)
    return res.json()
  }
}
