// A GitHub login turned into something the slug constraint accepts.
//
// One function in a file of its own because it now has two callers that must
// agree exactly, and they are on opposite sides of the application: the
// installation webhook, which creates an organization when somebody installs
// the App, and self serve signup, which creates one when somebody signs in
// having installed nothing. The whole reason a person's signup organization is
// ADOPTED by their later installation rather than duplicated beside it is that
// both derive the same slug from the same login. Two copies of this arithmetic
// would be two chances for that to stop being true, and the symptom would be a
// second empty organization appearing in somebody's switcher months later.
//
// It lived in github/webhook.ts first, and moving it out is also what stops
// auth/signin.ts and github/webhook.ts importing each other: the webhook
// already imports `grantMembership` from the sign-in path.

/** Thrown when a login contains nothing a slug may hold. Its own type so a
 *  caller can decide: the webhook refuses the delivery, and sign-in carries on
 *  with no organization rather than failing a login over a name. */
export class SlugError extends Error {}

/**
 * `organizations_slug_shape` is `^[a-z0-9][a-z0-9-]{0,62}$`. GitHub logins are
 * already close to that, but not identical: they may contain uppercase, and the
 * constraint would reject one, which would make an installation from an account
 * with a capital letter fail with a constraint violation rather than work.
 */
export function slugFor(login: string): string {
  const slug = login
    .toLowerCase()
    .replace(/[^a-z0-9-]/g, '-')
    .replace(/^-+/, '')
    .slice(0, 63)
  if (!slug) throw new SlugError(`"${login}" has no characters a slug may contain.`)
  return slug
}
