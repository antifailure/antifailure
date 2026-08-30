// GitHub, behind an interface, so that the sign-in flow can be tested.
//
// The interface is narrow on purpose: four calls, each returning exactly what
// the application stores. A wider one would tempt callers into passing GitHub's
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
}

export interface GitHubConfig {
  clientId: string
  clientSecret: string
  redirectUri: string
  apiBase?: string
  webBase?: string
  /** Mints installation tokens. Absent when no GitHub App is configured, and
   *  membersOf says so rather than returning an empty list. */
  installationTokens?: { for(installationId: number): Promise<string> }
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
    const orgs = (await this.get(accessToken, '/user/orgs')) as { id: number; login: string }[]
    return orgs.map((o) => ({ id: o.id, login: o.login }))
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
