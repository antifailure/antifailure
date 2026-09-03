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

  // Money, and the three things that decide what a customer gets.
  //
  // Read is separate from write on all three, and on billing that split is the
  // one that matters: an on-call engineer answering "why was this customer
  // charged twice" needs the invoices and the charges, and does not need the
  // refund button. Merging them would make everybody who can answer a question
  // able to move money, which is how a support rota becomes a financial risk.
  'admin.billing.read',
  'admin.billing.write',
  'admin.entitlements.read',
  'admin.entitlements.write',
  'admin.flags.read',
  'admin.flags.write',

  // Infrastructure. One read covers system health, the fleet of twins, the
  // teardown ledger and the egress firewall, because they are one question
  // asked from four angles and an operator who can see one and not the next
  // cannot answer it.
  'admin.infra.read',
  // Asking for twins to be torn down. Separate from read because it writes,
  // and separate from the emergency switches because it is a routine action
  // during an incident rather than one that stops the installation.
  'admin.infra.teardown',

  // The emergency switches. Read is separate from engage on purpose: every
  // operator should be able to SEE that the installation is paused, and almost
  // none of them should be able to pause it.
  'admin.emergency.read',
  'admin.emergency.engage',

  // Failures across every tenant, and the event stream by shape. Read only,
  // and there is deliberately no write half: nothing on that page changes
  // anything, so a write permission would guard no route.
  'admin.logs.read',
  // Whether this installation can send email, and what it has tried to send.
  // Separate from admin.logs.read because it reaches addresses, which is
  // personal data, and because the roles that need one rarely need the other.
  'admin.email.read',
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
  // Same lane as admin.logs. Claimed here rather than left unlisted, because
  // an unlisted prefix is one two agents can both reach for and neither will
  // see the other take.
  'admin.email': 'infra',
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
  'admin.infra.read':
    'See system health, every environment running on this installation, the teardown ledger, ' +
    'and the egress rules across every organization.',
  'admin.infra.teardown':
    'Ask for environments to be torn down. This records a request; the runtime confirms it.',
  'admin.emergency.read':
    'See whether maintenance mode, new sign-ups, or new runs are paused, and why.',
  'admin.emergency.engage':
    'Pause or resume the whole installation: maintenance mode, new sign-ups, and new runs.',
  'admin.logs.read':
    'See failing runs grouped by failure code across every organization, which workflows are ' +
    'failing, and the event stream by type, timing and shape. Event payloads are never returned.',
  'admin.email.read':
    'See whether this installation can send email at all, and every sign-in link it has issued ' +
    'recently with the address it was issued to and whether it was used.',
  'admin.billing.read':
    'See a customer\'s Stripe customer, subscription, invoices, charges, payment methods and ' +
    'credit balance, and the record of every administrative money action taken on the account.',
  'admin.billing.write':
    'Move money: issue a refund, add credit, change or cancel a plan, extend a trial, apply a ' +
    'discount, retry a payment, and resend an invoice.',
  'admin.entitlements.read':
    'See what an organization is entitled to, and which of its limits were granted rather than ' +
    'set by its plan.',
  'admin.entitlements.write':
    'Grant an organization, project or user a limit other than its plan\'s, and revoke one.',
  'admin.flags.read': 'See feature flags, their rollout and who they are targeted at.',
  'admin.flags.write':
    'Turn a feature flag on or off for everybody, target it, roll it out, and kill it during an ' +
    'incident.',
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
 * owner and security hold admin.audit.export; nobody else does. Reading is
 * oversight and every role has it; exporting produces a file of every operator
 * action that leaves the system, so it is held by the two roles whose job is
 * answering for what happened.
 *
 * security is not an exception grudgingly made, it is the point: a security
 * team that can read an incident's audit trail and cannot produce it for an
 * investigation or for counsel is not much use, and they are the role most
 * likely to need it at the worst moment.
 *
 * An earlier version of this comment said "nobody except owner", which
 * contradicted the table three lines below it AND misdescribed the tenant
 * catalog it claimed to mirror: there, audit.export is held by owner and
 * admin, and what the split actually withholds is a VIEWER exporting. Found by
 * admin-money reading the comment against the grant.
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
    'admin.infra.read', 'admin.infra.teardown',
    // The only role besides owner that may stop the installation. Gated on the
    // permission rather than on rank: ordering roles and comparing ranks is how
    // a permission model stops being a table and starts being an assumption.
    'admin.emergency.read', 'admin.emergency.engage',
    'admin.logs.read', 'admin.email.read',
  ],
  infrastructure: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.sessions.read',
    // Flags, both halves. A kill switch is an incident tool before it is a
    // product one, and the people holding the pager have to be able to reach
    // it without finding somebody from billing at three in the morning.
    'admin.flags.read', 'admin.flags.write',
    'admin.infra.read', 'admin.infra.teardown',
    // Sees the switches, cannot throw them. An infrastructure operator
    // debugging "their runs will not start" must be able to discover that runs
    // are frozen; pausing the installation is a different decision.
    'admin.emergency.read',
    // The failure explorer and the mail surface. "Their runs will not start"
    // and "the sign-in link never arrived" are both infrastructure questions
    // before they are anybody else's, and neither is answerable without these.
    'admin.logs.read', 'admin.email.read',
  ],
  security: [
    'admin.portal.access', 'admin.audit.read', 'admin.audit.export',
    'admin.operators.read',
    'admin.tenants.read', 'admin.tenants.suspend',
    'admin.users.read', 'admin.users.write',
    'admin.sessions.read', 'admin.sessions.revoke',
    // Read on all three, and write on flags. Security investigates money and
    // entitlements rather than changing them, and containing an incident by
    // killing a feature is the one write it needs at speed.
    'admin.billing.read', 'admin.entitlements.read',
    'admin.flags.read', 'admin.flags.write',
    'admin.infra.read', 'admin.emergency.read',
    // Sign-in links carry an address, an IP and a user agent, and an account
    // takeover investigation starts by reading exactly those three.
    'admin.logs.read', 'admin.email.read',
  ],
  billing: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read', 'admin.tenants.plan',
    'admin.users.read',
    // The role the money permissions exist for, and the only one below owner
    // that holds the write half of all three.
    'admin.billing.read', 'admin.billing.write',
    'admin.entitlements.read', 'admin.entitlements.write',
    'admin.flags.read', 'admin.flags.write',
  ],
  support: [
    'admin.portal.access', 'admin.audit.read',
    'admin.tenants.read',
    'admin.users.read', 'admin.sessions.read',
    // Support answers "why was I charged this" every day and must never be the
    // rota that can refund. Read without write is the whole point of the split.
    'admin.billing.read', 'admin.entitlements.read', 'admin.flags.read',
    // The two questions support is actually asked: why did this run fail, and
    // why has this person not received their sign-in link. Both are reads.
    'admin.logs.read', 'admin.email.read',
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
