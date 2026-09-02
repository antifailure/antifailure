// The platform permission catalog.
//
// WHY THIS IS A SECOND FILE AND NOT AN ADDITION TO permissions.ts. That file is
// the TENANT catalog, and its test enforces two rules that together make it a
// trap for anything platform level:
//
//   every permission in the catalog guards at least one route, and
//   every permission that guards a route is granted to some tenant role.
//
// Both are good rules for a tenant catalog. Together they mean that adding
// `admin.users.read` there would force granting it to owner, admin, member or
// viewer, which is exactly backwards: a customer's own organization owner would
// hold a permission that reads every other customer's data. So platform
// permissions live here, with their own roles and their own procedure builder,
// and the two catalogs never mix.
//
// The same deny-by-default reasoning applies as in the tenant catalog, and the
// second rule there is worth repeating because it is the one that actually
// fails in practice: the way access control breaks is not a wrong grant, it is
// a new endpoint nobody remembered to guard. adminProcedure takes the
// permission as an argument for exactly that reason, so a route cannot exist
// without one.

export const ADMIN_PERMISSIONS = [
  // The portal itself. Holding this is what makes /admin load at all, and
  // every role has it, because a role that can sign in and then sees nothing
  // is indistinguishable from a broken deployment.
  'admin.portal.access',

  // The operator directory: who can reach this portal, and with what role.
  'admin.operators.read',
  'admin.operators.write',

  // The platform's own audit chain.
  'admin.audit.read',
  'admin.audit.export',

  // Tenants and the people in them. Read is broad on purpose: an operator
  // answering a support question needs to see the account, and splitting that
  // finer produces roles nobody can hold usefully.
  'admin.tenants.read',
  'admin.tenants.suspend',
  'admin.tenants.plan',

  'admin.users.read',
  'admin.users.write',
  'admin.sessions.read',
  'admin.sessions.revoke',
] as const

export type AdminPermission = (typeof ADMIN_PERMISSIONS)[number]

/**
 * Permissions the other portal lanes own.
 *
 * Declared here rather than in their own files so that the catalog is one list
 * and the matrix test can see all of it, and so that two lanes cannot pick the
 * same string. Each prefix belongs to exactly one agent, and adding a
 * permission under somebody else's prefix is the kind of thing a review misses
 * and this comment does not.
 */
export const RESERVED_PREFIXES: Record<string, string> = {
  'admin.portal': 'the foundation',
  'admin.operators': 'the foundation',
  'admin.audit': 'the foundation',
  'admin.tenants': 'the foundation',
  'admin.users': 'ops',
  'admin.sessions': 'ops',
  'admin.projects': 'ops',
  'admin.impersonation': 'ops',
  'admin.support': 'ops',
  'admin.search': 'ops',
  'admin.billing': 'money',
  'admin.entitlements': 'money',
  'admin.flags': 'money',
  'admin.infra': 'infra',
  'admin.deploys': 'infra',
  'admin.webhooks': 'infra',
  'admin.keys': 'infra',
  'admin.logs': 'infra',
  'admin.security': 'infra',
  'admin.emergency': 'infra',
}

export const ADMIN_ROLES = [
  'owner',
  'super_admin',
  'infrastructure',
  'security',
  'billing',
  'support',
  'analytics',
  'read_only',
] as const

export type AdminRole = (typeof ADMIN_ROLES)[number]

/**
 * What each permission means, in the words an auditor reads.
 *
 * The documentation is generated from this, so a permission cannot be added
 * without describing it. Same rule as the tenant catalog, same reason: three
 * lists would disagree and the documentation is the one that would be wrong.
 */
export const ADMIN_PERMISSION_DESCRIPTIONS: Record<AdminPermission, string> = {
  'admin.portal.access': 'Sign in to the operator portal and see its navigation.',
  'admin.operators.read': 'See who holds an operator account and what role they have.',
  'admin.operators.write': 'Create operators, change their role, and suspend them.',
  'admin.audit.read': 'Read the platform audit chain.',
  'admin.audit.export': 'Export the platform audit chain and verify its hashes.',
  'admin.tenants.read': 'See every organization, its plan, its usage and its members.',
  'admin.tenants.suspend':
    'Stop an organization creating new work, and let it start again. Running environments are untouched.',
  'admin.tenants.plan': "Change an organization's plan, which is what its quotas are derived from.",
  'admin.users.read': 'See any account on the platform and which organizations it belongs to.',
  'admin.users.write': 'Suspend and restore an account.',
  'admin.sessions.read': 'See who is signed in, on what, and since when.',
  'admin.sessions.revoke': 'Sign any account out of any session.',
}

/**
 * The built-in operator roles.
 *
 * Written out per role rather than as inheritance, for the reason the tenant
 * catalog gives: inheritance reads well and hides exactly the thing a reviewer
 * is checking, which is whether a low-privilege role can do something it should
 * not. This table can be read straight down a column.
 *
 * Three decisions worth defending:
 *
 * Only owner and super_admin can write operators. Granting an operator account
 * is granting cross-tenant read of the entire customer base, so it is the one
 * permission that is genuinely about who runs the company rather than who is
 * on call.
 *
 * read_only holds admin.audit.read. Oversight is not a privilege to be
 * rationed, and a role that can see what everyone did without being able to do
 * anything is the role an auditor should be given.
 *
 * Nobody except owner holds admin.audit.export. Reading is oversight; exporting
 * produces a file of every operator action that leaves the system. Same split
 * as the tenant catalog makes for the same reason.
 */
export const ADMIN_ROLE_PERMISSIONS: Record<AdminRole, readonly AdminPermission[]> = {
  owner: [...ADMIN_PERMISSIONS],
  super_admin: [
    'admin.portal.access',
    'admin.operators.read', 'admin.operators.write',
    'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.suspend', 'admin.tenants.plan',
    'admin.users.read', 'admin.users.write',
    'admin.sessions.read', 'admin.sessions.revoke',
  ],
  infrastructure: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.sessions.read',
  ],
  security: [
    'admin.portal.access', 'admin.audit.read', 'admin.audit.export',
    'admin.operators.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.users.write',
    'admin.sessions.read', 'admin.sessions.revoke',
  ],
  billing: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.plan',
    'admin.users.read',
  ],
  support: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read',
    'admin.users.read', 'admin.sessions.read',
  ],
  analytics: ['admin.portal.access', 'admin.audit.read', 'admin.tenants.read', 'admin.users.read'],
  read_only: ['admin.portal.access', 'admin.audit.read', 'admin.tenants.read', 'admin.users.read'],
}

export function adminRoleHas(role: AdminRole, permission: AdminPermission): boolean {
  return ADMIN_ROLE_PERMISSIONS[role].includes(permission)
}

/** Every role that holds a permission, for the documentation table. */
export function adminRolesWith(permission: AdminPermission): AdminRole[] {
  return ADMIN_ROLES.filter((r) => adminRoleHas(r, permission))
}

/**
 * Ordered most to least privileged, for DISPLAY only.
 *
 * Never used to decide access, for the reason the tenant catalog states:
 * ordering roles and comparing ranks is how a permission model stops being a
 * table and starts being an assumption, and the assumption breaks the first
 * time a role does not fit the line. `security` outranks `infrastructure` on
 * sessions and not on anything else, which is exactly the case a rank cannot
 * express.
 */
export const ADMIN_ROLE_ORDER: readonly AdminRole[] = [...ADMIN_ROLES]

/**
 * The root operator's role, which the database also enforces.
 *
 * A trigger in 0030 refuses to let the root row be demoted from owner, so this
 * constant and that trigger have to agree. They are checked against each other
 * by a test rather than by anybody remembering.
 */
export const ROOT_ADMIN_ROLE: AdminRole = 'owner'
