// The permission catalog.
//
// One list, read by three things: the middleware that enforces a permission on
// a request, the test that proves every route declares one, and the
// documentation that tells a customer what a role can do. Three lists would
// disagree, and the one that would be wrong is the documentation, which is the
// one a security review reads.
//
// Deny by default. A permission not granted to a role is refused, and a route
// with no declared permission does not run at all rather than running
// unguarded. That second rule is the one worth stating twice: the way access
// control fails in practice is not a wrong grant, it is a new endpoint that
// nobody remembered to guard, and a wrong grant is visible in a table while an
// unguarded endpoint is visible nowhere.

export const PERMISSIONS = [
  'environments.view',
  'environments.create',
  'environments.teardown',
  'masking.edit',
  'masking.approve',
  'network.edit',
  'network.approve',
  'agents.run',
  'load.run',
  'members.manage',
  'billing.manage',
  'audit.read',
  'audit.export',
  'runtimes.manage',
  'tokens.manage',
  'organization.settings',
  'organization.delete',
  'sessions.manage',
  'data.export',
  'account.close',
  'analytics.read',
] as const

export type Permission = (typeof PERMISSIONS)[number]

export const ROLES = ['owner', 'admin', 'member', 'viewer'] as const
export type Role = (typeof ROLES)[number]

/**
 * What each permission means, in the words a customer's security team reads.
 * The documentation page is generated from this, so a permission cannot be
 * added without describing it.
 */
export const PERMISSION_DESCRIPTIONS: Record<Permission, string> = {
  'environments.view': 'See environments, their state, and their preview URLs.',
  'environments.create': 'Bring an environment up for a branch or pull request.',
  'environments.teardown': 'Tear an environment down, or put it to sleep.',
  'masking.edit': 'Propose a change to a repository’s masking rules.',
  'masking.approve': 'Approve a masking rule change so that it can be opened as a pull request.',
  'network.edit': 'Propose a change to the egress policy.',
  'network.approve': 'Approve an egress policy change, including one that loosens it.',
  'agents.run': 'Start an agent run against an environment.',
  'load.run': 'Start a load run against an environment.',
  'members.manage': 'Invite, remove, and change the role of members.',
  'billing.manage': 'See and change the plan, payment method, and spending caps.',
  'audit.read': 'Read the audit log.',
  'audit.export': 'Export the audit log, and verify its hash chain.',
  'runtimes.manage': 'Register, tag, and remove runtimes.',
  'tokens.manage': 'Create and revoke the tokens engines use to send events.',
  'organization.settings': 'Change the organization’s display name.',
  'organization.delete':
    'Ask for the organization to be deleted, follow that request, and call it off.',
  'sessions.manage': 'See who is signed in and sign any of them out.',
  'data.export': 'Take a copy of the organization’s configuration and history out of the product.',
  'account.close':
    'Close your own account: erase your name, address and identity, and leave the organization.',
  'analytics.read':
    'Read the analytics dashboard for this control plane installation. Granted here and ' +
    'checked again against the organization that operates the installation, because this is ' +
    'the one page that is not about the caller’s own organization.',
}

/**
 * The built-in roles.
 *
 * Written out per role rather than as "admin gets everything member gets, plus
 * these". Inheritance reads well and hides exactly the thing a reviewer is
 * checking, which is whether viewer can do something it should not. The table
 * below can be read straight down a column.
 *
 * Two decisions worth defending:
 *
 * A member can propose a policy change and cannot approve one. Masking rules
 * and egress rules are the two settings where a mistake is a data incident
 * rather than an inconvenience, so proposing and approving are separate
 * permissions even in the community edition, where the same person usually
 * holds both.
 *
 * A viewer can read the audit log but not export it. Reading is oversight;
 * exporting produces a file of who did what that leaves the system.
 *
 * An admin can change settings, sign people out and export, and cannot delete
 * the organization or touch billing. Those two are the actions with a
 * consequence outside this product, one on somebody's card and one on data that
 * does not come back, and they belong to whoever owns the relationship rather
 * than to whoever administers the day to day.
 *
 * Every role holds account.delete, including viewer, and that is not an
 * oversight in a deny-by-default table. It is the one permission that is about
 * the holder rather than about the organization: a person may always leave and
 * close their own account, and a role that could be trapped in an organization
 * it cannot leave would be a worse answer than a wide grant. The route refuses
 * the only case where leaving is destructive, which is the last owner, and says
 * what to do about it.
 *
 * Owner and admin hold analytics.read and member and viewer do not, and it is
 * the only permission on this list where the grant is not the whole gate. The
 * dashboard covers the installation rather than the organization, so it is
 * ALSO confined to members of the organization that operates the installation,
 * which a role table cannot express. See routers/analytics.ts.
 */
export const ROLE_PERMISSIONS: Record<Role, readonly Permission[]> = {
  owner: [...PERMISSIONS],
  admin: [
    'environments.view', 'environments.create', 'environments.teardown',
    'masking.edit', 'masking.approve', 'network.edit', 'network.approve',
    'agents.run', 'load.run', 'members.manage',
    'audit.read', 'audit.export', 'runtimes.manage', 'tokens.manage',
    'organization.settings', 'sessions.manage', 'data.export', 'account.close',
    'analytics.read',
  ],
  member: [
    'environments.view', 'environments.create', 'environments.teardown',
    'masking.edit', 'network.edit', 'agents.run', 'load.run',
    'audit.read', 'account.close',
  ],
  viewer: ['environments.view', 'audit.read', 'account.close'],
}

export function roleHas(role: Role, permission: Permission): boolean {
  return ROLE_PERMISSIONS[role].includes(permission)
}

/** Every role that holds a permission, for the documentation table. */
export function rolesWith(permission: Permission): Role[] {
  return ROLES.filter((r) => roleHas(r, permission))
}

/**
 * Roles ordered from most to least privileged, for display only.
 *
 * Never used to decide access. Ordering roles and comparing ranks is how a
 * permission model stops being a table and starts being an assumption, and the
 * assumption breaks the first time a custom role does not fit the line.
 */
export const ROLE_ORDER: readonly Role[] = ['owner', 'admin', 'member', 'viewer']

// ---------------------------------------------------------------------------
// The extension point
// ---------------------------------------------------------------------------

/**
 * What a request is asking to do, for a resolver that decides more finely than
 * a built-in role can.
 *
 * The scope is here because a permission is rarely global in a large
 * organization: somebody administers two repositories and reads the rest. The
 * community edition ignores it, which is correct for the built-in roles, and
 * the shape has to exist here or the enterprise resolver would need a different
 * call site and the two would drift.
 */
export interface PermissionRequest {
  orgId: string
  userId: string
  role: Role
  permission: Permission
  /** The repository the request concerns, when it concerns one. */
  repository?: string | null
  /** The environment, when it concerns one. */
  envId?: string | null
}

/**
 * Decides whether a request is permitted.
 *
 * Returning undefined means "no opinion", and the built-in role table decides.
 * That is what lets a resolver widen nothing by accident: a resolver that only
 * knows about two repositories returns undefined for everything else rather
 * than having to reproduce the whole table correctly.
 */
export type PermissionResolver = (req: PermissionRequest) => boolean | undefined

let resolver: PermissionResolver | null = null

/**
 * Installs a resolver. The community edition installs none.
 *
 * One at a time rather than a list. Two resolvers would need a rule for
 * combining their answers, and every such rule is either "any may grant", which
 * lets a narrow resolver widen access by accident, or "all must agree", which
 * makes adding one break the others.
 */
export function setPermissionResolver(next: PermissionResolver | null): void {
  resolver = next
}

export function hasPermissionResolver(): boolean {
  return resolver !== null
}

/**
 * The decision for one request.
 *
 * Deny by default, and the resolver can only be asked after the built-in table
 * has had its say, so a resolver that throws or returns nonsense degrades to
 * the community behaviour rather than to permitting everything.
 */
export function permits(req: PermissionRequest): boolean {
  const builtin = roleHas(req.role, req.permission)
  if (!resolver) return builtin

  let answer: boolean | undefined
  try {
    answer = resolver(req)
  } catch {
    // A resolver that fails must not open anything up. Falling back to the
    // built-in table is the conservative direction, and the failure surfaces
    // through the resolver's own reporting rather than by granting access.
    return builtin
  }
  return answer === undefined ? builtin : answer
}
