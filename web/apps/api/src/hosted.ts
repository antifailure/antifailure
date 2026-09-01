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

export function githubAppInstallUrlFrom(value: string | undefined | null): string | undefined {
  if (!value?.trim()) return undefined
  const url = new URL(value)
  if (
    url.protocol !== 'https:' ||
    url.hostname !== 'github.com' ||
    !/^\/apps\/[a-z0-9-]+\/installations\/new$/.test(url.pathname) ||
    url.search ||
    url.hash
  ) {
    throw new Error(
      'AF_GITHUB_APP_INSTALL_URL must be an https://github.com/apps/<slug>/installations/new address.',
    )
  }
  return url.toString()
}
