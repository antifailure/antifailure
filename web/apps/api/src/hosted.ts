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
