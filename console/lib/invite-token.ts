/**
 * Where an invitation token waits while the person goes and signs in.
 *
 * The token used to ride the sign-in round trip inside the return target,
 * `/auth/github?redirect_to=/invite?token=<raw>`, and the control plane stores
 * a return target verbatim: plain text in oauth_states.redirect_to, and in
 * email_signin_tokens.redirect_to for a sign-in link. An invitation is stored
 * as a sha256 for exactly the reason that a readable copy should not exist, so
 * the API now strips a `token` parameter out of any return target.
 *
 * It has to come back some other way, and the browser that already has it is
 * the obvious place: sessionStorage is this tab only, it is this origin only,
 * it never reaches the network, and it is gone when the tab closes.
 */
export const INVITE_TOKEN_KEY = 'af.invite.token'

/** Somewhere to keep one string. Storage in a browser, anything in a test. */
export interface TokenStore {
  getItem(key: string): string | null
  setItem(key: string, value: string): void
  removeItem(key: string): void
}

/**
 * The token to use: the one in the link if there is one, else the one this tab
 * kept while its person was away signing in.
 *
 * Storage throws rather than returning null in a private window with site data
 * blocked, and a screen that cannot read a token still has something to say, so
 * every access here is guarded.
 */
export function inviteToken(fromQuery: string | null, store: TokenStore | null): string {
  const queried = (fromQuery ?? '').trim()
  if (queried !== '') return queried
  if (!store) return ''
  try {
    return (store.getItem(INVITE_TOKEN_KEY) ?? '').trim()
  } catch {
    return ''
  }
}

/** Keeps the token for the trip through the identity provider and back. */
export function rememberInviteToken(store: TokenStore | null, token: string): void {
  if (!store || token === '') return
  try {
    store.setItem(INVITE_TOKEN_KEY, token)
  } catch {
    // A tab that cannot keep it will ask for the link again, which is the
    // same thing that happens to somebody who opens the invitation twice.
  }
}

/** Drops it, once the invitation has been accepted or refused. */
export function forgetInviteToken(store: TokenStore | null): void {
  if (!store) return
  try {
    store.removeItem(INVITE_TOKEN_KEY)
  } catch {
    // Nothing to do: it expires with the tab in any case.
  }
}

/** The session storage of this tab, or null where there is no browser. */
export function tabStore(): TokenStore | null {
  if (typeof window === 'undefined') return null
  try {
    return window.sessionStorage
  } catch {
    return null
  }
}
