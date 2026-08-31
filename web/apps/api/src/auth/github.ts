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

/** One installation as GitHub reports it for a repository, with what that
 *  installation was actually granted rather than what the App asked for. */
export interface InstalledOn {
  id: number
  /** GitHub's own map, `{ actions: 'write', contents: 'read', ... }`. A
   *  permission the installation does not hold is absent rather than 'none'. */
  permissions: Record<string, string>
}

/**
 * The states a dispatch can be refused in, which GitHub's status codes do not
 * separate.
 *
 * Measured against the real API on 2026-08-31, with two installations of the
 * same App on the same public repository, one holding `actions: write` and one
 * holding no actions permission at all:
 *
 *   403 Resource not accessible by integration   no `actions: write`
 *   403 Resource not accessible by integration   App not installed on the repo
 *   404 Not Found                                no workflow file of that name
 *   404 Not Found                                no repository of that name
 *   422 No ref found for: X                      the branch does not exist
 *   422 Workflow does not have ... trigger       no workflow_dispatch
 *   422 Unexpected inputs provided: [...]        the inputs are not declared
 *
 * Two things follow, and both were previously written down the other way
 * round. A missing permission is a 403 and not a 404, so the documentation
 * saying all three 403 causes "answer 404" was wrong. And the permission is
 * checked BEFORE the workflow file is looked for, so a 403 hides whether the
 * file is even there: fixing the permission can reveal a second failure.
 *
 * Which is why nothing here infers a cause from a status code. The 403 and 404
 * cases are settled by asking GitHub the two questions it will answer plainly,
 * and only the 422 cases are read from the response, because what a file
 * triggers on and what inputs it declares are inside the file.
 */
export type DispatchCause =
  | 'no-app-configured'
  | 'app-not-installed'
  | 'repository-not-visible'
  | 'permission-missing'
  | 'workflow-missing'
  | 'branch-missing'
  | 'trigger-missing'
  | 'inputs-refused'
  | 'github-refused'

export interface DispatchBlocker {
  /** Which state this is. Tests assert the cause and not the sentence, so that
   *  rewording a message cannot quietly turn a behaviour test into a spelling
   *  check. */
  cause: DispatchCause
  /** What is wrong and what to do about it, for the person in the console. */
  message: string
}

interface Subject {
  repository: string
  workflow: string
  ref?: string
}

/**
 * One sentence naming the state, one naming the remedy, and no status code.
 *
 * Written once and used from both directions: the console asks for this before
 * anybody presses a button, and a refused dispatch throws it afterwards. Two
 * copies would drift, and the version a person meets on the failure path is
 * the one that would have gone stale.
 */
export function blockerFor(cause: DispatchCause, s: Subject, said = ''): DispatchBlocker {
  const owner = s.repository.split('/')[0] ?? s.repository
  const messages: Record<DispatchCause, string> = {
    'no-app-configured':
      'This control plane has no GitHub App configured, so it cannot ask GitHub to run ' +
      'anything. Set AF_GITHUB_APP_ID, AF_GITHUB_APP_PRIVATE_KEY and ' +
      'AF_GITHUB_APP_WEBHOOK_SECRET on the control plane and restart it.',
    'app-not-installed':
      `The Antifailure GitHub App is not installed on ${s.repository}. Add that repository ` +
      `to the App's installation in the ${owner} organization on GitHub, under Settings, ` +
      `then GitHub Apps, then Antifailure, then Configure.`,
    'repository-not-visible':
      `GitHub will not show this App a repository named ${s.repository}. Either the owner ` +
      `or the name is wrong, or the repository is private and has not been added to the ` +
      `App's installation.`,
    'permission-missing':
      `The Antifailure GitHub App is installed on ${s.repository} but was not granted ` +
      `Actions write, and starting a workflow run needs it. An owner of the ${owner} ` +
      `organization has to approve that permission on GitHub, under Settings, then ` +
      `GitHub Apps, then Antifailure. Nothing in this console can grant it.`,
    'workflow-missing':
      `${s.repository} has no workflow file at .github/workflows/${s.workflow} on its ` +
      `default branch. Copy examples/github-workflow.yml there and commit it to the ` +
      `default branch, which is where GitHub reads it from even for a run on another one.`,
    'branch-missing':
      `${s.repository} has no branch named ${s.ref ?? 'the one asked for'}. Push it first, ` +
      `or leave the branch empty to use the repository's default branch.`,
    'trigger-missing':
      `${s.workflow} in ${s.repository} does not accept being started from outside. Add a ` +
      `workflow_dispatch trigger to the copy on the default branch: GitHub reads the ` +
      `trigger list from the default branch, so adding it on ${s.ref ?? 'a feature branch'} ` +
      `alone changes nothing.`,
    'inputs-refused':
      `${s.workflow} in ${s.repository} does not declare the inputs this console sends. Its ` +
      `workflow_dispatch block needs inputs named command, workflows, duration and scale, ` +
      `which examples/github-workflow.yml already carries.`,
    'github-refused':
      `GitHub would not start ${s.workflow} in ${s.repository}` +
      (said ? `, and gave the reason as: ${said}` : '') +
      `. The App is installed on the repository with Actions write and the workflow file ` +
      `is there, so this is not a setting in this console.`,
  }
  return { cause, message: messages[cause] }
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
   * What would stop a dispatch, asked before anybody presses anything.
   *
   * The console calls this when a repository is chosen, because the failure it
   * replaces was discovered at the worst possible moment: a person filled in a
   * form, pressed a button, and only then learned that the integration could
   * not do the thing at all. None of the four states it finds is caused by
   * anything in the form, and every one of them was already true when the page
   * loaded.
   *
   * Null means nothing GitHub will answer says a dispatch would fail. It is
   * not a promise that one will succeed: whether a workflow declares
   * `workflow_dispatch` and which inputs it takes are inside the file, and
   * reading them would mean parsing somebody's YAML to guess at an answer the
   * dispatch itself gives exactly.
   *
   * `ref` is optional because the console asks this while the branch box is
   * still being typed in, and a lookup per keystroke would be a lot of
   * requests to answer a question the person has not finished asking.
   */
  dispatchBlocker(
    installationId: number,
    repository: string,
    workflow: string,
    ref?: string,
  ): Promise<DispatchBlocker | null>
}

export interface GitHubConfig {
  clientId: string
  clientSecret: string
  redirectUri: string
  apiBase?: string
  webBase?: string
  /** Mints installation tokens. Absent when no GitHub App is configured, and
   *  membersOf says so rather than returning an empty list. */
  installationTokens?: {
    for(installationId: number): Promise<string>
    /**
     * The installation covering a repository, or null when the App is not
     * installed on it.
     *
     * Needs the App JWT rather than an installation token, and that is the
     * whole reason it lives on this port instead of being another fetch in
     * this file: an installation token cannot ask which installations exist.
     * It is also the only call that answers the permission question honestly.
     * A read of the repository does not: GitHub serves a PUBLIC repository to
     * any installation token, so `GET /repos/o/r` returns 200 for a repository
     * the App was never given and 200 for one it holds no Actions permission
     * on. Both were checked against the real API before this was written.
     */
    onRepository(repository: string): Promise<InstalledOn | null>
    /**
     * Drops the cached token, so the next call mints a new one.
     *
     * On the port rather than an implementation detail because the client is
     * the only thing that learns a token has stopped working: the cache cannot
     * know, and GitHub does not tell it until somebody uses it.
     */
    forget(installationId: number): void
  }
}

export class GitHubError extends Error {}

function refusal(cause: DispatchCause, s: Subject, said = ''): GitHubError {
  return new GitHubError(blockerFor(cause, s, said).message)
}

/** `owner/name` as two path segments, not one segment containing a %2F. */
function encodePath(repository: string): string {
  return repository.split('/').map(encodeURIComponent).join('/')
}

/**
 * GitHub's own sentence, and never GitHub's JSON.
 *
 * The message this replaced put `{"message":"Resource not accessible by
 * integration","documentation_url":"https://docs.github.com/...","status":
 * "403"}` on a product screen verbatim. A body with no `message` field is
 * worth showing none of: it is either HTML from a proxy or a shape nobody
 * here has seen, and both read to a person as the console having broken.
 */
function githubSaid(body: string): string {
  try {
    const parsed = JSON.parse(body) as { message?: unknown }
    return typeof parsed.message === 'string' ? parsed.message : ''
  } catch {
    return ''
  }
}

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
   * Dispatches a workflow run, and reads GitHub's refusals as the answers they
   * are rather than passing a status code up.
   *
   * Every failure here is a state the operator can fix and none of them is a
   * fault in this control plane, so each carries the sentence that names the
   * fix. What it deliberately does not do any more is GUESS which state it is
   * in. The message this replaced read a status code and then listed the three
   * things it might mean, which left the person holding an error naming a
   * missing file, an uninstalled App and an ungranted permission at once,
   * three remedies, and no way to tell which afternoon they were about to
   * spend. Underneath it, the raw `{"message":...,"documentation_url":...}`
   * went straight to the screen.
   *
   * So a refusal that could mean more than one thing is settled by asking, in
   * `dispatchBlocker`. The one class read from the response is 422, because a
   * 422 is the file existing and refusing this particular dispatch, and what a
   * file triggers on lives inside the file where no lookup reaches it.
   */
  async dispatchWorkflow(
    installationId: number,
    repository: string,
    workflow: string,
    ref: string,
    inputs: Record<string, string>,
  ): Promise<void> {
    const tokens = this.config.installationTokens
    if (!tokens) {
      throw new GitHubError(blockerFor('no-app-configured', { repository, workflow }).message)
    }
    const path =
      `/repos/${encodePath(repository)}` +
      `/actions/workflows/${encodeURIComponent(workflow)}/dispatches`

    const res = await this.authed(installationId, path, {
      method: 'POST',
      headers: { 'content-type': 'application/json' },
      body: JSON.stringify({ ref, inputs }),
    })
    // 204 and nothing else. GitHub returns no body and no run id, so there is
    // deliberately nothing here to return: the run appears in the customer's
    // Actions tab, and it reaches this control plane the same way every other
    // run does, as events the engine sends.
    if (res.status === 204) return

    const subject: Subject = { repository, workflow, ref }
    const said = githubSaid(await res.text().catch(() => ''))

    // 422 first, and without a lookup. It is the only refusal whose cause is
    // in the response rather than in GitHub's model of the App, and asking
    // about the installation here would answer a question nobody asked.
    if (res.status === 422) {
      if (/no ref found/i.test(said)) throw refusal('branch-missing', subject)
      if (/workflow_dispatch/i.test(said)) throw refusal('trigger-missing', subject)
      if (/inputs/i.test(said)) throw refusal('inputs-refused', subject)
      throw refusal('github-refused', subject, said)
    }

    // Everything else is 403 or 404, and both of those cover more than one
    // state. Ask.
    let blocker: DispatchBlocker | null = null
    try {
      blocker = await this.dispatchBlocker(installationId, repository, workflow, ref)
    } catch {
      // A diagnosis that fails must not replace the refusal it was explaining.
      // Falling through leaves the caller with GitHub's own sentence, which is
      // worse than the specific message and much better than an exception
      // about the lookup, which would name a request the person never made.
    }
    if (blocker) throw new GitHubError(blocker.message)
    throw refusal('github-refused', subject, said)
  }

  async dispatchBlocker(
    installationId: number,
    repository: string,
    workflow: string,
    ref?: string,
  ): Promise<DispatchBlocker | null> {
    const subject: Subject = { repository, workflow, ref }
    const tokens = this.config.installationTokens
    if (!tokens) return blockerFor('no-app-configured', subject)

    const installed = await tokens.onRepository(repository)
    if (!installed) {
      // Not installed, or not a repository GitHub will show this App at all.
      // One more call separates them, because "add the repository to the
      // installation" and "check the name you typed" are different afternoons.
      // A public repository answers 200 here whether or not the App holds it,
      // which is precisely why this is the second question and not the first.
      return blockerFor(
        (await this.reachable(installationId, `/repos/${encodePath(repository)}`))
          ? 'app-not-installed'
          : 'repository-not-visible',
        subject,
      )
    }
    // Absent, not 'none': GitHub omits a permission an installation does not
    // hold rather than reporting it at zero.
    if (installed.permissions.actions !== 'write') {
      return blockerFor('permission-missing', subject)
    }
    const workflowPath =
      `/repos/${encodePath(repository)}/actions/workflows/${encodeURIComponent(workflow)}`
    if (!(await this.reachable(installationId, workflowPath))) {
      return blockerFor('workflow-missing', subject)
    }
    if (ref !== undefined && ref !== '') {
      const branchPath = `/repos/${encodePath(repository)}/branches/${encodeURIComponent(ref)}`
      if (!(await this.reachable(installationId, branchPath))) {
        return blockerFor('branch-missing', subject)
      }
    }
    return null
  }

  /**
   * One request as the installation, retried once on a 401 with a fresh token.
   *
   * The retry is the whole reason this exists, and it is not defensive
   * programming. An installation token is cached for its full hour, and GitHub
   * invalidates the outstanding ones the moment somebody changes what the
   * installation is granted. Accepting a permission is exactly that, and it is
   * the last thing a person does before coming back here to press the button
   * again, so the ordinary path through this product ends with a cached token
   * that GitHub stopped honouring seconds ago and will keep refusing for up to
   * an hour.
   *
   * Worse, without the retry the diagnosis lies. A 401 makes every lookup in
   * `dispatchBlocker` answer "not 404", so it finds nothing wrong and the
   * caller is told the App is installed with Actions write and the workflow
   * file is there. All three sentences are true, and the button still does not
   * work, which is the failure this whole change exists to stop.
   *
   * `forget` had no callers anywhere in the tree until this. It was written for
   * this case and never wired to it.
   */
  private async authed(
    installationId: number,
    path: string,
    init: RequestInit = {},
  ): Promise<Response> {
    const tokens = this.config.installationTokens!
    const base = this.config.apiBase ?? 'https://api.github.com'
    const send = async (token: string): Promise<Response> =>
      fetch(new URL(path, base), {
        ...init,
        headers: {
          ...(init.headers as Record<string, string> | undefined),
          authorization: `Bearer ${token}`,
          accept: 'application/vnd.github+json',
          'x-github-api-version': '2022-11-28',
          'user-agent': 'antifailure',
        },
      })

    const res = await send(await tokens.for(installationId))
    if (res.status !== 401) return res
    // Once, and only once. A second 401 is a credential this process cannot
    // fix by asking again, and a loop would turn a bad private key into a
    // request storm against GitHub.
    tokens.forget(installationId)
    return send(await tokens.for(installationId))
  }

  /**
   * Does GitHub serve this path to this installation?
   *
   * Only 404 counts as absent. Anything else that is not ok is left as present
   * on purpose: a rate limit is a 403 and a bad gateway is a 502, and reading
   * either of those as "your workflow file is missing" would send somebody to
   * commit a file that is already committed. A gate whose findings are
   * sometimes invented is worse than one that occasionally says nothing.
   */
  private async reachable(installationId: number, path: string): Promise<boolean> {
    const res = await this.authed(installationId, path)
    return res.status !== 404
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
