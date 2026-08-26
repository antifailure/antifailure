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
}

export interface GitHubConfig {
  clientId: string
  clientSecret: string
  redirectUri: string
  apiBase?: string
  webBase?: string
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

  async membersOf(): Promise<{ user: GitHubUser; role: 'admin' | 'member' }[]> {
    // Needs an installation token rather than a user token, which the
    // installation flow supplies. Left unimplemented rather than half
    // implemented: a sync that silently returns nobody would remove every
    // member of the organization on its first run.
    throw new GitHubError('membership sync needs a GitHub App installation token')
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
