// The commercial boundary of a hosted control plane.
//
// Self-hosted installations leave this unset and keep the whole community
// control plane available. Antifailure's hosted service sets it to enterprise,
// which leaves authentication and billing reachable while every operational
// capability is refused until Stripe grants that plan.

export type HostedRequiredPlan = 'enterprise'

export const HOSTED_ACCESS_MESSAGE =
  'This hosted control plane requires the enterprise plan. Open Plan to subscribe or manage billing.'

export function hostedRequiredPlanFrom(value: string | undefined | null): HostedRequiredPlan | null {
  const plan = value?.trim().toLowerCase()
  if (!plan) return null
  if (plan === 'enterprise') return plan
  throw new Error(
    `AF_HOSTED_REQUIRED_PLAN must be enterprise or unset; received ${JSON.stringify(value)}.`,
  )
}

/**
 * Whether whoever runs this installation also decides each organization's plan.
 *
 * THE DEFAULT IS OFF, AND THAT IS THE WHOLE POINT. `billing.set` writes
 * `organizations.plan`, which is the column every quota is read from, and the
 * caller who reaches it is an org owner rather than the operator. On a plane
 * one person runs for themselves those are the same person and the route is an
 * administrative convenience, because that person can already write the column
 * with psql. On a plane that serves anybody else they are not the same person,
 * and the route is a signed-in stranger granting themselves the largest plan.
 *
 * Read that failure the other way round, because it decides the shape of this
 * flag. The dangerous configuration is the one where nothing is configured:
 * no Stripe, no plan gate, an operator who has not thought about billing yet.
 * A flag meaning "this plane is hosted" would have to be REMEMBERED by exactly
 * the operator who has not thought about it, so forgetting it would leave the
 * hole open. This flag means the opposite, so forgetting it closes the hole and
 * costs a self-hosted operator one variable they set once.
 *
 * It may not be combined with billing. See main.ts: a plan that can be granted
 * by hand is not a plan anybody has to buy, and the process refuses to start
 * rather than serving that contradiction.
 */
export function operatorSetsPlanFrom(value: string | undefined | null): boolean {
  const raw = value?.trim().toLowerCase()
  if (!raw || raw === '0' || raw === 'false') return false
  if (raw === '1' || raw === 'true') return true
  throw new Error(
    `AF_OPERATOR_SETS_PLAN must be 1, 0 or unset; received ${JSON.stringify(value)}.`,
  )
}

/**
 * Why the plan cannot be written by hand, said to the person who asked.
 *
 * Two different readers and two different next steps. A customer on a plane
 * that takes money is told where the plan comes from. An operator on a plane
 * that takes none is told the variable that turns the route on, because they
 * are the only person who could set it and the alternative is reading the
 * source to find out the route exists at all.
 */
export function planSetRefusal(takesPayment: boolean): string {
  return takesPayment
    ? 'This installation derives paid plans from Stripe. Use checkout or the billing portal; ' +
        'the plan cannot be set directly.'
    : 'This control plane does not grant plans by hand. Set AF_OPERATOR_SETS_PLAN=1 if you run ' +
        'it yourself and decide the plan yourself; a plane that serves anybody else should take ' +
        'payment instead.'
}

export function hasHostedAccess(
  plan: string | null | undefined,
  required: HostedRequiredPlan | null,
): boolean {
  return required === null || plan === required
}

/**
 * The permissions a lapsed plan may NOT refuse.
 *
 * THE LINE, and it is the line rather than the list that the next person adding
 * a permission has to classify against:
 *
 *   A permission is exempt when refusing it TRAPS somebody in the product, and
 *   gated when refusing it merely stops them using something they have not paid
 *   for. Taking your data out, removing the tenant, leaving, and containing a
 *   security incident are all exits. Reading a dashboard is not.
 *
 * This is a LEGAL EXPOSURE and not a courtesy. A hosted service that conditions
 * data export and account deletion on payment is a serious problem in every
 * jurisdiction with a data-portability or erasure right, which is most of them.
 * Nobody should shorten this set to tidy it.
 *
 * Why each one is here:
 *
 *   billing.manage       the path that RESOLVES the refusal. Gate it and the
 *                        refusal has no exit at all.
 *   data.export          taking your own data out.
 *   organization.delete  removing the tenant.
 *   account.close        a person leaving.
 *   sessions.manage      NOT arguable. Revoking a session is a SECURITY action.
 *                        If a credential leaks while a plan has lapsed, refusing
 *                        revocation leaves somebody unable to contain the
 *                        incident, and the only remaining remedy is deleting the
 *                        organization outright. A nuclear option is not a remedy.
 *
 * organization.settings and environments.view stay GATED. They are the product
 * doing work.
 *
 * One trap this set does not close, recorded so it is not rediscovered the hard
 * way: the exemption is by exact permission STRING. Adding a `billing.view`
 * later without adding it here blanks the Plan page for exactly the people who
 * have no other way to fix it.
 */
export const HOSTED_GATE_EXEMPT: ReadonlySet<string> = new Set([
  'billing.manage',
  'data.export',
  'organization.delete',
  'account.close',
  'sessions.manage',
])

/**
 * A permission being exempt is NOT the same as the exit being reachable, and
 * this is the sentence that stops the next person stopping one step early.
 *
 * Exempting these five left every one of them behind a page that could not
 * load, because the console's settings screen reads `org.settings`, which is
 * `environments.view` and stays gated. `account.context`, under `account.close`
 * because that is the one permission every role holds, is the read that makes
 * the exits reachable rather than merely permitted. If a future exit needs a
 * read of its own, it belongs there rather than in a gated route.
 */

/**
 * Where a person with no organization is sent to install the GitHub App.
 *
 * UNSET IS A SUPPORTED STATE and not a half configuration, which is why this
 * returns undefined rather than throwing on an empty value. Membership follows
 * a GitHub App installation on the hosted plane, but a self-hosted plane may
 * provision membership some other way and has no App of its own to point at.
 * Refusing to start would break that installation for a screen it never shows.
 *
 * What unset must NOT do is leave the console telling somebody to install an
 * App while offering no way to do it. See `NoOrganization` in
 * `console/components/Shell.tsx`: the copy changes with this value, and the
 * membership recheck is offered either way because it never depended on it.
 *
 * A malformed value IS refused, at start-up, because the alternative is a link
 * pointing wherever an operator's typo pointed. The parse is guarded rather
 * than left to throw: `new URL` on a string with no scheme raises `Invalid
 * URL`, which names neither the variable nor the shape it wanted, and an
 * operator reading that in a crash loop has to find this file to learn what
 * was wrong.
 */
export function githubAppInstallUrlFrom(value: string | undefined | null): string | undefined {
  if (!value?.trim()) return undefined
  let url: URL
  try {
    url = new URL(value)
  } catch {
    throw new Error(INSTALL_URL_SHAPE)
  }
  if (
    url.protocol !== 'https:' ||
    url.hostname !== 'github.com' ||
    !/^\/apps\/[a-z0-9-]+\/installations\/new$/.test(url.pathname) ||
    url.search ||
    url.hash
  ) {
    throw new Error(INSTALL_URL_SHAPE)
  }
  return url.toString()
}

/** One sentence, so a bad scheme and a bad shape cannot drift apart. */
const INSTALL_URL_SHAPE =
  'AF_GITHUB_APP_INSTALL_URL must be an https://github.com/apps/<slug>/installations/new address.'
